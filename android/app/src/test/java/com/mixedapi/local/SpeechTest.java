package com.mixedapi.local;

import static org.junit.Assert.*;

import android.content.Context;
import java.io.ByteArrayInputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 28)
public class SpeechTest {
  @Test
  public void allGoogleCapabilitiesReturnPlayableWaveAndPcm() throws Exception {
    Context ctx = RuntimeEnvironment.getApplication();
    ctx.getSharedPreferences("mixed_config", Context.MODE_PRIVATE).edit().clear().commit();
    JSONObject config = BridgeConfig.load(ctx).put("api_key", "client").put("admin_key", "admin");
    for (int i = 0; i < config.getJSONArray("backends").length(); i++) {
      JSONObject b = config.getJSONArray("backends").getJSONObject(i);
      if (java.util.Arrays.asList("vertex", "aps", "aistudio").contains(b.getString("id")))
        b.put("enabled", true).put("provider_key", "fixture-key");
    }
    BridgeConfig.save(ctx, config);
    String gemini =
        "{\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"inlineData\":{\"mimeType\":\"audio/L16;codec=pcm;rate=24000\",\"data\":\"AAABAA==\"}}]},\"finishReason\":\"STOP\"}]}";
    GoogleGateway.Transport pages =
        new GoogleGateway.Transport() {
          public void open(Context context, String id) {}

          public JSONObject state(String id) throws Exception {
            return GoogleProtocol.obj("status", "ready");
          }

          public GoogleSessions.Response send(String id, JSONObject operation, long deadline)
              throws Exception {
            assertTrue(operation.toString().contains("Kore"));
            String data;
            switch (operation.getString("kind")) {
              case "build":
                data = GoogleProtocol.arr(gemini).toString();
                break;
              case "playground":
                data =
                    "[[[[[[[[null,null,[\"audio/L16;codec=pcm;rate=24000\",\"AAABAA==\"]]],\"model\"],1]],null,[2,1,3]]]]";
                break;
              default:
                data =
                    "[{\"results\":[{\"data\":{\"ui\":{\"streamGenerateContentAnonymous\":"
                        + gemini
                        + "}}}]}]";
            }
            return new GoogleSessions.Response(
                200, new ByteArrayInputStream(data.getBytes(StandardCharsets.UTF_8)));
          }
        };
    NativeGateway.Transport nativeHttp =
        (method, url, headers, body, deadline) -> {
          assertTrue(new String(body, StandardCharsets.UTF_8).contains("Kore"));
          return new GoogleSessions.Response(
              200, new ByteArrayInputStream(gemini.getBytes(StandardCharsets.UTF_8)));
        };
    try (BridgeHttpServer server = new BridgeHttpServer(ctx, pages, nativeHttp)) {
      server.start();
      for (String model :
          new String[] {
            "vertex/gemini-tts",
            "aps/gemini-tts",
            "aistudio/build/gemini-tts",
            "aistudio/playground/gemini-tts"
          })
        for (String format : new String[] {"wav", "pcm"}) {
          HttpURLConnection connection =
              (HttpURLConnection) new URL("http://127.0.0.1:8765/v1/audio/speech").openConnection();
          connection.setReadTimeout(3000);
          connection.setRequestProperty("Authorization", "Bearer client");
          connection.setRequestMethod("POST");
          connection.setDoOutput(true);
          try (var output = connection.getOutputStream()) {
            output.write(
                GoogleProtocol.obj(
                        "model",
                        model,
                        "input",
                        "hello",
                        "voice",
                        "Kore",
                        "response_format",
                        format)
                    .toString()
                    .getBytes(StandardCharsets.UTF_8));
          }
          try {
            assertEquals(model, 200, connection.getResponseCode());
            byte[] audio = connection.getInputStream().readAllBytes();
            assertEquals(format.equals("wav") ? 48 : 4, audio.length);
            assertArrayEquals(
                new byte[] {0, 0, 1, 0},
                java.util.Arrays.copyOfRange(audio, audio.length - 4, audio.length));
            if (format.equals("wav")) {
              assertEquals("RIFF", new String(audio, 0, 4, StandardCharsets.US_ASCII));
              assertEquals(
                  24000,
                  java.nio.ByteBuffer.wrap(audio)
                      .order(java.nio.ByteOrder.LITTLE_ENDIAN)
                      .getInt(24));
            }
          } finally {
            connection.disconnect();
          }
        }
    }
  }
}
