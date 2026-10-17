package com.mixedapi.local;

import static org.junit.Assert.*;

import android.content.Context;
import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.*;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 28)
public class ExternalGatewayTest {
  @Test
  public void stoppingListenerClosesEveryExistingClient() throws Exception {
    Context ctx = RuntimeEnvironment.getApplication();
    ctx.getSharedPreferences("mixed_config", Context.MODE_PRIVATE).edit().clear().commit();
    java.util.List<Socket> clients = new java.util.ArrayList<>();
    try (BridgeHttpServer server = new BridgeHttpServer(ctx)) {
      server.start();
      // More connections than worker slots also exercises queued peers.
      for (int i = 0; i < 18; i++) {
        Socket client = new Socket("127.0.0.1", 8765);
        client.setSoTimeout(1000);
        clients.add(client);
        client
            .getOutputStream()
            .write("GET /health HTTP/1.1\r\n".getBytes(StandardCharsets.US_ASCII));
        client.getOutputStream().flush();
      }
      server.close();
      for (Socket client : clients) {
        try {
          assertEquals(-1, client.getInputStream().read());
        } catch (SocketException reset) {
          /* Closing unread accepted data may reset the peer. */
        }
      }
    } finally {
      for (Socket client : clients) client.close();
    }
  }

  @Test
  public void pcStreamDisconnectClosesStalledUpstream() throws Exception {
    try (ServerSocket provider = new ServerSocket(0, 1, InetAddress.getByName("127.0.0.1"))) {
      provider.setSoTimeout(4000);
      CompletableFuture<Boolean> cancelled = new CompletableFuture<>();
      Thread upstream =
          new Thread(
              () -> {
                try (Socket connection = provider.accept()) {
                  connection.setSoTimeout(4000);
                  BufferedReader input =
                      new BufferedReader(
                          new InputStreamReader(
                              connection.getInputStream(), StandardCharsets.UTF_8));
                  int length = 0;
                  String line;
                  while ((line = input.readLine()) != null && !line.isEmpty())
                    if (line.toLowerCase(java.util.Locale.ROOT).startsWith("content-length:"))
                      length = Integer.parseInt(line.substring(15).trim());
                  for (int i = 0; i < length; i++) assertTrue(input.read() >= 0);
                  connection
                      .getOutputStream()
                      .write(
                          ("HTTP/1.1 200 OK\r\n"
                                  + "Content-Type: text/event-stream\r\n"
                                  + "Content-Length: 1000000\r\n\r\n"
                                  + "data: hello\n\n")
                              .getBytes(StandardCharsets.UTF_8));
                  connection.getOutputStream().flush();
                  cancelled.complete(input.read() == -1);
                } catch (Exception e) {
                  cancelled.completeExceptionally(e);
                }
              },
              "PC HTTP fixture");
      upstream.setDaemon(true);
      upstream.start();
      Context ctx = RuntimeEnvironment.getApplication();
      ctx.getSharedPreferences("mixed_config", Context.MODE_PRIVATE).edit().clear().commit();
      JSONObject config = BridgeConfig.load(ctx).put("api_key", "client").put("admin_key", "admin");
      config.put(
          "backends",
          GoogleProtocol.arr(
              GoogleProtocol.obj(
                  "id",
                  "pc",
                  "url",
                  "http://127.0.0.1:" + provider.getLocalPort() + "/v1",
                  "auth",
                  "none",
                  "enabled",
                  true)));
      BridgeConfig.save(ctx, config);
      try (BridgeHttpServer server = new BridgeHttpServer(ctx)) {
        server.start();
        try (Socket client = new Socket("127.0.0.1", 8765)) {
          client.setSoTimeout(3000);
          byte[] body =
              "{\"model\":\"pc/codex/model\",\"stream\":true,\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}"
                  .getBytes(StandardCharsets.UTF_8);
          OutputStream output = client.getOutputStream();
          output.write(
              ("POST /v1/chat/completions HTTP/1.1\r\n"
                      + "Host: 127.0.0.1:8765\r\n"
                      + "Authorization: Bearer client\r\n"
                      + "Content-Length: "
                      + body.length
                      + "\r\n\r\n")
                  .getBytes(StandardCharsets.UTF_8));
          output.write(body);
          output.flush();
          BufferedReader input =
              new BufferedReader(
                  new InputStreamReader(client.getInputStream(), StandardCharsets.UTF_8));
          String line;
          boolean received = false;
          while ((line = input.readLine()) != null)
            if (line.equals("data: hello")) {
              received = true;
              break;
            }
          assertTrue(received);
        }
        assertTrue(
            "PC stream stayed open after client cancellation", cancelled.get(3, TimeUnit.SECONDS));
      }
    }
  }
}
