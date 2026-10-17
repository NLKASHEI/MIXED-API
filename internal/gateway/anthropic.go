package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// Protocol conversion only. All tools belong to the client; none are executed
// by MIXED. Provider selection remains fixed for the complete request.
func chatToAnthropic(chat map[string]any) (map[string]any, error) {
	out := map[string]any{"model": chat["model"], "max_tokens": 4096}
	for _, key := range []string{"stream", "temperature", "top_p", "top_k", "max_tokens"} {
		if v, ok := chat[key]; ok {
			out[key] = v
		}
	}
	if v, ok := chat["max_completion_tokens"]; ok {
		out["max_tokens"] = v
	}
	if v, ok := chat["stop"]; ok {
		if text, ok := v.(string); ok {
			v = []any{text}
		}
		out["stop_sequences"] = v
	}
	if n, ok := chat["n"].(float64); ok && n != 1 {
		return nil, errors.New("该模型只支持 n=1")
	}
	if err := configureAnthropicThinking(chat, out); err != nil {
		return nil, err
	}
	system := []string{}
	messages := []any{}
	input, ok := chat["messages"].([]any)
	if !ok || len(input) == 0 {
		return nil, errors.New("需要非空 messages 数组")
	}
	for _, value := range input {
		m, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("messages 无效")
		}
		role, _ := m["role"].(string)
		if role == "system" || role == "developer" {
			if text, ok := m["content"].(string); ok {
				system = append(system, text)
			} else {
				return nil, errors.New("系统消息需要文本")
			}
			continue
		}
		parts := []any{}
		if role == "assistant" {
			if details, ok := m["reasoning_details"].([]any); ok {
				for _, value := range details {
					d, ok := value.(map[string]any)
					if !ok || d["format"] != "anthropic-claude-v1" {
						return nil, errors.New("思考续接数据无效")
					}
					switch d["type"] {
					case "reasoning.text":
						text, textOK := d["text"].(string)
						signature, sigOK := d["signature"].(string)
						if !textOK || !sigOK || signature == "" {
							return nil, errors.New("思考续接缺少签名")
						}
						parts = append(parts, map[string]any{"type": "thinking", "thinking": text, "signature": signature})
					case "reasoning.encrypted":
						data, ok := d["data"].(string)
						if !ok || data == "" {
							return nil, errors.New("思考续接数据无效")
						}
						parts = append(parts, map[string]any{"type": "redacted_thinking", "data": data})
					default:
						return nil, errors.New("思考续接类型无效")
					}
				}
			}
		}
		if role == "tool" {
			id, _ := m["tool_call_id"].(string)
			if id == "" {
				return nil, errors.New("工具结果缺少 tool_call_id")
			}
			content := m["content"]
			if _, ok := content.(string); !ok {
				raw, _ := json.Marshal(content)
				content = string(raw)
			}
			parts = append(parts, map[string]any{"type": "tool_result", "tool_use_id": id, "content": content})
			role = "user"
		} else {
			if role != "user" && role != "assistant" {
				return nil, errors.New("消息角色不支持")
			}
			switch content := m["content"].(type) {
			case nil:
			case string:
				if content != "" {
					parts = append(parts, map[string]any{"type": "text", "text": content})
				}
			case []any:
				for _, value := range content {
					p, ok := value.(map[string]any)
					if !ok {
						return nil, errors.New("消息内容无效")
					}
					switch p["type"] {
					case "text":
						parts = append(parts, map[string]any{"type": "text", "text": p["text"]})
					case "image_url":
						image, ok := p["image_url"].(map[string]any)
						if !ok {
							return nil, errors.New("图片无效")
						}
						link, ok := image["url"].(string)
						if !ok {
							return nil, errors.New("图片地址无效")
						}
						var source map[string]any
						if strings.HasPrefix(link, "data:image/") {
							meta, data, ok := strings.Cut(link, ",")
							if !ok || !strings.HasSuffix(meta, ";base64") {
								return nil, errors.New("图片需要 base64 data URL")
							}
							source = map[string]any{"type": "base64", "media_type": strings.TrimSuffix(strings.TrimPrefix(meta, "data:"), ";base64"), "data": data}
						} else if strings.HasPrefix(link, "https://") {
							source = map[string]any{"type": "url", "url": link}
						} else {
							return nil, errors.New("图片需要 HTTPS 或 base64 data URL")
						}
						parts = append(parts, map[string]any{"type": "image", "source": source})
					default:
						return nil, errors.New("该消息内容类型尚不支持")
					}
				}
			default:
				return nil, errors.New("消息内容无效")
			}
			if calls, ok := m["tool_calls"].([]any); ok {
				for _, value := range calls {
					call, ok := value.(map[string]any)
					if !ok {
						return nil, errors.New("工具调用无效")
					}
					f, ok := call["function"].(map[string]any)
					if !ok {
						return nil, errors.New("工具调用无效")
					}
					args, _ := f["arguments"].(string)
					var input map[string]any
					if json.Unmarshal([]byte(args), &input) != nil || input == nil {
						return nil, errors.New("工具参数需要 JSON 对象")
					}
					parts = append(parts, map[string]any{"type": "tool_use", "id": call["id"], "name": f["name"], "input": input})
				}
			}
		}
		if len(parts) == 0 {
			continue
		}
		if len(messages) > 0 {
			last := messages[len(messages)-1].(map[string]any)
			if last["role"] == role {
				last["content"] = append(last["content"].([]any), parts...)
				continue
			}
		}
		messages = append(messages, map[string]any{"role": role, "content": parts})
	}
	if len(messages) == 0 {
		return nil, errors.New("需要用户或助手消息")
	}
	out["messages"] = messages
	if len(system) > 0 {
		out["system"] = strings.Join(system, "\n\n")
	}
	if tools, ok := chat["tools"].([]any); ok {
		mapped := []any{}
		for _, value := range tools {
			tool, ok := value.(map[string]any)
			if !ok || tool["type"] != "function" {
				return nil, errors.New("仅支持 function 工具")
			}
			f, ok := tool["function"].(map[string]any)
			if !ok {
				return nil, errors.New("工具声明无效")
			}
			item := map[string]any{"name": f["name"], "input_schema": f["parameters"]}
			if item["input_schema"] == nil {
				item["input_schema"] = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			for _, key := range []string{"description", "strict"} {
				if value, ok := f[key]; ok {
					item[key] = value
				}
			}
			mapped = append(mapped, item)
		}
		out["tools"] = mapped
	}
	if choice, ok := chat["tool_choice"]; ok {
		switch c := choice.(type) {
		case string:
			typ := map[string]string{"auto": "auto", "none": "none", "required": "any"}[c]
			if typ == "" {
				return nil, errors.New("tool_choice 无效")
			}
			out["tool_choice"] = map[string]any{"type": typ}
		case map[string]any:
			f, ok := c["function"].(map[string]any)
			if !ok || f["name"] == nil {
				return nil, errors.New("tool_choice 无效")
			}
			out["tool_choice"] = map[string]any{"type": "tool", "name": f["name"]}
		default:
			return nil, errors.New("tool_choice 无效")
		}
	}
	if parallel, ok := chat["parallel_tool_calls"].(bool); ok && !parallel {
		choice, _ := out["tool_choice"].(map[string]any)
		if choice == nil {
			choice = map[string]any{"type": "auto"}
		}
		choice["disable_parallel_tool_use"] = true
		out["tool_choice"] = choice
	}
	if format, ok := chat["response_format"].(map[string]any); ok && format["type"] != "text" {
		return nil, errors.New("该转换暂不支持 response_format，请使用模型的原生接口")
	}
	if out["thinking"] != nil {
		if choice, ok := out["tool_choice"].(map[string]any); ok && (choice["type"] == "any" || choice["type"] == "tool") {
			return nil, errors.New("思考模式不支持强制工具选择，请使用 auto")
		}
	}
	return out, nil
}

func configureAnthropicThinking(chat, out map[string]any) error {
	effort, exists := chat["reasoning_effort"]
	if !exists {
		return nil
	}
	if effort == "none" {
		out["thinking"] = map[string]any{"type": "disabled"}
		return nil
	}
	budgets := map[string]int{"low": 1024, "medium": 4096, "high": 8192, "max": 16384}
	level, ok := effort.(string)
	if !ok {
		return errors.New("reasoning_effort 需要字符串")
	}
	budget, ok := budgets[level]
	if !ok {
		return errors.New("该模型不支持此 reasoning_effort")
	}
	model, _ := chat["model"].(string)
	adaptive := false
	for _, prefix := range []string{"claude-opus-4-6", "claude-sonnet-4-6", "claude-opus-4-7", "claude-opus-4-8", "claude-sonnet-4-7", "claude-opus-5", "claude-sonnet-5", "claude-fable-5", "claude-haiku-5"} {
		if model == prefix || strings.HasPrefix(model, prefix+"-") {
			adaptive = true
		}
	}
	if adaptive {
		if level == "max" && !strings.HasPrefix(model, "claude-opus-") {
			return errors.New("该模型不支持 max 思考强度")
		}
		out["thinking"] = map[string]any{"type": "adaptive"}
		out["output_config"] = map[string]any{"effort": level}
		return nil
	}
	manual := false
	for _, prefix := range []string{"claude-3-7-sonnet", "claude-opus-4", "claude-sonnet-4", "claude-haiku-4-5"} {
		if model == prefix || strings.HasPrefix(model, prefix+"-") {
			manual = true
		}
	}
	if !manual {
		return errors.New("此模型尚无已核对的思考模式，请移除 reasoning_effort 或使用原生 Messages 接口")
	}
	limit := 4096
	if n, ok := out["max_tokens"].(float64); ok {
		limit = int(n)
	}
	if limit <= 1024 {
		return errors.New("启用思考时 max_tokens 必须大于 1024")
	}
	if budget >= limit {
		budget = limit - 1
	}
	out["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget}
	if strings.HasPrefix(model, "claude-opus-4-5") {
		if level == "max" {
			level = "high"
		}
		out["output_config"] = map[string]any{"effort": level}
	}
	return nil
}

type anthropicUsage struct {
	Input      int `json:"input_tokens"`
	Output     int `json:"output_tokens"`
	Cached     int `json:"cache_read_input_tokens"`
	CacheWrite int `json:"cache_creation_input_tokens"`
}

func (u anthropicUsage) chat() map[string]any {
	input := u.Input + u.Cached + u.CacheWrite
	return map[string]any{"prompt_tokens": input, "completion_tokens": u.Output, "total_tokens": input + u.Output, "prompt_tokens_details": map[string]int{"cached_tokens": u.Cached}}
}

type anthropicBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Signature string          `json:"signature"`
	Data      string          `json:"data"`
	Input     json.RawMessage `json:"input"`
}
type anthropicMessage struct {
	ID      string           `json:"id"`
	Type    string           `json:"type"`
	Content []anthropicBlock `json:"content"`
	Stop    string           `json:"stop_reason"`
	Usage   anthropicUsage   `json:"usage"`
}

func anthropicFinish(stop string) string {
	switch stop {
	case "tool_use":
		return "tool_calls"
	case "max_tokens", "model_context_window_exceeded":
		return "length"
	case "refusal":
		return "content_filter"
	default:
		return "stop"
	}
}
func anthropicToChat(m anthropicMessage, model string) map[string]any {
	text, thought := "", ""
	calls := []any{}
	details := []any{}
	for _, p := range m.Content {
		if d := p.reasoningDetail(len(details)); d != nil {
			details = append(details, d)
		}
		switch p.Type {
		case "text":
			text += p.Text
		case "thinking":
			thought += p.Thinking
		case "tool_use":
			calls = append(calls, map[string]any{"id": p.ID, "type": "function", "function": map[string]any{"name": p.Name, "arguments": string(p.Input)}})
		}
	}
	message := map[string]any{"role": "assistant", "content": text}
	if len(details) > 0 {
		message["reasoning_details"] = details
	}
	if thought != "" {
		message["reasoning_content"] = thought
	}
	if len(calls) > 0 {
		message["tool_calls"] = calls
	}
	return map[string]any{"id": m.ID, "model": model, "created": time.Now().Unix(), "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": anthropicFinish(m.Stop)}}, "usage": m.Usage.chat()}
}

// Keep opaque provider signatures intact; clients replay reasoning_details
// with the assistant message when returning tool results.
func (b anthropicBlock) reasoningDetail(index int) map[string]any {
	if b.Type == "thinking" && b.Signature != "" {
		return map[string]any{"type": "reasoning.text", "text": b.Thinking, "signature": b.Signature, "format": "anthropic-claude-v1", "index": index}
	}
	if b.Type == "redacted_thinking" && b.Data != "" {
		return map[string]any{"type": "reasoning.encrypted", "data": b.Data, "format": "anthropic-claude-v1", "index": index}
	}
	return nil
}

func streamAnthropicAsChat(w http.ResponseWriter, r *http.Request, body io.Reader, model string, includeUsage bool) {
	out := newSSEOutput(w, model)
	indices := map[int]int{}
	thinking := map[int]*anthropicBlock{}
	detailIndex, thinkingBytes := 0, 0
	usage := anthropicUsage{}
	finish := ""
	complete := false
	err := readSSE(r.Context(), body, func(data []byte) error {
		var event struct {
			Type    string           `json:"type"`
			Index   int              `json:"index"`
			Message anthropicMessage `json:"message"`
			Block   anthropicBlock   `json:"content_block"`
			Usage   anthropicUsage   `json:"usage"`
			Delta   struct {
				Type      string `json:"type"`
				Text      string `json:"text"`
				Thinking  string `json:"thinking"`
				Signature string `json:"signature"`
				JSON      string `json:"partial_json"`
				Stop      string `json:"stop_reason"`
			} `json:"delta"`
		}
		if json.Unmarshal(data, &event) != nil {
			return errors.New("invalid Anthropic event")
		}
		switch event.Type {
		case "error":
			return errors.New("provider generation failed")
		case "message_start":
			out.id = event.Message.ID
			usage = event.Message.Usage
			return out.chunk(map[string]any{"role": "assistant", "content": ""}, nil, nil)
		case "content_block_start":
			if event.Block.Type == "thinking" || event.Block.Type == "redacted_thinking" {
				b := event.Block
				thinking[event.Index] = &b
				thinkingBytes += len(b.Thinking) + len(b.Signature) + len(b.Data)
				if thinkingBytes > 4<<20 {
					return errors.New("thinking state too large")
				}
			}
			switch b := event.Block; b.Type {
			case "text":
				if b.Text != "" {
					return out.chunk(map[string]any{"content": b.Text}, nil, nil)
				}
			case "thinking":
				if b.Thinking != "" {
					return out.chunk(map[string]any{"reasoning_content": b.Thinking}, nil, nil)
				}
			case "tool_use":
				index := len(indices)
				indices[event.Index] = index
				return out.chunk(map[string]any{"tool_calls": []any{map[string]any{"index": index, "id": b.ID, "type": "function", "function": map[string]any{"name": b.Name, "arguments": ""}}}}, nil, nil)
			}
		case "content_block_delta":
			if b := thinking[event.Index]; b != nil {
				thinkingBytes += len(event.Delta.Thinking) + len(event.Delta.Signature)
				if thinkingBytes > 4<<20 {
					return errors.New("thinking state too large")
				}
				b.Thinking += event.Delta.Thinking
				b.Signature += event.Delta.Signature
			}
			switch event.Delta.Type {
			case "text_delta":
				return out.chunk(map[string]any{"content": event.Delta.Text}, nil, nil)
			case "thinking_delta":
				return out.chunk(map[string]any{"reasoning_content": event.Delta.Thinking}, nil, nil)
			case "input_json_delta":
				index, ok := indices[event.Index]
				if !ok {
					return errors.New("tool delta without call")
				}
				return out.chunk(map[string]any{"tool_calls": []any{map[string]any{"index": index, "function": map[string]any{"arguments": event.Delta.JSON}}}}, nil, nil)
			}
		case "content_block_stop":
			if b := thinking[event.Index]; b != nil {
				delete(thinking, event.Index)
				if d := b.reasoningDetail(detailIndex); d != nil {
					detailIndex++
					return out.chunk(map[string]any{"reasoning_details": []any{d}}, nil, nil)
				}
			}
		case "message_delta":
			usage.Output = event.Usage.Output
			if event.Delta.Stop != "" {
				finish = anthropicFinish(event.Delta.Stop)
			}
		case "message_stop":
			if finish == "" {
				return errors.New("missing stop reason")
			}
			if err := out.chunk(map[string]any{}, finish, nil); err != nil {
				return err
			}
			if includeUsage {
				if err := out.chunk(nil, nil, usage.chat()); err != nil {
					return err
				}
			}
			complete = true
			return io.EOF
		}
		return nil
	})
	if complete && err == io.EOF {
		out.done()
		return
	}
	if r.Context().Err() == nil {
		_ = out.send(map[string]any{"error": map[string]string{"message": "服务方流响应在完成前中断"}})
	}
}
