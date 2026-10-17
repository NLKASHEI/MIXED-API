package com.mixedapi.local;

import android.content.Context;
import android.util.Log;
import java.io.BufferedInputStream;
import java.io.BufferedOutputStream;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.util.LinkedHashMap;
import java.util.Locale;
import java.util.Map;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicBoolean;
import org.json.JSONArray;
import org.json.JSONObject;

/**
 * Small Android loopback HTTP/1.1 reverse proxy. Uses no WebView fetch bridge, so responses are
 * usable by any Android app that connects to 127.0.0.1. Only supports Content-Length request
 * bodies. No HTTP/2, multipart or WebSocket.
 */
public final class BridgeHttpServer implements AutoCloseable {
  private final Context context;
  private final GoogleGateway google;
  private final NativeGateway nativeGateway;
  private final NativeAccounts accounts;
  private final ExecutorService pool = Executors.newFixedThreadPool(12);
  private final ExecutorService discoveryPool = Executors.newFixedThreadPool(6);
  private final AtomicBoolean open = new AtomicBoolean(false);
  private final java.util.concurrent.ConcurrentHashMap<String, String> sourceState =
      new java.util.concurrent.ConcurrentHashMap<>();
  private ServerSocket socket;
  private Thread acceptor;
  // Guarded by this, including peers waiting in the executor queue.
  private final java.util.Set<Socket> peers = new java.util.HashSet<>();
  private static final int MAX_BODY = 32 * 1024 * 1024;

  public BridgeHttpServer(Context ctx) {
    this(ctx, GoogleSessions.shared());
  }

  BridgeHttpServer(Context ctx, GoogleGateway.Transport sessions) {
    this(ctx, sessions, null);
  }

  BridgeHttpServer(
      Context ctx, GoogleGateway.Transport sessions, NativeGateway.Transport transport) {
    this(ctx, sessions, transport, null);
  }

  BridgeHttpServer(
      Context ctx,
      GoogleGateway.Transport sessions,
      NativeGateway.Transport transport,
      AccountStore.Keys keys) {
    context = ctx.getApplicationContext();
    google = new GoogleGateway(sessions);
    accounts = new NativeAccounts(new AccountStore(context, keys), transport);
    nativeGateway = new NativeGateway(context, transport, accounts);
  }

  public synchronized void start() throws Exception {
    if (open.get()) return;
    BridgeConfig.load(context);
    socket = new ServerSocket();
    try {
      socket.setReuseAddress(true);
      socket.bind(new java.net.InetSocketAddress(InetAddress.getByName("127.0.0.1"), 8765), 50);
    } catch (Exception e) {
      socket.close();
      throw e;
    }
    open.set(true);
    acceptor =
        new Thread(
            () -> {
              while (open.get()) {
                try {
                  Socket connection = socket.accept();
                  synchronized (BridgeHttpServer.this) {
                    if (!open.get()) connection.close();
                    else {
                      peers.add(connection);
                      pool.execute(() -> process(connection));
                    }
                  }
                } catch (Exception ex) {
                  if (open.get()) Log.e("MIXED-API", "Accept failed");
                }
              }
            },
            "MixedApiAccept");
    acceptor.setDaemon(true);
    acceptor.start();
  }

  @Override
  public synchronized void close() {
    open.set(false);
    try {
      if (socket != null) socket.close();
    } catch (Exception ignored) {
    }
    for (Socket peer : peers) {
      try {
        peer.close();
      } catch (IOException ignored) {
      }
    }
    peers.clear();
    pool.shutdownNow();
    discoveryPool.shutdownNow();
    accounts.close();
  }

  static byte[] bounded(InputStream in, int max) throws IOException {
    ByteArrayOutputStream b = new ByteArrayOutputStream();
    byte[] tmp = new byte[8192];
    int n;
    while ((n = in.read(tmp)) != -1) {
      if (b.size() + n > max) throw new IOException("Response too large");
      b.write(tmp, 0, n);
    }
    return b.toByteArray();
  }

  private static String line(BufferedInputStream in) throws IOException {
    ByteArrayOutputStream b = new ByteArrayOutputStream();
    int c;
    while ((c = in.read()) != -1) {
      if (c == '\n') break;
      if (c != '\r') b.write(c);
      if (b.size() > 16384) throw new IOException("Header too long");
    }
    if (c == -1 && b.size() == 0) return null;
    return b.toString("UTF-8");
  }

  private static byte[] read(BufferedInputStream in, int len) throws IOException {
    byte[] result = new byte[len];
    int pos = 0;
    while (pos < len) {
      int n = in.read(result, pos, len - pos);
      if (n < 0) throw new IOException("Unexpected EOF");
      pos += n;
    }
    return result;
  }

  private static String status(int code) {
    switch (code) {
      case 200:
        return "OK";
      case 201:
        return "Created";
      case 400:
        return "Bad Request";
      case 401:
        return "Unauthorized";
      case 404:
        return "Not Found";
      case 405:
        return "Method Not Allowed";
      case 411:
        return "Length Required";
      case 413:
        return "Payload Too Large";
      case 422:
        return "Unprocessable Entity";
      case 500:
        return "Internal Server Error";
      case 501:
        return "Not Implemented";
      case 502:
        return "Bad Gateway";
      case 503:
        return "Service Unavailable";
      default:
        return "Status";
    }
  }

  private static void response(OutputStream out, int code, String type, byte[] body)
      throws IOException {
    response(out, code, type, body, "");
  }

  private static void response(OutputStream out, int code, String type, byte[] body, String backend)
      throws IOException {
    response(out, code, type, body, backend, "");
  }

  private static void response(
      OutputStream out, int code, String type, byte[] body, String backend, String retryAfter)
      throws IOException {
    String retry =
        retryAfter.length() <= 128 && retryAfter.matches("[A-Za-z0-9 ,:+-]+")
            ? "Retry-After: " + retryAfter + "\r\n"
            : "";
    String head =
        "HTTP/1.1 "
            + code
            + " "
            + status(code)
            + "\r\nContent-Type: "
            + type
            + "\r\nContent-Length: "
            + body.length
            + "\r\n"
            + "Connection: close\r\n"
            + "Cache-Control: no-store\r\n"
            + "X-Content-Type-Options: nosniff\r\n"
            + (backend.isEmpty() ? "" : "X-Mixed-Backend: " + backend + "\r\n")
            + retry
            + "\r\n";
    out.write(head.getBytes(StandardCharsets.UTF_8));
    out.write(body);
    out.flush();
  }

  static void json(OutputStream out, int status, JSONObject data) throws IOException {
    response(
        out,
        status,
        "application/json; charset=utf-8",
        data.toString().getBytes(StandardCharsets.UTF_8));
  }

  static void json(OutputStream out, int status, JSONObject data, String backend)
      throws IOException {
    response(
        out,
        status,
        "application/json; charset=utf-8",
        data.toString().getBytes(StandardCharsets.UTF_8),
        backend);
  }

  static void fail(OutputStream out, int status, String message) throws IOException {
    try {
      json(
          out,
          status,
          new JSONObject()
              .put(
                  "error",
                  new JSONObject().put("message", message).put("type", "mixed_api_error")));
    } catch (Exception e) {
      response(out, status, "text/plain; charset=utf-8", message.getBytes(StandardCharsets.UTF_8));
    }
  }

  static void providerFailure(OutputStream out, int status, String retryAfter) throws Exception {
    JSONObject data =
        GoogleProtocol.obj(
            "error",
            GoogleProtocol.obj("message", "服务方拒绝请求，请检查官方页面授权、权限或额度", "type", "mixed_api_error"));
    response(
        out,
        status,
        "application/json; charset=utf-8",
        data.toString().getBytes(StandardCharsets.UTF_8),
        "",
        retryAfter);
  }

  private static boolean authorized(Map<String, String> headers, String key) {
    String value = headers.getOrDefault("authorization", "");
    if (value.startsWith("Bearer ")) value = value.substring(7);
    else value = headers.getOrDefault("x-admin-key", "");
    return key != null
        && java.security.MessageDigest.isEqual(
            value.getBytes(StandardCharsets.UTF_8), key.getBytes(StandardCharsets.UTF_8));
  }

  private static GoogleSessions.Response connection(
      JSONObject b,
      String path,
      String method,
      byte[] body,
      Map<String, String> additionalHeaders,
      long timeout)
      throws Exception {
    URI base = new URI(b.getString("url"));
    URI dst =
        new URI(
            base.getScheme(),
            null,
            base.getHost(),
            base.getPort(),
            base.getPath().replaceAll("/+$", "") + path,
            null,
            null);
    Map<String, String> headers = new LinkedHashMap<>(additionalHeaders);
    String key = b.optString("key"), auth = b.optString("auth");
    if (!key.isEmpty()) {
      if ("bearer".equals(auth)) headers.put("Authorization", "Bearer " + key);
      else if ("x-api-key".equals(auth)) headers.put("x-api-key", key);
    }
    headers.put("Accept", "application/json, text/event-stream");
    return NativeHttp.send(
        method, dst.toString(), headers, body, System.currentTimeMillis() + timeout);
  }

  private static JSONArray enabled(JSONObject config) throws Exception {
    return config.getJSONArray("backends");
  }

  /**
   * After Content-Length has been read, this connection expects no more input. Interrupt the
   * request owner on disconnect, including while waiting for a source lease.
   */
  private static final class ClientDisconnect implements AutoCloseable {
    private boolean active = true;

    ClientDisconnect(Socket socket, InputStream input) throws IOException {
      socket.setSoTimeout(250);
      Thread owner = Thread.currentThread();
      Thread monitor =
          new Thread(
              () -> {
                for (; ; ) {
                  synchronized (this) {
                    if (!active) return;
                  }
                  try {
                    input.read();
                  } catch (java.net.SocketTimeoutException e) {
                    continue;
                  } catch (IOException ignored) {
                  }
                  synchronized (this) {
                    if (active) owner.interrupt();
                  }
                  return;
                }
              },
              "MixedApiDisconnect");
      monitor.setDaemon(true);
      monitor.start();
    }

    @Override
    public synchronized void close() {
      active = false;
      Thread.interrupted();
    }
  }

  private void process(Socket peer) {
    try (Socket s = peer) {
      s.setSoTimeout(20000);
      BufferedInputStream in = new BufferedInputStream(s.getInputStream());
      BufferedOutputStream out = new BufferedOutputStream(s.getOutputStream());
      String first = line(in);
      if (first == null) return;
      String[] request = first.split(" ");
      if (request.length < 3) {
        fail(out, 400, "Invalid request line");
        return;
      }
      String method = request[0], path = request[1];
      Map<String, String> headers = new LinkedHashMap<>();
      String l;
      while ((l = line(in)) != null && !l.isEmpty()) {
        int index = l.indexOf(':');
        if (index > 0)
          headers.put(
              l.substring(0, index).trim().toLowerCase(Locale.ROOT), l.substring(index + 1).trim());
        if (headers.size() > 100) {
          fail(out, 400, "Too many headers");
          return;
        }
      }
      if ("chunked".equalsIgnoreCase(headers.getOrDefault("transfer-encoding", ""))) {
        fail(out, 411, "Chunked request bodies not supported");
        return;
      }
      int len = 0;
      try {
        len = Integer.parseInt(headers.getOrDefault("content-length", "0"));
      } catch (NumberFormatException e) {
        fail(out, 400, "Invalid content length");
        return;
      }
      if (len < 0 || len > MAX_BODY) {
        fail(out, 413, "Body too large");
        return;
      }
      byte[] body = read(in, len);
      JSONObject config = BridgeConfig.load(context);
      if (("/".equals(path) || "/admin".equals(path)) && "GET".equals(method)) {
        try (InputStream asset = context.getAssets().open("index.html")) {
          response(out, 200, "text/html; charset=utf-8", bounded(asset, 2 * 1024 * 1024));
        }
        return;
      }
      if ("/licenses".equals(path) && "GET".equals(method)) {
        try (InputStream asset = context.getAssets().open("notices.txt")) {
          response(out, 200, "text/plain; charset=utf-8", bounded(asset, 1024 * 1024));
        }
        return;
      }
      if ("/health".equals(path)) {
        json(out, 200, new JSONObject().put("ok", true).put("service", "MIXED-API"));
        return;
      }
      if (path.startsWith("/api/")) {
        if (!authorized(headers, config.getString("admin_key"))) {
          fail(out, 401, "Admin key required");
          return;
        }
        admin(out, method, path, body, config);
        return;
      }
      if (!path.startsWith("/v1/")) {
        fail(out, 404, "Not found");
        return;
      }
      if (!authorized(headers, config.getString("api_key"))) {
        fail(out, 401, "Invalid MIXED-API key");
        return;
      }
      if ("/v1/models".equals(path) && "GET".equals(method)) {
        models(out, config);
        return;
      }
      if ("POST".equals(method)
          && ("/v1/chat/completions".equals(path)
              || "/v1/responses".equals(path)
              || "/v1/embeddings".equals(path)
              || "/v1/messages".equals(path)
              || "/v1/messages/count_tokens".equals(path))) {
        try (ClientDisconnect cancelled = new ClientDisconnect(s, in)) {
          forward(out, path, body, config, headers);
        }
        return;
      }
      fail(out, 404, "Unsupported endpoint");
    } catch (Exception ex) {
      Log.e("MIXED-API", "Request failed: " + ex.getClass().getSimpleName());
    } finally {
      synchronized (this) {
        peers.remove(peer);
      }
    }
  }

  private boolean googleReady(String id) {
    try {
      return "ready".equals(google.state(id).optString("status"));
    } catch (Exception e) {
      if (e instanceof InterruptedException) Thread.currentThread().interrupt();
      return false;
    }
  }

  private boolean sourceReady(JSONObject backend) throws Exception {
    String id = backend.getString("id");
    if (GoogleGateway.source(id)) return googleReady(id);
    if (NativeAccounts.source(id))
      return "authorized".equals(accounts.state(id).optString("status"));
    return id.equals("opencode")
        || id.equals("vertex") && !backend.optString("provider_key").trim().isEmpty();
  }

  private static String setupHelp(String id) {
    if (id.equals("vertex")) return "请先填写本人的 Express API Key";
    if (GoogleGateway.source(id)) return "请先在来源窗口完成本人授权并发送测试消息";
    return "请先完成本人账号授权";
  }

  private void admin(OutputStream out, String method, String path, byte[] body, JSONObject config)
      throws Exception {
    if (path.startsWith("/api/accounts/")) {
      String[] segments = path.substring("/api/accounts/".length()).split("/", -1);
      String id = segments[0];
      if (NativeAccounts.source(id) && segments.length <= 2) {
        accounts.admin(out, id, segments.length == 2 ? segments[1] : "", method, body);
        return;
      }
      if (!GoogleGateway.source(id)) {
        fail(out, 501, "此账号授权尚未在 Android 内置");
        return;
      }
      try {
        if (segments.length == 1 && "GET".equals(method)) {
          json(out, 200, google.state(id));
          return;
        }
        if (segments.length == 2 && "login".equals(segments[1]) && "POST".equals(method)) {
          google.open(context, id);
          json(
              out,
              200,
              GoogleProtocol.obj("status", "waiting", "message", "请在官方页面完成本人授权并手动发送测试消息"));
          return;
        }
      } catch (Exception e) {
        fail(out, 503, "来源窗口未就绪；请确认系统 WebView 支持隔离会话，再打开授权步骤");
        return;
      }
      fail(out, 405, "不支持此账号操作");
      return;
    }
    if (path.startsWith("/api/assist/") && "POST".equals(method)) {
      String[] segments = path.substring("/api/assist/".length()).split("/", -1);
      if (segments.length != 2 || NativeGateway.base(segments[0]) == null) {
        fail(out, 501, "此来源尚未在 Android 内置");
        return;
      }
      String id = segments[0], action = segments[1];
      JSONArray bs = config.getJSONArray("backends");
      JSONObject b = null;
      for (int i = 0; i < bs.length(); i++)
        if (id.equals(bs.getJSONObject(i).optString("id"))) {
          b = bs.getJSONObject(i);
          break;
        }
      if (b == null) {
        fail(out, 404, "来源不存在");
        return;
      }
      if ("prepare".equals(action)) {
        if (!"builtin".equals(b.optString("kind"))) b.put("enabled", false);
        b.put("kind", "builtin")
            .put("url", NativeGateway.base(id))
            .put("auth", "none")
            .put("key", "")
            .put("command", "")
            .put("workdir", "");
      } else if ("activate".equals(action)) {
        if (!"builtin".equals(b.optString("kind")) || !sourceReady(b)) {
          fail(out, 400, setupHelp(id));
          return;
        }
        b.put("enabled", true);
      } else {
        fail(out, 404, "未知来源操作");
        return;
      }
      BridgeConfig.save(context, config);
      sourceState.put("setup:" + id, "installed: 内置核心就绪");
      json(out, 200, GoogleProtocol.obj("ok", true));
      return;
    }
    if ("/api/engine-settings/aps".equals(path)) {
      JSONObject settings = config.optJSONObject("engine_settings");
      if (settings == null) settings = new JSONObject();
      if ("GET".equals(method)) {
        JSONObject value = settings.optJSONObject("aps");
        json(
            out,
            200,
            value == null
                ? GoogleProtocol.obj(
                    "max_retries", 0, "request_timeout_seconds", 300, "aggregate_stream", false)
                : value);
        return;
      }
      if ("PUT".equals(method)) {
        try {
          JSONObject value = new JSONObject(new String(body, StandardCharsets.UTF_8));
          settings.put("aps", value);
          config.put("engine_settings", settings);
          BridgeConfig.save(context, config);
          json(out, 200, GoogleProtocol.obj("saved", true));
        } catch (Exception e) {
          fail(out, 400, "请求设置无效");
        }
        return;
      }
      fail(out, 405, "Use GET or PUT");
      return;
    }
    if ("/api/config".equals(path) && "GET".equals(method)) {
      json(out, 200, config);
      return;
    }
    if ("/api/assist/status".equals(path) && "GET".equals(method)) {
      JSONArray sources = GoogleProtocol.arr("vertex", "codex", "opencode", "workbuddy");
      if (GoogleSessions.supported()) sources.put("aps").put("aistudio");
      json(
          out,
          200,
          new JSONObject()
              .put("platform", "android")
              .put("native_codex_supported", true)
              .put("builtin_backends", sources));
      return;
    }
    if ("/api/upstreams".equals(path) && "GET".equals(method)) {
      try (InputStream asset = context.getAssets().open("upstreams.json")) {
        response(out, 200, "application/json; charset=utf-8", bounded(asset, 1024 * 1024));
      }
      return;
    }
    if ("/api/logs".equals(path)) {
      if ("DELETE".equals(method)) BridgeLogs.clear();
      json(out, 200, new JSONObject().put("text", BridgeLogs.text()));
      return;
    }
    if ("/api/local/import".equals(path)) {
      fail(out, 410, "来源核心已内置，请在接入步骤中配置账号；无需导入程序。");
      return;
    }
    if ("/api/diagnostics".equals(path) && "GET".equals(method)) {
      JSONArray diagnostics = new JSONArray();
      for (Discovery d : discover(config)) diagnostics.put(d.diagnostic);
      json(
          out,
          200,
          new JSONObject()
              .put("platform", "android")
              .put("version", BuildConfig.VERSION_NAME)
              .put("backends", diagnostics));
      return;
    }
    if ("/api/update".equals(path) && "GET".equals(method)) {
      fail(out, 501, "请点击 Release，使用 Android 系统安装界面更新 APK。");
      return;
    }
    if ("/api/guides".equals(path) && "GET".equals(method)) {
      json(out, 200, new JSONObject());
      return;
    }
    if ("/api/config".equals(path) && "PUT".equals(method)) {
      try {
        JSONObject next = new JSONObject(new String(body, StandardCharsets.UTF_8));
        if (!config.getString("api_key").equals(next.optString("api_key"))
            || !config.getString("admin_key").equals(next.optString("admin_key")))
          throw new Exception("Cannot change keys in UI");
        BridgeConfig.save(context, next);
        json(out, 200, new JSONObject().put("saved", true));
      } catch (Exception e) {
        fail(out, 400, "配置无效，请检查来源地址、独立密钥与模型别名");
      }
      return;
    }
    if (path.startsWith("/api/process/") && "POST".equals(method)) {
      String[] segments = path.substring("/api/process/".length()).split("/", -1);
      if (segments.length != 2) {
        fail(out, 404, "未知操作");
        return;
      }
      String id = segments[0], action = segments[1];
      JSONArray bs = config.getJSONArray("backends");
      for (int i = 0; i < bs.length(); i++) {
        JSONObject b = bs.getJSONObject(i);
        if (!id.equals(b.optString("id"))) continue;
        if (NativeGateway.base(id) == null || !"builtin".equals(b.optString("kind"))) {
          fail(out, 410, "应用不启动外部来源程序");
          return;
        }
        if ("start".equals(action)) {
          if (!sourceReady(b)) {
            fail(out, 400, setupHelp(id));
            return;
          }
          b.put("enabled", true);
        } else if ("stop".equals(action)) b.put("enabled", false);
        else {
          fail(out, 404, "未知操作");
          return;
        }
        BridgeConfig.save(context, config);
        json(out, 200, GoogleProtocol.obj("ok", true));
        return;
      }
      fail(out, 404, "来源不存在");
      return;
    }
    if ("/api/status".equals(path)) {
      json(
          out,
          200,
          new JSONObject()
              .put("processes", new JSONObject(sourceState))
              .put("backends", config.getJSONArray("backends")));
      return;
    }
    fail(out, 404, "Unknown admin API");
  }

  private static final class Discovery {
    JSONArray models = new JSONArray();
    JSONObject diagnostic = new JSONObject();
    String error = "";
  }

  private Discovery probe(JSONObject b) throws Exception {
    Discovery result = new Discovery();
    String id = b.getString("id");
    long begin = System.nanoTime();
    result
        .diagnostic
        .put("id", id)
        .put("state", "disabled")
        .put("model_count", 0)
        .put("duration_ms", 0)
        .put("next", "在接入步骤中配置账号并启用来源，再检查模型。");
    if (!b.optBoolean("enabled")) return result;
    String code = "", next = "";
    GoogleSessions.Response c = null;
    try {
      if ("builtin".equals(b.optString("kind"))) {
        JSONArray nativeModels =
            GoogleGateway.source(id) ? google.models(id) : nativeGateway.models(b);
        for (int n = 0; n < nativeModels.length(); n++) {
          String name = nativeModels.getString(n);
          result.models.put(
              GoogleProtocol.obj(
                  "id", id + "/" + name, "object", "model", "owned_by", id, "original_id", name));
        }
        result
            .diagnostic
            .put("state", result.models.length() > 0 ? "catalog_ready" : "error")
            .put("code", result.models.length() > 0 ? "ok" : "empty_models")
            .put("model_count", result.models.length())
            .put("duration_ms", (System.nanoTime() - begin) / 1000000L)
            .put("next", "已读取模型目录；在客户端发送一次消息验证实际推理权限。");
        if (result.models.length() == 0) {
          result.error = "来源模型列表为空";
          result.diagnostic.put("error", result.error).put("next", "请打开来源窗口检查授权与模型权限。");
        }
        return result;
      }
      c = connection(b, "/models", "GET", null, java.util.Collections.emptyMap(), 8000);
      int status = c.status;
      if (status != 200) {
        code =
            status == 401 || status == 403
                ? "unauthorized"
                : status == 429
                    ? "rate_limited"
                    : status >= 300 && status < 400 ? "redirect" : "http_error";
        result.error = "上游模型接口返回 HTTP " + status;
        next = "检查账号授权、上游通信 Key、额度与最终 API 地址。";
      } else {
        JSONArray rows =
            new JSONObject(new String(bounded(c.body, 5 * 1024 * 1024), StandardCharsets.UTF_8))
                .getJSONArray("data");
        java.util.HashSet<String> seen = new java.util.HashSet<>();
        for (int i = 0; i < rows.length(); i++) {
          String original = rows.getJSONObject(i).optString("id");
          if (original.isEmpty() || !seen.add(original)) continue;
          String publicId = id + "/" + original;
          result.models.put(
              new JSONObject()
                  .put("id", publicId)
                  .put("object", "model")
                  .put("owned_by", id)
                  .put("original_id", original));
        }
        if (result.models.length() == 0) {
          code = "empty_models";
          result.error = "上游返回空模型列表";
          next = "检查账号授权与上游模型配置；模型为空不能算接入成功。";
        }
      }
    } catch (Exception e) {
      code = "unreachable";
      result.error = "无法连接上游或模型列表无效";
      next = "确认上游已启动、地址正确且手机可以访问。";
    } finally {
      if (c != null) c.close();
    }
    result
        .diagnostic
        .put("duration_ms", (System.nanoTime() - begin) / 1000000L)
        .put("model_count", result.models.length());
    if (result.error.isEmpty())
      result.diagnostic.put("state", "catalog_ready").put("next", "已读取模型目录；在客户端发送一次消息验证实际推理权限。");
    else
      result
          .diagnostic
          .put("state", "error")
          .put("code", code)
          .put("error", result.error)
          .put("next", next);
    return result;
  }

  private java.util.List<Discovery> discover(JSONObject cfg) throws Exception {
    JSONArray backends = enabled(cfg);
    java.util.List<java.util.concurrent.Future<Discovery>> jobs = new java.util.ArrayList<>();
    for (int i = 0; i < backends.length(); i++) {
      final JSONObject backend = backends.getJSONObject(i);
      jobs.add(discoveryPool.submit(() -> probe(backend)));
    }
    java.util.List<Discovery> out = new java.util.ArrayList<>();
    for (java.util.concurrent.Future<Discovery> job : jobs) out.add(job.get());
    return out;
  }

  private void models(OutputStream out, JSONObject cfg) throws Exception {
    JSONArray all = new JSONArray();
    JSONObject errors = new JSONObject();
    java.util.HashSet<String> available = new java.util.HashSet<>();
    for (Discovery result : discover(cfg)) {
      for (int i = 0; i < result.models.length(); i++) {
        JSONObject model = result.models.getJSONObject(i);
        if (available.add(model.getString("id"))) all.put(model);
      }
      if (!result.error.isEmpty()) errors.put(result.diagnostic.getString("id"), result.error);
    }
    JSONObject aliases = cfg.optJSONObject("aliases");
    if (aliases != null) {
      java.util.List<String> names = new java.util.ArrayList<>();
      aliases.keys().forEachRemaining(names::add);
      java.util.Collections.sort(names);
      for (String name : names) {
        String target = aliases.getString(name);
        String lookup = target;
        if (available.contains(lookup) && !available.contains(name))
          all.put(
              new JSONObject()
                  .put("id", name)
                  .put("object", "model")
                  .put("owned_by", "alias")
                  .put("original_id", target));
      }
    }
    BridgeLogs.add(200, "aggregate", "/v1/models", "模型总数 " + all.length());
    json(
        out,
        200,
        new JSONObject().put("object", "list").put("data", all).put("backend_errors", errors));
  }

  private void forward(
      OutputStream out, String path, byte[] body, JSONObject cfg, Map<String, String> headers)
      throws Exception {
    JSONObject input;
    try {
      input = new JSONObject(new String(body, StandardCharsets.UTF_8));
    } catch (Exception e) {
      fail(out, 400, "Invalid JSON body");
      return;
    }
    String requested = input.optString("model");
    JSONObject aliases = cfg.optJSONObject("aliases");
    boolean aliased = aliases != null && aliases.has(requested);
    String model = aliased ? aliases.getString(requested) : requested;
    int slash = model.indexOf('/');
    if (model.isEmpty() || slash < 1 || slash == model.length() - 1) {
      fail(out, 400, "Expected model: backend/model or configured alias");
      return;
    }
    String id = model.substring(0, slash), upstreamModel = model.substring(slash + 1);
    JSONObject b = null;
    JSONArray bs = enabled(cfg);
    for (int i = 0; i < bs.length(); i++) {
      JSONObject item = bs.getJSONObject(i);
      if (item.optBoolean("enabled") && id.equals(item.optString("id"))) {
        b = item;
        break;
      }
    }
    if (b == null) {
      fail(out, 404, "该模型的指定来源未启用");
      return;
    }
    if ("builtin".equals(b.optString("kind"))) {
      if (!GoogleGateway.source(id)) {
        nativeGateway.serve(out, path, b, requested, upstreamModel, input);
        return;
      }
      JSONObject allSettings = cfg.optJSONObject("engine_settings");
      google.serve(
          out,
          path,
          b,
          requested,
          upstreamModel,
          input,
          allSettings == null ? null : allSettings.optJSONObject(id));
      return;
    }
    input.put("model", upstreamModel);
    byte[] packet = input.toString().getBytes(StandardCharsets.UTF_8);
    GoogleSessions.Response c = null;
    boolean started = false;
    try {
      Map<String, String> upstreamHeaders = new LinkedHashMap<>();
      upstreamHeaders.put("Content-Type", "application/json");
      for (String field : new String[] {"anthropic-version", "anthropic-beta", "openai-beta"})
        if (headers.containsKey(field)) upstreamHeaders.put(field, headers.get(field));
      c = connection(b, path.substring(3), "POST", packet, upstreamHeaders, 240000);
      int status = c.status;
      BridgeLogs.add(
          status,
          id,
          path,
          status == 401
              ? "上游认证失败，请检查 Key / 登录状态"
              : status == 429 ? "上游返回限流或额度限制" : status >= 400 ? "上游返回 HTTP " + status : "");
      String content = c.contentType;
      if (content == null || content.contains("\r") || content.contains("\n"))
        content = "application/json";
      String header =
          "HTTP/1.1 "
              + status
              + " "
              + status(status)
              + "\r\nContent-Type: "
              + content
              + "\r\n"
              + "Transfer-Encoding: chunked\r\n"
              + "Connection: close\r\n"
              + "Cache-Control: no-store\r\n"
              + "X-Mixed-Backend: "
              + id
              + "\r\n\r\n";
      out.write(header.getBytes(StandardCharsets.UTF_8));
      out.flush();
      started = true;
      try (InputStream resp = c.body) {
        if (resp != null) {
          byte[] buf = new byte[16384];
          int n;
          while ((n = resp.read(buf)) != -1) {
            if (n == 0) continue;
            out.write((Integer.toHexString(n) + "\r\n").getBytes(StandardCharsets.US_ASCII));
            out.write(buf, 0, n);
            out.write("\r\n".getBytes(StandardCharsets.US_ASCII));
            out.flush();
          }
        }
      }
      out.write("0\r\n\r\n".getBytes(StandardCharsets.US_ASCII));
      out.flush();
    } catch (Exception e) {
      BridgeLogs.add(502, id, path, "无法连接上游：" + e.getClass().getSimpleName());
      Log.e("MIXED-API", "Upstream error: " + id);
      if (!started) {
        try {
          fail(out, 502, "Backend unavailable: " + id);
        } catch (Exception ignored) {
        }
      }
    } finally {
      if (c != null) c.close();
    }
  }
}
