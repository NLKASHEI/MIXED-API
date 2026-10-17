package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCodeUsesReviewedCurrentEndpoints(t *testing.T) {
	for _, example := range []struct{ model, path, response string }{
		{"mimo-v2.6-flash-free", "/inference/openai/v1/chat/completions", `{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`},
		{"gpt-5.5", "/inference/openai/v1/responses", `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"hello"}]}]}`},
		{"claude-opus-4-6", "/inference/anthropic/v1/messages", `{"id":"msg","type":"message","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn"}`},
		{"gemini-4-argon", "/inference/google/v1beta/models/gemini-4-argon:generateContent", `{"candidates":[{"index":0,"content":{"parts":[{"text":"hello"}]},"finishReason":"STOP"}]}`},
	} {
		t.Run(example.model, func(t *testing.T) {
			cfg := Defaults()
			cfg.APIKey, cfg.AdminKey = "client", "admin"
			cfg.Backends = []Backend{{ID: "opencode", Kind: "builtin", URL: builtinBase("opencode"), Auth: "none", ProviderKey: "zen-key", Enabled: true}}
			app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
			calls := 0
			app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				auth := "Bearer zen-key"
				if example.model == "mimo-v2.6-flash-free" {
					auth = "Bearer public"
				}
				if r.URL.Host != "opencode.ai" || r.URL.Path != example.path || r.Header.Get("Authorization") != auth {
					t.Errorf("incorrect route: %s", r.URL)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(example.response))}, nil
			})
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"opencode/`+example.model+`","messages":[{"role":"user","content":"hi"}]}`))
			r.Header.Set("Authorization", "Bearer client")
			w := httptest.NewRecorder()
			app.ServeHTTP(w, r)
			if w.Code != 200 || calls != 1 || !strings.Contains(w.Body.String(), `"content":"hello"`) {
				t.Fatalf("current protocol: %d %s; calls=%d", w.Code, w.Body.String(), calls)
			}
		})
	}
}

func TestOpenCodeRejectsCredentialRedirectInModelMetadata(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey, cfg.AdminKey = "client", "admin"
	cfg.Backends = []Backend{{ID: "opencode", Kind: "builtin", URL: builtinBase("opencode"), Auth: "none", ProviderKey: "zen-key", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	calls := 0
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"opencode":{"npm":"@ai-sdk/openai-compatible","models":{"injected":{"provider":{"api":"https://attacker.invalid/v1"}}}}}`))}, nil
	})
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"opencode/injected","messages":[{"role":"user","content":"hi"}]}`))
	r.Header.Set("Authorization", "Bearer client")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 503 || calls != 1 {
		t.Fatalf("unsafe metadata produced inference: %d calls=%d %s", w.Code, calls, w.Body.String())
	}
}
