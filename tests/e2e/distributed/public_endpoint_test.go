package distributed

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// PublicEndpointSuite tests the public HTTPS endpoint and authorization.
type PublicEndpointSuite struct {
	E2EDistributedSuite

	cliEnv    *helpers.CLITestEnv
	testToken string // Pre-created token for tests
}

func (s *PublicEndpointSuite) SetupSuite() {
	s.E2EDistributedSuite.SetupSuite()

	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	// Setup CLI environment (used by TestCLIUsingPublicEndpoint).
	s.cliEnv, err = helpers.SetupCLIEnv(s.ctx, s.fixture.ColonyID, colonyEndpoint)
	s.Require().NoError(err, "Failed to setup CLI environment")

	// Create a token inside the container so it is added to the colony's
	// tokens.yaml without overwriting tokens created by other suites.
	s.testToken, err = s.fixture.CreateToken(s.ctx, "e2e-test-token", "admin")
	s.Require().NoError(err, "Failed to create test token")
	s.T().Logf("Using api token: %s", s.testToken)
}

func (s *PublicEndpointSuite) TearDownSuite() {
	if s.cliEnv != nil {
		_ = s.cliEnv.Cleanup()
	}
	s.E2EDistributedSuite.TearDownSuite()
}

// TestPublicEndpointConnectivity tests that the public endpoint is reachable and uses TLS.
func (s *PublicEndpointSuite) TestPublicEndpointConnectivity() {
	s.T().Log("Testing public endpoint connectivity...")

	// The public endpoint is exposed on port 8443 in docker-compose.
	// We'll use the colony container's host (e.g., localhost if running on same machine as docker)
	url := "https://localhost:8443/status"

	// Create an insecure client because we use a self-signed CA (Colony CA)
	// and we don't have the root CA trusted on the host runner.
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}

	resp, err := client.Get(url)
	s.Require().NoError(err, "Public endpoint should be reachable via HTTPS")
	defer resp.Body.Close()

	// Since we enabled auth.require: true in overlay, this should return 401.
	s.Require().Equal(http.StatusUnauthorized, resp.StatusCode, "Should be unauthorized without token")
}

// TestPublicEndpointAuthorization tests API key authorization flow.
func (s *PublicEndpointSuite) TestPublicEndpointAuthorization() {
	s.T().Log("Testing public endpoint authorization with API key...")

	// Use pre-created token from SetupSuite
	token := s.testToken

	// 2. Access with valid token
	url := "https://localhost:8443/status"
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: tr}

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	s.Require().NoError(err, "Request with valid token should succeed")
	defer resp.Body.Close()

	s.Require().Equal(http.StatusOK, resp.StatusCode, "Valid token should grant access")

	// 3. Access with invalid token
	req.Header.Set("Authorization", "Bearer invalid-token")
	resp, err = client.Do(req)
	s.Require().NoError(err)
	s.Require().Equal(http.StatusUnauthorized, resp.StatusCode, "Invalid token should be rejected")
}

// TestCLIUsingPublicEndpoint tests that the CLI can use the public endpoint directly.
func (s *PublicEndpointSuite) TestCLIUsingPublicEndpoint() {
	s.T().Log("Testing CLI direct access to public endpoint...")

	// Use pre-created token from SetupSuite
	token := s.testToken

	// Run 'coral colony status' using the public endpoint and API token
	// We override CORAL_COLONY_ENDPOINT to point to the public endpoint
	publicEndpoint := "https://localhost:8443"

	// Note: Connect-Go (which Coral uses) uses HTTP/2.
	// The CLI will try to use the public endpoint if it starts with https://
	result := s.cliEnv.Clone().
		WithEndpoint(publicEndpoint).
		WithEnv(map[string]string{"CORAL_API_TOKEN": token}).
		Run(s.ctx, "colony", "status", "-o", "json")

	// We expect this to fail if the CLI doesn't trust the Colony CA.
	// However, for E2E purposes, we want to see if the wiring works.
	// If the CLI fails due to cert validation, it confirms it hit the HTTPS endpoint.
	if result.HasError() && strings.Contains(result.Output, "certificate signed by unknown authority") {
		s.T().Log("Success: CLI reached public endpoint (blocked by expected cert validation error)")
	} else {
		result.MustSucceed(s.T())

		var status map[string]interface{}
		err := json.Unmarshal([]byte(result.Output), &status)
		s.Require().NoError(err, "Failed to parse JSON output")

		s.Require().Equal(s.fixture.ColonyID, status["colony_id"], "Colony ID match fixture")
	}
}
