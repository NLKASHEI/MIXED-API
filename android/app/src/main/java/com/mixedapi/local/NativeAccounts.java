package com.mixedapi.local;

import java.io.OutputStream;
import org.json.JSONObject;

/** Credential lifecycle; admin responses only expose state, never provider credentials. */
final class NativeAccounts {
  private final AccountStore store;

  NativeAccounts(AccountStore store) {
    this.store = store;
  }

  static boolean source(String id) {
    return id.equals("codex") || id.equals("workbuddy");
  }

  synchronized JSONObject state(String id) throws Exception {
    try {
      JSONObject value = store.read(id);
      if (!value.optString("access_token").isEmpty() && !value.optBoolean("needs_login"))
        return GoogleProtocol.obj("status", "authorized");
    } catch (Exception ignored) {
    }
    return GoogleProtocol.obj("status", "none");
  }

  synchronized void admin(OutputStream out, String id, String action, String method, byte[] body)
      throws Exception {
    if (method.equals("GET") && action.isEmpty()) {
      BridgeHttpServer.json(out, 200, state(id));
      return;
    }
    if (method.equals("DELETE") && action.isEmpty()) {
      try {
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
