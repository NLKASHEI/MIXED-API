package gateway

import (
	"bytes"
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NLKASHEI/MIXED-API/internal/googleweb"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed index.html assets/hero.webp notices.txt
var static embed.FS

const maxBody = 32 << 20

type Server struct {
	resumes           resumeStore
	mu                sync.RWMutex
	cfg               Config
	path              string
	client            *http.Client
	status            map[string]string
	logsMu            sync.Mutex
	logs              []RequestLog
	protocolMu        sync.Mutex
	opencodeProtocols map[string]openCodeTarget
	protocolsAt       time.Time
	accountMu         sync.Mutex
	logins            map[string]*loginAttempt
	refreshes         map[string]*accountRefresh
	google            googleweb.Sessions
	googleBuildUnary  map[string]bool
	systemProxyAt     time.Time
	systemProxy       func(*http.Request) (*url.URL, error)
}

func NewServer(path string, cfg Config) *Server {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	s := &Server{cfg: cfg, path: path, systemProxy: systemProxyFunc(), systemProxyAt: time.Now(), status: make(map[string]string)}
	transport.Proxy = s.requestProxy
	transport.OnProxyConnectResponse = func(ctx context.Context, _ *url.URL, req *http.Request, resp *http.Response) error {
		d, _ := ctx.Value(diagnosticKey{}).(*requestDiagnostic)
		d.set(func(v *RequestDetails) { v.ProxyStatus = resp.StatusCode })
		return nil
	}
	s.client = &http.Client{Transport: transport, Timeout: 0, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return s
}
func (s *Server) config() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.cfg
	c.Backends = append([]Backend(nil), c.Backends...)
	c.EngineSettings = map[string]engineSettings{}
	for k, v := range s.cfg.EngineSettings {
		c.EngineSettings[k] = v
	}
	c.Aliases = make(map[string]string, len(s.cfg.Aliases))
	for name, target := range s.cfg.Aliases {
		c.Aliases[name] = target
	}
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
	if r.URL.Path == "/licenses" && r.Method == "GET" {
		data, _ := static.ReadFile("notices.txt")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write(data)
		return
	}
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
	if (r.URL.Path == "/" || r.URL.Path == "/admin") && r.Method == "GET" {
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
	r = r.WithContext(context.WithValue(r.Context(), resumeOwnerKey{}, resumeOwner(cfg.APIKey)))
	switch {
	case strings.HasPrefix(r.URL.Path, "/v1/resumable/"):
		s.handleResumable(w, r, cfg)
	case r.URL.Path == "/v1/models" && r.Method == "GET":
		s.listModels(w, r, cfg)
	case (r.URL.Path == "/v1/chat/completions" || r.URL.Path == "/v1/audio/speech" || r.URL.Path == "/v1/responses" || r.URL.Path == "/v1/embeddings" || r.URL.Path == "/v1/messages" || r.URL.Path == "/v1/messages/count_tokens") && r.Method == "POST":
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
	results := s.discover(r.Context(), c)
	models := make([]catalogModel, 0)
	errorsByBackend := map[string]string{}
	for _, result := range results {
		models = append(models, result.models...)
		if result.Error != "" {
			errorsByBackend[result.ID] = result.Error
			s.addLog(RequestLog{Time: time.Now().Format(time.RFC3339), Method: "GET", Path: "/v1/models", Backend: result.ID, Status: 502, Error: result.Error, Details: result.Details})
		}
	}
	available := map[string]bool{}
	access := map[string]*modelAccess{}
	for _, m := range models {
		access[m.ID] = m.Access
		available[m.ID] = true
	}
	names := make([]string, 0, len(c.Aliases))
	for name := range c.Aliases {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		target := c.Aliases[name]
		if available[target] {
			models = append(models, catalogModel{ID: name, Object: "model", OwnedBy: "alias", Original: target, Access: access[target]})
		}
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
	if target, exists := c.Aliases[model]; exists {
		model = target
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
	if observed, ok := w.(*observedResponse); ok {
		observed.backend = id
		observed.model = model
	}
	if b.Kind == "builtin" {
		s.serveIntegrated(w, r, *b, model, upstreamModel, payload)
		return
	}
	if b.Kind == "codex_cli" {
		jsonError(w, 410, "旧 CLI 接入已停用，请使用内置 Codex 重新授权")
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
	upstream, e := providerDo(s.client, req)
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
	case strings.HasPrefix(r.URL.Path, "/api/accounts/"):
		s.handleAccount(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/engine-settings/"):
		s.handleEngineSettings(w, r)
	case r.URL.Path == "/api/local/import" && r.Method == http.MethodPost:
		jsonError(w, 410, "内置来源无需导入或下载上游程序")
	case r.URL.Path == "/api/diagnostics" && r.Method == http.MethodGet:
		jsonResult(w, 200, map[string]any{"version": BuildVersion, "platform": runtime.GOOS, "backends": s.discover(r.Context(), c)})
	case r.URL.Path == "/api/upstreams" && r.Method == http.MethodGet:
		jsonResult(w, 200, upstreamCatalog)
	case r.URL.Path == "/api/logs" && (r.Method == http.MethodGet || r.Method == http.MethodDelete):
		s.writeLogs(w, r)
	case r.URL.Path == "/api/network":
		s.handleNetwork(w, r)
	case r.URL.Path == "/api/express/project":
		s.handleExpressProject(w, r)
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
		case "prepare":
			err = s.prepareBackend(id)
		case "activate":
			err = s.activateBackend(r.Context(), id)
		case "quickstart", "install":
			err = s.prepareBackend(id)
		case "launch":
			err = s.activateBackend(r.Context(), id)
		case "login":
			err = errors.New("请使用来源配置中的账号授权入口")
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
		jsonResult(w, 200, map[string]any{"service": "MIXED-API", "version": BuildVersion, "processes": out, "backends": c.Backends})
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
		if back.Kind == "builtin" {
			var err error
			switch action {
			case "start":
				err = s.activateBackend(r.Context(), id)
			case "stop":
				err = s.setBackendEnabled(id, false)
			default:
				jsonError(w, 404, "Unknown action")
				return
			}
			if err != nil {
				jsonError(w, 400, err.Error())
				return
			}
			jsonResult(w, 200, map[string]bool{"ok": true})
			return
		}
		jsonError(w, 410, "应用不再启动外部来源程序；请配置内置来源或已有服务连接")
	default:
		jsonError(w, 404, "Unknown admin API")
	}
}
func loopback(addr string) bool {
	host, _, e := net.SplitHostPort(addr)
	return e == nil && (host == "127.0.0.1" || host == "::1" || host == "localhost")
}
