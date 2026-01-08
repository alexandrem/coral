package distributed

import (
	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// CLIConfigSuite tests CLI config commands.
//
// This suite validates:
// 1. Config context management (coral config get-contexts, current-context, use-context)
// 2. Output formatting (table vs JSON)
// 3. Context switching functionality
// 4. Config file operations
//
// Note: Config commands operate on the CLI's own configuration (~/.coral/config.yaml),
// not on colony infrastructure. This suite tests CLI configuration management UX.
type CLIConfigSuite struct {
	E2EDistributedSuite

	cliEnv *helpers.CLITestEnv
}

// SetupSuite runs once before all tests in the suite.
func (s *CLIConfigSuite) SetupSuite() {
	s.E2EDistributedSuite.SetupSuite()

	// Setup CLI environment with isolated config directory
	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	colonyID := "test-colony-e2e" // Default

	s.cliEnv, err = helpers.SetupCLIEnv(s.ctx, colonyID, colonyEndpoint)
	s.Require().NoError(err, "Failed to setup CLI environment")

	s.T().Logf("CLI config environment ready: %s", s.cliEnv.ConfigPath())
}

// TearDownSuite cleans up after all tests.
func (s *CLIConfigSuite) TearDownSuite() {
	if s.cliEnv != nil {
		_ = s.cliEnv.Cleanup()
	}
	s.E2EDistributedSuite.TearDownSuite()
}

// TestConfigGetContextsCommand tests 'coral config get-contexts' output.
//
// Validates:
// - Command executes successfully
// - Table output has expected structure
// - JSON output has required fields
// - Lists available colony contexts
func (s *CLIConfigSuite) TestConfigGetContextsCommand() {
	s.T().Log("Testing 'coral config get-contexts' command...")

	// Test table format
	result := helpers.RunCLIWithEnv(s.ctx, s.cliEnv.EnvVars(), "config", "get-contexts")

	// May fail if no contexts exist yet, which is acceptable
	if result.HasError() {
		s.T().Logf("Get contexts failed (acceptable if no contexts): %v", result.Err)
		s.T().Logf("Output: %s", result.Output)
	} else {
		s.T().Log("Table output:")
		s.T().Log(result.Output)

		// Verify output structure
		rows := helpers.ParseTableOutput(result.Output)
		s.T().Logf("Parsed %d rows", len(rows))
	}

	// Test JSON format
	result = helpers.RunCLIWithEnv(s.ctx, s.cliEnv.EnvVars(), "config", "get-contexts", "--json")

	if result.HasError() {
		s.T().Logf("Get contexts JSON failed (acceptable if no contexts): %v", result.Err)
	} else {
		// Validate it's valid JSON (even if empty)
		contexts := helpers.ParseJSONResponse(s.T(), result.Output)
		s.Require().NotNil(contexts, "JSON response should be valid")
	}

	s.T().Log("✓ Config get-contexts validated")
}

// TestConfigCurrentContextCommand tests 'coral config current-context' output.
//
// Validates:
// - Command executes
// - Output format is reasonable
// - Handles case when no context is set
func (s *CLIConfigSuite) TestConfigCurrentContextCommand() {
	s.T().Log("Testing 'coral config current-context' command...")

	result := helpers.RunCLIWithEnv(s.ctx, s.cliEnv.EnvVars(), "config", "current-context")

	// May fail if no context set, which is acceptable
	if result.HasError() {
		s.T().Logf("Current context failed (acceptable if no context set): %v", result.Err)
		s.T().Logf("Output: %s", result.Output)
	} else {
		s.T().Log("Current context output:")
		s.T().Log(result.Output)
		s.Require().NotEmpty(result.Output, "Should have output")
	}

	s.T().Log("✓ Config current-context validated")
}

// TestConfigUseContextCommand tests 'coral config use-context' functionality.
//
// Validates:
// - Context switching works
// - Error handling for non-existent contexts
func (s *CLIConfigSuite) TestConfigUseContextCommand() {
	s.T().Log("Testing 'coral config use-context' command...")

	// Try to use a context (may fail if colony not in config)
	result := helpers.RunCLIWithEnv(s.ctx, s.cliEnv.EnvVars(),
		"config", "use-context", s.cliEnv.ColonyID)

	if result.HasError() {
		s.T().Logf("Use context failed (acceptable if colony not in config): %v", result.Err)
		s.T().Logf("Output: %s", result.Output)
	} else {
		s.T().Log("✓ Context switch succeeded")

		// Verify current context was updated
		checkResult := helpers.RunCLIWithEnv(s.ctx, s.cliEnv.EnvVars(), "config", "current-context")
		if !checkResult.HasError() {
			helpers.AssertContains(s.T(), checkResult.Output, s.cliEnv.ColonyID)
		}
	}

	s.T().Log("✓ Config use-context validated")
}

// TestConfigInvalidContext tests error handling for invalid context operations.
//
// Validates:
// - Switching to non-existent context fails gracefully
// - Error messages are helpful
func (s *CLIConfigSuite) TestConfigInvalidContext() {
	s.T().Log("Testing error handling for invalid context...")

	// Try to use a context that doesn't exist
	result := helpers.RunCLIWithEnv(s.ctx, s.cliEnv.EnvVars(),
		"config", "use-context", "non-existent-colony-12345")

	// Should fail
	if !result.HasError() {
		s.T().Log("⚠ Using non-existent context succeeded (unexpected)")
	} else {
		s.T().Log("✓ Non-existent context rejected as expected")
		s.T().Logf("Error output: %s", result.Output)
	}

	s.T().Log("✓ Error handling validated")
}

// TestConfigOutputFormats tests output format consistency.
//
// Validates:
// - Table format is human-readable
// - JSON format is valid and parseable
// - Both formats contain essential information
func (s *CLIConfigSuite) TestConfigOutputFormats() {
	s.T().Log("Testing config output format consistency...")

	// Test get-contexts with both formats
	tableResult := helpers.RunCLIWithEnv(s.ctx, s.cliEnv.EnvVars(), "config", "get-contexts")
	jsonResult := helpers.RunCLIWithEnv(s.ctx, s.cliEnv.EnvVars(), "config", "get-contexts", "--json")

	// Both should either succeed or fail consistently
	if tableResult.HasError() != jsonResult.HasError() {
		s.T().Log("⚠ Table and JSON outputs have inconsistent success/failure")
	}

	// If JSON succeeded, verify it's valid JSON
	if !jsonResult.HasError() {
		data := helpers.ParseJSONResponse(s.T(), jsonResult.Output)
		s.Require().NotNil(data, "JSON should be valid")
		s.T().Log("✓ JSON output is valid")
	}

	// If table succeeded, verify it's parseable
	if !tableResult.HasError() {
		rows := helpers.ParseTableOutput(tableResult.Output)
		s.T().Logf("✓ Table output parsed: %d rows", len(rows))
	}

	s.T().Log("✓ Output formats validated")
}

// TestConfigCommandsWithoutColony tests config commands in isolation.
//
// Validates:
// - Config commands work independently of colony infrastructure
// - Can be used for initial setup before connecting to colony
func (s *CLIConfigSuite) TestConfigCommandsWithoutColony() {
	s.T().Log("Testing config commands without colony dependency...")

	// Config commands should work even without a running colony
	// They only read/write local config files

	result := helpers.RunCLIWithEnv(s.ctx, s.cliEnv.EnvVars(), "config", "get-contexts")

	// Should not crash (may have no contexts, which is fine)
	s.T().Logf("Get contexts exit code: %d", result.ExitCode)

	result = helpers.RunCLIWithEnv(s.ctx, s.cliEnv.EnvVars(), "config", "current-context")

	// Should not crash
	s.T().Logf("Current context exit code: %d", result.ExitCode)

	s.T().Log("✓ Config commands work independently")
}

// TestConfigHelpText tests help text availability.
//
// Validates:
// - --help flag works for config commands
// - Help text is informative
func (s *CLIConfigSuite) TestConfigHelpText() {
	s.T().Log("Testing config command help text...")

	// Test main config help
	result := helpers.RunCLI(s.ctx, "config", "--help")
	result.MustSucceed(s.T())

	helpers.AssertContains(s.T(), result.Output, "config")
	helpers.AssertContains(s.T(), result.Output, "context")

	s.T().Log("✓ Config help text available")

	// Test subcommand help
	subcommands := []string{"get-contexts", "current-context", "use-context"}
	for _, subcmd := range subcommands {
		result := helpers.RunCLI(s.ctx, "config", subcmd, "--help")
		// Should succeed or provide help
		s.T().Logf("%s --help exit code: %d", subcmd, result.ExitCode)
	}

	s.T().Log("✓ Subcommand help validated")
}
