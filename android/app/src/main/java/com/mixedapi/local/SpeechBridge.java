package com.mixedapi.local;

import static com.mixedapi.local.GoogleProtocol.*;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import org.json.JSONArray;
import org.json.JSONObject;

final class SpeechBridge {
  static void serve(
      OutputStream out, String backend, JSONObject input, ProtocolBridge.Handler handler)
      throws Exception {
    if (!java.util.Arrays.asList("vertex", "aps", "aistudio").contains(backend)) {
      BridgeHttpServer.fail(out, 501, "此来源未提供内置语音生成");
      return;
    }
    JSONObject chat;
    String format;
    try {
      ProtocolRequest.only(
          input,
          "model",
          "input",
          "voice",
          "response_format",
          "instructions",
          "speed",
          "stream_format");
      String text = ProtocolRequest.required(input, "input");
      format = input.optString("response_format", "wav");
      if (!format.equals("wav") && !format.equals("pcm"))
        throw new IOException("Unsupported audio format");
      if (input.has("speed") && input.getDouble("speed") != 1)
        throw new IOException("Unsupported numeric speed");
      if (input.has("stream_format") && !input.getString("stream_format").equals("audio"))
        throw new IOException("Unsupported audio stream format");
      String voice = input.has("voice") ? ProtocolRequest.required(input, "voice") : "Zephyr";
      if (input.has("instructions") && !input.getString("instructions").isEmpty())
        text = input.getString("instructions") + "\n" + text;
      chat =
          obj(
              "model",
              input.getString("model"),
              "messages",
              arr(obj("role", "user", "content", text)),
              "audio",
              obj("voice", voice, "format", "pcm16"),
              "stream",
              false);
    } catch (Exception e) {
      BridgeHttpServer.fail(out, 400, "语音参数无效；请填写 input、来源声音名称和 wav 或 pcm 格式");
      return;
    }
    try {
      byte[] captured = ProtocolBridge.capture(handler, chat);
      String packet = new String(captured, StandardCharsets.UTF_8);
      int split = packet.indexOf("\r\n\r\n");
      if (split < 0) throw new IOException("Missing internal response");
      if (!packet.startsWith("HTTP/1.1 200 ")) {
        out.write(captured);
        out.flush();
        return;
      }
      JSONArray choices = new JSONObject(packet.substring(split + 4)).getJSONArray("choices");
      if (choices.length() != 1
          || !choices.getJSONObject(0).getString("finish_reason").equals("stop"))
        throw new IOException("Incomplete speech");
      JSONArray media = choices.getJSONObject(0).getJSONObject("message").getJSONArray("media");
      ByteArrayOutputStream pcm = new ByteArrayOutputStream();
      int rate = 0;
      for (int i = 0; i < media.length(); i++) {
        JSONObject part = media.getJSONObject(i);
        SpeechAudio audio =
            new SpeechAudio(
                java.util.Base64.getDecoder().decode(part.getString("data")),
                part.getString("mime_type"));
        if (rate != 0 && rate != audio.rate
            || pcm.size() + audio.pcm.length > 32 * 1024 * 1024 - 44)
          throw new IOException("Inconsistent or oversized audio");
        rate = audio.rate;
        pcm.write(audio.pcm);
      }
      if (pcm.size() == 0 || format.equals("pcm") && rate != 24000)
        throw new IOException("Unsupported audio sample rate");
      byte[] audio =
          format.equals("wav") ? SpeechAudio.wave(pcm.toByteArray(), rate) : pcm.toByteArray();
      out.write(
          ("HTTP/1.1 200 OK\r\nContent-Type: audio/"
                  + format
                  + "\r\nContent-Length: "
                  + audio.length
                  + "\r\nConnection: close\r\nCache-Control: no-store\r\nX-Mixed-Backend: "
                  + backend
                  + "\r\n\r\n")
              .getBytes(StandardCharsets.US_ASCII));
      out.write(audio);
      out.flush();
    } catch (Exception e) {
      if (!Thread.currentThread().isInterrupted()) BridgeHttpServer.fail(out, 502, "来源未返回有效且完整的语音");
    }
  }
}
