package gateway

import "testing"

func TestCodexSubscriptionStatusGate(t *testing.T) {
    for _, tc := range []struct{ text string; ok bool } {
        {"Logged in using ChatGPT", true},
        {"Logged in using an API key", false},
        {"Not logged in", false},
        {"Error: credentials expired", false},
        {"Logged in via ChatGPT (Plus)", true},
    } {
        if got := codexStatusIsSubscription(tc.text); got != tc.ok {
            t.Errorf("status %q: got %t, want %t", tc.text, got, tc.ok)
        }
    }
}
