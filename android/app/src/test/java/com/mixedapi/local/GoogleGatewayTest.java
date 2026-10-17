package com.mixedapi.local;

import static org.junit.Assert.*;

import android.content.Context;
import java.io.ByteArrayInputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.After;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 28)
public class GoogleGatewayTest {
  private BridgeHttpServer server;

  @After
  public void stop() {
    if (server != null) server.close();
  }

  private void start(GoogleGateway.Transport transport) throws Exception {
    Context ctx = RuntimeEnvironment.getApplication();
    ctx.getSharedPreferences("mixed_config", Context.MODE_PRIVATE).edit().clear().commit();
    JSONObject config = BridgeConfig.load(ctx).put("api_key", "client").put("admin_key", "admin");
    JSONArray backends = new JSONArray();
    for (String id : new String[] {"aps", "aistudio"})
      backends.put(
          GoogleProtocol.obj(
              "id",
              id,
              "name",
              id,
              "kind",
              "builtin",
              "url",
              GoogleGateway.base(id),
              "auth",
              "none",
              "enabled",
              true));
    config.put("backends", backends);
    BridgeConfig.save(ctx, config);
    server = new BridgeHttpServer(ctx, transport);
    server.start();
  }

  private HttpURLConnection request(String path, String key, String body) throws Exception {
    HttpURLConnection c =
        (HttpURLConnection) new URL("http://127.0.0.1:8765" + path).openConnection();
    c.setReadTimeout(3000);
    c.setRequestProperty("Authorization", "Bearer " + key);
    if (body != null) {
      c.setRequestMethod("POST");
      c.setDoOutput(true);
      byte[] bytes = body.getBytes(StandardCharsets.UTF_8);
      c.setFixedLengthStreamingMode(bytes.length);
      try (java.io.OutputStream out = c.getOutputStream()) {
        out.write(bytes);
      }
    }
    return c;
  }

  private static class Fixture implements GoogleGateway.Transport {
    JSONObject operation;
    String source;
    boolean rejected;

    public void open(Context c, String id) {}

    public JSONObject state(String id) throws Exception {
      return GoogleProtocol.obj("status", "ready");
    }

    public GoogleSessions.Response send(String id, JSONObject op, long deadline) throws Exception {
      operation = op;
      source = id;
      String kind = op.getString("kind"), data;
      String gemini =
          "{\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"text\":\"hello\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"totalTokenCount\":3}}";
      switch (kind) {
        case "aps":
          data =
              "[{\"results\":[{\"data\":{\"ui\":{\"streamGenerateContentAnonymous\":"
                  + gemini
                  + "}}}]}]";
          if (rejected) data = data.substring(0, data.length() - 1);
          break;
        case "build":
          data = GoogleProtocol.arr(gemini).toString();
          break;
        case "playground":
          data = "[[[[[[[[null,\"hello\"]],\"model\"],1]],null,[2,1,3]]]]";
          if (rejected) data = "[[],[7,\"private-secret\"]]";
          break;
        case "models":
          data = "[[[\"models/play-only\",null,null,null,null,null,null,[\"generateContent\"]]]]";
          break;
        case "build_models":
          data =
              GoogleProtocol.arr(
                      "{\"models\":[{\"name\":\"models/build-only\",\"supportedGenerationMethods\":[\"generateContent\"]}]}")
                  .toString();
          break;
        default:
          throw new AssertionError("Unexpected operation");
      }
      return new GoogleSessions.Response(
          200, new ByteArrayInputStream(data.getBytes(StandardCharsets.UTF_8)));
    }
  }

  @Test
  public void nativeGoogleSourcesUsePublicChatInterface() throws Exception {
    Fixture fixture = new Fixture();
    start(fixture);
    for (String model :
        new String[] {
          "aps/gemini-test", "aistudio/build/gemini-test", "aistudio/playground/gemini-test"
        })
      for (boolean stream : new boolean[] {false, true}) {
        HttpURLConnection c =
            request(
                "/v1/chat/completions",
                "client",
                GoogleProtocol.obj(
                        "model",
                        model,
                        "messages",
                        GoogleProtocol.arr(GoogleProtocol.obj("role", "user", "content", "hi")),
                        "stream",
                        stream)
                    .toString());
        try {
          assertEquals(200, c.getResponseCode());
          String body = new String(c.getInputStream().readAllBytes(), StandardCharsets.UTF_8);
          assertTrue(body, body.contains("hello"));
          assertEquals(stream, body.contains("[DONE]"));
          assertEquals("gemini-test", fixture.operation.getString("model"));
        } finally {
          c.disconnect();
        }
      }
  }

  @Test
  public void catalogsDoNotInventCapabilityAndRejectClientAdminAccess() throws Exception {
    start(new Fixture());
    HttpURLConnection c = request("/v1/models", "client", null);
    String body;
    try {
      body = new String(c.getInputStream().readAllBytes(), StandardCharsets.UTF_8);
    } finally {
      c.disconnect();
    }
    assertTrue(body, body.contains("aistudio/build/build-only"));
    assertTrue(body.contains("aistudio/playground/play-only"));
    assertFalse(body.contains("aistudio/build/play-only"));
    c = request("/api/accounts/aistudio", "client", null);
    assertEquals(401, c.getResponseCode());
    c.disconnect();
    c = request("/api/diagnostics", "admin", null);
    try {
      JSONObject report =
          new JSONObject(new String(c.getInputStream().readAllBytes(), StandardCharsets.UTF_8));
      JSONArray rows = report.getJSONArray("backends");
      for (int i = 0; i < rows.length(); i++)
        if (GoogleGateway.source(rows.getJSONObject(i).getString("id")))
          assertEquals("catalog_ready", rows.getJSONObject(i).getString("state"));
    } finally {
      c.disconnect();
    }
  }

  @Test
  public void interruptedNativeStreamNeverSendsDoneOrProviderErrorText() throws Exception {
    Fixture f = new Fixture();
    f.rejected = true;
    start(f);
    HttpURLConnection c =
        request(
            "/v1/chat/completions",
            "client",
            "{\"model\":\"aps/gemini-test\",\"stream\":true,\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}");
    try {
      String body = new String(c.getInputStream().readAllBytes(), StandardCharsets.UTF_8);
      assertTrue(body.contains("error"));
      assertFalse(body.contains("[DONE]"));
      assertFalse(body.contains("private-secret"));
    } finally {
      c.disconnect();
    }
  }

  @Test
  public void officialWindowRejectsLocalAndWrongProviderOrigins() {
    assertTrue(GoogleSessions.navigation("aistudio", "https://accounts.google.com/", false));
    assertFalse(GoogleSessions.navigation("aistudio", "http://127.0.0.1:8765/", false));
    assertFalse(GoogleSessions.navigation("aistudio", "https://console.cloud.google.com/", false));
    assertFalse(
        GoogleSessions.navigation(
            "aps", "https://console.cloud.google.com.attacker.invalid/", false));
  }

  @Test
  public void clientDisconnectCancelsQueuedProviderWork() throws Exception {
    java.util.concurrent.CountDownLatch entered = new java.util.concurrent.CountDownLatch(1),
        cancelled = new java.util.concurrent.CountDownLatch(1);
    start(
        new Fixture() {
          @Override
          public GoogleSessions.Response send(String id, JSONObject op, long deadline)
              throws Exception {
            entered.countDown();
            try {
              new java.util.concurrent.CountDownLatch(1).await();
              throw new AssertionError();
            } catch (InterruptedException e) {
              cancelled.countDown();
              throw e;
            }
          }
        });
    java.net.Socket client = new java.net.Socket("127.0.0.1", 8765);
    String body =
        "{\"model\":\"aps/gemini-test\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}]}";
    client
        .getOutputStream()
        .write(
            ("POST /v1/chat/completions HTTP/1.1\r\n"
                 + "Host: localhost\r\n"
                 + "Authorization: Bearer client\r\n"
                 + "Content-Length: "
                    + body.length()
                    + "\r\n\r\n"
                    + body)
                .getBytes(StandardCharsets.UTF_8));
    assertTrue(entered.await(2, java.util.concurrent.TimeUnit.SECONDS));
    client.close();
    assertTrue(
        "Provider survived client disconnect",
        cancelled.await(2, java.util.concurrent.TimeUnit.SECONDS));
  }

  @Test
  public void retriesOnlyBusyResponsesBeforeGeneration() throws Exception {
    java.util.concurrent.atomic.AtomicInteger count =
        new java.util.concurrent.atomic.AtomicInteger();
    start(
        new Fixture() {
          @Override
          public GoogleSessions.Response send(String id, JSONObject op, long deadline)
              throws Exception {
            if (count.incrementAndGet() == 1)
              return new GoogleSessions.Response(429, new ByteArrayInputStream(new byte[0]), "0");
            return super.send(id, op, deadline);
          }
        });
    Context ctx = RuntimeEnvironment.getApplication();
    JSONObject cfg = BridgeConfig.load(ctx);
    cfg.put(
        "engine_settings",
        GoogleProtocol.obj(
            "aps",
            GoogleProtocol.obj(
                "max_retries", 1, "request_timeout_seconds", 10, "aggregate_stream", false)));
    BridgeConfig.save(ctx, cfg);
    HttpURLConnection c =
        request(
            "/v1/chat/completions",
            "client",
            "{\"model\":\"aps/gemini-test\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}");
    try {
      assertEquals(200, c.getResponseCode());
      assertEquals("aps", c.getHeaderField("X-Mixed-Backend"));
      assertEquals(2, count.get());
    } finally {
      c.disconnect();
    }
  }

  @Test
  public void malformedOptionsAreRejectedBeforeProviderCall() throws Exception {
    Fixture fixture = new Fixture();
    start(fixture);
    for (String extra :
        new String[] {"\"n\":1.5", "\"stream\":\"false\"", "\"unknown_option\":true"}) {
      HttpURLConnection c =
          request(
              "/v1/chat/completions",
              "client",
              "{\"model\":\"aps/gemini-test\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],"
                  + extra
                  + "}");
      assertEquals(400, c.getResponseCode());
      c.disconnect();
      assertNull(fixture.operation);
    }
  }

  @Test
  public void rateLimitRetainsRetryAfterWithoutLeakingBody() throws Exception {
    start(
        new Fixture() {
          @Override
          public GoogleSessions.Response send(String id, JSONObject op, long deadline) {
            return new GoogleSessions.Response(
                429,
                new ByteArrayInputStream("provider-secret".getBytes(StandardCharsets.UTF_8)),
                "120");
          }
        });
    HttpURLConnection c =
        request(
            "/v1/chat/completions",
            "client",
            "{\"model\":\"aps/gemini-test\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}");
    try {
      assertEquals(429, c.getResponseCode());
      assertEquals("120", c.getHeaderField("Retry-After"));
      assertFalse(
          new String(c.getErrorStream().readAllBytes(), StandardCharsets.UTF_8)
              .contains("provider-secret"));
    } finally {
      c.disconnect();
    }
  }
}
