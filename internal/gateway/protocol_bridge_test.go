package gateway

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResponsesClientUsesExpressToolsAndUsage(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.Backends = []Backend{{ID: "vertex", Kind: "builtin", URL: builtinBase("vertex"), ProviderKey: "key", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	calls := 0
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		raw, _ := json.Marshal(body)
		for _, want := range []string{`"systemInstruction"`, `"text":"Be brief"`, `"functionCall":{"args":{"city":"Paris"},"name":"weather"}`, `"functionResponse":{"name":"weather","response":{"temp":20}}`, `"maxOutputTokens":128`} {
			if !strings.Contains(string(raw), want) {
				t.Errorf("missing %s: %s", want, raw)
			}
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"20 degrees"},{"functionCall":{"name":"weather","args":{"city":"Rome"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":3,"totalTokenCount":11}}`))}, nil
	})
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"vertex/gemini-3-flash-preview","instructions":"Be brief","input":[{"role":"user","content":[{"type":"input_text","text":"weather?"}]},{"type":"function_call","call_id":"call_1","name":"weather","arguments":"{\"city\":\"Paris\"}"},{"type":"function_call_output","call_id":"call_1","output":"{\"temp\":20}"}],"tools":[{"type":"function","name":"weather","parameters":{"type":"object"}}],"max_output_tokens":128}`))
	r.Header.Set("Authorization", "Bearer client")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	for _, want := range []string{`"object":"response"`, `"status":"completed"`, `"type":"output_text"`, `"text":"20 degrees"`, `"type":"function_call"`, `"name":"weather"`, `"input_tokens":8`, `"output_tokens":3`, `"model":"vertex/gemini-3-flash-preview"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("missing %s: %d %s", want, w.Code, w.Body.String())
		}
	}
	if w.Code != 200 || calls != 1 {
		t.Fatalf("status=%d calls=%d", w.Code, calls)
	}
}

func TestResponsesStreamDeliversBeforeCompletionAndCancelsProvider(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.Backends = []Backend{{ID: "vertex", Kind: "builtin", URL: builtinBase("vertex"), ProviderKey: "key", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	cancelled := make(chan struct{})
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		reader, writer := io.Pipe()
		go func() {
			defer writer.Close()
			io.WriteString(writer, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"first\"}]}}]}\n\n")
			<-r.Context().Done()
			close(cancelled)
		}()
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: reader}, nil
	})
	server := httptest.NewServer(app)
	defer server.Close()
	r, _ := http.NewRequest("POST", server.URL+"/v1/responses", strings.NewReader(`{"model":"vertex/gemini-3-flash-preview","input":"hi","stream":true}`))
	r.Header.Set("Authorization", "Bearer client")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(resp.Body)
	found := false
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), `"type":"response.output_text.delta"`) && strings.Contains(scanner.Text(), `"delta":"first"`) {
			found = true
			break
		}
	}
	resp.Body.Close()
	if !found {
		t.Fatalf("no incremental text, status=%d", resp.StatusCode)
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("disconnect did not cancel provider")
	}
}

func TestMessagesClientUsesOpenCodeChatWithTools(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.Backends = []Backend{{ID: "opencode", Kind: "builtin", URL: builtinBase("opencode"), Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	calls := 0
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/inference/openai/v1/chat/completions" {
			t.Errorf("wrong protocol: %s", r.URL.Path)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		raw, _ := json.Marshal(body)
		for _, want := range []string{`"role":"system"`, `"content":"Be brief"`, `"tool_call_id":"c1"`, `"arguments":"{\"city\":\"Paris\"}"`, `"max_tokens":128`, `"type":"function"`} {
			if !strings.Contains(string(raw), want) {
				t.Errorf("missing %s: %s", want, raw)
			}
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"chat1","choices":[{"message":{"role":"assistant","content":"Sunny","tool_calls":[{"id":"c2","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Rome\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11}}`))}, nil
	})
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"opencode/deepseek-v4-flash","system":"Be brief","max_tokens":128,"messages":[{"role":"user","content":"weather?"},{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"weather","input":{"city":"Paris"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":"20"}]}],"tools":[{"name":"weather","input_schema":{"type":"object"}}]}`))
	r.Header.Set("Authorization", "Bearer client")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	for _, want := range []string{`"type":"message"`, `"type":"tool_use"`, `"stop_reason":"tool_use"`, `"input_tokens":8`, `"output_tokens":3`, `"text":"Sunny"`, `"city":"Rome"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("missing %s: %d %s", want, w.Code, w.Body.String())
		}
	}
	if w.Code != 200 || calls != 1 {
		t.Fatalf("status=%d calls=%d", w.Code, calls)
	}
}

func TestMessagesStreamKeepsToolDeltasAndRejectsTruncatedArguments(t *testing.T) {
	for _, args := range []string{`{"city":"Rome"}`, `{"city":`} {
		t.Run(args, func(t *testing.T) {
			cfg := Defaults()
			cfg.APIKey = "client"
			cfg.Backends = []Backend{{ID: "opencode", Kind: "builtin", URL: builtinBase("opencode"), Enabled: true}}
			app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
			app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if body["stream"] != true {
					t.Error("not streaming")
				}
				encoded, _ := json.Marshal(args)
				data := "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call1\",\"function\":{\"name\":\"weather\",\"arguments\":\"\"}}]}}]}\n\n" +
					"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":" + string(encoded) + "}}]}}]}\n\n" +
					"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":4,\"total_tokens\":13}}\n\ndata: [DONE]\n\n"
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
			})
			r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"opencode/deepseek-v4-flash","messages":[{"role":"user","content":"weather?"}],"max_tokens":128,"stream":true}`))
			r.Header.Set("Authorization", "Bearer client")
			w := httptest.NewRecorder()
			app.ServeHTTP(w, r)
			text := w.Body.String()
			wanted := []string{"event: message_start"}
			if json.Valid([]byte(args)) {
				wanted = append(wanted, "event: content_block_start", `"type":"tool_use"`, `"type":"input_json_delta"`)
			}
			for _, want := range wanted {
				if !strings.Contains(text, want) {
					t.Errorf("missing %s: %s", want, text)
				}
			}
			if json.Valid([]byte(args)) {
				if !strings.Contains(text, "event: message_stop") || !strings.Contains(text, `"output_tokens":4`) || !strings.Contains(text, `"stop_reason":"tool_use"`) {
					t.Fatal(text)
				}
			} else {
				if strings.Contains(text, "event: message_stop") || !strings.Contains(text, "event: error") {
					t.Fatal("truncated tool marked complete", text)
				}
			}
		})
	}
}

func TestResponsesPreservesLengthAndRejectsUnknownFinish(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, finish := range []string{"length", "provider_error"} {
			t.Run(fmt.Sprintf("%v/%s", stream, finish), func(t *testing.T) {
				cfg := Defaults()
				cfg.APIKey = "client"
				cfg.Backends = []Backend{{ID: "opencode", Kind: "builtin", URL: builtinBase("opencode"), Enabled: true}}
				app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
				app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					var packet map[string]any
					json.NewDecoder(r.Body).Decode(&packet)
					message := map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call1", "type": "function", "function": map[string]any{"name": "weather", "arguments": "{\"city\":"}}}}
					if finish == "provider_error" {
						message["tool_calls"].([]any)[0].(map[string]any)["function"].(map[string]any)["arguments"] = "{}"
					}
					field := "message"
					if stream {
						field = "delta"
					}
					raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, field: message, "finish_reason": finish}}})
					data := string(raw)
					if stream {
						data = "data: " + data + "\n\ndata: [DONE]\n\n"
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
				})
				body, _ := json.Marshal(map[string]any{"model": "opencode/deepseek-v4-flash", "input": "hi", "stream": stream})
				r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(string(body)))
				r.Header.Set("Authorization", "Bearer client")
				w := httptest.NewRecorder()
				app.ServeHTTP(w, r)
				if finish == "length" {
					if !strings.Contains(w.Body.String(), `"status":"incomplete"`) || !strings.Contains(w.Body.String(), `"reason":"max_output_tokens"`) {
						t.Fatal(w.Code, w.Body.String())
					}
				} else {
					if strings.Contains(w.Body.String(), `"status":"completed"`) || (stream && !strings.Contains(w.Body.String(), "response.failed")) || (!stream && w.Code != 502) {
						t.Fatal(w.Code, w.Body.String())
					}
				}
			})
		}
	}
}

func TestResponsesGeminiToolSignatureSurvivesRoundTrip(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.Backends = []Backend{{ID: "vertex", Kind: "builtin", URL: builtinBase("vertex"), ProviderKey: "key", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	calls := 0
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if calls == 2 {
			raw, _ := json.Marshal(body)
			if !strings.Contains(string(raw), `"thoughtSignature":"opaque-signature"`) {
				t.Errorf("signature lost: %s", raw)
			}
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"thoughtSignature":"opaque-signature","functionCall":{"name":"weather","args":{"city":"Paris"}}}]},"finishReason":"STOP"}]}`))}, nil
	})
	call := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer client")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w
	}
	w := call(`{"model":"vertex/gemini-3-flash-preview","input":"weather?"}`)
	var result map[string]any
	json.Unmarshal(w.Body.Bytes(), &result)
	output, ok := result["output"].([]any)
	if !ok || len(output) != 1 {
		t.Fatal(w.Body.String())
	}
	tool := output[0].(map[string]any)
	if tool["thought_signature"] != "opaque-signature" {
		t.Errorf("signature absent from tool: %s", w.Body.String())
	}
	input := []any{map[string]any{"role": "user", "content": "weather?"}, tool, map[string]any{"type": "function_call_output", "call_id": tool["call_id"], "output": "20"}}
	body, _ := json.Marshal(map[string]any{"model": "vertex/gemini-3-flash-preview", "input": input})
	if next := call(string(body)); next.Code != 200 {
		t.Fatal(next.Body.String())
	}
}

func TestResponsesStreamAcceptsFragmentedToolNames(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.Backends = []Backend{{ID: "opencode", Kind: "builtin", URL: builtinBase("opencode"), Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		data := "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"get_\",\"arguments\":\"\"}}]}}]}\n\n" +
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"weather\",\"arguments\":\"{}\"},\"thought_signature\":\"late-signature\"}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
	})
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"opencode/deepseek-v4-flash","input":"weather?","stream":true}`))
	r.Header.Set("Authorization", "Bearer client")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	for _, want := range []string{`"name":"get_weather"`, `"thought_signature":"late-signature"`, "event: response.completed"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("missing %s: %s", want, w.Body.String())
		}
	}
}

func TestChatRejectsMalformedResponsesToolCompletion(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.Backends = []Backend{{ID: "opencode", Kind: "builtin", URL: builtinBase("opencode"), Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		data := "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc1\",\"call_id\":\"call1\",\"name\":\"weather\",\"arguments\":\"\"}}\n\n" +
			"data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":0,\"delta\":\"{\\\"city\\\":\"}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
	})
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"opencode/gpt-5.4","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	r.Header.Set("Authorization", "Bearer client")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), "[DONE]") || strings.Contains(w.Body.String(), `"finish_reason":"tool_calls"`) || !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatal(w.Body.String())
	}
}

func TestNonStreamingMessagesDoesNotInventTruncatedToolInput(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.Backends = []Backend{{ID: "opencode", Kind: "builtin", URL: builtinBase("opencode"), Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"tool_calls":[{"id":"call1","function":{"name":"weather","arguments":"{\"city\":"}}]},"finish_reason":"length"}]}`))}, nil
	})
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"opencode/deepseek-v4-flash","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`))
	r.Header.Set("Authorization", "Bearer client")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 502 || strings.Contains(w.Body.String(), `"input":null`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
