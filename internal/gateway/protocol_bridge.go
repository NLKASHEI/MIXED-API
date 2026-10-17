package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// Translates at the integrated provider seam, without a second HTTP listener or
// changing the selected backend. Native protocol requests retain their own path.
func (s *Server) serveProtocolBridge(w http.ResponseWriter, r *http.Request, b Backend, publicModel, model string, payload map[string]json.RawMessage) {
	body, err := responsesInputToChat(payloadObject(payload))
	if r.URL.Path == "/v1/messages" {
		body, err = messagesInputToChat(payloadObject(payload))
	}
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	if body["stream"] == true {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	raw, _ := json.Marshal(body)
	var converted map[string]json.RawMessage
	_ = json.Unmarshal(raw, &converted)
	request := r.Clone(r.Context())
	request.URL.Path = "/v1/chat/completions"
	if body["stream"] == true {
		s.streamProtocolBridge(w, request, b, publicModel, model, converted, r.URL.Path == "/v1/messages")
		return
	}
	buffer := &protocolBuffer{header: make(http.Header)}
	s.serveIntegrated(buffer, request, b, publicModel, model, converted)
	for _, key := range []string{"Retry-After", "X-Mixed-Backend", "X-Mixed-Protocol-Catalog"} {
		if value := buffer.header.Get(key); value != "" {
			w.Header().Set(key, value)
		}
	}
	if buffer.overflow {
		jsonError(w, 502, "服务方响应过大")
		return
	}
	if buffer.status != 200 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(buffer.status)
		_, _ = w.Write(buffer.Bytes())
		return
	}
	var chat map[string]any
	if json.Unmarshal(buffer.Bytes(), &chat) != nil {
		jsonError(w, 502, "服务方返回无效生成结果")
		return
	}
	result, err := chatResultToResponses(chat, publicModel)
	if r.URL.Path == "/v1/messages" {
		result, err = chatResultToMessages(chat, publicModel)
	}
	if err != nil {
		jsonError(w, 502, "服务方返回无效生成结果")
		return
	}
	jsonResult(w, 200, result)
}

type protocolBuffer struct {
	bytes.Buffer
	header   http.Header
	status   int
	overflow bool
}

func (b *protocolBuffer) Header() http.Header { return b.header }
func (b *protocolBuffer) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}
func (b *protocolBuffer) Write(raw []byte) (int, error) {
	b.WriteHeader(200)
	if b.Len()+len(raw) > maxBody {
		b.overflow = true
		return 0, errors.New("response too large")
	}
	return b.Buffer.Write(raw)
}

func responsesInputToChat(input map[string]any) (map[string]any, error) {
	if err := protocolFields(input, "model", "stream", "input", "instructions", "max_output_tokens", "temperature", "top_p", "reasoning", "text", "tools", "tool_choice", "parallel_tool_calls", "store"); err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, key := range []string{"model", "stream", "temperature", "top_p", "parallel_tool_calls"} {
		if v, ok := input[key]; ok {
			out[key] = v
		}
	}
	// Stateful Responses features cannot be implemented by dropping them.
	for _, key := range []string{"previous_response_id", "conversation", "background", "include", "truncation"} {
		if v := input[key]; v != nil && v != false {
			return nil, errors.New("此来源无法转换 Responses 参数：" + key)
		}
	}
	if input["store"] == true {
		return nil, errors.New("此来源不提供 Responses 云端保存")
	}
	if v, ok := input["max_output_tokens"]; ok {
		out["max_completion_tokens"] = v
	}
	if reasoning, ok := input["reasoning"].(map[string]any); ok {
		if err := protocolFields(reasoning, "effort"); err != nil {
			return nil, err
		}
		if v, ok := reasoning["effort"]; ok {
			out["reasoning_effort"] = v
		}
	}
	if text, ok := input["text"].(map[string]any); ok {
		if err := protocolFields(text, "format"); err != nil {
			return nil, err
		}
		if format, ok := text["format"].(map[string]any); ok {
			if format["type"] == "json_schema" {
				schema := map[string]any{}
				for k, v := range format {
					if k != "type" {
						schema[k] = v
					}
				}
				out["response_format"] = map[string]any{"type": "json_schema", "json_schema": schema}
			} else {
				out["response_format"] = format
			}
		}
	}
	messages := []any{}
	if v, ok := input["instructions"].(string); ok && v != "" {
		messages = append(messages, map[string]any{"role": "system", "content": v})
	}
	items, ok := input["input"].([]any)
	if text, isText := input["input"].(string); isText {
		items = []any{map[string]any{"role": "user", "content": text}}
		ok = true
	}
	if !ok || len(items) == 0 {
		return nil, errors.New("需要非空 input 文本或数组")
	}
	for _, value := range items {
		item, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("input 项目无效")
		}
		switch item["type"] {
		case nil, "message":
			role, _ := item["role"].(string)
			if role != "user" && role != "assistant" && role != "system" && role != "developer" {
				return nil, errors.New("消息角色无效")
			}
			content, err := responsesContentToChat(item["content"])
			if err != nil {
				return nil, err
			}
			message := map[string]any{"role": role, "content": content}
			if parts, ok := item["content"].([]any); ok {
				annotations := []any{}
				for _, part := range parts {
					if p, ok := part.(map[string]any); ok {
						if rows, ok := p["annotations"].([]any); ok {
							for _, row := range rows {
								a, ok := row.(map[string]any)
								if !ok {
									return nil, errors.New("引用无效")
								}
								if a["type"] == "url_citation" {
									copy := map[string]any{}
									for k, v := range a {
										if k != "type" {
											copy[k] = v
										}
									}
									annotations = append(annotations, map[string]any{"type": "url_citation", "url_citation": copy})
								} else {
									annotations = append(annotations, a)
								}
							}
						}
					}
				}
				if len(annotations) > 0 {
					message["annotations"] = annotations
				}
			}
			messages = append(messages, message)
		case "reasoning":
			thought := ""
			if summary, ok := item["summary"].([]any); ok {
				for _, value := range summary {
					part, ok := value.(map[string]any)
					if !ok || part["type"] != "summary_text" {
						return nil, errors.New("思考摘要无效")
					}
					text, ok := part["text"].(string)
					if !ok {
						return nil, errors.New("思考摘要无效")
					}
					thought += text
				}
			}
			if item["encrypted_content"] != nil {
				return nil, errors.New("此来源不能转换原生 Responses 加密思考，请使用匹配的原生协议")
			}
			message := map[string]any{"role": "assistant", "content": nil}
			if thought != "" {
				message["reasoning_content"] = thought
			}
			if details, ok := item["reasoning_details"].([]any); ok {
				message["reasoning_details"] = details
			}
			messages = append(messages, message)
		case "function_call":
			id, _ := item["call_id"].(string)
			name, _ := item["name"].(string)
			args, _ := item["arguments"].(string)
			if id == "" || name == "" || !json.Valid([]byte(args)) {
				return nil, errors.New("工具调用缺少 ID、名称或有效 JSON 参数")
			}
			call := map[string]any{"id": id, "type": "function", "function": map[string]any{"name": name, "arguments": args}}
			copyToolMetadata(call, item)
			messages = append(messages, map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{call}})
		case "function_call_output":
			id, _ := item["call_id"].(string)
			content, err := responsesContentToChat(item["output"])
			if id == "" || err != nil {
				return nil, errors.New("工具结果需要 call_id 和有效内容")
			}
			messages = append(messages, map[string]any{"role": "tool", "tool_call_id": id, "content": content})
		default:
			return nil, errors.New("该 Responses 输入类型无法转换到此来源")
		}
	}
	out["messages"] = messages
	if tools, ok := input["tools"].([]any); ok {
		converted := []any{}
		for _, value := range tools {
			tool, ok := value.(map[string]any)
			if !ok || tool["type"] != "function" {
				return nil, errors.New("此来源只接受客户端 function 工具")
			}
			f := map[string]any{}
			for k, v := range tool {
				if k != "type" {
					f[k] = v
				}
			}
			converted = append(converted, map[string]any{"type": "function", "function": f})
		}
		out["tools"] = converted
	}
	if choice, ok := input["tool_choice"]; ok {
		if c, ok := choice.(map[string]any); ok {
			if c["type"] != "function" {
				return nil, errors.New("tool_choice 类型无法转换")
			}
			choice = map[string]any{"type": "function", "function": map[string]any{"name": c["name"]}}
		}
		out["tool_choice"] = choice
	}
	return out, nil
}

func responsesContentToChat(content any) (any, error) {
	if text, ok := content.(string); ok {
		return text, nil
	}
	parts, ok := content.([]any)
	if !ok {
		return nil, errors.New("内容需要文本或数组")
	}
	out := []any{}
	for _, value := range parts {
		p, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("内容项目无效")
		}
		switch p["type"] {
		case "input_text", "output_text":
			out = append(out, map[string]any{"type": "text", "text": p["text"]})
		case "refusal":
			out = append(out, map[string]any{"type": "text", "text": p["refusal"]})
		case "input_image":
			if p["file_id"] != nil {
				return nil, errors.New("此来源不能读取 Responses 云端文件")
			}
			image := map[string]any{"url": p["image_url"]}
			if detail, ok := p["detail"]; ok {
				image["detail"] = detail
			}
			out = append(out, map[string]any{"type": "image_url", "image_url": image})
		default:
			return nil, errors.New("此来源无法转换该内容类型")
		}
	}
	return out, nil
}

func chatResultToResponses(chat map[string]any, model string) (map[string]any, error) {
	choices, ok := chat["choices"].([]any)
	if !ok || len(choices) != 1 {
		return nil, errors.New("invalid choices")
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		return nil, errors.New("invalid choice")
	}
	message, ok := choice["message"].(map[string]any)
	if !ok || choice["finish_reason"] == nil {
		return nil, errors.New("unfinished response")
	}
	if message["media"] != nil || message["audio"] != nil {
		return nil, errors.New("audio representation unavailable; use audio/speech")
	}
	status := "completed"
	var incomplete any
	if choice["finish_reason"] == "length" || choice["finish_reason"] == "content_filter" {
		status = "incomplete"
		reason := "max_output_tokens"
		if choice["finish_reason"] == "content_filter" {
			reason = "content_filter"
		}
		incomplete = map[string]any{"reason": reason}
	} else if choice["finish_reason"] != "stop" && choice["finish_reason"] != "tool_calls" {
		return nil, errors.New("unknown finish")
	}
	output := []any{}
	thought, _ := message["reasoning_content"].(string)
	details, _ := message["reasoning_details"].([]any)
	if thought != "" || len(details) > 0 {
		summary := []any{}
		if thought != "" {
			summary = append(summary, map[string]any{"type": "summary_text", "text": thought})
		}
		item := map[string]any{"id": "rs_" + newKey(""), "type": "reasoning", "summary": summary}
		if len(details) > 0 {
			item["reasoning_details"] = details
		}
		output = append(output, item)
	}
	parts := []any{}
	if message["content"] != nil {
		if _, ok := message["content"].(string); !ok {
			return nil, errors.New("unsupported generated content")
		}
	}
	if text, _ := message["content"].(string); text != "" {
		annotations, err := responseAnnotations(message["annotations"])
		if err != nil {
			return nil, err
		}
		parts = append(parts, map[string]any{"type": "output_text", "text": text, "annotations": annotations})
	}
	if refusal, _ := message["refusal"].(string); refusal != "" {
		parts = append(parts, map[string]any{"type": "refusal", "refusal": refusal})
	}
	if len(parts) > 0 {
		output = append(output, map[string]any{"id": "msg_" + newKey(""), "type": "message", "role": "assistant", "status": status, "content": parts})
	}
	if calls, ok := message["tool_calls"].([]any); ok {
		for _, value := range calls {
			call, ok := value.(map[string]any)
			if !ok {
				return nil, errors.New("invalid call")
			}
			f, ok := call["function"].(map[string]any)
			if !ok {
				return nil, errors.New("invalid function")
			}
			args, _ := f["arguments"].(string)
			if (status == "completed" && !json.Valid([]byte(args))) || f["name"] == nil || call["id"] == nil {
				return nil, errors.New("invalid tool arguments")
			}
			item := map[string]any{"id": "fc_" + newKey(""), "type": "function_call", "call_id": call["id"], "name": f["name"], "arguments": args, "status": status}
			copyToolMetadata(item, call)
			output = append(output, item)
		}
	}
	out := map[string]any{"id": "resp_" + newKey(""), "object": "response", "created_at": time.Now().Unix(), "model": model, "status": status, "output": output, "error": nil, "incomplete_details": incomplete, "store": false}
	if u, ok := chat["usage"].(map[string]any); ok {
		usage := map[string]any{"input_tokens": u["prompt_tokens"], "output_tokens": u["completion_tokens"], "total_tokens": u["total_tokens"]}
		if v, ok := u["prompt_tokens_details"]; ok {
			usage["input_tokens_details"] = v
		}
		if v, ok := u["completion_tokens_details"]; ok {
			usage["output_tokens_details"] = v
		}
		out["usage"] = usage
	}
	return out, nil
}

// Provider signatures are opaque extensions. Clients returning a tool item
// must retain them just as Chat clients retain thought_signature.
func copyToolMetadata(to, from map[string]any) {
	if signature, ok := from["thought_signature"].(string); ok && signature != "" {
		to["thought_signature"] = signature
	}
}
