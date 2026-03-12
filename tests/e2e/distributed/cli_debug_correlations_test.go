package distributed

import (
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// CLIDebugCorrelationsSuite tests `coral debug correlations` CLI commands (RFD 091).
//
// This suite validates the operator-facing CLI for correlation management:
// - `coral debug correlations` (list, table format)
// - `coral debug correlations --format json` (JSON output)
// - `coral debug correlations --service <name>` (service filter)
// - `coral debug correlations remove <id>` (removal)
// - Error handling for non-existent IDs
//
// State is set up via the gRPC helpers (same path as the E2E RPC test);
// the CLI commands are the system under test.
type CLIDebugCorrelationsSuite struct {
	E2EDistributedSuite

	cliEnv *helpers.CLITestEnv
}

// SetupSuite connects sdk-app and initialises the CLI environment.
func (s *CLIDebugCorrelationsSuite) SetupSuite() {
	s.T().Log("Setting up CLIDebugCorrelationsSuite...")

	helpers.EnsureServicesConnected(s.T(), s.ctx, s.fixture, 1, []helpers.ServiceConfig{
		{Name: "sdk-app", Port: 3001, HealthEndpoint: "/health"},
	})

	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	s.cliEnv, err = helpers.SetupCLIEnv(s.ctx, "test-colony-e2e", colonyEndpoint)
	s.Require().NoError(err, "Failed to setup CLI environment")

	s.T().Log("CLIDebugCorrelationsSuite setup complete")
}

// TearDownSuite disconnects services and cleans up the CLI env.
func (s *CLIDebugCorrelationsSuite) TearDownSuite() {
	helpers.DisconnectAllServices(s.T(), s.ctx, s.fixture, 1, []string{"sdk-app"})
	if s.cliEnv != nil {
		_ = s.cliEnv.Cleanup()
	}
	s.E2EDistributedSuite.TearDownSuite()
}

// TestCorrelationsListEmpty verifies `coral debug correlations` prints the
// empty-state message when no descriptors are deployed.
func (s *CLIDebugCorrelationsSuite) TestCorrelationsListEmpty() {
	s.T().Log("Testing 'coral debug correlations' with no active descriptors...")

	result := helpers.DebugCorrelations(s.ctx, s.cliEnv, "")
	result.MustSucceed(s.T())

	s.T().Logf("Output: %s", result.Output)
	s.Require().Contains(result.Output, "No active correlations",
		"Expected empty-state message when no correlations are deployed")

	s.T().Log("✓ Empty-state message verified")
}

// TestCorrelationsListShowsDeployed verifies that a deployed descriptor appears
// in `coral debug correlations` table output.
func (s *CLIDebugCorrelationsSuite) TestCorrelationsListShowsDeployed() {
	s.T().Log("Testing 'coral debug correlations' shows deployed descriptor...")

	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err)
	debugClient := helpers.NewDebugClient(colonyEndpoint)

	// Deploy a descriptor via gRPC.
	corrID := "e2e-cli-list-test"
	desc := &agentv1.CorrelationDescriptor{
		Id:          corrID,
		Strategy:    agentv1.StrategyKind_RATE_GATE,
		ServiceName: "sdk-app",
		Source:      &agentv1.SourceSpec{Probe: "main.ProcessPayment"},
		Window:      durationpb.New(5 * time.Second),
		Threshold:   3,
		Action:      &agentv1.ActionSpec{Kind: agentv1.ActionKind_EMIT_EVENT},
		CooldownMs:  1000,
	}
	deployResp, err := helpers.DeployCorrelation(s.ctx, debugClient, "sdk-app", desc)
	s.Require().NoError(err, "DeployCorrelation should succeed")
	s.Require().True(deployResp.Success, "Deploy should succeed: %s", deployResp.Error)
	defer func() {
		_, _ = helpers.RemoveCorrelation(s.ctx, debugClient, corrID, "sdk-app")
	}()

	// Verify CLI output contains the correlation ID and strategy.
	result := helpers.DebugCorrelations(s.ctx, s.cliEnv, "")
	result.MustSucceed(s.T())

	s.T().Logf("Output:\n%s", result.Output)
	s.Require().Contains(result.Output, corrID, "Table should contain correlation ID")
	s.Require().Contains(result.Output, "RATE_GATE", "Table should contain strategy name")
	s.Require().Contains(result.Output, "sdk-app", "Table should contain service name")

	s.T().Log("✓ Deployed descriptor visible in table output")
}

// TestCorrelationsListJSON verifies `coral debug correlations --format json`
// returns valid JSON with the expected structure.
func (s *CLIDebugCorrelationsSuite) TestCorrelationsListJSON() {
	s.T().Log("Testing 'coral debug correlations --format json'...")

	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err)
	debugClient := helpers.NewDebugClient(colonyEndpoint)

	corrID := "e2e-cli-json-test"
	desc := &agentv1.CorrelationDescriptor{
		Id:          corrID,
		Strategy:    agentv1.StrategyKind_EDGE_TRIGGER,
		ServiceName: "sdk-app",
		Source:      &agentv1.SourceSpec{Probe: "main.ProcessPayment"},
		Action:      &agentv1.ActionSpec{Kind: agentv1.ActionKind_EMIT_EVENT},
	}
	deployResp, err := helpers.DeployCorrelation(s.ctx, debugClient, "sdk-app", desc)
	s.Require().NoError(err)
	s.Require().True(deployResp.Success, "Deploy should succeed: %s", deployResp.Error)
	defer func() {
		_, _ = helpers.RemoveCorrelation(s.ctx, debugClient, corrID, "sdk-app")
	}()

	out, err := helpers.DebugCorrelationsJSON(s.ctx, s.cliEnv, "")
	s.Require().NoError(err, "JSON output must be valid JSON")
	s.Require().NotNil(out, "JSON output must not be nil")

	// The response wraps a ColonyListCorrelationsResponse.
	s.Require().Contains(out, "descriptors", "JSON must have 'descriptors' field")
	s.T().Logf("✓ JSON output valid: %v", out)
}

// TestCorrelationsListServiceFilter verifies `coral debug correlations --service`
// restricts output to the specified service.
func (s *CLIDebugCorrelationsSuite) TestCorrelationsListServiceFilter() {
	s.T().Log("Testing 'coral debug correlations --service sdk-app'...")

	// Filter to a service that has no correlations — should show empty state,
	// not fail with an error.
	result := helpers.DebugCorrelations(s.ctx, s.cliEnv, "no-such-service")
	result.MustSucceed(s.T())
	s.Require().Contains(result.Output, "No active correlations",
		"Filter to unknown service should show empty state, not error")

	// Filter to sdk-app (which has none right now) — same.
	result = helpers.DebugCorrelations(s.ctx, s.cliEnv, "sdk-app")
	result.MustSucceed(s.T())

	s.T().Log("✓ Service filter flag works")
}

// TestCorrelationsRemove verifies `coral debug correlations remove <id>` removes
// a deployed descriptor and that it no longer appears in list output.
func (s *CLIDebugCorrelationsSuite) TestCorrelationsRemove() {
	s.T().Log("Testing 'coral debug correlations remove'...")

	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err)
	debugClient := helpers.NewDebugClient(colonyEndpoint)

	corrID := "e2e-cli-remove-test"
	desc := &agentv1.CorrelationDescriptor{
		Id:          corrID,
		Strategy:    agentv1.StrategyKind_ABSENCE,
		ServiceName: "sdk-app",
		Source:      &agentv1.SourceSpec{Probe: "main.ProcessPayment"},
		Window:      durationpb.New(30 * time.Second),
		Action:      &agentv1.ActionSpec{Kind: agentv1.ActionKind_EMIT_EVENT},
	}
	deployResp, err := helpers.DeployCorrelation(s.ctx, debugClient, "sdk-app", desc)
	s.Require().NoError(err)
	s.Require().True(deployResp.Success, "Deploy should succeed: %s", deployResp.Error)

	// Confirm it appears in table output.
	list := helpers.DebugCorrelations(s.ctx, s.cliEnv, "")
	list.MustSucceed(s.T())
	s.Require().Contains(list.Output, corrID, "Deployed descriptor must appear before removal")

	// Remove via CLI.
	removeResult := helpers.DebugCorrelationsRemove(s.ctx, s.cliEnv, corrID)
	removeResult.MustSucceed(s.T())
	s.T().Logf("Remove output: %s", removeResult.Output)
	s.Require().True(
		strings.Contains(removeResult.Output, corrID) || strings.Contains(removeResult.Output, "removed"),
		"Remove output should confirm removal",
	)

	// Verify it is gone.
	listAfter := helpers.DebugCorrelations(s.ctx, s.cliEnv, "")
	listAfter.MustSucceed(s.T())
	s.Require().NotContains(listAfter.Output, corrID,
		"Removed descriptor must not appear in subsequent list output")

	s.T().Log("✓ CLI remove verified: descriptor deployed, removed, and confirmed absent")
}

// TestCorrelationsRemoveNotFound verifies `coral debug correlations remove` fails
// gracefully when the correlation ID does not exist.
func (s *CLIDebugCorrelationsSuite) TestCorrelationsRemoveNotFound() {
	s.T().Log("Testing 'coral debug correlations remove' with non-existent ID...")

	result := helpers.DebugCorrelationsRemove(s.ctx, s.cliEnv, "does-not-exist-xyz")
	// Should fail (non-zero exit) with a meaningful error message, not panic.
	s.Require().True(result.HasError() || strings.Contains(result.Output, "not found") ||
		strings.Contains(result.Output, "error") || strings.Contains(result.Output, "failed"),
		"Removing non-existent correlation should report an error\nOutput: %s", result.Output)

	s.T().Logf("Output: %s", result.Output)
	s.T().Log("✓ Non-existent ID handled gracefully")
}
