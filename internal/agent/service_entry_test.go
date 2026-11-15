package agent

import "testing"

func TestServiceEntry_Name(t *testing.T) {
	t.Run("falls back to auto name", func(t *testing.T) {
		e := &ServiceEntry{AutoName: "node-3000"}
		if got := e.Name(); got != "node-3000" {
			t.Fatalf("expected %q, got %q", "node-3000", got)
		}
	})

	t.Run("authoritative name wins", func(t *testing.T) {
		e := &ServiceEntry{AutoName: "node-3000", AuthoritativeName: "frontend"}
		if got := e.Name(); got != "frontend" {
			t.Fatalf("expected %q, got %q", "frontend", got)
		}
	})
}
