package com.mixedapi.local;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.OutputStream;
import org.json.JSONObject;

/** Same-thread protocol adapter: keeps the selected provider and its cancellation scope. */
final class ProtocolBridge {
  interface Handler {
    void serve(OutputStream out, JSONObject chat) throws Exception;
  }

  static void serve(
      OutputStream out,
      String path,
      String backend,
      String model,
      JSONObject input,
      Handler handler)
      throws Exception {
    boolean messages = path.equals("/v1/messages");
    if (!messages && !path.equals("/v1/responses")) {
      BridgeHttpServer.fail(out, 404, "未知协议接口");
      return;
    }
    JSONObject chat;
    try {
      chat = messages ? ProtocolRequest.messages(input) : ProtocolRequest.responses(input);
      if (chat.optBoolean("stream"))
        chat.put("stream_options", GoogleProtocol.obj("include_usage", true));
    } catch (Exception e) {
      BridgeHttpServer.fail(out, 400, "请求包含无法转换的参数、内容或状态功能");
      return;
    }
    if (chat.optBoolean("stream")) {
      ProtocolStream stream = new ProtocolStream(out, backend, model, messages);
      StreamCapture capture = new StreamCapture(out, stream);
      try {
        handler.serve(capture, chat);
      } catch (Exception ignored) {
      }
      if (!capture.passthrough && !stream.done) stream.error();
      return;
    }
    try {
      byte[] captured = capture(handler, chat);
      String packet = new String(captured, java.nio.charset.StandardCharsets.UTF_8);
      int split = packet.indexOf("\r\n\r\n");
      if (split < 0) throw new IOException("Missing internal response");
      if (!packet.startsWith("HTTP/1.1 200 ")) {
        out.write(captured);
        out.flush();
        return;
      }
      JSONObject response =
          ProtocolResult.convert(new JSONObject(packet.substring(split + 4)), model, messages);
      BridgeHttpServer.json(out, 200, response, backend);
    } catch (Exception e) {
      if (!Thread.currentThread().isInterrupted()) BridgeHttpServer.fail(out, 502, "来源结果无法转换为完整响应");
    }
  }

  static byte[] capture(Handler handler, JSONObject chat) throws Exception {
    ByteArrayOutputStream buffer = new ByteArrayOutputStream();
    OutputStream capture =
        new OutputStream() {
          @Override
          public void write(int value) throws IOException {
            if (buffer.size() >= 32 * 1024 * 1024) throw new IOException("Response too large");
            buffer.write(value);
          }

          @Override
          public void write(byte[] bytes, int offset, int count) throws IOException {
            if (buffer.size() + count > 32 * 1024 * 1024)
              throw new IOException("Response too large");
            buffer.write(bytes, offset, count);
          }
        };
    handler.serve(capture, chat);
    return buffer.toByteArray();
  }

  /**
   * Internal handlers emit close-delimited SSE. Parsing runs inline, so writes retain backpressure.
   */
  private static final class StreamCapture extends OutputStream {
    private final OutputStream out;
    private final ProtocolStream stream;
    private final ByteArrayOutputStream buffer = new ByteArrayOutputStream();
    private final StringBuilder event = new StringBuilder();
    private boolean headers, broken;
    boolean passthrough;
    private int last;

    StreamCapture(OutputStream out, ProtocolStream stream) {
      this.out = out;
      this.stream = stream;
    }

    @Override
    public void write(int value) throws IOException {
      if (broken || Thread.currentThread().isInterrupted())
        throw new IOException("Request cancelled");
      if (passthrough) {
        out.write(value);
        return;
      }
      buffer.write(value);
      if (buffer.size() > (headers ? 32 * 1024 * 1024 : 65536)) {
        broken = true;
        throw new IOException("Internal response too large");
      }
      if (value != '\n') {
        if (value != '\r') last = value;
        return;
      }
      try {
        if (!headers) {
          if (last == '\n') {
            String head = buffer.toString("UTF-8");
            if (!head.startsWith("HTTP/1.1 ")) throw new IOException("Invalid internal response");
            headers = true;
            if (!head.startsWith("HTTP/1.1 200 ")) {
              passthrough = true;
              out.write(buffer.toByteArray());
            } else if (!head.toLowerCase(java.util.Locale.ROOT)
                .contains("content-type: text/event-stream"))
              throw new IOException("Expected streaming response");
            buffer.reset();
          }
        } else {
          String line = buffer.toString("UTF-8").trim();
          buffer.reset();
          if (line.isEmpty()) {
            if (event.length() > 0) {
              stream.consume(event.toString());
              event.setLength(0);
            }
          } else if (line.startsWith("data:")) {
            if (event.length() > 0) event.append('\n');
            String data = line.substring(5);
            event.append(data.startsWith(" ") ? data.substring(1) : data);
            if (event.length() > 16 * 1024 * 1024)
              throw new IOException("Internal event too large");
          }
        }
        last = '\n';
      } catch (Exception e) {
        broken = true;
        throw new IOException("Protocol conversion failed");
      }
    }

    @Override
    public void write(byte[] data, int offset, int length) throws IOException {
      if (passthrough) {
        out.write(data, offset, length);
        return;
      }
      for (int i = offset; i < offset + length; i++) write(data[i] & 255);
    }

    @Override
    public void flush() throws IOException {
      out.flush();
    }
  }
}
