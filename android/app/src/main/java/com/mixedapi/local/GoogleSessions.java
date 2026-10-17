package com.mixedapi.local;

import android.content.Context;
import android.content.Intent;
import android.net.Uri;
import android.os.Handler;
import android.os.Looper;
import android.webkit.PermissionRequest;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import androidx.webkit.WebViewCompat;
import androidx.webkit.WebViewFeature;
import java.io.IOException;
import java.io.InputStream;
import java.util.HashMap;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.Semaphore;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;
import org.json.JSONObject;
import org.json.JSONTokener;

/** Native-owned bounded request transport. No JavascriptInterface is installed. */
public final class GoogleSessions implements AutoCloseable, GoogleGateway.Transport {
  private static final GoogleSessions INSTANCE = new GoogleSessions();

  public static GoogleSessions shared() {
    return INSTANCE;
  }

  private final Handler ui = new Handler(Looper.getMainLooper());
  private final Map<String, Page> pages = new HashMap<>();

  private static final class Page {
    WebView view;
    final Semaphore gate = new Semaphore(1, true);
  }

  public static boolean supported() {
    try {
      return WebViewFeature.isFeatureSupported(WebViewFeature.MULTI_PROFILE)
          && WebViewFeature.isFeatureSupported(WebViewFeature.DOCUMENT_START_SCRIPT);
    } catch (RuntimeException e) {
      return false;
    }
  }

  private static String origin(String id) {
    if ("aistudio".equals(id)) return "https://aistudio.google.com";
    if ("aps".equals(id)) return "https://console.cloud.google.com";
    throw new IllegalArgumentException("Unknown source");
  }

  static boolean navigation(String id, String raw, boolean frame) {
    try {
      Uri u = Uri.parse(raw);
      if (!"https".equals(u.getScheme()) || u.getUserInfo() != null || u.getPort() != -1)
        return false;
      String host = u.getHost();
      return "accounts.google.com".equals(host)
          || origin(id).equals("https://" + host)
          || frame
              && java.util.Arrays.asList(
                      "www.google.com",
                      "www.gstatic.com",
                      "recaptcha.google.com",
                      "www.recaptcha.net")
                  .contains(host);
    } catch (Exception e) {
      return false;
    }
  }

  private <T> T onUI(java.util.concurrent.Callable<T> operation, long wait) throws Exception {
    if (Looper.myLooper() == Looper.getMainLooper()) return operation.call();
    CountDownLatch done = new CountDownLatch(1);
    AtomicReference<T> value = new AtomicReference<>();
    AtomicReference<Exception> error = new AtomicReference<>();
    AtomicBoolean expired = new AtomicBoolean();
    Runnable action =
        () -> {
          try {
            if (!expired.get()) value.set(operation.call());
          } catch (Exception e) {
            error.set(e);
          } finally {
            done.countDown();
          }
        };
    ui.post(action);
    try {
      if (!done.await(wait, TimeUnit.MILLISECONDS)) throw new IOException("来源窗口响应超时");
      if (error.get() != null) throw error.get();
      return value.get();
    } finally {
      expired.set(true);
      ui.removeCallbacks(action);
    }
  }

  public void open(Context context, String id) throws Exception {
    final String origin = origin(id);
    onUI(
        () -> {
          if (!supported())
            throw new IOException("当前 Android System WebView 不支持隔离来源会话，请更新系统 WebView 后重试");
          Page p = pages.get(id);
          if (p == null) {
            p = new Page();
            p.view = new WebView(context.getApplicationContext());
            WebViewCompat.setProfile(p.view, "mixed-provider-" + id);
            WebSettings settings = p.view.getSettings();
            settings.setJavaScriptEnabled(true);
            settings.setDomStorageEnabled(true);
            settings.setAllowFileAccess(false);
            settings.setAllowContentAccess(false);
            settings.setMixedContentMode(WebSettings.MIXED_CONTENT_NEVER_ALLOW);
            settings.setSupportMultipleWindows(false);
            p.view.setWebViewClient(
                new WebViewClient() {
                  @Override
                  public boolean shouldOverrideUrlLoading(
                      WebView view, WebResourceRequest request) {
                    return !navigation(id, request.getUrl().toString(), !request.isForMainFrame());
                  }
                });
            p.view.setWebChromeClient(
                new WebChromeClient() {
                  @Override
                  public void onPermissionRequest(PermissionRequest request) {
                    request.deny();
                  }
                });
            String script;
            try (InputStream in = context.getAssets().open("google-session.js")) {
              java.io.ByteArrayOutputStream bytes = new java.io.ByteArrayOutputStream();
              byte[] buffer = new byte[8192];
              int n;
              while ((n = in.read(buffer)) != -1) {
                if (bytes.size() + n > 256 * 1024) throw new IOException("会话脚本过大");
                bytes.write(buffer, 0, n);
              }
              script = bytes.toString("UTF-8");
            }
            WebViewCompat.addDocumentStartJavaScript(
                p.view, script, java.util.Collections.singleton(origin));
            pages.put(id, p);
            p.view.loadUrl(
                origin + ("aps".equals(id) ? "/vertex-ai/studio/multimodal" : "/prompts/new_chat"));
          }
          context.startActivity(
              new Intent(context, GoogleSourceActivity.class)
                  .putExtra("source", id)
                  .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK));
          return null;
        },
        10000);
  }

  WebView view(String id) {
    Page p = pages.get(id);
    if (p == null) throw new IllegalStateException("Source not open");
    return p.view;
  }

  private Page page(String id) throws Exception {
    return onUI(() -> pages.get(id), 3000);
  }

  private Object eval(Page p, String id, String expression, long wait) throws Exception {
    if (Looper.myLooper() == Looper.getMainLooper())
      throw new IOException("Session I/O cannot block UI");
    CountDownLatch done = new CountDownLatch(1);
    AtomicReference<String> value = new AtomicReference<>();
    AtomicReference<RuntimeException> error = new AtomicReference<>();
    AtomicBoolean expired = new AtomicBoolean();
    Runnable action =
        () -> {
          if (expired.get()) return;
          try {
            p.view.evaluateJavascript(
                "location.origin === "
                    + JSONObject.quote(origin(id))
                    + " ? ("
                    + expression
                    + ") : null",
                raw -> {
                  value.set(raw);
                  done.countDown();
                });
          } catch (RuntimeException e) {
            error.set(e);
            done.countDown();
          }
        };
    ui.post(action);
    try {
      if (!done.await(Math.max(1, wait), TimeUnit.MILLISECONDS)) throw new IOException("来源页面响应超时");
      if (error.get() != null) throw new IOException("来源窗口不可用");
      return new JSONTokener(value.get() == null ? "null" : value.get()).nextValue();
    } finally {
      expired.set(true);
      ui.removeCallbacks(action);
    }
  }

  public JSONObject state(String id) throws Exception {
    Page p = page(id);
    if (p == null) return new JSONObject().put("status", "none").put("message", "请打开来源授权窗口");
    Object raw = eval(p, id, "window.__mixedGoogleWeb?.state() || {status:'waiting'}", 3000);
    return raw instanceof JSONObject ? (JSONObject) raw : new JSONObject().put("status", "waiting");
  }

  public static final class Response implements AutoCloseable {
    public final int status;
    public final InputStream body;
    public final String retryAfter;

    Response(int code, InputStream stream) {
      this(code, stream, "");
    }

    Response(int code, InputStream stream, String retryAfter) {
      status = code;
      body = stream;
      this.retryAfter = retryAfter;
    }

    @Override
    public void close() throws IOException {
      body.close();
    }
  }

  public Response send(String id, JSONObject operation, long deadline) throws Exception {
    Page p = page(id);
    if (p == null) throw new IOException("请先打开来源授权窗口");
    String kind = operation.optString("kind");
    if ("aps".equals(id)
        ? !"aps".equals(kind)
        : !java.util.Arrays.asList("build", "playground", "models", "build_models").contains(kind))
      throw new IOException("来源操作不匹配");
    if (!p.gate.tryAcquire(
        Math.max(1, deadline - System.currentTimeMillis()), TimeUnit.MILLISECONDS))
      throw new IOException("来源请求排队超时");
    String key =
        UUID.randomUUID().toString().replace("-", "")
            + UUID.randomUUID().toString().replace("-", "").substring(0, 16);
    Body body = new Body(p, id, key, deadline);
    try {
      if (!Boolean.TRUE.equals(
          eval(
              p,
              id,
              "window.__mixedGoogleWeb?.start('" + key + "'," + operation + ") || false",
              body.remaining()))) throw new IOException("来源会话未准备好");
      for (; ; ) {
        JSONObject state = body.poll();
        int code = state.optInt("status");
        body.accept(state);
        if (code > 0) {
          JSONObject headers = state.optJSONObject("headers");
          return new Response(code, body, headers == null ? "" : headers.optString("retry-after"));
        }
        if (body.done) throw new IOException("来源请求失败，请检查官方页面");
        Thread.sleep(20);
      }
    } catch (Exception e) {
      if (e instanceof InterruptedException) Thread.currentThread().interrupt();
      body.close();
      throw e;
    }
  }

  private final class Body extends InputStream {
    final Page page;
    final String id, key;
    final long deadline;
    byte[] pending = new byte[0];
    int offset;
    boolean done, failed;
    final AtomicBoolean closed = new AtomicBoolean();

    Body(Page p, String id, String key, long deadline) {
      page = p;
      this.id = id;
      this.key = key;
      this.deadline = deadline;
    }

    long remaining() throws IOException {
      long value = deadline - System.currentTimeMillis();
      if (closed.get() || value <= 0 || Thread.currentThread().isInterrupted())
        throw new IOException("来源请求超时或取消");
      return Math.min(value, 3000);
    }

    JSONObject poll() throws Exception {
      Object raw =
          eval(
              page,
              id,
              "window.__mixedGoogleWeb?.read('" + key + "') || {done:true,error:'session_lost'}",
              remaining());
      if (!(raw instanceof JSONObject)) throw new IOException("来源页面不可用");
      return (JSONObject) raw;
    }

    void accept(JSONObject s) throws IOException {
      String chunk = s.optString("chunk");
      if (chunk.length() > 48 * 1024) throw new IOException("来源数据帧超限");
      try {
        pending = android.util.Base64.decode(chunk, android.util.Base64.DEFAULT);
      } catch (Exception e) {
        throw new IOException("来源数据帧无效");
      }
      offset = 0;
      done = s.optBoolean("done");
      failed = !s.optString("error").isEmpty();
    }

    @Override
    public int read() throws IOException {
      byte[] one = new byte[1];
      return read(one, 0, 1) < 0 ? -1 : one[0] & 255;
    }

    @Override
    public int read(byte[] bytes, int start, int length) throws IOException {
      if (start < 0 || length < 0 || start > bytes.length - length)
        throw new IndexOutOfBoundsException();
      if (length == 0) return 0;
      if (closed.get()) throw new IOException("来源请求已关闭");
      try {
        while (offset >= pending.length) {
          remaining();
          if (done) {
            if (failed) throw new IOException("来源响应中断");
            return -1;
          }
          accept(poll());
          if (pending.length == 0 && !done) Thread.sleep(20);
        }
        int n = Math.min(length, pending.length - offset);
        System.arraycopy(pending, offset, bytes, start, n);
        offset += n;
        return n;
      } catch (Exception e) {
        if (e instanceof InterruptedException) Thread.currentThread().interrupt();
        throw new IOException("来源会话中断", e);
      }
    }

    @Override
    public void close() {
      if (!closed.compareAndSet(false, true)) return;
      // Keep the lease until cancellation has reached the UI queue. A busy UI
      // must not let a timed-out close admit another request ahead of its abort.
      ui.post(
          () -> {
            try {
              page.view.evaluateJavascript(
                  "location.origin === "
                      + JSONObject.quote(origin(id))
                      + " ? window.__mixedGoogleWeb?.cancel('"
                      + key
                      + "') : null",
                  ignored -> page.gate.release());
            } catch (RuntimeException e) {
              page.gate.release();
            }
          });
    }
  }

  @Override
  public void close() {
    ui.post(
        () -> {
          for (Page p : pages.values()) p.view.destroy();
          pages.clear();
        });
  }
}
