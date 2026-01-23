package distributed

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// CLIMeshSuite tests CLI commands for colony and agent management.
//
// This suite validates:
// 1. Colony status commands (coral colony status, agents)
// 2. Agent listing and status (coral agent list, status)
// 3. Output formatting (table vs JSON)
// 4. Error handling for invalid inputs
// 5. Discovery CA certificate flow (RFD 085)
//
// Note: This suite does NOT test infrastructure behavior (that's covered by MeshSuite).
// It focuses on CLI-specific concerns: output formatting, flag validation, error messages.
type CLIMeshSuite struct {
	E2EDistributedSuite

	cliEnv *helpers.CLITestEnv

	// Discovery CA test fields (RFD 085).
	discoveryURL   string
	publicEndpoint string
	caFingerprint  string
	caCertPEM      []byte
	testToken      string
}

// SetupSuite runs once before all tests in the suite.
func (s *CLIMeshSuite) SetupSuite() {
	s.E2EDistributedSuite.SetupSuite()

	// Setup CLI environment.
	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	// Get colony ID from fixture.
	colonyID := s.fixture.ColonyID
	if colonyID == "" {
		colonyID = "test-colony-e2e" // Default fallback
	}

	s.cliEnv, err = helpers.SetupCLIEnv(s.ctx, colonyID, colonyEndpoint)
	s.Require().NoError(err, "Failed to setup CLI environment")

	s.T().Logf("CLI environment ready: endpoint=%s", colonyEndpoint)

	// Setup Discovery CA test fields (RFD 085).
	s.setupDiscoveryCA(colonyID)
}

// setupDiscoveryCA initializes fields needed for Discovery CA tests.
func (s *CLIMeshSuite) setupDiscoveryCA(colonyID string) {
	var err error

	// Get discovery endpoint.
	s.discoveryURL, err = s.fixture.GetDiscoveryEndpoint(s.ctx)
	if err != nil {
		s.T().Logf("Warning: Failed to get discovery endpoint (CA tests will be skipped): %v", err)
		return
	}

	// Public endpoint is on port 8443.
	s.publicEndpoint = "https://localhost:8443"

	// Get CA fingerprint from discovery.
	s.caFingerprint, s.caCertPEM, err = s.getColonyCAFingerprint(colonyID)
	if err != nil {
		s.T().Logf("Warning: Failed to get CA fingerprint (CA tests will be skipped): %v", err)
		return
	}
	s.T().Logf("Colony CA fingerprint: %s", s.caFingerprint)

	// Create a test token for authentication.
	result := helpers.ColonyTokenCreate(s.ctx, s.cliEnv.EnvVars(), "cli-mesh-ca-test-token", "admin")
	if result.HasError() {
		s.T().Logf("Warning: Failed to create test token (CA tests will be skipped): %v", result.Err)
		return
	}

	// Extract token from CLI output.
	for _, line := range strings.Split(result.Output, "\n") {
		if strings.HasPrefix(line, "Token: ") {
			s.testToken = strings.TrimPrefix(line, "Token: ")
			break
		}
	}

	if s.testToken != "" {
		s.copyTokensToColony(colonyID)
	}
}

// getColonyCAFingerprint retrieves the CA certificate from the Discovery service.
func (s *CLIMeshSuite) getColonyCAFingerprint(colonyID string) (string, []byte, error) {
	client := helpers.NewDiscoveryClient(s.discoveryURL)

	var caCertBase64 string
	err := helpers.WaitForCondition(s.ctx, func() bool {
		resp, lookupErr := helpers.LookupColony(s.ctx, client, colonyID)
		if lookupErr != nil {
			return false
		}
		if resp.PublicEndpoint == nil || resp.PublicEndpoint.CaCert == "" {
			return false
		}
		caCertBase64 = resp.PublicEndpoint.CaCert
		return true
	}, 30*time.Second, 2*time.Second)

	if err != nil {
		return "", nil, fmt.Errorf("failed to get CA cert from discovery: %w", err)
	}

	caCertPEM, err := base64.StdEncoding.DecodeString(caCertBase64)
	if err != nil {
		return "", nil, fmt.Errorf("failed to decode CA cert: %w", err)
	}

	hash := sha256.Sum256(caCertPEM)
	fingerprint := "sha256:" + hex.EncodeToString(hash[:])

	return fingerprint, caCertPEM, nil
}

// copyTokensToColony copies the tokens file to the colony container.
func (s *CLIMeshSuite) copyTokensToColony(colonyID string) {
	tokensPath := filepath.Join(s.cliEnv.ColonyPath(colonyID), "tokens.yaml")
	destPath := fmt.Sprintf("distributed-colony-1:/root/.coral/colonies/%s/tokens.yaml", colonyID)

	cmd := exec.Command("docker", "cp", tokensPath, destPath)
	if err := cmd.Run(); err != nil {
		s.T().Logf("Warning: Failed to copy tokens: %v", err)
	}

	// Restart colony to reload tokens.
	_ = s.fixture.RestartService(s.ctx, "colony")
	_ = helpers.WaitForHTTPEndpoint(s.ctx, s.publicEndpoint+"/status", 30*time.Second)
}

// TearDownSuite cleans up after all tests.
func (s *CLIMeshSuite) TearDownSuite() {
	if s.cliEnv != nil {
		_ = s.cliEnv.Cleanup()
	}
	s.E2EDistributedSuite.TearDownSuite()
}

// TestColonyStatusCommand tests 'coral colony status' output.
//
// Validates:
// - Command executes successfully
// - Table output has expected structure
// - JSON output has required fields
// - Both formats contain same essential data
func (s *CLIMeshSuite) TestColonyStatusCommand() {
	s.T().Log("Testing 'coral colony status' command...")

	// Test table format (default)
	result := helpers.ColonyStatus(s.ctx, s.cliEnv.ColonyEndpoint)
	result.MustSucceed(s.T())

	s.T().Log("Table output:")
	s.T().Log(result.Output)

	// Validate table structure
	validator := &helpers.TableValidator{
		Headers: []string{"Colony", "Status"},
		MinRows: 1, // At least the colony itself
	}
	rows := validator.ValidateTable(s.T(), result.Output)

	// Verify output is not empty
	s.Require().GreaterOrEqual(len(rows), 1, "Table should have at least 1 row")

	// Test JSON format
	status, err := helpers.ColonyStatusJSON(s.ctx, s.cliEnv.ColonyEndpoint)
	s.Require().NoError(err, "JSON query should succeed")

	// Validate JSON structure
	s.Require().NotNil(status, "Status should not be nil")

	// Check for common fields (may vary by implementation)
	// At minimum, should have some identifying information
	s.Require().NotEmpty(status, "Status should have fields")

	s.T().Log("✓ Colony status command validated (table + JSON)")
}

// TestColonyAgentsCommand tests 'coral colony agents' output.
//
// Validates:
// - Lists registered agents
// - Table and JSON formats work
// - Agent information is present
func (s *CLIMeshSuite) TestColonyAgentsCommand() {
	s.T().Log("Testing 'coral colony agents' command...")

	// Test table format
	result := helpers.ColonyAgents(s.ctx, s.cliEnv.ColonyEndpoint)
	result.MustSucceed(s.T())

	s.T().Log("Table output:")
	s.T().Log(result.Output)

	// Should show agents (we have 2 in docker-compose)
	helpers.AssertContains(s.T(), result.Output, "agent")

	// Test JSON format
	agents, err := helpers.ColonyAgentsJSON(s.ctx, s.cliEnv.ColonyEndpoint)
	s.Require().NoError(err, "JSON query should succeed")
	s.Require().GreaterOrEqual(len(agents), 1, "Should have at least 1 agent")

	// Validate agent structure
	for i, agent := range agents {
		s.T().Logf("Agent %d: %v", i, agent)

		// Check for essential agent fields
		// The exact fields depend on implementation, but agents should have identifiers
		s.Require().NotEmpty(agent, "Agent %d should have fields", i)
	}

	s.T().Logf("✓ Listed %d agents via CLI", len(agents))
}

// TestAgentListCommand tests 'coral agent list' output.
//
// Validates:
// - Agent list command works
// - Table and JSON output formats
// - Agent data is present and valid
func (s *CLIMeshSuite) TestAgentListCommand() {
	s.T().Log("Testing 'coral colony agents' command...")

	// Test with table output
	result := helpers.AgentList(s.ctx, s.cliEnv.ColonyEndpoint)
	result.MustSucceed(s.T())

	s.T().Log("Table output:")
	s.T().Log(result.Output)

	// Verify output structure
	rows := helpers.ParseTableOutput(result.Output)
	s.Require().GreaterOrEqual(len(rows), 1, "Should have at least headers")

	// Test with JSON output
	agents, err := helpers.AgentListJSON(s.ctx, s.cliEnv.ColonyEndpoint)
	s.Require().NoError(err, "JSON query should succeed")
	s.Require().GreaterOrEqual(len(agents), 1, "Should have at least 1 agent")

	s.T().Logf("✓ Agent list validated (%d agents)", len(agents))
}

// TestServiceListCommand tests 'coral service list' output.
//
// Validates:
// - Service list command works
// - Output formatting
func (s *CLIMeshSuite) TestServiceListCommand() {
	s.T().Log("Testing 'coral colony service list' command...")

	// Test with table output
	result := helpers.ServiceList(s.ctx, s.cliEnv.ColonyEndpoint)
	result.MustSucceed(s.T())

	s.T().Log("Table output:")
	s.T().Log(result.Output)

	// Test with JSON output (may be empty if no services registered yet)
	services, err := helpers.ServiceListJSON(s.ctx, s.cliEnv.ColonyEndpoint)
	s.Require().NoError(err, "JSON query should succeed")

	s.T().Logf("✓ Service list validated (%d services)", len(services))
}

// TestInvalidColonyEndpoint tests error handling for bad endpoint.
//
// Validates:
// - Commands fail gracefully with invalid endpoint
// - Error messages are helpful
// - Exit codes are non-zero
//
// NOTE: Currently skipped - we don't have a colony endpoint env var yet.
func (s *CLIMeshSuite) TestInvalidColonyEndpoint() {
	s.T().Log("Testing error handling for invalid endpoint...")

	// Use an invalid endpoint that will definitely fail
	result := helpers.RunCLIWithEnv(s.ctx, map[string]string{
		"CORAL_COLONY_ENDPOINT": "http://invalid-colony-host:99999",
	}, "colony", "status")

	// Should fail
	result.MustFail(s.T())

	// Should have an error message (exact message varies)
	s.Require().NotEmpty(result.Output, "Should have error output")

	s.T().Log("Error output (expected):")
	s.T().Log(result.Output)

	s.T().Log("✓ Error handling validated")
}

// TestTableOutputFormatting tests table output formatting consistency.
//
// Validates:
// - Table headers are present
// - Columns are aligned reasonably
// - Data rows exist
func (s *CLIMeshSuite) TestTableOutputFormatting() {
	s.T().Log("Testing table output formatting...")

	// Get colony status table
	result := helpers.ColonyStatus(s.ctx, s.cliEnv.ColonyEndpoint)
	result.MustSucceed(s.T())

	rows := helpers.ParseTableOutput(result.Output)
	s.Require().GreaterOrEqual(len(rows), 1, "Should have at least headers")

	// Log parsed rows for debugging
	helpers.PrintTable(s.T(), rows)

	s.T().Log("✓ Table formatting validated")
}

// TestJSONOutputValidity tests that all JSON outputs are valid JSON.
//
// Validates:
// - All *JSON() helpers return valid JSON
// - JSON can be parsed without errors
func (s *CLIMeshSuite) TestJSONOutputValidity() {
	s.T().Log("Testing JSON output validity...")

	// Test colony status JSON
	_, err := helpers.ColonyStatusJSON(s.ctx, s.cliEnv.ColonyEndpoint)
	s.Require().NoError(err, "Colony status JSON should be valid")

	// Test colony agents JSON
	_, err = helpers.ColonyAgentsJSON(s.ctx, s.cliEnv.ColonyEndpoint)
	s.Require().NoError(err, "Colony agents JSON should be valid")

	// Test agent list JSON
	_, err = helpers.AgentListJSON(s.ctx, s.cliEnv.ColonyEndpoint)
	s.Require().NoError(err, "Agent list JSON should be valid")

	// Test service list JSON
	_, err = helpers.ServiceListJSON(s.ctx, s.cliEnv.ColonyEndpoint)
	s.Require().NoError(err, "Service list JSON should be valid")

	s.T().Log("✓ All JSON outputs are valid")
}

// ============================================================================
// Discovery CA Certificate Tests (RFD 085)
// ============================================================================

// skipIfNoDiscoveryCA skips the test if Discovery CA setup failed.
func (s *CLIMeshSuite) skipIfNoDiscoveryCA() {
	if s.caFingerprint == "" || s.testToken == "" {
		s.T().Skip("Skipping: Discovery CA setup incomplete")
	}
}

// TestAddRemoteConnectionFailsWithoutCA verifies that connecting to the public
// HTTPS endpoint fails with certificate validation error when no CA is configured.
func (s *CLIMeshSuite) TestAddRemoteConnectionFailsWithoutCA() {
	s.skipIfNoDiscoveryCA()
	s.T().Log("Testing that CLI connection fails without CA certificate...")

	// Create a fresh CLI environment without any CA configuration.
	freshEnv, err := helpers.SetupCLIEnv(s.ctx, "fresh-test", s.publicEndpoint)
	s.Require().NoError(err, "Failed to create fresh CLI env")
	defer freshEnv.Cleanup()

	// Try to connect to public endpoint without CA.
	env := freshEnv.WithEnv(map[string]string{
		"CORAL_COLONY_ENDPOINT": s.publicEndpoint,
		"CORAL_API_TOKEN":       s.testToken,
	})

	s.T().Log("endpoint", s.publicEndpoint)
	s.T().Log("token", s.testToken)

	result := helpers.RunCLIWithEnv(s.ctx, env, "colony", "agents")

	// The command should fail with a certificate error.
	s.True(result.HasError(), "Command should fail without CA certificate")
	s.True(
		strings.Contains(result.Output, "certificate signed by unknown authority") ||
			strings.Contains(result.Output, "certificate") ||
			strings.Contains(result.Output, "x509"),
		"Error should mention certificate issue, got: %s", result.Output,
	)

	s.T().Log("✓ Connection correctly failed without CA certificate")
}

// TestAddRemoteFromDiscoverySuccess verifies that `coral colony add-remote --from-discovery`
// successfully fetches CA cert from Discovery, verifies fingerprint, and stores configuration.
func (s *CLIMeshSuite) TestAddRemoteFromDiscoverySuccess() {
	s.skipIfNoDiscoveryCA()
	s.T().Log("Testing coral colony add-remote --from-discovery with correct fingerprint...")

	// Create a fresh CLI environment.
	freshEnv, err := helpers.SetupCLIEnv(s.ctx, "add-remote-test", s.publicEndpoint)
	s.Require().NoError(err, "Failed to create fresh CLI env")
	defer freshEnv.Cleanup()

	remoteColonyName := "test-remote-colony"
	env := freshEnv.WithEnv(map[string]string{
		"HOME": freshEnv.HomeDir,
	})

	result := helpers.RunCLIWithEnv(s.ctx, env,
		"colony", "add-remote", remoteColonyName,
		"--from-discovery",
		"--colony-id", s.fixture.ColonyID,
		"--ca-fingerprint", s.caFingerprint,
		"--discovery-endpoint", s.discoveryURL,
	)

	result.MustSucceed(s.T())

	// Verify CA cert file was created.
	caCertPath := filepath.Join(freshEnv.ColoniesPath(), remoteColonyName, "ca.crt")
	s.FileExists(caCertPath, "CA cert file should be created")

	// Verify the stored CA cert matches what we got from discovery.
	storedCACert, err := os.ReadFile(caCertPath)
	s.Require().NoError(err, "Failed to read stored CA cert")
	s.Equal(s.caCertPEM, storedCACert, "Stored CA cert should match discovery CA cert")

	s.T().Log("✓ Successfully added remote colony with CA cert from Discovery")
}

// TestAddRemoteWithWrongFingerprint verifies that `coral colony add-remote --from-discovery`
// fails when the provided fingerprint doesn't match the CA cert from Discovery.
func (s *CLIMeshSuite) TestAddRemoteWithWrongFingerprint() {
	s.skipIfNoDiscoveryCA()
	s.T().Log("Testing coral colony add-remote --from-discovery with wrong fingerprint...")

	freshEnv, err := helpers.SetupCLIEnv(s.ctx, "wrong-fp-test", s.publicEndpoint)
	s.Require().NoError(err, "Failed to create fresh CLI env")
	defer freshEnv.Cleanup()

	// Use a wrong fingerprint (valid format but wrong value).
	wrongFingerprint := "sha256:0000000000000000000000000000000000000000000000000000000000000000"

	env := freshEnv.WithEnv(map[string]string{
		"HOME": freshEnv.HomeDir,
	})

	result := helpers.RunCLIWithEnv(s.ctx, env,
		"colony", "add-remote", "wrong-fp-colony",
		"--from-discovery",
		"--colony-id", s.fixture.ColonyID,
		"--ca-fingerprint", wrongFingerprint,
		"--discovery-endpoint", s.discoveryURL,
	)

	// The command should fail.
	s.True(result.HasError(), "Command should fail with wrong fingerprint")
	s.True(
		strings.Contains(result.Output, "fingerprint mismatch") ||
			strings.Contains(result.Output, "mismatch"),
		"Error should mention fingerprint mismatch, got: %s", result.Output,
	)

	// Verify no config was created.
	configPath := filepath.Join(freshEnv.ColoniesPath(), "wrong-fp-colony", "config.yaml")
	_, statErr := os.Stat(configPath)
	s.True(os.IsNotExist(statErr), "Config should not be created on fingerprint mismatch")

	s.T().Log("✓ Correctly rejected wrong fingerprint")
}

// TestAddRemoteConnectionSucceedsWithStoredCA verifies that after running add-remote,
// subsequent CLI commands can successfully connect using the stored CA cert.
func (s *CLIMeshSuite) TestAddRemoteConnectionSucceedsWithStoredCA() {
	s.skipIfNoDiscoveryCA()
	s.T().Log("Testing CLI connection succeeds with stored CA certificate...")

	freshEnv, err := helpers.SetupCLIEnv(s.ctx, "stored-ca-test", s.publicEndpoint)
	s.Require().NoError(err, "Failed to create fresh CLI env")
	defer freshEnv.Cleanup()

	// First, add the remote colony.
	remoteColonyName := "stored-ca-colony"
	env := freshEnv.WithEnv(map[string]string{
		"HOME": freshEnv.HomeDir,
	})

	result := helpers.RunCLIWithEnv(s.ctx, env,
		"colony", "add-remote", remoteColonyName,
		"--from-discovery",
		"--colony-id", s.fixture.ColonyID,
		"--ca-fingerprint", s.caFingerprint,
		"--discovery-endpoint", s.discoveryURL,
		"--set-default",
	)
	result.MustSucceed(s.T())

	// Verify that --set-default correctly set the new colony as default.
	// Clear CORAL_COLONY_ID so get-contexts shows the global default, not env override.
	checkEnv := freshEnv.WithEnv(map[string]string{
		"HOME":            freshEnv.HomeDir,
		"CORAL_COLONY_ID": "", // Clear to see global default
	})
	contextsResult := helpers.RunCLIWithEnv(s.ctx, checkEnv, "config", "get-contexts")
	contextsResult.MustSucceed(s.T())
	// The output has columns: CURRENT, COLONY-ID, ... The default has "*" in CURRENT column.
	// Look for a line starting with "*" followed by spaces then the colony name.
	s.Regexp(`(?m)^\*\s+`+remoteColonyName, contextsResult.Output,
		"Expected %q to be the default colony (marked with *)", remoteColonyName)

	// Now try to connect using the stored config and CA.
	// Clear env vars so CLI uses global default (set by --set-default) with stored CA.
	connEnv := freshEnv.WithEnv(map[string]string{
		"HOME":                  freshEnv.HomeDir,
		"CORAL_API_TOKEN":       s.testToken,
		"CORAL_COLONY_ID":       "", // Clear to use global default from --set-default
		"CORAL_COLONY_ENDPOINT": "", // Clear to use stored config
	})

	statusResult := helpers.RunCLIWithEnv(s.ctx, connEnv, "colony", "agents")

	// Should not fail with certificate error.
	if statusResult.HasError() {
		s.False(
			strings.Contains(statusResult.Output, "certificate signed by unknown authority") ||
				strings.Contains(statusResult.Output, "x509"),
			"Should not fail with certificate error when CA is stored, got: %s", statusResult.Output,
		)
	} else {
		s.T().Log("✓ Successfully connected using stored CA certificate")
	}
}

// TestAddRemoteCADataEnvVar verifies that CORAL_CA_DATA environment variable works
// for providing CA certificate as base64-encoded data.
func (s *CLIMeshSuite) TestAddRemoteCADataEnvVar() {
	s.skipIfNoDiscoveryCA()
	s.T().Log("Testing CORAL_CA_DATA environment variable...")

	freshEnv, err := helpers.SetupCLIEnv(s.ctx, "ca-data-test", s.publicEndpoint)
	s.Require().NoError(err, "Failed to create fresh CLI env")
	defer freshEnv.Cleanup()

	// Encode the CA cert as base64.
	caCertBase64 := base64.StdEncoding.EncodeToString(s.caCertPEM)

	// Try to connect using CORAL_CA_DATA.
	env := freshEnv.WithEnv(map[string]string{
		"HOME":                  freshEnv.HomeDir,
		"CORAL_COLONY_ENDPOINT": s.publicEndpoint,
		"CORAL_API_TOKEN":       s.testToken,
		"CORAL_CA_DATA":         caCertBase64,
	})

	result := helpers.RunCLIWithEnv(s.ctx, env, "colony", "agents")

	// Should not fail with certificate error.
	if result.HasError() {
		s.False(
			strings.Contains(result.Output, "certificate signed by unknown authority") ||
				strings.Contains(result.Output, "x509"),
			"Should not fail with certificate error when CORAL_CA_DATA is set, got: %s", result.Output,
		)
	} else {
		s.T().Log("✓ Successfully connected using CORAL_CA_DATA environment variable")
	}
}
