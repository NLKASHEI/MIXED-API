package main

import (
	"strings"
	"testing"
)

func TestGuideForAllProvidersAndStages(t *testing.T) {
	for id := range onboardingProviders {
		for _, tt := range []struct {
			state  string
			models []string
			want   string
		}{
			{"", nil, "idle"}, {"installing: preparing", nil, "working"}, {"auth_pending: please login", nil, "auth"},
			{"needs_runtime: restart", nil, "runtime"}, {"failed: HTTP 401", nil, "failed"},
			{"ready: 已授权", nil, "checking"}, {"ready: 已验证", []string{id + "/test"}, "ready"},
		} {
			g := guideFor(id, tt.state, tt.models)
			if g.Stage != tt.want {
				t.Errorf("%s/%s: got %s want %s", id, tt.state, g.Stage, tt.want)
			}
			if !strings.Contains(guideSteps(g), "①") || !strings.Contains(guideSteps(g), "③") {
				t.Fatal("no instructions")
			}
			if g.Stage == "ready" && !strings.Contains(g.Next, id+"/test") {
				t.Fatalf("missing model ID: %s", g.Next)
			}
		}
	}
}
func TestGuidesNeverTreatLoginAsModelSuccess(t *testing.T) {
	x := guideFor("codex", "ready: ChatGPT 订阅已授权", nil)
	if x.Stage == "ready" {
		t.Fatal("login alone cannot mark chat ready")
	}
}
func TestOfficialKeysOnlyWhenNeeded(t *testing.T) {
	if !guideFor("vertex", "", nil).NeedsKey || !guideFor("opencode", "", nil).NeedsKey {
		t.Fatal("missing key fields")
	}
	if guideFor("codex", "", nil).NeedsKey {
		t.Fatal("codex oauth should not request manual token")
	}
}
