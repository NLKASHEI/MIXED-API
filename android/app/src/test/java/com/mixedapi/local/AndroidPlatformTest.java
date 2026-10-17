package com.mixedapi.local;

import static org.junit.Assert.*;

import android.app.Notification;
import android.app.NotificationManager;
import android.content.ComponentName;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.graphics.Insets;
import android.view.View;
import android.view.WindowInsets;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 35)
public class AndroidPlatformTest {
  @Test
  public void consoleStaysOutsideSystemBarsAndKeyboard() throws Exception {
    BridgeService.running = true;
    try (var controller = Robolectric.buildActivity(MainActivity.class).setup()) {
      var activity = controller.get();
      assertEquals(35, activity.getApplicationInfo().targetSdkVersion);
      View root = activity.findViewById(android.R.id.content);
      root.dispatchApplyWindowInsets(
          new WindowInsets.Builder()
              .setInsets(WindowInsets.Type.systemBars(), Insets.of(0, 32, 0, 24))
              .setInsets(WindowInsets.Type.displayCutout(), Insets.of(12, 0, 0, 0))
              .setInsets(WindowInsets.Type.ime(), Insets.of(0, 0, 0, 280))
              .build());
      assertEquals(32, root.getPaddingTop());
      assertEquals(12, root.getPaddingLeft());
      assertEquals(280, root.getPaddingBottom());
      root.dispatchApplyWindowInsets(
          new WindowInsets.Builder()
              .setInsets(WindowInsets.Type.systemBars(), Insets.of(0, 32, 0, 24))
              .build());
      assertEquals(24, root.getPaddingBottom());
    } finally {
      BridgeService.running = false;
    }
  }

  @Test
  public void foregroundListenerHasExplicitStopAction() throws Exception {
    var controller = Robolectric.buildService(BridgeService.class).create();
    try {
      var service = controller.get();
      ServiceInfo info =
          service
              .getPackageManager()
              .getServiceInfo(new ComponentName(service, BridgeService.class), 0);
      assertEquals(
          ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE, info.getForegroundServiceType());
      service.onStartCommand(new Intent(service, BridgeService.class), 0, 1);
      Notification notification =
          Shadows.shadowOf(service.getSystemService(NotificationManager.class))
              .getNotification(2048);
      assertNotNull(notification);
      assertEquals(1, notification.actions.length);
      Intent stop = Shadows.shadowOf(notification.actions[0].actionIntent).getSavedIntent();
      assertEquals("stop", stop.getAction());
      service.onStartCommand(stop, 0, 2);
      assertTrue(Shadows.shadowOf(service).isStoppedBySelf());
    } finally {
      controller.destroy();
    }
  }
}
