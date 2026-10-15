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

//go:embed index.html
var static embed.FS

const maxBody = 32 << 20

type Server struct {
	mu       sync.RWMutex
	cfg      Config
	path     string
	client   *http.Client
	children map[string]*exec.Cmd
	status   map[string]string
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
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
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
