package com.mixedapi.local;

import java.io.OutputStream;
import org.json.JSONObject;

/** Credential lifecycle; admin responses only expose state, never provider credentials. */
final class NativeAccounts {
  private final AccountStore store;
  private final NativeGateway.Transport transport;
  private final java.util.Map<String, Refresh> refreshes = new java.util.HashMap<>();
  private boolean closed;
  private final java.util.Map<String, Login> logins = new java.util.HashMap<>();

  private static final class Login {
    JSONObject visible;
    Thread worker;
  }

  static final class Failure extends java.io.IOException {
    final int status;

    Failure(int status, String message) {
      super(message);
      this.status = status;
    }
  }

  private static final class Refresh {
    java.util.concurrent.FutureTask<JSONObject> task;
    long finished;
    boolean succeeded;
    Thread worker;
  }

  NativeAccounts(AccountStore store, NativeGateway.Transport transport) {
    this.store = store;
    this.transport = transport == null ? NativeHttp::send : transport;
  }

  private void invalidate(String id) {
    Refresh old = refreshes.remove(id);
    if (old != null) {
      old.task.cancel(true);
      old.worker.interrupt();
    }
  }

  synchronized void close() {
    closed = true;
    for (String id : new java.util.ArrayList<>(refreshes.keySet())) invalidate(id);
    for (String id : new java.util.ArrayList<>(logins.keySet())) cancelLogin(id);
  }

  private void cancelLogin(String id) {
    Login old = logins.remove(id);
    if (old != null && old.worker != null) old.worker.interrupt();
  }

  private JSONObject beginLogin(String id) throws Exception {
    Login flow = new Login();
    synchronized (this) {
      if (closed) throw new Failure(503, "应用正在停止");
      cancelLogin(id);
      flow.visible = GoogleProtocol.obj("status", "pending", "message", "正在准备授权");
      flow.worker = Thread.currentThread();
      logins.put(id, flow);
    }
    try {
      NativeLogin.Session session = NativeLogin.begin(transport, id);
      synchronized (this) {
        if (logins.get(id) != flow || closed) throw new Failure(401, "授权操作已取消");
        flow.visible = session.visible;
        flow.worker = new Thread(() -> pollLogin(id, flow, session), "MIXED account authorization");
        flow.worker.setDaemon(true);
        flow.worker.start();
        return new JSONObject(flow.visible.toString());
      }
    } catch (Exception e) {
      synchronized (this) {
        if (logins.get(id) == flow) {
          flow.worker = null;
          flow.visible = GoogleProtocol.obj("status", "failed", "message", "无法发起授权，请稍后重试");
        }
      }
      throw new Failure(503, "无法发起授权，请稍后重试");
    }
  }

  private void pollLogin(String id, Login flow, NativeLogin.Session session) {
    try {
      while (!Thread.currentThread().isInterrupted()
          && System.currentTimeMillis() < session.deadline) {
        JSONObject credential = NativeLogin.poll(transport, id, session);
        if (credential != null) {
          synchronized (this) {
            if (closed || logins.get(id) != flow) return;
            invalidate(id);
            store.write(id, credential);
            logins.remove(id);
          }
          return;
        }
        Thread.sleep(
            Math.min(session.interval, Math.max(1, session.deadline - System.currentTimeMillis())));
      }
    } catch (Exception ignored) {
    }
    synchronized (this) {
      if (logins.get(id) == flow) {
        try {
          flow.visible = GoogleProtocol.obj("status", "failed", "message", "授权未完成，请重新登录");
          flow.worker = null;
        } catch (Exception ignored) {
          logins.remove(id);
        }
      }
    }
  }

  JSONObject fresh(String id, String rejected) throws Exception {
    Refresh flight;
    synchronized (this) {
      if (closed) throw new Failure(503, "应用正在停止");
      JSONObject credential;
      try {
        credential = store.read(id);
        AccountTokens.validate(credential);
      } catch (Exception e) {
        throw new Failure(401, "请先完成本人账号授权");
      }
      if (credential.optBoolean("needs_login")) throw new Failure(401, "账号授权已失效，请重新授权");
      long expires = AccountTokens.expiry(credential);
      if (!rejected.isEmpty() && !rejected.equals(credential.getString("access_token"))
          || rejected.isEmpty()
              && (expires == 0 || expires > System.currentTimeMillis() / 1000 + 60))
        return credential;
      if (credential.optString("refresh_token").isEmpty()) {
        credential.put("needs_login", true);
        store.write(id, credential);
        throw new Failure(401, "账号授权已过期，请重新授权");
      }
      flight = refreshes.get(id);
      if (flight == null
          || flight.finished > 0
              && (flight.succeeded || System.currentTimeMillis() - flight.finished > 5000)) {
        flight = new Refresh();
        Refresh current = flight;
        flight.task =
            new java.util.concurrent.FutureTask<>(
                () -> {
                  JSONObject next = null;
                  Failure failure = null;
                  try {
                    next = AccountTokens.refresh(transport, id, credential);
                  } catch (Failure e) {
                    failure = e;
                  } catch (Exception e) {
                    failure = new Failure(503, "账号刷新暂时失败，请稍后重试");
                  }
                  synchronized (NativeAccounts.this) {
                    if (closed || refreshes.get(id) != current)
                      throw new Failure(401, "账号授权已更改，请重试请求");
                    try {
                      if (failure == null) store.write(id, next);
                      else if (failure.status == 401)
                        store.write(id, credential.put("needs_login", true));
                    } catch (Exception e) {
                      failure = new Failure(500, "无法安全保存账号授权");
                    }
                    current.finished = System.currentTimeMillis();
                    current.succeeded = failure == null;
                    if (failure != null) throw failure;
                    return next;
                  }
                });
        refreshes.put(id, flight);
        flight.worker = new Thread(flight.task, "MIXED account refresh");
        flight.worker.setDaemon(true);
        flight.worker.start();
      }
    }
    try {
      return new JSONObject(flight.task.get(21, java.util.concurrent.TimeUnit.SECONDS).toString());
    } catch (java.util.concurrent.ExecutionException e) {
      if (e.getCause() instanceof Failure) throw (Failure) e.getCause();
      throw new Failure(503, "账号刷新暂时失败，请稍后重试");
    } catch (java.util.concurrent.CancellationException e) {
      throw new Failure(401, "账号授权已更改，请重试请求");
    } catch (java.util.concurrent.TimeoutException e) {
      throw new Failure(503, "账号刷新超时，请稍后重试");
    }
  }

  GoogleSessions.Response request(String id, String path, JSONObject body) throws Exception {
    JSONObject credential = fresh(id, "");
    for (int attempt = 0; ; attempt++) {
      java.util.Map<String, String> headers = new java.util.HashMap<>();
      headers.put("Authorization", "Bearer " + credential.getString("access_token"));
      headers.put("Accept", body == null ? "application/json" : "text/event-stream");
      if (id.equals("codex")) headers.put("ChatGPT-Account-ID", credential.getString("account_id"));
      else headers.putAll(AccountTokens.identity(credential));
      GoogleSessions.Response response =
          transport.send(
              body == null ? "GET" : "POST",
              NativeGateway.base(id) + path,
              headers,
              body == null
                  ? null
                  : body.toString().getBytes(java.nio.charset.StandardCharsets.UTF_8),
              System.currentTimeMillis() + (body == null ? 15000 : 300000));
      if (response.status != 401 || attempt != 0) return response;
      response.close();
      credential = fresh(id, credential.getString("access_token"));
    }
  }

  static boolean source(String id) {
    return id.equals("codex") || id.equals("workbuddy");
  }

  synchronized JSONObject state(String id) throws Exception {
    if (logins.containsKey(id)) return new JSONObject(logins.get(id).visible.toString());
    try {
      JSONObject value = store.read(id);
      AccountTokens.validate(value);
      long expiry = AccountTokens.expiry(value);
      if (!value.optBoolean("needs_login")
          && value.optString("refresh_token").isEmpty()
          && expiry > 0
          && expiry <= System.currentTimeMillis() / 1000)
        return GoogleProtocol.obj("status", "expired", "message", "账号授权已过期，请重新授权");
      if (!value.optBoolean("needs_login")) return GoogleProtocol.obj("status", "authorized");
    } catch (Exception ignored) {
    }
    return GoogleProtocol.obj("status", "none");
  }

  void admin(OutputStream out, String id, String action, String method, byte[] body)
      throws Exception {
    if (method.equals("POST") && action.equals("login")) {
      try {
        BridgeHttpServer.json(out, 200, beginLogin(id));
      } catch (Failure e) {
        BridgeHttpServer.fail(out, e.status, e.getMessage());
      }
      return;
    }
    adminSaved(out, id, action, method, body);
  }

  private synchronized void adminSaved(
      OutputStream out, String id, String action, String method, byte[] body) throws Exception {
    if (method.equals("GET") && action.isEmpty()) {
      BridgeHttpServer.json(out, 200, state(id));
      return;
    }
    if (method.equals("DELETE") && action.isEmpty()) {
      try {
        invalidate(id);
        cancelLogin(id);
        store.delete(id);
        BridgeHttpServer.json(out, 200, GoogleProtocol.obj("ok", true));
      } catch (Exception e) {
        BridgeHttpServer.fail(out, 500, "无法清除本机授权");
      }
      return;
    }
    if (method.equals("POST") && id.equals("codex") && action.equals("import")) {
      JSONObject credential;
      try {
        if (body.length > 1024 * 1024) throw new java.io.IOException("Oversized account file");
        JSONObject value =
            new JSONObject(new String(body, java.nio.charset.StandardCharsets.UTF_8));
        if (!value.getString("auth_mode").equals("chatgpt"))
          throw new java.io.IOException("Invalid account type");
        JSONObject tokens = value.getJSONObject("tokens");
        if (tokens.getString("access_token").trim().isEmpty()
            || tokens.getString("account_id").trim().isEmpty())
          throw new java.io.IOException("Missing credential");
        credential = new JSONObject();
        for (String key : new String[] {"access_token", "refresh_token", "account_id"})
          if (tokens.has(key)) {
            String token = tokens.getString(key);
            if (token.contains("\r") || token.contains("\n") || token.indexOf(0) >= 0)
              throw new java.io.IOException("Invalid credential");
            credential.put(key, token);
          }
        if (tokens.has("expires")) credential.put("expires", tokens.getLong("expires"));
      } catch (Exception e) {
        BridgeHttpServer.fail(out, 400, "请选择本人的 ChatGPT 授权文件，需包含账号标识与访问凭证");
        return;
      }
      try {
        invalidate(id);
        cancelLogin(id);
        store.write(id, credential);
        BridgeHttpServer.json(out, 200, GoogleProtocol.obj("status", "authorized"));
      } catch (Exception e) {
        BridgeHttpServer.fail(out, 500, "无法安全保存账号授权");
      }
      return;
    }
    BridgeHttpServer.fail(out, 405, "不支持此账号操作");
  }
}
