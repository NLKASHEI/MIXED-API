package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIOnlyLANDoesNotExposeAdminOrWeb(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "sk-mobile-pc-test"
	cfg.AdminKey = "admin-private-test"
	s := NewServer(t.TempDir()+"/settings.json", cfg)
	lan := APIOnly(s)
	for _, path := range []string{"/", "/api/config", "/api/process/codex/start", "/api/guides", "/api/assist/status"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+cfg.AdminKey)
		resp := httptest.NewRecorder()
		lan.ServeHTTP(resp, req)
		if resp.Code != 404 {
			t.Errorf("LAN exposed %s: HTTP %d", path, resp.Code)
		}
	}
	request := httptest.NewRequest("GET", "/v1/models", nil)
	result := httptest.NewRecorder()
	lan.ServeHTTP(result, request)
	if result.Code != 401 {
		t.Fatalf("LAN unauthenticated models returned %d", result.Code)
	}
	request = httptest.NewRequest("GET", "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	result = httptest.NewRecorder()
	lan.ServeHTTP(result, request)
	if result.Code != 200 {
		t.Fatalf("LAN authenticated models returned %d", result.Code)
	}
}
