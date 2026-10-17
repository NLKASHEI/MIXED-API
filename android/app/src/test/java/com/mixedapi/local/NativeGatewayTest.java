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
  @After public void stop() { if (server != null) server.close(); }

  @Test public void androidShipsSixOptionalNativeSources() throws Exception {
    Context ctx = RuntimeEnvironment.getApplication();
    ctx.getSharedPreferences("mixed_config", Context.MODE_PRIVATE).edit().clear().commit();
    JSONObject config = BridgeConfig.load(ctx);
    JSONArray sources = config.getJSONArray("backends");
    int nativeCount = 0;
    for (int i=0;i<sources.length();i++) {
      JSONObject source = sources.getJSONObject(i);
      if (source.getString("id").equals("pc")) continue;
      assertFalse(source.optBoolean("enabled"));
      assertEquals("builtin", source.optString("kind"));
      assertTrue(source.getString("url").startsWith("https://"));
      nativeCount++;
    }
    assertEquals(6,nativeCount);
  }
}
