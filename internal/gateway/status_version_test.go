package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// The desktop launcher must identify the running build through the same
// authenticated HTTP surface as the dashboard, without contacting a provider.
func TestStatusIdentifiesRunningBuild(t *testing.T) {
	cfg := Defaults()
	cfg.AdminKey, cfg.APIKey = "fixture-admin", "fixture-client"
	s := httptest.NewServer(NewServer(filepath.Join(t.TempDir(), "config.json"), cfg))
	defer s.Close()
	for _, key := range []string{"", cfg.APIKey, cfg.AdminKey} {
		req, _ := http.NewRequest("GET", s.URL+"/api/status", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := s.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var body struct{ Service, Version string }
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if key != cfg.AdminKey {
			if resp.StatusCode != 401 || body.Version != "" {
				t.Fatal("status exposed without administrator authentication")
			}
			continue
		}
		if err != nil || resp.StatusCode != 200 || body.Service != "MIXED-API" || body.Version != BuildVersion {
			t.Fatalf("running build cannot be identified: status=%d service=%q version=%q", resp.StatusCode, body.Service, body.Version)
		}
	}
}
