package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiscoveryProbesSourcesConcurrentlyAndPreservesOrder(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.AdminKey = "admin"
	cfg.Backends = []Backend{{ID: "first", URL: "http://127.0.0.1", Auth: "none", Enabled: true}, {ID: "second", URL: "http://127.0.0.1", Auth: "none", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	// A barrier, rather than timing performance, proves both requests are in flight.
	gate := make(chan struct{})
	entered := make(chan struct{}, 2)
	barrier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-gate
		w.Write([]byte(`{"data":[{"id":"same"},{"id":"same"}]}`))
	}))
	defer barrier.Close()
	cfg.Backends[0].URL = barrier.URL
	cfg.Backends[1].URL = barrier.URL
	app = NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest("GET", "/v1/models", nil)
		r.Header.Set("Authorization", "Bearer client")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		done <- w
	}()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	concurrent := true
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-timer.C:
			concurrent = false
		}
	}
	close(gate)
	w := <-done
	if !concurrent {
		t.Fatal("model discovery serialized requests; slow sources block other probes")
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Data) != 2 || out.Data[0].ID != "first/same" || out.Data[1].ID != "second/same" {
		t.Fatalf("unexpected ordered deduplicated models: %s", w.Body.String())
	}
}

func TestDiagnosticsExplainsEmptyModelsWithoutExposingSecrets(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"data":[]}`)) }))
	defer upstream.Close()
	cfg := Defaults()
	cfg.APIKey = "client-secret"
	cfg.AdminKey = "admin-secret"
	cfg.Backends = []Backend{{ID: "empty", URL: upstream.URL, Auth: "bearer", Key: "provider-secret", Enabled: true}, {ID: "off", URL: upstream.URL, Auth: "none"}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	for _, key := range []string{"client-secret", "admin-secret"} {
		r := httptest.NewRequest("GET", "/api/diagnostics", nil)
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if key == "client-secret" {
			if w.Code != 401 {
				t.Fatal("client key can read diagnostics")
			}
			continue
		}
		if w.Code != 200 {
			t.Fatalf("diagnostics unavailable: %d %s", w.Code, w.Body.String())
		}
		var out struct {
			Backends []struct {
				ID, State, Code, Next string
				ModelCount            int `json:"model_count"`
			} `json:"backends"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Backends) != 2 || out.Backends[0].State != "error" || out.Backends[0].Code != "empty_models" || out.Backends[0].Next == "" || out.Backends[1].State != "disabled" {
			t.Fatalf("misleading diagnostics: %s", w.Body.String())
		}
		for _, secret := range []string{cfg.APIKey, cfg.AdminKey, "provider-secret"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("diagnostics leaked credentials")
			}
		}
	}
}

func TestModelDiscoveryDoesNotFollowCredentialRedirect(t *testing.T) {
	reached := make(chan struct{}, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached <- struct{}{}
		w.Write([]byte(`{"data":[{"id":"stolen"}]}`))
	}))
	defer target.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer upstream.Close()
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.AdminKey = "admin"
	cfg.Backends = []Backend{{ID: "redirect", URL: upstream.URL, Key: "private-provider-key", Auth: "bearer", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	r := httptest.NewRequest("GET", "/v1/models", nil)
	r.Header.Set("Authorization", "Bearer client")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	select {
	case <-reached:
		t.Fatal("gateway followed an upstream redirect with credentials")
	default:
	}
	if !strings.Contains(w.Body.String(), `"data":[]`) || !strings.Contains(w.Body.String(), "backend_errors") {
		t.Fatalf("redirect accepted: %s", w.Body.String())
	}
}
