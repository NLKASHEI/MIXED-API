package com.mixedapi.local;

import static com.mixedapi.local.GoogleProtocol.*;

import java.io.IOException;
import java.util.Locale;
import org.json.JSONArray;
import org.json.JSONObject;

final class GoogleMedia {
  static JSONObject blob(String mime, String data) throws Exception {
    if (!(mime.startsWith("image/")
        || mime.startsWith("audio/")
        || mime.startsWith("video/")
        || mime.equals("application/pdf"))) throw new IOException("Unsupported media type");
    if (java.util.Base64.getDecoder().decode(data).length == 0)
      throw new IOException("Empty media");
    return obj("inlineData", obj("mimeType", mime, "data", data));
  }

  static JSONObject dataURL(String value) throws Exception {
    int split = value.indexOf(',');
    if (split < 0 || !value.startsWith("data:") || !value.substring(0, split).endsWith(";base64"))
      throw new IOException("Media requires a base64 data URL");
    return blob(value.substring(5, split - 7), value.substring(split + 1));
  }

  static void configure(JSONObject input, JSONObject config) throws Exception {
    if (input.has("media_resolution")) {
      String name = input.getString("media_resolution");
      if (!java.util.Arrays.asList("low", "medium", "high").contains(name))
        throw new IOException("Invalid media resolution");
      config.put("mediaResolution", "MEDIA_RESOLUTION_" + name.toUpperCase(Locale.ROOT));
    }
    if (input.has("modalities")) {
      JSONArray values = input.getJSONArray("modalities"), modes = new JSONArray();
      java.util.Set<String> seen = new java.util.HashSet<>();
      if (values.length() == 0) throw new IOException("Empty modalities");
      for (int i = 0; i < values.length(); i++) {
        String mode = values.getString(i);
        if (!java.util.Arrays.asList("text", "image", "audio").contains(mode) || !seen.add(mode))
          throw new IOException("Invalid output modality");
        modes.put(mode.toUpperCase(Locale.ROOT));
      }
      config.put("responseModalities", modes);
    }
    if (input.has("audio")) {
      JSONObject audio = input.getJSONObject("audio");
      ProtocolRequest.only(audio, "voice", "format");
      String voice = ProtocolRequest.required(audio, "voice");
      if (audio.has("format") && !audio.getString("format").equals("pcm16"))
        throw new IOException("Use audio/speech for WAV output");
      if (config.has("responseModalities")) {
        JSONArray modes = config.getJSONArray("responseModalities");
        boolean audioMode = false;
        for (int i = 0; i < modes.length(); i++)
          if (modes.getString(i).equals("AUDIO")) audioMode = true;
        if (!audioMode) throw new IOException("Audio configuration requires audio output modality");
      } else config.put("responseModalities", arr("AUDIO"));
      config.put(
          "speechConfig", obj("voiceConfig", obj("prebuiltVoiceConfig", obj("voiceName", voice))));
    }
  }

  static JSONArray speechWire(JSONObject speech) throws Exception {
    String voice =
        speech
            .getJSONObject("voiceConfig")
            .getJSONObject("prebuiltVoiceConfig")
            .getString("voiceName");
    if (voice.isEmpty()) throw new IOException("Missing voice");
    return arr(arr(arr(voice)));
  }
}
