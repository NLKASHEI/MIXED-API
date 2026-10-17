package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func advancedBridge(t *testing.T, handler func(*http.Request) string) *Server {
	t.Helper()
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.Backends = []Backend{{ID: "opencode", Kind: "builtin", URL: builtinBase("opencode"), Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(handler(r)))}, nil
	})
	return app
}

func TestChatReplaysNativeResponsesReasoningItems(t *testing.T) {
	calls := 0
	app := advancedBridge(t, func(r *http.Request) string {
		calls++
		if calls == 2 {
			raw, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(raw), `"encrypted_content":"opaque-fixture"`) {
				t.Error("native reasoning lost on replay")
			}
		}
		return `{"id":"r1","status":"completed","output":[{"id":"rs1","type":"reasoning","summary":[{"type":"summary_text","text":"plan"}],"encrypted_content":"opaque-fixture"},{"type":"message","content":[{"type":"output_text","text":"answer"}]}]}`
	})
	w := accountTestCall(app, "POST", "/v1/chat/completions", "client", `{"model":"opencode/gpt-5.4","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "opaque-fixture") {
		t.Fatalf("reasoning lost: %d %s", w.Code, w.Body.String())
	}
	var reply map[string]any
	json.Unmarshal(w.Body.Bytes(), &reply)
	message := reply["choices"].([]any)[0].(map[string]any)["message"]
	raw, _ := json.Marshal(map[string]any{"model": "opencode/gpt-5.4", "messages": []any{map[string]any{"role": "user", "content": "hi"}, message, map[string]any{"role": "user", "content": "continue"}}})
	w = accountTestCall(app, "POST", "/v1/chat/completions", "client", string(raw))
	if w.Code != 200 || calls != 2 {
		t.Fatal("replay failed", w.Code)
	}
}

func TestResponsesStreamPreservesOpaqueReasoningOnce(t *testing.T) {
	app := advancedBridge(t, func(r *http.Request) string {
		return "data: " + `{"type":"response.output_item.done","output_index":0,"item":{"id":"rs1","type":"reasoning","summary":[],"encrypted_content":"opaque-fixture"}}` + "\n\ndata: " + `{"type":"response.completed","response":{"id":"r1","status":"completed","output":[{"id":"rs1","type":"reasoning","summary":[],"encrypted_content":"opaque-fixture"}]}}` + "\n\n"
	})
	w := accountTestCall(app, "POST", "/v1/chat/completions", "client", `{"model":"opencode/gpt-5.4","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	if w.Code != 200 || strings.Count(w.Body.String(), "opaque-fixture") != 1 || !strings.Contains(w.Body.String(), "[DONE]") {
		t.Fatal("opaque stream state lost or duplicated", w.Body.String())
	}
}

func TestResponsesCanReplaySignedReasoningAndCitations(t *testing.T) {
	calls := 0
	app := advancedBridge(t, func(r *http.Request) string {
		calls++
		if calls == 2 {
			raw, _ := io.ReadAll(r.Body)
			for _, want := range []string{"reasoning_content", "signature-private", "cipher-private", "url_citation", "tool_call_id"} {
				if !strings.Contains(string(raw), want) {
					t.Errorf("replay lost %s", want)
				}
			}
		}
		return `{"choices":[{"finish_reason":"tool_calls","message":{"content":"answer","reasoning_content":"plan","reasoning_details":[{"type":"reasoning.text","text":"plan","signature":"signature-private","format":"anthropic-claude-v1"},{"type":"reasoning.encrypted","data":"cipher-private","format":"anthropic-claude-v1"}],"annotations":[{"type":"url_citation","url_citation":{"url":"https://example.com/","title":"source","start_index":0,"end_index":6}}],"tool_calls":[{"id":"c1","function":{"name":"weather","arguments":"{}"}}]}}]}`
	})
	w := accountTestCall(app, "POST", "/v1/responses", "client", `{"model":"opencode/deepseek-v4-flash","input":"hi"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	for _, want := range []string{"signature-private", "cipher-private", "url_citation"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("output lost %s", want)
		}
	}
	input := append(response["output"].([]any), map[string]any{"type": "function_call_output", "call_id": "c1", "output": "sunny"})
	body, _ := json.Marshal(map[string]any{"model": "opencode/deepseek-v4-flash", "input": input})
	w = accountTestCall(app, "POST", "/v1/responses", "client", string(body))
	if w.Code != 200 || calls != 2 {
		t.Fatalf("replay failed: %d %s", w.Code, w.Body.String())
	}
}

func TestBridgeRejectsUnrepresentableOptionsBeforeInference(t *testing.T) {
	app := advancedBridge(t, func(r *http.Request) string { t.Error("unsupported option reached inference"); return `{}` })
	for _, test := range []struct{ path, body string }{
		{"/v1/responses", `{"model":"opencode/deepseek-v4-flash","input":"hi","reasoning":{"effort":"high","summary":"detailed"}}`},
		{"/v1/responses", `{"model":"opencode/deepseek-v4-flash","input":"hi","text":{"verbosity":"low"}}`},
		{"/v1/messages", `{"model":"opencode/deepseek-v4-flash","messages":[{"role":"user","content":"hi"}],"metadata":{"user_id":"user"}}`},
	} {
		w := accountTestCall(app, "POST", test.path, "client", test.body)
		if w.Code != 400 {
			t.Errorf("unsupported option: %d", w.Code)
		}
	}
}

func TestBridgeDoesNotDropAudioIntoEmptyTextSuccess(t *testing.T) {
	app := advancedBridge(t, func(r *http.Request) string {
		return `{"choices":[{"finish_reason":"stop","message":{"content":"","media":[{"mime_type":"audio/L16;codec=pcm;rate=24000","data":"AAABAA=="}]}}]}`
	})
	w := accountTestCall(app, "POST", "/v1/responses", "client", `{"model":"opencode/deepseek-v4-flash","input":"hi"}`)
	if w.Code != 502 {
		t.Fatalf("unrepresentable output silently lost: %d %s", w.Code, w.Body.String())
	}
}

func TestBridgePreservesSignedThinkingAndRefusal(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/messages"} {
		for _, stream := range []bool{false, true} {
			t.Run(path+map[bool]string{false: "/full", true: "/stream"}[stream], func(t *testing.T) {
				app := advancedBridge(t, func(r *http.Request) string {
					if !stream {
						return `{"choices":[{"finish_reason":"stop","message":{"content":"answer","refusal":"cannot comply","reasoning_content":"firstsecond","reasoning_details":[{"type":"reasoning.text","text":"first","signature":"sign-one","format":"anthropic-claude-v1"},{"type":"reasoning.text","text":"second","signature":"sign-two","format":"anthropic-claude-v1"},{"type":"reasoning.encrypted","data":"cipher-private","format":"anthropic-claude-v1"}]}}]}`
					}
					deltas := []string{`{"reasoning_content":"first"}`, `{"reasoning_details":[{"type":"reasoning.text","text":"first","signature":"sign-one","format":"anthropic-claude-v1"}]}`, `{"reasoning_content":"second"}`, `{"reasoning_details":[{"type":"reasoning.text","text":"second","signature":"sign-two","format":"anthropic-claude-v1"},{"type":"reasoning.encrypted","data":"cipher-private","format":"anthropic-claude-v1"}]}`, `{"content":"answer","refusal":"cannot comply"}`}
					var result strings.Builder
					for _, d := range deltas {
						result.WriteString("data: {\"choices\":[{\"index\":0,\"delta\":" + d + "}]}\n\n")
					}
					result.WriteString("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					return result.String()
				})
				input := map[string]any{"model": "opencode/deepseek-v4-flash", "stream": stream}
				if path == "/v1/responses" {
					input["input"] = "hi"
				} else {
					input["messages"] = []any{map[string]any{"role": "user", "content": "hi"}}
				}
				raw, _ := json.Marshal(input)
				w := accountTestCall(app, "POST", path, "client", string(raw))
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				for _, want := range []string{"sign-one", "sign-two", "cipher-private", "cannot comply"} {
					if !strings.Contains(w.Body.String(), want) {
						t.Errorf("lost %s: %s", want, w.Body.String())
					}
				}
				if stream {
					done := "response.completed"
					if path == "/v1/messages" {
						done = "message_stop"
					}
					if !strings.Contains(w.Body.String(), done) {
						t.Error("missing completion")
					}
				}
			})
		}
	}
}
