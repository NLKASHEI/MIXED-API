package gateway

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefreshCannotChangeCodexAccount(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey, cfg.AdminKey = "client", "admin"
	cfg.Backends = []Backend{{ID: "codex", Kind: "builtin", URL: builtinBase("codex"), Auth: "none", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/oauth/token" {
			t.Error("mismatched account reached generation")
		}
		token := "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"other-account"}}`)) + ".signature"
		raw, _ := json.Marshal(map[string]any{"access_token": "new-access", "id_token": token, "refresh_token": "new-refresh", "expires_in": 3600})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})
	w := accountTestCall(app, "POST", "/api/accounts/codex/import", "admin", `{"auth_mode":"chatgpt","tokens":{"access_token":"old","refresh_token":"refresh","account_id":"original-account","expires":1}}`)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = accountTestCall(app, "POST", "/v1/responses", "client", `{"model":"codex/test","input":"hi"}`)
	if w.Code != 401 {
		t.Fatalf("account replacement: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "other-account") || strings.Contains(w.Body.String(), "new-access") {
		t.Fatal("private refresh response exposed")
	}
}

func TestAccountImportRejectsUnsafeHeaderValuesWithoutReplacingAccount(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey, cfg.AdminKey = "client", "admin"
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	valid := `{"auth_mode":"chatgpt","tokens":{"access_token":"access-good","account_id":"account"}}`
	if w := accountTestCall(app, "POST", "/api/accounts/codex/import", "admin", valid); w.Code != 200 {
		t.Fatal(w.Code)
	}
	for _, field := range []string{"access_token", "refresh_token", "account_id"} {
		for _, bad := range []string{"secret\r\nInjected: yes", "secret\x00tail"} {
			value := map[string]any{"auth_mode": "chatgpt", "tokens": map[string]any{"access_token": "access-new", "account_id": "account", field: bad}}
			raw, _ := json.Marshal(value)
			w := accountTestCall(app, "POST", "/api/accounts/codex/import", "admin", string(raw))
			if w.Code != http.StatusBadRequest {
				t.Errorf("%s: expected 400, got %d", field, w.Code)
			}
		}
	}
	c, err := app.credential("codex")
	if err != nil || c.AccessToken != "access-good" {
		t.Fatal("invalid import replaced the current account")
	}
}

func TestExpiredUnrefreshableAccountIsNotShownAuthorized(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey, cfg.AdminKey = "client", "admin"
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	w := accountTestCall(app, "POST", "/api/accounts/codex/import", "admin", `{"auth_mode":"chatgpt","tokens":{"access_token":"expired","account_id":"account","expires":1}}`)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	state := accountTestCall(app, "GET", "/api/accounts/codex", "admin", "")
	var visible map[string]any
	json.Unmarshal(state.Body.Bytes(), &visible)
	if visible["status"] != "expired" {
		t.Fatalf("expired credential status: %s", state.Body.String())
	}
}
