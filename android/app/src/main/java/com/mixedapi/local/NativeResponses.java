package com.mixedapi.local;

import java.io.IOException;
import java.io.InputStream;
import java.util.HashMap;
import java.util.Map;
import org.json.JSONArray;
import org.json.JSONObject;

/** Responses protocol encoding shared by OpenCode and Codex. */
final class NativeResponses {
  static void read(InputStream body, NativeChat chat) throws Exception {
    Map<Integer, Integer> tools = new HashMap<>();
    NativeWire.events(
        body,
        data -> {
          if (data.equals("[DONE]")) return;
          JSONObject event = new JSONObject(data);
          String type = event.getString("type");
          JSONObject delta = new JSONObject();
          Object finish = JSONObject.NULL;
          JSONObject usage = null;
          switch (type) {
            case "error":
            case "response.failed":
              throw new IOException("Provider generation failed");
            case "response.output_text.delta":
              delta.put("content", event.getString("delta"));
              break;
            case "response.reasoning_summary_text.delta":
              delta.put("reasoning_content", event.getString("delta"));
              break;
            case "response.refusal.delta":
              delta.put("refusal", event.getString("delta"));
              break;
            case "response.output_item.added":
              {
                JSONObject item = event.getJSONObject("item");
                if (!item.optString("type").equals("function_call")) return;
                int index = event.getInt("output_index");
                if (tools.containsKey(index)) throw new IOException("Duplicate tool identity");
                int toolIndex = tools.size();
                tools.put(index, toolIndex);
                delta.put(
                    "tool_calls",
                    GoogleProtocol.arr(
                        GoogleProtocol.obj(
                            "index",
                            toolIndex,
                            "id",
                            item.getString("call_id"),
                            "type",
                            "function",
                            "function",
                            GoogleProtocol.obj(
                                "name",
                                item.getString("name"),
                                "arguments",
                                item.optString("arguments")))));
                break;
              }
            case "response.function_call_arguments.delta":
              {
                Integer index = tools.get(event.getInt("output_index"));
                if (index == null) throw new IOException("Unknown tool delta");
                delta.put(
                    "tool_calls",
                    GoogleProtocol.arr(
                        GoogleProtocol.obj(
                            "index",
                            index,
                            "function",
                            GoogleProtocol.obj("arguments", event.getString("delta")))));
                break;
              }
            case "response.completed":
            case "response.incomplete":
              {
                JSONObject response = event.getJSONObject("response");
                finish = tools.isEmpty() ? "stop" : "tool_calls";
                if (type.equals("response.incomplete")) {
                  JSONObject detail = response.optJSONObject("incomplete_details");
                  finish =
                      detail != null && detail.optString("reason").equals("content_filter")
                          ? "content_filter"
                          : "length";
                }
                if (response.optJSONObject("usage") != null)
                  usage = usage(response.getJSONObject("usage"));
                break;
              }
            default:
              return;
          }
          JSONObject chunk =
              GoogleProtocol.obj(
                  "choices",
                  GoogleProtocol.arr(
                      GoogleProtocol.obj("index", 0, "delta", delta, "finish_reason", finish)));
          if (usage != null) chunk.put("usage", usage);
          chat.chunk(chunk);
          if (finish != JSONObject.NULL) {
            chat.complete();
            throw new NativeWire.End();
          }
        });
    if (!chat.done) throw new IOException("Incomplete Responses stream");
  }

  static JSONObject request(JSONObject chat) throws Exception {
    JSONObject out = new JSONObject();
    for (String key :
        new String[] {
          "model", "stream", "temperature", "top_p", "parallel_tool_calls", "store", "metadata"
        }) if (chat.has(key)) out.put(key, chat.get(key));
    if (chat.has("max_completion_tokens"))
      out.put("max_output_tokens", chat.get("max_completion_tokens"));
    else if (chat.has("max_tokens")) out.put("max_output_tokens", chat.get("max_tokens"));
    if (chat.has("reasoning_effort") && !chat.getString("reasoning_effort").equals("none"))
      out.put(
          "reasoning",
          GoogleProtocol.obj("effort", chat.get("reasoning_effort"), "summary", "auto"));
    if (chat.optJSONObject("reasoning") != null) out.put("reasoning", chat.get("reasoning"));
    if (chat.has("n") && chat.getInt("n") != 1)
      throw new IOException("Responses supports one candidate");
    JSONArray messages = chat.getJSONArray("messages"), input = new JSONArray();
    if (messages.length() == 0) throw new IOException("Missing messages");
    for (int i = 0; i < messages.length(); i++) {
      JSONObject message = messages.getJSONObject(i);
      String role = message.getString("role");
      if (role.equals("tool")) {
        String call = message.getString("tool_call_id");
        if (call.isEmpty()) throw new IOException("Missing tool identity");
        Object content = message.opt("content");
        input.put(
            GoogleProtocol.obj(
                "type",
                "function_call_output",
                "call_id",
                call,
                "output",
                content instanceof String ? content : String.valueOf(content)));
        continue;
      }
      if (!java.util.Arrays.asList("system", "developer", "user", "assistant").contains(role))
        throw new IOException("Invalid role");
      if (!message.isNull("content")) {
        Object content = message.get("content");
        if (content instanceof JSONArray) {
          JSONArray parts = (JSONArray) content, converted = new JSONArray();
          for (int n = 0; n < parts.length(); n++) {
            JSONObject p = parts.getJSONObject(n);
            String type = p.getString("type");
            if (type.equals("text"))
              converted.put(
                  GoogleProtocol.obj(
                      "type",
                      role.equals("assistant") ? "output_text" : "input_text",
                      "text",
                      p.getString("text")));
            else if (type.equals("image_url")) {
              JSONObject image = p.getJSONObject("image_url"),
                  item =
                      GoogleProtocol.obj(
                          "type", "input_image", "image_url", image.getString("url"));
              if (image.has("detail")) item.put("detail", image.get("detail"));
              converted.put(item);
            } else throw new IOException("Unsupported content type");
          }
          content = converted;
        } else if (!(content instanceof String)) throw new IOException("Invalid content");
        input.put(GoogleProtocol.obj("type", "message", "role", role, "content", content));
      }
      JSONArray calls = message.optJSONArray("tool_calls");
      if (calls != null)
        for (int n = 0; n < calls.length(); n++) {
          JSONObject c = calls.getJSONObject(n), f = c.getJSONObject("function");
          input.put(
              GoogleProtocol.obj(
                  "type",
                  "function_call",
                  "call_id",
                  c.getString("id"),
                  "name",
                  f.getString("name"),
                  "arguments",
                  f.getString("arguments")));
        }
    }
    out.put("input", input);
    JSONArray tools = chat.optJSONArray("tools");
    if (tools != null) {
      JSONArray converted = new JSONArray();
      for (int i = 0; i < tools.length(); i++) {
        JSONObject tool = tools.getJSONObject(i);
        if (!tool.getString("type").equals("function"))
          throw new IOException("Only client functions can be converted");
        converted.put(
            new JSONObject(tool.getJSONObject("function").toString()).put("type", "function"));
      }
      out.put("tools", converted);
    }
    if (chat.has("tool_choice")) {
      Object choice = chat.get("tool_choice");
      if (choice instanceof JSONObject) {
        JSONObject c = (JSONObject) choice;
        choice =
            GoogleProtocol.obj(
                "type", "function", "name", c.getJSONObject("function").getString("name"));
      }
      out.put("tool_choice", choice);
    }
    JSONObject format = chat.optJSONObject("response_format");
    if (format != null) {
      JSONObject f = new JSONObject(format.toString());
      if (f.optString("type").equals("json_schema"))
        f = new JSONObject(f.getJSONObject("json_schema").toString()).put("type", "json_schema");
      out.put("text", GoogleProtocol.obj("format", f));
    }
    return out;
  }

  static JSONObject result(JSONObject response, String model) throws Exception {
    String status = response.getString("status");
    if (!status.equals("completed") && !status.equals("incomplete"))
      throw new IOException("Unfinished response");
    JSONArray output = response.getJSONArray("output"), calls = new JSONArray();
    StringBuilder text = new StringBuilder(),
        reasoning = new StringBuilder(),
        refusal = new StringBuilder();
    for (int i = 0; i < output.length(); i++) {
      JSONObject item = output.getJSONObject(i);
      String type = item.getString("type");
      if (type.equals("function_call")) {
        calls.put(
            GoogleProtocol.obj(
                "id",
                item.optString("call_id", item.optString("id")),
                "type",
                "function",
                "function",
                GoogleProtocol.obj(
                    "name", item.getString("name"), "arguments", item.getString("arguments"))));
      } else if (type.equals("message")) {
        JSONArray parts = item.getJSONArray("content");
        for (int n = 0; n < parts.length(); n++) {
          JSONObject p = parts.getJSONObject(n);
          if (p.optString("type").equals("output_text")) text.append(p.getString("text"));
          else if (p.optString("type").equals("refusal")) refusal.append(p.getString("refusal"));
          else throw new IOException("Unsupported response content");
        }
      } else if (type.equals("reasoning")) {
        JSONArray summary = item.optJSONArray("summary");
        if (summary != null)
          for (int n = 0; n < summary.length(); n++)
            reasoning.append(summary.getJSONObject(n).optString("text"));
      } else throw new IOException("Unsupported Responses output item");
    }
    JSONObject message = GoogleProtocol.obj("role", "assistant", "content", text.toString());
    if (reasoning.length() > 0) message.put("reasoning_content", reasoning.toString());
    if (refusal.length() > 0) message.put("refusal", refusal.toString());
    if (calls.length() > 0) message.put("tool_calls", calls);
    String finish = calls.length() > 0 ? "tool_calls" : "stop";
    if (status.equals("incomplete")) {
      JSONObject detail = response.optJSONObject("incomplete_details");
      finish =
          detail != null && detail.optString("reason").equals("content_filter")
              ? "content_filter"
              : "length";
    }
    JSONObject result =
        GoogleProtocol.obj(
            "id",
            response.optString("id"),
            "object",
            "chat.completion",
            "model",
            model,
            "created",
            System.currentTimeMillis() / 1000,
            "choices",
            GoogleProtocol.arr(
                GoogleProtocol.obj("index", 0, "message", message, "finish_reason", finish)));
    if (response.optJSONObject("usage") != null)
      result.put("usage", usage(response.getJSONObject("usage")));
    return result;
  }

  static JSONObject usage(JSONObject value) throws Exception {
    JSONObject usage =
        GoogleProtocol.obj(
            "prompt_tokens",
            value.optInt("input_tokens"),
            "completion_tokens",
            value.optInt("output_tokens"),
            "total_tokens",
            value.optInt(
                "total_tokens", value.optInt("input_tokens") + value.optInt("output_tokens")));
    if (value.has("input_tokens_details"))
      usage.put("prompt_tokens_details", value.get("input_tokens_details"));
    if (value.has("output_tokens_details"))
      usage.put("completion_tokens_details", value.get("output_tokens_details"));
    return usage;
  }
}
