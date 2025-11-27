package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	discoverypb "github.com/coral-mesh/coral/coral/discovery/v1"
	"github.com/coral-mesh/coral/internal/logging"
)

// TestConnectionManager_GetColonyEndpoint tests the agent's colony endpoint selection logic
// for multi-public endpoint scenarios.
func TestConnectionManager_GetColonyEndpoint(t *testing.T) {
	logger := logging.NewWithComponent(logging.Config{
		Level:  "error", // Reduce noise in tests
		Pretty: false,
	}, "endpoint-test")

	tests := []struct {
		name             string
		colonyInfo       *discoverypb.LookupColonyResponse
		lastSuccessful   string
		expectedEndpoint string
		description      string
	}{
		{
			name: "skip localhost in observed endpoints",
			colonyInfo: &discoverypb.LookupColonyResponse{
				MeshId: "test-colony",
				Pubkey: "test-pubkey",
				ObservedEndpoints: []*discoverypb.Endpoint{
					{Ip: "127.0.0.1", Port: 41580},   // Should be skipped (loopback)
					{Ip: "192.168.5.2", Port: 41820}, // Should be selected
				},
			},
			lastSuccessful:   "",
			expectedEndpoint: "192.168.5.2:41820",
			description:      "Should skip localhost in observed endpoints and use first non-localhost",
		},
		{
			name: "skip localhost in regular endpoints",
			colonyInfo: &discoverypb.LookupColonyResponse{
				MeshId: "test-colony",
				Pubkey: "test-pubkey",
				Endpoints: []string{
					"127.0.0.1:9000",   // Should be skipped
					"192.168.5.2:9000", // Should be selected
					"10.0.0.5:9000",    // Alternative
				},
				Metadata: map[string]string{
					"wireguard_port": "41820",
				},
			},
			lastSuccessful:   "",
			expectedEndpoint: "192.168.5.2:41820",
			description:      "Should skip localhost in regular endpoints and use first non-localhost",
		},
		{
			name: "skip multiple localhost variants",
			colonyInfo: &discoverypb.LookupColonyResponse{
				MeshId: "test-colony",
				Pubkey: "test-pubkey",
				Endpoints: []string{
					"127.0.0.1:9000",
					"::1:9000", // IPv6 localhost
					"192.168.5.2:9000",
				},
				Metadata: map[string]string{
					"wireguard_port": "41820",
				},
			},
			lastSuccessful:   "",
			expectedEndpoint: "192.168.5.2:41820",
			description:      "Should skip both IPv4 and IPv6 localhost",
		},
		{
			name: "prefer observed endpoints over regular",
			colonyInfo: &discoverypb.LookupColonyResponse{
				MeshId: "test-colony",
				Pubkey: "test-pubkey",
				ObservedEndpoints: []*discoverypb.Endpoint{
					{Ip: "203.0.113.10", Port: 41820}, // Should be selected (observed)
				},
				Endpoints: []string{
					"192.168.5.2:9000", // Should NOT be selected (regular)
				},
			},
			lastSuccessful:   "",
			expectedEndpoint: "203.0.113.10:41820",
			description:      "Should prefer observed endpoints over regular endpoints",
		},
		{
			name: "reuse last successful endpoint if valid",
			colonyInfo: &discoverypb.LookupColonyResponse{
				MeshId: "test-colony",
				Pubkey: "test-pubkey",
				Endpoints: []string{
					"192.168.5.2:9000",
					"10.0.0.5:9000",
					"203.0.113.10:9000",
				},
				Metadata: map[string]string{
					"wireguard_port": "41820",
				},
			},
			lastSuccessful:   "10.0.0.5:41820", // Should be selected even though not first
			expectedEndpoint: "10.0.0.5:41820",
			description:      "Should reuse last successful endpoint if still in list",
		},
		{
			name: "reuse_last_successful_localhost_for_same_host",
			colonyInfo: &discoverypb.LookupColonyResponse{
				MeshId: "test-colony",
				Pubkey: "test-pubkey",
				Endpoints: []string{
					"127.0.0.1:9000",
					"192.168.5.2:9000",
				},
				Metadata: map[string]string{
					"wireguard_port": "41820",
				},
			},
			lastSuccessful:   "127.0.0.1:41820", // Was successful before - honor it (same-host deployment)
			expectedEndpoint: "127.0.0.1:41820",
			description:      "Should reuse last successful localhost endpoint (same-host deployment)",
		},
		{
			name: "use localhost as last resort for same_host deployment",
			colonyInfo: &discoverypb.LookupColonyResponse{
				MeshId: "test-colony",
				Pubkey: "test-pubkey",
				Endpoints: []string{
					"127.0.0.1:9000",
					"::1:9000",
				},
				Metadata: map[string]string{
					"wireguard_port": "41820",
				},
			},
			lastSuccessful:   "",
			expectedEndpoint: "127.0.0.1:41820", // Use localhost as last resort (same-host deployment)
			description:      "Should use localhost as last resort (same-host deployment)",
		},
		{
			name: "extract WireGuard port from observed endpoints",
			colonyInfo: &discoverypb.LookupColonyResponse{
				MeshId: "test-colony",
				Pubkey: "test-pubkey",
				ObservedEndpoints: []*discoverypb.Endpoint{
					{Ip: "192.168.5.2", Port: 12345}, // Custom WireGuard port
				},
			},
			lastSuccessful:   "",
			expectedEndpoint: "192.168.5.2:12345",
			description:      "Should use port from observed endpoint",
		},
		{
			name: "extract WireGuard port from metadata for regular endpoints",
			colonyInfo: &discoverypb.LookupColonyResponse{
				MeshId: "test-colony",
				Pubkey: "test-pubkey",
				Endpoints: []string{
					"192.168.5.2:9000", // HTTP port
				},
				Metadata: map[string]string{
					"wireguard_port": "54321", // WireGuard port in metadata
				},
			},
			lastSuccessful:   "",
			expectedEndpoint: "192.168.5.2:54321",
			description:      "Should extract WireGuard port from metadata",
		},
		{
			name: "use default port when not specified",
			colonyInfo: &discoverypb.LookupColonyResponse{
				MeshId: "test-colony",
				Pubkey: "test-pubkey",
				Endpoints: []string{
					"192.168.5.2:9000",
				},
			},
			lastSuccessful:   "",
			expectedEndpoint: "192.168.5.2:51820", // Default WireGuard port
			description:      "Should use default WireGuard port 51820 when not specified",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create minimal connection manager
			cm := NewConnectionManager(
				"test-agent",
				tt.colonyInfo,
				nil, // config
				nil, // service specs
				"test-pubkey",
				nil, // wg device
				nil, // runtimeService
				logger,
			)

			// Set last successful endpoint if provided
			if tt.lastSuccessful != "" {
				cm.SetLastSuccessfulEndpoint(tt.lastSuccessful)
			}

			// Get colony endpoint
			endpoint := cm.GetColonyEndpoint()

			if tt.expectedEndpoint == "" {
				assert.Empty(t, endpoint, tt.description)
			} else {
				assert.Equal(t, tt.expectedEndpoint, endpoint, tt.description)
			}

			t.Logf("✓ %s", tt.description)
		})
	}
}

// TestConnectionManager_GetColonyEndpoint_RealWorldScenario tests real-world scenarios
// matching the original bug report.
func TestConnectionManager_GetColonyEndpoint_RealWorldScenario(t *testing.T) {
	logger := logging.NewWithComponent(logging.Config{
		Level:  "error",
		Pretty: false,
	}, "real-world-test")

	t.Run("original bug - localhost first in endpoints", func(t *testing.T) {
		// Simulates the original bug scenario:
		// - Colony registered with endpoints: [127.0.0.1:41580, <public-ip>:9000, ...]
		// - Agent should NOT select localhost as WireGuard peer endpoint
		// - Agent is running in container and cannot reach colony at localhost
		colonyInfo := &discoverypb.LookupColonyResponse{
			MeshId: "test-colony",
			Pubkey: "test-pubkey",
			Endpoints: []string{
				"127.0.0.1:9000",   // This was being incorrectly selected
				"192.168.5.2:9000", // Agent should select this
				"10.0.0.5:9000",    // Alternative
			},
			Metadata: map[string]string{
				"wireguard_port": "41820",
			},
		}

		cm := NewConnectionManager(
			"container-agent",
			colonyInfo,
			nil,
			nil,
			"test-pubkey",
			nil,
			nil, // runtimeService
			logger,
		)

		endpoint := cm.GetColonyEndpoint()

		require.NotEmpty(t, endpoint, "Should select a valid endpoint")
		assert.NotContains(t, endpoint, "127.0.0.1", "Should NOT select localhost")
		assert.Equal(t, "192.168.5.2:41820", endpoint, "Should select first non-localhost endpoint")

		t.Logf("✓ Original bug fixed: skipped localhost, selected %s", endpoint)
	})

	t.Run("docker-compose scenario - multiple networks", func(t *testing.T) {
		// Docker Compose scenario where colony has multiple network interfaces
		colonyInfo := &discoverypb.LookupColonyResponse{
			MeshId: "test-colony",
			Pubkey: "test-pubkey",
			ObservedEndpoints: []*discoverypb.Endpoint{
				{Ip: "127.0.0.1", Port: 41580},   // Loopback - should be skipped
				{Ip: "172.18.0.10", Port: 41820}, // Docker network - should be selected
			},
		}

		cm := NewConnectionManager(
			"docker-agent",
			colonyInfo,
			nil,
			nil,
			"test-pubkey",
			nil,
			nil, // runtimeService
			logger,
		)

		endpoint := cm.GetColonyEndpoint()

		require.NotEmpty(t, endpoint)
		assert.Equal(t, "172.18.0.10:41820", endpoint, "Should select Docker network endpoint")

		t.Logf("✓ Docker Compose scenario: correctly selected %s", endpoint)
	})

	t.Run("production scenario - NAT traversal with STUN", func(t *testing.T) {
		// Production scenario where colony has STUN-discovered public endpoint
		colonyInfo := &discoverypb.LookupColonyResponse{
			MeshId: "test-colony",
			Pubkey: "test-pubkey",
			ObservedEndpoints: []*discoverypb.Endpoint{
				{Ip: "203.0.113.10", Port: 41820}, // STUN-discovered public IP
			},
			Endpoints: []string{
				"127.0.0.1:9000",     // Local endpoint - should be ignored
				"192.168.1.100:9000", // Private network - should be ignored
			},
		}

		cm := NewConnectionManager(
			"remote-agent",
			colonyInfo,
			nil,
			nil,
			"test-pubkey",
			nil,
			nil, // runtimeService
			logger,
		)

		endpoint := cm.GetColonyEndpoint()

		require.NotEmpty(t, endpoint)
		assert.Equal(t, "203.0.113.10:41820", endpoint, "Should prefer STUN-discovered endpoint")

		t.Logf("✓ Production scenario: correctly selected STUN endpoint %s", endpoint)
	})

	t.Run("endpoint failover scenario", func(t *testing.T) {
		// Scenario where agent previously connected to one endpoint,
		// but now needs to failover to another
		colonyInfo := &discoverypb.LookupColonyResponse{
			MeshId: "test-colony",
			Pubkey: "test-pubkey",
			Endpoints: []string{
				"192.168.5.2:9000",  // Primary
				"10.0.0.5:9000",     // Secondary - was last successful
				"203.0.113.10:9000", // Tertiary
			},
			Metadata: map[string]string{
				"wireguard_port": "41820",
			},
		}

		cm := NewConnectionManager(
			"failover-agent",
			colonyInfo,
			nil,
			nil,
			"test-pubkey",
			nil,
			nil, // runtimeService
			logger,
		)
		cm.SetLastSuccessfulEndpoint("10.0.0.5:41820")

		endpoint := cm.GetColonyEndpoint()

		require.NotEmpty(t, endpoint)
		assert.Equal(t, "10.0.0.5:41820", endpoint, "Should reuse last successful endpoint")

		t.Logf("✓ Failover scenario: correctly reused last successful endpoint %s", endpoint)
	})
}

// TestConnectionManager_GetColonyEndpoint_EdgeCases tests edge cases and error handling.
func TestConnectionManager_GetColonyEndpoint_EdgeCases(t *testing.T) {
	logger := logging.NewWithComponent(logging.Config{
		Level:  "error",
		Pretty: false,
	}, "edge-case-test")

	t.Run("no colony info", func(t *testing.T) {
		cm := NewConnectionManager(
			"test-agent",
			nil, // No colony info
			nil,
			nil,
			"test-pubkey",
			nil,
			nil, // runtimeService
			logger,
		)

		endpoint := cm.GetColonyEndpoint()
		assert.Empty(t, endpoint, "Should return empty when no colony info")
	})

	t.Run("empty endpoints lists", func(t *testing.T) {
		colonyInfo := &discoverypb.LookupColonyResponse{
			MeshId:            "test-colony",
			Pubkey:            "test-pubkey",
			ObservedEndpoints: []*discoverypb.Endpoint{},
			Endpoints:         []string{},
		}

		cm := NewConnectionManager(
			"test-agent",
			colonyInfo,
			nil,
			nil,
			"test-pubkey",
			nil,
			nil, // runtimeService
			logger,
		)

		endpoint := cm.GetColonyEndpoint()
		assert.Empty(t, endpoint, "Should return empty when no endpoints available")
	})

	t.Run("malformed endpoints", func(t *testing.T) {
		colonyInfo := &discoverypb.LookupColonyResponse{
			MeshId: "test-colony",
			Pubkey: "test-pubkey",
			Endpoints: []string{
				"",                 // Empty
				"invalid",          // No port
				"192.168.5.2:9000", // Valid
			},
			Metadata: map[string]string{
				"wireguard_port": "41820",
			},
		}

		cm := NewConnectionManager(
			"test-agent",
			colonyInfo,
			nil,
			nil,
			"test-pubkey",
			nil,
			nil, // runtimeService
			logger,
		)

		endpoint := cm.GetColonyEndpoint()
		assert.Equal(t, "192.168.5.2:41820", endpoint, "Should skip malformed endpoints and use valid one")
	})

	t.Run("nil observed endpoints", func(t *testing.T) {
		colonyInfo := &discoverypb.LookupColonyResponse{
			MeshId: "test-colony",
			Pubkey: "test-pubkey",
			ObservedEndpoints: []*discoverypb.Endpoint{
				nil,
				{Ip: "192.168.5.2", Port: 41820},
				nil,
			},
		}

		cm := NewConnectionManager(
			"test-agent",
			colonyInfo,
			nil,
			nil,
			"test-pubkey",
			nil,
			nil, // runtimeService
			logger,
		)

		endpoint := cm.GetColonyEndpoint()
		assert.Equal(t, "192.168.5.2:41820", endpoint, "Should skip nil observed endpoints")
	})

	t.Run("observed endpoints with empty IP", func(t *testing.T) {
		colonyInfo := &discoverypb.LookupColonyResponse{
			MeshId: "test-colony",
			Pubkey: "test-pubkey",
			ObservedEndpoints: []*discoverypb.Endpoint{
				{Ip: "", Port: 41820},
				{Ip: "192.168.5.2", Port: 41820},
			},
		}

		cm := NewConnectionManager(
			"test-agent",
			colonyInfo,
			nil,
			nil,
			"test-pubkey",
			nil,
			nil, // runtimeService
			logger,
		)

		endpoint := cm.GetColonyEndpoint()
		assert.Equal(t, "192.168.5.2:41820", endpoint, "Should skip endpoints with empty IP")
	})
}
