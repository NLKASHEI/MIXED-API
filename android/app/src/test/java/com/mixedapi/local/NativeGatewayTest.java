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
public class NativeGatewayTest {
  private BridgeHttpServer server;

  private void startSource(String id, NativeGateway.Transport transport) throws Exception {
    Context ctx = RuntimeEnvironment.getApplication();
    ctx.getSharedPreferences("mixed_config", Context.MODE_PRIVATE).edit().clear().commit();
    JSONObject config = BridgeConfig.load(ctx).put("api_key", "client").put("admin_key", "admin");
    config.put(
        "backends",
        GoogleProtocol.arr(
            GoogleProtocol.obj(
                "id",
                id,
                "kind",
                "builtin",
                "url",
                NativeGateway.base(id),
                "auth",
                "none",
                "enabled",
                true)));
    BridgeConfig.save(ctx, config);
    server = new BridgeHttpServer(ctx, GoogleSessions.shared(), transport);
    server.start();
  }

  private JSONObject call(String path, String body) throws Exception {
    return new JSONObject(rawCall(path, body));
  }

  private String rawCall(String path, String body) throws Exception {
    HttpURLConnection c =
        (HttpURLConnection) new URL("http://127.0.0.1:8765" + path).openConnection();
    c.setRequestProperty("Authorization", "Bearer client");
    c.setReadTimeout(3000);
    if (body != null) {
      c.setRequestMethod("POST");
      c.setDoOutput(true);
      try (java.io.OutputStream out = c.getOutputStream()) {
        out.write(body.getBytes(StandardCharsets.UTF_8));
      }
    }
    int status = c.getResponseCode();
    String text;
    try (java.io.InputStream in = status >= 400 ? c.getErrorStream() : c.getInputStream()) {
      text = new String(in.readAllBytes(), StandardCharsets.UTF_8);
    } finally {
      c.disconnect();
    }
    assertEquals(text, 200, status);
    return text;
  }

  @After
  public void stop() {
    if (server != null) server.close();
  }

  @Test
  public void messagesModelsPreserveSignedThinkingAndToolResults() throws Exception {
    java.util.concurrent.atomic.AtomicInteger calls =
        new java.util.concurrent.atomic.AtomicInteger();
    startSource(
        "opencode",
        (method, url, headers, body, deadline) -> {
          assertEquals("https://opencode.ai/inference/anthropic/v1/messages", url);
          assertEquals("2023-06-01", headers.get("anthropic-version"));
          JSONObject request = new JSONObject(new String(body, StandardCharsets.UTF_8));
          assertEquals("adaptive", request.getJSONObject("thinking").getString("type"));
          assertEquals("high", request.getJSONObject("output_config").getString("effort"));
          if (calls.incrementAndGet() == 2) {
            JSONArray messages = request.getJSONArray("messages");
            JSONObject thinking =
                messages.getJSONObject(1).getJSONArray("content").getJSONObject(0);
            assertEquals("signed-state", thinking.getString("signature"));
            assertEquals("plan", thinking.getString("thinking"));
            assertEquals(
                "c1",
                messages
                    .getJSONObject(2)
                    .getJSONArray("content")
                    .getJSONObject(0)
                    .getString("tool_use_id"));
          }
          return new GoogleSessions.Response(
              200,
              new ByteArrayInputStream(
                  ("{\"id\":\"m1\",\"content\":[{\"type\":\"thinking\",\"thinking\":\"plan\",\"signature\":\"signed-state\"},{\"type\":\"tool_use\",\"id\":\"c1\",\"name\":\"weather\",\"input\":{}}],\"stop_reason\":\"tool_use\",\"usage\":{\"input_tokens\":2,\"output_tokens\":3,\"cache_read_input_tokens\":4}}")
                      .getBytes(StandardCharsets.UTF_8)));
        });
    JSONObject request =
        new JSONObject(
            "{\"model\":\"opencode/claude-opus-4-8\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\"reasoning_effort\":\"high\",\"tools\":[{\"type\":\"function\",\"function\":{\"name\":\"weather\",\"parameters\":{\"type\":\"object\"}}}]}");
    JSONObject response = call("/v1/chat/completions", request.toString());
    JSONObject choice = response.getJSONArray("choices").getJSONObject(0);
    assertEquals("tool_calls", choice.getString("finish_reason"));
    assertEquals(9, response.getJSONObject("usage").getInt("total_tokens"));
    JSONObject message = choice.getJSONObject("message");
    assertEquals(
        "signed-state",
        message.getJSONArray("reasoning_details").getJSONObject(0).getString("signature"));
    request
        .getJSONArray("messages")
        .put(message)
        .put(GoogleProtocol.obj("role", "tool", "tool_call_id", "c1", "content", "sunny"));
    call("/v1/chat/completions", request.toString());
    assertEquals(2, calls.get());
  }

  @Test
  public void messagesStreamRetainsSignaturesAndRejectsBrokenTools() throws Exception {
    java.util.concurrent.atomic.AtomicBoolean broken =
        new java.util.concurrent.atomic.AtomicBoolean();
    startSource(
        "opencode",
        (method, url, headers, body, deadline) -> {
          String data = "";
          for (String event :
              new String[] {
                "{\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":2}}}",
                "{\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}",
                "{\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"plan\"}}",
                "{\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"signed\"}}",
                "{\"type\":\"content_block_stop\",\"index\":0}",
                "{\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call1\",\"name\":\"weather\",\"input\":{}}}",
                "{\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\""
                    + (broken.get() ? "{" : "{}")
                    + "\"}}",
                "{\"type\":\"content_block_stop\",\"index\":1}",
                "{\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":3}}",
                "{\"type\":\"message_stop\"}"
              }) data += "data: " + event + "\n\n";
          return new GoogleSessions.Response(
              200, new ByteArrayInputStream(data.getBytes(StandardCharsets.UTF_8)));
        });
    for (boolean invalid : new boolean[] {false, true}) {
      broken.set(invalid);
      String result =
          rawCall(
              "/v1/chat/completions",
              "{\"model\":\"opencode/claude-opus-4-6\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\"stream\":true,\"stream_options\":{\"include_usage\":true}}");
      assertTrue(
          result,
          result.contains("signed")
              && result.contains("reasoning_details")
              && result.contains("call1"));
      assertEquals(result, !invalid, result.contains("[DONE]"));
      assertEquals(result, invalid, result.contains("\"error\""));
    }
  }

  @Test
  public void opencodeGeminiUsesProviderDeclaredProtocol() throws Exception {
    startSource(
        "opencode",
        (method, url, headers, body, deadline) -> {
          JSONObject request = new JSONObject(new String(body, StandardCharsets.UTF_8));
          assertEquals(
              "hi",
              request
                  .getJSONArray("contents")
                  .getJSONObject(0)
                  .getJSONArray("parts")
                  .getJSONObject(0)
                  .getString("text"));
          boolean stream = url.endsWith(":streamGenerateContent?alt=sse");
          assertEquals(
              "https://opencode.ai/inference/google/v1beta/models/gemini-3-flash"
                  + (stream ? ":streamGenerateContent?alt=sse" : ":generateContent"),
              url);
          String packet =
              "{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"},{\"functionCall\":{\"name\":\"weather\",\"args\":{}},\"thoughtSignature\":\"state\"}]},\"finishReason\":\"STOP\"}]}";
          return new GoogleSessions.Response(
              200,
              new ByteArrayInputStream(
                  (stream ? "data: " + packet + "\n\n" : packet).getBytes(StandardCharsets.UTF_8)));
        });
    for (boolean stream : new boolean[] {false, true}) {
      String result =
          rawCall(
              "/v1/chat/completions",
              "{\"model\":\"opencode/gemini-3-flash\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\"stream\":"
                  + stream
                  + "}");
      assertTrue(
          result,
          result.contains("hello")
              && result.contains("thought_signature")
              && result.contains("state"));
      if (stream) assertTrue(result, result.contains("[DONE]"));
    }
  }

  @Test
  public void androidShipsSixOptionalNativeSources() throws Exception {
    Context ctx = RuntimeEnvironment.getApplication();
    ctx.getSharedPreferences("mixed_config", Context.MODE_PRIVATE).edit().clear().commit();
    BridgeConfig.save(
        ctx, BridgeConfig.load(ctx).put("api_key", "client").put("admin_key", "admin"));
    server = new BridgeHttpServer(ctx);
    server.start();
    HttpURLConnection request =
        (HttpURLConnection) new URL("http://127.0.0.1:8765/api/config").openConnection();
    request.setRequestProperty("Authorization", "Bearer admin");
    request.setReadTimeout(3000);
    assertEquals(200, request.getResponseCode());
    JSONObject config;
    try (java.io.InputStream input = request.getInputStream()) {
      config = new JSONObject(new String(input.readAllBytes(), StandardCharsets.UTF_8));
    } finally {
      request.disconnect();
    }
    JSONArray sources = config.getJSONArray("backends");
    int nativeCount = 0;
    for (int i = 0; i < sources.length(); i++) {
      JSONObject source = sources.getJSONObject(i);
      if (source.getString("id").equals("pc")) continue;
      assertFalse(source.optBoolean("enabled"));
      assertEquals("builtin", source.optString("kind"));
      assertTrue(source.getString("url").startsWith("https://"));
      nativeCount++;
    }
    assertEquals(6, nativeCount);
  }

  @Test
  public void expressInferenceUsesNativeGeminiAndPrivateProviderKey() throws Exception {
    Context ctx = RuntimeEnvironment.getApplication();
    ctx.getSharedPreferences("mixed_config", Context.MODE_PRIVATE).edit().clear().commit();
    JSONObject config = BridgeConfig.load(ctx).put("api_key", "client").put("admin_key", "admin");
    config.put(
        "backends",
        GoogleProtocol.arr(
            GoogleProtocol.obj(
                "id",
                "vertex",
                "kind",
                "builtin",
                "url",
                "https://aiplatform.googleapis.com",
                "auth",
                "none",
                "provider_key",
                "express-private",
                "enabled",
                true)));
    BridgeConfig.save(ctx, config);
    java.util.concurrent.atomic.AtomicInteger calls =
        new java.util.concurrent.atomic.AtomicInteger();
    NativeGateway.Transport transport =
        (method, url, headers, body, deadline) -> {
          calls.incrementAndGet();
          assertEquals(
              "https://aiplatform.googleapis.com/v1/publishers/google/models/gemini-3-flash-preview:generateContent",
              url);
          assertEquals("express-private", headers.get("x-goog-api-key"));
          assertFalse(headers.containsKey("Authorization"));
          assertTrue(
              new JSONObject(new String(body, StandardCharsets.UTF_8))
                  .getJSONArray("tools")
                  .getJSONObject(0)
                  .has("googleSearch"));
          JSONObject packet = new JSONObject(new String(body, StandardCharsets.UTF_8));
          assertEquals(
              "hi",
              packet
                  .getJSONArray("contents")
                  .getJSONObject(0)
                  .getJSONArray("parts")
                  .getJSONObject(0)
                  .getString("text"));
          return new GoogleSessions.Response(
              200,
              new ByteArrayInputStream(
                  "{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":2,\"candidatesTokenCount\":1,\"totalTokenCount\":3}}"
                      .getBytes(StandardCharsets.UTF_8)));
        };
    server = new BridgeHttpServer(ctx, GoogleSessions.shared(), transport);
    server.start();
    HttpURLConnection request =
        (HttpURLConnection) new URL("http://127.0.0.1:8765/v1/chat/completions").openConnection();
    request.setRequestProperty("Authorization", "Bearer client");
    request.setRequestMethod("POST");
    request.setDoOutput(true);
    request.setReadTimeout(3000);
    try (java.io.OutputStream out = request.getOutputStream()) {
      out.write(
          "{\"model\":\"vertex/gemini-3-flash-preview-search\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}"
              .getBytes(StandardCharsets.UTF_8));
    }
    assertEquals(200, request.getResponseCode());
    String response;
    try (java.io.InputStream input = request.getInputStream()) {
      response = new String(input.readAllBytes(), StandardCharsets.UTF_8);
    } finally {
      request.disconnect();
    }
    JSONObject result = new JSONObject(response);
    assertEquals(
        "hello",
        result
            .getJSONArray("choices")
            .getJSONObject(0)
            .getJSONObject("message")
            .getString("content"));
    assertEquals("vertex/gemini-3-flash-preview-search", result.getString("model"));
    assertEquals(3, result.getJSONObject("usage").getInt("total_tokens"));
    assertFalse(response.contains("express-private"));
    assertEquals(1, calls.get());
  }

  @Test
  public void opencodeDiscoversAndCallsNativeChatWithoutPC() throws Exception {
    java.util.List<String> requests = new java.util.ArrayList<>();
    startSource(
        "opencode",
        (method, url, headers, body, deadline) -> {
          requests.add(url);
          String response;
          if (url.equals("https://opencode.ai/inference/v1/models")) {
            assertEquals("GET", method);
            response = "{\"data\":[{\"id\":\"deepseek-v4-flash\"}]}";
          } else {
            assertEquals("https://opencode.ai/inference/openai/v1/chat/completions", url);
            assertEquals(
                "deepseek-v4-flash",
                new JSONObject(new String(body, StandardCharsets.UTF_8)).getString("model"));
            response =
                "{\"choices\":[{\"message\":{\"role\":\"assistant\",\"content\":\"hello\"},\"finish_reason\":\"stop\"}]}";
          }
          return new GoogleSessions.Response(
              200, new ByteArrayInputStream(response.getBytes(StandardCharsets.UTF_8)));
        });
    assertEquals(
        "opencode/deepseek-v4-flash",
        call("/v1/models", null).getJSONArray("data").getJSONObject(0).getString("id"));
    JSONObject response =
        call(
            "/v1/chat/completions",
            "{\"model\":\"opencode/deepseek-v4-flash\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}");
    assertEquals(
        "hello",
        response
            .getJSONArray("choices")
            .getJSONObject(0)
            .getJSONObject("message")
            .getString("content"));
    assertEquals(2, requests.size());
  }

  @Test
  public void opencodeStreamRequiresTerminalFrameAndKeepsToolCalls() throws Exception {
    java.util.concurrent.atomic.AtomicBoolean truncated =
        new java.util.concurrent.atomic.AtomicBoolean();
    startSource(
        "opencode",
        (method, url, headers, body, deadline) -> {
          assertEquals("https://opencode.ai/inference/openai/v1/chat/completions", url);
          String data =
              "data:"
                  + " {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"plan\",\"tool_calls\":[{\"index\":0,\"id\":\"call1\",\"function\":{\"name\":\"weather\",\"arguments\":\"{}\"}},{\"index\":1,\"id\":\"call2\",\"function\":{\"name\":\"time\",\"arguments\":\"{}\"}}]}}]}\n\n"
                  + "data:"
                  + " {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\n"
                  + (truncated.get() ? "" : "data: [DONE]\n\n");
          return new GoogleSessions.Response(
              200, new ByteArrayInputStream(data.getBytes(StandardCharsets.UTF_8)));
        });
    for (boolean missing : new boolean[] {false, true}) {
      truncated.set(missing);
      HttpURLConnection c =
          (HttpURLConnection) new URL("http://127.0.0.1:8765/v1/chat/completions").openConnection();
      c.setRequestProperty("Authorization", "Bearer client");
      c.setRequestMethod("POST");
      c.setDoOutput(true);
      c.setReadTimeout(3000);
      try (java.io.OutputStream out = c.getOutputStream()) {
        out.write(
            "{\"model\":\"opencode/deepseek-v4-flash\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\"stream\":true,\"stream_options\":{\"include_usage\":true}}"
                .getBytes(StandardCharsets.UTF_8));
      }
      assertEquals(200, c.getResponseCode());
      String response;
      try (java.io.InputStream in = c.getInputStream()) {
        response = new String(in.readAllBytes(), StandardCharsets.UTF_8);
      } finally {
        c.disconnect();
      }
      assertTrue(
          response,
          response.contains("call1") && response.contains("call2") && response.contains("plan"));
      if (missing) {
        assertFalse(response, response.contains("[DONE]"));
        assertTrue(response, response.contains("error"));
      } else {
        assertTrue(response, response.contains("[DONE]") && response.contains("total_tokens"));
        for (String line : response.split("\n"))
          if (line.startsWith("data: {") && !line.contains("\"error\""))
            assertEquals(
                "opencode/deepseek-v4-flash", new JSONObject(line.substring(6)).getString("model"));
      }
    }
  }

  @Test
  public void chatClientCanUseResponsesModelsWithToolsAndThinking() throws Exception {
    startSource(
        "opencode",
        (method, url, headers, body, deadline) -> {
          assertEquals("https://opencode.ai/inference/openai/v1/responses", url);
          JSONObject input = new JSONObject(new String(body, StandardCharsets.UTF_8));
          assertEquals("high", input.getJSONObject("reasoning").getString("effort"));
          assertEquals("weather", input.getJSONArray("tools").getJSONObject(0).getString("name"));
          assertEquals("hi", input.getJSONArray("input").getJSONObject(0).getString("content"));
          assertEquals(128, input.getInt("max_output_tokens"));
          String response =
              "{\"id\":\"resp1\",\"error\":null,\"status\":\"completed\",\"output\":[{\"type\":\"reasoning\",\"summary\":[{\"type\":\"summary_text\",\"text\":\"plan\"}]},{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]},{\"type\":\"function_call\",\"call_id\":\"call1\",\"name\":\"weather\",\"arguments\":\"{}\"}],\"usage\":{\"input_tokens\":8,\"output_tokens\":3,\"total_tokens\":11}}";
          return new GoogleSessions.Response(
              200, new ByteArrayInputStream(response.getBytes(StandardCharsets.UTF_8)));
        });
    JSONObject result =
        call(
            "/v1/chat/completions",
            "{\"model\":\"opencode/gpt-5.4\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\"max_tokens\":128,\"reasoning_effort\":\"high\",\"tools\":[{\"type\":\"function\",\"function\":{\"name\":\"weather\",\"parameters\":{\"type\":\"object\"}}}]}");
    JSONObject choice = result.getJSONArray("choices").getJSONObject(0),
        message = choice.getJSONObject("message");
    assertEquals("hello", message.getString("content"));
    assertEquals("plan", message.getString("reasoning_content"));
    assertEquals("call1", message.getJSONArray("tool_calls").getJSONObject(0).getString("id"));
    assertEquals("tool_calls", choice.getString("finish_reason"));
    assertEquals(11, result.getJSONObject("usage").getInt("total_tokens"));
  }

  @Test
  public void responsesStreamConvertsIncrementalTextAndTools() throws Exception {
    startSource(
        "opencode",
        (method, url, headers, body, deadline) -> {
          assertEquals("https://opencode.ai/inference/openai/v1/responses", url);
          String data =
              "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"
                  + "data:"
                  + " {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"call_id\":\"c1\",\"name\":\"weather\",\"arguments\":\"\"}}\n\n"
                  + "data:"
                  + " {\"type\":\"response.function_call_arguments.delta\",\"output_index\":1,\"delta\":\"{}\"}\n\n"
                  + "data:"
                  + " {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":2,\"output_tokens\":3,\"total_tokens\":5}}}\n\n";
          return new GoogleSessions.Response(
              200, new ByteArrayInputStream(data.getBytes(StandardCharsets.UTF_8)));
        });
    HttpURLConnection c =
        (HttpURLConnection) new URL("http://127.0.0.1:8765/v1/chat/completions").openConnection();
    c.setRequestProperty("Authorization", "Bearer client");
    c.setRequestMethod("POST");
    c.setDoOutput(true);
    c.setReadTimeout(3000);
    try (java.io.OutputStream out = c.getOutputStream()) {
      out.write(
          "{\"model\":\"opencode/gpt-5.4\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\"stream\":true,\"stream_options\":{\"include_usage\":true}}"
              .getBytes(StandardCharsets.UTF_8));
    }
    assertEquals(200, c.getResponseCode());
    String response;
    try (java.io.InputStream in = c.getInputStream()) {
      response = new String(in.readAllBytes(), StandardCharsets.UTF_8);
    } finally {
      c.disconnect();
    }
    assertTrue(
        response,
        response.contains("hello")
            && response.contains("weather")
            && response.contains("[DONE]")
            && response.contains("total_tokens"));
    assertFalse(response, response.contains("error"));
  }
}
