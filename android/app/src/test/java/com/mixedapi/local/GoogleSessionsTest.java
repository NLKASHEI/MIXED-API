package com.mixedapi.local;

import static org.junit.Assert.*;
import static org.robolectric.Shadows.shadowOf;

import android.os.Looper;
import android.webkit.ValueCallback;
import android.webkit.WebView;
import java.lang.reflect.Field;
import java.time.Duration;
import java.util.Map;
import java.util.concurrent.Callable;
import java.util.concurrent.FutureTask;
import java.util.concurrent.TimeUnit;
import org.json.JSONObject;
import org.junit.After;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.LooperMode;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 28)
@LooperMode(LooperMode.Mode.PAUSED)
public class GoogleSessionsTest {
  private final GoogleSessions sessions = new GoogleSessions();

  // Substitute the native renderer boundary, never a real provider or login.
  static final class Renderer extends WebView {
    ValueCallback<String> abort;
    boolean destroyed;
    int starts;

    Renderer() {
      super(RuntimeEnvironment.getApplication());
    }

    @Override
    public void evaluateJavascript(String script, ValueCallback<String> callback) {
      if (script.contains(".cancel(") || script.contains(".cancelInitialize(")) {
        abort = callback;
        return;
      }
      if (script.contains(".start(")) {
        starts++;
        callback.onReceiveValue("true");
      } else if (script.contains(".read(")) callback.onReceiveValue("{\"status\":200}");
      else callback.onReceiveValue("{\"status\":\"ready\"}");
    }

    @Override
    public void destroy() {
      destroyed = true;
      super.destroy();
    }
  }

  @SuppressWarnings("unchecked")
  private void install(Renderer view) throws Exception {
    Class<?> type = Class.forName("com.mixedapi.local.GoogleSessions$Page");
    var constructor = type.getDeclaredConstructor();
    constructor.setAccessible(true);
    Object page = constructor.newInstance();
    Field renderer = type.getDeclaredField("view");
    renderer.setAccessible(true);
    renderer.set(page, view);
    Field pages = GoogleSessions.class.getDeclaredField("pages");
    pages.setAccessible(true);
    ((Map<String, Object>) pages.get(sessions)).put("aps", page);
  }

  private <T> FutureTask<T> start(Callable<T> call) {
    FutureTask<T> future = new FutureTask<>(call);
    Thread worker = new Thread(future, "renderer-test");
    worker.setDaemon(true);
    worker.start();
    return future;
  }

  private <T> T finish(FutureTask<T> future) throws Exception {
    long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(3);
    while (!future.isDone() && System.nanoTime() < deadline) {
      shadowOf(Looper.getMainLooper()).idle();
      Thread.sleep(1);
    }
    return future.get(100, TimeUnit.MILLISECONDS);
  }

  private GoogleSessions.Response send() throws Exception {
    return finish(
        start(
            () ->
                sessions.send(
                    "aps",
                    new JSONObject().put("kind", "aps"),
                    System.currentTimeMillis() + 3000)));
  }

  @After
  public void cleanup() {
    sessions.close();
    shadowOf(Looper.getMainLooper()).idle();
  }

  @Test
  public void initializationWithoutAbortAcknowledgementDestroysPage() throws Exception {
    Renderer view = new Renderer();
    install(view);
    assertEquals("ready", finish(start(() -> sessions.initialize("aps"))).getString("status"));
    shadowOf(Looper.getMainLooper()).idle();
    assertNotNull(view.abort);
    view.abort.onReceiveValue("null");
    assertTrue("null is not an abort acknowledgement", view.destroyed);
    assertEquals("none", finish(start(() -> sessions.state("aps"))).getString("status"));
  }

  @Test
  public void missingAbortCallbackRetiresPageBeforeAdmittingAnotherRequest() throws Exception {
    Renderer old = new Renderer();
    install(old);
    GoogleSessions.Response first = send();
    first.close();
    shadowOf(Looper.getMainLooper()).idle();
    assertNotNull(old.abort);
    FutureTask<Boolean> queued =
        start(
            () -> {
              try (var ignored =
                  sessions.send(
                      "aps",
                      new JSONObject().put("kind", "aps"),
                      System.currentTimeMillis() + 3000)) {
                return true;
              } catch (Exception expected) {
                return false;
              }
            });
    shadowOf(Looper.getMainLooper()).idleFor(Duration.ofSeconds(2));
    assertTrue("unresponsive renderer must be destroyed", old.destroyed);
    assertFalse("old page must never accept the queued request", finish(queued));
    assertEquals(1, old.starts);
    assertEquals("none", finish(start(() -> sessions.state("aps"))).getString("status"));
    Renderer replacement = new Renderer();
    install(replacement);
    old.abort.onReceiveValue("null"); // late completion cannot affect the replacement
    try (var response = send()) {
      assertEquals(200, response.status);
    }
    shadowOf(Looper.getMainLooper()).idle();
    replacement.abort.onReceiveValue("true");
    assertFalse(replacement.destroyed);
  }

  @Test
  public void confirmedAbortKeepsSessionAndReleasesLeaseOnce() throws Exception {
    Renderer view = new Renderer();
    install(view);
    GoogleSessions.Response first = send();
    first.close();
    first.close();
    shadowOf(Looper.getMainLooper()).idle();
    view.abort.onReceiveValue("true");
    shadowOf(Looper.getMainLooper()).idleFor(Duration.ofSeconds(2));
    assertFalse(view.destroyed);
    try (var response = send()) {
      assertEquals(200, response.status);
    }
    shadowOf(Looper.getMainLooper()).idle();
    view.abort.onReceiveValue("true");
    assertEquals(2, view.starts);
  }
}
