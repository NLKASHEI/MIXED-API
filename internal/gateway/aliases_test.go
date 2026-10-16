package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelAliasUsesExplicitTargetAndNeverFallsBack(t *testing.T) {
	s, upstream := fixture(t, false)
	defer upstream.Close()
	c := s.config()
	c.Aliases = map[string]string{"daily-chat": "mock/gpt-test", "offline": "vertex/unavailable"}
	s = NewServer(s.path, c)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"daily-chat","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != 200 || w.Header().Get("X-Mixed-Backend") != "mock" {
		t.Fatalf("alias did not resolve explicit target: %d %s", w.Code, w.Body.String())
	}
	req = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"offline","messages":[]}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("disabled alias silently routed: %d %s", w.Code, w.Body.String())
	}
	req = httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer sk-test")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, req)
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &list)
	found := false
	for _, m := range list.Data {
		if m.ID == "daily-chat" {
			found = true
		}
		if m.ID == "offline" {
			t.Fatal("unavailable alias advertised")
		}
	}
	if !found {
		t.Fatalf("available alias missing: %s", w.Body.String())
	}
}

func TestAliasConfigRejectsAmbiguousNamesAndTargets(t *testing.T) {
	s, up := fixture(t, false)
	defer up.Close()
	for _, aliases := range []map[string]string{{"mock/gpt-test": "mock/gpt-test"}, {"chat": "missing/model"}, {"a": "b", "b": "mock/gpt-test"}, {"": "mock/gpt-test"}} {
		c := s.config()
		c.Aliases = aliases
		raw, _ := json.Marshal(c)
		r := httptest.NewRequest("PUT", "/api/config", strings.NewReader(string(raw)))
		r.Header.Set("Authorization", "Bearer admin-test")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("invalid aliases accepted: %v HTTP %d", aliases, w.Code)
		}
	}
}
