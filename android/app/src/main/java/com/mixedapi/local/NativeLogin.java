package com.mixedapi.local;

import static com.mixedapi.local.GoogleProtocol.obj;

import java.io.IOException;
import java.net.URI;
import java.net.URLEncoder;
import java.nio.charset.StandardCharsets;
import java.util.HashMap;
import java.util.Map;
import org.json.JSONObject;

/** Official consent exchanges. Only the user opens and authorizes the returned official URL. */
final class NativeLogin {
  static final class Session {
    final JSONObject visible;
    final String state;
    final long deadline;
    final long interval;

    Session(JSONObject visible, String state, long duration, long interval) {
      this.visible = visible;
      this.state = state;
      this.deadline = System.currentTimeMillis() + duration;
      this.interval = interval;
    }
  }

  private static Map<String, String> anonymous() {
    Map<String, String> headers = new HashMap<>();
    headers.put("X-Product-Code", "codebuddy");
    for (String name :
        new String[] {
          "X-No-Authorization", "X-No-User-Id", "X-No-Enterprise-Id", "X-No-Department-Info"
        }) headers.put(name, "true");
    return headers;
  }

  static Session begin(NativeGateway.Transport transport, String id) throws Exception {
    if (id.equals("codex")) return CodexLogin.begin(transport);
    if (!id.equals("workbuddy")) throw new IOException("Unsupported login");
    try (GoogleSessions.Response response =
        transport.send(
            "POST",
            "https://copilot.tencent.com/v2/plugin/auth/state?platform=workbuddy",
            anonymous(),
            "{}".getBytes(StandardCharsets.UTF_8),
            System.currentTimeMillis() + 15000)) {
      if (response.status != 200) throw new IOException("Authorization unavailable");
      JSONObject data = AccountTokens.unwrap(AccountTokens.json(response.body));
      String state = data.getString("state"), link = data.getString("authUrl");
      URI url = new URI(link);
      if (state.isEmpty()
          || !"https".equals(url.getScheme())
          || url.getUserInfo() != null
          || url.getPort() != -1
          || !("copilot.tencent.com".equals(url.getHost())
              || "www.codebuddy.cn".equals(url.getHost())))
        throw new IOException("Invalid consent URL");
      return new Session(
          obj("status", "pending", "url", link, "message", "请在官方页面完成本人授权"), state, 300000, 2000);
    }
  }

  static JSONObject poll(NativeGateway.Transport transport, String id, Session session)
      throws Exception {
    if (id.equals("codex")) return CodexLogin.poll(transport, session);
    if (!id.equals("workbuddy")) throw new IOException("Unsupported login");
    String query = URLEncoder.encode(session.state, "UTF-8");
    long deadline = Math.min(session.deadline, System.currentTimeMillis() + 20000);
    JSONObject credential;
    try (GoogleSessions.Response response =
        transport.send(
            "GET",
            "https://copilot.tencent.com/v2/plugin/auth/token?state=" + query,
            anonymous(),
            null,
            deadline)) {
      if (response.status == 401 || response.status == 403)
        throw new IOException("Authorization denied");
      if (response.status != 200) return null;
      JSONObject data = AccountTokens.unwrap(AccountTokens.json(response.body));
      if (data.optString("accessToken").isEmpty()) return null;
      credential = AccountTokens.workbuddy(data, new JSONObject());
    }
    Map<String, String> headers = anonymous();
    headers.remove("X-No-Authorization");
    headers.put("Authorization", "Bearer " + credential.getString("access_token"));
    headers.putAll(AccountTokens.identity(credential));
    try (GoogleSessions.Response response =
        transport.send(
            "GET",
            "https://copilot.tencent.com/v2/plugin/login/account?state=" + query,
            headers,
            null,
            deadline)) {
      if (response.status != 200) return null;
      JSONObject data = AccountTokens.unwrap(AccountTokens.json(response.body));
      if (data.optString("uid").isEmpty()) return null;
      credential.put("user_id", data.get("uid").toString());
      if (!data.isNull("enterpriseId"))
        credential.put("enterprise_id", data.get("enterpriseId").toString());
      if (!data.isNull("departmentInfo"))
        credential.put("department", data.get("departmentInfo").toString());
      AccountTokens.validate(credential);
      return credential;
    }
  }
}
