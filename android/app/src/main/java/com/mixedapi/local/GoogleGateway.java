package com.mixedapi.local;

import android.content.Context;
import java.io.IOException;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import java.util.HashSet;
import java.util.Map;
import java.util.Set;
import java.util.TreeMap;
import java.util.UUID;
import org.json.JSONArray;
import org.json.JSONObject;

/** Android public API adapter; the browser owns only official-site session I/O. */
final class GoogleGateway {
  interface Transport {
    void open(Context context, String id) throws Exception;

    JSONObject state(String id) throws Exception;

    GoogleSessions.Response send(String id, JSONObject operation, long deadline) throws Exception;
  }

  private final Transport sessions;
  private volatile Map<String, Boolean> buildUnary = java.util.Collections.emptyMap();

  GoogleGateway(Transport sessions) {
    this.sessions = sessions;
  }

  static boolean source(String id) {
    return "aps".equals(id) || "aistudio".equals(id);
  }

  static String base(String id) {
    return "aps".equals(id)
        ? "https://cloudconsole-pa.clients6.google.com"
        : "https://alkalimakersuite-pa.clients6.google.com";
  }

  JSONObject state(String id) throws Exception {
    return sessions.state(id);
  }

  void open(Context context, String id) throws Exception {
    sessions.open(context, id);
  }

  private GoogleSessions.Response send(String id, JSONObject op, long deadline, int retries)
      throws Exception {
    for (int attempt = 0; ; attempt++) {
      if (Thread.currentThread().isInterrupted() || System.currentTimeMillis() >= deadline)
        throw new IOException("来源请求超时或取消");
      GoogleSessions.Response response = sessions.send(id, op, deadline);
      if (attempt >= retries || (response.status != 429 && response.status != 503)) return response;
      long wait = (1L << Math.min(attempt, 5)) * 1000;
      try {
        long seconds = Long.parseLong(response.retryAfter);
        if (seconds >= 0) wait = Math.min(seconds, 86400) * 1000;
      } catch (NumberFormatException e) {
        try {
          wait =
              Math.max(
                  0,
                  java.time.ZonedDateTime.parse(
                              response.retryAfter,
                              java.time.format.DateTimeFormatter.RFC_1123_DATE_TIME)
                          .toInstant()
                          .toEpochMilli()
                      - System.currentTimeMillis());
        } catch (java.time.DateTimeException ignored) {
        }
      }
      response.close();
      long remaining = deadline - System.currentTimeMillis();
      if (remaining <= 0) throw new IOException("来源请求超时");
      Thread.sleep(Math.min(wait, remaining));
    }
  }

  JSONArray models(String id) throws Exception {
    if (!"ready".equals(state(id).optString("status")))
      throw new IOException("请在来源窗口完成本人授权并发送测试消息");
    JSONArray models = new JSONArray();
    if ("aps".equals(id)) {
      for (String name :
          new String[] {
            "gemini-2.5-flash",
            "gemini-2.5-flash-lite",
            "gemini-2.5-pro",
            "gemini-3-flash-preview",
            "gemini-3.1-pro-preview",
            "gemini-3.1-flash-lite",
            "gemini-3.1-flash-image",
            "gemini-3.5-flash"
          }) models.put(name);
      return models;
    }
    long deadline = System.currentTimeMillis() + 15000;
    Map<String, Boolean> unary = new java.util.HashMap<>();
    try (GoogleSessions.Response response =
        sessions.send(id, GoogleProtocol.obj("kind", "models"), deadline)) {
      if (response.status != 200) throw new IOException("AIS 模型目录请求失败");
      GoogleWire p = new GoogleWire(response.body);
      JSONArray root = (JSONArray) p.value(p.next());
      if (p.next() != -1) throw new IOException("模型目录有额外内容");
      JSONArray rows = root.getJSONArray(0);
      Set<String> seen = new HashSet<>();
      for (int i = 0; i < rows.length(); i++) {
        JSONArray row = rows.getJSONArray(i);
        String name = row.optString(0).replaceFirst("^models/", "");
        if (!name.matches("[A-Za-z0-9._-]+") || !row.isNull(78) || !seen.add(name)) continue;
        JSONArray methods = row.optJSONArray(7);
        if (methods != null)
          for (int n = 0; n < methods.length(); n++)
            if ("generateContent".equals(methods.getString(n))) {
              models.put("playground/" + name);
              JSONArray modes = row.optJSONArray(82);
              unary.put(name, modes != null && modes.length() > 0);
              break;
            }
      }
    }
    String token = "";
    Set<String> seen = new HashSet<>();
    for (int page = 0; page < 10; page++) {
      try (GoogleSessions.Response response =
          sessions.send(
              id, GoogleProtocol.obj("kind", "build_models", "page_token", token), deadline)) {
        if (response.status != 200) throw new IOException("Build 模型目录请求失败");
        GoogleWire p = new GoogleWire(response.body);
        JSONArray root = (JSONArray) p.value(p.next());
        if (p.next() != -1) throw new IOException("Build 模型目录有额外内容");
        JSONObject catalog = new JSONObject(GoogleWire.proxyBody(root));
        JSONArray entries = catalog.getJSONArray("models");
        for (int i = 0; i < entries.length(); i++) {
          JSONObject m = entries.getJSONObject(i);
          String name = m.optString("name").replaceFirst("^models/", "");
          JSONArray methods = m.optJSONArray("supportedGenerationMethods");
          if (!name.matches("[A-Za-z0-9._-]+") || methods == null || !seen.add(name)) continue;
          for (int n = 0; n < methods.length(); n++)
            if ("generateContent".equals(methods.getString(n))) {
              models.put("build/" + name);
              break;
            }
        }
        String next = catalog.optString("nextPageToken");
        if (next.isEmpty()) {
          buildUnary = java.util.Collections.unmodifiableMap(unary);
          return models;
        }
        if (next.equals(token)) throw new IOException("Build 模型分页重复");
        token = next;
      }
    }
    throw new IOException("Build 模型分页超限");
  }

  void serve(
      OutputStream out,
      String path,
      JSONObject backend,
      String publicModel,
      String model,
      JSONObject input,
      JSONObject settings)
      throws Exception {
    String id = backend.getString("id");
    if (!base(id).equals(backend.optString("url"))) {
      BridgeHttpServer.fail(out, 400, "内置来源端点无效");
      return;
    }
    if (!"/v1/chat/completions".equals(path)) {
      String selectedModel = model;
      ProtocolBridge.serve(
          out,
          path,
          id,
          publicModel,
          input,
          (target, chat) ->
              serve(
                  target,
                  "/v1/chat/completions",
                  backend,
                  publicModel,
                  selectedModel,
                  chat,
                  settings));
      return;
    }
    String kind = id;
    if ("aistudio".equals(id)) {
      int slash = model.indexOf('/');
      if (slash < 1) {
        BridgeHttpServer.fail(out, 400, "AIS 模型需要 build/ 或 playground/ 前缀");
        return;
      }
      kind = model.substring(0, slash);
      model = model.substring(slash + 1);
      if (!"build".equals(kind) && !"playground".equals(kind)) {
        BridgeHttpServer.fail(out, 400, "AIS 能力名称无效");
        return;
      }
    }
    JSONObject op;
    try {
      op = GoogleProtocol.operation(kind, model, GoogleProtocol.gemini(input));
    } catch (Exception e) {
      BridgeHttpServer.fail(out, 400, "请求参数无效或该能力尚不支持，请检查配置指引");
      return;
    }
    if ("build".equals(kind))
      op.put("unary", !input.optBoolean("stream") || buildUnary.getOrDefault(model, true));
    if (op.optBoolean("unary")) kind = "build_unary";
    int seconds = settings == null ? 300 : settings.optInt("request_timeout_seconds", 300);
    long deadline = System.currentTimeMillis() + seconds * 1000L;
    boolean stream = input.optBoolean("stream"),
        aggregate = settings != null && settings.optBoolean("aggregate_stream");
    Output result = new Output(out, id, publicModel, stream && !aggregate, input.optInt("n", 1));
    try (GoogleSessions.Response response =
        send(id, op, deadline, settings == null ? 0 : settings.optInt("max_retries"))) {
      if (response.status != 200) {
        BridgeHttpServer.providerFailure(
            out,
            response.status >= 400 && response.status < 600 ? response.status : 502,
            response.retryAfter);
        return;
      }
      GoogleWire.decode(kind, response.body, result::frame);
      result.complete(stream, input.optJSONObject("stream_options"));
    } catch (Exception e) {
      if (e instanceof InterruptedException) Thread.currentThread().interrupt();
      if (Thread.currentThread().isInterrupted()) return;
      if (result.started) result.error();
      else BridgeHttpServer.fail(out, 502, "来源生成未完成，请检查官方页面授权和网络");
    }
  }

  static final class Output {
    final OutputStream out;
    final String backend, model, id = "chatcmpl-" + UUID.randomUUID();
    final boolean incremental;
    final int expected;
    final long created = System.currentTimeMillis() / 1000;
    final Map<Integer, JSONArray> parts = new TreeMap<>();
    final Map<Integer, String> finishes = new TreeMap<>();
    final Map<Integer, Integer> offsets = new TreeMap<>();
    final Set<Integer> calls = new HashSet<>();
    JSONObject usage = new JSONObject();
    boolean started;
    long size;

    Output(OutputStream out, String backend, String model, boolean incremental, int expected) {
      this.out = out;
      this.backend = backend;
      this.model = model;
      this.incremental = incremental;
      this.expected = expected;
    }

    void frame(JSONObject frame) throws Exception {
      if (frame.has("error") && !frame.isNull("error")) throw new IOException("服务方生成失败");
      JSONObject u = frame.optJSONObject("usageMetadata");
      if (u != null && u.optInt("totalTokenCount") > 0) usage = u;
      JSONArray cs = frame.optJSONArray("candidates"), choices = new JSONArray();
      if (cs == null) return;
      for (int i = 0; i < cs.length(); i++) {
        JSONObject c = cs.getJSONObject(i);
        int index = c.optInt("index");
        if (index < 0 || index >= expected) throw new IOException("候选索引无效");
        JSONObject content = c.optJSONObject("content");
        JSONArray p = content == null ? new JSONArray() : content.optJSONArray("parts");
        if (p == null) p = new JSONArray();
        if (!incremental) {
          size += p.toString().getBytes(StandardCharsets.UTF_8).length;
          if (size > 32 * 1024 * 1024) throw new IOException("生成内容过大");
          JSONArray stored = parts.computeIfAbsent(index, k -> new JSONArray());
          for (int n = 0; n < p.length(); n++) stored.put(p.get(n));
        }
        JSONObject message = GoogleProtocol.message(p, true, offsets.getOrDefault(index, 0));
        JSONArray tools = message.optJSONArray("tool_calls");
        if (tools != null) {
          calls.add(index);
          offsets.put(index, offsets.getOrDefault(index, 0) + tools.length());
        }
        String finish = c.optString("finishReason");
        if (!finish.isEmpty() && !"FINISH_REASON_UNSPECIFIED".equals(finish))
          finishes.put(index, finish);
        choices.put(
            GoogleProtocol.obj("index", index, "delta", message, "finish_reason", JSONObject.NULL));
      }
      if (incremental && choices.length() > 0) event(chunk(choices));
    }

    JSONObject chunk(JSONArray choices) throws Exception {
      return GoogleProtocol.obj(
          "id",
          id,
          "object",
          "chat.completion.chunk",
          "created",
          created,
          "model",
          model,
          "choices",
          choices);
    }

    void complete(boolean stream, JSONObject options) throws Exception {
      if (finishes.size() != expected) throw new IOException("来源响应在完成前中断");
      JSONArray choices = new JSONArray();
      for (int index : finishes.keySet()) {
        JSONArray p = parts.getOrDefault(index, new JSONArray());
        choices.put(
            GoogleProtocol.obj(
                "index",
                index,
                stream ? "delta" : "message",
                incremental ? new JSONObject() : GoogleProtocol.message(p, stream, 0),
                "finish_reason",
                GoogleProtocol.finish(finishes.get(index), calls.contains(index))));
      }
      if (stream) {
        event(chunk(choices));
        if (options != null && options.optBoolean("include_usage"))
          event(chunk(new JSONArray()).put("usage", GoogleProtocol.usage(usage)));
        write("data: [DONE]\n\n");
      } else
        BridgeHttpServer.json(
            out,
            200,
            GoogleProtocol.obj(
                "id",
                id,
                "object",
                "chat.completion",
                "created",
                created,
                "model",
                model,
                "choices",
                choices,
                "usage",
                GoogleProtocol.usage(usage)),
            backend);
    }

    void event(JSONObject value) throws Exception {
      if (!started) {
        out.write(
            ("HTTP/1.1 200 OK\r\n"
                    + "Content-Type: text/event-stream\r\n"
                    + "Cache-Control: no-store\r\n"
                    + "Connection: close\r\n"
                    + "X-Mixed-Backend: "
                    + backend
                    + "\r\n\r\n")
                .getBytes(StandardCharsets.US_ASCII));
        started = true;
      }
      write("data: " + value + "\n\n");
    }

    void write(String value) throws IOException {
      out.write(value.getBytes(StandardCharsets.UTF_8));
      out.flush();
    }

    void error() throws Exception {
      event(GoogleProtocol.obj("error", GoogleProtocol.obj("message", "来源流响应在完成前中断")));
    }
  }
}
