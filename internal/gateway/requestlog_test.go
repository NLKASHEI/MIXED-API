package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestLogRecordsErrorsWithoutSecrets(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.AdminKey = "admin"
	for i := range cfg.Backends {
		cfg.Backends[i].Enabled = false
	}
	s := NewServer("", cfg)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"missing/test"}`))
	req.Header.Set("Authorization", "Bearer client")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("wrong status %d", w.Code)
	}
	logs := s.logsSnapshot()
	if len(logs) != 1 || logs[0].Status != 404 || !strings.Contains(logs[0].Error, "missing") {
		t.Fatalf("unexpected logs: %+v", logs)
	}
	admin := httptest.NewRequest("GET", "/api/logs", nil)
	admin.Header.Set("Authorization", "Bearer admin")
	aw := httptest.NewRecorder()
	s.ServeHTTP(aw, admin)
	if aw.Code != 200 || !strings.Contains(aw.Body.String(), `"entries"`) {
		t.Fatalf("admin logs unavailable: %d %s", aw.Code, aw.Body.String())
	}
	denied := httptest.NewRequest("GET", "/api/logs", nil)
	denied.Header.Set("Authorization", "Bearer client")
	dw := httptest.NewRecorder()
	s.ServeHTTP(dw, denied)
	if dw.Code != 401 {
		t.Fatal("client key can read admin-only logs")
	}
}
func TestRedactSensitiveErrorData(t *testing.T) {
	in := []byte(`{"error":{"message":"authorization bearer abcdefg secret sk-abcdefghijklmnopqrstuvwxyz token=abcdefgh"}}`)
	text := safeDiagnostic(in)
	if strings.Contains(text, "abcdefghijkl") || strings.Contains(text, "abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("secret leaked: %q", text)
	}
}
func TestLogRingBounded(t *testing.T) {
	s := NewServer("", Defaults())
	for i := 0; i < 300; i++ {
		s.addLog(RequestLog{Status: 200})
	}
	if got := len(s.logsSnapshot()); got != 200 {
		t.Fatalf("size %d", got)
	}
}
