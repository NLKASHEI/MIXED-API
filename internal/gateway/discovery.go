package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

type catalogModel struct {
	ID       string       `json:"id"`
	Object   string       `json:"object"`
	OwnedBy  string       `json:"owned_by"`
	Original string       `json:"original_id"`
	Access   *modelAccess `json:"access,omitempty"`
}

type backendDiscovery struct {
	Details    *RequestDetails `json:"details,omitempty"`
	ID         string          `json:"id"`
	State      string          `json:"state"`
	Code       string          `json:"code,omitempty"`
	Error      string          `json:"error,omitempty"`
	Next       string          `json:"next"`
	ModelCount int             `json:"model_count"`
	DurationMs int64           `json:"duration_ms"`
	models     []catalogModel
}

// One bounded probe per configured backend. A fixed worker pool avoids an
// unbounded number of subprocesses for custom configurations. Results occupy
// their original index, so response order never depends on network timing.
func (s *Server) discover(ctx context.Context, cfg Config) []backendDiscovery {
	results := make([]backendDiscovery, len(cfg.Backends))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for n := 0; n < min(6, len(cfg.Backends)); n++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				results[i] = s.probeBackend(ctx, cfg.Backends[i])
			}
		}()
	}
	for i := range cfg.Backends {
		jobs <- i
	}
	close(jobs)
	workers.Wait()
	return results
}

func (s *Server) probeBackend(ctx context.Context, b Backend) (result backendDiscovery) {
	detail := &requestDiagnostic{}
	ctx = context.WithValue(ctx, diagnosticKey{}, detail)
	defer func() {
		detail.mu.Lock()
		snapshot := detail.details
		detail.mu.Unlock()
		if snapshot.Target != "" {
			result.Details = &snapshot
		}
	}()
	result = backendDiscovery{ID: b.ID, State: "disabled", Next: "启用来源后检查模型；Windows 可点击一键接入。"}
	if !b.Enabled {
		return result
	}
	begin := time.Now()
	result.State = "error"
	fail := func(code, message, next string) backendDiscovery {
		result.Code = code
		result.Error = message
		result.Next = next
		result.DurationMs = time.Since(begin).Milliseconds()
		return result
	}
	var ids []string
	if b.Kind == "builtin" {
		probe, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		var err error
		ids, err = s.integratedModels(probe, b)
		if err != nil {
			return fail("builtin_auth", err.Error(), "检查该来源的账号授权和网络。")
		}
	} else if b.Kind == "codex_cli" {
		return fail("legacy_cli", "旧 CLI 接入已停用", "在来源配置中准备内置 Codex，完成本人授权。")
	} else {
		probe, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		req, err := s.backendRequest(probe, b, http.MethodGet, "/models", nil)
		if err != nil {
			return fail("invalid_url", "上游地址无效", "填写上游 API Base URL，例如 http://127.0.0.1:8080/v1。")
		}
		resp, err := providerDo(s.client, req)
		if err != nil {
			return fail("unreachable", "无法连接上游或请求超时", "确认上游已启动、端口正确且可访问，再检查网络。")
		}
		defer resp.Body.Close()
		switch {
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			return fail("unauthorized", "上游拒绝授权", "在该来源完成本人授权，检查上游通信 Key、账号权限与额度。")
		case resp.StatusCode == 429:
			return fail("rate_limited", "上游请求受限", "等待上游限流解除并检查账号额度，不会切换到其他来源。")
		case resp.StatusCode >= 300 && resp.StatusCode < 400:
			return fail("redirect", "上游返回重定向，已停止转发凭证", "将 Base URL 改为上游最终 API 地址，不能填写登录页地址。")
		case resp.StatusCode != 200:
			return fail("http_error", "上游模型接口返回错误", "检查上游日志及 Base URL 是否以 /v1 结尾。")
		}
		var data struct {
			Data *[]struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 5<<20)).Decode(&data); err != nil || data.Data == nil {
			return fail("invalid_models", "上游未返回有效的模型列表", "确认地址指向 OpenAI 兼容 API，响应应含 data 数组。")
		}
		for _, m := range *data.Data {
			ids = append(ids, m.ID)
		}
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		model := catalogModel{ID: b.ID + "/" + id, Object: "model", OwnedBy: b.ID, Original: id}
		if b.ID == "opencode" {
			model.Access = openCodeAccess(id)
			if model.Access.Kind != "free_candidate" {
				continue
			}
		}
		result.models = append(result.models, model)
	}
	if len(result.models) == 0 {
		return fail("empty_models", "上游返回空模型列表", "检查账号授权与上游模型配置；模型为空不能算接入成功。")
	}
	result.State = "catalog_ready"
	result.ModelCount = len(result.models)
	result.DurationMs = time.Since(begin).Milliseconds()
	result.Next = "已读取模型目录。复制完整模型 ID 到客户端，发送一次消息验证实际推理权限。"
	if b.Kind == "builtin" && (b.ID == "vertex" || b.ID == "workbuddy") {
		result.Next = "内置模型目录；请发送消息验证实际权限。"
	}
	return result
}
