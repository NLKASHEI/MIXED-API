package gateway

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkBuddyLiveCatalogReflectsPromotionalPricing(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.AdminKey = "admin"
	cfg.Backends = []Backend{{ID: "workbuddy", Kind: "builtin", URL: builtinBase("workbuddy"), WorkBuddyRegion: "intl", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	if err := app.storeCredential("workbuddy-intl", accountCredential{AccessToken: "private"}); err != nil {
		t.Fatal(err)
	}
	price := "x0.00 credits"
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "www.workbuddy.ai" || r.URL.Path != "/v3/config" || r.Header.Get("Authorization") != "Bearer private" {
			t.Error("wrong catalog endpoint or credential", r.URL)
		}
		body := `{"code":0,"data":{"models":[{"id":"deepseek-v4.1-flash","credits":"` + price + `"},{"id":"unknown-price"},{"id":"disabled","disabled":true,"credits":"x0.00 credits"}],"agents":[{"name":"cli","models":["deepseek-v4.1-flash","unknown-price","disabled"]}]}}`
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	w := accountTestCall(app, "GET", "/v1/models", "client", "")
	if !strings.Contains(w.Body.String(), "workbuddy/deepseek-v4.1-flash") || !strings.Contains(w.Body.String(), `"kind":"free_promotion"`) || strings.Contains(w.Body.String(), "workbuddy/disabled") || strings.Contains(w.Body.String(), "private") {
		t.Fatal(w.Code, w.Body.String())
	}
	price = "x0.13 credits"
	w = accountTestCall(app, "GET", "/v1/models", "client", "")
	if strings.Contains(w.Body.String(), `"kind":"free_promotion"`) || !strings.Contains(w.Body.String(), `"kind":"credits"`) {
		t.Fatal("expired promotion retained free label", w.Body.String())
	}
}
