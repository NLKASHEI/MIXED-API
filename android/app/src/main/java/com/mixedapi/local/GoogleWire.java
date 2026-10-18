package com.mixedapi.local;

import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.PushbackReader;
import java.nio.charset.StandardCharsets;
import org.json.JSONArray;
import org.json.JSONObject;
import org.json.JSONTokener;

/** Bounded frame parser; terminal success is decided only after the RPC root closes. */
final class GoogleWire {
  interface Consumer {
    void accept(JSONObject frame) throws Exception;
  }

  private final PushbackReader reader;

  GoogleWire(InputStream stream) {
    reader = new PushbackReader(new InputStreamReader(stream, StandardCharsets.UTF_8), 1);
  }

  int next() throws IOException {
    int c;
    do {
      if (Thread.currentThread().isInterrupted()) throw new IOException("Request cancelled");
      c = reader.read();
    } while (c >= 0 && Character.isWhitespace(c));
    return c;
  }

  void require(int token) throws IOException {
    if (next() != token) throw new IOException("来源响应结构无效");
  }

  Object value(int first) throws Exception {
    if (first < 0) throw new IOException("来源响应截断");
    StringBuilder raw = new StringBuilder();
    int depth = 0;
    boolean quoted = false, escaped = false;
    int c = first;
    do {
      if (Thread.currentThread().isInterrupted()) throw new IOException("Request cancelled");
      raw.append((char) c);
      if (raw.length() > 32 * 1024 * 1024) throw new IOException("来源数据帧过大");
      if (quoted) {
        if (escaped) escaped = false;
        else if (c == '\\') escaped = true;
        else if (c == '"') quoted = false;
      } else {
        if (c == '"') quoted = true;
        else if (c == '[' || c == '{') depth++;
        else if (c == ']' || c == '}') depth--;
      }
      if (depth == 0 && !quoted) break;
      c = reader.read();
      if (c < 0) throw new IOException("来源响应截断");
    } while (true);
    return new JSONTokener(sparse(raw.toString())).nextValue();
  }

  static String sparse(String text) {
    StringBuilder result = new StringBuilder();
    boolean quoted = false, escaped = false;
    char prev = 0;
    for (int i = 0; i < text.length(); i++) {
      char c = text.charAt(i);
      if (quoted) {
        result.append(c);
        if (escaped) escaped = false;
        else if (c == '\\') escaped = true;
        else if (c == '"') quoted = false;
        continue;
      }
      if (c == ',' && (prev == '[' || prev == ',') || c == ']' && prev == ',')
        result.append("null");
      result.append(c);
      if (c == '"') quoted = true;
      if (!Character.isWhitespace(c)) prev = c;
    }
    return result.toString();
  }

  static String proxyBody(JSONArray frame) throws Exception {
    if (!frame.isNull(0)) return frame.getString(0);
    if (!frame.isNull(2))
      return new String(
          android.util.Base64.decode(frame.getString(2), android.util.Base64.DEFAULT),
          StandardCharsets.UTF_8);
    throw new IOException("RPC 正文缺失");
  }

  static void decode(String kind, InputStream stream, Consumer emit) throws Exception {
    GoogleWire parser = new GoogleWire(stream);
    if ("aps".equals(kind)) {
      boolean array = false, value = false, seen = false;
      for (; ; ) {
        int c = parser.next();
        if (c < 0) {
          if (array || !seen) throw new IOException("GraphQL 响应截断");
          return;
        }
        switch (c) {
          case '[':
            if (array) throw new IOException("GraphQL 根结构无效");
            array = true;
            value = false;
            continue;
          case ']':
            if (!array || !value) throw new IOException("GraphQL 结束结构无效");
            array = false;
            continue;
          case ',':
            if (!array || !value) throw new IOException("GraphQL 分隔符无效");
            value = false;
            continue;
          case '{':
            if (array && value) throw new IOException("GraphQL 缺少分隔符");
            value = true;
            seen = true;
            break;
          default:
            throw new IOException("GraphQL 内容无效");
        }
        JSONObject object = (JSONObject) parser.value(c);
        JSONArray results = object.optJSONArray("results");
        if (results == null) throw new IOException("GraphQL 缺少结果");
        for (int i = 0; i < results.length(); i++) {
          JSONObject r = results.getJSONObject(i);
          if (r.optJSONArray("errors") != null && r.getJSONArray("errors").length() > 0)
            throw new IOException("GraphQL 请求失败");
          JSONObject data = r.optJSONObject("data"),
              ui = data == null ? null : data.optJSONObject("ui");
          Object frame = ui == null ? null : ui.opt("streamGenerateContentAnonymous");
          if (frame instanceof JSONObject) emit.accept((JSONObject) frame);
          else if (frame instanceof JSONArray) {
            JSONArray frames = (JSONArray) frame;
            for (int n = 0; n < frames.length(); n++) emit.accept(frames.getJSONObject(n));
          }
        }
      }
    }
    if ("build_unary".equals(kind)) {
      Object frame = parser.value(parser.next());
      if (!(frame instanceof JSONArray) || parser.next() != -1) throw new IOException("Build 响应无效");
      emit.accept(new JSONObject(proxyBody((JSONArray) frame)));
      return;
    }
    parser.require('[');
    parser.require('[');
    int c = parser.next();
    if (c != ']')
      for (; ; ) {
        if (c != '[') throw new IOException("RPC 数据帧无效");
        JSONArray frame = (JSONArray) parser.value(c);
        emit.accept(
            "playground".equals(kind)
                ? GoogleProtocol.playgroundFrame(frame)
                : new JSONObject(proxyBody(frame)));
        c = parser.next();
        if (c == ']') break;
        if (c != ',') throw new IOException("RPC 分隔符无效");
        c = parser.next();
      }
    c = parser.next();
    while (c == ',') {
      Object trailer = parser.value(parser.next());
      if (!(trailer instanceof JSONArray) || ((JSONArray) trailer).optInt(0, -1) != 0)
        throw new IOException("RPC 拒绝生成");
      c = parser.next();
    }
    if (c != ']' || parser.next() != -1) throw new IOException("RPC 响应截断");
  }
}
