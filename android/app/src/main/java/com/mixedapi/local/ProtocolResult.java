package com.mixedapi.local;

import static com.mixedapi.local.GoogleProtocol.arr;
import static com.mixedapi.local.GoogleProtocol.obj;

import java.io.IOException;
import java.util.UUID;
import org.json.JSONArray;
import org.json.JSONObject;

/** One completed Chat candidate into client protocol results, including tools and truncation. */
final class ProtocolResult {
  static String id(String prefix) {
    return prefix + UUID.randomUUID().toString().replace("-", "");
  }

  static JSONObject response(String model) throws Exception {
    return obj(
        "id",
        id("resp_"),
        "object",
        "response",
        "model",
        model,
        "created_at",
        System.currentTimeMillis() / 1000,
        "status",
        "in_progress",
        "output",
        new JSONArray(),
        "error",
        JSONObject.NULL,
        "incomplete_details",
        JSONObject.NULL,
        "store",
        false);
  }

  static JSONObject usage(JSONObject chat, boolean messages) throws Exception {
    JSONObject result =
        obj(
            "input_tokens",
            chat == null ? 0 : chat.optLong("prompt_tokens"),
            "output_tokens",
            chat == null ? 0 : chat.optLong("completion_tokens"));
    if (!messages) {
      result.put(
          "total_tokens",
          chat == null
              ? 0
              : chat.optLong(
                  "total_tokens",
                  chat.optLong("prompt_tokens") + chat.optLong("completion_tokens")));
      if (chat != null) {
        if (chat.has("prompt_tokens_details"))
          result.put("input_tokens_details", chat.get("prompt_tokens_details"));
        if (chat.has("completion_tokens_details"))
          result.put("output_tokens_details", chat.get("completion_tokens_details"));
      }
    } else if (chat != null && chat.optJSONObject("prompt_tokens_details") != null) {
      long cached = chat.getJSONObject("prompt_tokens_details").optLong("cached_tokens");
      result.put("cache_read_input_tokens", cached);
      result.put("input_tokens", Math.max(0, result.getLong("input_tokens") - cached));
    }
    return result;
  }

  static String status(String finish) throws IOException {
    switch (finish) {
      case "stop":
      case "tool_calls":
        return "completed";
      case "length":
      case "content_filter":
        return "incomplete";
      default:
        throw new IOException("Invalid finish reason");
    }
  }

  static String stop(String finish) throws IOException {
    status(finish);
    switch (finish) {
      case "tool_calls":
        return "tool_use";
      case "length":
        return "max_tokens";
      case "content_filter":
        return "refusal";
      default:
        return "end_turn";
    }
  }

  static JSONObject convert(JSONObject chat, String model, boolean messages) throws Exception {
    if (!chat.isNull("error")) throw new IOException("Provider error");
    JSONArray choices = chat.getJSONArray("choices");
    if (choices.length() != 1) throw new IOException("Invalid choices");
    JSONObject choice = choices.getJSONObject(0), message = choice.getJSONObject("message");
    String finish = choice.getString("finish_reason"), status = status(finish);
    JSONArray output = new JSONArray(), content = new JSONArray();
    JSONObject details = null;
    JSONArray reasoning = message.optJSONArray("reasoning_details");
    String thought = message.optString("reasoning_content", "");
    if (reasoning != null && reasoning.length() > 0 && messages) {
      for (int i = 0; i < reasoning.length(); i++) {
        JSONObject d = reasoning.getJSONObject(i);
        if (!d.optString("format").equals("anthropic-claude-v1"))
          throw new IOException("Unsupported opaque reasoning");
        if (d.optString("type").equals("reasoning.text"))
          content.put(
              obj(
                  "type",
                  "thinking",
                  "thinking",
                  d.getString("text"),
                  "signature",
                  d.getString("signature")));
        else if (d.optString("type").equals("reasoning.encrypted"))
          content.put(obj("type", "redacted_thinking", "data", d.getString("data")));
        else throw new IOException("Unsupported reasoning type");
      }
    } else if (!thought.isEmpty() || reasoning != null && reasoning.length() > 0) {
      details =
          obj(
              "id",
              id("rs_"),
              "type",
              "reasoning",
              "summary",
              thought.isEmpty()
                  ? new JSONArray()
                  : arr(obj("type", "summary_text", "text", thought)));
      if (reasoning != null) details.put("reasoning_details", reasoning);
      output.put(details);
      if (messages) content.put(obj("type", "thinking", "thinking", thought, "signature", ""));
    }
    Object textValue = message.opt("content");
    if (textValue != null && textValue != JSONObject.NULL && !(textValue instanceof String))
      throw new IOException("Unsupported generated content");
    String text = textValue instanceof String ? (String) textValue : "",
        refusal = message.optString("refusal", "");
    JSONArray parts = new JSONArray();
    if (!text.isEmpty()) {
      JSONArray annotations = new JSONArray(), source = message.optJSONArray("annotations");
      if (source != null)
        for (int i = 0; i < source.length(); i++) {
          JSONObject a = source.getJSONObject(i);
          annotations.put(
              a.optJSONObject("url_citation") == null
                  ? a
                  : new JSONObject(a.getJSONObject("url_citation").toString())
                      .put("type", "url_citation"));
        }
      parts.put(obj("type", "output_text", "text", text, "annotations", annotations));
      if (messages && annotations.length() > 0)
        throw new IOException("Citation representation unavailable");
      content.put(obj("type", "text", "text", text));
    }
    if (!refusal.isEmpty()) {
      parts.put(obj("type", "refusal", "refusal", refusal));
      content.put(obj("type", "text", "text", refusal));
    }
    if (parts.length() > 0)
      output.put(
          obj(
              "id",
              id("msg_"),
              "type",
              "message",
              "role",
              "assistant",
              "status",
              status,
              "content",
              parts));
    JSONArray calls = message.optJSONArray("tool_calls");
    if (calls != null)
      for (int i = 0; i < calls.length(); i++) {
        JSONObject call = calls.getJSONObject(i), f = call.getJSONObject("function");
        String args = f.getString("arguments");
        JSONObject parsed = null;
        if (status.equals("completed") || messages) parsed = ProtocolRequest.objectArguments(args);
        JSONObject item =
            obj(
                "id",
                id("fc_"),
                "type",
                "function_call",
                "call_id",
                ProtocolRequest.required(call, "id"),
                "name",
                ProtocolRequest.required(f, "name"),
                "arguments",
                args,
                "status",
                status);
        ProtocolRequest.signature(item, call);
        output.put(item);
        if (messages) {
          JSONObject tool =
              obj(
                  "type",
                  "tool_use",
                  "id",
                  call.getString("id"),
                  "name",
                  f.getString("name"),
                  "input",
                  parsed);
          ProtocolRequest.signature(tool, call);
          content.put(tool);
        }
      }
    if (messages)
      return obj(
          "id",
          id("msg_"),
          "type",
          "message",
          "role",
          "assistant",
          "model",
          model,
          "content",
          content,
          "stop_reason",
          refusal.isEmpty() ? stop(finish) : "refusal",
          "stop_sequence",
          JSONObject.NULL,
          "usage",
          usage(chat.optJSONObject("usage"), true));
    JSONObject result =
        response(model)
            .put("status", status)
            .put("output", output)
            .put("usage", usage(chat.optJSONObject("usage"), false));
    if (status.equals("incomplete"))
      result.put(
          "incomplete_details",
          obj("reason", finish.equals("content_filter") ? "content_filter" : "max_output_tokens"));
    return result;
  }
}
