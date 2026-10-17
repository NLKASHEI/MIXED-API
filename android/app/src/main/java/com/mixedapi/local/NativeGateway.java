package com.mixedapi.local;

import android.content.Context;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import java.util.HashMap;
import java.util.Map;
import org.json.JSONArray;
import org.json.JSONObject;

/** Fixed provider endpoints. User-defined HTTP services retain their own mode. */
final class NativeGateway {
  interface Transport {
    GoogleSessions.Response send(
        String method, String url, Map<String, String> headers, byte[] body, long deadline)
        throws Exception;
  }

  private final Context context;
  private final Transport transport;

  NativeGateway(Context context, Transport transport) {
    this.context = context;
    this.transport = transport == null ? NativeHttp::send : transport;
  }

  JSONArray models(JSONObject backend) throws Exception {
    String id = backend.getString("id");
    if ("opencode".equals(id)) {
      try (GoogleSessions.Response response =
          transport.send(
              "GET",
              base(id) + "/models",
              providerHeaders(backend),
              null,
              System.currentTimeMillis() + 15000)) {
        if (response.status != 200) throw new IOException("OpenCode 模型目录请求失败");
        JSONArray rows = readJSON(response.body).getJSONArray("data"), ids = new JSONArray();
        for (int i = 0; i < rows.length(); i++) {
          String name = rows.getJSONObject(i).optString("id");
          if (!name.isEmpty()) ids.put(name);
        }
        return ids;
      }
    }
    if (!"vertex".equals(id)) throw new IOException("内置模型目录尚未完成");
    if (backend.optString("provider_key").trim().isEmpty())
      throw new IOException("请填写 Express API Key");
    try (InputStream input = context.getAssets().open("builtin-models.json")) {
      return new JSONObject(
              new String(BridgeHttpServer.bounded(input, 1024 * 1024), StandardCharsets.UTF_8))
          .getJSONArray(id);
    }
  }

  void serve(
      OutputStream out,
      String path,
      JSONObject backend,
      String publicModel,
      String model,
      JSONObject input)
      throws Exception {
    String id = backend.getString("id");
    if (!backend.optString("url").equals(base(id))) {
      BridgeHttpServer.fail(out, 400, "内置来源必须使用官方端点");
      return;
    }
    if ("opencode".equals(id)) {
      serveOpenCode(out, path, backend, publicModel, model, input);
      return;
    }
    if (!"vertex".equals(id) || !"/v1/chat/completions".equals(path)) {
      BridgeHttpServer.fail(out, 501, "该内置协议尚未完成");
      return;
    }
    if (backend.optString("provider_key").trim().isEmpty()) {
      BridgeHttpServer.fail(out, 401, "请填写本人的 Express API Key");
      return;
    }
    if (!model.matches("[A-Za-z0-9._-]+")) {
      BridgeHttpServer.fail(out, 400, "模型 ID 无效");
      return;
    }
    Map<String, String> headers = new HashMap<>();
    headers.put("x-goog-api-key", backend.getString("provider_key"));
    serveGemini(
        out,
        id,
        publicModel,
        input,
        base(id) + "/v1/publishers/google/models/" + model.replaceFirst("-search$", ""),
        headers);
  }

  private void serveGemini(
      OutputStream out,
      String id,
      String publicModel,
      JSONObject input,
      String modelURL,
      Map<String, String> headers)
      throws Exception {
    JSONObject packet;
    try {
      packet = GoogleProtocol.gemini(input);
    } catch (Exception e) {
      BridgeHttpServer.fail(out, 400, "请求参数无效或不支持");
      return;
    }
    boolean stream = input.optBoolean("stream");
    String endpoint = modelURL + (stream ? ":streamGenerateContent?alt=sse" : ":generateContent");
    GoogleGateway.Output result =
        new GoogleGateway.Output(out, id, publicModel, stream, input.optInt("n", 1));
    try (GoogleSessions.Response response =
        transport.send(
            "POST",
            endpoint,
            headers,
            packet.toString().getBytes(StandardCharsets.UTF_8),
            System.currentTimeMillis() + 300000)) {
      if (response.status != 200) {
        BridgeHttpServer.providerFailure(
            out,
            response.status >= 400 && response.status < 600 ? response.status : 502,
            response.retryAfter);
        return;
      }
      if (stream)
        NativeWire.events(
            response.body,
            data -> {
              if (!data.equals("[DONE]")) result.frame(new JSONObject(data));
            });
      else result.frame(readJSON(response.body));
      result.complete(stream, input.optJSONObject("stream_options"));
    } catch (Exception e) {
      if (e instanceof InterruptedException) Thread.currentThread().interrupt();
      if (Thread.currentThread().isInterrupted()) return;
      if (result.started) result.error();
      else BridgeHttpServer.fail(out, 502, "来源请求失败或生成未完成");
    }
  }

  private Map<String, String> providerHeaders(JSONObject backend) {
    Map<String, String> headers = new HashMap<>();
    String key = backend.optString("provider_key");
    if (!key.isEmpty()) headers.put("Authorization", "Bearer " + key);
    return headers;
  }

  private static JSONObject readJSON(InputStream body) throws Exception {
    return new JSONObject(
        new String(BridgeHttpServer.bounded(body, 32 * 1024 * 1024), StandardCharsets.UTF_8));
  }

  private void serveOpenCode(
      OutputStream out,
      String path,
      JSONObject backend,
      String publicModel,
      String model,
      JSONObject input)
      throws Exception {
    JSONObject target;
    try (InputStream asset = context.getAssets().open("opencode-models.json")) {
      target = readJSON(asset).optJSONObject(model);
    }
    if (target == null) {
      BridgeHttpServer.fail(out, 503, "目录未声明此模型的协议");
      return;
    }
    String protocol = target.getString("protocol"), base = target.getString("base");
    if (!java.util.Arrays.asList(
            "https://opencode.ai/inference/openai/v1",
            "https://opencode.ai/inference/systemone/v1",
            "https://opencode.ai/inference/anthropic/v1",
            "https://opencode.ai/inference/google/v1beta")
        .contains(base)) {
      BridgeHttpServer.fail(out, 503, "模型目录端点无效");
      return;
    }
    if (protocol.equals("gemini") && path.equals("/v1/chat/completions")) {
      if (!model.matches("[A-Za-z0-9._-]+")) {
        BridgeHttpServer.fail(out, 400, "模型 ID 无效");
        return;
      }
      serveGemini(
          out, "opencode", publicModel, input, base + "/models/" + model, providerHeaders(backend));
      return;
    }
    boolean converted =
        path.equals("/v1/chat/completions")
            && (protocol.equals("responses") || protocol.equals("messages"));
    if ((!path.equals("/v1/" + protocol) && !converted)
        || (input.optBoolean("stream") && !protocol.equals("chat/completions") && !converted)) {
      BridgeHttpServer.fail(out, 501, "此协议转换尚未完成");
      return;
    }
    JSONObject packet = new JSONObject(input.toString()).put("model", model);
    if (converted) {
      try {
        packet =
            protocol.equals("responses")
                ? NativeResponses.request(packet)
                : NativeMessages.request(packet);
      } catch (Exception e) {
        BridgeHttpServer.fail(out, 400, "请求参数无法转换为 Responses");
        return;
      }
    }
    boolean stream = input.optBoolean("stream");
    JSONObject options = input.optJSONObject("stream_options");
    NativeChat chat =
        new NativeChat(
            out,
            "opencode",
            publicModel,
            stream,
            options != null && options.optBoolean("include_usage"),
            input.optInt("n", 1));
    Map<String, String> headers = providerHeaders(backend);
    if (protocol.equals("messages")) headers.put("anthropic-version", "2023-06-01");
    try (GoogleSessions.Response response =
        transport.send(
            "POST",
            base + "/" + protocol,
            headers,
            packet.toString().getBytes(StandardCharsets.UTF_8),
            System.currentTimeMillis() + 300000)) {
      if (response.status != 200) {
        BridgeHttpServer.providerFailure(
            out,
            response.status >= 400 && response.status < 600 ? response.status : 502,
            response.retryAfter);
        return;
      }
      if (stream) {
        if (converted && protocol.equals("messages"))
          NativeMessagesStream.read(response.body, chat);
        else if (converted) NativeResponses.read(response.body, chat);
        else chat.read(response.body);
        return;
      }
      JSONObject result = readJSON(response.body);
      if (result.has("error")) {
        BridgeHttpServer.fail(out, 502, "服务方生成失败");
        return;
      }
      BridgeHttpServer.json(
          out,
          200,
          converted
              ? (protocol.equals("responses")
                  ? NativeResponses.result(result, publicModel)
                  : NativeMessages.result(result, publicModel))
              : result.put("model", publicModel),
          "opencode");
    } catch (Exception e) {
      if (Thread.currentThread().isInterrupted()) return;
      chat.error();
    }
  }

  static String base(String id) {
    switch (id) {
      case "vertex":
        return "https://aiplatform.googleapis.com";
      case "opencode":
        return "https://opencode.ai/inference/v1";
      case "codex":
        return "https://chatgpt.com/backend-api/codex";
      case "workbuddy":
        return "https://copilot.tencent.com/v2";
      case "aps":
      case "aistudio":
        return GoogleGateway.base(id);
      default:
        return null;
    }
  }
}
