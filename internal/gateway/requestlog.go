package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// RequestLog contains only routing metadata and a short diagnostic; never prompts,
// user messages, cookies, authentication headers, or response bodies on success.
type RequestLog struct {
	RequestID  string          `json:"request_id,omitempty"`
	Model      string          `json:"model,omitempty"`
	Details    *RequestDetails `json:"details,omitempty"`
	Time       string          `json:"time"`
	Method     string          `json:"method"`
	Path       string          `json:"path"`
	Backend    string          `json:"backend,omitempty"`
	Status     int             `json:"status"`
	DurationMs int64           `json:"duration_ms"`
	Error      string          `json:"error,omitempty"`
}

// Remove likely credentials even if a malicious upstream reflects secrets in an error.
var secretPattern = regexp.MustCompile(`(?i)(bearer\s+\S+|sk-[\w-]{12,}|admin-[\w-]{12,}|AIza[\w-]{20,}|cookie\s*[:=]\s*\S+|token\s*[:=]\s*\S+)`)

func safeDiagnostic(raw []byte) string {
	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	var value string
	if json.Unmarshal(raw, &parsed) == nil {
		value = parsed.Error.Message
		if value == "" {
			value = parsed.Message
		}
	}
	if value == "" {
		value = string(raw)
	}
	value = strings.ReplaceAll(strings.ReplaceAll(value, "\r", " "), "\n", " ")
	value = secretPattern.ReplaceAllString(value, "[redacted]")
	r := []rune(value)
	if len(r) > 180 {
		r = r[:180]
	}
	return string(r)
}

type observedResponse struct {
	http.ResponseWriter
	code      int
	errorBody []byte
	backend   string
	model     string
}

func (w *observedResponse) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *observedResponse) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	if w.code >= 400 && len(w.errorBody) < 1024 {
		remaining := 1024 - len(w.errorBody)
		if len(b) < remaining {
			remaining = len(b)
		}
		w.errorBody = append(w.errorBody, b[:remaining]...)
	}
	return w.ResponseWriter.Write(b)
}
func (w *observedResponse) Flush() {
	if w.code == 0 {
		w.code = 200
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (w *observedResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (s *Server) tracked(w http.ResponseWriter, r *http.Request) (http.ResponseWriter, func()) {
	if !strings.HasPrefix(r.URL.Path, "/v1/") {
		return w, func() {}
	}
	begin := time.Now()
	detail := &requestDiagnostic{}
	*r = *r.WithContext(context.WithValue(r.Context(), diagnosticKey{}, detail))
	requestID := newKey("req-")
	w.Header().Set("X-Mixed-Request-ID", requestID)
	observed := &observedResponse{ResponseWriter: w}
	return observed, func() {
		code := observed.code
		if code == 0 {
			code = 200
		}
		entry := RequestLog{Time: begin.Format(time.RFC3339), Method: r.Method, Path: r.URL.Path, Backend: observed.backend, Status: code, DurationMs: time.Since(begin).Milliseconds()}
		entry.RequestID, entry.Model = requestID, observed.model
		detail.mu.Lock()
		snapshot := detail.details
		detail.mu.Unlock()
		if snapshot.Target != "" {
			entry.Details = &snapshot
		}
		if snapshot.ErrorType != "" && code < 400 {
			entry.Error = snapshot.Cause
		}
		if code >= 400 {
			entry.Error = safeDiagnostic(observed.errorBody)
		}

		s.addLog(entry)
	}
}
func (s *Server) addLog(entry RequestLog) {
	cfg := s.config()
	secrets := []string{cfg.APIKey, cfg.AdminKey}
	for _, b := range cfg.Backends {
		secrets = append(secrets, b.Key, b.ProviderKey)
	}
	fields := []*string{&entry.Error, &entry.Model}
	if entry.Details != nil {
		cloned := *entry.Details
		entry.Details = &cloned
		fields = append(fields, &cloned.Target, &cloned.Proxy, &cloned.Address, &cloned.Cause, &cloned.Hint, &cloned.UpstreamCode, &cloned.ResolvedLocation)
	}
	for _, field := range fields {
		for _, secret := range secrets {
			if secret != "" {
				*field = strings.ReplaceAll(*field, secret, "[redacted]")
			}
		}
	}

	s.logsMu.Lock()
	defer s.logsMu.Unlock()
	if len(s.logs) >= 200 {
		copy(s.logs, s.logs[1:])
		s.logs[len(s.logs)-1] = entry
		return
	}
	s.logs = append(s.logs, entry)
}
func (s *Server) logsSnapshot() []RequestLog {
	s.logsMu.Lock()
	defer s.logsMu.Unlock()
	return append([]RequestLog{}, s.logs...)
}
func (s *Server) writeLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		s.logsMu.Lock()
		s.logs = nil
		s.logsMu.Unlock()
		jsonResult(w, 200, map[string]any{"cleared": true})
		return
	}
	if r.Method != http.MethodGet {
		jsonError(w, 405, "Method not allowed")
		return
	}
	jsonResult(w, 200, map[string]any{"entries": s.logsSnapshot()})
}
