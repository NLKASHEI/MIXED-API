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

  @Test
  public void googleSignedThoughtReplayRejectsOtherProtocols() throws Exception {
    java.util.concurrent.atomic.AtomicInteger calls =
        new java.util.concurrent.atomic.AtomicInteger();
    startSource(
        "vertex",
        (method, url, headers, body, deadline) -> {
          if (calls.incrementAndGet() == 2)
            assertTrue(
                new String(body, StandardCharsets.UTF_8)
                    .contains("\"thoughtSignature\":\"signature-fixture\""));
          return new GoogleSessions.Response(
              200,
              new ByteArrayInputStream(
                  "{\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"text\":\"plan\",\"thought\":true,\"thoughtSignature\":\"signature-fixture\"},{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}"
                      .getBytes(StandardCharsets.UTF_8)));
        });
    Context ctx = RuntimeEnvironment.getApplication();
    JSONObject config = BridgeConfig.load(ctx);
    config.getJSONArray("backends").getJSONObject(0).put("provider_key", "fixture");
    BridgeConfig.save(ctx, config);
    JSONObject reply =
        call(
            "/v1/chat/completions",
            "{\"model\":\"vertex/gemini-test\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}");
    assertTrue(reply.toString().contains("signature-fixture"));
    String input =
        GoogleProtocol.obj(
                "model",
                "vertex/gemini-test",
                "messages",
                GoogleProtocol.arr(
                    GoogleProtocol.obj("role", "user", "content", "hi"),
                    reply.getJSONArray("choices").getJSONObject(0).getJSONObject("message"),
                    GoogleProtocol.obj("role", "user", "content", "continue")))
            .toString();
    call("/v1/chat/completions", input);
    rawCall("/v1/chat/completions", input.replace("google-gemini-v1", "anthropic-claude-v1"), 400);
    assertEquals(2, calls.get());
  }

  @Test
  public void responsesStreamPreservesOpaqueReasoningOnce() throws Exception {
    startSource(
        "opencode",
        (method, url, headers, body, deadline) ->
            new GoogleSessions.Response(
                200,
                new ByteArrayInputStream(
                    ("data:"
                         + " {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"id\":\"rs1\",\"type\":\"reasoning\",\"summary\":[],\"encrypted_content\":\"opaque-fixture\"}}\n\n"
                         + "data:"
                         + " {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\",\"output\":[{\"id\":\"rs1\",\"type\":\"reasoning\",\"summary\":[],\"encrypted_content\":\"opaque-fixture\"}]}}\n\n")
                        .getBytes(StandardCharsets.UTF_8))));
    String result =
        rawCall(
            "/v1/chat/completions",
            "{\"model\":\"opencode/gpt-5.4\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\"stream\":true}");
    assertTrue(result.contains("[DONE]"));
    assertEquals(1, result.split("opaque-fixture", -1).length - 1);
  }

  @Test
  public void chatReplaysNativeResponsesReasoningItems() throws Exception {
    java.util.concurrent.atomic.AtomicInteger calls =
        new java.util.concurrent.atomic.AtomicInteger();
    startSource(
        "opencode",
        (method, url, headers, body, deadline) -> {
          if (calls.incrementAndGet() == 2)
            assertTrue(
                new String(body, StandardCharsets.UTF_8)
                    .contains("\"encrypted_content\":\"opaque-fixture\""));
          return new GoogleSessions.Response(
              200,
              new ByteArrayInputStream(
                  "{\"id\":\"r1\",\"status\":\"completed\",\"output\":[{\"id\":\"rs1\",\"type\":\"reasoning\",\"summary\":[{\"type\":\"summary_text\",\"text\":\"plan\"}],\"encrypted_content\":\"opaque-fixture\"},{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"answer\"}]}]}"
                      .getBytes(StandardCharsets.UTF_8)));
        });
    JSONObject reply =
        call(
            "/v1/chat/completions",
            "{\"model\":\"opencode/gpt-5.4\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}");
    assertTrue(reply.toString().contains("opaque-fixture"));
    call(
        "/v1/chat/completions",
        GoogleProtocol.obj(
                "model",
                "opencode/gpt-5.4",
                "messages",
                GoogleProtocol.arr(
                    GoogleProtocol.obj("role", "user", "content", "hi"),
                    reply.getJSONArray("choices").getJSONObject(0).getJSONObject("message"),
                    GoogleProtocol.obj("role", "user", "content", "continue")))
            .toString());
    assertEquals(2, calls.get());
  }

  @Test
  public void googleAudioPreservesModalitiesAndMediaInput() throws Exception {
    java.util.concurrent.atomic.AtomicInteger calls =
        new java.util.concurrent.atomic.AtomicInteger();
    startSource(
        "vertex",
        (method, url, headers, body, deadline) -> {
          calls.incrementAndGet();
          JSONObject input = new JSONObject(new String(body, StandardCharsets.UTF_8));
          assertEquals(
              "[\"TEXT\",\"AUDIO\"]",
              input
                  .getJSONObject("generationConfig")
                  .getJSONArray("responseModalities")
                  .toString());
          String content = input.getJSONArray("contents").toString().replace("\\/", "/");
          assertTrue(content.contains("audio/wav"));
          assertTrue(content.contains("application/pdf"));
          assertEquals(
              "MEDIA_RESOLUTION_HIGH",
              input.getJSONObject("generationConfig").getString("mediaResolution"));
          return new GoogleSessions.Response(
              200,
              new ByteArrayInputStream(
                  "{\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}"
                      .getBytes(StandardCharsets.UTF_8)));
        });
    Context ctx = RuntimeEnvironment.getApplication();
    JSONObject config = BridgeConfig.load(ctx);
    config.getJSONArray("backends").getJSONObject(0).put("provider_key", "fixture-key");
    BridgeConfig.save(ctx, config);
    String request =
        "{\"model\":\"vertex/tts\",\"messages\":[{\"role\":\"user\",\"content\":[{\"type\":\"input_audio\",\"input_audio\":{\"format\":\"wav\",\"data\":\"AAABAA==\"}},{\"type\":\"file\",\"file\":{\"file_data\":\"data:application/pdf;base64,JVBERg==\"}}]}],\"media_resolution\":\"high\",\"modalities\":[\"text\",\"audio\"],\"audio\":{\"voice\":\"Kore\",\"format\":\"pcm16\"}}";
    rawCall("/v1/chat/completions", request, 200);
    rawCall("/v1/chat/completions", request.replace("[\"text\",\"audio\"]", "[\"text\"]"), 400);
    assertEquals(1, calls.get());
  }

  @Test
  public void bridgeDoesNotDropAudioIntoEmptyTextSuccess() throws Exception {
    startSource(
        "opencode",
        (method, url, headers, body, deadline) ->
            new GoogleSessions.Response(
                200,
                new ByteArrayInputStream(
                    "{\"choices\":[{\"finish_reason\":\"stop\",\"message\":{\"content\":\"\",\"media\":[{\"mime_type\":\"audio/L16;codec=pcm;rate=24000\",\"data\":\"AAABAA==\"}]}}]}"
                        .getBytes(StandardCharsets.UTF_8))));
    rawCall("/v1/responses", "{\"model\":\"opencode/deepseek-v4-flash\",\"input\":\"hi\"}", 502);
  }

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
    return rawCall(path, body, 200);
  }

  private String rawCall(String path, String body, int expected) throws Exception {
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
    assertEquals(text, expected, status);
    return text;
  }

  @After
  public void stop() {
    if (server != null) server.close();
  }

  @Test
  public void responsesCanReplayTheirOwnTextReasoningAndTools() throws Exception {
    java.util.concurrent.atomic.AtomicInteger calls =
        new java.util.concurrent.atomic.AtomicInteger();
    startSource(
        "opencode",
        (method, url, headers, body, deadline) -> {
          JSONObject input = new JSONObject(new String(body, StandardCharsets.UTF_8));
          if (calls.incrementAndGet() == 2) {
            String messages = input.getJSONArray("messages").toString();
            assertTrue(
                messages,
                messages.contains("reasoning_content")
                    && messages.contains("plan")
                    && messages.contains("tool_call_id")
                    && messages.contains("tool-result")
                    && messages.contains("url_citation"));
          }
          String response =
              "{\"choices\":[{\"finish_reason\":\"tool_calls\",\"message\":{\"content\":\"hello\",\"annotations\":[{\"type\":\"url_citation\",\"url_citation\":{\"url\":\"https://example.com/\",\"title\":\"source\",\"start_index\":0,\"end_index\":5}}],\"reasoning_content\":\"plan\",\"tool_calls\":[{\"id\":\"c1\",\"function\":{\"name\":\"weather\",\"arguments\":\"{}\"}}]}}]}";
          return new GoogleSessions.Response(
              200, new ByteArrayInputStream(response.getBytes(StandardCharsets.UTF_8)));
        });
    JSONObject first =
        call(
            "/v1/responses",
            GoogleProtocol.obj("model", "opencode/deepseek-v4-flash", "input", "hi").toString());
    JSONArray history = GoogleProtocol.arr(GoogleProtocol.obj("role", "user", "content", "hi"));
    JSONArray output = first.getJSONArray("output");
    for (int i = 0; i < output.length(); i++) history.put(output.get(i));
    history.put(
        GoogleProtocol.obj(
            "type", "function_call_output", "call_id", "c1", "output", "tool-result"));
    call(
        "/v1/responses",
        GoogleProtocol.obj("model", "opencode/deepseek-v4-flash", "input", history).toString());
    assertEquals(2, calls.get());
  }

  @Test
  public void responsesStreamKeepsTwoSignedThinkingBlocksSeparate() throws Exception {
    startSource(
        "opencode",
        (method, url, headers, body, deadline) -> {
          StringBuilder events = new StringBuilder();
          events.append(
              "data:"
                  + " {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1}}}\n\n");
          for (int i = 0; i < 2; i++) {
            events
                .append("data: ")
                .append(
                    GoogleProtocol.obj(
                        "type",
                        "content_block_start",
                        "index",
                        i,
                        "content_block",
                        GoogleProtocol.obj("type", "thinking", "thinking", "")))
                .append("\n\n");
            events
                .append("data: ")
                .append(
                    GoogleProtocol.obj(
                        "type",
                        "content_block_delta",
                        "index",
                        i,
                        "delta",
                        GoogleProtocol.obj("type", "thinking_delta", "thinking", "plan" + i)))
                .append("\n\n");
            events
                .append("data: ")
                .append(
                    GoogleProtocol.obj(
                        "type",
                        "content_block_delta",
                        "index",
                        i,
                        "delta",
                        GoogleProtocol.obj(
                            "type", "signature_delta", "signature", "signature" + i)))
                .append("\n\n");
            events
                .append("data: ")
                .append(GoogleProtocol.obj("type", "content_block_stop", "index", i))
                .append("\n\n");
          }
          events.append(
              "data:"
                  + " {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n"
                  + "data: {\"type\":\"message_stop\"}\n\n");
          return new GoogleSessions.Response(
              200, new ByteArrayInputStream(events.toString().getBytes(StandardCharsets.UTF_8)));
        });
    String result =
        rawCall(
            "/v1/responses",
            GoogleProtocol.obj("model", "opencode/claude-opus-4-8", "input", "hi", "stream", true)
                .toString());
    assertTrue(result, result.contains("event: response.completed"));
    assertFalse(result, result.contains("response.failed"));
    JSONObject completed = null;
    for (String line : result.split("\n"))
      if (line.startsWith("data: ")) {
        JSONObject event = new JSONObject(line.substring(6));
        if (event.getString("type").equals("response.completed"))
          completed = event.getJSONObject("response");
      }
    assertNotNull(completed);
    JSONArray output = completed.getJSONArray("output");
    assertEquals(2, output.length());
    for (int i = 0; i < 2; i++)
      assertEquals(
          "signature" + i,
          output
              .getJSONObject(i)
              .getJSONArray("reasoning_details")
              .getJSONObject(0)
              .getString("signature"));
  }

  @Test
  public void nativeMessagesRejectInvalidCompletedToolArguments() throws Exception {
    java.util.concurrent.atomic.AtomicBoolean limited =
        new java.util.concurrent.atomic.AtomicBoolean();
    startSource(
        "opencode",
        (method, url, headers, body, deadline) -> {
          String data =
              "data: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-opus-4-8\"}}\n\n"
                  + "data:"
                  + " {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"c1\",\"name\":\"weather\",\"input\":{}}}\n\n"
                  + "data:"
                  + " {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\"}}\n\n"
                  + "data: {\"type\":\"content_block_stop\",\"index\":0}\n\n"
                  + "data: "
                  + GoogleProtocol.obj(
                      "type",
                      "message_delta",
                      "delta",
                      GoogleProtocol.obj("stop_reason", limited.get() ? "max_tokens" : "tool_use"))
                  + "\n\ndata: {\"type\":\"message_stop\"}\n\n";
          return new GoogleSessions.Response(
              200, new ByteArrayInputStream(data.getBytes(StandardCharsets.UTF_8)));
        });
    for (boolean truncated : new boolean[] {false, true}) {
      limited.set(truncated);
      String result =
          rawCall(
              "/v1/messages",
              GoogleProtocol.obj(
                      "model",
                      "opencode/claude-opus-4-8",
                      "messages",
                      GoogleProtocol.arr(GoogleProtocol.obj("role", "user", "content", "hi")),
                      "stream",
                      true,
                      "max_tokens",
                      100)
                  .toString());
      assertEquals(result, truncated, result.contains("event: message_stop"));
      assertEquals(result, !truncated, result.contains("event: error"));
    }
  }

  @Test
  public void protocolStreamsKeepFragmentedToolsUsageAndExplicitTruncation() throws Exception {
    java.util.concurrent.atomic.AtomicReference<String> finish =
        new java.util.concurrent.atomic.AtomicReference<>("tool_calls");
    startSource(
        "opencode",
        (method, url, headers, body, deadline) -> {
          JSONObject request = new JSONObject(new String(body, StandardCharsets.UTF_8));
          assertTrue(request.getBoolean("stream"));
          assertTrue(request.getJSONObject("stream_options").getBoolean("include_usage"));
          String args = finish.get().equals("length") ? "{" : "{}";
          String data =
              "data: "
                  + GoogleProtocol.obj(
                      "choices",
                      GoogleProtocol.arr(
                          GoogleProtocol.obj(
                              "index",
                              0,
                              "delta",
                              GoogleProtocol.obj(
                                  "content",
                                  "hello",
                                  "tool_calls",
                                  GoogleProtocol.arr(
                                      GoogleProtocol.obj(
                                          "index",
                                          0,
                                          "id",
                                          "c1",
                                          "function",
                                          GoogleProtocol.obj("name", "wea", "arguments", "")))))))
                  + "\n\n"
                  + "data: "
                  + GoogleProtocol.obj(
                      "choices",
                      GoogleProtocol.arr(
                          GoogleProtocol.obj(
                              "index",
                              0,
                              "delta",
                              GoogleProtocol.obj(
                                  "tool_calls",
                                  GoogleProtocol.arr(
                                      GoogleProtocol.obj(
                                          "index",
                                          0,
                                          "function",
                                          GoogleProtocol.obj("name", "ther", "arguments", args),
                                          "thought_signature",
                                          "signature"))))))
                  + "\n\n"
                  + "data: "
                  + GoogleProtocol.obj(
                      "choices",
                      GoogleProtocol.arr(
                          GoogleProtocol.obj(
                              "index",
                              0,
                              "delta",
                              new JSONObject(),
                              "finish_reason",
                              finish.get())),
                      "usage",
                      GoogleProtocol.obj(
                          "prompt_tokens", 2, "completion_tokens", 3, "total_tokens", 5))
                  + "\n\ndata: [DONE]\n\n";
          return new GoogleSessions.Response(
              200, new ByteArrayInputStream(data.getBytes(StandardCharsets.UTF_8)));
        });
    for (String ending : new String[] {"tool_calls", "length", "unexpected"}) {
      finish.set(ending);
      for (String protocol : new String[] {"responses", "messages"}) {
        JSONObject input =
            GoogleProtocol.obj("model", "opencode/deepseek-v4-flash", "stream", true);
        if (protocol.equals("responses")) input.put("input", "hi");
        else
          input
              .put(
                  "messages",
                  GoogleProtocol.arr(GoogleProtocol.obj("role", "user", "content", "hi")))
              .put("max_tokens", 100);
        String result = rawCall("/v1/" + protocol, input.toString());
        assertTrue(result, result.contains("hello"));
        if (ending.equals("unexpected")) {
          assertFalse(
              result,
              result.contains("event: response.completed")
                  || result.contains("event: message_stop"));
          assertTrue(
              result, result.contains("event: error") || result.contains("event: response.failed"));
        } else {
          assertTrue(
              result,
              result.contains("weather")
                  && result.contains("signature")
                  && result.contains("output_tokens"));
          assertTrue(
              result,
              result.contains(
                  protocol.equals("responses")
                      ? (ending.equals("length")
                          ? "event: response.incomplete"
                          : "event: response.completed")
                      : "event: message_stop"));
          if (ending.equals("length"))
            assertTrue(
                result,
                result.contains(protocol.equals("responses") ? "max_output_tokens" : "max_tokens"));
        }
      }
    }
  }

  @Test
  public void responsesAndMessagesClientsUseTheSelectedChatBackendWithoutLosingTools()
      throws Exception {
    java.util.concurrent.atomic.AtomicInteger count =
        new java.util.concurrent.atomic.AtomicInteger();
    startSource(
        "opencode",
        (method, url, headers, body, deadline) -> {
          count.incrementAndGet();
          assertTrue(url.endsWith("/chat/completions"));
          JSONObject packet = new JSONObject(new String(body, StandardCharsets.UTF_8));
          assertEquals("deepseek-v4-flash", packet.getString("model"));
          assertEquals(
              "weather",
              packet
                  .getJSONArray("tools")
                  .getJSONObject(0)
                  .getJSONObject("function")
                  .getString("name"));
          String reply =
              "{\"choices\":[{\"message\":{\"role\":\"assistant\",\"content\":\"hello\",\"reasoning_content\":\"plan\",\"tool_calls\":[{\"id\":\"c1\",\"type\":\"function\",\"function\":{\"name\":\"weather\",\"arguments\":\"{}\"},\"thought_signature\":\"opaque-signature\"}]},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":5,\"total_tokens\":13}}";
          return new GoogleSessions.Response(
              200, new ByteArrayInputStream(reply.getBytes(StandardCharsets.UTF_8)));
        });
    for (String protocol : new String[] {"responses", "messages"}) {
      JSONObject input = GoogleProtocol.obj("model", "opencode/deepseek-v4-flash");
      if (protocol.equals("responses"))
        input
            .put("input", "hi")
            .put("max_output_tokens", 100)
            .put(
                "tools",
                GoogleProtocol.arr(
                    GoogleProtocol.obj(
                        "type",
                        "function",
                        "name",
                        "weather",
                        "parameters",
                        GoogleProtocol.obj("type", "object"))));
      else
        input
            .put(
                "messages", GoogleProtocol.arr(GoogleProtocol.obj("role", "user", "content", "hi")))
            .put("max_tokens", 100)
            .put(
                "tools",
                GoogleProtocol.arr(
                    GoogleProtocol.obj(
                        "name", "weather", "input_schema", GoogleProtocol.obj("type", "object"))));
      JSONObject result = call("/v1/" + protocol, input.toString());
      assertEquals("opencode/deepseek-v4-flash", result.getString("model"));
      assertTrue(
          result.toString(),
          result.toString().contains("hello")
              && result.toString().contains("plan")
              && result.toString().contains("opaque-signature"));
      assertEquals(5, result.getJSONObject("usage").getInt("output_tokens"));
      if (protocol.equals("responses")) {
        assertEquals("completed", result.getString("status"));
        assertEquals(
            "function_call", result.getJSONArray("output").getJSONObject(2).getString("type"));
        input.put("previous_response_id", "missing-history");
      } else {
        assertEquals("tool_use", result.getString("stop_reason"));
        assertEquals("tool_use", result.getJSONArray("content").getJSONObject(2).getString("type"));
        input.put("mcp_servers", new JSONArray());
      }
      rawCall("/v1/" + protocol, input.toString(), 400);
    }
    assertEquals(2, count.get());
  }

  @Test
  public void nativeResponsesAndMessagesStreamsPreserveProviderItemsAndRequireCompletion()
      throws Exception {
    java.util.concurrent.atomic.AtomicBoolean truncated =
        new java.util.concurrent.atomic.AtomicBoolean();
    startSource(
        "opencode",
        (method, url, headers, body, deadline) -> {
          JSONObject input = new JSONObject(new String(body, StandardCharsets.UTF_8));
          String events;
          if (url.endsWith("/responses")) {
            assertEquals("resp_previous", input.getString("previous_response_id"));
            events =
                "data:"
                    + " {\"type\":\"response.created\",\"response\":{\"id\":\"r1\",\"model\":\"gpt-5.4\",\"status\":\"in_progress\"}}\n\n"
                    + "data:"
                    + " {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"reasoning\",\"id\":\"rs1\",\"encrypted_content\":\"opaque-state\",\"summary\":[]}}\n\n"
                    + (truncated.get()
                        ? ""
                        : "data:"
                              + " {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"model\":\"gpt-5.4\",\"status\":\"completed\",\"output\":[{\"type\":\"reasoning\",\"encrypted_content\":\"opaque-state\",\"summary\":[]}]}}\n\n");
          } else {
            assertTrue(url.endsWith("/messages"));
            assertEquals("2023-06-01", headers.get("anthropic-version"));
            events =
                "data:"
                    + " {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-opus-4-8\",\"content\":[]}}\n\n"
                    + "data:"
                    + " {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"redacted_thinking\",\"data\":\"opaque-state\"}}\n\n"
                    + "data: {\"type\":\"content_block_stop\",\"index\":0}\n\n"
                    + (truncated.get()
                        ? ""
                        : "data:"
                              + " {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n"
                              + "data: {\"type\":\"message_stop\"}\n\n");
          }
          return new GoogleSessions.Response(
              200, new ByteArrayInputStream(events.getBytes(StandardCharsets.UTF_8)));
        });
    for (boolean missing : new boolean[] {false, true}) {
      truncated.set(missing);
      for (String protocol : new String[] {"responses", "messages"}) {
        JSONObject request =
            GoogleProtocol.obj(
                "model",
                "opencode/" + (protocol.equals("responses") ? "gpt-5.4" : "claude-opus-4-8"),
                "stream",
                true);
        if (protocol.equals("responses"))
          request.put("input", "hi").put("previous_response_id", "resp_previous");
        else
          request
              .put(
                  "messages",
                  GoogleProtocol.arr(GoogleProtocol.obj("role", "user", "content", "hi")))
              .put("max_tokens", 100);
        String result = rawCall("/v1/" + protocol, request.toString());
        assertTrue(result, result.contains("opaque-state"));
        assertTrue(result, result.replace("\\/", "/").contains(request.getString("model")));
        String terminal =
            protocol.equals("responses") ? "event: response.completed" : "event: message_stop";
        assertEquals(result, !missing, result.contains(terminal));
        assertEquals(result, missing, result.contains("event: error"));
      }
    }
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
  public void disconnectedClientCancelsActualProviderHttpRead() throws Exception {
    for (String path : new String[] {"/v1/chat/completions", "/v1/responses", "/v1/messages"}) {
      try {
        assertDisconnect(path);
      } finally {
        if (server != null) server.close();
      }
    }
  }

  private void assertDisconnect(String path) throws Exception {
    try (okhttp3.mockwebserver.MockWebServer upstream = new okhttp3.mockwebserver.MockWebServer()) {
      upstream.start(java.net.InetAddress.getByName("127.0.0.1"), 0);
      upstream.enqueue(
          new okhttp3.mockwebserver.MockResponse()
              .setBody("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n")
              .setHeader("Content-Length", 1000000));
      java.util.concurrent.CountDownLatch closed = new java.util.concurrent.CountDownLatch(1);
      startSource(
          "opencode",
          (method, url, headers, body, deadline) -> {
            GoogleSessions.Response response =
                NativeHttp.send(
                    method,
                    "http://127.0.0.1:" + upstream.getPort() + "/chat/completions",
                    headers,
                    body,
                    Math.min(deadline, System.currentTimeMillis() + 6000));
            return new GoogleSessions.Response(
                response.status,
                new java.io.FilterInputStream(response.body) {
                  @Override
                  public void close() throws java.io.IOException {
                    try {
                      super.close();
                    } finally {
                      closed.countDown();
                    }
                  }
                });
          });
      try (java.net.Socket client = new java.net.Socket("127.0.0.1", 8765)) {
        client.setSoTimeout(3000);
        JSONObject input =
            GoogleProtocol.obj("model", "opencode/deepseek-v4-flash", "stream", true);
        if (path.equals("/v1/responses")) input.put("input", "hi");
        else
          input.put(
              "messages", GoogleProtocol.arr(GoogleProtocol.obj("role", "user", "content", "hi")));
        byte[] body = input.toString().getBytes(StandardCharsets.UTF_8);
        java.io.OutputStream out = client.getOutputStream();
        out.write(
            ("POST "
                    + path
                    + " HTTP/1.1\r\n"
                    + "Host: 127.0.0.1:8765\r\n"
                    + "Authorization: Bearer client\r\n"
                    + "Content-Length: "
                    + body.length
                    + "\r\n\r\n")
                .getBytes(StandardCharsets.US_ASCII));
        out.write(body);
        out.flush();
        java.io.BufferedReader reader =
            new java.io.BufferedReader(
                new java.io.InputStreamReader(client.getInputStream(), StandardCharsets.UTF_8));
        String line;
        boolean first = false;
        while ((line = reader.readLine()) != null)
          if (line.startsWith("data:") && line.contains("hello")) {
            first = true;
            break;
          }
        assertTrue("Incremental provider data was not forwarded", first);
      }
      assertTrue(
          "Upstream remained blocked after client disconnect",
          closed.await(3, java.util.concurrent.TimeUnit.SECONDS));
    }
  }

  @Test
  public void transportDoesNotReplayGenerationOnRetryAfterOrRedirect() throws Exception {
    try (okhttp3.mockwebserver.MockWebServer upstream = new okhttp3.mockwebserver.MockWebServer()) {
      upstream.start(java.net.InetAddress.getByName("127.0.0.1"), 0);
      java.util.concurrent.atomic.AtomicInteger status =
          new java.util.concurrent.atomic.AtomicInteger(503);
      upstream.setDispatcher(
          new okhttp3.mockwebserver.Dispatcher() {
            @Override
            public okhttp3.mockwebserver.MockResponse dispatch(
                okhttp3.mockwebserver.RecordedRequest request) {
              return new okhttp3.mockwebserver.MockResponse()
                  .setResponseCode(status.get())
                  .setHeader("Retry-After", "0")
                  .setHeader("Location", "http://127.0.0.1:" + upstream.getPort() + "/replayed")
                  .setBody("{\"error\":\"private-provider-details\"}");
            }
          });
      startSource(
          "opencode",
          (method, url, headers, body, deadline) ->
              NativeHttp.send(
                  method,
                  "http://127.0.0.1:" + upstream.getPort() + "/chat/completions",
                  headers,
                  body,
                  deadline));
      int count = 0;
      for (int code : new int[] {503, 307, 408}) {
        status.set(code);
        String result =
            rawCall(
                "/v1/chat/completions",
                "{\"model\":\"opencode/deepseek-v4-flash\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}",
                code == 307 ? 502 : code);
        assertFalse(result.contains("private-provider-details"));
        assertEquals(++count, upstream.getRequestCount());
      }
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
