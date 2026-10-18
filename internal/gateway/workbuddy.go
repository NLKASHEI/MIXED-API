package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

func (s *Server) serveWorkBuddy(w http.ResponseWriter, r *http.Request, b Backend, publicModel, model string, payload map[string]json.RawMessage) {
	if !isBuiltinURL(b) {
		jsonError(w, 400, "WorkBuddy 内置核心必须连接官方端点")
		return
	}
	if r.URL.Path != "/v1/chat/completions" {
		jsonError(w, 501, "WorkBuddy 当前提供 chat/completions")
		return
	}
	body := payloadObject(payload)
	body["model"] = model
	stream, _ := body["stream"].(bool)
	body["stream"] = true
	resp, err := s.accountJSON(r, workbuddyAccountID(b), "/chat/completions", body)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		providerFailure(w, resp)
		return
	}
	w.Header().Set("X-Mixed-Backend", b.ID)
	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		copyProvider(w, resp)
		return
	}
	result, err := collectChatStream(r, resp.Body, publicModel)
	if err != nil {
		jsonError(w, 502, "WorkBuddy 流响应未正常完成")
		return
	}
	jsonResult(w, 200, result)
}

func collectChatStream(r *http.Request, body io.Reader, model string) (map[string]any, error) {
	message := map[string]any{"role": "assistant", "content": ""}
	calls := map[int]map[string]any{}
	var usage any
	var finish any
	size := 0
	done := false
	err := readSSE(r.Context(), body, func(raw []byte) error {
		if string(raw) == "[DONE]" {
			done = true
			return io.EOF
		}
		size += len(raw)
		if size > maxBody {
			return errors.New("response too large")
		}
		var chunk struct {
			Choices []struct {
				Delta  map[string]any `json:"delta"`
				Finish any            `json:"finish_reason"`
			} `json:"choices"`
			Usage any             `json:"usage"`
			Error json.RawMessage `json:"error"`
		}
		decodeErr := json.Unmarshal(raw, &chunk)
		if len(chunk.Error) > 0 {
			recordProviderErrorBody(r, raw)
		}
		if decodeErr != nil || len(chunk.Error) > 0 {
			return errors.New("invalid stream")
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		if len(chunk.Choices) > 1 {
			return errors.New("multiple candidates not supported")
		}
		for _, choice := range chunk.Choices {
			if choice.Finish != nil {
				finish = choice.Finish
			}
			for _, key := range []string{"content", "reasoning_content"} {
				if text, ok := choice.Delta[key].(string); ok {
					prior, _ := message[key].(string)
					message[key] = prior + text
				}
			}
			if list, ok := choice.Delta["tool_calls"].([]any); ok {
				for _, v := range list {
					call, ok := v.(map[string]any)
					if !ok {
						return errors.New("invalid tool call")
					}
					n, _ := call["index"].(float64)
					index := int(n)
					target := calls[index]
					if target == nil {
						target = map[string]any{"type": "function", "function": map[string]any{"name": "", "arguments": ""}}
						calls[index] = target
					}
					if id, ok := call["id"]; ok {
						target["id"] = id
					}
					if f, ok := call["function"].(map[string]any); ok {
						dest := target["function"].(map[string]any)
						for _, key := range []string{"name", "arguments"} {
							if text, ok := f[key].(string); ok {
								dest[key] = dest[key].(string) + text
							}
						}
					}
				}
			}
		}
		return nil
	})
	if !done || err != io.EOF || finish == nil {
		return nil, errors.New("incomplete stream")
	}
	if len(calls) > 0 {
		list := []any{}
		for i := 0; i < len(calls); i++ {
			call := calls[i]
			if call == nil {
				return nil, errors.New("non-contiguous tool calls")
			}
			list = append(list, call)
		}
		message["tool_calls"] = list
	}
	result := map[string]any{"id": "chatcmpl-" + newKey(""), "object": "chat.completion", "model": model, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}}
	if usage != nil {
		result["usage"] = usage
	}
	return result, nil
}
