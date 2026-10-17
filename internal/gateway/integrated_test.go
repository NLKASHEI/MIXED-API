package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestIntegratedExpressConvertsChatWithoutExternalRuntime(t *testing.T) {
	var calls int
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/publishers/google/models/gemini-3-flash-preview:generateContent" {
			t.Errorf("wrong official protocol path: %s", r.URL.Path)
		}
		if r.Header.Get("x-goog-api-key") != "express-secret" || r.Header.Get("Authorization") != "" || r.URL.RawQuery != "" {
			t.Error("Express authentication leaked into another credential channel")
		}
		var body struct {
			Contents []struct {
				Role  string `json:"role"`
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"contents"`
			System struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"systemInstruction"`
			Generation struct {
				Temperature float64 `json:"temperature"`
			} `json:"generationConfig"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Contents) != 1 || body.Contents[0].Role != "user" || body.Contents[0].Parts[0].Text != "hello" || body.System.Parts[0].Text != "Be concise" || body.Generation.Temperature != 0.4 {
			t.Errorf("chat was not translated into Gemini protocol: %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"Hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":2,"totalTokenCount":10}}`)
	}))
	defer provider.Close()
	cfg := Defaults()
	cfg.APIKey, cfg.AdminKey = "client", "admin"
	cfg.Backends = []Backend{{ID: "vertex", Name: "快速V", Kind: "builtin", URL: "https://aiplatform.googleapis.com", ProviderKey: "express-secret", Auth: "none", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "aiplatform.googleapis.com" {
			t.Fatalf("unexpected dependency/network call: %s", r.URL.Host)
		}
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host = "http", strings.TrimPrefix(provider.URL, "http://")
		return transport.RoundTrip(r)
	})
	api := httptest.NewServer(app)
	defer api.Close()
	r, _ := http.NewRequest("POST", api.URL+"/v1/chat/completions", strings.NewReader(`{"model":"vertex/gemini-3-flash-preview","messages":[{"role":"system","content":"Be concise"},{"role":"user","content":"hello"}],"temperature":0.4}`))
	r.Header.Set("Authorization", "Bearer client")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(data), `"content":"Hi"`) || !strings.Contains(string(data), `"total_tokens":10`) || !strings.Contains(string(data), `"model":"vertex/gemini-3-flash-preview"`) || calls != 1 {
		t.Fatalf("integrated inference failed: %d %s calls=%d", resp.StatusCode, data, calls)
	}
	if strings.Contains(string(data), "express-secret") {
		t.Fatal("provider key leaked")
	}
}

func TestWorkBuddyUserAuthorizationAndInferenceNeedNoCLI(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey, cfg.AdminKey = "client", "admin"
	cfg.Backends = []Backend{{ID: "workbuddy", Kind: "builtin", URL: "https://copilot.tencent.com/v2", Auth: "none", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var data string
		switch r.URL.Path {
		case "/v2/plugin/auth/state":
			data = `{"data":{"state":"flow-test","authUrl":"https://copilot.tencent.com/login?state=flow-test"}}`
		case "/v2/plugin/auth/token":
			data = `{"data":{"accessToken":"private-access","refreshToken":"private-refresh"}}`
		case "/v2/plugin/login/account":
			data = `{"data":{"uid":"user-test"}}`
		case "/v2/chat/completions":
			if r.Header.Get("Authorization") != "Bearer private-access" {
				t.Error("not using authorized account")
			}
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["stream"] != true {
				t.Error("WorkBuddy requires streaming upstream")
			}
			data = "data: {\"id\":\"chat-wb\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hi\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
		default:
			t.Errorf("unexpected external dependency %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
	})
	call := func(method, path, key, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w
	}
	if w := call("POST", "/api/accounts/workbuddy/login", "client", ""); w.Code != 401 {
		t.Fatal("client can start admin authorization")
	}
	if w := call("POST", "/api/accounts/workbuddy/login", "admin", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "copilot.tencent.com") {
		t.Fatalf("login start failed: %d %s", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		w := call("GET", "/api/accounts/workbuddy", "admin", "")
		if strings.Contains(w.Body.String(), "private-access") || strings.Contains(w.Body.String(), "private-refresh") {
			t.Fatal("account API leaked credentials")
		}
		if strings.Contains(w.Body.String(), `"status":"authorized"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("authorization did not complete: %s", w.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	w := call("POST", "/v1/chat/completions", "client", `{"model":"workbuddy/auto","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"content":"Hi"`) {
		t.Fatalf("native WorkBuddy inference failed: %d %s", w.Code, w.Body.String())
	}
}

func TestBuiltinPreparationNeedsNoDownloadsOrActivation(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey, cfg.AdminKey = "client", "admin"
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	call := func(method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer admin")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w
	}
	var saved Config
	json.Unmarshal(call("GET", "/api/config").Body.Bytes(), &saved)
	for _, id := range []string{"vertex", "opencode"} {
		found := false
		for _, b := range saved.Backends {
			if b.ID == id {
				found = true
				if b.Kind != "builtin" || b.Enabled {
					t.Fatalf("%s must ship as disabled built-in core", id)
				}
			}
		}
		if !found {
			t.Fatal("missing builtin", id)
		}
		w := call("POST", "/api/assist/"+id+"/prepare")
		if w.Code != 200 {
			t.Fatalf("offline preparation failed: %d %s", w.Code, w.Body.String())
		}
	}
	json.Unmarshal(call("GET", "/api/config").Body.Bytes(), &saved)
	for _, b := range saved.Backends {
		if b.Enabled || b.Command != "" {
			t.Fatal("prepare enabled or configured a child process")
		}
	}
}

func TestIntegratedExpressStreamsToolsAndDoesNotClaimTruncatedSuccess(t *testing.T) {
	for _, complete := range []bool{false, true} {
		cfg := Defaults()
		cfg.APIKey, cfg.AdminKey = "client", "admin"
		cfg.Backends = []Backend{{ID: "vertex", Kind: "builtin", URL: builtinBase("vertex"), Auth: "none", ProviderKey: "secret", Enabled: true}}
		app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
		app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/v1/publishers/google/models/gemini-3-flash-preview:streamGenerateContent" || r.URL.Query().Get("alt") != "sse" {
				t.Errorf("wrong stream protocol: %s", r.URL.Path)
			}
			data := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]}}]}\n\n"
			if complete {
				data += "data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"weather\",\"args\":{\"city\":\"Paris\"}},\"thoughtSignature\":\"signed-call\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":2,\"totalTokenCount\":5}}\n\n"
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(data))}, nil
		})
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"vertex/gemini-3-flash-preview","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":true}}`))
		r.Header.Set("Authorization", "Bearer client")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, `"content":"hello"`) {
			t.Fatalf("no incremental response: %d %s", w.Code, body)
		}
		if strings.Contains(body, "data: [DONE]") != complete {
			t.Fatalf("truncated stream claimed success: %s", body)
		}
		if complete && (!strings.Contains(body, `"finish_reason":"tool_calls"`) || !strings.Contains(body, `"thought_signature":"signed-call"`) || !strings.Contains(body, `"total_tokens":5`)) {
			t.Fatalf("stream lost tool or usage data: %s", body)
		}
	}
}

func TestIntegratedOpenCodeUsesNativeResponsesAndPreservesReasoning(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey, cfg.AdminKey = "client", "admin"
	cfg.Backends = []Backend{{ID: "opencode", Kind: "builtin", URL: builtinBase("opencode"), Auth: "none", ProviderKey: "zen-secret", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	var inference int
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		response := `{"opencode":{"npm":"@ai-sdk/openai-compatible","models":{"test-reasoner":{"id":"test-reasoner","provider":{"npm":"@ai-sdk/openai"}}}}}`
		if r.URL.Host == "models.opencode.ai" {
			if r.Header.Get("Authorization") != "" {
				t.Fatal("key leaked to metadata host")
			}
		} else {
			inference++
			if r.URL.String() != "https://opencode.ai/inference/openai/v1/responses" || r.Header.Get("Authorization") != "Bearer zen-secret" {
				t.Errorf("wrong native endpoint/auth")
			}
			if r.Header.Get("x-opencode-session-id") != "" {
				t.Error("must not fabricate an official client session")
			}
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["model"] != "test-reasoner" || body["seed"] != nil || body["reasoning_effort"] != nil {
				t.Errorf("invalid Responses payload: %+v", body)
			}
			if v, ok := body["reasoning"].(map[string]any); !ok || v["effort"] != "high" || v["summary"] != "auto" {
				t.Error("reasoning was lost")
			}
			if v, ok := body["input"].([]any); !ok || len(v) != 1 {
				t.Error("messages were lost")
			}
			response = `{"id":"resp-test","status":"completed","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"Thinking"}]},{"type":"message","content":[{"type":"output_text","text":"Answer"}]}],"usage":{"input_tokens":4,"output_tokens":6,"total_tokens":10}}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
	})
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"opencode/test-reasoner","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"high","seed":7}`))
	r.Header.Set("Authorization", "Bearer client")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 200 || inference != 1 || !strings.Contains(w.Body.String(), `"content":"Answer"`) || !strings.Contains(w.Body.String(), `"reasoning_content":"Thinking"`) {
		t.Fatalf("native protocol integration failed: %d %s", w.Code, w.Body.String())
	}
}

func TestIntegratedOpenCodeStreamsReasoningAndParallelTools(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey, cfg.AdminKey = "client", "admin"
	cfg.Backends = []Backend{{ID: "opencode", Kind: "builtin", URL: builtinBase("opencode"), Auth: "none", ProviderKey: "zen-secret", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		data := `{"opencode":{"npm":"@ai-sdk/openai","models":{"test-reasoner":{}}}}`
		ctype := "application/json"
		if r.URL.Host == "opencode.ai" {
			ctype = "text/event-stream"
			data = "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"Thinking\"}\n\n" +
				"data: {\"type\":\"response.output_item.added\",\"output_index\":2,\"item\":{\"type\":\"function_call\",\"call_id\":\"call-a\",\"name\":\"weather\"}}\n\n" +
				"data: {\"type\":\"response.output_item.added\",\"output_index\":3,\"item\":{\"type\":\"function_call\",\"call_id\":\"call-b\",\"name\":\"clock\"}}\n\n" +
				"data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":3,\"delta\":\"{}\"}\n\n" +
				"data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":2,\"delta\":\"{\\\"city\\\":\\\"Paris\\\"}\"}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-done\",\"status\":\"completed\",\"usage\":{\"input_tokens\":4,\"output_tokens\":6,\"total_tokens\":10}}}\n\n"
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{ctype}}, Body: io.NopCloser(strings.NewReader(data))}, nil
	})
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"opencode/test-reasoner","messages":[{"role":"user","content":"hello"}],"stream":true,"stream_options":{"include_usage":true}}`))
	r.Header.Set("Authorization", "Bearer client")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	body := w.Body.String()
	for _, want := range []string{`"reasoning_content":"Thinking"`, `"id":"call-a"`, `"id":"call-b"`, `"index":1`, `"finish_reason":"tool_calls"`, `"total_tokens":10`, "data: [DONE]"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s: %d %s", want, w.Code, body)
		}
	}
}

func TestCodexNativeTransportUsesUserImportedAuthorization(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey, cfg.AdminKey = "client", "admin"
	cfg.Backends = []Backend{{ID: "codex", Kind: "builtin", URL: builtinBase("codex"), Auth: "none", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "chatgpt.com" || r.URL.Path != "/backend-api/codex/responses" || r.Header.Get("Authorization") != "Bearer user-access" || r.Header.Get("ChatGPT-Account-ID") != "user-account" {
			t.Errorf("wrong Codex transport identity")
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["stream"] != true || body["store"] != false {
			t.Error("Codex response must stream with storage disabled")
		}
		data := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-codex\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"Hello\"}]}]}}\n\n"
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(data))}, nil
	})
	call := func(path, key, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w
	}
	if w := call("/api/accounts/codex/import", "admin", `{"auth_mode":"chatgpt","tokens":{"access_token":"user-access","account_id":"user-account"}}`); w.Code != 200 {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}
	w := call("/v1/chat/completions", "client", `{"model":"codex/gpt-test","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"content":"Hello"`) {
		t.Fatalf("Codex native response: %d %s", w.Code, w.Body.String())
	}
}

func TestCodexDeviceAuthorizationReturnsOnlyUserChallenge(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey, cfg.AdminKey = "client", "admin"
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var data string
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			data = `{"device_auth_id":"device-private","user_code":"USER-CODE","interval":"1"}`
		case "/api/accounts/deviceauth/token":
			data = `{"authorization_code":"code-private","code_verifier":"verifier-private"}`
		case "/oauth/token":
			r.ParseForm()
			if r.Form.Get("code_verifier") != "verifier-private" || r.Form.Get("grant_type") != "authorization_code" {
				t.Error("missing PKCE token exchange")
			}
			data = `{"access_token":"access-private","refresh_token":"refresh-private","id_token":"eyJhbGciOiJub25lIn0.eyJodHRwczovL2FwaS5vcGVuYWkuY29tL2F1dGgiOnsiY2hhdGdwdF9hY2NvdW50X2lkIjoiYWNjb3VudC10ZXN0In19.sig"}`
		default:
			t.Errorf("unexpected authorization dependency: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
	})
	r := httptest.NewRequest("POST", "/api/accounts/codex/login", nil)
	r.Header.Set("Authorization", "Bearer admin")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "USER-CODE") || strings.Contains(w.Body.String(), "device-private") {
		t.Fatalf("invalid user challenge: %d %s", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		r := httptest.NewRequest("GET", "/api/accounts/codex", nil)
		r.Header.Set("Authorization", "Bearer admin")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if strings.Contains(w.Body.String(), "private") {
			t.Fatal("private OAuth data leaked")
		}
		if strings.Contains(w.Body.String(), `"status":"authorized"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("device login did not finish", w.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
