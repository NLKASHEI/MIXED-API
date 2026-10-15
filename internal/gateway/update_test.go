package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVersionCompareAndPermanentUpdateURL(t *testing.T) {
	for _, test := range []struct {
		current, latest string
		newer           bool
	}{
		{"v0.2.9", "v0.2.10", true},
		{"v0.2.9", "v0.3.0", true},
		{"v0.2.9", "v0.2.9", false},
		{"v0.2.9", "v0.2.1", false},
		{"dev", "v0.2.10", false},
		{"v0.2.9", "v0.2.10-preview", false},
	} {
		if isNewerVersion(test.current, test.latest) != test.newer {
			t.Errorf("%q -> %q should be newer=%t", test.current, test.latest, test.newer)
		}
	}
	if latestReleasePage != "https://github.com/NLKASHEI/MIXED-API/releases/latest" {
		t.Fatal("updater must use stable latest URL")
	}
}
func TestReleaseInstallerMustBePinnedToRepository(t *testing.T) {
	tag := "v0.2.10"
	good := "https://github.com/NLKASHEI/MIXED-API/releases/download/" + tag + "/" + setupAssetName
	for _, url := range []string{
		"http://github.com/NLKASHEI/MIXED-API/releases/download/" + tag + "/" + setupAssetName,
		"https://evil.example/" + setupAssetName,
		"https://github.com/NLKASHEI/OTHER/releases/download/" + tag + "/" + setupAssetName,
		good + "?asset=wrong",
	} {
		if validAssetURL(tag, setupAssetName, url) {
			t.Errorf("unsafe installer URL accepted %s", url)
		}
	}
	r := releaseInfo{Tag: tag, URL: "https://github.com/NLKASHEI/MIXED-API/releases/tag/" + tag, Assets: []releaseAsset{{Name: setupAssetName, DownloadURL: good, Digest: "sha256:" + strings.Repeat("a", 64), Size: 2222432}}}
	if _, e := releaseSetupAsset(r); e != nil {
		t.Fatal(e)
	}
	r.Assets[0].Digest = "sha256:not-a-real-digest"
	if _, e := releaseSetupAsset(r); e == nil {
		t.Fatal("should reject release with missing digest")
	}
}
func TestUpdateEndpointsRequireAdminNotApiKey(t *testing.T) {
	c := Defaults()
	c.APIKey = "client-secret"
	c.AdminKey = "admin-secret"
	app := NewServer("", c)
	for _, p := range []string{"/api/update", "/api/update/install"} {
		method := "GET"
		if strings.HasSuffix(p, "/install") {
			method = "POST"
		}
		for _, key := range []string{"", "client-secret"} {
			req := httptest.NewRequest(method, p, nil)
			req.Header.Set("Authorization", "Bearer "+key)
			result := httptest.NewRecorder()
			app.ServeHTTP(result, req)
			if result.Code != http.StatusUnauthorized {
				t.Errorf("%s accepted non-admin key, got %d", p, result.Code)
			}
		}
		// LAN API must never expose software update or administrative operations.
		req := httptest.NewRequest(method, p, nil)
		req.Header.Set("Authorization", "Bearer admin-secret")
		result := httptest.NewRecorder()
		APIOnly(app).ServeHTTP(result, req)
		if result.Code != http.StatusNotFound {
			t.Fatalf("LAN exposes updater route %s", p)
		}
	}
}
