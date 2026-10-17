package com.mixedapi.local;

import static org.junit.Assert.*;

import android.content.Context;
import java.net.HttpURLConnection;
import java.net.InetAddress;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;
import okhttp3.mockwebserver.Dispatcher;
import okhttp3.mockwebserver.MockResponse;
import okhttp3.mockwebserver.MockWebServer;
import okhttp3.mockwebserver.RecordedRequest;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.After;
import org.junit.Before;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.annotation.Config;

/** Exercises the Android gateway over real loopback HTTP without accounts. */
@RunWith(RobolectricTestRunner.class)
@Config(sdk = 28)
public class BridgeHttpServerTest {
  private BridgeHttpServer gateway;
  private MockWebServer upstream;
  private final AtomicBoolean empty = new AtomicBoolean(false);
  private final AtomicReference<String> forwardedModel = new AtomicReference<>();

  @Before
  public void start() throws Exception {
    Context context = RuntimeEnvironment.getApplication();
    context.getSharedPreferences("mixed_config", Context.MODE_PRIVATE).edit().clear().commit();
    upstream = new MockWebServer();
    upstream.setDispatcher(
        new Dispatcher() {
          @Override
          public MockResponse dispatch(RecordedRequest request) {
            if ("/v1/models".equals(request.getPath()))
              return new MockResponse()
                  .setHeader("Content-Type", "application/json")
                  .setBody(
                      empty.get()
                          ? "{\"data\":[]}"
                          : "{\"data\":[{\"id\":\"example\"},{\"id\":\"example\"}]}");
            if ("/v1/chat/completions".equals(request.getPath())) {
              try {
                forwardedModel.set(new JSONObject(request.getBody().readUtf8()).getString("model"));
              } catch (Exception ex) {
                throw new RuntimeException(ex);
              }
              return new MockResponse()
                  .setHeader("Content-Type", "application/json")
                  .setBody("{\"choices\":[]}");
            }
            return new MockResponse().setResponseCode(404);
          }
        });
    upstream.start(InetAddress.getByName("127.0.0.1"), 0);
    JSONObject config =
        BridgeConfig.load(context)
            .put("api_key", "android-client")
            .put("admin_key", "android-admin");
    config.put(
        "backends",
        new JSONArray()
            .put(
                new JSONObject()
                    .put("id", "mock")
                    .put("name", "Test")
                    .put("url", "http://127.0.0.1:" + upstream.getPort() + "/v1")
                    .put("auth", "none")
                    .put("enabled", true)));
    config.put("aliases", new JSONObject().put("daily-chat", "mock/example"));
    BridgeConfig.save(context, config);
    gateway = new BridgeHttpServer(context);
    gateway.start();
  }

  @After
  public void stop() throws Exception {
    if (gateway != null) gateway.close();
    if (upstream != null) upstream.shutdown();
  }

  private HttpURLConnection request(String method, String path, String key, String body)
      throws Exception {
    HttpURLConnection connection =
        (HttpURLConnection) new URL("http://127.0.0.1:8765" + path).openConnection();
    connection.setRequestMethod(method);
    connection.setReadTimeout(5000);
    connection.setConnectTimeout(2000);
    connection.setRequestProperty("Authorization", "Bearer " + key);
    if (body != null) {
      connection.setDoOutput(true);
      byte[] bytes = body.getBytes(StandardCharsets.UTF_8);
      connection.setFixedLengthStreamingMode(bytes.length);
      connection.getOutputStream().write(bytes);
      connection.getOutputStream().close();
    }
    return connection;
  }

  private JSONObject read(HttpURLConnection connection) throws Exception {
    try {
      return new JSONObject(
          new String(connection.getInputStream().readAllBytes(), StandardCharsets.UTF_8));
    } finally {
      connection.disconnect();
    }
  }

  @Test
  public void aliasesDiscoverAndForwardThroughTheSamePublicInterface() throws Exception {
    JSONObject catalog = read(request("GET", "/v1/models", "android-client", null));
    JSONArray models = catalog.getJSONArray("data");
    assertEquals(2, models.length());
    assertEquals("mock/example", models.getJSONObject(0).getString("id"));
    assertEquals("daily-chat", models.getJSONObject(1).getString("id"));
    HttpURLConnection chat =
        request(
            "POST",
            "/v1/chat/completions",
            "android-client",
            "{\"model\":\"daily-chat\",\"messages\":[]}");
    assertEquals(200, chat.getResponseCode());
    read(chat);
    assertEquals("example", forwardedModel.get());
  }

  @Test
  public void diagnosticsRejectClientCredentialsAndExplainAnEmptyCatalog() throws Exception {
    HttpURLConnection rejected = request("GET", "/api/diagnostics", "android-client", null);
    assertEquals(401, rejected.getResponseCode());
    rejected.disconnect();
    empty.set(true);
    JSONObject diagnostic =
        read(request("GET", "/api/diagnostics", "android-admin", null))
            .getJSONArray("backends")
            .getJSONObject(0);
    assertEquals("error", diagnostic.getString("state"));
    assertEquals("empty_models", diagnostic.getString("code"));
    assertFalse(diagnostic.toString().contains("android-client"));
    JSONObject catalog = read(request("GET", "/v1/models", "android-client", null));
    assertEquals(0, catalog.getJSONArray("data").length());
    assertTrue(catalog.getJSONObject("backend_errors").has("mock"));
  }

  @Test
  public void pcModelsRemainExplicitAndNeverCatchOtherProviders() throws Exception {
    Context ctx = RuntimeEnvironment.getApplication();
    JSONObject cfg = BridgeConfig.load(ctx);
    cfg.getJSONArray("backends").getJSONObject(0).put("id", "pc");
    cfg.put("aliases", new JSONObject().put("daily-chat", "pc/example"));
    BridgeConfig.save(ctx, cfg);
    JSONObject catalog = read(request("GET", "/v1/models", "android-client", null));
    assertEquals("pc/example", catalog.getJSONArray("data").getJSONObject(0).getString("id"));
    for (String model : new String[] {"aps/example", "example"}) {
      HttpURLConnection c =
          request(
              "POST",
              "/v1/chat/completions",
              "android-client",
              "{\"model\":\"" + model + "\",\"messages\":[]}");
      assertEquals(model.contains("/") ? 404 : 400, c.getResponseCode());
      c.disconnect();
      assertNull(forwardedModel.get());
    }
    read(
        request(
            "POST",
            "/v1/chat/completions",
            "android-client",
            "{\"model\":\"pc/example\",\"messages\":[]}"));
    assertEquals("example", forwardedModel.get());
  }
}
