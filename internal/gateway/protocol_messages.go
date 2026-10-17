package gateway

import (
	"encoding/json"
	"errors"
)

func messagesInputToChat(input map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for _, key := range []string{"model", "stream", "max_tokens", "temperature", "top_p", "top_k", "thinking"} {
		if v, ok := input[key]; ok {
			out[key] = v
		}
	}
	if v, ok := input["stop_sequences"]; ok {
		out["stop"] = v
	}
	for _, key := range []string{"container", "context_management", "mcp_servers", "service_tier"} {
		if input[key] != nil {
			return nil, errors.New("此来源无法转换 Messages 参数：" + key)
		}
	}
	messages := []any{}
	if system := input["system"]; system != nil {
		text, err := messagesText(system)
		if err != nil {
			return nil, err
		}
		messages = append(messages, map[string]any{"role": "system", "content": text})
	}
	items, ok := input["messages"].([]any)
	if !ok || len(items) == 0 {
		return nil, errors.New("需要非空 messages")
	}
	for _, value := range items {
		m, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("消息无效")
		}
		role, _ := m["role"].(string)
		if role != "user" && role != "assistant" {
			return nil, errors.New("消息角色无效")
		}
		if text, ok := m["content"].(string); ok {
			messages = append(messages, map[string]any{"role": role, "content": text})
			continue
		}
		parts, ok := m["content"].([]any)
		if !ok {
			return nil, errors.New("content 无效")
		}
		content, calls, details := []any{}, []any{}, []any{}
		for _, v := range parts {
			p, ok := v.(map[string]any)
			if !ok {
				return nil, errors.New("内容项目无效")
			}
			if p["cache_control"] != nil {
				return nil, errors.New("此来源无法转换显式缓存控制")
			}
			switch p["type"] {
			case "text":
				content = append(content, map[string]any{"type": "text", "text": p["text"]})
			case "image":
				source, ok := p["source"].(map[string]any)
				if !ok {
					return nil, errors.New("图片来源无效")
				}
				url := ""
				if source["type"] == "base64" {
					mime, _ := source["media_type"].(string)
					data, _ := source["data"].(string)
					url = "data:" + mime + ";base64," + data
				} else if source["type"] == "url" {
					url, _ = source["url"].(string)
				}
				if url == "" {
					return nil, errors.New("图片类型无法转换")
				}
				content = append(content, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
			case "tool_use":
				if role != "assistant" {
					return nil, errors.New("tool_use 必须属于 assistant")
				}
				args, err := json.Marshal(p["input"])
				if err != nil || p["id"] == nil || p["name"] == nil {
					return nil, errors.New("工具调用无效")
				}
				call := map[string]any{"id": p["id"], "type": "function", "function": map[string]any{"name": p["name"], "arguments": string(args)}}
				copyToolMetadata(call, p)
				calls = append(calls, call)
			case "tool_result":
				if role != "user" || p["tool_use_id"] == nil {
					return nil, errors.New("工具结果无效")
				}
				text, err := messagesText(p["content"])
				if err != nil {
					return nil, err
				}
				if p["is_error"] == true {
					data, _ := json.Marshal(map[string]any{"error": text})
					text = string(data)
				}
				messages = append(messages, map[string]any{"role": "tool", "tool_call_id": p["tool_use_id"], "content": text})
			case "thinking", "redacted_thinking":
				if role != "assistant" {
					return nil, errors.New("思考块必须属于 assistant")
				}
				details = append(details, p)
			default:
				return nil, errors.New("该 Messages 内容类型无法转换到此来源")
			}
		}
		if len(content) > 0 || len(calls) > 0 || len(details) > 0 {
			message := map[string]any{"role": role, "content": content}
			if len(calls) > 0 {
				message["tool_calls"] = calls
			}
			if len(details) > 0 {
				message["reasoning_details"] = details
			}
			messages = append(messages, message)
		}
	}
	out["messages"] = messages
	if tools, ok := input["tools"].([]any); ok {
		list := []any{}
		for _, v := range tools {
			t, ok := v.(map[string]any)
			if !ok {
				return nil, errors.New("tools 无效")
			}
			if kind := t["type"]; kind != nil && kind != "custom" {
				return nil, errors.New("此来源只接受客户端函数工具")
			}
			if t["cache_control"] != nil {
				return nil, errors.New("此来源无法转换工具缓存控制")
			}
			f := map[string]any{"name": t["name"], "parameters": t["input_schema"]}
			if d, ok := t["description"]; ok {
				f["description"] = d
			}
			list = append(list, map[string]any{"type": "function", "function": f})
		}
		out["tools"] = list
	}
	if c, ok := input["tool_choice"].(map[string]any); ok {
		switch c["type"] {
		case "auto", "none":
			out["tool_choice"] = c["type"]
		case "any":
			out["tool_choice"] = "required"
		case "tool":
			out["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": c["name"]}}
		default:
			return nil, errors.New("tool_choice 无效")
		}
		if v, ok := c["disable_parallel_tool_use"].(bool); ok {
			out["parallel_tool_calls"] = !v
		}
	}
	return out, nil
}

func messagesText(value any) (string, error) {
	if text, ok := value.(string); ok {
		return text, nil
	}
	parts, ok := value.([]any)
	if !ok {
		return "", errors.New("内容需要文本或文本数组")
	}
	text := ""
	for _, v := range parts {
		p, ok := v.(map[string]any)
		if !ok || p["type"] != "text" || p["cache_control"] != nil {
			return "", errors.New("此内容无法转换为文本")
		}
		t, ok := p["text"].(string)
		if !ok {
			return "", errors.New("文本无效")
		}
		text += t
	}
	return text, nil
}

func chatResultToMessages(chat map[string]any, model string) (map[string]any, error) {
	converted, err := chatResultToResponses(chat, model)
	if err != nil {
		return nil, err
	}
	content := []any{}
	stop := "end_turn"
	for _, v := range converted["output"].([]any) {
		item := v.(map[string]any)
		switch item["type"] {
		case "message":
			for _, p := range item["content"].([]any) {
				part := p.(map[string]any)
				content = append(content, map[string]any{"type": "text", "text": part["text"]})
			}
		case "reasoning":
			for _, p := range item["summary"].([]any) {
				part := p.(map[string]any)
				content = append(content, map[string]any{"type": "thinking", "thinking": part["text"], "signature": ""})
			}
		case "function_call":
			var args any
			if json.Unmarshal([]byte(item["arguments"].(string)), &args) != nil {
				return nil, errors.New("截断的工具参数无法表示为完整 Messages input；请使用流式或 Responses 接口")
			}
			tool := map[string]any{"type": "tool_use", "id": item["call_id"], "name": item["name"], "input": args}
			copyToolMetadata(tool, item)
			content = append(content, tool)
			stop = "tool_use"
		}
	}
	if d, ok := converted["incomplete_details"].(map[string]any); ok {
		if d["reason"] == "content_filter" {
			stop = "refusal"
		} else {
			stop = "max_tokens"
		}
	}
	u := map[string]any{"input_tokens": 0, "output_tokens": 0}
	if usage, ok := converted["usage"].(map[string]any); ok {
		u["input_tokens"], u["output_tokens"] = usage["input_tokens"], usage["output_tokens"]
	}
	return map[string]any{"id": "msg_" + newKey(""), "type": "message", "role": "assistant", "model": model, "content": content, "stop_reason": stop, "stop_sequence": nil, "usage": u}, nil
}
