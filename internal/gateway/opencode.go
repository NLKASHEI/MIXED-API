package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

func (s *Server) openCodeProtocol(ctx context.Context, model string) (string, error) {
	s.protocolMu.Lock()
	defer s.protocolMu.Unlock()
	if time.Since(s.protocolsAt) < 5*time.Minute {
		if protocol, ok := s.opencodeProtocols[model]; ok {
			return protocol, nil
		}
	}
	probe, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(probe, "GET", "https://models.opencode.ai/api.json", nil)
	resp, err := s.client.Do(req)
	if err != nil {
		return "", errors.New("无法读取 OpenCode 模型协议目录")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", errors.New("OpenCode 模型协议目录暂不可用")
	}
	var providers map[string]struct {
		NPM    string `json:"npm"`
		Models map[string]struct {
			ID       string `json:"id"`
			Provider *struct {
				NPM string `json:"npm"`
			} `json:"provider"`
		} `json:"models"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&providers) != nil {
		return "", errors.New("OpenCode 协议目录格式无效")
	}
	protocols := map[string]string{}
	for id, p := range providers {
		if id != "opencode" {
			continue
		}
		for name, m := range p.Models {
			if m.ID != "" {
				name = m.ID
			}
			sdk := p.NPM
			if m.Provider != nil && m.Provider.NPM != "" {
				sdk = m.Provider.NPM
			}
			switch sdk {
			case "@ai-sdk/openai":
				protocols[name] = "responses"
			case "@ai-sdk/openai-compatible":
				protocols[name] = "chat/completions"
			case "@ai-sdk/anthropic":
				protocols[name] = "messages"
			}
		}
	}
	s.opencodeProtocols, s.protocolsAt = protocols, time.Now()
	if protocol, ok := protocols[model]; ok {
		return protocol, nil
	}
	return "", errors.New("目录未声明此模型的可用协议")
}

func (s *Server) serveOpenCode(w http.ResponseWriter, r *http.Request, b Backend, publicModel, model string, payload map[string]json.RawMessage) {
	if !isBuiltinURL(b) {
		jsonError(w, 400, "OpenCode 内置核心必须连接官方端点")
		return
	}
	protocol, err := s.openCodeProtocol(r.Context(), model)
	if err != nil {
		jsonError(w, 503, err.Error())
		return
	}
	requested := strings.TrimPrefix(r.URL.Path, "/v1/")
	body := payloadObject(payload)
	body["model"] = model
	converted := requested == "chat/completions" && protocol == "responses"
	if converted {
		body, err = chatToResponses(body)
		if err != nil {
			jsonError(w, 400, err.Error())
			return
		}
		if _, ok := payload["seed"]; ok {
			w.Header().Set("X-Mixed-Ignored-Parameters", "seed")
		}
	} else if requested != protocol {
		jsonError(w, 501, "此模型当前要求 "+protocol+" 协议；该转换尚未完成")
		return
	}
	headers := map[string]string{"User-Agent": "MIXED-API/" + BuildVersion}
	if b.ProviderKey != "" {
		headers["Authorization"] = "Bearer " + b.ProviderKey
	}
	resp, err := providerJSON(s.client, r, builtinBase("opencode")+"/"+protocol, body, headers)
	if err != nil {
		jsonError(w, 502, "无法连接 OpenCode 服务")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		providerFailure(w, resp)
		return
	}
	w.Header().Set("X-Mixed-Backend", b.ID)
	stream, _ := body["stream"].(bool)
	if converted && stream {
		var options struct {
			Include bool `json:"include_usage"`
		}
		json.Unmarshal(payload["stream_options"], &options)
		streamResponsesAsChat(w, r, resp.Body, publicModel, options.Include)
		return
	}
	if converted {
		var result map[string]any
		if json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&result) != nil || result["status"] == "failed" {
			jsonError(w, 502, "OpenCode 返回无效生成结果")
			return
		}
		jsonResult(w, 200, responsesToChat(result, publicModel))
		return
	}
	copyProvider(w, resp)
}

func copyProvider(w http.ResponseWriter, resp *http.Response) {
	for _, key := range []string{"Content-Type", "Cache-Control", "X-Request-ID"} {
		if value := resp.Header.Get(key); value != "" {
			w.Header().Set(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	buffer := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buffer)
		if n > 0 {
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}
