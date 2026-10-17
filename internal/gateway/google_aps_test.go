package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/NLKASHEI/MIXED-API/internal/googleweb"
)

func TestAPSUsesGraphQLSchemasForToolsAndStructuredOutput(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.Backends = []Backend{{ID: "aps", Kind: "builtin", URL: builtinBase("aps"), Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	called := 0
	app.SetGoogleSessions(&fixtureGoogleBrowser{send: func(ctx context.Context, id string, op googleweb.Operation) (*http.Response, error) {
		called++
		var body map[string]any
		if json.Unmarshal(op.Body, &body) != nil {
			t.Fatal("invalid source body")
		}
		tool := body["tools"].([]any)[0].(map[string]any)["functionDeclarations"].([]any)[0].(map[string]any)
		// GraphQL map fields use key/value entries, not REST JSON dictionaries.
		want := `{"type":"OBJECT","required":["city"],"properties":[{"key":"city","value":{"type":"STRING","minLength":"1"}},{"key":"days","value":{"type":"ARRAY","maxItems":"7","items":{"type":"INTEGER"}}}]}`
		var expected any
		json.Unmarshal([]byte(want), &expected)
		if !reflect.DeepEqual(tool["parameters"], expected) {
			t.Errorf("tool schema was not converted to GraphQL: %v", tool["parameters"])
		}
		config := body["generationConfig"].(map[string]any)
		if config["responseJsonSchema"] != nil || !reflect.DeepEqual(config["responseSchema"], expected) {
			t.Errorf("structured output still uses the REST schema: %v", config)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}}}}]}`))}, nil
	}})
	schema := `{"type":"object","required":["city"],"properties":{"city":{"type":"string","minLength":1},"days":{"type":"array","maxItems":7,"items":{"type":"integer"}}}}`
	body := `{"model":"aps/gemini-test","messages":[{"role":"user","content":"weather"}],"tools":[{"type":"function","function":{"name":"weather","parameters":` + schema + `}}],"response_format":{"type":"json_schema","json_schema":{"name":"weather","schema":` + schema + `}}}`
	w := accountTestCall(app, "POST", "/v1/chat/completions", "client", body)
	if w.Code != 200 || called != 1 {
		t.Fatalf("generation failed: %d %s", w.Code, w.Body.String())
	}
}

func TestAPSOuterGraphQLErrorCannotFinishSuccessfully(t *testing.T) {
	for _, stream := range []bool{false, true} {
		cfg := Defaults()
		cfg.APIKey = "client"
		cfg.Backends = []Backend{{ID: "aps", Kind: "builtin", URL: builtinBase("aps"), Enabled: true}}
		app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
		app.SetGoogleSessions(&fixtureGoogleBrowser{send: func(ctx context.Context, id string, op googleweb.Operation) (*http.Response, error) {
			data := `[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"content":{"parts":[{"text":"partial"}]},"finishReason":"STOP"}]}}}}]},{"errors":[{"message":"fixture denied"}]}]`
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
		}})
		body := `{"model":"aps/gemini-test","messages":[{"role":"user","content":"hello"}],"stream":` + fmtBool(stream) + `}`
		w := accountTestCall(app, "POST", "/v1/chat/completions", "client", body)
		if stream {
			if !strings.Contains(w.Body.String(), `"error"`) || strings.Contains(w.Body.String(), "[DONE]") {
				t.Fatalf("GraphQL error became completed stream: %s", w.Body.String())
			}
		} else if w.Code != 502 {
			t.Fatalf("GraphQL error became successful completion: %d", w.Code)
		}
	}
}

func TestGoogleBusinessErrorsRemainVisibleBehindHTTP200(t *testing.T) {
	for _, tc := range []struct {
		model, wire, name string
		code              int
	}{
		{"aps/gemini-test", `{"results":[{"errors":[{"errorType":"RESOURCE_EXHAUSTED","message":"PRIVATE_PROMPT PRIVATE_COOKIE","extensions":{"status":{"code":"8","message":"PRIVATE_TOKEN"}}}]}]}`, "RESOURCE_EXHAUSTED", 8},
		{"aistudio/playground/gemini-test", `[[],[8,"PRIVATE_PROMPT PRIVATE_COOKIE PRIVATE_TOKEN"]]`, "RESOURCE_EXHAUSTED", 8},
		{"aps/gemini-test", `{"results":[{"errors":[{"extensions":{"status":{"status":"PERMISSION_DENIED","message":"PRIVATE_TOKEN"}}}]}]}`, "PERMISSION_DENIED", 0},
	} {
		for _, stream := range []bool{false, true} {
			cfg := Defaults()
			cfg.APIKey = "client"
			cfg.AdminKey = "admin"
			id := strings.Split(tc.model, "/")[0]
			cfg.Backends = []Backend{{ID: id, Kind: "builtin", URL: builtinBase(id), Enabled: true}}
			app := NewServer("", cfg)
			calls := 0
			app.SetGoogleSessions(&fixtureGoogleBrowser{send: func(context.Context, string, googleweb.Operation) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tc.wire))}, nil
			}})
			body := `{"model":"` + tc.model + `","messages":[{"role":"user","content":"PRIVATE_PROMPT"}],"stream":` + fmtBool(stream) + `}`
			w := accountTestCall(app, "POST", "/v1/chat/completions", "client", body)
			if calls != 1 || strings.Contains(w.Body.String(), "[DONE]") {
				t.Fatal("business error replayed or completed")
			}
			logs := accountTestCall(app, "GET", "/api/logs", "admin", "").Body.String()
			for _, want := range []string{`"upstream_status":200`, tc.name} {
				if !strings.Contains(logs, want) {
					t.Errorf("missing %s: %s", want, logs)
				}
			}
			if tc.code > 0 && !strings.Contains(logs, `"business_code":8`) {
				t.Error("missing business code", logs)
			}
			if strings.Contains(logs, "PRIVATE_") || strings.Contains(w.Body.String(), "PRIVATE_") {
				t.Fatal("provider contents leaked")
			}
		}
	}
}
