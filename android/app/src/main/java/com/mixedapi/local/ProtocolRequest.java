package com.mixedapi.local;

import static com.mixedapi.local.GoogleProtocol.arr;
import static com.mixedapi.local.GoogleProtocol.obj;

import java.io.IOException;
import org.json.JSONArray;
import org.json.JSONObject;

/**
 * Converts stateless client requests; unsupported state and cache semantics fail before inference.
 */
final class ProtocolRequest {
  static void only(JSONObject value, String... fields) throws Exception {
    java.util.List<String> allowed = java.util.Arrays.asList(fields);
    for (String key : GoogleProtocol.keys(value))
      if (!allowed.contains(key) && !value.isNull(key))
        throw new IOException("Unsupported protocol option");
  }

  static void copy(JSONObject to, JSONObject from, String... fields) throws Exception {
    for (String key : fields) if (from.has(key)) to.put(key, from.get(key));
  }

  static void signature(JSONObject to, JSONObject from) throws Exception {
    copy(to, from, "thought_signature");
  }

  static JSONObject responses(JSONObject input) throws Exception {
    only(
        input,
        "model",
        "stream",
        "input",
        "instructions",
        "max_output_tokens",
        "temperature",
        "top_p",
        "reasoning",
        "text",
        "tools",
        "tool_choice",
        "parallel_tool_calls",
        "store");
    if (input.optBoolean("store")) throw new IOException("Cloud storage unavailable");
    JSONObject out = new JSONObject();
    copy(out, input, "model", "stream", "temperature", "top_p", "parallel_tool_calls");
    if (input.has("max_output_tokens"))
      out.put("max_completion_tokens", input.get("max_output_tokens"));
    if (input.has("reasoning")) {
      JSONObject r = input.getJSONObject("reasoning");
      only(r, "effort");
      if (r.has("effort")) out.put("reasoning_effort", r.get("effort"));
    }
    if (input.has("text")) {
      JSONObject text = input.getJSONObject("text");
      only(text, "format");
      if (text.has("format")) {
        JSONObject format = new JSONObject(text.getJSONObject("format").toString());
        if (format.optString("type").equals("json_schema")) {
          format.remove("type");
          out.put("response_format", obj("type", "json_schema", "json_schema", format));
        } else out.put("response_format", format);
      }
    }
    JSONArray messages = new JSONArray();
    if (input.has("instructions") && !input.getString("instructions").isEmpty())
      messages.put(obj("role", "system", "content", input.getString("instructions")));
    Object value = input.get("input");
    JSONArray items =
        value instanceof String
            ? arr(obj("role", "user", "content", value))
            : input.getJSONArray("input");
    if (items.length() == 0) throw new IOException("Empty input");
    for (int i = 0; i < items.length(); i++) {
      JSONObject item = items.getJSONObject(i);
      switch (item.optString("type", "message")) {
        case "message":
          {
            only(item, "type", "role", "content", "id", "status");
            String role = item.getString("role");
            if (!java.util.Arrays.asList("user", "assistant", "system", "developer").contains(role))
              throw new IOException("Invalid role");
            messages.put(obj("role", role, "content", responseContent(item.get("content"))));
            break;
          }
        case "function_call":
          {
            only(item, "type", "id", "call_id", "name", "arguments", "status", "thought_signature");
            JSONObject call =
                obj(
                    "type",
                    "function",
                    "id",
                    required(item, "call_id"),
                    "function",
                    obj("name", required(item, "name"), "arguments", item.getString("arguments")));
            objectArguments(item.getString("arguments"));
            signature(call, item);
            messages.put(
                obj("role", "assistant", "content", JSONObject.NULL, "tool_calls", arr(call)));
            break;
          }
        case "function_call_output":
          only(item, "type", "id", "call_id", "output", "status");
          messages.put(
              obj(
                  "role",
                  "tool",
                  "tool_call_id",
                  required(item, "call_id"),
                  "content",
                  responseContent(item.get("output"))));
          break;
        default:
          throw new IOException("Unsupported input item");
      }
    }
    out.put("messages", messages);
    if (input.has("tools")) {
      JSONArray tools = input.getJSONArray("tools"), result = new JSONArray();
      for (int i = 0; i < tools.length(); i++) {
        JSONObject tool = new JSONObject(tools.getJSONObject(i).toString());
        if (!tool.getString("type").equals("function"))
          throw new IOException("Unsupported server tool");
        only(tool, "type", "name", "description", "parameters", "strict");
        tool.remove("type");
        result.put(obj("type", "function", "function", tool));
      }
      out.put("tools", result);
    }
    if (input.has("tool_choice")) {
      Object choice = input.get("tool_choice");
      if (choice instanceof JSONObject) {
        JSONObject c = (JSONObject) choice;
        only(c, "type", "name");
        if (!c.getString("type").equals("function"))
          throw new IOException("Unsupported tool choice");
        choice = obj("type", "function", "function", obj("name", required(c, "name")));
      }
      out.put("tool_choice", choice);
    }
    return out;
  }

  private static Object responseContent(Object value) throws Exception {
    if (value instanceof String) return value;
    if (!(value instanceof JSONArray)) throw new IOException("Invalid content");
    JSONArray parts = (JSONArray) value, result = new JSONArray();
    for (int i = 0; i < parts.length(); i++) {
      JSONObject p = parts.getJSONObject(i);
      switch (p.getString("type")) {
        case "input_text":
        case "output_text":
          only(p, "type", "text");
          result.put(obj("type", "text", "text", p.getString("text")));
          break;
        case "input_image":
          {
            only(p, "type", "image_url", "detail");
            JSONObject image = obj("url", required(p, "image_url"));
            copy(image, p, "detail");
            result.put(obj("type", "image_url", "image_url", image));
            break;
          }
        default:
          throw new IOException("Unsupported content type");
      }
    }
    return result;
  }

  static JSONObject messages(JSONObject input) throws Exception {
    only(
        input,
        "model",
        "stream",
        "messages",
        "system",
        "max_tokens",
        "temperature",
        "top_p",
        "top_k",
        "stop_sequences",
        "thinking",
        "tools",
        "tool_choice");
    JSONObject out = new JSONObject();
    copy(out, input, "model", "stream", "max_tokens", "temperature", "top_p", "top_k", "thinking");
    if (input.has("stop_sequences")) out.put("stop", input.get("stop_sequences"));
    JSONArray messages = new JSONArray();
    if (input.has("system"))
      messages.put(obj("role", "system", "content", text(input.get("system"))));
    JSONArray items = input.getJSONArray("messages");
    if (items.length() == 0) throw new IOException("Empty messages");
    for (int i = 0; i < items.length(); i++) {
      JSONObject item = items.getJSONObject(i);
      only(item, "role", "content");
      String role = item.getString("role");
      if (!role.equals("user") && !role.equals("assistant")) throw new IOException("Invalid role");
      if (item.get("content") instanceof String) {
        messages.put(obj("role", role, "content", item.get("content")));
        continue;
      }
      JSONArray content = new JSONArray(),
          calls = new JSONArray(),
          details = new JSONArray(),
          parts = item.getJSONArray("content");
      for (int n = 0; n < parts.length(); n++) {
        JSONObject part = parts.getJSONObject(n);
        switch (part.getString("type")) {
          case "text":
            only(part, "type", "text");
            content.put(obj("type", "text", "text", part.getString("text")));
            break;
          case "image":
            {
              only(part, "type", "source");
              JSONObject source = part.getJSONObject("source");
              only(source, "type", "url", "media_type", "data");
              String url;
              if (source.getString("type").equals("base64"))
                url =
                    "data:"
                        + source.getString("media_type")
                        + ";base64,"
                        + source.getString("data");
              else if (source.getString("type").equals("url")) url = required(source, "url");
              else throw new IOException("Unsupported image source");
              content.put(obj("type", "image_url", "image_url", obj("url", url)));
              break;
            }
          case "tool_use":
            {
              only(part, "type", "id", "name", "input", "thought_signature");
              if (!role.equals("assistant")) throw new IOException("Invalid tool role");
              JSONObject call =
                  obj(
                      "id",
                      required(part, "id"),
                      "type",
                      "function",
                      "function",
                      obj(
                          "name",
                          required(part, "name"),
                          "arguments",
                          part.getJSONObject("input").toString()));
              signature(call, part);
              calls.put(call);
              break;
            }
          case "tool_result":
            {
              only(part, "type", "tool_use_id", "content", "is_error");
              if (!role.equals("user")) throw new IOException("Invalid tool result role");
              String value = text(part.get("content"));
              if (part.optBoolean("is_error")) value = obj("error", value).toString();
              messages.put(
                  obj(
                      "role",
                      "tool",
                      "tool_call_id",
                      required(part, "tool_use_id"),
                      "content",
                      value));
              break;
            }
          case "thinking":
          case "redacted_thinking":
            {
              only(part, "type", "thinking", "signature", "data");
              if (!role.equals("assistant")) throw new IOException("Invalid thinking role");
              JSONObject detail = obj("format", "anthropic-claude-v1");
              if (part.getString("type").equals("thinking"))
                detail
                    .put("type", "reasoning.text")
                    .put("text", part.getString("thinking"))
                    .put("signature", required(part, "signature"));
              else detail.put("type", "reasoning.encrypted").put("data", required(part, "data"));
              details.put(detail);
              break;
            }
          default:
            throw new IOException("Unsupported Messages content");
        }
      }
      if (content.length() > 0 || calls.length() > 0 || details.length() > 0) {
        JSONObject message = obj("role", role, "content", content);
        if (calls.length() > 0) message.put("tool_calls", calls);
        if (details.length() > 0) message.put("reasoning_details", details);
        messages.put(message);
      }
    }
    out.put("messages", messages);
    if (input.has("tools")) {
      JSONArray tools = input.getJSONArray("tools"), result = new JSONArray();
      for (int i = 0; i < tools.length(); i++) {
        JSONObject tool = tools.getJSONObject(i);
        only(tool, "type", "name", "description", "input_schema");
        if (!tool.optString("type", "custom").equals("custom"))
          throw new IOException("Unsupported server tool");
        JSONObject function =
            obj("name", required(tool, "name"), "parameters", tool.getJSONObject("input_schema"));
        copy(function, tool, "description");
        result.put(obj("type", "function", "function", function));
      }
      out.put("tools", result);
    }
    if (input.has("tool_choice")) {
      JSONObject choice = input.getJSONObject("tool_choice");
      only(choice, "type", "name", "disable_parallel_tool_use");
      switch (choice.getString("type")) {
        case "auto":
        case "none":
          out.put("tool_choice", choice.getString("type"));
          break;
        case "any":
          out.put("tool_choice", "required");
          break;
        case "tool":
          out.put(
              "tool_choice",
              obj("type", "function", "function", obj("name", required(choice, "name"))));
          break;
        default:
          throw new IOException("Invalid tool choice");
      }
      if (choice.has("disable_parallel_tool_use"))
        out.put("parallel_tool_calls", !choice.getBoolean("disable_parallel_tool_use"));
    }
    return out;
  }

  static String required(JSONObject value, String key) throws Exception {
    String text = value.getString(key);
    if (text.isEmpty()) throw new IOException("Missing required field");
    return text;
  }

  static JSONObject objectArguments(String text) throws Exception {
    org.json.JSONTokener parser = new org.json.JSONTokener(text);
    Object value = parser.nextValue();
    if (!(value instanceof JSONObject) || parser.nextClean() != 0)
      throw new IOException("Invalid tool arguments");
    return (JSONObject) value;
  }

  private static String text(Object value) throws Exception {
    if (value instanceof String) return (String) value;
    if (!(value instanceof JSONArray)) throw new IOException("Invalid text");
    StringBuilder result = new StringBuilder();
    JSONArray parts = (JSONArray) value;
    for (int i = 0; i < parts.length(); i++) {
      JSONObject part = parts.getJSONObject(i);
      only(part, "type", "text");
      if (!part.getString("type").equals("text")) throw new IOException("Unsupported text content");
      result.append(part.getString("text"));
    }
    return result.toString();
  }
}
