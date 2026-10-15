package gateway

import (
	"bytes"
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

//go:embed index.html assets/hero.webp
var static embed.FS

const maxBody = 32 << 20

type Server struct {
	mu       sync.RWMutex
	cfg      Config
	path     string
	client   *http.Client
	children map[string]*exec.Cmd
	status   map[string]string
	logsMu   sync.Mutex
	logs     []RequestLog
}

func NewServer(path string, cfg Config) *Server {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	return &Server{cfg: cfg, path: path, client: &http.Client{Transport: transport, Timeout: 0}, children: make(map[string]*exec.Cmd), status: make(map[string]string)}
}
func (s *Server) config() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.cfg
	c.Backends = append([]Backend(nil), c.Backends...)
	return c
}
func backendURL(s string) (*url.URL, error) {
	u, e := url.Parse(s)
	if e != nil || u == nil || u.Scheme != "http" && u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("URL must be absolute http(s), without credentials, query or fragment")
	}
	return u, nil
}
func bearer(req *http.Request, key string) bool {
	if key == "" {
		return false
	}
	p := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
	if p == req.Header.Get("Authorization") {
		p = req.Header.Get("X-Admin-Key")
	}
	return subtle.ConstantTimeCompare([]byte(p), []byte(key)) == 1
}
func jsonResult(w http.ResponseWriter, status int, val any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(val)
}
func jsonError(w http.ResponseWriter, status int, msg string) {
	jsonResult(w, status, map[string]any{"error": map[string]any{"message": msg, "type": "mixed_api_error"}})
}

// APIOnly exposes inference endpoints on an explicitly enabled private-LAN listener.
// Crucially, administrative endpoints /api/* and the web configuration panel
// are never reachable through the LAN port, even with the API key.
func APIOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" && !strings.HasPrefix(r.URL.Path, "/v1/") {
			jsonError(w, http.StatusNotFound, "Not found on LAN API listener")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only inference routes are logged; never record prompts or admin secrets.
	w, finishLog := s.tracked(w, r)
	defer finishLog()
	if r.URL.Path == "/assets/hero.webp" && r.Method == http.MethodGet {
		data, e := static.ReadFile("assets/hero.webp")
		if e != nil {
			jsonError(w, http.StatusInternalServerError, "Asset not available")
			return
		}
		w.Header().Set("Content-Type", "image/webp")
		w.Header().Set("Cache-Control", "public, max-age=604800")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(data)
		return
	}
	if r.URL.Path == "/" && r.Method == "GET" {
		data, _ := static.ReadFile("index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
		return
	}
	if r.URL.Path == "/health" {
		jsonResult(w, 200, map[string]any{"ok": true, "service": "MIXED-API"})
		return
	}
	cfg := s.config()
	if strings.HasPrefix(r.URL.Path, "/api/") {
		if !bearer(r, cfg.AdminKey) {
			jsonError(w, 401, "Admin key required")
			return
		}
		s.handleAdmin(w, r, cfg)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/v1/") {
		jsonError(w, 404, "Not found")
		return
	}
	if !bearer(r, cfg.APIKey) {
		jsonError(w, 401, "Invalid MIXED-API key")
		return
	}
	switch {
	case r.URL.Path == "/v1/models" && r.Method == "GET":
		s.listModels(w, r, cfg)
	case (r.URL.Path == "/v1/chat/completions" || r.URL.Path == "/v1/responses" || r.URL.Path == "/v1/embeddings" || r.URL.Path == "/v1/messages" || r.URL.Path == "/v1/messages/count_tokens") && r.Method == "POST":
		s.forward(w, r, cfg)
	default:
		jsonError(w, 404, "Unsupported endpoint")
	}
}
func (s *Server) backendRequest(ctx context.Context, b Backend, method, path string, body io.Reader) (*http.Request, error) {
	u, e := backendURL(b.URL)
	if e != nil {
		return nil, e
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	req, e := http.NewRequestWithContext(ctx, method, u.String(), body)
	if e != nil {
		return nil, e
	}
	switch b.Auth {
	case "bearer":
		if b.Key != "" {
			req.Header.Set("Authorization", "Bearer "+b.Key)
		}
	case "x-api-key":
		if b.Key != "" {
			req.Header.Set("x-api-key", b.Key)
		}
	}
	return req, nil
}
func (s *Server) listModels(w http.ResponseWriter, r *http.Request, c Config) {
	type Model struct {
		ID       string `json:"id"`
		Object   string `json:"object"`
		OwnedBy  string `json:"owned_by"`
		Original string `json:"original_id"`
	}
	models := make([]Model, 0)
	errorsByBackend := map[string]string{}
	for _, b := range c.Backends {
		if !b.Enabled {
			continue
		}
		if b.Kind == "codex_cli" {
			ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
			cli, err := openCodex(ctx)
			if err != nil {
				errorsByBackend[b.ID] = err.Error()
				cancel()
				continue
			}
			ids, err := cli.models(ctx)
			cli.close()
			cancel()
			if err != nil {
				errorsByBackend[b.ID] = err.Error()
				continue
			}
			for _, id := range ids {
				models = append(models, Model{ID: b.ID + "/" + id, Object: "model", OwnedBy: b.ID, Original: id})
			}
			continue
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		q, e := s.backendRequest(ctx, b, "GET", "/models", nil)
		if e != nil {
			cancel()
			errorsByBackend[b.ID] = e.Error()
			continue
		}
		resp, e := s.client.Do(q)
		if e != nil {
			cancel()
			errorsByBackend[b.ID] = e.Error()
			continue
		}
		var data struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		e = json.NewDecoder(io.LimitReader(resp.Body, 5<<20)).Decode(&data)
		resp.Body.Close()
		cancel()
		if resp.StatusCode != 200 {
			errorsByBackend[b.ID] = fmt.Sprintf("upstream HTTP %d", resp.StatusCode)
			continue
		}
		if e != nil {
			errorsByBackend[b.ID] = "invalid upstream model list"
			continue
		}
		for _, m := range data.Data {
			if m.ID != "" {
				models = append(models, Model{ID: b.ID + "/" + m.ID, Object: "model", OwnedBy: b.ID, Original: m.ID})
			}
		}
	}
	for source, reason := range errorsByBackend {
		s.addLog(RequestLog{Time: time.Now().Format(time.RFC3339), Method: "GET", Path: "/v1/models", Backend: source, Status: 502, Error: safeDiagnostic([]byte(reason))})
	}
	jsonResult(w, 200, map[string]any{"object": "list", "data": models, "backend_errors": errorsByBackend})
}
func (s *Server) forward(w http.ResponseWriter, r *http.Request, c Config) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	raw, e := io.ReadAll(r.Body)
	if e != nil {
		jsonError(w, 413, "Body too large or unreadable")
		return
	}
	var payload map[string]json.RawMessage
	if e = json.Unmarshal(raw, &payload); e != nil {
		jsonError(w, 400, "Expected JSON object")
		return
	}
	var model string
	if e = json.Unmarshal(payload["model"], &model); e != nil {
		jsonError(w, 400, "Missing model")
		return
	}
	id, upstreamModel, ok := strings.Cut(model, "/")
	if !ok || upstreamModel == "" {
		jsonError(w, 400, "Model must have backend prefix: backend/model")
		return
	}
	var b *Backend
	for i := range c.Backends {
		if c.Backends[i].ID == id && c.Backends[i].Enabled {
			b = &c.Backends[i]
			break
		}
	}
	if b == nil {
		jsonError(w, 404, "Backend not enabled: "+id)
		return
	}
	if observed, ok := w.(*observedResponse); ok { observed.backend = id }
	if b.Kind == "codex_cli" {
		if r.URL.Path != "/v1/chat/completions" {
			jsonError(w, 501, "Codex CLI supports /v1/chat/completions only")
			return
		}
		s.forwardCodex(w, r, model, upstreamModel, payload)
		return
	}
	payload["model"], _ = json.Marshal(upstreamModel)
	raw, e = json.Marshal(payload)
	if e != nil {
		jsonError(w, 400, "Invalid request body")
		return
	}
	suffix := strings.TrimPrefix(r.URL.Path, "/v1")
	req, e := s.backendRequest(r.Context(), *b, r.Method, suffix, bytes.NewReader(raw))
	if e != nil {
		jsonError(w, 502, "Bad backend URL")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", r.Header.Get("Accept"))
	for _, header := range []string{"Anthropic-Version", "Anthropic-Beta", "OpenAI-Beta", "X-Request-ID"} {
		if v := r.Header.Get(header); v != "" {
			req.Header.Set(header, v)
		}
	}
	upstream, e := s.client.Do(req)
	if e != nil {
		log.Printf("backend %s: %s", id, e)
		jsonError(w, 502, "Backend unavailable: "+id)
		return
	}
	defer upstream.Body.Close()
	for _, key := range []string{"Content-Type", "Cache-Control", "X-Request-ID"} {
		if v := upstream.Header.Get(key); v != "" {
			w.Header().Set(key, v)
		}
	}
	w.Header().Set("X-Mixed-Backend", id)
	w.WriteHeader(upstream.StatusCode)
	buf := make([]byte, 32*1024)
	for {
		n, e := upstream.Body.Read(buf)
		if n > 0 {
			if _, wrErr := w.Write(buf[:n]); wrErr != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		if e != nil {
			return
		}
	}
}
func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request, c Config) {
	switch {
	case r.URL.Path == "/api/logs" && (r.Method == http.MethodGet || r.Method == http.MethodDelete):
		s.writeLogs(w,r)
	case r.URL.Path == "/api/config" && r.Method == "GET":
		jsonResult(w, 200, c)
	case r.URL.Path == "/api/guides" && r.Method == "GET":
		jsonResult(w, 200, Guide)
	case r.URL.Path == "/api/config" && r.Method == "PUT":
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var next Config
		if e := json.NewDecoder(r.Body).Decode(&next); e != nil {
			jsonError(w, 400, "Invalid JSON")
			return
		}
		if next.Listen != c.Listen {
			jsonError(w, 400, "Listen address cannot be changed while running")
			return
		}
		if next.APIKey != c.APIKey || next.AdminKey != c.AdminKey {
			jsonError(w, 400, "Keys are immutable in the UI; edit local config offline to rotate")
			return
		}
		if e := Validate(next); e != nil {
			jsonError(w, 400, e.Error())
			return
		}
		if e := Save(s.path, next); e != nil {
			jsonError(w, 500, "Could not save config")
			return
		}
		s.mu.Lock()
		s.cfg = next
		s.mu.Unlock()
		jsonResult(w, 200, map[string]bool{"saved": true})
	case r.URL.Path == "/api/update" && r.Method == "GET":
		info, err := s.updateInfo(r.Context())
		if err != nil {
			jsonError(w, http.StatusBadGateway, "无法检查更新："+err.Error())
			return
		}
		jsonResult(w, 200, info)
	case r.URL.Path == "/api/update/install" && r.Method == "POST":
		if err := s.downloadAndOpenUpdate(); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResult(w, 202, map[string]any{"ok": true, "message": "正在下载并校验安装包，请在更新页面查看进度"})
	case r.URL.Path == "/api/assist/status" && r.Method == "GET":
		jsonResult(w, 200, s.assistStatus())
	case strings.HasPrefix(r.URL.Path, "/api/assist/") && r.Method == "POST":
		target := strings.TrimPrefix(r.URL.Path, "/api/assist/")
		id, action, ok := strings.Cut(target, "/")
		if !ok {
			jsonError(w, 404, "Unknown action")
			return
		}
		var err error
		switch action {
		case "quickstart":
			err = s.quickstartBackend(id)
		case "install":
			err = s.setupBackend(id)
		case "launch":
			err = s.launchInstalled(id)
		case "login":
			err = s.loginInstalled(id)
		default:
			jsonError(w, 404, "Unknown action")
			return
		}
		if err != nil {
			jsonError(w, 400, err.Error())
			return
		}
		jsonResult(w, 200, map[string]any{"ok": true, "message": "操作已发起；可刷新状态确认结果"})
	case r.URL.Path == "/api/status" && r.Method == "GET":
		out := map[string]string{}
		s.mu.RLock()
		for k, v := range s.status {
			out[k] = v
		}
		s.mu.RUnlock()
		jsonResult(w, 200, map[string]any{"processes": out, "backends": c.Backends})
	case strings.HasPrefix(r.URL.Path, "/api/process/") && r.Method == "POST":
		target := strings.TrimPrefix(r.URL.Path, "/api/process/")
		id, action, ok := strings.Cut(target, "/")
		if !ok {
			jsonError(w, 404, "Not found")
			return
		}
		var back *Backend
		for i := range c.Backends {
			if c.Backends[i].ID == id {
				back = &c.Backends[i]
				break
			}
		}
		if back == nil {
			jsonError(w, 404, "Unknown backend")
			return
		}
		if action == "start" {
			if e := s.startProcess(*back); e != nil {
				jsonError(w, 400, e.Error())
				return
			}
		} else if action == "stop" {
			s.stopProcess(id)
		} else {
			jsonError(w, 404, "Unknown action")
			return
		}
		jsonResult(w, 200, map[string]bool{"ok": true})
	default:
		jsonError(w, 404, "Unknown admin API")
	}
}
func (s *Server) startProcess(b Backend) error {
	if strings.TrimSpace(b.Command) == "" {
		return errors.New("No command configured for " + b.ID)
	}
	s.mu.Lock()
	if _, ok := s.children[b.ID]; ok {
		s.mu.Unlock()
		return nil
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", b.Command)
	} else {
		cmd = exec.Command("sh", "-c", b.Command)
	}
	if b.Workdir != "" {
		cmd.Dir = b.Workdir
	}
	s.bindBackendOutput(cmd, b.ID)
	if e := cmd.Start(); e != nil {
		s.mu.Unlock()
		return e
	}
	s.children[b.ID] = cmd
	s.status[b.ID] = "running"
	s.mu.Unlock()
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.children[b.ID] == cmd {
			delete(s.children, b.ID)
			s.status[b.ID] = "exited"
			if err != nil {
				s.status[b.ID] = "exited: " + err.Error()
			}
		}
	}()
	return nil
}
func (s *Server) stopProcess(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.children[id]; ok {
		_ = c.Process.Kill()
		s.status[id] = "stopping"
	}
}
func loopback(addr string) bool {
	host, _, e := net.SplitHostPort(addr)
	return e == nil && (host == "127.0.0.1" || host == "::1" || host == "localhost")
}

func (s *Server) forwardCodex(w http.ResponseWriter, r *http.Request, publicModel, model string, payload map[string]json.RawMessage) {
	ctx, cancel := context.WithTimeout(r.Context(), 14*time.Minute)
	defer cancel()
	cli, err := openCodex(ctx)
	if err != nil {
		jsonError(w, 502, err.Error())
		return
	}
	defer cli.close()
	var stream bool
	_ = json.Unmarshal(payload["stream"], &stream)
	var effort string
	_ = json.Unmarshal(payload["reasoning_effort"], &effort)
	id := fmt.Sprintf("chatcmpl-mixed-%d", time.Now().UnixNano())
	created := time.Now().Unix()
	started := false
	send := func(delta string) error {
		if !started {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("X-Accel-Buffering", "no")
			w.WriteHeader(200)
			started = true
		}
		chunk := map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": publicModel, "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": delta}, "finish_reason": nil}}}
		b, _ := json.Marshal(chunk)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return err
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return nil
	}
	var emit func(string) error
	if stream {
		emit = send
	}
	content, err := cli.chat(ctx, model, payload["messages"], effort, emit)
	if err != nil {
		if !started {
			jsonError(w, 502, err.Error())
		} else {
			msg, _ := json.Marshal(map[string]any{"error": map[string]string{"message": err.Error(), "type": "codex_error"}})
			fmt.Fprintf(w, "data: %s\n\n", msg)
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
		return
	}
	if stream {
		if !started {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
		}
		last := map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": publicModel, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}}
		b, _ := json.Marshal(last)
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
		return
	}
	jsonResult(w, 200, map[string]any{"id": id, "object": "chat.completion", "created": created, "model": publicModel, "choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": content}, "finish_reason": "stop"}}})
}
