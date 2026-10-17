package gateway

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

//go:embed builtin-models.json
var builtinModelsJSON []byte

func (s *Server) integratedModels(ctx context.Context, b Backend) ([]string, error) {
	if !isBuiltinURL(b) {
		return nil, errors.New("内置来源端点无效")
	}
	if b.ID == "aps" || b.ID == "aistudio" {
		return s.googleWebModels(ctx, b.ID)
	}
	if b.ID == "vertex" || b.ID == "workbuddy" {
		if b.ID == "vertex" && b.ProviderKey == "" {
			return nil, errors.New("请填写 Express API Key")
		}
		if b.ID == "workbuddy" {
			if _, err := s.freshCredential(ctx, b.ID, ""); err != nil {
				return nil, err
			}
		}
		var catalogs map[string][]string
		_ = json.Unmarshal(builtinModelsJSON, &catalogs)
		return catalogs[b.ID], nil
	}
	if b.ID == "opencode" {
		r, err := http.NewRequestWithContext(ctx, "GET", builtinBase(b.ID)+"/models", nil)
		if err != nil {
			return nil, err
		}
		if b.ProviderKey != "" {
			r.Header.Set("Authorization", "Bearer "+b.ProviderKey)
		}
		r.Header.Set("User-Agent", "MIXED-API/"+BuildVersion)
		resp, err := s.client.Do(r)
		if err != nil {
			return nil, errors.New("无法连接 OpenCode 模型目录")
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, errors.New("OpenCode 拒绝模型目录请求，请检查授权或额度")
		}
		var list struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 5<<20)).Decode(&list) != nil {
			return nil, errors.New("OpenCode 模型目录格式无效")
		}
		ids := []string{}
		for _, m := range list.Data {
			if m.ID != "" {
				ids = append(ids, m.ID)
			}
		}
		return ids, nil
	}
	if b.ID == "codex" {
		r, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1/v1/models", nil)
		resp, err := s.accountJSON(r, b.ID, "/models?client_version=0.163.0", nil)
		if err != nil {
			return nil, errors.New("无法读取 Codex 模型目录")
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, errors.New("Codex 拒绝模型目录请求，请检查授权")
		}
		var data struct {
			Models []struct {
				Slug string `json:"slug"`
			} `json:"models"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 5<<20)).Decode(&data) != nil {
			return nil, errors.New("Codex 模型目录格式无效")
		}
		ids := []string{}
		for _, m := range data.Models {
			if m.Slug != "" {
				ids = append(ids, m.Slug)
			}
		}
		return ids, nil
	}
	return nil, errors.New("该来源的内置核心尚未完成")
}

func (s *Server) prepareIntegrated(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cfg
	next.Backends = append([]Backend(nil), next.Backends...)
	found := false
	for i := range next.Backends {
		b := &next.Backends[i]
		if b.ID != id {
			continue
		}
		found = true
		if b.Kind != "builtin" {
			b.Enabled = false
		}
		b.Kind = "builtin"
		b.URL = builtinBase(id)
		b.Auth = "none"
		b.Key = ""
		b.Command = ""
		b.Workdir = ""
	}
	if !found {
		return errors.New("来源不存在")
	}
	if err := Save(s.path, next); err != nil {
		return errors.New("无法保存来源配置")
	}
	s.cfg = next
	s.status["setup:"+id] = "installed: 内置核心就绪"
	return nil
}

// Provider protocol implementations live behind the same authenticated HTTP
// seam as external services. They never start an upstream application.
func (s *Server) serveIntegrated(w http.ResponseWriter, r *http.Request, b Backend, publicModel, model string, payload map[string]json.RawMessage) {
	if b.ID == "aps" || b.ID == "aistudio" {
		s.serveGoogleWeb(w, r, b, publicModel, model, payload)
		return
	}
	if b.ID == "vertex" {
		s.serveExpress(w, r, b, publicModel, model, payload)
		return
	}
	if b.ID == "opencode" {
		s.serveOpenCode(w, r, b, publicModel, model, payload)
		return
	}
	if b.ID == "workbuddy" {
		s.serveWorkBuddy(w, r, b, publicModel, model, payload)
		return
	}
	if b.ID == "codex" {
		s.serveCodexHTTP(w, r, b, publicModel, model, payload)
		return
	}
	jsonError(w, http.StatusNotImplemented, "该来源的内置核心尚未完成，不会下载或启动外部上游")
}

func providerJSON(client *http.Client, r *http.Request, endpoint string, body any, headers map[string]string) (*http.Response, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return client.Do(req)
}

// Do not echo arbitrary provider error bodies: some echo request headers or
// credential-bearing URLs. Preserve the status and useful quota metadata.
func providerFailure(w http.ResponseWriter, resp *http.Response) {
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if v := resp.Header.Get("Retry-After"); v != "" {
		w.Header().Set("Retry-After", v)
	}
	code := resp.StatusCode
	if code >= 300 && code < 400 {
		code = http.StatusBadGateway
	}
	message := "服务方请求失败，请检查账号权限、模型和网络"
	switch resp.StatusCode {
	case 401, 403:
		message = "服务方拒绝授权，请检查本人的凭证和使用权限"
	case 429:
		message = "服务方额度不足或请求受限，请稍后重试"
	case 404:
		message = "服务方未提供该模型或接口"
	}
	jsonError(w, code, message)
}

func knownBuiltin(id string) bool {
	switch id {
	case "vertex", "opencode", "codex", "workbuddy", "aps", "aistudio":
		return true
	}
	return false
}

func builtinBase(id string) string {
	switch id {
	case "vertex":
		return "https://aiplatform.googleapis.com"
	case "opencode":
		return "https://opencode.ai/inference/v1"
	case "codex":
		return "https://chatgpt.com/backend-api/codex"
	case "workbuddy":
		return "https://copilot.tencent.com/v2"
	case "aps":
		return "https://cloudconsole-pa.clients6.google.com"
	case "aistudio":
		return "https://alkalimakersuite-pa.clients6.google.com"
	}
	return ""
}

func isBuiltinURL(b Backend) bool {
	return b.Kind == "builtin" && strings.TrimSuffix(b.URL, "/") == builtinBase(b.ID) && builtinBase(b.ID) != ""
}
