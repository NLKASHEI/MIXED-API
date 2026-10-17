package com.mixedapi.local;

import static com.mixedapi.local.GoogleProtocol.obj;

import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.HashMap;
import java.util.Map;
import org.json.JSONObject;

/** Provider token formats and fixed refresh endpoints. Never returns upstream error text. */
final class AccountTokens {
  static final String CODEX_CLIENT = "app_EMoamEEZ73f0CkXaXp7hrann";

  static JSONObject json(InputStream body) throws Exception {
    return new JSONObject(
        new String(BridgeHttpServer.bounded(body, 1024 * 1024), StandardCharsets.UTF_8));
  }

  static JSONObject unwrap(JSONObject value) throws Exception {
    for (int i = 0; i < 3 && value.optJSONObject("data") != null; i++)
      value = value.getJSONObject("data");
    return value;
  }

  static JSONObject claims(String token) {
    try {
      String[] parts = token.split("\\.");
      if (parts.length == 3)
        return new JSONObject(
            new String(java.util.Base64.getUrlDecoder().decode(parts[1]), StandardCharsets.UTF_8));
    } catch (Exception ignored) {
    }
    return new JSONObject();
  }

  static long expiry(JSONObject value) {
    long expires = value.optLong("expires");
    return expires > 0 ? expires : claims(value.optString("access_token")).optLong("exp");
  }

  static Map<String, String> identity(JSONObject value) {
    Map<String, String> headers = new HashMap<>();
    headers.put("X-Product-Code", "codebuddy");
    String[][] fields = {
      {"domain", "X-Domain"},
      {"user_id", "X-User-Id"},
      {"enterprise_id", "X-Enterprise-Id"},
      {"enterprise_id", "X-Tenant-Id"},
      {"department", "X-Department-Info"}
    };
    for (String[] f : fields)
      if (!value.optString(f[0]).isEmpty()) headers.put(f[1], value.optString(f[0]));
    return headers;
  }

  static JSONObject workbuddy(JSONObject data, JSONObject old) throws Exception {
    JSONObject value =
        new JSONObject(old.toString())
            .put("access_token", data.getString("accessToken"))
            .put("expires", 0)
            .put("needs_login", false);
    if (!data.optString("refreshToken").isEmpty())
      value.put("refresh_token", data.getString("refreshToken"));
    if (!data.optString("domain").isEmpty()) value.put("domain", data.getString("domain"));
    long at = data.optLong("expiresAt"), seconds = data.optLong("expiresIn");
    if (at > 0) value.put("expires", at / 1000);
    else if (seconds > 0 && seconds < 31536000)
      value.put("expires", System.currentTimeMillis() / 1000 + seconds);
    validate(value);
    return value;
  }

  static void validate(JSONObject value) throws Exception {
    if (value.getString("access_token").trim().isEmpty())
      throw new java.io.IOException("Missing access token");
    for (String key :
        new String[] {
          "access_token",
          "refresh_token",
          "account_id",
          "domain",
          "user_id",
          "enterprise_id",
          "department"
        }) {
      String text = value.optString(key);
      if (text.contains("\r") || text.contains("\n") || text.indexOf(0) >= 0)
        throw new java.io.IOException("Invalid account value");
    }
  }

  static JSONObject refresh(NativeGateway.Transport transport, String id, JSONObject old)
      throws Exception {
    Map<String, String> headers = new HashMap<>();
    String url = "https://auth.openai.com/oauth/token";
    JSONObject body =
        obj(
            "grant_type",
            "refresh_token",
            "client_id",
            CODEX_CLIENT,
            "refresh_token",
            old.getString("refresh_token"));
    if (id.equals("workbuddy")) {
      url = "https://copilot.tencent.com/v2/plugin/auth/token/refresh";
      body = new JSONObject();
      headers.putAll(identity(old));
      headers.put("X-Refresh-Token", old.getString("refresh_token"));
      headers.put("X-Auth-Refresh-Source", "plugin");
    }
    try (GoogleSessions.Response response =
        transport.send(
            "POST",
            url,
            headers,
            body.toString().getBytes(StandardCharsets.UTF_8),
            System.currentTimeMillis() + 20000)) {
      JSONObject data;
      try {
        data = json(response.body);
      } catch (Exception e) {
        throw new NativeAccounts.Failure(response.status == 401 ? 401 : 503, "账号刷新暂时失败，请稍后重试");
      }
      if (response.status != 200) {
        Object error = data.opt("error");
        String code =
            error instanceof JSONObject
                ? ((JSONObject) error).optString("code")
                : data.optString("error");
        boolean permanent =
            response.status == 401
                || response.status == 400
                    && java.util.Arrays.asList(
                            "invalid_grant",
                            "refresh_token_expired",
                            "refresh_token_reused",
                            "refresh_token_invalidated")
                        .contains(code);
        throw new NativeAccounts.Failure(
            permanent ? 401 : 503, permanent ? "账号授权已失效，请重新授权" : "账号刷新暂时失败，请稍后重试");
      }
      if (id.equals("workbuddy")) return workbuddy(unwrap(data), old);
      for (String field : new String[] {"id_token", "access_token"}) {
        JSONObject auth =
            claims(data.optString(field)).optJSONObject("https://api.openai.com/auth");
        if (auth != null
            && !auth.optString("chatgpt_account_id").isEmpty()
            && !auth.getString("chatgpt_account_id").equals(old.optString("account_id")))
          throw new NativeAccounts.Failure(401, "账号身份已更改，请重新授权");
      }
      JSONObject value =
          new JSONObject(old.toString())
              .put("access_token", data.getString("access_token"))
              .put("expires", 0)
              .put("needs_login", false);
      if (!data.optString("refresh_token").isEmpty())
        value.put("refresh_token", data.getString("refresh_token"));
      long expiry = data.optLong("expires_in");
      if (expiry > 0 && expiry < 31536000)
        value.put("expires", System.currentTimeMillis() / 1000 + expiry);
      validate(value);
      return value;
    }
  }
}
