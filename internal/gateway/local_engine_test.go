package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLocalEngineImportIsPrivateAndDoesNotExecuteOrModifyOriginal(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows local package")
	}
	root := t.TempDir()
	t.Setenv("APPDATA", filepath.Join(root, "private"))
	source := filepath.Join(root, "source")
	os.MkdirAll(source, 0700)
	os.WriteFile(filepath.Join(source, "go.mod"), []byte("module github.com/bsfdsagfadg/vertex\n\ngo 1.23\n"), 0600)
	marker := filepath.Join(source, "executed.txt")
	code := "package main\nimport \"os\"\nfunc main(){os.WriteFile(" + `"` + strings.ReplaceAll(marker, `\`, `\\`) + `"` + ",[]byte(\"executed\"),0600)}"
	os.WriteFile(filepath.Join(source, "main.go"), []byte(code), 0600)
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go.exe"), "build", "-o", "vertex-proxy.exe", ".")
	cmd.Dir = source
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture build: %v %s", err, out)
	}
	os.MkdirAll(filepath.Join(source, "config", "state"), 0700)
	os.WriteFile(filepath.Join(source, "config", "state", "agreed-rules.txt"), []byte("never copy this"), 0600)
	os.WriteFile(filepath.Join(source, "config", "config.json"), []byte(`{"admin_password":"original-private-secret"}`), 0600)
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.AdminKey = "admin"
	app := NewServer(filepath.Join(root, "gateway.json"), cfg)
	raw, _ := json.Marshal(map[string]string{"path": source})
	req := httptest.NewRequest("POST", "/api/local/import", strings.NewReader(string(raw)))
	req.Header.Set("Authorization", "Bearer admin")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("import failed: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("import executed third-party program")
	}
	original, _ := os.ReadFile(filepath.Join(source, "config", "config.json"))
	if string(original) != `{"admin_password":"original-private-secret"}` {
		t.Fatal("original config modified")
	}
	dst, _ := localEngineDir()
	data, _ := os.ReadFile(filepath.Join(dst, "config", "config.json"))
	if strings.Contains(string(data), "original-private-secret") || !strings.Contains(string(data), "2157") {
		t.Fatalf("import copied source secrets or used conflicting port: %s", data)
	}
	if _, err := os.Stat(filepath.Join(dst, "config", "state")); !os.IsNotExist(err) {
		t.Fatal("import copied consent state")
	}
	current := app.config()
	var b Backend
	for _, candidate := range current.Backends {
		if candidate.ID == localEngineID {
			b = candidate
		}
	}
	if len(current.Backends) != len(cfg.Backends) {
		t.Fatal("local component created an extra user-facing source")
	}
	if b.ID != localEngineID || b.Enabled || b.Key == "" {
		t.Fatal("import must register a disabled engine with fresh credentials")
	}
	if strings.Contains(w.Body.String(), b.Key) {
		t.Fatal("import response contains engine credential")
	}
	// Reimport cannot overwrite its data, even with administrator credentials.
	req = httptest.NewRequest("POST", "/api/local/import", strings.NewReader(string(raw)))
	req.Header.Set("Authorization", "Bearer admin")
	w = httptest.NewRecorder()
	app.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatal("reimport overwrote managed workspace")
	}
}
