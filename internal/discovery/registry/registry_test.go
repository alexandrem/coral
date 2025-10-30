package registry

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistry_Register(t *testing.T) {
	reg := New(5 * time.Minute)

	t.Run("successful registration", func(t *testing.T) {
		entry, err := reg.Register("mesh-1", "pubkey-1", []string{"10.0.0.1:41820"}, "10.42.0.1", "fd42::1", 9000, map[string]string{"env": "prod"})
		require.NoError(t, err)
		assert.Equal(t, "mesh-1", entry.MeshID)
		assert.Equal(t, "pubkey-1", entry.PubKey)
		assert.Equal(t, []string{"10.0.0.1:41820"}, entry.Endpoints)
		assert.Equal(t, "10.42.0.1", entry.MeshIPv4)
		assert.Equal(t, "fd42::1", entry.MeshIPv6)
		assert.Equal(t, uint32(9000), entry.ConnectPort)
		assert.Equal(t, "prod", entry.Metadata["env"])
		assert.False(t, entry.LastSeen.IsZero())
		assert.False(t, entry.ExpiresAt.IsZero())
	})

	t.Run("empty mesh_id", func(t *testing.T) {
		_, err := reg.Register("", "pubkey-1", []string{"10.0.0.1:41820"}, "", "", 0, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "mesh_id cannot be empty")
	})

	t.Run("empty pubkey", func(t *testing.T) {
		_, err := reg.Register("mesh-1", "", []string{"10.0.0.1:41820"}, "", "", 0, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "pubkey cannot be empty")
	})

	t.Run("no endpoints", func(t *testing.T) {
		_, err := reg.Register("mesh-1", "pubkey-1", []string{}, "", "", 0, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "at least one endpoint is required")
	})

	t.Run("update existing registration", func(t *testing.T) {
		reg := New(5 * time.Minute)

		// Initial registration
		entry1, err := reg.Register("mesh-1", "pubkey-1", []string{"10.0.0.1:41820"}, "10.42.0.1", "fd42::1", 9000, nil)
		require.NoError(t, err)

		time.Sleep(10 * time.Millisecond)

		// Update registration
		entry2, err := reg.Register("mesh-1", "pubkey-2", []string{"10.0.0.2:41820"}, "10.42.0.2", "fd42::2", 9001, map[string]string{"updated": "true"})
		require.NoError(t, err)

		assert.Equal(t, "pubkey-2", entry2.PubKey)
		assert.Equal(t, []string{"10.0.0.2:41820"}, entry2.Endpoints)
		assert.True(t, entry2.LastSeen.After(entry1.LastSeen))
	})
}

func TestRegistry_Lookup(t *testing.T) {
	reg := New(5 * time.Minute)

	t.Run("lookup existing colony", func(t *testing.T) {
		_, err := reg.Register("mesh-1", "pubkey-1", []string{"10.0.0.1:41820"}, "10.42.0.1", "fd42::1", 9000, map[string]string{"env": "prod"})
		require.NoError(t, err)

		entry, err := reg.Lookup("mesh-1")
		require.NoError(t, err)
		assert.Equal(t, "mesh-1", entry.MeshID)
		assert.Equal(t, "pubkey-1", entry.PubKey)
		assert.Equal(t, "prod", entry.Metadata["env"])
	})

	t.Run("lookup nonexistent colony", func(t *testing.T) {
		_, err := reg.Lookup("nonexistent")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "colony not found")
	})

	t.Run("empty mesh_id", func(t *testing.T) {
		_, err := reg.Lookup("")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "mesh_id cannot be empty")
	})

	t.Run("lookup expired colony", func(t *testing.T) {
		reg := New(50 * time.Millisecond)

		_, err := reg.Register("mesh-expire", "pubkey-1", []string{"10.0.0.1:41820"}, "", "", 0, nil)
		require.NoError(t, err)

		// Wait for expiration
		time.Sleep(100 * time.Millisecond)

		_, err = reg.Lookup("mesh-expire")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "expired")
	})
}

func TestRegistry_Count(t *testing.T) {
	reg := New(5 * time.Minute)

	assert.Equal(t, 0, reg.Count())

	reg.Register("mesh-1", "pubkey-1", []string{"10.0.0.1:41820"}, "", "", 0, nil)
	assert.Equal(t, 1, reg.Count())

	reg.Register("mesh-2", "pubkey-2", []string{"10.0.0.2:41820"}, "", "", 0, nil)
	assert.Equal(t, 2, reg.Count())
}

func TestRegistry_CountActive(t *testing.T) {
	reg := New(50 * time.Millisecond)

	// Register two colonies
	reg.Register("mesh-1", "pubkey-1", []string{"10.0.0.1:41820"}, "", "", 0, nil)
	reg.Register("mesh-2", "pubkey-2", []string{"10.0.0.2:41820"}, "", "", 0, nil)

	assert.Equal(t, 2, reg.CountActive())

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, 0, reg.CountActive())
}

func TestRegistry_Cleanup(t *testing.T) {
	reg := New(50 * time.Millisecond)

	// Register two colonies
	reg.Register("mesh-1", "pubkey-1", []string{"10.0.0.1:41820"}, "", "", 0, nil)
	reg.Register("mesh-2", "pubkey-2", []string{"10.0.0.2:41820"}, "", "", 0, nil)

	assert.Equal(t, 2, reg.Count())

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Run cleanup
	removed := reg.Cleanup()
	assert.Equal(t, 2, removed)
	assert.Equal(t, 0, reg.Count())
}

func TestRegistry_StartCleanup(t *testing.T) {
	reg := New(50 * time.Millisecond)

	// Register colonies
	reg.Register("mesh-1", "pubkey-1", []string{"10.0.0.1:41820"}, "", "", 0, nil)
	reg.Register("mesh-2", "pubkey-2", []string{"10.0.0.2:41820"}, "", "", 0, nil)

	assert.Equal(t, 2, reg.Count())

	// Start background cleanup
	stopCh := make(chan struct{})
	t.Cleanup(func() {
		close(stopCh)
	})

	go reg.StartCleanup(100*time.Millisecond, stopCh)

	// Wait for cleanup to run
	time.Sleep(200 * time.Millisecond)

	// Should be cleaned up
	assert.Equal(t, 0, reg.Count())
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	reg := New(5 * time.Minute)
	done := make(chan bool)

	// Concurrent writes
	for i := 0; i < 10; i++ {
		go func(id int) {
			reg.Register(
				"mesh-concurrent",
				"pubkey",
				[]string{"10.0.0.1:41820"},
				"", "", 0,
				nil,
			)
			done <- true
		}(i)
	}

	// Wait for all writes
	for i := 0; i < 10; i++ {
		<-done
	}

	// Concurrent reads
	for i := 0; i < 10; i++ {
		go func() {
			reg.Lookup("mesh-concurrent")
			done <- true
		}()
	}

	// Wait for all reads
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should have exactly 1 entry (all writes to same mesh_id)
	assert.Equal(t, 1, reg.Count())
}
