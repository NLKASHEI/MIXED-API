package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

func (s *Server) serveCodexHTTP(w http.ResponseWriter, r *http.Request, b Backend, publicModel, model string, payload map[string]json.RawMessage) {
	if !isBuiltinURL(b) {
		jsonError(w, 400, "Codex 内置核心必须连接官方端点")
		return
	}
	if r.URL.Path != "/v1/chat/completions" && r.URL.Path != "/v1/responses" {
		jsonError(w, 501, "Codex 支持 chat/completions 与 responses")
		return
	}
	c, err := s.credential("codex")
	if err != nil {
		jsonError(w, 401, err.Error())
		return
	}
	body := payloadObject(payload)
	body["model"] = model
	stream, _ := body["stream"].(bool)
	chat := r.URL.Path == "/v1/chat/completions"
	if chat {
		body, err = chatToResponses(body)
		if err != nil {
			jsonError(w, 400, err.Error())
			return
		}
	}
	body["stream"] = true
	body["store"] = false
	if _, ok := body["instructions"]; !ok {
		body["instructions"] = ""
	}
	resp, err := providerJSON(s.client, r, builtinBase("codex")+"/responses", body, map[string]string{"Authorization": "Bearer " + c.AccessToken, "ChatGPT-Account-ID": c.AccountID, "User-Agent": "MIXED-API/" + BuildVersion, "Accept": "text/event-stream"})
	if err != nil {
		jsonError(w, 502, "无法连接 Codex 服务")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		providerFailure(w, resp)
		return
	}
	w.Header().Set("X-Mixed-Backend", b.ID)
	if stream && chat {
		var options struct {
			Include bool `json:"include_usage"`
		}
		json.Unmarshal(payload["stream_options"], &options)
		streamResponsesAsChat(w, r, resp.Body, publicModel, options.Include)
		return
	}
	if stream {
		copyProvider(w, resp)
		return
	}
	result, err := collectResponses(r, resp.Body)
	if err != nil {
		jsonError(w, 502, "Codex 流响应未正常完成")
		return
	}
	if chat {
		jsonResult(w, 200, responsesToChat(result, publicModel))
	} else {
		result["model"] = publicModel
		jsonResult(w, 200, result)
	}
}

func collectResponses(r *http.Request, body io.Reader) (map[string]any, error) {
	var result map[string]any
	err := readSSE(r.Context(), body, func(raw []byte) error {
		var e struct {
			Type     string         `json:"type"`
			Response map[string]any `json:"response"`
		}
		if json.Unmarshal(raw, &e) != nil {
			return errors.New("invalid responses event")
		}
		switch e.Type {
		case "response.completed", "response.incomplete":
			if e.Response == nil {
				return errors.New("empty response")
			}
			result = e.Response
			return io.EOF
		case "response.failed", "error":
			return errors.New("generation failed")
		}
		return nil
	})
	if err != io.EOF || result == nil {
		return nil, errors.New("incomplete responses stream")
	}
	return result, nil
}
