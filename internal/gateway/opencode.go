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

func (s *Server) openCodeModelRoute(ctx context.Context, model string) (openCodeTarget, error) {
	if target, ok := openCodePinned[model]; ok {
		return target, nil
	}
	s.protocolMu.Lock()
	defer s.protocolMu.Unlock()
	if time.Since(s.protocolsAt) < 5*time.Minute {
		if protocol, ok := s.opencodeProtocols[model]; ok {
			return protocol, nil
		}
		return openCodeTarget{}, errors.New("目录未声明此模型的可用协议")
	}
	probe, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(probe, "GET", "https://models.opencode.ai/api.json", nil)
	req.Header.Set("User-Agent", "MIXED-API/"+BuildVersion)
	resp, err := s.client.Do(req)
	if err != nil {
		return openCodeTarget{}, errors.New("无法读取 OpenCode 模型协议目录")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return openCodeTarget{}, errors.New("OpenCode 模型协议目录暂不可用")
	}
	var providers map[string]struct {
		NPM    string `json:"npm"`
		API    string `json:"api"`
		Models map[string]struct {
			ID       string `json:"id"`
			Provider *struct {
				NPM string `json:"npm"`
				API string `json:"api"`
			} `json:"provider"`
		} `json:"models"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&providers) != nil {
		return openCodeTarget{}, errors.New("OpenCode 协议目录格式无效")
	}
	protocols := map[string]openCodeTarget{}
	for id, p := range providers {
		if id != "opencode" {
			continue
		}
		for name, m := range p.Models {
			if m.ID != "" {
				name = m.ID
			}
			sdk, api := p.NPM, p.API
			if m.Provider != nil {
				if m.Provider.NPM != "" {
					sdk = m.Provider.NPM
				}
				if m.Provider.API != "" {
					api = m.Provider.API
				}
			}
			if target, err := targetForSDK(sdk, api); err == nil {
				protocols[name] = target
			}

		}
	}
	s.opencodeProtocols, s.protocolsAt = protocols, time.Now()
	if protocol, ok := protocols[model]; ok {
		return protocol, nil
	}
	return openCodeTarget{}, errors.New("目录未声明此模型的可用协议")
}

func (s *Server) serveOpenCode(w http.ResponseWriter, r *http.Request, b Backend, publicModel, model string, payload map[string]json.RawMessage) {
	if !isBuiltinURL(b) {
		jsonError(w, 400, "OpenCode 内置核心必须连接官方端点")
		return
	}
	target, err := s.openCodeModelRoute(r.Context(), model)
	if err != nil {
		jsonError(w, 503, err.Error())
		return
	}
	protocol := target.Protocol
	requested := strings.TrimPrefix(r.URL.Path, "/v1/")
	if (requested == "responses" || requested == "messages") && requested != protocol {
		s.serveProtocolBridge(w, r, b, publicModel, model, payload)
		return
	}
	headers := map[string]string{"User-Agent": "MIXED-API/" + BuildVersion}
	if b.ProviderKey != "" {
		headers["Authorization"] = "Bearer " + b.ProviderKey
	}
	if _, ok := openCodePinned[model]; ok {
		w.Header().Set("X-Mixed-Protocol-Catalog", "models.dev/f4088b386739")
	}
	if protocol == "gemini" {
		s.serveGeminiHTTP(w, r, b, publicModel, model, payload, target.Base+"/models/", headers)
		return
	}
	body := payloadObject(payload)
	body["model"] = model
	converted := requested == "chat/completions" && (protocol == "responses" || protocol == "messages")
	if converted {
		if protocol == "responses" {
			body, err = chatToResponses(body)
		} else {
			body, err = chatToAnthropic(body)
		}
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
	if protocol == "messages" {
		headers["Anthropic-Version"] = "2023-06-01"
	}
	if b.ProviderKey != "" {
		headers["Authorization"] = "Bearer " + b.ProviderKey
	}
	resp, err := providerJSON(s.client, r, target.Base+"/"+protocol, body, headers)
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
		if protocol == "responses" {
			streamResponsesAsChat(w, r, resp.Body, publicModel, options.Include)
		} else {
			streamAnthropicAsChat(w, r, resp.Body, publicModel, options.Include)
		}
		return
	}
	if converted {
		if protocol == "messages" {
			var result anthropicMessage
			if json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&result) != nil || result.Type != "message" || result.Stop == "" {
				jsonError(w, 502, "OpenCode 返回无效生成结果")
				return
			}
			jsonResult(w, 200, anthropicToChat(result, publicModel))
			return
		}
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
