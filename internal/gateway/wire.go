package gateway

// Shared wire conversion. The phone v58 implementation supplied useful
// Responses reasoning/usage mapping examples; see THIRD_PARTY_NOTICES.md.
// No tools are invented or executed by this module.
import (
	"encoding/json"
	"errors"
	"time"
)

func payloadObject(payload map[string]json.RawMessage) map[string]any {
	out := map[string]any{}
	for key, raw := range payload {
		var v any
		if json.Unmarshal(raw, &v) == nil {
			out[key] = v
		}
	}
	return out
}

func chatToResponses(chat map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for _, key := range []string{"model", "stream", "temperature", "top_p", "parallel_tool_calls", "store", "metadata"} {
		if v, ok := chat[key]; ok {
			out[key] = v
		}
	}
	if v, ok := chat["max_completion_tokens"]; ok {
		out["max_output_tokens"] = v
	} else if v, ok := chat["max_tokens"]; ok {
		out["max_output_tokens"] = v
	}
	if v, ok := chat["reasoning_effort"].(string); ok && v != "none" {
		out["reasoning"] = map[string]any{"effort": v, "summary": "auto"}
	}
	if v, ok := chat["reasoning"].(map[string]any); ok {
		out["reasoning"] = v
	}
	messages, ok := chat["messages"].([]any)
	if !ok || len(messages) == 0 {
		return nil, errors.New("需要非空 messages 数组")
	}
	input := []any{}
	for _, value := range messages {
		m, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("messages 格式无效")
		}
		role, _ := m["role"].(string)
		if role == "tool" {
			id, ok := m["tool_call_id"].(string)
			if !ok || id == "" {
				return nil, errors.New("工具结果缺少 tool_call_id")
			}
			output := m["content"]
			if _, ok := output.(string); !ok {
				data, _ := json.Marshal(output)
				output = string(data)
			}
			input = append(input, map[string]any{"type": "function_call_output", "call_id": id, "output": output})
			continue
		}
		if role != "system" && role != "developer" && role != "user" && role != "assistant" {
			return nil, errors.New("消息角色不支持")
		}
		if content := m["content"]; content != nil {
			if parts, ok := content.([]any); ok {
				converted := []any{}
				for _, v := range parts {
					p, ok := v.(map[string]any)
					if !ok {
						return nil, errors.New("内容数组无效")
					}
					switch p["type"] {
					case "text":
						kind := "input_text"
						if role == "assistant" {
							kind = "output_text"
						}
						converted = append(converted, map[string]any{"type": kind, "text": p["text"]})
					case "image_url":
						img, ok := p["image_url"].(map[string]any)
						if !ok {
							return nil, errors.New("图片内容无效")
						}
						part := map[string]any{"type": "input_image", "image_url": img["url"]}
						if detail, ok := img["detail"]; ok {
							part["detail"] = detail
						}
						converted = append(converted, part)
					default:
						return nil, errors.New("该内容类型尚不支持")
					}
				}
				content = converted
			}
			input = append(input, map[string]any{"type": "message", "role": role, "content": content})
		}
		if calls, ok := m["tool_calls"].([]any); ok {
			for _, v := range calls {
				c, ok := v.(map[string]any)
				if !ok {
					return nil, errors.New("工具调用无效")
				}
				f, ok := c["function"].(map[string]any)
				if !ok {
					return nil, errors.New("工具调用无效")
				}
				input = append(input, map[string]any{"type": "function_call", "call_id": c["id"], "name": f["name"], "arguments": f["arguments"]})
			}
		}
	}
	out["input"] = input
	if tools, ok := chat["tools"].([]any); ok {
		converted := []any{}
		for _, v := range tools {
			t, ok := v.(map[string]any)
			if !ok {
				return nil, errors.New("tools 无效")
			}
			if t["type"] != "function" {
				return nil, errors.New("仅支持 function 工具")
			}
			f, ok := t["function"].(map[string]any)
			if !ok {
				return nil, errors.New("function 无效")
			}
			tool := map[string]any{"type": "function"}
			for k, v := range f {
				tool[k] = v
			}
			converted = append(converted, tool)
		}
		out["tools"] = converted
	}
	if choice, ok := chat["tool_choice"]; ok {
		if c, ok := choice.(map[string]any); ok {
			f, ok := c["function"].(map[string]any)
			if !ok {
				return nil, errors.New("tool_choice 无效")
			}
			choice = map[string]any{"type": "function", "name": f["name"]}
		}
		out["tool_choice"] = choice
	}
	if format, ok := chat["response_format"].(map[string]any); ok {
		f := format
		if format["type"] == "json_schema" {
			schema, ok := format["json_schema"].(map[string]any)
			if !ok {
				return nil, errors.New("json_schema 无效")
			}
			f = map[string]any{"type": "json_schema"}
			for k, v := range schema {
				f[k] = v
			}
		}
		out["text"] = map[string]any{"format": f}
	}
	return out, nil
}

func responsesToChat(resp map[string]any, model string) map[string]any {
	text, reasoning := "", ""
	calls := []any{}
	if output, ok := resp["output"].([]any); ok {
		for _, v := range output {
			item, ok := v.(map[string]any)
			if !ok {
				continue
			}
			if item["type"] == "function_call" {
				id := item["call_id"]
				if id == nil {
					id = item["id"]
				}
				calls = append(calls, map[string]any{"id": id, "type": "function", "function": map[string]any{"name": item["name"], "arguments": item["arguments"]}})
			}
			if content, ok := item["content"].([]any); ok {
				for _, v := range content {
					if part, ok := v.(map[string]any); ok && part["type"] == "output_text" {
						if t, ok := part["text"].(string); ok {
							text += t
						}
					}
				}
			}
			if item["type"] == "reasoning" {
				if summary, ok := item["summary"].([]any); ok {
					for _, v := range summary {
						if p, ok := v.(map[string]any); ok {
							if t, ok := p["text"].(string); ok {
								reasoning += t
							}
						}
					}
				}
			}
		}
	}
	message := map[string]any{"role": "assistant", "content": text}
	if reasoning != "" {
		message["reasoning_content"] = reasoning
	}
	if len(calls) > 0 {
		message["tool_calls"] = calls
	}
	finish := "stop"
	if len(calls) > 0 {
		finish = "tool_calls"
	}
	if resp["status"] == "incomplete" {
		finish = "length"
		if d, ok := resp["incomplete_details"].(map[string]any); ok && d["reason"] == "content_filter" {
			finish = "content_filter"
		}
	}
	out := map[string]any{"id": resp["id"], "object": "chat.completion", "model": model, "created": time.Now().Unix(), "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}}
	if usage, ok := resp["usage"].(map[string]any); ok {
		out["usage"] = chatUsage(usage)
	}
	return out
}

func chatUsage(usage map[string]any) map[string]any {
	out := map[string]any{"prompt_tokens": usage["input_tokens"], "completion_tokens": usage["output_tokens"], "total_tokens": usage["total_tokens"]}
	if detail, ok := usage["input_tokens_details"]; ok {
		out["prompt_tokens_details"] = detail
	}
	if detail, ok := usage["output_tokens_details"]; ok {
		out["completion_tokens_details"] = detail
	}
	return out
}
