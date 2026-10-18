package com.mixedapi.local;

import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import java.util.Map;
import java.util.TreeMap;
import java.util.UUID;
import org.json.JSONArray;
import org.json.JSONObject;
import org.json.JSONTokener;

/** Validates and collects Chat streams; terminal success requires provider completion. */
final class NativeChat {
  private static final class Choice {
    final JSONObject message;
    final Map<Integer, JSONObject> tools = new TreeMap<>();
    String finish = "";

    Choice() throws Exception {
      message = GoogleProtocol.obj("role", "assistant", "content", "");
    }
  }

  private final OutputStream out;
  private final String backend, model, id = "chatcmpl-" + UUID.randomUUID();
  private final boolean stream, includeUsage;
  private final int expected;
  private final Map<Integer, Choice> choices = new TreeMap<>();
  private JSONObject usage;
  private long size;
  boolean started, done;

  NativeChat(
      OutputStream out,
      String backend,
      String model,
      boolean stream,
      boolean includeUsage,
      int expected)
      throws IOException {
    if (expected < 1 || expected > 8) throw new IOException("Invalid candidate count");
    this.out = out;
    this.backend = backend;
    this.model = model;
    this.stream = stream;
    this.includeUsage = includeUsage;
    this.expected = expected;
  }

  void read(InputStream input) throws Exception {
    NativeWire.events(
        input,
        data -> {
          if ("[DONE]".equals(data)) {
            complete();
            throw new NativeWire.End();
          }
          chunk(new JSONObject(data));
        });
    if (!done) throw new IOException("Incomplete provider stream");
  }

  void chunk(JSONObject packet) throws Exception {
    size += packet.toString().getBytes(StandardCharsets.UTF_8).length;
    if (size > 32 * 1024 * 1024 || !packet.isNull("error"))
      throw new IOException("Invalid provider stream");
    if (packet.optJSONObject("usage") != null) usage = packet.getJSONObject("usage");
    JSONArray incoming = packet.optJSONArray("choices"), deltas = new JSONArray();
    if (incoming == null) throw new IOException("Missing choices");
    for (int i = 0; i < incoming.length(); i++) {
      JSONObject item = incoming.getJSONObject(i);
      int index = item.optInt("index", 0);
      if (index < 0 || index >= expected) throw new IOException("Invalid candidate index");
      Choice choice = choices.get(index);
      if (choice == null) {
        choice = new Choice();
        choices.put(index, choice);
      }
      if (!choice.finish.isEmpty()) throw new IOException("Data after completion");
      JSONObject delta = item.getJSONObject("delta");
      if (!stream)
        for (String key : new String[] {"content", "reasoning_content", "refusal"})
          if (delta.opt(key) instanceof String)
            choice.message.put(key, choice.message.optString(key) + delta.getString(key));
      if (!stream && delta.has("reasoning_details")) {
        JSONArray saved = choice.message.optJSONArray("reasoning_details");
        if (saved == null) saved = new JSONArray();
        JSONArray incomingDetails = delta.getJSONArray("reasoning_details");
        for (int n = 0; n < incomingDetails.length(); n++) saved.put(incomingDetails.get(n));
        choice.message.put("reasoning_details", saved);
      }
      JSONArray calls = delta.optJSONArray("tool_calls");
      if (calls != null)
        for (int n = 0; n < calls.length(); n++) {
          JSONObject call = calls.getJSONObject(n);
          int toolIndex = call.optInt("index", -1);
          if (toolIndex < 0 || toolIndex > 1024) throw new IOException("Invalid tool index");
          JSONObject target = choice.tools.get(toolIndex);
          if (target == null) {
            target =
                GoogleProtocol.obj(
                    "id",
                    "",
                    "type",
                    "function",
                    "function",
                    GoogleProtocol.obj("name", "", "arguments", ""));
            choice.tools.put(toolIndex, target);
          }
          if (call.has("id")) target.put("id", target.getString("id") + call.getString("id"));
          JSONObject function = call.getJSONObject("function"),
              saved = target.getJSONObject("function");
          for (String key : new String[] {"name", "arguments"})
            if (function.has(key)) saved.put(key, saved.getString(key) + function.getString(key));
          if (call.has("thought_signature"))
            target.put("thought_signature", call.getString("thought_signature"));
        }
      if (!item.isNull("finish_reason")) choice.finish = item.getString("finish_reason");
      deltas.put(
          GoogleProtocol.obj("index", index, "delta", delta, "finish_reason", JSONObject.NULL));
    }
    if (stream && deltas.length() > 0) event(packet(deltas));
  }

  private JSONObject packet(JSONArray values) throws Exception {
    return GoogleProtocol.obj(
        "id",
        id,
        "object",
        stream ? "chat.completion.chunk" : "chat.completion",
        "model",
        model,
        "created",
        System.currentTimeMillis() / 1000,
        "choices",
        values);
  }

  void complete() throws Exception {
    if (done || choices.size() != expected) throw new IOException("Missing completion");
    JSONArray values = new JSONArray();
    for (Map.Entry<Integer, Choice> entry : choices.entrySet()) {
      Choice choice = entry.getValue();
      if (!java.util.Arrays.asList("stop", "tool_calls", "length", "content_filter")
          .contains(choice.finish)) throw new IOException("Invalid finish reason");
      JSONArray tools = new JSONArray();
      for (JSONObject tool : choice.tools.values()) {
        JSONObject function = tool.getJSONObject("function");
        if (tool.getString("id").isEmpty() || function.getString("name").isEmpty())
          throw new IOException("Missing tool identity");
        if (!choice.finish.equals("length") && !choice.finish.equals("content_filter")) {
          JSONTokener parser = new JSONTokener(function.getString("arguments"));
          if (!(parser.nextValue() instanceof JSONObject) || parser.nextClean() != 0)
            throw new IOException("Invalid tool arguments");
        }
        tools.put(tool);
      }
      if (tools.length() > 0) choice.message.put("tool_calls", tools);
      values.put(
          GoogleProtocol.obj(
              "index",
              entry.getKey(),
              stream ? "delta" : "message",
              stream ? new JSONObject() : choice.message,
              "finish_reason",
              choice.finish));
    }
    if (stream) {
      event(packet(values));
      if (includeUsage && usage != null) event(packet(new JSONArray()).put("usage", usage));
      write("data: [DONE]\n\n");
    } else {
      JSONObject result = packet(values);
      if (usage != null) result.put("usage", usage);
      BridgeHttpServer.json(out, 200, result, backend);
    }
    done = true;
  }

  private void event(JSONObject packet) throws Exception {
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
    write("data: " + packet + "\n\n");
  }

  private void write(String text) throws IOException {
    out.write(text.getBytes(StandardCharsets.UTF_8));
    out.flush();
  }

  void error() throws Exception {
    if (started) event(GoogleProtocol.obj("error", GoogleProtocol.obj("message", "来源流响应在完成前中断")));
    else BridgeHttpServer.fail(out, 502, "来源生成未完成");
  }
}
