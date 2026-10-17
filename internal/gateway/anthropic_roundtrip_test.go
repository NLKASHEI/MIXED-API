package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise two client requests, including the opaque signed content required
// by Anthropic when a thinking response is continued with tool results.
func TestAnthropicSignedThinkingRoundTrip(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "full", true: "stream"}[stream], func(t *testing.T) {
			cfg := Defaults()
			cfg.APIKey = "client"
			cfg.Backends = []Backend{{ID: "opencode", Kind: "builtin", URL: builtinBase("opencode"), Auth: "none", Enabled: true}}
			app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
			calls := 0
			app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if calls == 2 {
					messages := body["messages"].([]any)
					parts := messages[1].(map[string]any)["content"].([]any)
					raw, _ := json.Marshal(parts)
					for _, value := range []string{`"signature":"signed-data"`, `"thinking":"consider"`, `"type":"redacted_thinking"`, `"data":"opaque-data"`} {
						if !strings.Contains(string(raw), value) {
							t.Errorf("signed context missing %s: %s", value, raw)
						}
					}
				}
				data := `{"id":"reply","type":"message","content":[{"type":"thinking","thinking":"consider","signature":"signed-data"},{"type":"redacted_thinking","data":"opaque-data"},{"type":"tool_use","id":"call","name":"clock","input":{}}],"stop_reason":"tool_use"}`
				if stream && calls == 1 {
					data = ""
					events := []any{
						map[string]any{"type": "message_start", "message": map[string]any{"id": "reply"}},
						map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "thinking", "thinking": ""}},
						map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "thinking_delta", "thinking": "consider"}},
						map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "signature_delta", "signature": "signed-data"}},
						map[string]any{"type": "content_block_stop", "index": 0},
						map[string]any{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "redacted_thinking", "data": "opaque-data"}},
						map[string]any{"type": "content_block_stop", "index": 1},
						map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use"}}, map[string]any{"type": "message_stop"},
					}
					for _, e := range events {
						raw, _ := json.Marshal(e)
						data += "data: " + string(raw) + "\n\n"
					}
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
			})
			send := func(payload map[string]any) *httptest.ResponseRecorder {
				raw, _ := json.Marshal(payload)
				r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(raw)))
				r.Header.Set("Authorization", "Bearer client")
				w := httptest.NewRecorder()
				app.ServeHTTP(w, r)
				if w.Code != 200 {
					t.Fatalf("%d %s", w.Code, w.Body.String())
				}
				return w
			}
			user := map[string]any{"role": "user", "content": "time"}
			payload := map[string]any{"model": "opencode/claude-sonnet-4-6", "stream": stream, "messages": []any{user}}
			w := send(payload)
			assistant := map[string]any{}
			if stream {
				details := []any{}
				for _, line := range strings.Split(w.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data: {") {
						continue
					}
					var chunk struct {
						Choices []struct {
							Delta map[string]any `json:"delta"`
						} `json:"choices"`
					}
					json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk)
					if len(chunk.Choices) > 0 {
						if d, ok := chunk.Choices[0].Delta["reasoning_details"].([]any); ok {
							details = append(details, d...)
						}
					}
				}
				assistant = map[string]any{"role": "assistant", "reasoning_details": details, "tool_calls": []any{map[string]any{"id": "call", "function": map[string]any{"name": "clock", "arguments": "{}"}}}}
			} else {
				var full struct {
					Choices []struct {
						Message map[string]any `json:"message"`
					} `json:"choices"`
				}
				json.Unmarshal(w.Body.Bytes(), &full)
				assistant = full.Choices[0].Message
			}
			payload["stream"] = false
			payload["messages"] = []any{user, assistant, map[string]any{"role": "tool", "tool_call_id": "call", "content": "noon"}}
			send(payload)
		})
	}
}

func TestAnthropicThinkingUsesModelCapability(t *testing.T) {
	for _, tc := range []struct {
		model, effort, thinking string
		status                  int
	}{
		{"claude-opus-4-5", "high", "enabled", 200}, {"claude-sonnet-4-6", "high", "adaptive", 200}, {"claude-3-5-haiku", "high", "", 400},
	} {
		t.Run(tc.model, func(t *testing.T) {
			cfg := Defaults()
			cfg.APIKey = "client"
			cfg.Backends = []Backend{{ID: "opencode", Kind: "builtin", URL: builtinBase("opencode"), Auth: "none", Enabled: true}}
			app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
			sent := false
			app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "models.opencode.ai" {
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"opencode":{"npm":"@ai-sdk/anthropic","models":{"claude-3-5-haiku":{}}}}`))}, nil
				}
				sent = true
				var b map[string]any
				json.NewDecoder(r.Body).Decode(&b)
				thinking, _ := b["thinking"].(map[string]any)
				if thinking["type"] != tc.thinking {
					t.Errorf("wrong thinking mode: %v", thinking)
				}
				if tc.thinking == "enabled" {
					budget, _ := thinking["budget_tokens"].(float64)
					max, _ := b["max_tokens"].(float64)
					if budget < 1024 || budget >= max {
						t.Errorf("invalid budget: %v / %v", budget, max)
					}
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"r","type":"message","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))}, nil
			})
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"opencode/`+tc.model+`","reasoning_effort":"`+tc.effort+`","messages":[{"role":"user","content":"hi"}]}`))
			r.Header.Set("Authorization", "Bearer client")
			w := httptest.NewRecorder()
			app.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if tc.status == 400 && sent {
				t.Error("unsupported thinking reached provider")
			}
		})
	}
}
