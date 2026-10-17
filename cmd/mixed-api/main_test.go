package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NLKASHEI/MIXED-API/internal/gateway"
)

func TestReopenOnlyMatchingGateway(t *testing.T) {
	for _, tc := range []struct {
		name, body, contentType string
		status                  int
		want                    bool
	}{
		{"same build", fmt.Sprintf(`{"service":"MIXED-API","version":%q}`, gateway.BuildVersion), "application/json; charset=utf-8", 200, true},
		{"old build", `{"service":"MIXED-API","version":"v0.4.0"}`, "application/json", 200, false},
		{"legacy status", `{"processes":{},"backends":[]}`, "application/json", 200, false},
		{"unrelated listener", fmt.Sprintf(`{"service":"other","version":%q}`, gateway.BuildVersion), "application/json", 200, false},
		{"wrong key", `{"error":{"message":"denied"}}`, "application/json", 401, false},
		{"malformed status", `{`, "application/json", 200, false},
		{"HTML", `<html>not the gateway</html>`, "text/html", 200, false},
		{"oversized response", `{"padding":"` + strings.Repeat("x", 1<<20) + `"}`, "application/json", 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/status" || r.Header.Get("Authorization") != "Bearer fixture-admin" {
					t.Error("launcher did not authenticate its status check")
				}
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer s.Close()
			if got := existingGateway(s.URL, "fixture-admin"); got != tc.want {
				t.Fatalf("reopen=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestReopenNeverFollowsRedirect(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("launcher followed redirect with its administrator credential")
	}))
	defer destination.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer s.Close()
	if existingGateway(s.URL, "fixture-admin") {
		t.Fatal("redirect accepted as the running gateway")
	}
}
