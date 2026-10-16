package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"runtime"
	"testing"
)

func fixture(t *testing.T, streaming bool) (*Server, *httptest.Server) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer upstream-secret" {
			t.Errorf("wrong upstream auth header: %q", r.Header.Get("Authorization"))
			http.Error(w, "unauthorized", 401)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"data":[{"id":"gpt-test"}]}`)
		case "/v1/chat/completions":
			body, _ := io.ReadAll(r.Body)
			var v map[string]any
			_ = json.Unmarshal(body, &v)
			if v["model"] != "gpt-test" {
				t.Errorf("upstream model should be unprefixed, got %v", v["model"])
			}
			if streaming {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(200)
				io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
				w.(http.Flusher).Flush()
				io.WriteString(w, "data: [DONE]\n\n")
			} else {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"model":"gpt-test","choices":[]}`)
			}
		default:
			http.Error(w, "unknown", 404)
		}
	}))
	cfg := Defaults()
	cfg.APIKey = "sk-test"
	cfg.AdminKey = "admin-test"
	for i := range cfg.Backends {
		cfg.Backends[i].Enabled = false
	}
	cfg.Backends = append(cfg.Backends, Backend{ID: "mock", Name: "Mock", URL: upstream.URL + "/v1", Key: "upstream-secret", Auth: "bearer", Enabled: true})
	s := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	return s, upstream
}
func TestAuthAndModelIsolation(t *testing.T) {
	s, upstream := fixture(t, false)
	defer upstream.Close()
	for _, tc := range []struct {
		key  string
		code int
	}{{"", 401}, {"admin-test", 401}, {"sk-test", 200}} {
		req := httptest.NewRequest("GET", "http://localhost/v1/models", nil)
		req.Header.Set("Authorization", "Bearer "+tc.key)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != tc.code {
			t.Fatalf("key %q: status=%d want=%d", tc.key, rec.Code, tc.code)
		}
		if rec.Code == 200 && !strings.Contains(rec.Body.String(), `"id":"mock/gpt-test"`) {
			t.Fatalf("models missing prefix: %s", rec.Body.String())
		}
	}
	req := httptest.NewRequest("GET", "/api/config", nil)
	req.Header.Set("Authorization", "Bearer sk-test")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("client key escalated to admin: %d", rec.Code)
	}
}
func TestForwardJSONAndStreaming(t *testing.T) {
	for _, stream := range []bool{false, true} {
		s, upstream := fixture(t, stream)
		body := `{"model":"mock/gpt-test","messages":[{"role":"user","content":"hi"}],"stream":true}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer sk-test")
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("stream %v status=%d body=%s", stream, rec.Code, rec.Body.String())
		}
		if stream && !strings.Contains(rec.Body.String(), "data: [DONE]") {
			t.Fatalf("broken stream: %s", rec.Body.String())
		}
		upstream.Close()
	}
}
func TestRejectInvalidModels(t *testing.T) {
	s, upstream := fixture(t, false)
	defer upstream.Close()
	for _, model := range []string{"gpt-test", "disabled/x", "mock/"} {
		raw, _ := json.Marshal(map[string]any{"model": model})
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer sk-test")
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != 400 && rec.Code != 404 {
			t.Errorf("accepted invalid model %q: %d", model, rec.Code)
		}
	}
}
func TestFreshKeysAndSecureStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	cfg, e := LoadOrCreate(path)
	if e != nil {
		t.Fatal(e)
	}
	if cfg.APIKey == cfg.AdminKey || len(cfg.APIKey) < 40 || len(cfg.AdminKey) < 40 {
		t.Fatal("bad generated keys")
	}
	b, _ := os.ReadFile(path)
	if !bytes.Contains(b, []byte(cfg.APIKey)) {
		t.Fatal("config missing API key")
	}
	fi, _ := os.Stat(path)
	// Windows reports POSIX modes as 0666 even when the file resides in a private user directory.
	// Verify 0600 only on platforms where POSIX mode bits are meaningful.
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0077 != 0 {
		t.Fatalf("file should not be group/world readable, got %v", fi.Mode())
	}
	cfg2, e := LoadOrCreate(path)
	if e != nil || cfg2.APIKey != cfg.APIKey {
		t.Fatalf("keys changed on restart %v", e)
	}
}
func TestCannotBindPublicly(t *testing.T) {
	c := Defaults()
	c.AdminKey = "a"
	c.APIKey = "b"
	c.Listen = "0.0.0.0:8765"
	if Validate(c) == nil {
		t.Fatal("allowed public listening by default")
	}
}

func TestEmbeddedDesktopUIAndImage(t *testing.T) {
	s, up := fixture(t, false)
	defer up.Close()
	for _, tc := range []struct {
		path, contentType string
		contains          string
	}{
		{"/", "text/html", "把模型，连在一起。"},
		{"/assets/hero.webp", "image/webp", ""},
	} {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), tc.contentType) || w.Body.Len() == 0 {
			t.Fatalf("asset %s failed: status=%d content-type=%q size=%d", tc.path, w.Code, w.Header().Get("Content-Type"), w.Body.Len())
		}
		if tc.contains != "" && !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("asset %s missing expected UI", tc.path)
		}
	}
}
