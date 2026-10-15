package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestManagedOneClickWaitsForAvailableModels(t *testing.T) {
	authenticated := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/models" || req.Header.Get("Authorization") != "Bearer local-backend-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if authenticated {
			w.Write([]byte(`{"data":[{"id":"model-available"}]}`))
		} else {
			w.Write([]byte(`{"data":[]}`))
		}
	}))
	defer server.Close()
	cfg := Defaults()
	cfg.APIKey = "api-key"
	cfg.AdminKey = "admin-key"
	cfg.Backends = []Backend{{ID: "mock", URL: server.URL + "/v1", Key: "local-backend-key", Auth: "bearer", Enabled: true}}
	app := NewServer("", cfg)
	app.status["mock"] = "running"
	count, status, err := app.probeManagedModels(context.Background(), "mock")
	if err != nil || status != 200 || count != 0 {
		t.Fatalf("no models yet: count=%d status=%d err=%v", count, status, err)
	}
	app.verifyManagedBackend("mock", 35*time.Millisecond)
	if state := app.status["setup:mock"]; !strings.HasPrefix(state, "needs_auth:") {
		t.Fatalf("empty models incorrectly ready: %q", state)
	}
	authenticated = true
	app.verifyManagedBackend("mock", 35*time.Millisecond)
	if state := app.status["setup:mock"]; !strings.HasPrefix(state, "ready:") {
		t.Fatalf("available models not recognized: %q", state)
	}
}

func TestManagedOneClickDetectsUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer server.Close()
	cfg := Defaults()
	cfg.APIKey = "api-key"
	cfg.AdminKey = "admin-key"
	cfg.Backends = []Backend{{ID: "mock", URL: server.URL + "/v1", Auth: "none", Enabled: true}}
	app := NewServer("", cfg)
	app.status["mock"] = "running"
	app.verifyManagedBackend("mock", 100*time.Millisecond)
	if state := app.status["setup:mock"]; !strings.HasPrefix(state, "needs_auth:") {
		t.Fatalf("unauthorized incorrectly ready: %q", state)
	}
}
