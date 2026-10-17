package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExpressProjectProbeReturnsRoutingWithoutExposingKey(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey, cfg.AdminKey = "client", "admin"
	app := NewServer("", cfg)
	attempts := 0
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		if r.URL.Host != "aiplatform.googleapis.com" || r.Header.Get("x-goog-api-key") != "private-key" || !strings.Contains(r.URL.Path, "mixed-api-probe-nonexistent-model") {
			t.Fatal("wrong probe request")
		}
		return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{\"error\":{\"status\":\"NOT_FOUND\",\"message\":\"Publisher model `projects/123456/locations/us-central1/publishers/google/models/mixed-api-probe-nonexistent-model` was not found private-key\"}}"))}, nil
	})
	if w := accountTestCall(app, "POST", "/api/express/project", "client", `{"provider_key":"private-key"}`); w.Code != 401 {
		t.Fatal("client key reached probe", w.Code)
	}
	w := accountTestCall(app, "POST", "/api/express/project", "admin", `{"provider_key":"private-key"}`)
	if w.Code != 200 || attempts != 1 || !strings.Contains(w.Body.String(), `"project":"123456"`) || !strings.Contains(w.Body.String(), `"location":"us-central1"`) || strings.Contains(w.Body.String(), "private-key") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestExpress404ExplainsResolvedRegionWithoutRetry(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey, cfg.AdminKey = "client", "admin"
	cfg.Backends = []Backend{{ID: "vertex", Kind: "builtin", URL: builtinBase("vertex"), ProviderKey: "private-key", Enabled: true}}
	app := NewServer("", cfg)
	attempts := 0
	app.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{\"error\":{\"status\":\"NOT_FOUND\",\"message\":\"Publisher model `projects/test-project/locations/us-central1/publishers/google/models/gemini-test` was not found private-key PRIVATE_PROMPT\"}}"))}, nil
	})
	w := accountTestCall(app, "POST", "/v1/chat/completions", "client", `{"model":"vertex/gemini-test","messages":[{"role":"user","content":"PRIVATE_PROMPT"}]}`)
	if w.Code != 404 || attempts != 1 {
		t.Fatal(w.Code, attempts)
	}
	logs := accountTestCall(app, "GET", "/api/logs", "admin", "").Body.String()
	if !strings.Contains(logs, `"resolved_location":"us-central1"`) || strings.Contains(logs, "PRIVATE_PROMPT") || strings.Contains(logs, "private-key") {
		t.Fatal(logs)
	}
}

func TestExpressRetryStopsWhenClientCancels(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.Backends = []Backend{{ID: "vertex", Kind: "builtin", URL: builtinBase("vertex"), ProviderKey: "key", Enabled: true}}
	app := NewServer("", cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	app.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		cancel()
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"30"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"status":"RESOURCE_EXHAUSTED"}}`))}, nil
	})
	// The real HTTP handler propagates this request's cancellation to retries.
	r, _ := http.NewRequestWithContext(ctx, "POST", "/v1/chat/completions", strings.NewReader(`{"model":"vertex/gemini-test","messages":[{"role":"user","content":"hi"}]}`))
	r.Header.Set("Authorization", "Bearer client")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if calls != 1 {
		t.Fatal("cancelled request retried", calls)
	}
}

func TestExpressRetriesBusyResponseBeforeStreaming(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey, cfg.AdminKey = "client", "admin"
	cfg.Backends = []Backend{{ID: "vertex", Kind: "builtin", URL: builtinBase("vertex"), ProviderKey: "private-key", Enabled: true}}
	cfg.EngineSettings = map[string]engineSettings{"vertex": {MaxRetries: 1, RequestTimeoutSeconds: 10}}
	app := NewServer("", cfg)
	attempts := 0
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		if r.URL.Path != "/v1/publishers/google/models/gemini-test:streamGenerateContent" || r.Header.Get("x-goog-api-key") != "private-key" {
			t.Fatal("retry changed the model, route or credentials")
		}
		if attempts == 1 {
			return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"0"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"status":"RESOURCE_EXHAUSTED"}}`))}, nil
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: {\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"text\":\"OK\"}]},\"finishReason\":\"STOP\"}]}\n\n"))}, nil
	})
	w := accountTestCall(app, "POST", "/v1/chat/completions", "client", `{"model":"vertex/gemini-test","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	if attempts != 2 || w.Code != 200 || !strings.Contains(w.Body.String(), "[DONE]") {
		t.Fatalf("attempts=%d HTTP=%d body=%s", attempts, w.Code, w.Body.String())
	}
	logs := accountTestCall(app, "GET", "/api/logs", "admin", "").Body.String()
	if !strings.Contains(logs, `"attempt":2`) || !strings.Contains(logs, `"retry_status":429`) {
		t.Fatal(logs)
	}
}
