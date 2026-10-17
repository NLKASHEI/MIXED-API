package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenCodeChatToAnthropic(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "full", true: "stream"}[stream], func(t *testing.T) {
			cfg := Defaults()
			cfg.APIKey, cfg.AdminKey = "client", "admin"
			cfg.Backends = []Backend{{ID: "opencode", Kind: "builtin", URL: builtinBase("opencode"), Auth: "none", ProviderKey: "zen-key", Enabled: true}}
			app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
			app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				data := `{"opencode":{"npm":"@ai-sdk/anthropic","models":{"claude-test":{}}}}`
				if r.URL.Host == "opencode.ai" {
					if r.URL.Path != "/inference/anthropic/v1/messages" || r.Header.Get("Anthropic-Version") != "2023-06-01" || r.Header.Get("Authorization") != "Bearer zen-key" {
						t.Error("incorrect Anthropic protocol or identity")
					}
					var b map[string]any
					json.NewDecoder(r.Body).Decode(&b)
					messages, _ := b["messages"].([]any)
					if len(messages) != 3 || b["system"] != "Be brief" || b["max_tokens"] != float64(123) {
						t.Errorf("message context or limit lost: %+v", b)
					}
					tools, _ := b["tools"].([]any)
					if len(tools) != 1 {
						t.Fatal("user tool lost")
					}
					if tools[0].(map[string]any)["input_schema"] == nil {
						t.Error("tool schema lost")
					}
					data = `{"id":"msg-a","type":"message","content":[{"type":"thinking","thinking":"Think"},{"type":"text","text":"Answer"},{"type":"tool_use","id":"call-a","name":"clock","input":{"zone":"UTC"}}],"stop_reason":"tool_use","usage":{"input_tokens":4,"output_tokens":6}}`
					if stream {
						data = "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-a\",\"usage\":{\"input_tokens\":4,\"output_tokens\":1}}}\n\n" +
							"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"Think\"}}\n\n" +
							"data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"Answer\"}}\n\n" +
							"data: {\"type\":\"content_block_start\",\"index\":2,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call-a\",\"name\":\"clock\",\"input\":{}}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":2,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"zone\\\":\\\"UTC\\\"}\"}}\n\n" +
							"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":6}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
					}
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
			})
			payload := map[string]any{"model": "opencode/claude-test", "stream": stream, "stream_options": map[string]any{"include_usage": true}, "max_tokens": 123, "messages": []any{map[string]any{"role": "system", "content": "Be brief"}, map[string]any{"role": "user", "content": "time"}, map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{"id": "previous", "type": "function", "function": map[string]any{"name": "clock", "arguments": "{}"}}}}, map[string]any{"role": "tool", "tool_call_id": "previous", "content": "noon"}}, "tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "clock", "parameters": map[string]any{"type": "object"}}}}}
			raw, _ := json.Marshal(payload)
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(raw)))
			r.Header.Set("Authorization", "Bearer client")
			w := httptest.NewRecorder()
			app.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("Anthropic conversion: %d %s", w.Code, w.Body.String())
			}
			for _, want := range []string{`"reasoning_content":"Think"`, `"content":"Answer"`, `"id":"call-a"`, `"finish_reason":"tool_calls"`, `"total_tokens":10`} {
				if !strings.Contains(w.Body.String(), want) {
					t.Errorf("missing %s: %s", want, w.Body.String())
				}
			}
			if stream && !strings.Contains(w.Body.String(), "data: [DONE]") {
				t.Fatal("missing terminal event")
			}
		})
	}
}

func TestOpenCodeProviderRejectionDoesNotLeakOrFallback(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey, cfg.AdminKey = "client", "admin"
	cfg.Backends = []Backend{{ID: "opencode", Kind: "builtin", URL: builtinBase("opencode"), Auth: "none", ProviderKey: "private-zen-token", Enabled: true}, {ID: "vertex", Kind: "builtin", URL: builtinBase("vertex"), Auth: "none", Enabled: true, ProviderKey: "other"}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "models.opencode.ai" {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"opencode":{"npm":"@ai-sdk/openai-compatible","models":{"test":{}}}}`))}, nil
		}
		if r.URL.Host != "opencode.ai" {
			t.Error("request switched providers")
		}
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"30"}}, Body: io.NopCloser(strings.NewReader(`{"message":"Authorization Bearer private-zen-token"}`))}, nil
	})
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"opencode/test","messages":[{"role":"user","content":"hi"}]}`))
	r.Header.Set("Authorization", "Bearer client")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 429 || w.Header().Get("Retry-After") != "30" || strings.Contains(w.Body.String(), "private-zen-token") {
		t.Fatalf("unsafe error: %d %s", w.Code, w.Body.String())
	}
}

// A real HTTP connection at both ends verifies that closing the client stream
// cancels the provider request, including when its next event never arrives.
func TestOpenCodeClientCancellationClosesProvider(t *testing.T) {
	canceled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api.json" {
			io.WriteString(w, `{"opencode":{"npm":"@ai-sdk/openai","models":{"test":{}}}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"start\"}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(canceled)
	}))
	defer upstream.Close()
	cfg := Defaults()
	cfg.APIKey, cfg.AdminKey = "client", "admin"
	cfg.Backends = []Backend{{ID: "opencode", Kind: "builtin", URL: builtinBase("opencode"), Auth: "none", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	target, _ := url.Parse(upstream.URL)
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		copyURL := *r.URL
		clone.URL = &copyURL
		clone.URL.Scheme, clone.URL.Host = target.Scheme, target.Host
		return http.DefaultTransport.RoundTrip(clone)
	})
	public := httptest.NewServer(app)
	defer public.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "POST", public.URL+"/v1/chat/completions", strings.NewReader(`{"model":"opencode/test","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	r.Header.Set("Authorization", "Bearer client")
	resp, err := public.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buffer := make([]byte, 32)
	if _, err = resp.Body.Read(buffer); err != nil {
		t.Fatal(err)
	}
	cancel()
	resp.Body.Close()
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("provider outlived canceled client")
	}
}
