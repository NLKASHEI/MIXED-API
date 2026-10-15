package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The portable EXE serves the entire desktop console and its illustration
// from embedded resources; no Node runtime, CDN, or external web UI is needed.
func TestEmbeddedDesktopConsoleAndAssets(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey, cfg.AdminKey = "sk-test-key", "admin-test-key"
	app := NewServer("", cfg)
	page := httptest.NewRecorder()
	app.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("desktop console unavailable: %d %s", page.Code, page.Header().Get("Content-Type"))
	}
	body := page.Body.String()
	for _, want := range []string{"page-home", "page-providers", "page-models", "page-guide", "page-settings", "page-update", "一键接入", "检查更新", "AI Studio", "Codex", "WorkBuddy", "APS2api", "OpenCode", "Vertex"} {
		if !strings.Contains(body, want) {
			t.Errorf("desktop UI missing %q", want)
		}
	}
	asset := httptest.NewRecorder()
	app.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/assets/hero.webp", nil))
	if asset.Code != http.StatusOK || asset.Header().Get("Content-Type") != "image/webp" || asset.Body.Len() < 1000 {
		t.Fatalf("desktop illustration unavailable: HTTP %d, %d bytes", asset.Code, asset.Body.Len())
	}
	restricted := httptest.NewRecorder()
	APIOnly(app).ServeHTTP(restricted, httptest.NewRequest(http.MethodGet, "/assets/hero.webp", nil))
	if restricted.Code != http.StatusNotFound {
		t.Fatalf("LAN API listener must not serve dashboard assets: HTTP %d", restricted.Code)
	}
}
