package com.mixedapi.local;

import static org.junit.Assert.*;

import android.app.Activity;
import android.content.Intent;
import android.net.Uri;
import android.webkit.WebView;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 28)
public class MainActivityTest {
  @Test
  public void localConsoleCanSelectAndCancelAnAccountFile() {
    BridgeService.running = true;
    try (org.robolectric.android.controller.ActivityController<MainActivity> controller =
        Robolectric.buildActivity(MainActivity.class).setup()) {
      MainActivity activity = controller.get();
      WebView view =
          (WebView)
              ((android.view.ViewGroup) activity.findViewById(android.R.id.content)).getChildAt(0);
      java.util.concurrent.atomic.AtomicReference<Uri[]> chosen =
          new java.util.concurrent.atomic.AtomicReference<>();
      assertTrue(
          Shadows.shadowOf(view).getWebChromeClient().onShowFileChooser(view, chosen::set, null));
      org.robolectric.shadows.ShadowActivity.IntentForResult request =
          Shadows.shadowOf(activity).getNextStartedActivityForResult();
      assertEquals(Intent.ACTION_OPEN_DOCUMENT, request.intent.getAction());
      assertEquals("application/json", request.intent.getType());
      Uri uri = Uri.parse("content://documents/auth.json");
      Shadows.shadowOf(activity)
          .receiveResult(request.intent, Activity.RESULT_OK, new Intent().setData(uri));
      assertArrayEquals(new Uri[] {uri}, chosen.get());
      java.util.concurrent.atomic.AtomicInteger cancelled =
          new java.util.concurrent.atomic.AtomicInteger();
      assertTrue(
          Shadows.shadowOf(view)
              .getWebChromeClient()
              .onShowFileChooser(
                  view,
                  value -> {
                    assertNull(value);
                    cancelled.incrementAndGet();
                  },
                  null));
      controller.pause().stop().destroy();
      assertEquals(1, cancelled.get());
    } finally {
      BridgeService.running = false;
    }
  }
}
