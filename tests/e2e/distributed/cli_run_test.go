package distributed

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// CLIRunSuite tests the `coral run` command (RFD 076 / RFD 093).
//
// This suite validates:
// - Inline TypeScript execution via the embedded Deno runtime
// - stdout capture and relay through the coral process
// - Non-zero exit code propagation on script failure
// - The --timeout flag is accepted
//
// Note: `coral run` invokes Deno without an import map, so scripts that
// import @coral/sdk require an explicit deno.json alongside the script.
// SDK import-map injection is tested via the coral_run MCP tool (see mcp_test.go).
type CLIRunSuite struct {
	E2EDistributedSuite

	cliEnv  *helpers.CLITestEnv
	scriptsDir string
}

// SetupSuite creates an isolated CLI environment and a temp directory to hold
// test scripts.
func (s *CLIRunSuite) SetupSuite() {
	s.E2EDistributedSuite.SetupSuite()

	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	s.cliEnv, err = helpers.SetupCLIEnv(s.ctx, "test-colony-e2e", colonyEndpoint)
	s.Require().NoError(err, "Failed to setup CLI environment")

	// Create a dedicated directory for test scripts inside the env's temp tree.
	s.scriptsDir = filepath.Join(s.cliEnv.TempDir, "scripts")
	s.Require().NoError(os.MkdirAll(s.scriptsDir, 0o700), "Failed to create scripts dir")
}

// TearDownSuite cleans up the CLI environment.
func (s *CLIRunSuite) TearDownSuite() {
	if s.cliEnv != nil {
		_ = s.cliEnv.Cleanup()
	}
	s.E2EDistributedSuite.TearDownSuite()
}

// writeScript writes TypeScript source to a named file in scriptsDir and
// returns its absolute path.
func (s *CLIRunSuite) writeScript(name, src string) string {
	path := filepath.Join(s.scriptsDir, name)
	s.Require().NoError(os.WriteFile(path, []byte(src), 0o600))
	return path
}

// TestRunBasicScript verifies that `coral run` executes a self-contained
// TypeScript script and relays its stdout output.
func (s *CLIRunSuite) TestRunBasicScript() {
	s.T().Log("Testing 'coral run' with a basic TypeScript script...")

	script := `
const result = {
  summary: "coral run ok",
  status: "healthy",
  data: { value: 42 },
};
console.log(JSON.stringify(result));
`
	path := s.writeScript("basic.ts", script)

	result := s.cliEnv.Run(s.ctx, "run", path)
	result.MustSucceed(s.T())

	s.T().Logf("Script output: %s", result.Output)
	s.Require().NotEmpty(result.Output, "Script should produce output")

	// Extract the JSON line from combined output (stderr may include Deno download notices).
	var parsed map[string]interface{}
	for _, line := range strings.Split(result.Output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "{") {
			s.Require().NoError(json.Unmarshal([]byte(line), &parsed),
				"Script stdout should be valid JSON")
			break
		}
	}
	s.Require().NotNil(parsed, "Should find a JSON object in output")
	s.Assert().Equal("coral run ok", parsed["summary"])
	s.Assert().Equal("healthy", parsed["status"])

	s.T().Log("✓ coral run executed script and captured stdout")
}

// TestRunScriptError verifies that `coral run` propagates a non-zero exit code
// when the script throws an uncaught exception.
func (s *CLIRunSuite) TestRunScriptError() {
	s.T().Log("Testing 'coral run' error propagation...")

	script := `throw new Error("intentional test failure");`
	path := s.writeScript("error.ts", script)

	result := s.cliEnv.Run(s.ctx, "run", path)

	s.Assert().NotEqual(0, result.ExitCode, "Failed script should return non-zero exit code")
	s.T().Logf("Exit code: %d, output: %s", result.ExitCode, result.Output)

	s.T().Log("✓ coral run propagated script failure exit code")
}

// TestRunTimeoutFlag verifies that the --timeout flag is accepted and does not
// cause an immediate error for a fast script.
func (s *CLIRunSuite) TestRunTimeoutFlag() {
	s.T().Log("Testing 'coral run' --timeout flag...")

	script := `console.log(JSON.stringify({ ok: true }));`
	path := s.writeScript("timeout.ts", script)

	result := s.cliEnv.Run(s.ctx, "run", path, "--timeout", "30")
	result.MustSucceed(s.T())

	s.Require().Contains(result.Output, `"ok":true`, "--timeout flag should not break execution")

	s.T().Log("✓ coral run --timeout flag accepted")
}

// TestRunHelpText verifies that `coral run --help` prints usage information.
func (s *CLIRunSuite) TestRunHelpText() {
	s.T().Log("Testing 'coral run --help'...")

	result := s.cliEnv.Run(s.ctx, "run", "--help")

	s.Require().NotEmpty(result.Output, "Help output should not be empty")
	s.Assert().Contains(result.Output, "run", "Help should mention the run command")
	s.Assert().Contains(result.Output, "script", "Help should describe the script argument")

	s.T().Log("✓ coral run --help produced usage text")
}
