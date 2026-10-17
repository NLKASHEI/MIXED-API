package com.mixedapi.local;

import static com.mixedapi.local.GoogleProtocol.arr;
import static com.mixedapi.local.GoogleProtocol.obj;

import java.io.IOException;
import java.io.InputStream;
import java.util.HashMap;
import java.util.Map;
import org.json.JSONObject;

/** Stateful Messages events; incomplete blocks and malformed tools cannot complete. */
final class NativeMessagesStream {
  private final NativeChat chat;
  private final Map<Integer, JSONObject> blocks = new HashMap<>();
  private final Map<Integer, Integer> tools = new HashMap<>();
  private JSONObject usage = new JSONObject();
  private String finish = "";
  private boolean started;
  private int details;
  private long size;

  private NativeMessagesStream(NativeChat chat) {
    this.chat = chat;
  }

  static void read(InputStream input, NativeChat chat) throws Exception {
    NativeMessagesStream reader = new NativeMessagesStream(chat);
    NativeWire.events(input, reader::event);
    if (!chat.done) throw new IOException("Incomplete Messages stream");
  }

  private void event(String data) throws Exception {
    size += data.length();
    if (size > 16 * 1024 * 1024) throw new IOException("Messages stream too large");
    JSONObject event = new JSONObject(data), delta = new JSONObject();
    String type = event.getString("type");
    if (type.equals("ping")) return;
    if (!started && !type.equals("message_start")) throw new IOException("Missing message start");
    switch (type) {
      case "message_start":
        if (started) throw new IOException("Duplicate message start");
        started = true;
        usage = event.getJSONObject("message").getJSONObject("usage");
        delta.put("role", "assistant");
        break;
      case "content_block_start":
        {
          if (!finish.isEmpty()) throw new IOException("Data after finish");
          int index = event.getInt("index");
          if (index < 0 || index > 1024 || blocks.containsKey(index))
            throw new IOException("Invalid block index");
          JSONObject block = event.getJSONObject("content_block");
          blocks.put(index, block);
          switch (block.getString("type")) {
            case "text":
              delta.put("content", block.getString("text"));
              break;
            case "thinking":
              delta.put("reasoning_content", block.getString("thinking"));
              break;
            case "redacted_thinking":
              break;
            case "tool_use":
              int toolIndex = tools.size();
              tools.put(index, toolIndex);
              String args =
                  block.getJSONObject("input").length() > 0
                      ? block.getJSONObject("input").toString()
                      : "";
              block.put("mixed_args", !args.isEmpty());
              delta.put(
                  "tool_calls",
                  arr(
                      obj(
                          "index",
                          toolIndex,
                          "id",
                          block.getString("id"),
                          "type",
                          "function",
                          "function",
                          obj("name", block.getString("name"), "arguments", args))));
              break;
            default:
              throw new IOException("Unsupported Messages block");
          }
          break;
        }
      case "content_block_delta":
        {
          int index = event.getInt("index");
          JSONObject block = blocks.get(index), change = event.getJSONObject("delta");
          if (block == null || block.optBoolean("mixed_closed"))
            throw new IOException("Unknown block delta");
          switch (change.getString("type")) {
            case "text_delta":
              require(block, "text");
              delta.put("content", change.getString("text"));
              break;
            case "thinking_delta":
              require(block, "thinking");
              block.put("thinking", block.getString("thinking") + change.getString("thinking"));
              delta.put("reasoning_content", change.getString("thinking"));
              break;
            case "signature_delta":
              require(block, "thinking");
              block.put("signature", block.optString("signature") + change.getString("signature"));
              break;
            case "input_json_delta":
              require(block, "tool_use");
              block.put("mixed_args", true);
              delta.put(
                  "tool_calls",
                  arr(
                      obj(
                          "index",
                          tools.get(index),
                          "function",
                          obj("arguments", change.getString("partial_json")))));
              break;
            default:
              throw new IOException("Unsupported Messages delta");
          }
          break;
        }
      case "content_block_stop":
        {
          int index = event.getInt("index");
          JSONObject block = blocks.get(index);
          if (block == null || block.optBoolean("mixed_closed"))
            throw new IOException("Unknown block stop");
          block.put("mixed_closed", true);
          String kind = block.getString("type");
          if (kind.equals("thinking") || kind.equals("redacted_thinking"))
            delta.put("reasoning_details", arr(NativeMessages.detail(block, details++)));
          else if (kind.equals("tool_use") && !block.optBoolean("mixed_args"))
            delta.put(
                "tool_calls",
                arr(obj("index", tools.get(index), "function", obj("arguments", "{}"))));
          break;
        }
      case "message_delta":
        JSONObject change = event.getJSONObject("delta");
        if (!change.isNull("stop_reason"))
          finish = NativeMessages.finish(change.getString("stop_reason"));
        JSONObject incoming = event.optJSONObject("usage");
        if (incoming != null)
          for (String key : GoogleProtocol.keys(incoming)) usage.put(key, incoming.get(key));
        return;
      case "message_stop":
        if (finish.isEmpty()) throw new IOException("Missing finish");
        for (JSONObject b : blocks.values())
          if (!b.optBoolean("mixed_closed")) throw new IOException("Unfinished content block");
        chat.chunk(
            obj(
                "choices",
                arr(obj("index", 0, "delta", delta, "finish_reason", finish)),
                "usage",
                NativeMessages.usage(usage)));
        chat.complete();
        throw new NativeWire.End();
      default:
        throw new IOException("Unsupported Messages event");
    }
    if (delta.length() > 0)
      chat.chunk(
          obj("choices", arr(obj("index", 0, "delta", delta, "finish_reason", JSONObject.NULL))));
  }

  private static void require(JSONObject block, String type) throws Exception {
    if (!block.getString("type").equals(type)) throw new IOException("Wrong block delta type");
  }
}
