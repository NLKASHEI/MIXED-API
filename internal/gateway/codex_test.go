package gateway

import "testing"

func TestDefaultCodexUsesBuiltinHTTP(t *testing.T) {
	c := Defaults()
	for _, b := range c.Backends {
		if b.ID == "codex" && b.Kind != "builtin" {
			t.Fatal("Codex must use the in-process HTTP implementation by default")
		}
	}
}
