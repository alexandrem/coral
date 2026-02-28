package distributed

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// DuckDBSuite tests DuckDB remote access via colony proxy.
type DuckDBSuite struct {
	E2EDistributedSuite

	cliEnv *helpers.CLITestEnv
}

// SetupSuite runs once before all tests in the suite.
func (s *DuckDBSuite) SetupSuite() {
	s.E2EDistributedSuite.SetupSuite()

	// Use local endpoint to create the initial token.
	localEndpoint := "http://localhost:9000"
	colonyID := s.fixture.ColonyID

	// Create a temporary CLI env for token creation.
	tempCliEnv, err := helpers.SetupCLIEnv(s.ctx, colonyID, localEndpoint)
	s.Require().NoError(err, "Failed to setup temp CLI environment for token creation")
	defer tempCliEnv.Cleanup()

	// Create an admin token.
	result := helpers.ColonyTokenCreate(s.ctx, tempCliEnv.EnvVars(), "duckdb-test", "admin")
	result.MustSucceed(s.T())

	// Extract token.
	var token string
	for _, line := range strings.Split(result.Output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Token: ") {
			token = strings.TrimPrefix(line, "Token: ")
			break
		}
	}
	s.Require().NotEmpty(token, "Failed to extract token from output")

	// Copy tokens.yaml to the colony container.
	tokensPath := filepath.Join(tempCliEnv.ColonyPath(colonyID), "tokens.yaml")
	destPath := fmt.Sprintf("coral-e2e-colony-1:/root/.coral/colonies/%s/tokens.yaml", colonyID)
	cmd := exec.Command("docker", "cp", tokensPath, destPath)
	err = cmd.Run()
	s.Require().NoError(err, "Failed to copy tokens.yaml to colony container")

	// Reload colony config so it picks up the new token.
	err = s.fixture.ReloadColonyConfig(s.ctx)
	s.Require().NoError(err, "Failed to reload colony config")

	// Now setup the permanent CLI environment with public HTTPS endpoint.
	colonyEndpoint := "https://localhost:8443"
	s.cliEnv, err = helpers.SetupCLIEnv(s.ctx, colonyID, colonyEndpoint)
	s.Require().NoError(err, "Failed to setup CLI environment")

	// Set insecure mode and token.
	s.cliEnv.ExtraVars["CORAL_INSECURE"] = "true"
	s.cliEnv.ExtraVars["CORAL_API_TOKEN"] = token

	s.T().Logf("DuckDB test environment ready: endpoint=%s, colonyID=%s", colonyEndpoint, colonyID)
}

// TearDownSuite cleans up after all tests.
func (s *DuckDBSuite) TearDownSuite() {
	if s.cliEnv != nil {
		_ = s.cliEnv.Cleanup()
	}
	s.E2EDistributedSuite.TearDownSuite()
}

// TestDuckDBListAgentsRemote tests 'coral duckdb list-agents --databases' via public endpoint.
func (s *DuckDBSuite) TestDuckDBListAgentsRemote() {
	s.T().Log("Testing 'coral duckdb list-agents --databases' via public endpoint...")

	// Execute command.
	result := helpers.DuckDBListAgents(s.ctx, s.cliEnv)
	result.MustSucceed(s.T())

	s.T().Log("List agents output:")
	s.T().Log(result.Output)

	// Verify output contains agent IDs and database names.
	s.Require().NotEmpty(result.Output, "Output should not be empty")

	// Expect at least one agent with metrics.duckdb.
	s.Require().Contains(result.Output, "metrics.duckdb", "Should list metrics.duckdb for agents")
}

// TestDuckDBQueryRemote tests 'coral duckdb query <agent-id> <sql>' via public endpoint.
func (s *DuckDBSuite) TestDuckDBQueryRemote() {
	s.T().Log("Testing 'coral duckdb query' via public endpoint...")

	// Get a healthy agent ID from registry (avoid stale agents loaded from DB).
	agents, err := helpers.ColonyAgentsJSON(s.ctx, s.cliEnv)
	s.Require().NoError(err)
	s.Require().NotEmpty(agents)

	agentID := helpers.GetHealthyAgentID(agents)
	s.Require().NotEmpty(agentID, "Expected to find at least one healthy agent")

	// Execute a simple query against the agent's database.
	// We query the internal duckdb_tables to verify connectivity without assuming data.
	sql := "SELECT database_name, table_name FROM duckdb_tables() LIMIT 1"
	result := helpers.DuckDBQuery(s.ctx, s.cliEnv, agentID, sql)
	result.MustSucceed(s.T())

	s.T().Log("Query result:")
	s.T().Log(result.Output)

	// Verify output looks like a table and contains expected headers or data.
	s.Require().Contains(strings.ToLower(result.Output), "database_name", "Should contain column header")
}

// TestDuckDBShellRemote tests 'coral duckdb shell <agent-id>' functionality via public endpoint.
func (s *DuckDBSuite) TestDuckDBShellRemote() {
	s.T().Log("Testing 'coral duckdb shell' via public endpoint...")

	agents, err := helpers.ColonyAgentsJSON(s.ctx, s.cliEnv)
	s.Require().NoError(err)
	s.Require().NotEmpty(agents)

	agentID := helpers.GetHealthyAgentID(agents)
	s.Require().NotEmpty(agentID, "Expected to find at least one healthy agent")

	// Pre-feed commands simulating user interaction
	// 1. .tables
	// 2. .exit
	input := ".tables\n.exit\n"
	stdin := strings.NewReader(input)

	result := helpers.DuckDBShell(s.ctx, s.cliEnv, agentID, stdin)
	result.MustSucceed(s.T())

	s.T().Log("Shell result:")
	s.T().Log(result.Output)

	// Verify output formatting from shell containing metadata
	s.Require().Contains(result.Output, "DuckDB interactive shell", "Should display welcome message")
	// Verify proxy success through proxying meta command outputs
	s.Require().Contains(result.Output, "database_name", "Should have listed tables header info from meta command")
}
