package com.mixedapi.local;

import java.io.IOException;
import java.nio.ByteBuffer;
import java.nio.ByteOrder;
import java.nio.charset.StandardCharsets;

/** Validates mono 16-bit provider audio and wraps/unwraps WAV without an external encoder. */
final class SpeechAudio {
  final byte[] pcm;
  final int rate;

  SpeechAudio(byte[] data, String mime) throws Exception {
    String[] fields = mime.toLowerCase(java.util.Locale.ROOT).split(";");
    int hz = 24000;
    if (fields[0].equals("audio/wav") || fields[0].equals("audio/x-wav")) {
      ByteBuffer buffer = ByteBuffer.wrap(data).order(ByteOrder.LITTLE_ENDIAN);
      if (data.length < 12
          || !tag(data, 0).equals("RIFF")
          || !tag(data, 8).equals("WAVE")
          || Integer.toUnsignedLong(buffer.getInt(4)) + 8 != data.length)
        throw new IOException("Invalid WAV container");
      byte[] samples = null;
      hz = 0;
      int position = 12;
      while (position + 8 <= data.length) {
        long size = Integer.toUnsignedLong(buffer.getInt(position + 4)), end = position + 8L + size;
        if (end > data.length) throw new IOException("Truncated WAV chunk");
        String type = tag(data, position);
        int start = position + 8;
        if (type.equals("fmt ")) {
          if (size < 16
              || hz != 0
              || buffer.getShort(start) != 1
              || buffer.getShort(start + 2) != 1
              || buffer.getShort(start + 14) != 16)
            throw new IOException("Unsupported WAV sample encoding");
          hz = buffer.getInt(start + 4);
          if (hz < 8000
              || hz > 96000
              || buffer.getInt(start + 8) != hz * 2
              || buffer.getShort(start + 12) != 2) throw new IOException("Invalid WAV format");
        } else if (type.equals("data")) {
          if (samples != null) throw new IOException("Duplicate WAV samples");
          samples = java.util.Arrays.copyOfRange(data, start, (int) end);
        }
        position = (int) (end + size % 2);
      }
      if (position != data.length || samples == null || hz == 0)
        throw new IOException("Incomplete WAV");
      pcm = samples;
    } else {
      if (!fields[0].equals("audio/l16") && !fields[0].equals("audio/pcm"))
        throw new IOException("Unsupported audio encoding");
      for (int i = 1; i < fields.length; i++) {
        String field = fields[i].trim();
        if (field.startsWith("rate=")) hz = Integer.parseInt(field.substring(5));
        else if (!field.equals("codec=pcm")) throw new IOException("Unsupported audio parameter");
      }
      pcm = data;
    }
    if (hz < 8000 || hz > 96000 || pcm.length == 0 || pcm.length % 2 != 0)
      throw new IOException("Invalid PCM samples");
    rate = hz;
  }

  private static String tag(byte[] data, int position) {
    return new String(data, position, 4, StandardCharsets.US_ASCII);
  }

  static byte[] wave(byte[] pcm, int rate) {
    ByteBuffer output = ByteBuffer.allocate(pcm.length + 44).order(ByteOrder.LITTLE_ENDIAN);
    output
        .put("RIFF".getBytes(StandardCharsets.US_ASCII))
        .putInt(pcm.length + 36)
        .put("WAVEfmt ".getBytes(StandardCharsets.US_ASCII));
    output
        .putInt(16)
        .putShort((short) 1)
        .putShort((short) 1)
        .putInt(rate)
        .putInt(rate * 2)
        .putShort((short) 2)
        .putShort((short) 16);
    output.put("data".getBytes(StandardCharsets.US_ASCII)).putInt(pcm.length).put(pcm);
    return output.array();
  }
}
