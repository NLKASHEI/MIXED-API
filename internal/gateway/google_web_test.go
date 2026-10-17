package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NLKASHEI/MIXED-API/internal/googleweb"
)

type fixtureGoogleBrowser struct {
	send func(context.Context, string, googleweb.Operation) (*http.Response, error)
}

func TestBuildUsesUpstreamUnaryCompatibilityWithinSameChannel(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       int
		body         string
		subscription bool
		wantCalls    int
	}{
		{"unary unsupported", 405, "unsupported", false, 2},
		{"unary unimplemented", 501, "unsupported", false, 2},
		{"RPC unimplemented", 400, `[12,"Method is not implemented"]`, false, 2},
		{"wrapped RPC unimplemented", 500, `[null,[12,"Method is not implemented"]]`, false, 2},
		{"explicit unary error", 400, `[3,"This model does not support unary calls"]`, false, 2},
		{"explicit stream error", 400, `[[],[3,"Only streamed calls are supported"]]`, false, 2},
		{"ordinary invalid argument", 400, `[3,"Invalid temperature"]`, false, 1},
		{"unstructured error", 400, "unary unsupported", false, 1},
		{"generation already started", 400, `[[["partial"]],[12,"Not implemented"]]`, false, 1},
		{"oversized error", 400, `[12,"` + strings.Repeat("x", 64<<10) + `"]`, false, 1},
		{"subscription stays unary", 405, "unsupported", true, 1},
		{"permission denied", 403, `[12,"private-provider-secret"]`, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.APIKey = "client"
			cfg.Backends = []Backend{{ID: "aistudio", Kind: "builtin", URL: builtinBase("aistudio"), Enabled: true}}
			app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
			app.googleBuildUnary = map[string]bool{"gemini-test": tc.subscription}
			calls := 0
			app.SetGoogleSessions(&fixtureGoogleBrowser{send: func(ctx context.Context, id string, op googleweb.Operation) (*http.Response, error) {
				calls++
				if id != "aistudio" || op.Kind != "build" || op.Model != "gemini-test" {
					t.Fatal("Build request switched model or channel")
				}
				if calls == 1 {
					if !op.Unary {
						t.Fatal("first request was not unary")
					}
					return &http.Response{StatusCode: tc.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
				}
				if op.Unary {
					t.Fatal("compatibility request still uses unary")
				}
				chunk := `{"candidates":[{"content":{"parts":[{"text":"answer"}]},"finishReason":"STOP"}]}`
				data, _ := json.Marshal([]any{[]any{[]any{chunk}}})
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
			}})
			w := accountTestCall(app, "POST", "/v1/chat/completions", "client", `{"model":"aistudio/build/gemini-test","messages":[{"role":"user","content":"hello"}]}`)
			if calls != tc.wantCalls || tc.wantCalls == 2 && (w.Code != 200 || !strings.Contains(w.Body.String(), `"content":"answer"`)) || tc.wantCalls == 1 && w.Code != tc.status {
				t.Fatalf("Build compatibility: calls=%d status=%d body=%s", calls, w.Code, w.Body.String())
			}
		})
	}
}

func (*fixtureGoogleBrowser) Open(context.Context, string) error { return nil }
func (*fixtureGoogleBrowser) State(context.Context, string) googleweb.State {
	return googleweb.State{Status: "ready"}
}
func (b *fixtureGoogleBrowser) Send(ctx context.Context, id string, op googleweb.Operation) (*http.Response, error) {
	return b.send(ctx, id, op)
}
func (*fixtureGoogleBrowser) Close() error { return nil }

type initializingGoogleBrowser struct {
	fixtureGoogleBrowser
	initialized int
}

func (b *initializingGoogleBrowser) Initialize(context.Context, string) googleweb.State {
	b.initialized++
	return googleweb.State{Status: "waiting", Message: "官方页面未就绪"}
}
func TestGoogleInitializationUsesAdminBoundaryAndPreservesWaiting(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.AdminKey = "admin"
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	b := &initializingGoogleBrowser{}
	app.SetGoogleSessions(b)
	for _, key := range []string{"client", "admin"} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8765/api/accounts/aistudio/initialize", nil)
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if key == "client" {
			if w.Code == 200 || b.initialized != 0 {
				t.Fatal("client key initialized account")
			}
			continue
		}
		if w.Code != 200 || b.initialized != 1 || !strings.Contains(w.Body.String(), `"status":"waiting"`) {
			t.Fatalf("initialization: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestGoogleWebIntegratedChat(t *testing.T) {
	for _, tc := range []struct{ id, model, kind string }{{"aps", "gemini-2.5-flash", "aps"}, {"aistudio", "build/gemini-2.5-flash", "build"}, {"aistudio", "playground/gemini-2.5-flash", "playground"}} {
		t.Run(tc.kind, func(t *testing.T) {
			for _, stream := range []bool{false, true} {
				cfg := Defaults()
				cfg.APIKey = "client"
				cfg.AdminKey = "admin"
				cfg.Backends = []Backend{{ID: tc.id, Kind: "builtin", URL: builtinBase(tc.id), Auth: "none", Enabled: true}}
				app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
				app.googleBuildUnary = map[string]bool{"gemini-2.5-flash": false}
				app.SetGoogleSessions(&fixtureGoogleBrowser{send: func(ctx context.Context, id string, op googleweb.Operation) (*http.Response, error) {
					if id != tc.id || op.Kind != tc.kind || op.Model != "gemini-2.5-flash" {
						t.Fatalf("wrong operation: %s %+v", id, op)
					}
					var body map[string]any
					if tc.kind == "playground" {
						var request []any
						if json.Unmarshal(op.Body, &request) != nil || len(request) < 11 || request[0] != "models/gemini-2.5-flash" || op.Binding == "" {
							t.Fatal("invalid Playground protocol")
						}
					} else if json.Unmarshal(op.Body, &body) != nil || body["contents"] == nil {
						t.Fatal("missing native Gemini request")
					}
					chunk := `{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}}`
					data := `[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":` + chunk + `}}}]}]`
					if tc.kind == "build" {
						raw, _ := json.Marshal(chunk)
						data = `[[[` + string(raw) + `]]]`
						if op.Unary {
							data = `[` + string(raw) + `]`
						}
					}
					if tc.kind == "playground" {
						data = `[[[[[[[[null,"hello"]],"model"],1]],null,[2,1,3]]]]`
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
				}})
				payload := map[string]any{"model": tc.id + "/" + tc.model, "messages": []any{map[string]any{"role": "user", "content": "hi"}}, "stream": stream}
				payload["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "answer", "schema": map[string]any{"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "string"}}, "required": []string{"answer"}}}}
				raw, _ := json.Marshal(payload)
				r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(raw)))
				r.Header.Set("Authorization", "Bearer client")
				w := httptest.NewRecorder()
				app.ServeHTTP(w, r)
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"content":"hello"`) {
					t.Fatalf("%d %s", w.Code, w.Body.String())
				}
				if stream && !strings.Contains(w.Body.String(), "data: [DONE]") {
					t.Fatal("missing completed stream")
				}
			}
		})
	}
}

func TestGoogleWebRejectsTruncatedAndOversizeOutput(t *testing.T) {
	for _, tc := range []struct{ name, data string }{
		{"truncated", `[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"index":0,"content":{"parts":[{"text":"partial"}]},"finishReason":"FINISH_REASON_UNSPECIFIED"}]}}}}]}]`},
		{"oversize", strings.Repeat(`{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"index":0,"content":{"parts":[{"text":"`+strings.Repeat("a", 1<<20)+`"}]},"finishReason":"STOP"}]}}}}]}`, 33)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.APIKey = "client"
			cfg.Backends = []Backend{{ID: "aps", Kind: "builtin", URL: builtinBase("aps"), Auth: "none", Enabled: true}}
			app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
			app.SetGoogleSessions(&fixtureGoogleBrowser{send: func(context.Context, string, googleweb.Operation) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tc.data))}, nil
			}})
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"aps/gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`))
			r.Header.Set("Authorization", "Bearer client")
			w := httptest.NewRecorder()
			app.ServeHTTP(w, r)
			if w.Code != 502 {
				t.Fatalf("%d %s", w.Code, w.Body.String()[:min(w.Body.Len(), 100)])
			}
		})
	}
}

func TestGoogleWebRequiresEveryRequestedCandidate(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.Backends = []Backend{{ID: "aps", Kind: "builtin", URL: builtinBase("aps"), Auth: "none", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	app.SetGoogleSessions(&fixtureGoogleBrowser{send: func(context.Context, string, googleweb.Operation) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"index":0,"content":{"parts":[{"text":"only one"}]},"finishReason":"STOP"}]}}}}]}]`))}, nil
	}})
	server := httptest.NewServer(app)
	defer server.Close()
	r, _ := http.NewRequest("POST", server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"aps/gemini-test","n":2,"messages":[{"role":"user","content":"hi"}]}`))
	r.Header.Set("Authorization", "Bearer client")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 502 {
		t.Fatalf("missing candidate reported as %d", resp.StatusCode)
	}
}

func TestGoogleWebRejectsMalformedOptionsBeforeSending(t *testing.T) {
	for _, extra := range []string{`"n":1.5`, `"stream":"false"`, `"unknown_option":true`, `"temperature":-1`, `"stream_options":{"include_usage":"yes"}`} {
		cfg := Defaults()
		cfg.APIKey = "client"
		cfg.Backends = []Backend{{ID: "aps", Kind: "builtin", URL: builtinBase("aps"), Auth: "none", Enabled: true}}
		app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
		called := false
		app.SetGoogleSessions(&fixtureGoogleBrowser{send: func(context.Context, string, googleweb.Operation) (*http.Response, error) {
			called = true
			return &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
		}})
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"aps/gemini-test","messages":[{"role":"user","content":"hi"}],`+extra+`}`))
		r.Header.Set("Authorization", "Bearer client")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != 400 || called {
			t.Fatalf("%s: status %d, provider called %v", extra, w.Code, called)
		}
	}
}

func TestGoogleWebDoesNotCompleteRejectedStream(t *testing.T) {
	frame := `{"candidates":[{"index":0,"content":{"parts":[{"text":"partial"}]},"finishReason":"STOP"}]}`
	encoded, _ := json.Marshal(frame)
	for _, tc := range []struct{ kind, data string }{
		{"build", `[[[` + string(encoded) + `]],[7,"private-provider-secret"]]`},
		{"build", `[[[` + string(encoded) + `]]`},
		{"aps", `[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":` + frame + `}}}]}`},
		{"aps", `[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":` + frame + `}}}]}]garbage`},
	} {
		for _, streaming := range []bool{false, true} {
			t.Run(tc.kind+"/"+strconv.FormatBool(streaming), func(t *testing.T) {
				id, model := "aistudio", "build/gemini-2.5-flash"
				if tc.kind == "aps" {
					id, model = "aps", "gemini-2.5-flash"
				}
				cfg := Defaults()
				cfg.APIKey = "client"
				cfg.Backends = []Backend{{ID: id, Kind: "builtin", URL: builtinBase(id), Auth: "none", Enabled: true}}
				app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
				app.googleBuildUnary = map[string]bool{"gemini-2.5-flash": false}
				app.SetGoogleSessions(&fixtureGoogleBrowser{send: func(context.Context, string, googleweb.Operation) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tc.data))}, nil
				}})
				r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"`+id+`/`+model+`","stream":`+strconv.FormatBool(streaming)+`,"messages":[{"role":"user","content":"hi"}]}`))
				r.Header.Set("Authorization", "Bearer client")
				w := httptest.NewRecorder()
				app.ServeHTTP(w, r)
				if !strings.Contains(w.Body.String(), `"error"`) || strings.Contains(w.Body.String(), "[DONE]") || strings.Contains(w.Body.String(), "private-provider-secret") {
					t.Fatalf("invalid failure: %d %.300s", w.Code, w.Body.String())
				}
			})
		}
	}
}

func TestGoogleWebClientCancellationStopsProvider(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.Backends = []Backend{{ID: "aps", Kind: "builtin", URL: builtinBase("aps"), Auth: "none", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	stopped := make(chan struct{})
	app.SetGoogleSessions(&fixtureGoogleBrowser{send: func(ctx context.Context, _ string, _ googleweb.Operation) (*http.Response, error) {
		r, w := io.Pipe()
		frame := `[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"content":{"parts":[{"text":"first"}]}}]}}}}]}]`
		if !json.Valid([]byte(frame)) {
			t.Fatal("invalid fixture")
		}
		go func() { defer close(stopped); defer w.Close(); w.Write([]byte(frame)); <-ctx.Done() }()
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: r}, nil
	}})
	srv := httptest.NewServer(app)
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "POST", srv.URL+"/v1/chat/completions", strings.NewReader(`{"model":"aps/gemini-2.5-flash","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	r.Header.Set("Authorization", "Bearer client")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buffer := make([]byte, 128)
	if _, err = resp.Body.Read(buffer); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("provider survived client cancellation")
	}
}

func TestAISSeparatesBuildAndPlaygroundCatalogs(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.Backends = []Backend{{ID: "aistudio", Kind: "builtin", URL: builtinBase("aistudio"), Auth: "none", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	app.SetGoogleSessions(&fixtureGoogleBrowser{send: func(_ context.Context, _ string, op googleweb.Operation) (*http.Response, error) {
		data := `[[["models/playground-only",null,null,null,null,null,null,["generateContent"]]]]`
		if op.Kind == "build_models" {
			body, _ := json.Marshal(`{"models":[{"name":"models/build-only","supportedGenerationMethods":["generateContent"]}]}`)
			data = `[` + string(body) + `]`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
	}})
	r := httptest.NewRequest("GET", "/v1/models", nil)
	r.Header.Set("Authorization", "Bearer client")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	want := map[string]bool{"aistudio/build/build-only": true, "aistudio/playground/playground-only": true}
	if w.Code != 200 || len(result.Data) != 2 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	for _, m := range result.Data {
		if !want[m.ID] {
			t.Fatalf("advertised wrong capability: %s", m.ID)
		}
	}
}
