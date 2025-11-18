package registry

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	discoveryv1 "github.com/coral-io/coral/coral/discovery/v1"
)

func TestRegistry_Register(t *testing.T) {
	reg := New(5 * time.Minute)

	t.Run("successful registration", func(t *testing.T) {
		entry, err := reg.Register("mesh-1", "pubkey-1", []string{"10.0.0.1:41820"}, "100.64.0.1", "fd42::1", 9000, map[string]string{"env": "prod"}, nil, discoveryv1.NatHint_NAT_UNKNOWN)
		require.NoError(t, err)
		assert.Equal(t, "mesh-1", entry.MeshID)
		assert.Equal(t, "pubkey-1", entry.PubKey)
		assert.Equal(t, []string{"10.0.0.1:41820"}, entry.Endpoints)
		assert.Equal(t, "100.64.0.1", entry.MeshIPv4)
		assert.Equal(t, "fd42::1", entry.MeshIPv6)
		assert.Equal(t, uint32(9000), entry.ConnectPort)
		assert.Equal(t, "prod", entry.Metadata["env"])
		assert.False(t, entry.LastSeen.IsZero())
		assert.False(t, entry.ExpiresAt.IsZero())
	})

	t.Run("empty mesh_id", func(t *testing.T) {
		_, err := reg.Register("", "pubkey-1", []string{"10.0.0.1:41820"}, "", "", 0, nil, nil, discoveryv1.NatHint_NAT_UNKNOWN)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "mesh_id cannot be empty")
	})

	t.Run("empty pubkey", func(t *testing.T) {
		_, err := reg.Register("mesh-1", "", []string{"10.0.0.1:41820"}, "", "", 0, nil, nil, discoveryv1.NatHint_NAT_UNKNOWN)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "pubkey cannot be empty")
	})

	t.Run("no endpoints and no observed endpoint", func(t *testing.T) {
		_, err := reg.Register("mesh-1", "pubkey-1", []string{}, "", "", 0, nil, nil, discoveryv1.NatHint_NAT_UNKNOWN)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "at least one endpoint or observed endpoint is required")
	})

	t.Run("no endpoints but has observed endpoint", func(t *testing.T) {
		observedEndpoint := &discoveryv1.Endpoint{
			Ip:       "1.2.3.4",
			Port:     51820,
			Protocol: "udp",
		}
		entry, err := reg.Register("agent-1", "pubkey-agent", []string{}, "", "", 0, nil, observedEndpoint, discoveryv1.NatHint_NAT_CONE)
		assert.NoError(t, err)
		assert.NotNil(t, entry)
		assert.Equal(t, "agent-1", entry.MeshID)
		assert.Equal(t, observedEndpoint, entry.ObservedEndpoint)
	})

	t.Run("update with same pubkey succeeds (renewal)", func(t *testing.T) {
		reg := New(5 * time.Minute)

		// Initial registration
		entry1, err := reg.Register("mesh-1", "pubkey-1", []string{"10.0.0.1:41820"}, "100.64.0.1", "fd42::1", 9000, nil, nil, discoveryv1.NatHint_NAT_UNKNOWN)
		require.NoError(t, err)

		time.Sleep(10 * time.Millisecond)

		// Update with same pubkey (should succeed - this is a renewal)
		entry2, err := reg.Register("mesh-1", "pubkey-1", []string{"10.0.0.2:41820"}, "100.64.0.2", "fd42::2", 9001, map[string]string{"updated": "true"}, nil, discoveryv1.NatHint_NAT_UNKNOWN)
		require.NoError(t, err)

		assert.Equal(t, "pubkey-1", entry2.PubKey)
		assert.Equal(t, []string{"10.0.0.2:41820"}, entry2.Endpoints)
		assert.True(t, entry2.LastSeen.After(entry1.LastSeen))
	})
}

func TestRegistry_Lookup(t *testing.T) {
	reg := New(5 * time.Minute)

	t.Run("lookup existing colony", func(t *testing.T) {
		_, err := reg.Register("mesh-1", "pubkey-1", []string{"10.0.0.1:41820"}, "100.64.0.1", "fd42::1", 9000, map[string]string{"env": "prod"}, nil, discoveryv1.NatHint_NAT_UNKNOWN)
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

		_, err := reg.Register("mesh-expire", "pubkey-1", []string{"10.0.0.1:41820"}, "", "", 0, nil, nil, discoveryv1.NatHint_NAT_UNKNOWN)
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

	reg.Register("mesh-1", "pubkey-1", []string{"10.0.0.1:41820"}, "", "", 0, nil, nil, discoveryv1.NatHint_NAT_UNKNOWN)
	assert.Equal(t, 1, reg.Count())

	reg.Register("mesh-2", "pubkey-2", []string{"10.0.0.2:41820"}, "", "", 0, nil, nil, discoveryv1.NatHint_NAT_UNKNOWN)
	assert.Equal(t, 2, reg.Count())
}

func TestRegistry_CountActive(t *testing.T) {
	reg := New(50 * time.Millisecond)

	// Register two colonies
	reg.Register("mesh-1", "pubkey-1", []string{"10.0.0.1:41820"}, "", "", 0, nil, nil, discoveryv1.NatHint_NAT_UNKNOWN)
	reg.Register("mesh-2", "pubkey-2", []string{"10.0.0.2:41820"}, "", "", 0, nil, nil, discoveryv1.NatHint_NAT_UNKNOWN)

	assert.Equal(t, 2, reg.CountActive())

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, 0, reg.CountActive())
}

func TestRegistry_Cleanup(t *testing.T) {
	reg := New(50 * time.Millisecond)

	// Register two colonies
	reg.Register("mesh-1", "pubkey-1", []string{"10.0.0.1:41820"}, "", "", 0, nil, nil, discoveryv1.NatHint_NAT_UNKNOWN)
	reg.Register("mesh-2", "pubkey-2", []string{"10.0.0.2:41820"}, "", "", 0, nil, nil, discoveryv1.NatHint_NAT_UNKNOWN)

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
	reg.Register("mesh-1", "pubkey-1", []string{"10.0.0.1:41820"}, "", "", 0, nil, nil, discoveryv1.NatHint_NAT_UNKNOWN)
	reg.Register("mesh-2", "pubkey-2", []string{"10.0.0.2:41820"}, "", "", 0, nil, nil, discoveryv1.NatHint_NAT_UNKNOWN)

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
				nil,
				discoveryv1.NatHint_NAT_UNKNOWN,
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

func TestRegistry_SplitBrainDetection(t *testing.T) {
	reg := New(5 * time.Minute)

	t.Run("prevent duplicate registration with different pubkey", func(t *testing.T) {
		// First registration
		_, err := reg.Register("mesh-1", "pubkey-1", []string{"10.0.0.1:41820"}, "", "", 0, nil, nil, discoveryv1.NatHint_NAT_UNKNOWN)
		require.NoError(t, err)

		// Try to register same mesh_id with different pubkey (should fail)
		_, err = reg.Register("mesh-1", "pubkey-2", []string{"10.0.0.2:41820"}, "", "", 0, nil, nil, discoveryv1.NatHint_NAT_UNKNOWN)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already registered with different public key")
		assert.Contains(t, err.Error(), "split-brain")
	})

	t.Run("allow update with same pubkey (renewal)", func(t *testing.T) {
		reg := New(5 * time.Minute)

		// First registration
		entry1, err := reg.Register("mesh-1", "pubkey-1", []string{"10.0.0.1:41820"}, "", "", 0, nil, nil, discoveryv1.NatHint_NAT_UNKNOWN)
		require.NoError(t, err)

		time.Sleep(10 * time.Millisecond)

		// Update with same pubkey (should succeed)
		entry2, err := reg.Register("mesh-1", "pubkey-1", []string{"10.0.0.2:41820"}, "", "", 0, map[string]string{"updated": "true"}, nil, discoveryv1.NatHint_NAT_UNKNOWN)
		require.NoError(t, err)
		assert.Equal(t, "pubkey-1", entry2.PubKey)
		assert.Equal(t, []string{"10.0.0.2:41820"}, entry2.Endpoints)
		assert.True(t, entry2.LastSeen.After(entry1.LastSeen))
	})

	t.Run("allow registration after expiration", func(t *testing.T) {
		reg := New(50 * time.Millisecond)

		// First registration
		_, err := reg.Register("mesh-1", "pubkey-1", []string{"10.0.0.1:41820"}, "", "", 0, nil, nil, discoveryv1.NatHint_NAT_UNKNOWN)
		require.NoError(t, err)

		// Wait for expiration
		time.Sleep(100 * time.Millisecond)

		// Register with different pubkey (should succeed after expiration)
		_, err = reg.Register("mesh-1", "pubkey-2", []string{"10.0.0.2:41820"}, "", "", 0, nil, nil, discoveryv1.NatHint_NAT_UNKNOWN)
		assert.NoError(t, err)
	})
}

func TestRegistry_EndpointValidation(t *testing.T) {
	reg := New(5 * time.Minute)

	t.Run("reject observed endpoint with port 0 and no static endpoints", func(t *testing.T) {
		// HTTP-extracted endpoint without port info
		observedEndpoint := &discoveryv1.Endpoint{
			Ip:       "1.2.3.4",
			Port:     0, // No port info from HTTP headers
			Protocol: "udp",
		}

		_, err := reg.Register("agent-1", "pubkey-1", []string{}, "", "", 0, nil, observedEndpoint, discoveryv1.NatHint_NAT_CONE)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "observed endpoint must have a valid port")
		assert.Contains(t, err.Error(), "STUN discovery")
	})

	t.Run("accept observed endpoint with valid port and no static endpoints", func(t *testing.T) {
		// STUN-discovered endpoint with port info
		observedEndpoint := &discoveryv1.Endpoint{
			Ip:       "1.2.3.4",
			Port:     51820,
			Protocol: "udp",
		}

		entry, err := reg.Register("agent-1", "pubkey-1", []string{}, "", "", 0, nil, observedEndpoint, discoveryv1.NatHint_NAT_CONE)
		assert.NoError(t, err)
		assert.NotNil(t, entry)
		assert.Equal(t, observedEndpoint, entry.ObservedEndpoint)
	})

	t.Run("accept static endpoints even with observed endpoint port 0", func(t *testing.T) {
		// Colonies can have static endpoints + HTTP-extracted observed endpoint
		observedEndpoint := &discoveryv1.Endpoint{
			Ip:       "1.2.3.4",
			Port:     0,
			Protocol: "udp",
		}

		entry, err := reg.Register("colony-1", "pubkey-1", []string{"10.0.0.1:41820"}, "", "", 0, nil, observedEndpoint, discoveryv1.NatHint_NAT_CONE)
		assert.NoError(t, err)
		assert.NotNil(t, entry)
	})
}

func TestRegistry_RelayManagement(t *testing.T) {
	reg := New(5 * time.Minute)

	t.Run("allocate and lookup relay session", func(t *testing.T) {
		relayEndpoint := &discoveryv1.Endpoint{
			Ip:       "relay.example.com",
			Port:     3478,
			Protocol: "udp",
			ViaRelay: true,
		}

		session, err := reg.AllocateRelay("session-1", "mesh-1", "agent-key", "colony-key", relayEndpoint, "relay-1")
		require.NoError(t, err)
		assert.Equal(t, "session-1", session.SessionID)
		assert.Equal(t, "mesh-1", session.MeshID)
		assert.Equal(t, relayEndpoint, session.RelayEndpoint)

		// Lookup the session
		found, err := reg.LookupRelaySession("session-1")
		require.NoError(t, err)
		assert.Equal(t, session.SessionID, found.SessionID)
	})

	t.Run("release relay session", func(t *testing.T) {
		relayEndpoint := &discoveryv1.Endpoint{
			Ip:       "relay.example.com",
			Port:     3478,
			Protocol: "udp",
		}

		_, err := reg.AllocateRelay("session-2", "mesh-1", "agent-key", "colony-key", relayEndpoint, "relay-1")
		require.NoError(t, err)

		// Release the session
		err = reg.ReleaseRelay("session-2")
		assert.NoError(t, err)

		// Lookup should fail
		_, err = reg.LookupRelaySession("session-2")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("lookup expired relay session", func(t *testing.T) {
		// Create registry with short relay TTL
		reg := New(5 * time.Minute)
		reg.relayTTL = 50 * time.Millisecond

		relayEndpoint := &discoveryv1.Endpoint{
			Ip:       "relay.example.com",
			Port:     3478,
			Protocol: "udp",
		}

		_, err := reg.AllocateRelay("session-3", "mesh-1", "agent-key", "colony-key", relayEndpoint, "relay-1")
		require.NoError(t, err)

		// Wait for expiration
		time.Sleep(100 * time.Millisecond)

		// Lookup should fail
		_, err = reg.LookupRelaySession("session-3")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "expired")
	})

	t.Run("cleanup expired relay sessions", func(t *testing.T) {
		reg := New(5 * time.Minute)
		reg.relayTTL = 50 * time.Millisecond

		relayEndpoint := &discoveryv1.Endpoint{
			Ip:       "relay.example.com",
			Port:     3478,
			Protocol: "udp",
		}

		// Allocate multiple sessions
		reg.AllocateRelay("session-4", "mesh-1", "agent-key", "colony-key", relayEndpoint, "relay-1")
		reg.AllocateRelay("session-5", "mesh-1", "agent-key", "colony-key", relayEndpoint, "relay-1")

		// Wait for expiration
		time.Sleep(100 * time.Millisecond)

		// Cleanup
		removed := reg.CleanupRelaySessions()
		assert.Equal(t, 2, removed)
	})
}
