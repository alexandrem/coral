package registry

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistry_Register(t *testing.T) {
	reg := New()

	t.Run("successful registration", func(t *testing.T) {
		entry, err := reg.Register("agent-1", "frontend", "100.64.0.2", "fd42::2", nil, nil, "")
		require.NoError(t, err)
		assert.Equal(t, "agent-1", entry.AgentID)
		assert.Equal(t, "frontend", entry.Name)
		assert.Equal(t, "100.64.0.2", entry.MeshIPv4)
		assert.Equal(t, "fd42::2", entry.MeshIPv6)
		assert.False(t, entry.RegisteredAt.IsZero())
		assert.False(t, entry.LastSeen.IsZero())
		assert.Equal(t, entry.RegisteredAt, entry.LastSeen)
	})

	t.Run("empty agent_id", func(t *testing.T) {
		_, err := reg.Register("", "frontend", "100.64.0.2", "fd42::2", nil, nil, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "agent_id cannot be empty")
	})

	t.Run("empty name and services", func(t *testing.T) {
		// Agents can register without services - they may add them later.
		entry, err := reg.Register("agent-1", "", "100.64.0.2", "fd42::2", nil, nil, "")
		assert.NoError(t, err)
		assert.NotNil(t, entry)
		assert.Equal(t, "agent-1", entry.AgentID)
		assert.Equal(t, "", entry.Name)
		assert.Equal(t, 0, len(entry.Services))
	})

	t.Run("update existing registration", func(t *testing.T) {
		reg := New()

		// Initial registration.
		entry1, err := reg.Register("agent-1", "frontend", "100.64.0.2", "fd42::2", nil, nil, "")
		require.NoError(t, err)

		initialLastSeen := entry1.LastSeen
		initialRegisteredAt := entry1.RegisteredAt

		time.Sleep(10 * time.Millisecond)

		// Update registration (re-register with new IPs).
		entry2, err := reg.Register("agent-1", "frontend-v2", "100.64.0.3", "fd42::3", nil, nil, "")
		require.NoError(t, err)

		assert.Equal(t, "frontend-v2", entry2.Name)
		assert.Equal(t, "100.64.0.3", entry2.MeshIPv4)
		assert.Equal(t, "fd42::3", entry2.MeshIPv6)
		assert.True(t, entry2.LastSeen.After(initialLastSeen))
		assert.Equal(t, initialRegisteredAt, entry2.RegisteredAt) // RegisteredAt should not change.
	})
}

func TestRegistry_UpdateHeartbeat(t *testing.T) {
	reg := New()

	t.Run("update existing agent", func(t *testing.T) {
		entry, err := reg.Register("agent-1", "frontend", "100.64.0.2", "fd42::2", nil, nil, "")
		require.NoError(t, err)

		originalLastSeen := entry.LastSeen
		time.Sleep(10 * time.Millisecond)

		err = reg.UpdateHeartbeat("agent-1")
		require.NoError(t, err)

		updatedEntry, err := reg.Get("agent-1")
		require.NoError(t, err)
		assert.True(t, updatedEntry.LastSeen.After(originalLastSeen))
	})

	t.Run("update nonexistent agent", func(t *testing.T) {
		err := reg.UpdateHeartbeat("nonexistent")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "agent not found")
	})

	t.Run("empty agent_id", func(t *testing.T) {
		err := reg.UpdateHeartbeat("")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "agent_id cannot be empty")
	})
}

func TestRegistry_Get(t *testing.T) {
	reg := New()

	t.Run("get existing agent", func(t *testing.T) {
		_, err := reg.Register("agent-1", "frontend", "100.64.0.2", "fd42::2", nil, nil, "")
		require.NoError(t, err)

		entry, err := reg.Get("agent-1")
		require.NoError(t, err)
		assert.Equal(t, "agent-1", entry.AgentID)
		assert.Equal(t, "frontend", entry.Name)
	})

	t.Run("get nonexistent agent", func(t *testing.T) {
		_, err := reg.Get("nonexistent")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "agent not found")
	})

	t.Run("empty agent_id", func(t *testing.T) {
		_, err := reg.Get("")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "agent_id cannot be empty")
	})
}

func TestRegistry_ListAll(t *testing.T) {
	reg := New()

	t.Run("empty registry", func(t *testing.T) {
		entries := reg.ListAll()
		assert.Empty(t, entries)
	})

	t.Run("multiple agents", func(t *testing.T) {
		_, _ = reg.Register("agent-1", "frontend", "100.64.0.2", "fd42::2", nil, nil, "")
		_, _ = reg.Register("agent-2", "api", "100.64.0.3", "fd42::3", nil, nil, "")
		_, _ = reg.Register("agent-3", "worker", "100.64.0.4", "fd42::4", nil, nil, "")

		entries := reg.ListAll()
		assert.Len(t, entries, 3)

		// Verify all agents are present.
		agentIDs := make(map[string]bool)
		for _, entry := range entries {
			agentIDs[entry.AgentID] = true
		}
		assert.True(t, agentIDs["agent-1"])
		assert.True(t, agentIDs["agent-2"])
		assert.True(t, agentIDs["agent-3"])
	})
}

func TestRegistry_Count(t *testing.T) {
	reg := New()

	assert.Equal(t, 0, reg.Count())

	_, _ = reg.Register("agent-1", "frontend", "100.64.0.2", "fd42::2", nil, nil, "")
	assert.Equal(t, 1, reg.Count())

	_, _ = reg.Register("agent-2", "api", "100.64.0.3", "fd42::3", nil, nil, "")
	assert.Equal(t, 2, reg.Count())

	// Re-registering same agent shouldn't increase count.
	_, _ = reg.Register("agent-1", "frontend", "100.64.0.2", "fd42::2", nil, nil, "")
	assert.Equal(t, 2, reg.Count())
}

func TestRegistry_CountActive(t *testing.T) {
	reg := New()

	t.Run("all healthy agents", func(t *testing.T) {
		_, _ = reg.Register("agent-1", "frontend", "100.64.0.2", "fd42::2", nil, nil, "")
		_, _ = reg.Register("agent-2", "api", "100.64.0.3", "fd42::3", nil, nil, "")

		assert.Equal(t, 2, reg.CountActive())
	})

	t.Run("mixed status agents", func(t *testing.T) {
		reg := New()

		// Register agents.
		_, _ = reg.Register("agent-healthy", "frontend", "100.64.0.2", "fd42::2", nil, nil, "")
		_, _ = reg.Register("agent-degraded", "api", "100.64.0.3", "fd42::3", nil, nil, "")
		_, _ = reg.Register("agent-unhealthy", "worker", "100.64.0.4", "fd42::4", nil, nil, "")

		// Manually adjust LastSeen timestamps to simulate different statuses.
		now := time.Now()
		entries := reg.ListAll()
		for _, entry := range entries {
			switch entry.AgentID {
			case "agent-healthy":
				entry.LastSeen = now.Add(-10 * time.Second) // Healthy.
			case "agent-degraded":
				entry.LastSeen = now.Add(-60 * time.Second) // Degraded.
			case "agent-unhealthy":
				entry.LastSeen = now.Add(-5 * time.Minute) // Unhealthy.
			}
		}

		// Should count healthy and degraded (not unhealthy).
		assert.Equal(t, 2, reg.CountActive())
	})
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	reg := New()
	done := make(chan bool)

	// Concurrent writes.
	for i := 0; i < 10; i++ {
		go func(id int) {
			_, _ = reg.Register("agent-concurrent", "frontend", "100.64.0.2", "fd42::2", nil, nil, "")
			done <- true
		}(i)
	}

	// Wait for all writes.
	for i := 0; i < 10; i++ {
		<-done
	}

	// Concurrent reads.
	for i := 0; i < 10; i++ {
		go func() {
			_, _ = reg.Get("agent-concurrent")
			done <- true
		}()
	}

	// Wait for all reads.
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should have exactly 1 entry (all writes to same agent_id).
	assert.Equal(t, 1, reg.Count())
}

func TestDetermineStatus(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		lastSeen time.Time
		expected AgentStatus
	}{
		{
			name:     "healthy - just seen",
			lastSeen: now,
			expected: StatusHealthy,
		},
		{
			name:     "healthy - 10 seconds ago",
			lastSeen: now.Add(-10 * time.Second),
			expected: StatusHealthy,
		},
		{
			name:     "healthy - 29 seconds ago",
			lastSeen: now.Add(-29 * time.Second),
			expected: StatusHealthy,
		},
		{
			name:     "degraded - 30 seconds ago",
			lastSeen: now.Add(-30 * time.Second),
			expected: StatusDegraded,
		},
		{
			name:     "degraded - 1 minute ago",
			lastSeen: now.Add(-1 * time.Minute),
			expected: StatusDegraded,
		},
		{
			name:     "degraded - 119 seconds ago",
			lastSeen: now.Add(-119 * time.Second),
			expected: StatusDegraded,
		},
		{
			name:     "unhealthy - 2 minutes ago",
			lastSeen: now.Add(-2 * time.Minute),
			expected: StatusUnhealthy,
		},
		{
			name:     "unhealthy - 5 minutes ago",
			lastSeen: now.Add(-5 * time.Minute),
			expected: StatusUnhealthy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := DetermineStatus(tt.lastSeen, now)
			assert.Equal(t, tt.expected, status)
		})
	}
}
