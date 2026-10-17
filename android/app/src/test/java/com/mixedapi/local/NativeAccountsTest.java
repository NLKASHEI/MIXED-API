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
  public void expiredAccountRefreshesOnceAcrossConcurrentModelRequestsAndRestart()
      throws Exception {
    JSONObject cfg = call("GET", "/api/config", "admin", null, 200);
    for (int i = 0; i < cfg.getJSONArray("backends").length(); i++) {
      JSONObject b = cfg.getJSONArray("backends").getJSONObject(i);
      b.put("enabled", b.getString("id").equals("codex"));
    }
    call("PUT", "/api/config", "admin", cfg, 200);
    JSONObject source = auth("expired-private", "refresh-private");
    source.getJSONObject("tokens").put("expires", 1);
    call("POST", "/api/accounts/codex/import", "admin", source, 200);
    java.util.concurrent.atomic.AtomicInteger refreshes =
        new java.util.concurrent.atomic.AtomicInteger();
    transport =
        (method, url, headers, body, deadline) -> {
          String response;
          if (url.equals("https://auth.openai.com/oauth/token")) {
            assertEquals("POST", method);
            assertFalse(headers.containsKey("Authorization"));
            JSONObject packet = new JSONObject(new String(body, StandardCharsets.UTF_8));
            assertEquals("refresh-private", packet.getString("refresh_token"));
            assertEquals("refresh_token", packet.getString("grant_type"));
            refreshes.incrementAndGet();
            Thread.sleep(150);
            response =
                "{\"access_token\":\"fresh-private\",\"refresh_token\":\"rotated-private\",\"expires_in\":3600}";
          } else {
            assertEquals(
                "https://chatgpt.com/backend-api/codex/models?client_version=0.163.0", url);
            assertEquals("Bearer fresh-private", headers.get("Authorization"));
            assertEquals("account", headers.get("ChatGPT-Account-ID"));
            response = "{\"models\":[{\"slug\":\"gpt-5.4\"}]}";
          }
          return new GoogleSessions.Response(
              200, new java.io.ByteArrayInputStream(response.getBytes(StandardCharsets.UTF_8)));
        };
    java.util.concurrent.ExecutorService pool =
        java.util.concurrent.Executors.newFixedThreadPool(6);
    try {
      java.util.List<java.util.concurrent.Future<JSONObject>> requests =
          new java.util.ArrayList<>();
      for (int i = 0; i < 6; i++)
        requests.add(pool.submit(() -> call("GET", "/v1/models", "client", null, 200)));
      for (java.util.concurrent.Future<JSONObject> f : requests)
        assertEquals(
            "codex/gpt-5.4",
            f.get(5, java.util.concurrent.TimeUnit.SECONDS)
                .getJSONArray("data")
                .getJSONObject(0)
                .getString("id"));
    } finally {
      pool.shutdownNow();
    }
    assertEquals(1, refreshes.get());
    server.close();
    start();
    assertEquals(1, call("GET", "/v1/models", "client", null, 200).getJSONArray("data").length());
    assertEquals(1, refreshes.get());
  }

  @Test
  public void codexGenerationRetriesAuthenticationOnceAndUsesNativeResponses() throws Exception {
    JSONObject cfg = call("GET", "/api/config", "admin", null, 200);
    for (int i = 0; i < cfg.getJSONArray("backends").length(); i++) {
      JSONObject b = cfg.getJSONArray("backends").getJSONObject(i);
      b.put("enabled", b.getString("id").equals("codex"));
    }
    call("PUT", "/api/config", "admin", cfg, 200);
    call(
        "POST",
        "/api/accounts/codex/import",
        "admin",
        auth("expired-private", "refresh-private"),
        200);
    java.util.concurrent.atomic.AtomicInteger
        attempts = new java.util.concurrent.atomic.AtomicInteger(),
        refreshes = new java.util.concurrent.atomic.AtomicInteger();
    transport =
        (method, url, headers, body, deadline) -> {
          int status = 200;
          String response;
          if (url.equals("https://auth.openai.com/oauth/token")) {
            int rotation = refreshes.incrementAndGet();
            assertEquals(
                rotation == 1 ? "refresh-private" : "rotated-private",
                new JSONObject(new String(body, StandardCharsets.UTF_8))
                    .getString("refresh_token"));
            response =
                "{\"access_token\":\"fresh-private-"
                    + rotation
                    + "\",\"refresh_token\":\"rotated-private\",\"expires_in\":3600}";
          } else {
            assertEquals("https://chatgpt.com/backend-api/codex/responses", url);
            JSONObject request = new JSONObject(new String(body, StandardCharsets.UTF_8));
            assertTrue(request.getBoolean("stream"));
            assertFalse(request.getBoolean("store"));
            assertEquals("gpt-5.4", request.getString("model"));
            int attempt = attempts.incrementAndGet();
            if (attempt == 1
                || attempt == 3
                || !headers.get("Authorization").equals("Bearer fresh-private-" + (attempt / 2))) {
              status = 401;
              response = "{\"error\":\"private-details\"}";
            } else {
              assertEquals("Bearer fresh-private-" + (attempt / 2), headers.get("Authorization"));
              response =
                  "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"
                      + "data:"
                      + " {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":2,\"output_tokens\":1}}}\n\n";
            }
          }
          return new GoogleSessions.Response(
              status, new java.io.ByteArrayInputStream(response.getBytes(StandardCharsets.UTF_8)));
        };
    for (int round = 0; round < 2; round++) {
      JSONObject answer =
          call(
              "POST",
              "/v1/chat/completions",
              "client",
              new JSONObject(
                  "{\"model\":\"codex/gpt-5.4\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}"),
              200);
      assertEquals(
          "hello",
          answer
              .getJSONArray("choices")
              .getJSONObject(0)
              .getJSONObject("message")
              .getString("content"));
      assertEquals("codex/gpt-5.4", answer.getString("model"));
    }
    assertEquals(4, attempts.get());
    assertEquals(2, refreshes.get());
  }

  @Test
  public void logoutAndReimportInvalidateLateRefreshResults() throws Exception {
    JSONObject cfg = call("GET", "/api/config", "admin", null, 200);
    for (int i = 0; i < cfg.getJSONArray("backends").length(); i++) {
      JSONObject b = cfg.getJSONArray("backends").getJSONObject(i);
      b.put("enabled", b.getString("id").equals("codex"));
    }
    call("PUT", "/api/config", "admin", cfg, 200);
    for (boolean reimport : new boolean[] {false, true}) {
      JSONObject source = auth("expired-private", "refresh-private");
      source.getJSONObject("tokens").put("expires", 1);
      call("POST", "/api/accounts/codex/import", "admin", source, 200);
      java.util.concurrent.CountDownLatch entered = new java.util.concurrent.CountDownLatch(1),
          release = new java.util.concurrent.CountDownLatch(1);
      java.util.concurrent.atomic.AtomicReference<Thread> refreshThread =
          new java.util.concurrent.atomic.AtomicReference<>();
      transport =
          (method, url, headers, body, deadline) -> {
            String response;
            if (url.equals("https://auth.openai.com/oauth/token")) {
              refreshThread.set(Thread.currentThread());
              entered.countDown();
              while (release.getCount() > 0)
                try {
                  release.await();
                } catch (InterruptedException ignored) {
                }
              response = "{\"access_token\":\"stale-private\",\"expires_in\":3600}";
            } else {
              assertEquals("Bearer replacement-private", headers.get("Authorization"));
              response = "{\"models\":[{\"slug\":\"gpt-5.4\"}]}";
            }
            return new GoogleSessions.Response(
                200, new java.io.ByteArrayInputStream(response.getBytes(StandardCharsets.UTF_8)));
          };
      java.util.concurrent.ExecutorService pool =
          java.util.concurrent.Executors.newSingleThreadExecutor();
      try {
        java.util.concurrent.Future<JSONObject> request =
            pool.submit(() -> call("GET", "/v1/models", "client", null, 200));
        assertTrue(entered.await(3, java.util.concurrent.TimeUnit.SECONDS));
        call("DELETE", "/api/accounts/codex", "admin", null, 200);
        if (reimport)
          call("POST", "/api/accounts/codex/import", "admin", auth("replacement-private", ""), 200);
        release.countDown();
        refreshThread.get().join(3000);
        assertFalse(refreshThread.get().isAlive());
        assertEquals(
            0, request.get(3, java.util.concurrent.TimeUnit.SECONDS).getJSONArray("data").length());
        assertEquals(
            reimport ? "authorized" : "none",
            call("GET", "/api/accounts/codex", "admin", null, 200).getString("status"));
        assertEquals(
            reimport ? 1 : 0,
            call("GET", "/v1/models", "client", null, 200).getJSONArray("data").length());
      } finally {
        release.countDown();
        pool.shutdownNow();
      }
    }
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
