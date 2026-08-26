package distributed

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/llm"
	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// CLIAskDispatchSuite tests 'coral ask' dispatch-mode bootstrap parity (RFD
// 114): CLI dispatch is now the default, and both CLI and explicit MCP
// dispatch must fetch equivalent service and health context through the
// shared commandRunner abstraction (internal/cli/ask/agent.go). This exact
// parity gap — MCP-only bootstrap calling retired per-operation MCP tools,
// leaving CLI dispatch topology-only — was RFD 114's stated problem, but no
// e2e test exercised either dispatch mode's real system prompt content; the
// original implementation (commit 85ba5fb) only covers this at the unit
// level (internal/cli/ask/agent_test.go).
//
// `coral ask --debug` prints "[DEBUG] System prompt: ..." to stderr
// (internal/cli/ask/agent.go's Ask method), which RunCLIWithEnv captures via
// CombinedOutput — used here as the observation point instead of a new
// prompt-inspection API.
type CLIAskDispatchSuite struct {
	E2EDistributedSuite

	cliEnv *helpers.CLITestEnv
}

func (s *CLIAskDispatchSuite) SetupSuite() {
	s.E2EDistributedSuite.SetupSuite()

	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	s.cliEnv, err = helpers.SetupCLIEnv(s.ctx, "test-colony-e2e", colonyEndpoint)
	s.Require().NoError(err, "Failed to setup CLI environment")

	helpers.EnsureServicesConnected(s.T(), s.ctx, s.fixture, 0, []helpers.ServiceConfig{
		{Name: "otel-app", Port: 8090, HealthEndpoint: "/health"},
	})

	// Drive a real degraded state so the bootstrap ALERTS block (RFD 114:
	// "Include service and degraded-health context in both dispatch modes")
	// has something non-empty to carry in both dispatch modes.
	s.generateCheckoutErrors(60)
	s.waitForDegraded("otel-app", 90*time.Second)
}

func (s *CLIAskDispatchSuite) TearDownSuite() {
	helpers.DisconnectAllServices(s.T(), s.ctx, s.fixture, 0, []string{"otel-app"})

	if s.cliEnv != nil {
		_ = s.cliEnv.Cleanup()
	}
	s.E2EDistributedSuite.TearDownSuite()
}

func (s *CLIAskDispatchSuite) generateCheckoutErrors(n int) {
	endpoint, err := s.fixture.GetOTELAppEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get otel-app endpoint")

	client := &http.Client{Timeout: 5 * time.Second}
	url := fmt.Sprintf("http://%s/api/checkout", endpoint)
	for i := 0; i < n; i++ {
		resp, err := client.Post(url, "application/json", nil)
		if err != nil {
			s.T().Logf("checkout request %d failed: %v", i, err)
			continue
		}
		_ = resp.Body.Close()
	}
}

func (s *CLIAskDispatchSuite) waitForDegraded(serviceName string, timeout time.Duration) {
	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")
	colonyClient := helpers.NewColonyClient(colonyEndpoint)

	var lastSeen *colonyv1.UnifiedSummaryResult
	err = helpers.WaitForCondition(s.ctx, func() bool {
		resp, queryErr := helpers.QueryColonySummary(s.ctx, colonyClient, serviceName, "5m")
		if queryErr != nil {
			return false
		}
		for _, r := range resp.Summaries {
			if r.ServiceName == serviceName {
				lastSeen = r
				if r.Status == "critical" || r.Status == "degraded" {
					return true
				}
			}
		}
		return false
	}, timeout, 2*time.Second)
	if err != nil && lastSeen != nil {
		s.T().Logf("last seen %s summary: status=%s error_rate=%.1f%% avg_latency=%.0fms issues=%v",
			serviceName, lastSeen.Status, lastSeen.ErrorRate, lastSeen.AvgLatencyMs, lastSeen.Issues)
	}
	s.Require().NoError(err, "timed out waiting for %s to become critical/degraded", serviceName)
}

func (s *CLIAskDispatchSuite) createMockScript(script llm.MockScript) string {
	data, err := json.Marshal(script)
	s.Require().NoError(err)

	f, err := os.CreateTemp("", "mock_dispatch_script_*.json")
	s.Require().NoError(err)
	defer f.Close()

	_, err = f.Write(data)
	s.Require().NoError(err)

	return f.Name()
}

// pingPongScript is a minimal single-turn interaction; these tests assert on
// the system prompt content, not on tool-calling behavior.
func pingPongScript() llm.MockScript {
	return llm.MockScript{
		Interactions: []llm.MockInteraction{
			{
				ExpectedMessages: []llm.Message{
					{Role: "user", Content: "ping"},
				},
				Response: llm.MockResponse{Content: "pong"},
			},
		},
	}
}

// TestCLIDispatchIsDefaultAndBootstraps validates RFD 114's core dispatch
// change: with dispatch_mode unset, NewAgent selects CLI dispatch, and its
// system prompt carries real service and health bootstrap context fetched
// through coral CLI subprocesses (bootstrapServiceContext/bootstrapHealthAlerts).
func (s *CLIAskDispatchSuite) TestCLIDispatchIsDefaultAndBootstraps() {
	scriptPath := s.createMockScript(pingPongScript())
	defer os.Remove(scriptPath)

	result := helpers.RunAskDebug(s.ctx, s.cliEnv.EnvVars(), "ping", "mock:"+scriptPath)
	result.MustSucceed(s.T())

	helpers.AssertContains(s.T(), result.Output, "[DEBUG] System prompt:")
	// unset dispatch_mode must resolve to CLI, not MCP.
	helpers.AssertContains(s.T(), result.Output, "You operate in CLI dispatch mode.")
	helpers.AssertContains(s.T(), result.Output, "Available services: otel-app")
	helpers.AssertContains(s.T(), result.Output, "ALERTS (last 5m):")
	helpers.AssertContains(s.T(), result.Output, "otel-app")
}

// TestMCPDispatchBootstrapParity validates that explicit MCP dispatch
// (dispatch_mode: mcp) fetches equivalent service and health context through
// the proxy's sole coral_cli tool, not the retired per-operation MCP tools
// this RFD replaced.
func (s *CLIAskDispatchSuite) TestMCPDispatchBootstrapParity() {
	mcpEnv, err := helpers.SetupCLIEnv(s.ctx, "test-colony-e2e", s.cliEnv.ColonyEndpoint)
	s.Require().NoError(err, "Failed to setup MCP dispatch CLI environment")
	defer func() { _ = mcpEnv.Cleanup() }()

	s.setDispatchMode(mcpEnv, "mcp")

	scriptPath := s.createMockScript(pingPongScript())
	defer os.Remove(scriptPath)

	result := helpers.RunAskDebug(s.ctx, mcpEnv.EnvVars(), "ping", "mock:"+scriptPath)
	result.MustSucceed(s.T())

	helpers.AssertContains(s.T(), result.Output, "[DEBUG] System prompt:")
	// explicit mcp dispatch_mode must not build the CLI-mode prompt.
	helpers.AssertNotContains(s.T(), result.Output, "You operate in CLI dispatch mode.")
	helpers.AssertContains(s.T(), result.Output, "Available services: otel-app")
	helpers.AssertContains(s.T(), result.Output, "ALERTS (last 5m):")
	helpers.AssertContains(s.T(), result.Output, "otel-app")
}

// setDispatchMode rewrites the isolated colony config's ask.agent.dispatch_mode
// (internal/config/schema.go AskAgentConfig, RFD 114) after SetupCLIEnv has
// already written its default ("mode: ephemeral", dispatch_mode unset).
func (s *CLIAskDispatchSuite) setDispatchMode(env *helpers.CLITestEnv, mode string) {
	configPath := filepath.Join(env.ConfigDir, "colonies", env.ColonyID, "config.yaml")

	data, err := os.ReadFile(configPath)
	s.Require().NoError(err, "Failed to read colony config")

	var cfg map[string]interface{}
	s.Require().NoError(yaml.Unmarshal(data, &cfg))

	ask, _ := cfg["ask"].(map[string]interface{})
	if ask == nil {
		ask = map[string]interface{}{}
	}
	agent, _ := ask["agent"].(map[string]interface{})
	if agent == nil {
		agent = map[string]interface{}{}
	}
	agent["dispatch_mode"] = mode
	ask["agent"] = agent
	cfg["ask"] = ask

	out, err := yaml.Marshal(cfg)
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(configPath, out, 0600))
}
