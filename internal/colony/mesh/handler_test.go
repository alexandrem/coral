package mesh

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	discoveryclient "github.com/coral-mesh/coral/internal/discovery/client"
	"github.com/coral-mesh/coral/internal/logging"
)

// TestSelectBestAgentEndpoint tests the endpoint selection logic for multi-public endpoint scenarios.
func TestSelectBestAgentEndpoint(t *testing.T) {
	logger := logging.NewWithComponent(logging.Config{
		Level:  "error", // Reduce noise in tests
		Pretty: false,
	}, "endpoint-test")

	tests := []struct {
		name              string
		observedEndpoints []*discoveryclient.Endpoint
		peerHost          string
		expectedIP        string
		expectedPort      uint32
		expectedMatchType string
		description       string
	}{
		{
			name: "skip localhost and select matching endpoint",
			observedEndpoints: []*discoveryclient.Endpoint{
				{IP: "127.0.0.1", Port: 41580},   // Should be skipped (localhost)
				{IP: "192.168.5.2", Port: 41820}, // Should be selected (matches peer)
				{IP: "10.0.0.5", Port: 41820},    // Alternative endpoint
			},
			peerHost:          "192.168.5.2",
			expectedIP:        "192.168.5.2",
			expectedPort:      41820,
			expectedMatchType: "matching",
			description:       "Should skip localhost and prefer endpoint matching connection source",
		},
		{
			name: "skip localhost when it's first in list",
			observedEndpoints: []*discoveryclient.Endpoint{
				{IP: "127.0.0.1", Port: 41580},    // Should be skipped (localhost)
				{IP: "203.0.113.10", Port: 41820}, // Should be selected (first non-localhost)
				{IP: "198.51.100.5", Port: 41820}, // Alternative endpoint
			},
			peerHost:          "172.16.0.1", // Different IP (no match)
			expectedIP:        "203.0.113.10",
			expectedPort:      41820,
			expectedMatchType: "first",
			description:       "Should skip localhost and use first non-localhost endpoint",
		},
		{
			name: "skip multiple localhost variants",
			observedEndpoints: []*discoveryclient.Endpoint{
				{IP: "127.0.0.1", Port: 41580}, // Should be skipped (localhost IPv4)
				{IP: "::1", Port: 41580},       // Should be skipped (localhost IPv6)
				{IP: "10.42.0.5", Port: 41820}, // Should be selected
			},
			peerHost:          "10.42.0.5",
			expectedIP:        "10.42.0.5",
			expectedPort:      41820,
			expectedMatchType: "matching",
			description:       "Should skip all localhost variants (127.0.0.1, ::1)",
		},
		{
			name: "skip localhost keyword",
			observedEndpoints: []*discoveryclient.Endpoint{
				{IP: "localhost", Port: 41580}, // Should be skipped
				{IP: "192.168.1.5", Port: 41820},
			},
			peerHost:          "10.0.0.1",
			expectedIP:        "192.168.1.5",
			expectedPort:      41820,
			expectedMatchType: "first",
			description:       "Should skip 'localhost' keyword",
		},
		{
			name: "prefer matching endpoint over first when both non-localhost",
			observedEndpoints: []*discoveryclient.Endpoint{
				{IP: "198.51.100.1", Port: 41820},  // First but doesn't match
				{IP: "192.168.1.100", Port: 41820}, // Matches peer address
				{IP: "10.0.0.1", Port: 41820},      // Alternative
			},
			peerHost:          "192.168.1.100",
			expectedIP:        "192.168.1.100",
			expectedPort:      41820,
			expectedMatchType: "matching",
			description:       "Should prefer endpoint matching peer address even if not first",
		},
		{
			name: "handle all localhost endpoints gracefully",
			observedEndpoints: []*discoveryclient.Endpoint{
				{IP: "127.0.0.1", Port: 41580},
				{IP: "::1", Port: 41580},
				{IP: "localhost", Port: 41580},
			},
			peerHost:          "192.168.1.1",
			expectedIP:        "", // No valid endpoint available
			expectedPort:      0,
			expectedMatchType: "",
			description:       "Should handle case where all endpoints are localhost",
		},
		{
			name: "no peer host - use first non-localhost",
			observedEndpoints: []*discoveryclient.Endpoint{
				{IP: "127.0.0.1", Port: 41580},
				{IP: "10.0.0.5", Port: 41820},
				{IP: "192.168.1.5", Port: 41820},
			},
			peerHost:          "", // No peer host available
			expectedIP:        "10.0.0.5",
			expectedPort:      41820,
			expectedMatchType: "first",
			description:       "Should use first non-localhost when no peer host available",
		},
		{
			name:              "empty endpoint list",
			observedEndpoints: []*discoveryclient.Endpoint{},
			peerHost:          "192.168.1.1",
			expectedIP:        "",
			expectedPort:      0,
			expectedMatchType: "",
			description:       "Should handle empty endpoint list",
		},
		{
			name: "nil endpoints in list",
			observedEndpoints: []*discoveryclient.Endpoint{
				nil,
				{IP: "192.168.1.5", Port: 41820},
				nil,
			},
			peerHost:          "10.0.0.1",
			expectedIP:        "192.168.1.5",
			expectedPort:      41820,
			expectedMatchType: "first",
			description:       "Should skip nil endpoints",
		},
		{
			name: "endpoints with empty IP",
			observedEndpoints: []*discoveryclient.Endpoint{
				{IP: "", Port: 41820},
				{IP: "192.168.1.5", Port: 41820},
			},
			peerHost:          "10.0.0.1",
			expectedIP:        "192.168.1.5",
			expectedPort:      41820,
			expectedMatchType: "first",
			description:       "Should skip endpoints with empty IP",
		},
		{
			name: "multiple matching endpoints - use first match",
			observedEndpoints: []*discoveryclient.Endpoint{
				{IP: "192.168.1.5", Port: 41820},
				{IP: "192.168.1.5", Port: 41821}, // Same IP, different port
				{IP: "10.0.0.1", Port: 41820},
			},
			peerHost:          "192.168.1.5",
			expectedIP:        "192.168.1.5",
			expectedPort:      41820, // First match
			expectedMatchType: "matching",
			description:       "Should use first matching endpoint when multiple matches",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selectedEp, matchType := selectBestAgentEndpoint(
				tt.observedEndpoints,
				tt.peerHost,
				logger,
				"test-agent",
			)

			if tt.expectedIP == "" {
				// Expecting no valid endpoint
				assert.Nil(t, selectedEp, tt.description)
				assert.Empty(t, matchType, tt.description)
			} else {
				// Expecting a valid endpoint
				require.NotNil(t, selectedEp, tt.description)
				assert.Equal(t, tt.expectedIP, selectedEp.IP, tt.description)
				assert.Equal(t, tt.expectedPort, selectedEp.Port, tt.description)
				assert.Equal(t, tt.expectedMatchType, matchType, tt.description)
			}

			t.Logf("✓ %s", tt.description)
		})
	}
}

// TestSelectBestAgentEndpoint_RealWorldScenario tests a real-world scenario
// matching the original bug report.
func TestSelectBestAgentEndpoint_RealWorldScenario(t *testing.T) {
	logger := logging.NewWithComponent(logging.Config{
		Level:  "error",
		Pretty: false,
	}, "real-world-test")

	t.Run("original bug - localhost first, agent connects via port 9000", func(t *testing.T) {
		// Simulates the original bug scenario:
		// - Colony registered with endpoints: [127.0.0.1:41580, <public-ip>:9000, ...]
		// - Agent connected via the port 9000 endpoint
		// - Colony should NOT use 127.0.0.1 for WireGuard peer
		observedEndpoints := []*discoveryclient.Endpoint{
			{IP: "127.0.0.1", Port: 41580},   // This was being incorrectly selected
			{IP: "192.168.5.2", Port: 41820}, // Agent's actual public IP
			{IP: "10.0.0.5", Port: 41820},    // Alternative public endpoint
		}

		// Agent connected from 192.168.5.2
		peerHost := "192.168.5.2"

		selectedEp, matchType := selectBestAgentEndpoint(
			observedEndpoints,
			peerHost,
			logger,
			"container-agent",
		)

		require.NotNil(t, selectedEp, "Should select a valid endpoint")
		assert.NotEqual(t, "127.0.0.1", selectedEp.IP, "Should NOT select localhost")
		assert.Equal(t, "192.168.5.2", selectedEp.IP,
			"Should select endpoint matching how agent connected")
		assert.Equal(t, uint32(41820), selectedEp.Port)
		assert.Equal(t, "matching", matchType, "Should indicate this is a matching endpoint")

		t.Logf("✓ Original bug fixed: skipped localhost (127.0.0.1:41580), selected matching endpoint (%s:%d)",
			selectedEp.IP, selectedEp.Port)
	})

	t.Run("production scenario - multiple public endpoints", func(t *testing.T) {
		// Production scenario with multiple public IPs/hostnames
		observedEndpoints := []*discoveryclient.Endpoint{
			{IP: "127.0.0.1", Port: 41580},     // Local testing endpoint - should be skipped
			{IP: "203.0.113.10", Port: 41820},  // Primary public IP
			{IP: "198.51.100.50", Port: 41820}, // Secondary public IP
			{IP: "192.0.2.100", Port: 41820},   // Tertiary public IP
		}

		// Agent connected via secondary IP
		peerHost := "198.51.100.50"

		selectedEp, matchType := selectBestAgentEndpoint(
			observedEndpoints,
			peerHost,
			logger,
			"production-agent",
		)

		require.NotNil(t, selectedEp)
		assert.Equal(t, "198.51.100.50", selectedEp.IP,
			"Should select endpoint matching connection source")
		assert.Equal(t, "matching", matchType)

		t.Logf("✓ Production scenario: correctly selected matching endpoint from multiple options")
	})

	t.Run("docker-compose scenario - agent from different network", func(t *testing.T) {
		// Docker Compose scenario where agent is on different Docker network
		observedEndpoints := []*discoveryclient.Endpoint{
			{IP: "127.0.0.1", Port: 41580},     // Should be skipped
			{IP: "172.18.0.10", Port: 41820},   // Docker network IP
			{IP: "192.168.1.100", Port: 41820}, // Host network IP
		}

		// Agent connected via Docker network
		peerHost := "172.18.0.10"

		selectedEp, matchType := selectBestAgentEndpoint(
			observedEndpoints,
			peerHost,
			logger,
			"docker-agent",
		)

		require.NotNil(t, selectedEp)
		assert.Equal(t, "172.18.0.10", selectedEp.IP, "Should select Docker network endpoint")
		assert.Equal(t, "matching", matchType)

		t.Logf("✓ Docker Compose scenario: correctly selected Docker network endpoint")
	})
}

// BenchmarkSelectBestAgentEndpoint benchmarks the endpoint selection performance.
func BenchmarkSelectBestAgentEndpoint(b *testing.B) {
	logger := logging.NewWithComponent(logging.Config{
		Level:  "error",
		Pretty: false,
	}, "bench")

	// Create a realistic list of endpoints
	endpoints := []*discoveryclient.Endpoint{
		{IP: "127.0.0.1", Port: 41580},
		{IP: "::1", Port: 41580},
		{IP: "192.168.1.10", Port: 41820},
		{IP: "192.168.1.20", Port: 41820},
		{IP: "10.0.0.5", Port: 41820},
		{IP: "203.0.113.10", Port: 41820},
		{IP: "198.51.100.50", Port: 41820},
	}

	peerHost := "192.168.1.20"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		selectBestAgentEndpoint(endpoints, peerHost, logger, fmt.Sprintf("agent-%d", i))
	}
}
