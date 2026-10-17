package com.mixedapi.local;

import static com.mixedapi.local.GoogleProtocol.arr;
import static com.mixedapi.local.GoogleProtocol.obj;

import java.io.IOException;
import org.json.JSONArray;
import org.json.JSONObject;

/** Messages wire codec. Opaque thinking signatures are retained for the next tool turn. */
final class NativeMessages {
  static JSONObject request(JSONObject chat) throws Exception {
    if (chat.optInt("n", 1) != 1) throw new IOException("Messages supports one candidate");
    JSONObject out =
        obj(
            "model",
            chat.getString("model"),
            "max_tokens",
            chat.optInt("max_completion_tokens", chat.optInt("max_tokens", 4096)));
    for (String key : new String[] {"stream", "temperature", "top_p", "top_k"})
      if (chat.has(key)) out.put(key, chat.get(key));
    if (chat.has("stop"))
      out.put(
          "stop_sequences",
          chat.get("stop") instanceof String ? arr(chat.get("stop")) : chat.getJSONArray("stop"));
    JSONArray messages = new JSONArray();
    StringBuilder system = new StringBuilder();
    JSONArray input = chat.getJSONArray("messages");
    for (int i = 0; i < input.length(); i++) {
      JSONObject m = input.getJSONObject(i);
      String role = m.getString("role");
      if (role.equals("system") || role.equals("developer")) {
        if (!(m.get("content") instanceof String)) throw new IOException("System requires text");
        if (system.length() > 0) system.append("\n\n");
        system.append(m.getString("content"));
        continue;
      }
      JSONArray parts = new JSONArray();
      JSONArray details = m.optJSONArray("reasoning_details");
      if (details != null) {
        if (!role.equals("assistant")) throw new IOException("Thinking requires assistant");
        for (int n = 0; n < details.length(); n++) {
          JSONObject d = details.getJSONObject(n);
          if (!d.getString("format").equals("anthropic-claude-v1"))
            throw new IOException("Invalid thinking format");
          if (d.getString("type").equals("reasoning.text")) {
            if (d.getString("signature").isEmpty())
              throw new IOException("Missing thinking signature");
            parts.put(
                obj(
                    "type",
                    "thinking",
                    "thinking",
                    d.getString("text"),
                    "signature",
                    d.getString("signature")));
          } else if (d.getString("type").equals("reasoning.encrypted")) {
            if (d.getString("data").isEmpty()) throw new IOException("Missing encrypted thinking");
            parts.put(obj("type", "redacted_thinking", "data", d.getString("data")));
          } else throw new IOException("Invalid thinking type");
        }
      }
      if (role.equals("tool")) {
        parts.put(
            obj(
                "type",
                "tool_result",
                "tool_use_id",
                m.getString("tool_call_id"),
                "content",
                m.get("content")));
        role = "user";
      } else {
        if (!role.equals("user") && !role.equals("assistant"))
          throw new IOException("Invalid message role");
        Object content = m.opt("content");
        if (content instanceof String) {
          if (!((String) content).isEmpty()) parts.put(obj("type", "text", "text", content));
        } else if (content instanceof JSONArray) {
          JSONArray values = (JSONArray) content;
          for (int n = 0; n < values.length(); n++) {
            JSONObject value = values.getJSONObject(n);
            switch (value.getString("type")) {
              case "text":
                parts.put(obj("type", "text", "text", value.getString("text")));
                break;
              case "image_url":
                String url = value.getJSONObject("image_url").getString("url");
                JSONObject source;
                if (url.startsWith("data:image/") && url.contains(";base64,")) {
                  int split = url.indexOf(";base64,");
                  source =
                      obj(
                          "type",
                          "base64",
                          "media_type",
                          url.substring(5, split),
                          "data",
                          url.substring(split + 8));
                } else if (url.startsWith("https://")) source = obj("type", "url", "url", url);
                else throw new IOException("Invalid image URL");
                parts.put(obj("type", "image", "source", source));
                break;
              default:
                throw new IOException("Unsupported content");
            }
          }
        } else if (content != null && content != JSONObject.NULL)
          throw new IOException("Invalid content");
        JSONArray calls = m.optJSONArray("tool_calls");
        if (calls != null)
          for (int n = 0; n < calls.length(); n++) {
            JSONObject c = calls.getJSONObject(n), f = c.getJSONObject("function");
            parts.put(
                obj(
                    "type",
                    "tool_use",
                    "id",
                    c.getString("id"),
                    "name",
                    f.getString("name"),
                    "input",
                    new JSONObject(f.getString("arguments"))));
          }
      }
      if (parts.length() == 0) continue;
      if (messages.length() > 0
          && messages.getJSONObject(messages.length() - 1).getString("role").equals(role)) {
        JSONArray previous = messages.getJSONObject(messages.length() - 1).getJSONArray("content");
        for (int n = 0; n < parts.length(); n++) previous.put(parts.get(n));
      } else messages.put(obj("role", role, "content", parts));
    }
    if (messages.length() == 0) throw new IOException("Empty messages");
    out.put("messages", messages);
    if (system.length() > 0) out.put("system", system.toString());
    JSONArray tools = chat.optJSONArray("tools");
    if (tools != null) {
      JSONArray mapped = new JSONArray();
      for (int i = 0; i < tools.length(); i++) {
        JSONObject t = tools.getJSONObject(i);
        if (!t.getString("type").equals("function")) throw new IOException("Unsupported tool");
        JSONObject f = t.getJSONObject("function"),
            item =
                obj(
                    "name",
                    f.getString("name"),
                    "input_schema",
                    f.optJSONObject("parameters") == null
                        ? obj("type", "object", "properties", new JSONObject())
                        : f.getJSONObject("parameters"));
        for (String key : new String[] {"description", "strict"})
          if (f.has(key)) item.put(key, f.get(key));
        mapped.put(item);
      }
      out.put("tools", mapped);
    }
    if (chat.has("tool_choice")) {
      Object value = chat.get("tool_choice");
      JSONObject choice;
      if (value instanceof JSONObject)
        choice =
            obj(
                "type",
                "tool",
                "name",
                ((JSONObject) value).getJSONObject("function").getString("name"));
      else {
        String type = value.toString();
        if (type.equals("required")) type = "any";
        if (!java.util.Arrays.asList("auto", "none", "any").contains(type))
          throw new IOException("Invalid tool choice");
        choice = obj("type", type);
      }
      out.put("tool_choice", choice);
    }
    if (chat.has("parallel_tool_calls") && !chat.getBoolean("parallel_tool_calls")) {
      if (!out.has("tool_choice")) out.put("tool_choice", obj("type", "auto"));
      out.getJSONObject("tool_choice").put("disable_parallel_tool_use", true);
    }
    JSONObject format = chat.optJSONObject("response_format");
    if (format != null && !format.optString("type").equals("text"))
      throw new IOException("Use native Messages for output format");
    thinking(chat, out);
    return out;
  }

  private static void thinking(JSONObject input, JSONObject out) throws Exception {
    if (!input.has("reasoning_effort")) return;
    String effort = input.getString("reasoning_effort"), model = input.getString("model");
    if (effort.equals("none")) {
      out.put("thinking", obj("type", "disabled"));
      return;
    }
    int budget;
    switch (effort) {
      case "low":
        budget = 1024;
        break;
      case "medium":
        budget = 4096;
        break;
      case "high":
        budget = 8192;
        break;
      case "max":
        budget = 16384;
        break;
      default:
        throw new IOException("Invalid reasoning effort");
    }
    boolean adaptive =
        model.matches("claude-(opus|sonnet)-4-[67](-.*)?")
            || model.matches("claude-(opus|sonnet|fable|haiku)-5(-.*)?");
    if (adaptive) {
      if (effort.equals("max") && !model.startsWith("claude-opus-"))
        throw new IOException("Unsupported reasoning effort");
      out.put("thinking", obj("type", "adaptive")).put("output_config", obj("effort", effort));
    } else {
      if (!model.matches("claude-(3-7-sonnet|opus-4|sonnet-4|haiku-4-5)(-.*)?"))
        throw new IOException("Unknown model thinking capability");
      if (out.getInt("max_tokens") <= 1024) throw new IOException("Insufficient thinking budget");
      out.put(
          "thinking",
          obj("type", "enabled", "budget_tokens", Math.min(budget, out.getInt("max_tokens") - 1)));
      if (model.startsWith("claude-opus-4-5"))
        out.put("output_config", obj("effort", effort.equals("max") ? "high" : effort));
    }
    JSONObject choice = out.optJSONObject("tool_choice");
    if (choice != null && java.util.Arrays.asList("any", "tool").contains(choice.optString("type")))
      throw new IOException("Thinking cannot force tools");
  }

  static String finish(String value) throws IOException {
    switch (value) {
      case "end_turn":
      case "stop_sequence":
        return "stop";
      case "tool_use":
        return "tool_calls";
      case "max_tokens":
      case "model_context_window_exceeded":
        return "length";
      case "refusal":
        return "content_filter";
      default:
        throw new IOException("Unknown Messages stop reason");
    }
  }

  static JSONObject usage(JSONObject value) throws Exception {
    int cached = value.optInt("cache_read_input_tokens"),
        input = value.optInt("input_tokens") + cached + value.optInt("cache_creation_input_tokens"),
        output = value.optInt("output_tokens");
    return obj(
        "prompt_tokens",
        input,
        "completion_tokens",
        output,
        "total_tokens",
        input + output,
        "prompt_tokens_details",
        obj("cached_tokens", cached));
  }

  static JSONObject detail(JSONObject block, int index) throws Exception {
    String type = block.getString("type");
    JSONObject d;
    if (type.equals("thinking")) {
      if (block.optString("signature").isEmpty())
        throw new IOException("Missing thinking signature");
      d =
          obj(
              "type",
              "reasoning.text",
              "text",
              block.getString("thinking"),
              "signature",
              block.getString("signature"));
    } else d = obj("type", "reasoning.encrypted", "data", block.getString("data"));
    return d.put("format", "anthropic-claude-v1").put("index", index);
  }

  static JSONObject result(JSONObject input, String model) throws Exception {
    JSONArray content = input.getJSONArray("content"),
        tools = new JSONArray(),
        details = new JSONArray();
    StringBuilder text = new StringBuilder(), thinking = new StringBuilder();
    for (int i = 0; i < content.length(); i++) {
      JSONObject part = content.getJSONObject(i);
      switch (part.getString("type")) {
        case "text":
          text.append(part.getString("text"));
          break;
        case "thinking":
          thinking.append(part.getString("thinking"));
          details.put(detail(part, details.length()));
          break;
        case "redacted_thinking":
          details.put(detail(part, details.length()));
          break;
        case "tool_use":
          tools.put(
              obj(
                  "id",
                  part.getString("id"),
                  "type",
                  "function",
                  "function",
                  obj(
                      "name",
                      part.getString("name"),
                      "arguments",
                      part.getJSONObject("input").toString())));
          break;
        default:
          throw new IOException("Unsupported Messages output");
      }
    }
    JSONObject message = obj("role", "assistant", "content", text.toString());
    if (thinking.length() > 0) message.put("reasoning_content", thinking.toString());
    if (details.length() > 0) message.put("reasoning_details", details);
    if (tools.length() > 0) message.put("tool_calls", tools);
    return obj(
        "id",
        input.getString("id"),
        "model",
        model,
        "object",
        "chat.completion",
        "created",
        System.currentTimeMillis() / 1000,
        "choices",
        arr(
            obj(
                "index",
                0,
                "message",
                message,
                "finish_reason",
                finish(input.getString("stop_reason")))),
        "usage",
        usage(input.getJSONObject("usage")));
  }
}
