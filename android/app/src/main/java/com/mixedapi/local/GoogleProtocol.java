package com.mixedapi.local;

import java.io.IOException;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.UUID;
import org.json.JSONArray;
import org.json.JSONObject;
import org.json.JSONTokener;

/** Gemini is the common representation; no provider URLs or credentials enter this codec. */
final class GoogleProtocol {
  static JSONObject obj(Object... values) throws Exception {
    JSONObject o = new JSONObject();
    for (int i = 0; i < values.length; i += 2) o.put((String) values[i], values[i + 1]);
    return o;
  }

  static JSONArray arr(Object... values) {
    JSONArray a = new JSONArray();
    for (Object v : values) a.put(v == null ? JSONObject.NULL : v);
    return a;
  }

  static JSONArray array(Object v) {
    return v instanceof JSONArray ? (JSONArray) v : new JSONArray();
  }

  static Object at(JSONArray a, int n) {
    return a.isNull(n) ? JSONObject.NULL : a.opt(n);
  }

  static JSONArray trim(JSONArray a) throws Exception {
    while (a.length() > 0 && a.isNull(a.length() - 1)) a.remove(a.length() - 1);
    return a;
  }

  static List<String> keys(JSONObject o) {
    List<String> result = new ArrayList<>();
    o.keys().forEachRemaining(result::add);
    Collections.sort(result);
    return result;
  }

  static JSONObject gemini(JSONObject input) throws Exception {
    java.util.List<String> allowed =
        java.util.Arrays.asList(
            "model",
            "messages",
            "stream",
            "stream_options",
            "temperature",
            "top_p",
            "top_k",
            "max_tokens",
            "max_completion_tokens",
            "seed",
            "n",
            "stop",
            "response_format",
            "reasoning_effort",
            "tools",
            "tool_choice",
            "user");
    for (String key : keys(input)) if (!allowed.contains(key)) throw new IOException("不支持的请求参数");
    if (input.has("stream") && !(input.get("stream") instanceof Boolean))
      throw new IOException("stream 必须是布尔值");
    for (String key : new String[] {"n", "top_k", "max_tokens", "max_completion_tokens", "seed"})
      if (input.has(key)) {
        Object value = input.get(key);
        if (!(value instanceof Number)
            || ((Number) value).doubleValue() != ((Number) value).longValue())
          throw new IOException("请求参数需要整数");
        if (!"seed".equals(key) && ((Number) value).longValue() < 1)
          throw new IOException("请求参数超出范围");
      }
    for (String key : new String[] {"temperature", "top_p"})
      if (input.has(key)) {
        Object value = input.get(key);
        if (!(value instanceof Number)) throw new IOException("请求参数需要数字");
        double v = ((Number) value).doubleValue();
        if (!Double.isFinite(v) || v < 0 || v > ("temperature".equals(key) ? 2 : 1))
          throw new IOException("请求参数超出范围");
      }
    if (input.has("stream_options")) {
      JSONObject options = input.getJSONObject("stream_options");
      for (String key : keys(options))
        if (!"include_usage".equals(key) || !(options.get(key) instanceof Boolean))
          throw new IOException("stream_options 无效");
    }
    JSONArray contents = new JSONArray(), system = new JSONArray();
    Map<String, String> names = new HashMap<>();
    JSONArray messages = input.getJSONArray("messages");
    if (messages.length() == 0) throw new IOException("需要非空 messages 数组");
    for (int i = 0; i < messages.length(); i++) {
      JSONObject m = messages.getJSONObject(i);
      String role = m.getString("role");
      JSONArray parts = parts(m.opt("content"));
      switch (role) {
        case "system":
        case "developer":
          for (int p = 0; p < parts.length(); p++) system.put(parts.get(p));
          continue;
        case "assistant":
          JSONArray calls = m.optJSONArray("tool_calls");
          if (calls != null)
            for (int c = 0; c < calls.length(); c++) {
              JSONObject call = calls.getJSONObject(c), f = call.getJSONObject("function");
              String name = f.getString("name");
              names.put(call.getString("id"), name);
              JSONObject part =
                  obj(
                      "functionCall",
                      obj("name", name, "args", new JSONObject(f.getString("arguments"))));
              if (call.has("thought_signature"))
                part.put("thoughtSignature", call.getString("thought_signature"));
              parts.put(part);
            }
          role = "model";
          break;
        case "tool":
          String name = names.get(m.optString("tool_call_id"));
          if (name == null) throw new IOException("工具结果缺少对应调用");
          Object result = m.opt("content");
          if (result instanceof String) {
            try {
              result = new JSONTokener((String) result).nextValue();
            } catch (Exception ignored) {
            }
          }
          if (!(result instanceof JSONObject)) result = obj("result", result);
          parts = arr(obj("functionResponse", obj("name", name, "response", result)));
          role = "user";
          break;
        case "user":
          break;
        default:
          throw new IOException("消息角色无效");
      }
      if (parts.length() > 0) contents.put(obj("role", role, "parts", parts));
    }
    if (contents.length() == 0) throw new IOException("消息内容不能为空");
    JSONObject out = obj("contents", contents);
    if (system.length() > 0) out.put("systemInstruction", obj("parts", system));
    JSONObject config = new JSONObject();
    String[][] fields = {
      {"temperature", "temperature"},
      {"top_p", "topP"},
      {"top_k", "topK"},
      {"max_tokens", "maxOutputTokens"},
      {"max_completion_tokens", "maxOutputTokens"},
      {"seed", "seed"},
      {"n", "candidateCount"}
    };
    for (String[] pair : fields) if (input.has(pair[0])) config.put(pair[1], input.get(pair[0]));
    if (input.has("n") && (input.getInt("n") < 1 || input.getInt("n") > 8))
      throw new IOException("n 必须是 1–8 的整数");
    if (input.has("stop"))
      config.put(
          "stopSequences",
          input.get("stop") instanceof String
              ? arr(input.getString("stop"))
              : input.getJSONArray("stop"));
    JSONObject format = input.optJSONObject("response_format");
    if (format != null) {
      String type = format.optString("type");
      if ("json_object".equals(type) || "json_schema".equals(type)) {
        config.put("responseMimeType", "application/json");
        if (format.has("json_schema"))
          config.put(
              "responseJsonSchema", format.getJSONObject("json_schema").getJSONObject("schema"));
      } else if (!"text".equals(type)) throw new IOException("response_format 无效");
    }
    if (input.has("reasoning_effort")) {
      String effort = input.getString("reasoning_effort");
      if (!java.util.Arrays.asList("low", "medium", "high", "minimal").contains(effort))
        throw new IOException("reasoning_effort 无效");
      config.put(
          "thinkingConfig",
          obj("thinkingLevel", effort.toUpperCase(Locale.ROOT), "includeThoughts", true));
    }
    if (input.optString("model").contains("-image"))
      config.put("responseModalities", arr("TEXT", "IMAGE"));
    if (config.length() > 0) out.put("generationConfig", config);
    JSONArray tools = input.optJSONArray("tools"), declarations = new JSONArray();
    if (tools != null)
      for (int i = 0; i < tools.length(); i++) {
        JSONObject t = tools.getJSONObject(i);
        if (!"function".equals(t.optString("type"))) throw new IOException("工具类型无效");
        JSONObject f = t.getJSONObject("function"), d = obj("name", f.getString("name"));
        if (f.has("description")) d.put("description", f.get("description"));
        if (f.has("parameters")) d.put("parameters", f.getJSONObject("parameters"));
        declarations.put(d);
      }
    if (declarations.length() > 0) out.put("tools", arr(obj("functionDeclarations", declarations)));
    if (input.has("tool_choice")) {
      Object choice = input.get("tool_choice");
      String mode;
      JSONObject calling = new JSONObject();
      if (choice instanceof JSONObject) {
        mode = "ANY";
        calling.put(
            "allowedFunctionNames",
            arr(((JSONObject) choice).getJSONObject("function").getString("name")));
      } else {
        switch (choice.toString()) {
          case "none":
            mode = "NONE";
            break;
          case "auto":
            mode = "AUTO";
            break;
          case "required":
            mode = "ANY";
            break;
          default:
            throw new IOException("tool_choice 无效");
        }
      }
      calling.put("mode", mode);
      out.put("toolConfig", obj("functionCallingConfig", calling));
    }
    return out;
  }

  private static JSONArray parts(Object content) throws Exception {
    JSONArray out = new JSONArray();
    if (content == null || content == JSONObject.NULL) return out;
    if (content instanceof String) {
      if (!((String) content).isEmpty()) out.put(obj("text", content));
      return out;
    }
    if (!(content instanceof JSONArray)) throw new IOException("消息内容必须是文本或数组");
    JSONArray items = (JSONArray) content;
    for (int i = 0; i < items.length(); i++) {
      JSONObject p = items.getJSONObject(i);
      switch (p.getString("type")) {
        case "text":
          out.put(obj("text", p.getString("text")));
          break;
        case "image_url":
          String url = p.getJSONObject("image_url").getString("url");
          int cut = url.indexOf(",");
          if (cut < 0
              || !url.startsWith("data:image/")
              || !url.substring(0, cut).endsWith(";base64"))
            throw new IOException("图片请使用 base64 data URL");
          out.put(
              obj(
                  "inlineData",
                  obj("mimeType", url.substring(5, cut - 7), "data", url.substring(cut + 1))));
          break;
        default:
          throw new IOException("消息内容类型尚未支持");
      }
    }
    return out;
  }

  static JSONObject operation(String kind, String model, JSONObject gemini) throws Exception {
    if (!model.matches("[A-Za-z0-9._-]+")) throw new IOException("模型 ID 无效");
    JSONObject op = obj("kind", kind, "model", model);
    if (!"playground".equals(kind)) {
      op.put("body", gemini);
      if ("build".equals(kind)) op.put("unary", true);
      return op;
    }
    JSONArray wire = new JSONArray(), contents = new JSONArray();
    List<String> binding = new ArrayList<>(), prompt = new ArrayList<>();
    JSONArray source = gemini.getJSONArray("contents");
    for (int i = 0; i < source.length(); i++)
      contents.put(encodeContent(source.getJSONObject(i), binding, prompt));
    wire.put(0, "models/" + model);
    wire.put(1, contents);
    wire.put(10, 1);
    op.put("binding", String.join(" ", binding));
    op.put("prompt", String.join(" ", prompt));
    if (gemini.has("systemInstruction"))
      wire.put(
          5,
          encodeContent(
              gemini.getJSONObject("systemInstruction"), new ArrayList<>(), new ArrayList<>()));
    JSONObject g = gemini.optJSONObject("generationConfig");
    JSONArray config = new JSONArray();
    config.put(13, 1);
    if (g != null)
      for (String key : keys(g)) {
        Object v = g.get(key);
        switch (key) {
          case "stopSequences":
            config.put(1, v);
            break;
          case "candidateCount":
            if (g.getInt(key) != 1) throw new IOException("Playground 仅支持 n=1");
            break;
          case "maxOutputTokens":
            config.put(3, v);
            break;
          case "temperature":
            config.put(4, v);
            break;
          case "topP":
            config.put(5, v);
            break;
          case "topK":
            config.put(6, v);
            break;
          case "responseMimeType":
            config.put(7, v);
            break;
          case "responseSchema":
          case "responseJsonSchema":
            config.put(8, schema((JSONObject) v, 0));
            break;
          case "seed":
            config.put(18, v);
            break;
          case "thinkingConfig":
            JSONObject t = (JSONObject) v;
            int level =
                java.util.Arrays.asList("", "LOW", "MEDIUM", "HIGH", "MINIMAL")
                    .indexOf(t.optString("thinkingLevel"));
            config.put(
                16,
                arr(t.opt("includeThoughts"), t.opt("thinkingBudget"), null, Math.max(0, level)));
            break;
          case "responseModalities":
            JSONArray values = (JSONArray) v, mapped = new JSONArray();
            for (int n = 0; n < values.length(); n++) {
              int code =
                  java.util.Arrays.asList("", "TEXT", "IMAGE", "AUDIO")
                      .indexOf(values.getString(n));
              if (code < 1) throw new IOException("输出类型无效");
              mapped.put(code);
            }
            config.put(14, mapped);
            break;
          default:
            throw new IOException("Playground 生成参数尚未支持");
        }
      }
    wire.put(3, trim(config));
    JSONArray tools = gemini.optJSONArray("tools"), mapped = new JSONArray();
    if (tools != null)
      for (int i = 0; i < tools.length(); i++) {
        JSONObject tool = tools.getJSONObject(i);
        if (tool.has("functionDeclarations")) {
          JSONArray declarations = tool.getJSONArray("functionDeclarations"),
              list = new JSONArray();
          for (int n = 0; n < declarations.length(); n++) {
            JSONObject f = declarations.getJSONObject(n);
            list.put(
                arr(
                    f.getString("name"),
                    f.opt("description"),
                    schema(
                        f.optJSONObject("parameters") == null
                            ? new JSONObject()
                            : f.getJSONObject("parameters"),
                        0)));
          }
          mapped.put(arr(null, list));
        } else if (tool.has("googleSearch"))
          mapped.put(arr(null, null, null, arr(null, arr(new JSONArray()))));
        else if (tool.has("codeExecution")) mapped.put(arr(new JSONArray()));
        else throw new IOException("Playground 工具尚未支持");
      }
    JSONObject tc = gemini.optJSONObject("toolConfig");
    if (tc != null) {
      String mode = tc.getJSONObject("functionCallingConfig").optString("mode");
      if ("NONE".equals(mode)) mapped = new JSONArray();
      else if (!"AUTO".equals(mode)) throw new IOException("Playground 强制工具选择尚未支持");
    }
    if (mapped.length() > 0) wire.put(6, mapped);
    op.put("body", wire);
    return op;
  }

  private static JSONArray encodeContent(
      JSONObject content, List<String> binding, List<String> prompt) throws Exception {
    JSONArray result = new JSONArray(), parts = content.getJSONArray("parts");
    for (int i = 0; i < parts.length(); i++) {
      JSONObject p = parts.getJSONObject(i);
      JSONArray v = new JSONArray();
      String bound = "";
      if (p.has("text")) {
        String text = p.getString("text");
        v.put(1, text);
        bound = text;
        prompt.add(text);
      }
      if (p.has("inlineData")) {
        JSONObject d = p.getJSONObject("inlineData");
        v.put(2, arr(d.getString("mimeType"), d.getString("data")));
        bound = d.getString("data");
      }
      if (p.has("thought")) v.put(12, p.get("thought"));
      if (p.has("thoughtSignature")) v.put(14, p.getString("thoughtSignature"));
      for (String key : new String[] {"functionCall", "functionResponse"})
        if (p.has(key)) {
          JSONObject f = p.getJSONObject(key);
          boolean call = "functionCall".equals(key);
          v.put(
              call ? 10 : 11,
              arr(
                  f.getString("name"),
                  struct(f.getJSONObject(call ? "args" : "response")),
                  f.opt("id")));
        }
      binding.add(bound);
      result.put(trim(v));
    }
    return arr(result, content.optString("role", "user"));
  }

  static JSONArray schema(JSONObject source, int depth) throws Exception {
    if (depth > 32) throw new IOException("Schema 层级过深");
    JSONArray wire = new JSONArray();
    String[] names = {
      "type",
      "format",
      "description",
      "nullable",
      "enum",
      "items",
      "properties",
      "required",
      "minProperties",
      "maxProperties",
      "minimum",
      "maximum",
      "minLength",
      "maxLength",
      "pattern"
    };
    for (String key : keys(source)) {
      Object value = source.get(key);
      int index = java.util.Arrays.asList(names).indexOf(key);
      switch (key) {
        case "$schema":
        case "title":
          continue;
        case "type":
          if (!(value instanceof String)) throw new IOException("联合类型 Schema 尚未支持");
          int type =
              java.util.Arrays.asList(
                      "", "string", "number", "integer", "boolean", "array", "object")
                  .indexOf(((String) value).toLowerCase(Locale.ROOT));
          if (type < 1) throw new IOException("Schema 类型无效");
          value = type;
          break;
        case "items":
          value = schema((JSONObject) value, depth + 1);
          break;
        case "properties":
          JSONArray properties = new JSONArray();
          JSONObject map = (JSONObject) value;
          for (String name : keys(map))
            properties.put(arr(name, schema(map.getJSONObject(name), depth + 1)));
          value = properties;
          break;
        case "maxItems":
          index = 20;
          break;
        case "minItems":
          index = 21;
          break;
        default:
          if (index < 0) throw new IOException("Playground Schema 约束尚未支持");
      }
      wire.put(index, value);
    }
    return trim(wire);
  }

  static JSONArray struct(JSONObject value) throws Exception {
    JSONArray entries = new JSONArray();
    for (String key : keys(value)) entries.put(arr(key, value(value.get(key))));
    return arr(entries);
  }

  private static JSONArray value(Object value) throws Exception {
    if (value == null || value == JSONObject.NULL) return arr(0);
    if (value instanceof Number) return arr(null, value);
    if (value instanceof String) return arr(null, null, value);
    if (value instanceof Boolean) return arr(null, null, null, value);
    if (value instanceof JSONObject) return arr(null, null, null, null, struct((JSONObject) value));
    JSONArray items = new JSONArray();
    for (int i = 0; i < ((JSONArray) value).length(); i++)
      items.put(value(((JSONArray) value).get(i)));
    return arr(null, null, null, null, null, arr(items));
  }

  static JSONObject fromStruct(JSONArray value, int depth) throws Exception {
    if (depth > 32) throw new IOException("工具响应层级过深");
    JSONObject result = new JSONObject();
    JSONArray entries = array(at(value, 0));
    for (int i = 0; i < entries.length(); i++) {
      JSONArray e = entries.getJSONArray(i);
      result.put(e.getString(0), fromValue(e.getJSONArray(1), depth + 1));
    }
    return result;
  }

  private static Object fromValue(JSONArray value, int depth) throws Exception {
    if (depth > 32) throw new IOException("工具响应层级过深");
    for (int i = 0; i < value.length(); i++) {
      if (value.isNull(i)) continue;
      switch (i) {
        case 0:
          return JSONObject.NULL;
        case 1:
        case 2:
        case 3:
          return value.get(i);
        case 4:
          return fromStruct(value.getJSONArray(i), depth + 1);
        case 5:
          JSONArray items = value.getJSONArray(i).getJSONArray(0), out = new JSONArray();
          for (int n = 0; n < items.length(); n++)
            out.put(fromValue(items.getJSONArray(n), depth + 1));
          return out;
        default:
          throw new IOException("工具响应类型无效");
      }
    }
    throw new IOException("工具响应值缺失");
  }

  static JSONObject playgroundFrame(JSONArray frame) throws Exception {
    JSONArray source = array(at(frame, 0)), candidates = new JSONArray();
    String[] reasons = {
      "",
      "STOP",
      "MAX_TOKENS",
      "SAFETY",
      "RECITATION",
      "OTHER",
      "BLOCKLIST",
      "PROHIBITED_CONTENT",
      "SPII",
      "MALFORMED_FUNCTION_CALL"
    };
    for (int i = 0; i < source.length(); i++) {
      JSONArray c = source.getJSONArray(i),
          content = array(at(c, 0)),
          parts = array(at(content, 0)),
          mapped = new JSONArray();
      for (int p = 0; p < parts.length(); p++) {
        JSONArray part = parts.getJSONArray(p);
        JSONObject m = new JSONObject();
        if (!part.isNull(1)) m.put("text", part.getString(1));
        if (part.optJSONArray(2) != null)
          m.put(
              "inlineData",
              obj(
                  "mimeType",
                  part.getJSONArray(2).getString(0),
                  "data",
                  part.getJSONArray(2).getString(1)));
        if (part.optBoolean(12) || part.optInt(12) == 1) m.put("thought", true);
        if (!part.isNull(14)) m.put("thoughtSignature", part.getString(14));
        JSONArray call = part.optJSONArray(10);
        if (call != null)
          m.put(
              "functionCall",
              obj("name", call.getString(0), "args", fromStruct(call.getJSONArray(1), 0)));
        if (m.length() > 0) mapped.put(m);
      }
      int reason = c.optInt(1);
      if (reason < 0 || reason >= reasons.length) throw new IOException("Playground 结束状态无效");
      candidates.put(
          obj(
              "index",
              i,
              "content",
              obj("role", "model", "parts", mapped),
              "finishReason",
              reasons[reason]));
    }
    JSONArray u = array(at(frame, 2));
    return obj(
        "candidates",
        candidates,
        "usageMetadata",
        obj(
            "promptTokenCount",
            u.optInt(0),
            "candidatesTokenCount",
            u.optInt(1),
            "totalTokenCount",
            u.optInt(2),
            "cachedContentTokenCount",
            u.optInt(3),
            "thoughtsTokenCount",
            u.optInt(9)));
  }

  static JSONObject message(JSONArray parts, boolean stream, int offset) throws Exception {
    StringBuilder text = new StringBuilder(), thoughts = new StringBuilder();
    JSONArray calls = new JSONArray();
    JSONObject result = obj("role", "assistant");
    for (int i = 0; i < parts.length(); i++) {
      JSONObject p = parts.getJSONObject(i);
      if (p.optBoolean("thought")) thoughts.append(p.optString("text"));
      else text.append(p.optString("text"));
      JSONObject media = p.optJSONObject("inlineData");
      if (media != null && media.optString("mimeType").startsWith("image/"))
        text.append("\n\n![image](data:")
            .append(media.getString("mimeType"))
            .append(";base64,")
            .append(media.getString("data"))
            .append(")");
      JSONObject f = p.optJSONObject("functionCall");
      if (f != null) {
        JSONObject call =
            obj(
                "id",
                "call-" + UUID.randomUUID(),
                "type",
                "function",
                "function",
                obj("name", f.getString("name"), "arguments", f.getJSONObject("args").toString()));
        if (stream) call.put("index", offset + calls.length());
        if (p.has("thoughtSignature"))
          call.put("thought_signature", p.getString("thoughtSignature"));
        calls.put(call);
      }
    }
    result.put("content", text.toString());
    if (thoughts.length() > 0) result.put("reasoning_content", thoughts.toString());
    if (calls.length() > 0) result.put("tool_calls", calls);
    return result;
  }

  static Object finish(String reason, boolean tools) {
    if (reason.isEmpty() || "FINISH_REASON_UNSPECIFIED".equals(reason)) return JSONObject.NULL;
    if (tools) return "tool_calls";
    if ("STOP".equals(reason)) return "stop";
    if ("MAX_TOKENS".equals(reason)) return "length";
    return "content_filter";
  }

  static JSONObject usage(JSONObject u) throws Exception {
    if (u == null) u = new JSONObject();
    return obj(
        "prompt_tokens",
        u.optInt("promptTokenCount"),
        "completion_tokens",
        u.optInt("candidatesTokenCount") + u.optInt("thoughtsTokenCount"),
        "total_tokens",
        u.optInt("totalTokenCount"),
        "prompt_tokens_details",
        obj("cached_tokens", u.optInt("cachedContentTokenCount")),
        "completion_tokens_details",
        obj("reasoning_tokens", u.optInt("thoughtsTokenCount")));
  }
}
