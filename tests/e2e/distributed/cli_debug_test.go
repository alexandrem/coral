package distributed

import (
	"encoding/json"

	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// CLIDebugSuite tests CLI debug and profiling commands.
//
// This suite validates:
// 1. Debug search command (coral debug search)
// 2. Debug session list command (coral debug session list)
// 3. Debug session get command (coral debug session get)
// 4. Query cpu-profile command (coral query cpu-profile)
// 5. Query memory-profile command (coral query memory-profile)
//
// Note: Requires mesh and services infrastructure to be running.
// Tests validate CLI UX and command correctness, not data accuracy.
type CLIDebugSuite struct {
	E2EDistributedSuite

	cliEnv *helpers.CLITestEnv
}

// SetupSuite runs once before all tests in the suite.
func (s *CLIDebugSuite) SetupSuite() {
	s.E2EDistributedSuite.SetupSuite()

	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	colonyID := "test-colony-e2e" // Default colony ID from docker-compose.

	s.cliEnv, err = helpers.SetupCLIEnv(s.ctx, colonyID, colonyEndpoint)
	s.Require().NoError(err, "Failed to setup CLI environment")

	s.T().Logf("CLI debug test environment ready: endpoint=%s, colonyID=%s", colonyEndpoint, colonyID)
}

// TearDownSuite cleans up after all tests.
func (s *CLIDebugSuite) TearDownSuite() {
	if s.cliEnv != nil {
		_ = s.cliEnv.Cleanup()
	}
	s.E2EDistributedSuite.TearDownSuite()
}

// TestDebugSearchCommand tests 'coral debug search <query>'.
//
// Validates:
// - Command executes successfully on a connected service
// - Result may be empty or non-empty but must not error
// - JSON output is valid JSON
func (s *CLIDebugSuite) TestDebugSearchCommand() {
	s.T().Log("Testing 'coral debug search' command...")

	helpers.EnsureServicesConnected(s.T(), s.ctx, s.fixture, 0, []helpers.ServiceConfig{
		{Name: "otel-app", Port: 8090, HealthEndpoint: "/health"},
	})

	// Text format — must succeed regardless of result count.
	result := s.cliEnv.Run(s.ctx, "debug", "search", "handler", "--service", "otel-app")
	result.MustSucceed(s.T())

	s.T().Log("Debug search output:")
	s.T().Log(result.Output)

	// JSON format — must produce valid JSON.
	jsonResult := s.cliEnv.Run(s.ctx, "debug", "search", "handler", "--service", "otel-app", "--format", "json")
	jsonResult.MustSucceed(s.T())

	s.Require().NotEmpty(jsonResult.Output, "JSON output should not be empty")

	var parsed interface{}
	s.Require().NoError(
		json.Unmarshal([]byte(jsonResult.Output), &parsed),
		"JSON output must be valid JSON",
	)

	s.T().Log("✓ debug search validated")
}

// TestDebugSessionListCommand tests 'coral debug session list'.
//
// Validates:
// - Command executes successfully (empty list is acceptable)
// - JSON output is valid JSON
func (s *CLIDebugSuite) TestDebugSessionListCommand() {
	s.T().Log("Testing 'coral debug session list' command...")

	// Text format.
	result := s.cliEnv.Run(s.ctx, "debug", "session", "list")
	result.MustSucceed(s.T())

	s.T().Log("Debug session list output:")
	s.T().Log(result.Output)

	// JSON format.
	jsonResult := s.cliEnv.Run(s.ctx, "debug", "session", "list", "--format", "json")
	jsonResult.MustSucceed(s.T())

	s.Require().NotEmpty(jsonResult.Output, "JSON output should not be empty")

	var parsed interface{}
	s.Require().NoError(
		json.Unmarshal([]byte(jsonResult.Output), &parsed),
		"JSON output must be valid JSON",
	)

	s.T().Log("✓ debug session list validated")
}

// TestDebugSessionGetNotFound tests 'coral debug session get' with a nonexistent ID.
//
// Validates:
// - Command fails with a non-zero exit code for a nonexistent session ID.
func (s *CLIDebugSuite) TestDebugSessionGetNotFound() {
	s.T().Log("Testing 'coral debug session get' with nonexistent session ID...")

	result := s.cliEnv.Run(s.ctx, "debug", "session", "get", "nonexistent-session-id-xyz")
	result.MustFail(s.T())

	s.T().Logf("Expected failure output: %s", result.Output)
	s.T().Log("✓ debug session get correctly fails for nonexistent session")
}

// TestQueryCPUProfileCommand tests 'coral query cpu-profile --service otel-app'.
//
// Validates:
// - Command executes without transport error (data may or may not exist)
// - JSON format flag is accepted
func (s *CLIDebugSuite) TestQueryCPUProfileCommand() {
	s.T().Log("Testing 'coral query cpu-profile' command...")

	helpers.EnsureServicesConnected(s.T(), s.ctx, s.fixture, 0, []helpers.ServiceConfig{
		{Name: "otel-app", Port: 8090, HealthEndpoint: "/health"},
	})

	// Default format (folded) — success or graceful "no data" message.
	result := s.cliEnv.Run(s.ctx, "query", "cpu-profile", "--service", "otel-app")
	result.MustSucceed(s.T())

	s.T().Log("CPU profile output (truncated):")
	out := result.Output
	if len(out) > 300 {
		out = out[:300] + "..."
	}
	s.T().Log(out)

	// JSON format flag — must be accepted without error.
	jsonResult := s.cliEnv.Run(s.ctx, "query", "cpu-profile", "--service", "otel-app", "--format", "json")
	jsonResult.MustSucceed(s.T())

	s.T().Log("✓ query cpu-profile validated")
}

// TestQueryMemoryProfileCommand tests 'coral query memory-profile --service otel-app'.
//
// Validates:
// - Command executes without transport error (data may or may not exist)
// - Summary format (default) is accepted
func (s *CLIDebugSuite) TestQueryMemoryProfileCommand() {
	s.T().Log("Testing 'coral query memory-profile' command...")

	helpers.EnsureServicesConnected(s.T(), s.ctx, s.fixture, 0, []helpers.ServiceConfig{
		{Name: "otel-app", Port: 8090, HealthEndpoint: "/health"},
	})

	// Default format (summary).
	result := s.cliEnv.Run(s.ctx, "query", "memory-profile", "--service", "otel-app")
	result.MustSucceed(s.T())

	s.T().Log("Memory profile output (truncated):")
	out := result.Output
	if len(out) > 300 {
		out = out[:300] + "..."
	}
	s.T().Log(out)

	// Folded format — also must be accepted.
	foldedResult := s.cliEnv.Run(s.ctx, "query", "memory-profile", "--service", "otel-app", "--format", "folded")
	foldedResult.MustSucceed(s.T())

	s.T().Log("✓ query memory-profile validated")
}
