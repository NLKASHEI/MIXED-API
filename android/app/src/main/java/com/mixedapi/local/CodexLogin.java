package com.mixedapi.local;

import static com.mixedapi.local.GoogleProtocol.obj;

import java.io.IOException;
import java.net.URLEncoder;
import java.nio.charset.StandardCharsets;
import java.util.HashMap;
import java.util.Map;
import org.json.JSONObject;

/** Codex device consent and PKCE token exchange, implemented without the CLI. */
final class CodexLogin {
  private static final String ISSUER = "https://auth.openai.com";

  static NativeLogin.Session begin(NativeGateway.Transport transport) throws Exception {
    byte[] body =
        obj("client_id", AccountTokens.CODEX_CLIENT).toString().getBytes(StandardCharsets.UTF_8);
    try (GoogleSessions.Response response =
        transport.send(
            "POST",
            ISSUER + "/api/accounts/deviceauth/usercode",
            new HashMap<>(),
            body,
            System.currentTimeMillis() + 15000)) {
      if (response.status != 200) throw new IOException("Device authorization unavailable");
      JSONObject data = AccountTokens.json(response.body);
      String device = data.getString("device_auth_id"),
          code = data.optString("user_code", data.optString("usercode"));
      if (device.isEmpty() || code.isEmpty()) throw new IOException("Invalid device authorization");
      int interval = Math.max(1, Math.min(30, data.optInt("interval", 5)));
      return new NativeLogin.Session(
          obj(
              "status",
              "pending",
              "url",
              ISSUER + "/codex/device",
              "code",
              code,
              "message",
              "在官方页面输入设备码并完成本人授权"),
          device,
          900000,
          interval * 1000L);
    }
  }

  static JSONObject poll(NativeGateway.Transport transport, NativeLogin.Session session)
      throws Exception {
    long deadline = Math.min(session.deadline, System.currentTimeMillis() + 20000);
    byte[] body =
        obj("device_auth_id", session.state, "user_code", session.visible.getString("code"))
            .toString()
            .getBytes(StandardCharsets.UTF_8);
    JSONObject result;
    try (GoogleSessions.Response response =
        transport.send(
            "POST", ISSUER + "/api/accounts/deviceauth/token", new HashMap<>(), body, deadline)) {
      if (response.status == 403 || response.status == 404 || response.status == 429) return null;
      if (response.status != 200) throw new IOException("Device authorization denied");
      result = AccountTokens.json(response.body);
    }
    String code = result.getString("authorization_code"),
        verifier = result.getString("code_verifier");
    if (code.isEmpty() || verifier.isEmpty()) throw new IOException("Invalid token exchange");
    String[][] fields = {
      {"grant_type", "authorization_code"},
      {"client_id", AccountTokens.CODEX_CLIENT},
      {"code", code},
      {"code_verifier", verifier},
      {"redirect_uri", ISSUER + "/deviceauth/callback"}
    };
    StringBuilder form = new StringBuilder();
    for (String[] field : fields) {
      if (form.length() > 0) form.append('&');
      form.append(field[0]).append('=').append(URLEncoder.encode(field[1], "UTF-8"));
    }
    Map<String, String> headers = new HashMap<>();
    headers.put("Content-Type", "application/x-www-form-urlencoded");
    try (GoogleSessions.Response response =
        transport.send(
            "POST",
            ISSUER + "/oauth/token",
            headers,
            form.toString().getBytes(StandardCharsets.UTF_8),
            deadline)) {
      if (response.status != 200) throw new IOException("Token exchange denied");
      JSONObject data = AccountTokens.json(response.body),
          auth =
              AccountTokens.claims(data.getString("id_token"))
                  .getJSONObject("https://api.openai.com/auth");
      // Claims choose the account header only. The provider validates the issued access token.
      String account = auth.getString("chatgpt_account_id");
      if (account.isEmpty()) throw new IOException("Missing account identity");
      JSONObject credential =
          obj(
              "access_token",
              data.getString("access_token"),
              "refresh_token",
              data.optString("refresh_token"),
              "account_id",
              account);
      long seconds = data.optLong("expires_in");
      if (seconds > 0 && seconds < 31536000)
        credential.put("expires", System.currentTimeMillis() / 1000 + seconds);
      AccountTokens.validate(credential);
      return credential;
    }
  }
}
