package gateway

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type geminiPart struct {
	Text             string `json:"text,omitempty"`
	Thought          bool   `json:"thought,omitempty"`
	ThoughtSignature string `json:"thoughtSignature,omitempty"`
	InlineData       *struct {
		MIME string `json:"mimeType"`
		Data string `json:"data"`
	} `json:"inlineData,omitempty"`
	Call *struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"functionCall,omitempty"`
	Response *struct {
		Name     string `json:"name"`
		Response any    `json:"response"`
	} `json:"functionResponse,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiResult struct {
	Candidates []struct {
		Index   int           `json:"index"`
		Content geminiContent `json:"content"`
		Finish  string        `json:"finishReason"`
	} `json:"candidates"`
	Usage struct {
		Input    int `json:"promptTokenCount"`
		Output   int `json:"candidatesTokenCount"`
		Total    int `json:"totalTokenCount"`
		Thoughts int `json:"thoughtsTokenCount"`
		Cached   int `json:"cachedContentTokenCount"`
	} `json:"usageMetadata"`
	Feedback json.RawMessage `json:"promptFeedback"`
	Error    json.RawMessage `json:"error"`
}

func geminiParts(raw json.RawMessage) ([]geminiPart, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []geminiPart{{Text: text}}, nil
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var items []struct {
		Type  string `json:"type"`
		Text  string `json:"text"`
		Image struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	if json.Unmarshal(raw, &items) != nil {
		return nil, errors.New("消息内容必须是文本或内容数组")
	}
	parts := make([]geminiPart, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case "text":
			parts = append(parts, geminiPart{Text: item.Text})
		case "image_url":
			meta, data, ok := strings.Cut(item.Image.URL, ",")
			if !ok || !strings.HasPrefix(meta, "data:image/") || !strings.HasSuffix(meta, ";base64") {
				return nil, errors.New("图片请使用 base64 data URL；不会代替用户下载任意远程地址")
			}
			p := geminiPart{}
			p.InlineData = &struct {
				MIME string `json:"mimeType"`
				Data string `json:"data"`
			}{strings.TrimSuffix(strings.TrimPrefix(meta, "data:"), ";base64"), data}
			parts = append(parts, p)
		default:
			return nil, errors.New("该消息内容类型尚不支持")
		}
	}
	return parts, nil
}

func expressPayload(payload map[string]json.RawMessage, model string) (map[string]any, error) {
	var messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		ToolID  string          `json:"tool_call_id"`
		Calls   []struct {
			ID       string `json:"id"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
			Signature string `json:"thought_signature"`
		} `json:"tool_calls"`
	}
	if json.Unmarshal(payload["messages"], &messages) != nil || len(messages) == 0 {
		return nil, errors.New("需要非空 messages 数组")
	}
	contents := []geminiContent{}
	system := []geminiPart{}
	names := map[string]string{}
	for _, m := range messages {
		parts, err := geminiParts(m.Content)
		if err != nil {
			return nil, err
		}
		switch m.Role {
		case "system", "developer":
			system = append(system, parts...)
			continue
		case "assistant":
			for _, call := range m.Calls {
				if !json.Valid([]byte(call.Function.Arguments)) {
					return nil, errors.New("工具调用参数必须是 JSON")
				}
				names[call.ID] = call.Function.Name
				p := geminiPart{ThoughtSignature: call.Signature}
				p.Call = &struct {
					Name string          `json:"name"`
					Args json.RawMessage `json:"args"`
				}{call.Function.Name, json.RawMessage(call.Function.Arguments)}
				parts = append(parts, p)
			}
		case "tool":
			name := names[m.ToolID]
			if name == "" {
				return nil, errors.New("工具结果缺少对应调用")
			}
			var result any
			var text string
			if json.Unmarshal(m.Content, &text) == nil {
				if json.Unmarshal([]byte(text), &result) != nil {
					result = map[string]any{"result": text}
				}
			} else if json.Unmarshal(m.Content, &result) != nil {
				return nil, errors.New("工具结果无效")
			}
			if _, ok := result.(map[string]any); !ok {
				result = map[string]any{"result": result}
			}
			p := geminiPart{}
			p.Response = &struct {
				Name     string `json:"name"`
				Response any    `json:"response"`
			}{name, result}
			parts = []geminiPart{p}
		case "user":
		default:
			return nil, errors.New("消息角色不支持")
		}
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		if len(parts) == 0 {
			continue
		}
		// Gemini represents consecutive messages of the same role as one turn.
		if n := len(contents); n > 0 && contents[n-1].Role == role {
			contents[n-1].Parts = append(contents[n-1].Parts, parts...)
		} else {
			contents = append(contents, geminiContent{role, parts})
		}
	}
	if len(contents) == 0 {
		return nil, errors.New("需要至少一条用户或助手消息")
	}
	out := map[string]any{"contents": contents}
	if len(system) > 0 {
		out["systemInstruction"] = geminiContent{Parts: system}
	}
	config := map[string]any{}
	for input, output := range map[string]string{"temperature": "temperature", "top_p": "topP", "top_k": "topK", "max_tokens": "maxOutputTokens", "max_completion_tokens": "maxOutputTokens", "n": "candidateCount", "seed": "seed"} {
		if v, ok := payload[input]; ok {
			config[output] = v
		}
	}
	if raw, ok := payload["stop"]; ok {
		var text string
		if json.Unmarshal(raw, &text) == nil {
			config["stopSequences"] = []string{text}
		} else {
			config["stopSequences"] = raw
		}
	}
	if raw, ok := payload["response_format"]; ok {
		var f struct {
			Type   string `json:"type"`
			Schema struct {
				Schema json.RawMessage `json:"schema"`
			} `json:"json_schema"`
		}
		if json.Unmarshal(raw, &f) != nil {
			return nil, errors.New("response_format 无效")
		}
		if f.Type == "json_object" || f.Type == "json_schema" {
			config["responseMimeType"] = "application/json"
			if len(f.Schema.Schema) > 0 {
				config["responseJsonSchema"] = f.Schema.Schema
			}
		}
	}
	if raw, ok := payload["reasoning_effort"]; ok {
		var effort string
		json.Unmarshal(raw, &effort)
		switch effort {
		case "low", "medium", "high", "minimal":
			config["thinkingConfig"] = map[string]any{"thinkingLevel": strings.ToUpper(effort), "includeThoughts": true}
		default:
			return nil, errors.New("reasoning_effort 不支持")
		}
	}
	if len(config) > 0 {
		out["generationConfig"] = config
	}
	var tools []struct {
		Type     string          `json:"type"`
		Function json.RawMessage `json:"function"`
	}
	if raw, ok := payload["tools"]; ok {
		if json.Unmarshal(raw, &tools) != nil {
			return nil, errors.New("tools 无效")
		}
	}
	declarations := []json.RawMessage{}
	for _, tool := range tools {
		if tool.Type != "function" {
			return nil, errors.New("仅支持 function 工具")
		}
		declarations = append(declarations, tool.Function)
	}
	allTools := []any{}
	if len(declarations) > 0 {
		allTools = append(allTools, map[string]any{"functionDeclarations": declarations})
	}
	var choice string
	json.Unmarshal(payload["tool_choice"], &choice)
	if strings.HasSuffix(model, "-search") && choice != "none" {
		allTools = append(allTools, map[string]any{"googleSearch": map[string]any{}})
	}
	if len(allTools) > 0 {
		out["tools"] = allTools
	}
	if choice != "" {
		mode := map[string]string{"none": "NONE", "auto": "AUTO", "required": "ANY"}[choice]
		if mode == "" {
			return nil, errors.New("tool_choice 不支持")
		}
		out["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{"mode": mode}}
	}
	return out, nil
}

func (s *Server) serveExpress(w http.ResponseWriter, r *http.Request, b Backend, publicModel, model string, payload map[string]json.RawMessage) {
	if r.URL.Path != "/v1/chat/completions" {
		jsonError(w, 501, "快速V内置核心当前提供 chat/completions")
		return
	}
	if !isBuiltinURL(b) {
		jsonError(w, 400, "内置快速V必须连接官方端点")
		return
	}
	if strings.TrimSpace(b.ProviderKey) == "" {
		jsonError(w, 401, "请先填写本人的 Express API Key")
		return
	}
	body, err := expressPayload(payload, model)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	var stream bool
	json.Unmarshal(payload["stream"], &stream)
	baseModel := strings.TrimSuffix(model, "-search")
	if strings.ContainsAny(baseModel, "/\\?#%") || baseModel == "" {
		jsonError(w, 400, "快速V模型 ID 无效")
		return
	}
	action := "generateContent"
	if stream {
		action = "streamGenerateContent?alt=sse"
	}
	endpoint := builtinBase("vertex") + "/v1/publishers/google/models/" + url.PathEscape(baseModel) + ":" + action
	resp, err := providerJSON(s.client, r, endpoint, body, map[string]string{"x-goog-api-key": b.ProviderKey})
	if err != nil {
		jsonError(w, 502, "无法连接 Google Express 服务")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		providerFailure(w, resp)
		return
	}
	w.Header().Set("X-Mixed-Backend", b.ID)
	id := "chatcmpl-" + newKey("")
	created := time.Now().Unix()
	if !stream {
		var result geminiResult
		if json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&result) != nil || len(result.Candidates) == 0 {
			jsonError(w, 502, "Google 未返回有效生成结果")
			return
		}
		choices := []any{}
		for _, candidate := range result.Candidates {
			message := geminiMessage(candidate.Content.Parts, false, 0)
			choices = append(choices, map[string]any{"index": candidate.Index, "message": message, "finish_reason": geminiFinish(candidate.Finish, len(candidate.Content.Parts) > 0 && hasGeminiCalls(candidate.Content.Parts))})
		}
		jsonResult(w, 200, map[string]any{"id": id, "object": "chat.completion", "created": created, "model": publicModel, "choices": choices, "usage": geminiUsage(result)})
		return
	}
	s.streamExpress(w, r, resp.Body, id, publicModel, created, payload)
}

func hasGeminiCalls(parts []geminiPart) bool {
	for _, p := range parts {
		if p.Call != nil {
			return true
		}
	}
	return false
}
func geminiFinish(reason string, tools bool) any {
	if reason == "" {
		return nil
	}
	if tools {
		return "tool_calls"
	}
	switch reason {
	case "MAX_TOKENS":
		return "length"
	case "STOP":
		return "stop"
	default:
		return "content_filter"
	}
}
func geminiUsage(r geminiResult) map[string]any {
	return map[string]any{"prompt_tokens": r.Usage.Input, "completion_tokens": r.Usage.Output + r.Usage.Thoughts, "total_tokens": r.Usage.Total, "prompt_tokens_details": map[string]int{"cached_tokens": r.Usage.Cached}, "completion_tokens_details": map[string]int{"reasoning_tokens": r.Usage.Thoughts}}
}
func geminiMessage(parts []geminiPart, stream bool, offset int) map[string]any {
	message := map[string]any{"role": "assistant"}
	var text, thought strings.Builder
	calls := []any{}
	for _, p := range parts {
		if p.Thought {
			thought.WriteString(p.Text)
		} else {
			text.WriteString(p.Text)
		}
		if p.Call != nil {
			call := map[string]any{"id": "call-" + newKey(""), "type": "function", "function": map[string]any{"name": p.Call.Name, "arguments": string(p.Call.Args)}}
			if stream {
				call["index"] = offset + len(calls)
			}
			if p.ThoughtSignature != "" {
				call["thought_signature"] = p.ThoughtSignature
			}
			calls = append(calls, call)
		}
	}
	message["content"] = text.String()
	if thought.Len() > 0 {
		message["reasoning_content"] = thought.String()
	}
	if len(calls) > 0 {
		message["tool_calls"] = calls
	}
	return message
}

func (s *Server) streamExpress(w http.ResponseWriter, r *http.Request, body io.Reader, id, model string, created int64, payload map[string]json.RawMessage) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	send := func(v any) bool {
		b, _ := json.Marshal(v)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return false
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return true
	}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), maxBody)
	var event strings.Builder
	toolOffsets := map[int]int{}
	toolSeen := map[int]bool{}
	finished := false
	var last geminiResult
	process := func() bool {
		if event.Len() == 0 {
			return true
		}
		var result geminiResult
		data := event.String()
		event.Reset()
		if json.Unmarshal([]byte(data), &result) != nil || len(result.Error) > 0 {
			send(map[string]any{"error": map[string]string{"message": "Google 流响应无效或中断"}})
			return false
		}
		if result.Usage.Total > 0 {
			last = result
		}
		choices := []any{}
		for _, candidate := range result.Candidates {
			message := geminiMessage(candidate.Content.Parts, true, toolOffsets[candidate.Index])
			for _, p := range candidate.Content.Parts {
				if p.Call != nil {
					toolOffsets[candidate.Index]++
					toolSeen[candidate.Index] = true
				}
			}
			reason := geminiFinish(candidate.Finish, toolSeen[candidate.Index])
			if reason != nil {
				finished = true
			}
			choices = append(choices, map[string]any{"index": candidate.Index, "delta": message, "finish_reason": reason})
		}
		if len(choices) > 0 {
			return send(map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": choices})
		}
		return true
	}
	for scanner.Scan() {
		if r.Context().Err() != nil {
			return
		}
		line := scanner.Text()
		if line == "" {
			if !process() {
				return
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if event.Len() > 0 {
				event.WriteByte('\n')
			}
			event.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if !process() {
		return
	}
	if scanner.Err() != nil || !finished {
		send(map[string]any{"error": map[string]string{"message": "Google 流响应在完成前中断"}})
		return
	}
	var options struct {
		Include bool `json:"include_usage"`
	}
	json.Unmarshal(payload["stream_options"], &options)
	if options.Include {
		if !send(map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{}, "usage": geminiUsage(last)}) {
			return
		}
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
