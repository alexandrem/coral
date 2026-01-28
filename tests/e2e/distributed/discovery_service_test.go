package distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	discoverypb "github.com/coral-mesh/coral/coral/discovery/v1"
	discoveryclient "github.com/coral-mesh/coral/internal/discovery/client"
	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// DiscoveryServiceSuite tests the Discovery service implementation.
// It can run against either the local Go discovery service or the deployed
// Cloudflare Workers service by setting CORAL_WORKERS_DISCOVERY_ENDPOINT.
//
// This suite consolidates:
// - RPC tests (register, lookup, health)
// - JWKS validation
// - Auth flow integration tests
type DiscoveryServiceSuite struct {
	E2EDistributedSuite

	// workersEndpoint is the URL of the Workers discovery service.
	// If empty, tests use the fixture's local discovery endpoint.
	workersEndpoint string

	// isWorkersMode indicates if we're testing against Workers (vs local Go).
	isWorkersMode bool
}

// SetupSuite checks if Workers endpoint is configured.
func (s *DiscoveryServiceSuite) SetupSuite() {
	s.E2EDistributedSuite.SetupSuite()

	// Check for Workers endpoint override.
	s.workersEndpoint = os.Getenv("CORAL_WORKERS_DISCOVERY_ENDPOINT")
	s.isWorkersMode = s.workersEndpoint != ""

	if s.isWorkersMode {
		s.T().Logf("Testing against Workers discovery service: %s", s.workersEndpoint)
	} else {
		s.T().Log("Testing against local Go discovery service")
	}
}

// getDiscoveryEndpoint returns the discovery endpoint to test against.
func (s *DiscoveryServiceSuite) getDiscoveryEndpoint() string {
	if s.isWorkersMode {
		return s.workersEndpoint
	}
	return s.fixture.DiscoveryEndpoint
}

// skipIfWorkers skips the test if running against Workers (test requires local env).
func (s *DiscoveryServiceSuite) skipIfWorkers(reason string) {
	if s.isWorkersMode {
		s.T().Skipf("Skipping test against Workers: %s", reason)
	}
}

// skipIfLocal skips the test if running against local discovery.
func (s *DiscoveryServiceSuite) skipIfLocal(reason string) {
	if !s.isWorkersMode {
		s.T().Skipf("Skipping test against local discovery: %s", reason)
	}
}

// =============================================================================
// JWKS Tests
// =============================================================================

// TestJWKSEndpoint verifies the Discovery service exposes a valid JWKS endpoint
// with Ed25519 keys (RFD 049).
func (s *DiscoveryServiceSuite) TestJWKSEndpoint() {
	endpoint := s.getDiscoveryEndpoint()
	jwksURL := endpoint + "/.well-known/jwks.json"

	s.T().Logf("Testing JWKS endpoint at %s", jwksURL)

	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", jwksURL, nil)
	s.Require().NoError(err)

	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err)
	defer resp.Body.Close()

	s.Equal(http.StatusOK, resp.StatusCode, "JWKS endpoint should return 200 OK")

	var jwks struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			Alg string `json:"alg"`
			Crv string `json:"crv"`
			X   string `json:"x"`
		} `json:"keys"`
	}

	err = json.NewDecoder(resp.Body).Decode(&jwks)
	s.Require().NoError(err, "Failed to decode JWKS")

	s.T().Logf("JWKS contains %d keys", len(jwks.Keys))

	// Local Go discovery should always have keys.
	// Workers may have empty JWKS if signing key not configured.
	if !s.isWorkersMode {
		s.NotEmpty(jwks.Keys, "Local discovery JWKS should contain at least one key")
	}

	for _, key := range jwks.Keys {
		s.Equal("OKP", key.Kty, "Key type must be OKP for Ed25519")
		s.Equal("EdDSA", key.Alg, "Algorithm must be EdDSA")
		s.Equal("Ed25519", key.Crv, "Curve must be Ed25519")
		s.NotEmpty(key.Kid, "Key ID must not be empty")
		s.T().Logf("Found EdDSA key: kid=%s", key.Kid)
	}
}

// =============================================================================
// Auth Flow Tests (requires docker-compose environment)
// =============================================================================

// TestAuthorizationFlow verifies the entire authorization flow works.
// This test validates that agents can bootstrap via Discovery.
// Requires docker-compose environment - skipped for Workers mode.
func (s *DiscoveryServiceSuite) TestAuthorizationFlow() {
	s.skipIfWorkers("auth flow test requires docker-compose environment with agents")

	agentEndpoint := s.fixture.Agent0Endpoint
	s.T().Logf("Verifying Agent authentication via %s", agentEndpoint)

	// Setup CLI environment.
	cliEnv, err := helpers.SetupCLIEnv(s.ctx, s.fixture.ColonyID, s.fixture.ColonyEndpoint)
	s.Require().NoError(err, "Failed to setup CLI environment")
	defer cliEnv.Cleanup()

	// List agents.
	agents, err := helpers.AgentListJSON(s.ctx, s.fixture.ColonyEndpoint)
	s.Require().NoError(err, "Failed to list agents")

	s.NotEmpty(agents, "Should have at least one agent registered")

	// Check status of the first agent.
	agent := agents[0]
	s.Equal("healthy", agent["status"], "Agent should be healthy")
	s.T().Logf("Agent %v is healthy", agent["agent_id"])

	s.T().Log("Agent successfully authenticated via Discovery and connected to Colony")
}

// =============================================================================
// RPC Tests (work with both local and Workers)
// =============================================================================

// TestHealthEndpoint tests the Health RPC.
func (s *DiscoveryServiceSuite) TestHealthEndpoint() {
	endpoint := s.getDiscoveryEndpoint()
	s.T().Logf("Testing Health RPC against %s", endpoint)

	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()

	// Use JSON encoding for compatibility with Cloudflare Workers.
	client := helpers.NewDiscoveryClient(endpoint)

	healthResp, err := client.Health(ctx)
	s.Require().NoError(err, "Health check should succeed")
	s.Equal("ok", healthResp.Status)
	s.NotEmpty(healthResp.Version)
	s.T().Logf("Health: status=%s, version=%s, uptime=%ds",
		healthResp.Status, healthResp.Version, healthResp.UptimeSeconds)
}

// TestRegisterAndLookupColony tests colony registration and lookup.
func (s *DiscoveryServiceSuite) TestRegisterAndLookupColony() {
	endpoint := s.getDiscoveryEndpoint()
	s.T().Logf("Testing RegisterColony/LookupColony against %s", endpoint)

	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	client := helpers.NewDiscoveryClient(endpoint)

	meshID := fmt.Sprintf("e2e-test-mesh-%d", time.Now().UnixNano())

	registerResp, err := client.RegisterColony(ctx, &discoveryclient.RegisterColonyRequest{
		MeshID:      meshID,
		PublicKey:   "dGVzdC1wdWJrZXktZTJlLWNvbG9ueQ==",
		Endpoints:   []string{"192.0.2.1:51820", "192.0.2.2:51820"},
		MeshIPv4:    "10.42.0.1",
		MeshIPv6:    "fd42::1",
		ConnectPort: 9000,
		PublicPort:  8443,
		Metadata:    map[string]string{"environment": "e2e-test"},
	})
	s.Require().NoError(err, "RegisterColony should succeed")
	s.True(registerResp.Success)
	s.Greater(registerResp.TTL, int32(0))
	s.T().Logf("Colony registered with TTL: %d seconds", registerResp.TTL)

	// Lookup.
	lookupResp, err := client.LookupColony(ctx, meshID)
	s.Require().NoError(err, "LookupColony should succeed")
	s.Equal(meshID, lookupResp.MeshID)
	s.Equal("dGVzdC1wdWJrZXktZTJlLWNvbG9ueQ==", lookupResp.Pubkey)
	s.Len(lookupResp.Endpoints, 2)
	s.Equal("10.42.0.1", lookupResp.MeshIPv4)
	s.Equal(uint32(9000), lookupResp.ConnectPort)
	s.T().Logf("Lookup successful: mesh_id=%s", lookupResp.MeshID)
}

// TestRegisterAndLookupAgent tests agent registration and lookup.
func (s *DiscoveryServiceSuite) TestRegisterAndLookupAgent() {
	endpoint := s.getDiscoveryEndpoint()
	s.T().Logf("Testing RegisterAgent/LookupAgent against %s", endpoint)

	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	client := helpers.NewDiscoveryClient(endpoint)

	meshID := fmt.Sprintf("e2e-mesh-agent-%d", time.Now().UnixNano())
	agentID := fmt.Sprintf("e2e-agent-%d", time.Now().UnixNano())

	// Register colony first.
	_, err := client.RegisterColony(ctx, &discoveryclient.RegisterColonyRequest{
		MeshID:    meshID,
		PublicKey: "Y29sb255LXB1YmtleQ==",
		Endpoints: []string{"192.0.2.100:51820"},
	})
	s.Require().NoError(err)

	// Register agent.
	registerResp, err := client.RegisterAgent(ctx, &discoveryclient.RegisterAgentRequest{
		AgentID:   agentID,
		MeshID:    meshID,
		Pubkey:    "YWdlbnQtcHVia2V5",
		Endpoints: []string{"192.0.2.50:51820"},
		Metadata:  map[string]string{"hostname": "test-agent"},
	})
	s.Require().NoError(err)
	s.True(registerResp.Success)
	s.T().Logf("Agent registered: %s", agentID)

	// Required for Workers-based discovery.
	lookupResp, err := client.LookupAgent(ctx, agentID, meshID)
	s.Require().NoError(err)
	s.Equal(agentID, lookupResp.AgentID)
	s.Equal(meshID, lookupResp.MeshID)
	s.T().Logf("Lookup successful: agent_id=%s", lookupResp.AgentID)
}

// TestSplitBrainProtection tests rejection of duplicate registrations with different pubkeys.
func (s *DiscoveryServiceSuite) TestSplitBrainProtection() {
	endpoint := s.getDiscoveryEndpoint()
	s.T().Logf("Testing split-brain protection against %s", endpoint)

	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	client := helpers.NewDiscoveryClient(endpoint)
	meshID := fmt.Sprintf("e2e-split-brain-%d", time.Now().UnixNano())

	// First registration.
	resp1, err := client.RegisterColony(ctx, &discoveryclient.RegisterColonyRequest{
		MeshID:    meshID,
		PublicKey: "cHVia2V5LW9uZQ==",
		Endpoints: []string{"192.0.2.1:51820"},
	})
	s.Require().NoError(err)
	s.True(resp1.Success)

	// Second with DIFFERENT pubkey should fail.
	_, err = client.RegisterColony(ctx, &discoveryclient.RegisterColonyRequest{
		MeshID:    meshID,
		PublicKey: "cHVia2V5LXR3bw==",
		Endpoints: []string{"192.0.2.2:51820"},
	})
	s.Require().Error(err)
	s.Contains(err.Error(), "already")
	s.T().Log("Split-brain protection working")

	// Third with SAME pubkey should succeed (renewal).
	resp3, err := client.RegisterColony(ctx, &discoveryclient.RegisterColonyRequest{
		MeshID:    meshID,
		PublicKey: "cHVia2V5LW9uZQ==",
		Endpoints: []string{"192.0.2.3:51820"},
	})
	s.Require().NoError(err)
	s.True(resp3.Success)
	s.T().Log("Renewal with same pubkey succeeded")
}

// TestObservedEndpointCapture tests observed endpoint capture.
func (s *DiscoveryServiceSuite) TestObservedEndpointCapture() {
	endpoint := s.getDiscoveryEndpoint()
	s.T().Logf("Testing observed endpoint capture against %s", endpoint)

	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	client := helpers.NewDiscoveryClient(endpoint)
	meshID := fmt.Sprintf("e2e-observed-%d", time.Now().UnixNano())

	// Register with observed endpoint.
	registerResp, err := client.RegisterColony(ctx, &discoveryclient.RegisterColonyRequest{
		MeshID:    meshID,
		PublicKey: "b2JzZXJ2ZWQtdGVzdA==",
		Endpoints: []string{"10.0.0.1:51820"},
		ObservedEndpoint: &discoverypb.Endpoint{
			Ip:       "203.0.113.50",
			Port:     51820,
			Protocol: "udp",
		},
	})
	s.Require().NoError(err)
	s.True(registerResp.Success)

	if registerResp.ObservedEndpoint != nil {
		s.T().Logf("Response observed endpoint: %s:%d",
			registerResp.ObservedEndpoint.IP, registerResp.ObservedEndpoint.Port)
	}

	// Lookup should include observed endpoints.
	lookupResp, err := client.LookupColony(ctx, meshID)
	s.Require().NoError(err)

	if len(lookupResp.ObservedEndpoints) > 0 {
		s.T().Logf("Lookup returned %d observed endpoints", len(lookupResp.ObservedEndpoints))
	}
}

// =============================================================================
// Workers-Specific Tests
// =============================================================================

// TestRelayNotImplemented verifies relay RPCs return UNIMPLEMENTED.
// Only relevant for Workers which doesn't support relay.
func (s *DiscoveryServiceSuite) TestRelayNotImplemented() {
	s.skipIfLocal("relay is implemented in local Go discovery")

	endpoint := s.getDiscoveryEndpoint()
	s.T().Logf("Testing Relay RPCs against Workers (expecting UNIMPLEMENTED)")

	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()

	client := helpers.NewDiscoveryClient(endpoint)

	// Test RequestRelay.
	_, err := client.RequestRelay(ctx, &discoveryclient.RequestRelayRequest{
		MeshID:       "test-mesh",
		AgentPubkey:  "test-agent",
		ColonyPubkey: "test-colony",
	})
	s.Require().Error(err)
	s.Contains(err.Error(), "unimplemented")
	s.T().Log("RequestRelay correctly returned UNIMPLEMENTED")
}

// TestTTLExpiration tests registration TTL expiration.
// Requires a short DEFAULT_TTL_SECONDS (e.g. 10) in the e2e environment.
// Skipped for deployed Workers where TTL is typically 5 minutes.
func (s *DiscoveryServiceSuite) TestTTLExpiration() {
	s.skipIfWorkers("TTL too long for Workers")

	endpoint := s.getDiscoveryEndpoint()
	s.T().Logf("Testing TTL expiration against %s", endpoint)

	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Minute)
	defer cancel()

	client := helpers.NewDiscoveryClient(endpoint)
	meshID := fmt.Sprintf("e2e-ttl-%d", time.Now().UnixNano())

	// Register.
	registerResp, err := client.RegisterColony(ctx, &discoveryclient.RegisterColonyRequest{
		MeshID:    meshID,
		PublicKey: "dHRsLXRlc3Q=",
		Endpoints: []string{"192.0.2.99:51820"},
	})
	s.Require().NoError(err)
	s.T().Logf("Registered with TTL: %d seconds", registerResp.TTL)

	// Verify lookup works immediately.
	_, err = client.LookupColony(ctx, meshID)
	s.Require().NoError(err)

	// Wait for TTL + cleanup interval buffer.
	waitTime := time.Duration(registerResp.TTL+10) * time.Second
	s.Require().LessOrEqual(waitTime, 90*time.Second,
		"TTL too long for e2e test; lower DEFAULT_TTL_SECONDS in docker-compose")

	s.T().Logf("Waiting %v for TTL expiration...", waitTime)
	time.Sleep(waitTime)

	// Lookup should fail.
	_, err = client.LookupColony(ctx, meshID)
	s.Require().Error(err)
	s.Contains(err.Error(), "not found")
	s.T().Log("TTL expiration working correctly")
}
