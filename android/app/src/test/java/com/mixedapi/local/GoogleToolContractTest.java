package com.mixedapi.local;

import static com.mixedapi.local.GoogleProtocol.*;
import static org.junit.Assert.*;

import android.content.Context;
import java.io.ByteArrayInputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 28)
public class GoogleToolContractTest {
  @Test
  public void explicitChoicesAreEnforcedForFullAndStreamResponses() throws Exception {
    Context ctx = RuntimeEnvironment.getApplication();
    ctx.getSharedPreferences("mixed_config", Context.MODE_PRIVATE).edit().clear().commit();
    JSONObject cfg = BridgeConfig.load(ctx).put("api_key", "client").put("admin_key", "admin");
    for (int i = 0; i < cfg.getJSONArray("backends").length(); i++) {
      JSONObject b = cfg.getJSONArray("backends").getJSONObject(i);
      if (b.getString("id").equals("vertex") || b.getString("id").equals("aistudio"))
        b.put("enabled", true).put("provider_key", "fixture");
    }
    BridgeConfig.save(ctx, cfg);
    String[] part = {""};
    int[] count = {0};
    boolean[] stream = {false};
    boolean[] none = {false};
    GoogleGateway.Transport pages =
        new GoogleGateway.Transport() {
          public void open(Context c, String id) {}

          public JSONObject state(String id) throws Exception {
            return obj("status", "ready");
          }

          public GoogleSessions.Response send(String id, JSONObject op, long deadline)
              throws Exception {
            JSONArray wire = op.getJSONArray("body");
            if (none[0]) assertTrue(wire.isNull(6) || wire.getJSONArray(6).length() == 0);
            JSONArray parts = new JSONArray();
            if (count[0] == 0) parts.put(arr(null, "result"));
            else
              for (int n = 0; n < count[0]; n++) {
                JSONArray p = new JSONArray();
                p.put(10, arr(part[0], arr(new JSONArray()), null));
                parts.put(p);
              }
            return response(
                arr(arr(arr(arr(arr(arr(parts, "model"), 1)), null, arr(2, 1, 3)))).toString());
          }
        };
    NativeGateway.Transport http =
        (method, url, headers, body, deadline) -> {
          JSONArray parts = new JSONArray();
          if (count[0] == 0) parts.put(obj("text", "result"));
          else
            for (int n = 0; n < count[0]; n++)
              parts.put(obj("functionCall", obj("name", part[0], "args", new JSONObject())));
          String value =
              obj(
                      "candidates",
                      arr(obj("index", 0, "content", obj("parts", parts), "finishReason", "STOP")))
                  .toString();
          return response(stream[0] ? "data: " + value + "\n\n" : value);
        };
    try (BridgeHttpServer server = new BridgeHttpServer(ctx, pages, http)) {
      server.start();
      for (String model : new String[] {"vertex/gemini-test", "aistudio/playground/gemini-test"})
        for (boolean streaming : new boolean[] {false, true})
          for (int test = 0; test < 6; test++) {
            stream[0] = streaming;
            none[0] = test == 3 || test == 4;
            part[0] = test == 1 ? "other" : "lookup";
            count[0] = (test == 2 || test == 3) ? 0 : (test == 5 ? 2 : 1);
            Object choice =
                none[0]
                    ? "none"
                    : (test == 2 || test == 5)
                        ? "required"
                        : obj("type", "function", "function", obj("name", "lookup"));
            JSONObject input =
                obj(
                    "model",
                    model,
                    "messages",
                    arr(obj("role", "user", "content", "hi")),
                    "tools",
                    arr(
                        obj(
                            "type",
                            "function",
                            "function",
                            obj("name", "lookup", "parameters", obj("type", "object"))),
                        obj(
                            "type",
                            "function",
                            "function",
                            obj("name", "other", "parameters", obj("type", "object")))),
                    "tool_choice",
                    choice,
                    "parallel_tool_calls",
                    false,
                    "stream",
                    streaming);
            HttpURLConnection c =
                (HttpURLConnection)
                    new URL("http://127.0.0.1:8765/v1/chat/completions").openConnection();
            c.setReadTimeout(5000);
            c.setRequestMethod("POST");
            c.setRequestProperty("Authorization", "Bearer client");
            c.setDoOutput(true);
            try {
              try (var out = c.getOutputStream()) {
                out.write(input.toString().getBytes(StandardCharsets.UTF_8));
              }
              int status = c.getResponseCode();
              String data;
              try (var in = status >= 400 ? c.getErrorStream() : c.getInputStream()) {
                data = new String(in.readAllBytes(), StandardCharsets.UTF_8);
              }
              if (test == 0 || test == 3) {
                assertEquals(model + data, 200, status);
                if (streaming) assertTrue(data, data.contains("[DONE]"));
              } else
                assertTrue(
                    model + data,
                    status == 502
                        || streaming
                            && status == 200
                            && data.contains("\"error\"")
                            && !data.contains("[DONE]"));
            } finally {
              c.disconnect();
            }
          }
    }
  }

  private static GoogleSessions.Response response(String value) {
    return new GoogleSessions.Response(
        200, new ByteArrayInputStream(value.getBytes(StandardCharsets.UTF_8)));
  }
}
