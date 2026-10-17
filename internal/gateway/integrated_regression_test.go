package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpressInputToolRoundTrip(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey, cfg.AdminKey = "client", "admin"
	cfg.Backends = []Backend{{ID: "vertex", Kind: "builtin", URL: builtinBase("vertex"), Auth: "none", ProviderKey: "express", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body struct {
			Contents []struct {
				Parts []map[string]any `json:"parts"`
			} `json:"contents"`
			Tools []struct {
				Functions []map[string]any `json:"functionDeclarations"`
			} `json:"tools"`
			Config struct {
				Function struct {
					Mode  string   `json:"mode"`
					Names []string `json:"allowedFunctionNames"`
				} `json:"functionCallingConfig"`
			} `json:"toolConfig"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.Contents) != 3 || len(body.Contents[1].Parts) != 1 || body.Contents[1].Parts[0]["functionCall"] == nil {
			t.Errorf("assistant null content created empty provider part: %+v", body.Contents)
		}
		if len(body.Tools) != 1 || len(body.Tools[0].Functions) != 1 {
			t.Fatal("missing function declaration")
		}
		if _, ok := body.Tools[0].Functions[0]["strict"]; ok {
			t.Error("OpenAI strict leaked into Gemini function declaration")
		}
		if body.Config.Function.Mode != "ANY" || len(body.Config.Function.Names) != 1 || body.Config.Function.Names[0] != "weather" {
			t.Error("named tool choice was lost")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"candidates":[{"index":0,"content":{"parts":[{"functionCall":{"name":"weather","args":{}}}]},"finishReason":"STOP"}]}`))}, nil
	})
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"vertex/gemini-test","messages":[{"role":"user","content":"weather"},{"role":"assistant","content":null,"tool_calls":[{"id":"a","type":"function","function":{"name":"weather","arguments":"{}"}}]},{"role":"tool","tool_call_id":"a","content":"sunny"}],"tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object","properties":{}},"strict":true}}],"tool_choice":{"type":"function","function":{"name":"weather"}}}`))
	r.Header.Set("Authorization", "Bearer client")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("tool roundtrip: %d %s", w.Code, w.Body.String())
	}
}

func TestExpressImagesAndIncompleteCandidates(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "image", true: "unfinished-second-candidate"}[stream], func(t *testing.T) {
			cfg := Defaults()
			cfg.APIKey, cfg.AdminKey = "client", "admin"
			cfg.Backends = []Backend{{ID: "vertex", Kind: "builtin", URL: builtinBase("vertex"), Auth: "none", ProviderKey: "express", Enabled: true}}
			app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
			app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if !stream {
					config, _ := body["generationConfig"].(map[string]any)
					if config["responseModalities"] == nil {
						t.Error("image model never requested image output")
					}
				}
				data := `{"candidates":[{"index":0,"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"aGVsbG8="}}]},"finishReason":"STOP"}]}`
				if stream {
					data = "data: " + data + "\n\ndata: {\"candidates\":[{\"index\":1,\"content\":{\"parts\":[{\"text\":\"partial\"}]}}]}\n\n"
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
			})
			body := `{"model":"vertex/gemini-3.1-flash-image","messages":[{"role":"user","content":"draw"}]}`
			if stream {
				body = `{"model":"vertex/gemini-test","messages":[{"role":"user","content":"hi"}],"n":2,"stream":true}`
			}
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer client")
			w := httptest.NewRecorder()
			app.ServeHTTP(w, r)
			if !stream && !strings.Contains(w.Body.String(), "data:image/png;base64,aGVsbG8=") {
				t.Fatalf("image lost: %s", w.Body.String())
			}
			if stream && (strings.Contains(w.Body.String(), "[DONE]") || !strings.Contains(w.Body.String(), `"error"`)) {
				t.Fatalf("incomplete candidate reported success: %s", w.Body.String())
			}
		})
	}
}

func TestLegacyCodexMigrationRequiresNativeReauthorization(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := `{"listen":"127.0.0.1:8765","api_key":"client","admin_key":"admin","backends":[{"id":"codex","kind":"codex_cli","name":"Codex","url":"http://127.0.0.1:8901/codex/v1","auth":"none","enabled":true,"command":"codex app-server"}]}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	b := cfg.Backends[0]
	if b.Kind != "builtin" || b.URL != builtinBase("codex") || b.Enabled || b.Command != "" {
		t.Fatalf("legacy CLI still active: %+v", b)
	}
	backup, err := os.ReadFile(path + ".before-integrated")
	if err != nil || string(backup) != data {
		t.Fatal("legacy configuration not preserved")
	}
}

func TestAdminCanDisableAndEnableBuiltinWithoutChangingItsKind(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey, cfg.AdminKey = "client", "admin"
	cfg.Backends = []Backend{{ID: "vertex", Kind: "builtin", URL: builtinBase("vertex"), Auth: "none", ProviderKey: "express", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	for _, step := range []struct {
		action, key string
		code        int
		enabled     bool
	}{{"stop", "client", 401, true}, {"stop", "admin", 200, false}, {"start", "admin", 200, true}} {
		r := httptest.NewRequest("POST", "/api/process/vertex/"+step.action, nil)
		r.Header.Set("Authorization", "Bearer "+step.key)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != step.code {
			t.Fatalf("%s: %d %s", step.action, w.Code, w.Body.String())
		}
		r = httptest.NewRequest("GET", "/api/config", nil)
		r.Header.Set("Authorization", "Bearer admin")
		w = httptest.NewRecorder()
		app.ServeHTTP(w, r)
		var stored Config
		json.Unmarshal(w.Body.Bytes(), &stored)
		if len(stored.Backends) != 1 || stored.Backends[0].Kind != "builtin" || stored.Backends[0].Enabled != step.enabled {
			t.Fatalf("builtin lifecycle damaged config: %s", w.Body.String())
		}
	}
}
