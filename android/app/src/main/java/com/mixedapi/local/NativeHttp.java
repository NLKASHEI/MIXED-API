package com.mixedapi.local;

import java.io.FilterInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.util.Map;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.ScheduledFuture;
import java.util.concurrent.TimeUnit;
import okhttp3.Call;
import okhttp3.MediaType;
import okhttp3.OkHttpClient;
import okhttp3.Request;
import okhttp3.RequestBody;
import okhttp3.Response;

/** HTTP cancellation closes the exchange even while a provider stalls inside a body read. */
final class NativeHttp {
  private static final OkHttpClient CLIENT =
      new OkHttpClient.Builder()
          .followRedirects(false)
          .followSslRedirects(false)
          .retryOnConnectionFailure(false)
          .connectTimeout(15, TimeUnit.SECONDS)
          .readTimeout(300, TimeUnit.SECONDS)
          .writeTimeout(30, TimeUnit.SECONDS)
          .build();
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
    long remaining = deadline - System.currentTimeMillis();
    if (owner.isInterrupted() || remaining <= 0) throw new IOException("Request cancelled");
    Request.Builder request =
        new Request.Builder()
            .url(url)
            .header("User-Agent", "MIXED-API/" + BuildConfig.VERSION_NAME);
    String contentType = "application/json";
    for (Map.Entry<String, String> header : headers.entrySet()) {
      request.header(header.getKey(), header.getValue());
      if (header.getKey().equalsIgnoreCase("Content-Type")) contentType = header.getValue();
    }
    MediaType type = MediaType.get(contentType);
    RequestBody payload =
        body == null
            ? null
            : new RequestBody() {
              @Override
              public MediaType contentType() {
                return type;
              }

              @Override
              public long contentLength() {
                return body.length;
              }

              // A generation must not be replayed by HTTP recovery or status follow-ups.
              @Override
              public boolean isOneShot() {
                return true;
              }

              @Override
              public void writeTo(okio.BufferedSink sink) throws IOException {
                sink.write(body);
              }
            };
    Call call = CLIENT.newCall(request.method(method, payload).build());
    call.timeout().timeout(Math.min(300000, remaining), TimeUnit.MILLISECONDS);
    ScheduledFuture<?> watch =
        WATCHDOG.scheduleWithFixedDelay(
            () -> {
              if (owner.isInterrupted() || System.currentTimeMillis() >= deadline) call.cancel();
            },
            50,
            50,
            TimeUnit.MILLISECONDS);
    try {
      Response response = call.execute();
      InputStream stream =
          new FilterInputStream(response.body().byteStream()) {
            @Override
            public void close() throws IOException {
              try {
                super.close();
              } finally {
                watch.cancel(false);
                response.close();
              }
            }
          };
      String retry = response.header("Retry-After");
      return new GoogleSessions.Response(
          response.code(),
          stream,
          retry == null ? "" : retry,
          response.header("Content-Type", "application/json"));
    } catch (Exception e) {
      watch.cancel(false);
      call.cancel();
      throw new IOException("Provider network request failed");
    }
  }
}
