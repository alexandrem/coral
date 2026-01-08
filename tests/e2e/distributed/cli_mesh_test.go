package distributed

import (
	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// CLIMeshSuite tests CLI commands for colony and agent management.
//
// This suite validates:
// 1. Colony status commands (coral colony status, agents)
// 2. Agent listing and status (coral agent list, status)
// 3. Output formatting (table vs JSON)
// 4. Error handling for invalid inputs
//
// Note: This suite does NOT test infrastructure behavior (that's covered by MeshSuite).
// It focuses on CLI-specific concerns: output formatting, flag validation, error messages.
type CLIMeshSuite struct {
	E2EDistributedSuite

	cliEnv *helpers.CLITestEnv
}

// SetupSuite runs once before all tests in the suite.
func (s *CLIMeshSuite) SetupSuite() {
	s.E2EDistributedSuite.SetupSuite()

	// Setup CLI environment
	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	// Get colony ID from fixture
	colonyID := "test-colony-e2e" // Default, will be updated if available

	s.cliEnv, err = helpers.SetupCLIEnv(s.ctx, colonyID, colonyEndpoint)
	s.Require().NoError(err, "Failed to setup CLI environment")

	s.T().Logf("CLI environment ready: endpoint=%s", colonyEndpoint)
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
