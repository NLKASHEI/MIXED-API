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

func TestOpenCodeCatalogExplainsAccessWithoutClaimingInference(t *testing.T) {
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
	if json.NewDecoder(resp.Body).Decode(&body) != nil || len(body.Data) != 5 {
		t.Fatal("invalid catalog")
	}
	want := []string{"free_candidate", "key_paid", "retired", "unknown", "key_paid"}
	for i, m := range body.Data {
		if m.Access.Kind != want[i] || m.Access.Verified {
			t.Errorf("%s access=%+v, want %s unverified", m.ID, m.Access, want[i])
		}
	}
}
