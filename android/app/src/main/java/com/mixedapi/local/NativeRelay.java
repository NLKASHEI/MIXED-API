package com.mixedapi.local;

import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import org.json.JSONArray;
import org.json.JSONObject;

/** Native protocol passthrough preserves opaque provider fields and validates terminal events. */
final class NativeRelay {
  private final OutputStream out;
  private final String backend, model, protocol;
  private final boolean stream;
  private boolean started, done, messageStarted;
  private String messageFinish = "";
  private final java.util.Set<Integer> blocks = new java.util.HashSet<>();
  private JSONObject result;

  NativeRelay(OutputStream out, String backend, String model, String protocol, boolean stream) {
    this.out = out;
    this.backend = backend;
    this.model = model;
    this.protocol = protocol;
    this.stream = stream;
  }

  void read(InputStream input) throws Exception {
    NativeWire.events(
        input,
        data -> {
          if (data.equals("[DONE]")) return;
          JSONObject event = new JSONObject(data);
          String type = event.getString("type");
          if (!type.matches("[A-Za-z0-9_.-]{1,120}")
              || type.equals("error")
              || !event.isNull("error")
              || type.equals("response.failed"))
            throw new IOException("Provider generation failed");
          if (protocol.equals("responses")) {
            JSONObject response = event.optJSONObject("response");
            if (response != null) response.put("model", model);
            if (type.equals("response.completed") || type.equals("response.incomplete")) {
              if (response == null || !type.equals("response." + response.optString("status")))
                throw new IOException("Invalid completion status");
              validate(response, protocol);
              result = response;
              done = true;
            }
          } else {
            if (type.equals("message_start")) {
              if (messageStarted) throw new IOException("Duplicate message");
              messageStarted = true;
              event.getJSONObject("message").put("model", model);
            } else if (!type.equals("ping") && !messageStarted)
              throw new IOException("Missing message start");
            if (type.equals("content_block_start")) {
              int index = event.getInt("index");
              if (index < 0 || index > 1024 || !blocks.add(index))
                throw new IOException("Invalid content block");
            } else if (type.equals("content_block_delta")
                && !blocks.contains(event.getInt("index")))
              throw new IOException("Unknown content block");
            else if (type.equals("content_block_stop") && !blocks.remove(event.getInt("index")))
              throw new IOException("Unknown content block");
            else if (type.equals("message_delta")) {
              JSONObject delta = event.getJSONObject("delta");
              if (!delta.isNull("stop_reason")) messageFinish = delta.getString("stop_reason");
            } else if (type.equals("message_stop")) {
              if (!blocks.isEmpty() || !messageFinish(messageFinish))
                throw new IOException("Missing completion");
              done = true;
            }
          }
          if (stream) event(type, event);
          if (done) throw new NativeWire.End();
        });
    if (!done) throw new IOException("Incomplete provider stream");
    if (!stream) {
      if (result == null || result.optJSONArray("output") == null)
        throw new IOException("Missing generated output");
      BridgeHttpServer.json(out, 200, result, backend);
    }
  }

  static boolean messageFinish(String value) {
    return java.util.Arrays.asList(
            "end_turn",
            "stop_sequence",
            "tool_use",
            "max_tokens",
            "pause_turn",
            "refusal",
            "model_context_window_exceeded")
        .contains(value);
  }

  static void validate(JSONObject result, String protocol) throws Exception {
    if (!result.isNull("error")) throw new IOException("Provider generation failed");
    if (protocol.equals("responses")) {
      String status = result.optString("status");
      if (!status.equals("completed") && !status.equals("incomplete"))
        throw new IOException("Missing response completion");
      JSONArray output = result.optJSONArray("output");
      if (output != null && status.equals("completed"))
        for (int i = 0; i < output.length(); i++) {
          JSONObject item = output.getJSONObject(i);
          if (item.optString("type").equals("function_call")) {
            if (item.optString("call_id").isEmpty() || item.optString("name").isEmpty())
              throw new IOException("Missing tool identity");
            org.json.JSONTokener parser = new org.json.JSONTokener(item.getString("arguments"));
            if (!(parser.nextValue() instanceof JSONObject) || parser.nextClean() != 0)
              throw new IOException("Invalid tool arguments");
          }
        }
    } else if (protocol.equals("messages") && !messageFinish(result.optString("stop_reason")))
      throw new IOException("Missing message completion");
  }

  void event(String type, JSONObject data) throws Exception {
    if (!started) {
      write(
          "HTTP/1.1 200 OK\r\n"
              + "Content-Type: text/event-stream\r\n"
              + "Cache-Control: no-store\r\n"
              + "Connection: close\r\n"
              + "X-Mixed-Backend: "
              + backend
              + "\r\n\r\n");
      started = true;
    }
    write("event: " + type + "\ndata: " + data + "\n\n");
  }

  private void write(String data) throws IOException {
    out.write(data.getBytes(StandardCharsets.UTF_8));
    out.flush();
  }

  void error() throws Exception {
    if (Thread.currentThread().isInterrupted()) return;
    if (!started) BridgeHttpServer.fail(out, 502, "来源生成未完成");
    else
      event(
          "error",
          GoogleProtocol.obj(
              "type",
              "error",
              "error",
              GoogleProtocol.obj("type", "api_error", "message", "来源流响应在完成前中断")));
  }
}
