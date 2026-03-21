package registry

import (
	"testing"
	"time"
)

func TestFindAgentByIP_InternalEdge(t *testing.T) {
	r := New(nil)

	_, _ = r.Register("agent-a", "a", "10.100.0.1", "", nil, nil, "")
	_, _ = r.Register("agent-b", "b", "10.100.0.2", "", nil, nil, "")

	entry := r.FindAgentByIP("10.100.0.2")
	if entry == nil {
		t.Fatal("expected to find agent-b by mesh IP, got nil")
	}
	if entry.AgentID != "agent-b" {
		t.Errorf("expected agent-b, got %s", entry.AgentID)
	}
}

func TestFindAgentByIP_IPv6(t *testing.T) {
	r := New(nil)

	_, _ = r.Register("agent-c", "c", "10.100.0.3", "fd00::3", nil, nil, "")

	entry := r.FindAgentByIP("fd00::3")
	if entry == nil {
		t.Fatal("expected to find agent-c by IPv6, got nil")
	}
}

func TestFindAgentByIP_ExternalIP(t *testing.T) {
	r := New(nil)

	_, _ = r.Register("agent-d", "d", "10.100.0.4", "", nil, nil, "")

	entry := r.FindAgentByIP("8.8.8.8")
	if entry != nil {
		t.Errorf("expected nil for external IP, got agent %s", entry.AgentID)
	}
}

func TestFindAgentByIP_EmptyRegistry(t *testing.T) {
	r := New(nil)
	if r.FindAgentByIP("10.0.0.1") != nil {
		t.Error("expected nil on empty registry")
	}
}

func TestFindAgentByIP_LastSeenUpdated(t *testing.T) {
	r := New(nil)
	_, _ = r.Register("agent-e", "e", "10.100.0.5", "", nil, nil, "")

	// Simulate heartbeat updating last_seen.
	_ = r.UpdateHeartbeat("agent-e")

	entry := r.FindAgentByIP("10.100.0.5")
	if entry == nil {
		t.Fatal("expected entry after heartbeat update")
	}
	if entry.LastSeen.IsZero() {
		t.Error("expected non-zero LastSeen")
	}
	_ = entry.LastSeen.Before(time.Now()) // just ensure it's populated
}
