package gateway

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEngineSettingsAreValidatedAndPreservePrivateConfiguration(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows managed engine profile")
	}
	t.Setenv("APPDATA", t.TempDir())
	dir, _ := localEngineDir()
	os.MkdirAll(filepath.Join(dir, "config"), 0700)
	os.WriteFile(filepath.Join(dir, "vertex-proxy.exe"), []byte("test component marker"), 0600)
	original := `{"admin_password":"private-admin","port_api":2157,"max_retries":2,"request_timeout_seconds":180,"aggregate_stream":false,"custom_setting":"keep"}`
	path := filepath.Join(dir, "config", "config.json")
	os.WriteFile(path, []byte(original), 0600)
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.AdminKey = "admin"
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	for _, tc := range []struct {
		method, body, key string
		code              int
	}{{"GET", "", "client", 401}, {"GET", "", "admin", 200}, {"PUT", `{"max_retries":100,"request_timeout_seconds":180,"aggregate_stream":false}`, "admin", 400}, {"PUT", `{"max_retries":1,"request_timeout_seconds":60,"aggregate_stream":true,"admin_password":"overwrite"}`, "admin", 400}, {"PUT", `{"max_retries":1,"request_timeout_seconds":60,"aggregate_stream":true}`, "admin", 200}} {
		r := httptest.NewRequest(tc.method, "/api/engine-settings/aps", strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer "+tc.key)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatalf("%s: HTTP %d want %d: %s", tc.method, w.Code, tc.code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "private-admin") {
			t.Fatal("engine settings exposed private admin password")
		}
	}
	updated, _ := os.ReadFile(path)
	if !strings.Contains(string(updated), "private-admin") || !strings.Contains(string(updated), "keep") || !strings.Contains(string(updated), `"request_timeout_seconds": 60`) {
		t.Fatalf("settings lost or not updated: %s", updated)
	}
	backup, _ := os.ReadFile(filepath.Join(dir, "config", ".mixed-settings.previous.json"))
	if string(backup) != original {
		t.Fatal("missing exact original settings backup")
	}
}
