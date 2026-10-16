package gateway

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPrepareInstallsCredentialsWithoutEnablingOrStartingSource(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows installer")
	}
	t.Setenv("APPDATA", t.TempDir())
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	file, _ := writer.Create("opencode2api.exe")
	file.Write([]byte("fixture: must not execute"))
	writer.Close()
	digest := sha256.Sum256(archive.Bytes())
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(archive.Bytes()) }))
	defer vendor.Close()
	original := binaryUpstreams["opencode"]
	binaryUpstreams["opencode"] = binaryUpstream{vendor.URL, hex.EncodeToString(digest[:]), "opencode2api.exe"}
	defer func() { binaryUpstreams["opencode"] = original }()
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.AdminKey = "admin"
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	call := func(method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer admin")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w
	}
	if w := call("POST", "/api/assist/opencode/prepare"); w.Code != 200 {
		t.Fatalf("prepare: %d %s", w.Code, w.Body.String())
	}
	deadline := time.After(5 * time.Second)
	for {
		w := call("GET", "/api/status")
		var status struct {
			Processes map[string]string `json:"processes"`
		}
		json.Unmarshal(w.Body.Bytes(), &status)
		state := status.Processes["setup:opencode"]
		if strings.HasPrefix(state, "failed:") {
			t.Fatal(state)
		}
		if strings.HasPrefix(state, "installed:") {
			break
		}
		select {
		case <-deadline:
			t.Fatal("preparation did not finish")
		case <-time.After(10 * time.Millisecond):
		}
	}
	w := call("GET", "/api/config")
	var saved Config
	json.Unmarshal(w.Body.Bytes(), &saved)
	for _, backend := range saved.Backends {
		if backend.Enabled {
			t.Fatal("preparation activated a source before user confirmation")
		}
		if backend.ID == "opencode" && backend.Key == "" {
			t.Fatal("preparation failed to generate communication credentials")
		}
	}
	status := call("GET", "/api/status")
	if strings.Contains(status.Body.String(), `"opencode":"running"`) {
		t.Fatal("preparation started the executable")
	}
	// Another portable gateway has no component key in its fresh config, but
	// reuses the same private runtime. Preparation must restore that connection.
	var expectedKey string
	for _, b := range saved.Backends {
		if b.ID == "opencode" {
			expectedKey = b.Key
		}
	}
	app = NewServer(filepath.Join(t.TempDir(), "new-config.json"), cfg)
	if w := call("POST", "/api/assist/opencode/prepare"); w.Code != 200 {
		t.Fatalf("reconnect prepare: %d %s", w.Code, w.Body.String())
	}
	current := call("GET", "/api/config")
	var reconnected Config
	json.Unmarshal(current.Body.Bytes(), &reconnected)
	for _, b := range reconnected.Backends {
		if b.ID == "opencode" && (b.Enabled || b.Key != expectedKey) {
			t.Fatal("existing component was not reconnected privately before activation")
		}
	}
	// A broken existing component config must not leave the legacy quickstart
	// busy forever. After repair, a retry reaches the (intentionally invalid)
	// fixture executable rather than being rejected as an active task.
	runtimeDir, _ := app.binaryDir("opencode")
	componentConfig := filepath.Join(runtimeDir, "config.json")
	valid, _ := os.ReadFile(componentConfig)
	os.WriteFile(componentConfig, []byte("{"), 0600)
	failed := call("POST", "/api/assist/opencode/quickstart")
	if failed.Code != 400 {
		t.Fatal("malformed runtime configuration accepted")
	}
	stateAfterFailure := call("GET", "/api/status")
	if !strings.Contains(stateAfterFailure.Body.String(), "failed:") {
		t.Fatal("recovery failure left quickstart busy")
	}
	os.WriteFile(componentConfig, valid, 0600)
	retry := call("POST", "/api/assist/opencode/quickstart")
	if strings.Contains(retry.Body.String(), "已有部署任务") {
		t.Fatal("repaired component cannot retry")
	}
}
