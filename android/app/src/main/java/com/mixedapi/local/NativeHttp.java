package com.mixedapi.local;

import java.io.ByteArrayInputStream;
import java.io.FilterInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.util.Map;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.ScheduledFuture;
import java.util.concurrent.TimeUnit;

/** Fixed-endpoint HTTP I/O with bounded time and cancellation during upload/read. */
final class NativeHttp {
  private static final ScheduledExecutorService WATCHDOG =
      Executors.newSingleThreadScheduledExecutor(
          r -> {
            Thread t = new Thread(r, "MIXED network deadlines");
            t.setDaemon(true);
            return t;
          });

  static GoogleSessions.Response send(
      String method, String url, Map<String, String> headers, byte[] body, long deadline)
      throws Exception {
    Thread owner = Thread.currentThread();
    if (owner.isInterrupted() || System.currentTimeMillis() >= deadline)
      throw new IOException("Request cancelled");
    HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
    c.setInstanceFollowRedirects(false);
    c.setRequestMethod(method);
    int timeout = (int) Math.min(300000, Math.max(1, deadline - System.currentTimeMillis()));
    c.setConnectTimeout(Math.min(15000, timeout));
    c.setReadTimeout(timeout);
    c.setRequestProperty("User-Agent", "MIXED-API/" + BuildConfig.VERSION_NAME);
    for (Map.Entry<String, String> h : headers.entrySet())
      c.setRequestProperty(h.getKey(), h.getValue());
    ScheduledFuture<?> watch =
        WATCHDOG.scheduleAtFixedRate(
            () -> {
              if (owner.isInterrupted() || System.currentTimeMillis() >= deadline) c.disconnect();
            },
            50,
            50,
            TimeUnit.MILLISECONDS);
    try {
      if (body != null) {
        c.setDoOutput(true);
        c.setRequestProperty("Content-Type", "application/json");
        c.setFixedLengthStreamingMode(body.length);
        try (java.io.OutputStream out = c.getOutputStream()) {
          out.write(body);
        }
      }
      int status = c.getResponseCode();
      InputStream stream = status >= 400 ? c.getErrorStream() : c.getInputStream();
      if (stream == null) stream = new ByteArrayInputStream(new byte[0]);
      InputStream response =
          new FilterInputStream(stream) {
            @Override
            public void close() throws IOException {
              try {
                super.close();
              } finally {
                watch.cancel(false);
                c.disconnect();
              }
            }
          };
      String retry = c.getHeaderField("Retry-After");
      return new GoogleSessions.Response(status, response, retry == null ? "" : retry);
    } catch (Exception e) {
      watch.cancel(false);
      c.disconnect();
      throw new IOException("Provider network request failed");
    }
  }
}
