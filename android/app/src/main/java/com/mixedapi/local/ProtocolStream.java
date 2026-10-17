package com.mixedapi.local;

import static com.mixedapi.local.GoogleProtocol.arr;
import static com.mixedapi.local.GoogleProtocol.obj;

import java.io.IOException;
import java.io.OutputStream;
import org.json.JSONArray;
import org.json.JSONObject;

/** Incremental client events; tool identities are buffered because Chat permits name fragments. */
final class ProtocolStream {
  private final boolean messages;
  private final NativeRelay wire;
  private final JSONObject response;
  private final JSONArray items = new JSONArray();
  private final java.util.Map<String, Integer> texts = new java.util.HashMap<>();
  private final java.util.Map<Integer, JSONObject> tools = new java.util.TreeMap<>();
  private JSONObject usage;
  private String finish = "";
  private boolean started, failed;
  boolean done;
  private int sequence;
  private long size;

  ProtocolStream(OutputStream out, String backend, String model, boolean messages)
      throws Exception {
    this.messages = messages;
    response = ProtocolResult.response(model);
    wire = new NativeRelay(out, backend, model, messages ? "messages" : "responses", true);
  }

  private void send(String type, JSONObject data) throws Exception {
    data.put("type", type);
    if (!messages) data.put("sequence_number", sequence++);
    wire.event(type, data);
  }

  private void start() throws Exception {
    if (started) return;
    started = true;
    if (messages)
      send(
          "message_start",
          obj(
              "message",
              obj(
                  "id",
                  response.getString("id"),
                  "type",
                  "message",
                  "role",
                  "assistant",
                  "model",
                  response.getString("model"),
                  "content",
                  new JSONArray(),
                  "stop_reason",
                  JSONObject.NULL,
                  "stop_sequence",
                  JSONObject.NULL,
                  "usage",
                  ProtocolResult.usage(null, true))));
    else {
      send("response.created", obj("response", response));
      send("response.in_progress", obj("response", response));
    }
  }

  void consume(String data) throws Exception {
    if (done || failed) throw new IOException("Data after completion");
    size += data.length();
    if (size > 16 * 1024 * 1024) throw new IOException("Response too large");
    if (data.equals("[DONE]")) {
      complete();
      return;
    }
    JSONObject packet = new JSONObject(data);
    if (!packet.isNull("error") || !packet.isNull("logprobs"))
      throw new IOException("Invalid upstream stream");
    if (packet.optJSONObject("usage") != null) usage = packet.getJSONObject("usage");
    JSONArray choices = packet.getJSONArray("choices");
    if (choices.length() > 1) throw new IOException("Multiple candidates unavailable");
    for (int i = 0; i < choices.length(); i++) {
      JSONObject choice = choices.getJSONObject(i);
      if (choice.optInt("index", 0) != 0 || !finish.isEmpty())
        throw new IOException("Unexpected candidate");
      JSONObject delta = choice.getJSONObject("delta");
      ProtocolRequest.only(
          delta,
          "role",
          "content",
          "reasoning_content",
          "refusal",
          "tool_calls",
          "reasoning_details",
          "annotations");
      for (String key : new String[] {"content", "reasoning_content", "refusal"}) {
        if (!delta.isNull(key)) {
          if (!(delta.get(key) instanceof String))
            throw new IOException("Unsupported output content");
          text(key, delta.getString(key));
        }
      }
      if (delta.has("reasoning_details")) reasoning(delta.getJSONArray("reasoning_details"));
      if (delta.has("annotations")) annotations(delta.getJSONArray("annotations"));
      if (delta.has("tool_calls")) {
        JSONArray calls = delta.getJSONArray("tool_calls");
        for (int n = 0; n < calls.length(); n++) {
          JSONObject call = calls.getJSONObject(n), f = call.getJSONObject("function");
          int index = call.getInt("index");
          if (index < 0 || index > 1024) throw new IOException("Invalid tool index");
          JSONObject tool = tools.get(index);
          if (tool == null) {
            tool =
                obj(
                    "id",
                    ProtocolResult.id("fc_"),
                    "type",
                    "function_call",
                    "call_id",
                    "",
                    "name",
                    "",
                    "arguments",
                    "",
                    "status",
                    "in_progress");
            tools.put(index, tool);
          }
          if (call.has("id")) tool.put("call_id", tool.getString("call_id") + call.getString("id"));
          for (String key : new String[] {"name", "arguments"})
            if (f.has(key)) tool.put(key, tool.getString(key) + f.getString(key));
          ProtocolRequest.signature(tool, call);
        }
      }
      if (!choice.isNull("finish_reason")) finish = choice.getString("finish_reason");
    }
  }

  private void text(String key, String value) throws Exception {
    if (value.isEmpty()) return;
    start();
    boolean thought = key.equals("reasoning_content"), refusal = key.equals("refusal");
    Integer index = texts.get(key);
    if (index == null) {
      index = items.length();
      texts.put(key, index);
      JSONObject part =
          thought
              ? obj("type", "summary_text", "text", "")
              : refusal
                  ? obj("type", "refusal", "refusal", "")
                  : obj("type", "output_text", "text", "", "annotations", new JSONArray());
      JSONObject item =
          thought
              ? obj("id", ProtocolResult.id("rs_"), "type", "reasoning", "summary", arr(part))
              : obj(
                  "id",
                  ProtocolResult.id("msg_"),
                  "type",
                  "message",
                  "role",
                  "assistant",
                  "status",
                  "in_progress",
                  "content",
                  arr(part));
      items.put(item);
      if (messages)
        send(
            "content_block_start",
            obj(
                "index",
                index,
                "content_block",
                thought
                    ? obj("type", "thinking", "thinking", "", "signature", "")
                    : obj("type", "text", "text", "")));
      else {
        send("response.output_item.added", obj("output_index", index, "item", item));
        send(
            thought ? "response.reasoning_summary_part.added" : "response.content_part.added",
            obj(
                "item_id",
                item.getString("id"),
                "output_index",
                index,
                thought ? "summary_index" : "content_index",
                0,
                "part",
                part));
      }
    }
    JSONObject item = items.getJSONObject(index),
        part = item.getJSONArray(thought ? "summary" : "content").getJSONObject(0);
    String field = refusal ? "refusal" : "text";
    part.put(field, part.getString(field) + value);
    if (messages)
      send(
          "content_block_delta",
          obj(
              "index",
              index,
              "delta",
              thought
                  ? obj("type", "thinking_delta", "thinking", value)
                  : obj("type", "text_delta", "text", value)));
    else
      send(
          thought
              ? "response.reasoning_summary_text.delta"
              : refusal ? "response.refusal.delta" : "response.output_text.delta",
          obj(
              "item_id",
              item.getString("id"),
              "output_index",
              index,
              thought ? "summary_index" : "content_index",
              0,
              "delta",
              value));
  }

  private void reasoning(JSONArray details) throws Exception {
    for (int i = 0; i < details.length(); i++) {
      JSONObject detail = details.getJSONObject(i);
      if (messages && !detail.optString("format").equals("anthropic-claude-v1"))
        throw new IOException("Unsupported signed reasoning");
      if (detail.optString("type").equals("reasoning.text")) {
        Integer index = texts.get("reasoning_content");
        if (index == null) {
          text("reasoning_content", detail.getString("text"));
          index = texts.get("reasoning_content");
        }
        if (index == null) throw new IOException("Empty signed reasoning");
        JSONObject item = items.getJSONObject(index);
        if (!item.getJSONArray("summary")
            .getJSONObject(0)
            .getString("text")
            .equals(detail.getString("text"))) throw new IOException("Inconsistent thinking text");
        JSONArray saved = item.optJSONArray("reasoning_details");
        if (saved == null) saved = new JSONArray();
        item.put("reasoning_details", saved.put(detail));
        if (messages)
          send(
              "content_block_delta",
              obj(
                  "index",
                  index,
                  "delta",
                  obj("type", "signature_delta", "signature", detail.getString("signature"))));
      } else if (detail.optString("type").equals("reasoning.encrypted")) {
        start();
        int index = items.length();
        JSONObject item =
            obj(
                "id",
                ProtocolResult.id("rs_"),
                "type",
                "reasoning",
                "summary",
                new JSONArray(),
                "reasoning_details",
                arr(detail));
        items.put(item);
        if (messages)
          send(
              "content_block_start",
              obj(
                  "index",
                  index,
                  "content_block",
                  obj("type", "redacted_thinking", "data", detail.getString("data"))));
        else send("response.output_item.added", obj("output_index", index, "item", item));
      } else throw new IOException("Unsupported reasoning type");
    }
  }

  private void annotations(JSONArray annotations) throws Exception {
    if (annotations.length() == 0) return;
    if (messages) throw new IOException("Unsupported citation representation");
    Integer index = texts.get("content");
    if (index == null) throw new IOException("Citation without text");
    JSONObject item = items.getJSONObject(index);
    JSONArray saved = item.getJSONArray("content").getJSONObject(0).getJSONArray("annotations");
    for (int i = 0; i < annotations.length(); i++) {
      JSONObject a = annotations.getJSONObject(i);
      if (a.optJSONObject("url_citation") != null)
        a = new JSONObject(a.getJSONObject("url_citation").toString()).put("type", "url_citation");
      send(
          "response.output_text.annotation.added",
          obj(
              "item_id",
              item.getString("id"),
              "output_index",
              index,
              "content_index",
              0,
              "annotation_index",
              saved.length(),
              "annotation",
              a));
      saved.put(a);
    }
  }

  private void complete() throws Exception {
    String status = ProtocolResult.status(finish);
    for (JSONObject tool : tools.values()) {
      ProtocolRequest.required(tool, "call_id");
      ProtocolRequest.required(tool, "name");
      if (status.equals("completed")) ProtocolRequest.objectArguments(tool.getString("arguments"));
    }
    start();
    for (JSONObject tool : tools.values()) {
      String args = tool.getString("arguments");
      int index = items.length();
      tool.put("arguments", "");
      items.put(tool);
      if (messages) {
        JSONObject block =
            obj(
                "type",
                "tool_use",
                "id",
                tool.getString("call_id"),
                "name",
                tool.getString("name"),
                "input",
                new JSONObject());
        ProtocolRequest.signature(block, tool);
        send("content_block_start", obj("index", index, "content_block", block));
        send(
            "content_block_delta",
            obj("index", index, "delta", obj("type", "input_json_delta", "partial_json", args)));
      } else {
        send("response.output_item.added", obj("output_index", index, "item", tool));
        send(
            "response.function_call_arguments.delta",
            obj("item_id", tool.getString("id"), "output_index", index, "delta", args));
      }
      tool.put("arguments", args);
    }
    for (int i = 0; i < items.length(); i++) {
      JSONObject item = items.getJSONObject(i);
      if (messages) {
        send("content_block_stop", obj("index", i));
        continue;
      }
      String type = item.getString("type");
      if (type.equals("function_call")) {
        item.put("status", status);
        send(
            "response.function_call_arguments.done",
            obj(
                "item_id",
                item.getString("id"),
                "output_index",
                i,
                "arguments",
                item.getString("arguments")));
      } else {
        boolean thought = type.equals("reasoning");
        JSONArray parts = item.getJSONArray(thought ? "summary" : "content");
        if (!thought) item.put("status", status);
        for (int n = 0; n < parts.length(); n++) {
          JSONObject part = parts.getJSONObject(n);
          boolean refusal = part.getString("type").equals("refusal");
          send(
              thought
                  ? "response.reasoning_summary_text.done"
                  : refusal ? "response.refusal.done" : "response.output_text.done",
              obj(
                  "item_id",
                  item.getString("id"),
                  "output_index",
                  i,
                  thought ? "summary_index" : "content_index",
                  n,
                  refusal ? "refusal" : "text",
                  part.getString(refusal ? "refusal" : "text")));
          send(
              thought ? "response.reasoning_summary_part.done" : "response.content_part.done",
              obj(
                  "item_id",
                  item.getString("id"),
                  "output_index",
                  i,
                  thought ? "summary_index" : "content_index",
                  n,
                  "part",
                  part));
        }
      }
      send("response.output_item.done", obj("output_index", i, "item", item));
    }
    if (messages) {
      send(
          "message_delta",
          obj(
              "delta",
              obj(
                  "stop_reason",
                  texts.containsKey("refusal") ? "refusal" : ProtocolResult.stop(finish),
                  "stop_sequence",
                  JSONObject.NULL),
              "usage",
              ProtocolResult.usage(usage, true)));
      send("message_stop", new JSONObject());
    } else {
      response
          .put("status", status)
          .put("output", items)
          .put("usage", ProtocolResult.usage(usage, false));
      if (status.equals("incomplete"))
        response.put(
            "incomplete_details",
            obj("reason", finish.equals("length") ? "max_output_tokens" : "content_filter"));
      send("response." + status, obj("response", response));
    }
    done = true;
  }

  void error() throws Exception {
    if (done || failed || Thread.currentThread().isInterrupted()) return;
    failed = true;
    if (!messages && started) {
      response
          .put("status", "failed")
          .put("error", obj("code", "provider_stream_error", "message", "来源流响应未正常完成"));
      send("response.failed", obj("response", response));
    } else wire.error();
  }
}
