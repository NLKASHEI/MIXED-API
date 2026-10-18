package com.mixedapi.local;

import static com.mixedapi.local.GoogleProtocol.*;

import java.io.IOException;
import java.util.*;
import org.json.*;

/** Explicit choice validation for native Gemini and Playground's emulated tool choice. */
final class GoogleTools {
  private final boolean required, none, single, active;
  private final Set<String> names = new TreeSet<>();
  private final Map<Integer, Integer> counts = new HashMap<>();

  GoogleTools(JSONObject input) throws Exception {
    boolean parallel = true;
    if (input.has("parallel_tool_calls")) {
      Object v = input.get("parallel_tool_calls");
      if (!(v instanceof Boolean)) throw new IOException("Invalid parallel_tool_calls");
      parallel = (Boolean) v;
    }
    Object choice = input.opt("tool_choice");
    String mode = "auto", name = "";
    if (choice instanceof JSONObject) {
      JSONObject c = (JSONObject) choice;
      if (!c.optString("type").equals("function")) throw new IOException("Invalid tool choice");
      name = ProtocolRequest.required(c.getJSONObject("function"), "name");
      mode = "required";
    } else if (choice != null) {
      if (!(choice instanceof String)) throw new IOException("Invalid tool choice");
      mode = (String) choice;
    }
    if (!Arrays.asList("auto", "none", "required").contains(mode))
      throw new IOException("Invalid tool choice");
    required = mode.equals("required");
    none = mode.equals("none");
    single = !parallel;
    active = required || none || single;
    if (!active) return;
    JSONArray tools = input.optJSONArray("tools");
    if (tools != null)
      for (int i = 0; i < tools.length(); i++) {
        String n =
            ProtocolRequest.required(tools.getJSONObject(i).getJSONObject("function"), "name");
        if (!names.add(n)) throw new IOException("Duplicate tool name");
      }
    if (!name.isEmpty()) {
      if (!names.contains(name)) throw new IOException("Unknown chosen tool");
      names.clear();
      names.add(name);
    }
    if (required && names.isEmpty()) throw new IOException("Required choice needs function tools");
  }

  void prepare(JSONObject gem, boolean playground) throws Exception {
    if (playground) gem.remove("toolConfig");
    if (!active) return;
    if (none) {
      gem.remove("tools");
      return;
    }
    if (playground) {
      JSONArray tools = gem.optJSONArray("tools");
      if (tools != null)
        for (int i = 0; i < tools.length(); i++) {
          JSONObject tool = tools.getJSONObject(i);
          JSONArray declarations = tool.optJSONArray("functionDeclarations");
          if (declarations == null) continue;
          JSONArray selected = new JSONArray();
          for (int n = 0; n < declarations.length(); n++) {
            JSONObject d = declarations.getJSONObject(n);
            if (names.contains(d.getString("name"))) selected.put(d);
          }
          tool.put("functionDeclarations", selected);
        }
    }
    String instruction =
        playground && required
            ? "Use one of these functions before answering: " + String.join(", ", names) + "."
            : "";
    if (single) instruction += "\nCall at most one function in this response.";
    if (!instruction.trim().isEmpty()) {
      JSONObject system = gem.optJSONObject("systemInstruction");
      if (system == null) system = obj("parts", new JSONArray());
      system.getJSONArray("parts").put(obj("text", instruction));
      gem.put("systemInstruction", system);
    }
  }

  void check(JSONObject candidate) throws Exception {
    if (!active) return;
    int index = candidate.optInt("index");
    JSONObject content = candidate.optJSONObject("content");
    JSONArray parts = content == null ? null : content.optJSONArray("parts");
    if (parts != null)
      for (int i = 0; i < parts.length(); i++) {
        JSONObject call = parts.getJSONObject(i).optJSONObject("functionCall");
        if (call == null) continue;
        int count = counts.getOrDefault(index, 0) + 1;
        counts.put(index, count);
        if (none
            || !names.contains(call.optString("name"))
            || single && count > 1
            || call.optJSONObject("args") == null)
          throw new IOException("Provider violated tool contract");
      }
    String finish = candidate.optString("finishReason");
    if (finish.equals("STOP") && required && counts.getOrDefault(index, 0) == 0)
      throw new IOException("Required tool not called");
  }
}
