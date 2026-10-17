package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type geminiPart struct {
	Text             string      `json:"text,omitempty"`
	Thought          bool        `json:"thought,omitempty"`
	ThoughtSignature string      `json:"thoughtSignature,omitempty"`
	InlineData       *geminiBlob `json:"inlineData,omitempty"`
	Call             *struct {
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
	Error    json.RawMessage `json:"error,omitempty"`
}

func geminiParts(raw json.RawMessage) ([]geminiPart, error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return nil, nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if text == "" {
			return nil, nil
		}
		return []geminiPart{{Text: text}}, nil
	}
	var items []struct {
		Type  string `json:"type"`
		Text  string `json:"text"`
		Image struct {
			URL string `json:"url"`
		} `json:"image_url"`
		Audio struct {
			Format string `json:"format"`
			Data   string `json:"data"`
		} `json:"input_audio"`
		File struct {
			Data string `json:"file_data"`
		} `json:"file"`
	}
	if json.Unmarshal(raw, &items) != nil {
		return nil, errors.New("消息内容必须是文本或内容数组")
	}
	parts := make([]geminiPart, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case "text":
			if item.Text != "" {
				parts = append(parts, geminiPart{Text: item.Text})
			}
		case "image_url":
			meta, data, ok := strings.Cut(item.Image.URL, ",")
			if !ok || !strings.HasPrefix(meta, "data:image/") || !strings.HasSuffix(meta, ";base64") {
				return nil, errors.New("图片请使用 base64 data URL；不会代替用户下载任意远程地址")
			}
			p := geminiPart{}
			p.InlineData = &geminiBlob{strings.TrimSuffix(strings.TrimPrefix(meta, "data:"), ";base64"), data}
			parts = append(parts, p)
		case "input_audio":
			mime := map[string]string{"wav": "audio/wav", "mp3": "audio/mpeg"}[item.Audio.Format]
			blob, err := inlineMedia(mime, item.Audio.Data)
			if err != nil {
				return nil, err
			}
			parts = append(parts, geminiPart{InlineData: blob})
		case "file":
			blob, err := mediaDataURL(item.File.Data)
			if err != nil {
				return nil, err
			}
			parts = append(parts, geminiPart{InlineData: blob})
		default:
			return nil, errors.New("该消息内容类型尚不支持")
		}
	}
	return parts, nil
}

func expressPayload(payload map[string]json.RawMessage, model string) (map[string]any, error) {
	var messages []struct {
		Role      string          `json:"role"`
		Content   json.RawMessage `json:"content"`
		Reasoning []struct {
			Type      string `json:"type"`
			Format    string `json:"format"`
			Text      string `json:"text"`
			Signature string `json:"signature"`
		} `json:"reasoning_details"`
		ToolID string `json:"tool_call_id"`
		Calls  []struct {
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
		if len(m.Reasoning) > 0 {
			thoughts := []geminiPart{}
			for _, d := range m.Reasoning {
				if m.Role != "assistant" || d.Type != "reasoning.text" || d.Format != "google-gemini-v1" || d.Signature == "" {
					return nil, errors.New("此来源无法回传其他协议的思考签名")
				}
				thoughts = append(thoughts, geminiPart{Text: d.Text, Thought: true, ThoughtSignature: d.Signature})
			}
			parts = append(thoughts, parts...)
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
	if strings.Contains(model, "-image") || strings.Contains(model, "nano-banana") {
		config["responseModalities"] = []string{"TEXT", "IMAGE"}
	}
	if raw, ok := payload["n"]; ok {
		var n int
		if json.Unmarshal(raw, &n) != nil || n < 1 || n > 8 {
			return nil, errors.New("n 必须是 1–8 的整数")
		}
	}
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
	if err := googleMediaConfig(payload, config); err != nil {
		return nil, err
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
	declarations := []map[string]any{}
	for _, tool := range tools {
		if tool.Type != "function" {
			return nil, errors.New("仅支持 function 工具")
		}
		var function map[string]any
		if json.Unmarshal(tool.Function, &function) != nil || function["name"] == nil {
			return nil, errors.New("function 声明无效")
		}
		declaration := map[string]any{"name": function["name"]}
		for _, field := range []string{"description", "parameters"} {
			if value, ok := function[field]; ok {
				declaration[field] = value
			}
		}
		declarations = append(declarations, declaration)
	}
	allTools := []any{}
	if len(declarations) > 0 {
		allTools = append(allTools, map[string]any{"functionDeclarations": declarations})
	}
	var choice string
	var allowed []string
	if raw, ok := payload["tool_choice"]; ok {
		if json.Unmarshal(raw, &choice) != nil {
			var named struct {
				Type     string `json:"type"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			}
			if json.Unmarshal(raw, &named) != nil || named.Type != "function" || named.Function.Name == "" {
				return nil, errors.New("tool_choice 无效")
			}
			choice, allowed = "required", []string{named.Function.Name}
		}
	}
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
		config := map[string]any{"mode": mode}
		if len(allowed) > 0 {
			config["allowedFunctionNames"] = allowed
		}
		out["toolConfig"] = map[string]any{"functionCallingConfig": config}
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
	s.serveGeminiHTTP(w, r, b, publicModel, model, payload, builtinBase("vertex")+"/v1/publishers/google/models/", map[string]string{"x-goog-api-key": b.ProviderKey})
}

// Shared Gemini wire implementation, also used for OpenCode's Google family.
func (s *Server) serveGeminiHTTP(w http.ResponseWriter, r *http.Request, b Backend, publicModel, model string, payload map[string]json.RawMessage, modelBase string, headers map[string]string) {
	if r.URL.Path != "/v1/chat/completions" {
		jsonError(w, 501, "该模型当前支持 chat/completions")
		return
	}
	body, err := expressPayload(payload, model)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	contract, err := googleTools(payload)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	contract.prepare(body, false)
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
	endpoint := modelBase + url.PathEscape(baseModel) + ":" + action
	resp, err := providerJSON(s.client, r, endpoint, body, headers)
	if err != nil {
		jsonError(w, 502, "无法连接模型服务")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		if b.ID == "opencode" {
			openCodeFailure(w, resp)
		} else {
			providerFailure(w, resp)
		}
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
		if err := contract.check(result); err != nil {
			jsonError(w, 502, "来源违反工具调用约束")
			return
		}
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
	media := []any{}
	details := []any{}
	for _, p := range parts {
		if p.InlineData != nil && strings.HasPrefix(p.InlineData.MIME, "image/") {
			text.WriteString("\n\n![image](data:" + p.InlineData.MIME + ";base64," + p.InlineData.Data + ")")
		}
		if p.InlineData != nil && !strings.HasPrefix(p.InlineData.MIME, "image/") {
			media = append(media, map[string]any{"mime_type": p.InlineData.MIME, "data": p.InlineData.Data})
		}
		if p.Thought {
			thought.WriteString(p.Text)
			if p.ThoughtSignature != "" {
				details = append(details, map[string]any{"type": "reasoning.text", "format": "google-gemini-v1", "text": p.Text, "signature": p.ThoughtSignature})
			}
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
	if len(details) > 0 {
		message["reasoning_details"] = details
	}
	if len(media) > 0 {
		message["media"] = media
	}
	if thought.Len() > 0 {
		message["reasoning_content"] = thought.String()
	}
	if len(calls) > 0 {
		message["tool_calls"] = calls
	}
	return message
}

func (s *Server) streamExpress(w http.ResponseWriter, r *http.Request, body io.Reader, id, model string, created int64, payload map[string]json.RawMessage) {
	contract, err := googleTools(payload)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	out := newSSEOutput(w, model)
	out.id, out.created = id, created
	offsets := map[int]int{}
	tools := map[int]bool{}
	finished := map[int]bool{}
	expected := 1
	if raw, ok := payload["n"]; ok {
		json.Unmarshal(raw, &expected)
	}
	var last geminiResult
	err = readSSE(r.Context(), body, func(data []byte) error {
		var result geminiResult
		if json.Unmarshal(data, &result) != nil || len(result.Error) > 0 {
			return errors.New("invalid Google event")
		}
		if err := contract.check(result); err != nil {
			return err
		}
		if result.Usage.Total > 0 {
			last = result
		}
		choices := []any{}
		for _, candidate := range result.Candidates {
			if candidate.Index < 0 || candidate.Index >= expected {
				return errors.New("unexpected candidate")
			}
			message := geminiMessage(candidate.Content.Parts, true, offsets[candidate.Index])
			for _, p := range candidate.Content.Parts {
				if p.Call != nil {
					offsets[candidate.Index]++
					tools[candidate.Index] = true
				}
			}
			reason := geminiFinish(candidate.Finish, tools[candidate.Index])
			if reason != nil {
				finished[candidate.Index] = true
			}
			choices = append(choices, map[string]any{"index": candidate.Index, "delta": message, "finish_reason": reason})
		}
		if len(choices) > 0 {
			return out.send(map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": choices})
		}
		return nil
	})
	if r.Context().Err() != nil {
		return
	}
	if err != nil || len(finished) != expected {
		_ = out.send(map[string]any{"error": map[string]string{"message": "Google 流响应在完成前中断"}})
		return
	}
	var options struct {
		Include bool `json:"include_usage"`
	}
	json.Unmarshal(payload["stream_options"], &options)
	if options.Include {
		if err = out.chunk(nil, nil, geminiUsage(last)); err != nil {
			return
		}
	}
	out.done()
}
