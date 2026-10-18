package com.mixedapi.local;

import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.nio.charset.StandardCharsets;

/** SSE framing shared by native protocols; no unbounded readLine allocation. */
final class NativeWire {
  static final class End extends Exception {}

  interface Consumer {
    void accept(String data) throws Exception;
  }

  static void events(InputStream input, Consumer consumer) throws Exception {
    try {
      readEvents(input, consumer);
    } catch (End completed) {
      /* terminal provider event */
    }
  }

  private static void readEvents(InputStream input, Consumer consumer) throws Exception {
    InputStreamReader reader = new InputStreamReader(input, StandardCharsets.UTF_8);
    StringBuilder line = new StringBuilder(), data = new StringBuilder();
    int next;
    while ((next = reader.read()) != -1) {
      if (Thread.currentThread().isInterrupted()) throw new IOException("Request cancelled");
      if (next == '\n') {
        String text = line.toString();
        line.setLength(0);
        if (text.endsWith("\r")) text = text.substring(0, text.length() - 1);
        if (text.isEmpty()) {
          if (data.length() > 0) {
            consumer.accept(data.toString());
            data.setLength(0);
          }
        } else if (text.startsWith("data:")) {
          String value = text.substring(5);
          if (value.startsWith(" ")) value = value.substring(1);
          if (data.length() > 0) data.append('\n');
          data.append(value);
        }
      } else line.append((char) next);
      if (line.length() + data.length() > 32 * 1024 * 1024)
        throw new IOException("Provider event too large");
    }
    if (line.length() > 0) {
      String text = line.toString();
      if (text.endsWith("\r")) text = text.substring(0, text.length() - 1);
      if (text.startsWith("data:")) {
        if (data.length() > 0) data.append('\n');
        String value = text.substring(5);
        data.append(value.startsWith(" ") ? value.substring(1) : value);
      }
    }
    if (data.length() > 0) consumer.accept(data.toString());
  }
}
