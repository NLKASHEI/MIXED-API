package com.mixedapi.local;

import static org.junit.Assert.*;

import android.content.Context;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import org.json.JSONObject;
import org.junit.After;
import org.junit.Before;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 28)
public class NativeAccountsTest {
  private Context context;
  private BridgeHttpServer server;
  private javax.crypto.SecretKey encryptionKey;
  private NativeGateway.Transport transport =
      (m, u, h, b, d) -> {
        throw new AssertionError("Unexpected provider request");
      };

  @Before
  public void setup() throws Exception {
    context = RuntimeEnvironment.getApplication();
    encryptionKey = javax.crypto.KeyGenerator.getInstance("AES").generateKey();
    context.getSharedPreferences("mixed_config", Context.MODE_PRIVATE).edit().clear().commit();
    BridgeConfig.save(
        context, BridgeConfig.load(context).put("api_key", "client").put("admin_key", "admin"));
    start();
  }

  private void start() throws Exception {
    server =
        new BridgeHttpServer(
            context,
            GoogleSessions.shared(),
            (m, u, h, b, d) -> transport.send(m, u, h, b, d),
            () -> encryptionKey);
    server.start();
  }

  @After
  public void stop() {
    if (server != null) server.close();
  }

  private JSONObject call(String method, String path, String key, JSONObject body, int expected)
      throws Exception {
    HttpURLConnection c =
        (HttpURLConnection) new URL("http://127.0.0.1:8765" + path).openConnection();
    c.setRequestMethod(method);
    c.setRequestProperty("Authorization", "Bearer " + key);
    c.setReadTimeout(5000);
    if (body != null) {
      c.setDoOutput(true);
      try (java.io.OutputStream out = c.getOutputStream()) {
        out.write(body.toString().getBytes(StandardCharsets.UTF_8));
      }
    }
    int status = c.getResponseCode();
    String text;
    try (InputStream in = status >= 400 ? c.getErrorStream() : c.getInputStream()) {
      text = new String(in.readAllBytes(), StandardCharsets.UTF_8);
    } finally {
      c.disconnect();
    }
    assertEquals(text, expected, status);
    return new JSONObject(text);
  }

  private JSONObject auth(String access, String refresh) throws Exception {
    return GoogleProtocol.obj(
        "auth_mode",
        "chatgpt",
        "tokens",
        GoogleProtocol.obj(
            "access_token", access, "refresh_token", refresh, "account_id", "account"));
  }

  @Test
  public void importPersistsEncryptedAndLogoutRemovesAuthorization() throws Exception {
    JSONObject source = auth("access-private", "refresh-private");
    call("POST", "/api/accounts/codex/import", "client", source, 401);
    assertEquals(
        "authorized",
        call("POST", "/api/accounts/codex/import", "admin", source, 200).getString("status"));
    String state = call("GET", "/api/accounts/codex", "admin", null, 200).toString();
    assertFalse(state.contains("access-private"));
    assertFalse(state.contains("refresh-private"));
    byte[] saved =
        java.nio.file.Files.readAllBytes(
            new java.io.File(context.getNoBackupFilesDir(), "accounts/codex.dat").toPath());
    assertFalse(new String(saved, StandardCharsets.UTF_8).contains("access-private"));
    assertFalse(new String(saved, StandardCharsets.UTF_8).contains("refresh-private"));
    server.close();
    start();
    assertEquals(
        "authorized", call("GET", "/api/accounts/codex", "admin", null, 200).getString("status"));
    call("DELETE", "/api/accounts/codex", "admin", null, 200);
    assertEquals(
        "none", call("GET", "/api/accounts/codex", "admin", null, 200).getString("status"));
  }
}
