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

func TestOpenCodeCatalogOnlyListsConfirmedFreeCandidates(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.AdminKey = "admin"
	cfg.Aliases = map[string]string{"paid-alias": "opencode/gpt-5.5"}
	cfg.Backends = []Backend{{ID: "opencode", Kind: "builtin", URL: builtinBase("opencode"), Auth: "none", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"big-pickle"},{"id":"gpt-5.5"},{"id":"muse-spark-1.3-contributor-free"},{"id":"future-model"}]}`))}, nil
	})
	server := httptest.NewServer(app)
	defer server.Close()
	r, _ := http.NewRequest("GET", server.URL+"/v1/models", nil)
	r.Header.Set("Authorization", "Bearer client")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Data []struct {
			ID     string `json:"id"`
			Access struct {
				Kind     string `json:"kind"`
				Verified bool   `json:"verified"`
			} `json:"access"`
		} `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil || len(body.Data) != 1 {
		t.Fatal("invalid catalog")
	}
	want := []string{"free_candidate"}
	for i, m := range body.Data {
		if m.Access.Kind != want[i] || m.Access.Verified {
			t.Errorf("%s access=%+v, want %s unverified", m.ID, m.Access, want[i])
		}
	}
}

func TestOpenCodeAnonymousKeyAndFreeTierRejection(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.Backends = []Backend{{ID: "opencode", Kind: "builtin", URL: builtinBase("opencode"), Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	calls := 0
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "Bearer public" || !strings.HasPrefix(r.Header.Get("User-Agent"), "MIXED-API/") {
			t.Error("anonymous request lacks public credential or uses another client's identity")
		}
		if r.URL.Path == "/inference/v1/models" {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"big-pickle"}]}`))}, nil
		}
		return &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"type":"FreeTierError","message":"fixture private upstream details"}`))}, nil
	})
	list := accountTestCall(app, "GET", "/v1/models", "client", "")
	if list.Code != 200 || !strings.Contains(list.Body.String(), "opencode/big-pickle") {
		t.Fatalf("model request failed: %d", list.Code)
	}
	w := accountTestCall(app, "POST", "/v1/chat/completions", "client", `{"model":"opencode/big-pickle","messages":[{"role":"user","content":"hello"}]}`)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "FreeTierError") || strings.Contains(w.Body.String(), "private upstream") || calls != 2 {
		t.Fatalf("free rejection hidden or rerouted: %d calls=%d %s", w.Code, calls, w.Body.String())
	}
}

func TestOpenCodePaidModelRequiresUserKeyBeforeUpstreamRequest(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.Backends = []Backend{{ID: "opencode", Kind: "builtin", URL: builtinBase("opencode"), Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Error("paid request was sent without a user key")
		return &http.Response{StatusCode: 401, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("denied"))}, nil
	})
	w := accountTestCall(app, "POST", "/v1/chat/completions", "client", `{"model":"opencode/gpt-5.5","messages":[{"role":"user","content":"hello"}]}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "付费模型") {
		t.Fatalf("missing paid-model setup instruction: %d %s", w.Code, w.Body.String())
	}
}
