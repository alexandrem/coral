package distributed

import (
	"encoding/json"
	"net/http"

	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

type DiscoveryAuthSuite struct {
	E2EDistributedSuite
}

// TestJWKSEndpoint verifies that the Discovery service exposes a valid JWKS endpoint
// with Ed25519 keys (RFD 049).
func (s *DiscoveryAuthSuite) TestJWKSEndpoint() {
	discoveryURL := s.fixture.DiscoveryEndpoint
	jwksURL := discoveryURL + "/.well-known/jwks.json"

	s.T().Logf("Checking JWKS endpoint at %s", jwksURL)

	// Fetch JWKS
	resp, err := http.Get(jwksURL)
	s.Require().NoError(err, "Failed to fetch JWKS")
	defer resp.Body.Close()

	s.Equal(http.StatusOK, resp.StatusCode, "JWKS endpoint should return 200 OK")

	// Parse JWKS
	var jwks struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			Alg string `json:"alg"`
			Crv string `json:"crv"`
		} `json:"keys"`
	}

	err = json.NewDecoder(resp.Body).Decode(&jwks)
	s.Require().NoError(err, "Failed to decode JWKS JSON")

	// Validate Keys
	s.NotEmpty(jwks.Keys, "JWKS should contain at least one key")

	for _, key := range jwks.Keys {
		s.Equal("OKP", key.Kty, "Key type (kty) must be OKP")
		s.Equal("EdDSA", key.Alg, "Algorithm (alg) must be EdDSA")
		s.Equal("Ed25519", key.Crv, "Curve (crv) must be Ed25519")
		s.NotEmpty(key.Kid, "Key ID (kid) must not be empty")
		s.T().Logf("Found valid EdDSA key: kid=%s", key.Kid)
	}
}

// TestDiscoveryAuthorizationFlow verifies that the entire authorization flow works.
// Since the environment starts with Agent 0 auto-bootstrapping, verifying that
// Agent 0 is healthy and registered implies that:
// 1. Agent 0 got a referral ticket from Discovery (signed with Ed25519).
// 2. Agent 0 sent the ticket to Colony (RequestCertificate).
// 3. Colony fetched JWKS from Discovery and validated the ticket.
// 4. Colony issued a certificate.
func (s *DiscoveryAuthSuite) TestDiscoveryAuthorizationFlow() {
	agentEndpoint := s.fixture.Agent0Endpoint
	s.T().Logf("Verifying Agent authentication via %s", agentEndpoint)

	// Setup CLI environment connecting to this colony
	cliEnv, err := helpers.SetupCLIEnv(s.ctx, s.fixture.ColonyID, s.fixture.ColonyEndpoint)
	s.Require().NoError(err, "Failed to setup CLI environment")
	defer cliEnv.Cleanup()

	// List agents
	agents, err := helpers.AgentListJSON(s.ctx, s.fixture.ColonyEndpoint)
	s.Require().NoError(err, "Failed to list agents")

	s.NotEmpty(agents, "Should have at least one agent registered")

	// Check status of the first agent
	agent := agents[0]
	// Use Correct JSON keys from colony.pb.go
	// Status should be "healthy" as seen in logs
	s.Equal("healthy", agent["status"], "Agent should be healthy")
	s.T().Logf("Agent %v is healthy", agent["agent_id"])

	s.T().Log("Agent successfully authenticated via Discovery and connected to Colony")
}
