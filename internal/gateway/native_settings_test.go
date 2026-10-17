package gateway

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntegratedSettingsBelongToGateway(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.AdminKey = "admin"
	cfg.Backends = []Backend{{ID: "aps", Kind: "builtin", URL: builtinBase("aps"), Auth: "none"}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	call := func(method, key, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/engine-settings/aps", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w
	}
	if w := call("GET", "admin", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"request_timeout_seconds":300`) {
		t.Fatalf("native settings require upstream installation: %d %s", w.Code, w.Body.String())
	}
	if w := call("PUT", "client", `{}`); w.Code != 401 {
		t.Fatal("client edited admin settings")
	}
	if w := call("PUT", "admin", `{"max_retries":1,"request_timeout_seconds":180,"aggregate_stream":true}`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if w := call("GET", "admin", ""); !strings.Contains(w.Body.String(), `"aggregate_stream":true`) {
		t.Fatal(w.Body.String())
	}
	if w := call("PUT", "admin", `{"max_retries":1,"request_timeout_seconds":1,"aggregate_stream":false}`); w.Code != 400 {
		t.Fatal("accepted unsafe timeout")
	}
}
func TestLegacyProcessStartIsDisabled(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.AdminKey = "admin"
	cfg.Backends = []Backend{{ID: "legacy", URL: "http://127.0.0.1:1", Auth: "none"}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	r := httptest.NewRequest("POST", "/api/process/legacy/start", nil)
	r.Header.Set("Authorization", "Bearer admin")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 410 {
		t.Fatalf("legacy process launcher remains reachable: %d", w.Code)
	}
}
