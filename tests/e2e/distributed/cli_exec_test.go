package distributed

import (
	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// CLIExecSuite tests the 'coral exec' command (RFD 056 / RFD 093).
//
// Validates container name resolution via cgroup-based PID lookup:
// - Named exec targets the correct container in sidecar/DaemonSet mode.
// - Non-existent container name returns a not_found error.
//
// Requires agent-0 and otel-app to be running; otel-app shares agent-0's
// PID namespace (pid: "service:agent-0" in docker-compose).
type CLIExecSuite struct {
	E2EDistributedSuite

	cliEnv *helpers.CLITestEnv
}

// SetupSuite runs once before all tests in the suite.
func (s *CLIExecSuite) SetupSuite() {
	s.E2EDistributedSuite.SetupSuite()

	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	s.cliEnv, err = helpers.SetupCLIEnv(s.ctx, "test-colony-e2e", colonyEndpoint)
	s.Require().NoError(err, "Failed to setup CLI environment")
}

// agent0Addr returns the locally mapped gRPC address for agent-0.
// coral exec talks directly to the agent over gRPC, so we bypass mesh IP
// resolution and use the host-exposed port instead.
func (s *CLIExecSuite) agent0Addr() string {
	addr, err := s.fixture.GetAgentGRPCEndpoint(s.ctx, 0)
	s.Require().NoError(err, "Failed to get agent-0 endpoint")
	return addr
}

// TearDownSuite cleans up after all tests.
func (s *CLIExecSuite) TearDownSuite() {
	if s.cliEnv != nil {
		_ = s.cliEnv.Cleanup()
	}
	s.E2EDistributedSuite.TearDownSuite()
}

// TestContainerExecSidecarMode verifies that a named container exec targets
// the correct container via cgroup-based lookup (RFD 093).
//
// Runs 'coral exec otel-app --container otel-app cat /etc/hostname' against
// agent-0, which shares otel-app's PID namespace. The cgroup path for otel-app
// processes contains "otel-app" as a substring, so the lookup should succeed
// and return output from that container's filesystem.
func (s *CLIExecSuite) TestContainerExecSidecarMode() {
	s.T().Log("Testing 'coral exec --container otel-app cat /etc/hostname'...")

	helpers.EnsureServicesConnected(s.T(), s.ctx, s.fixture, 0, []helpers.ServiceConfig{
		{Name: "otel-app", Port: 8090, HealthEndpoint: "/health"},
	})

	result := s.cliEnv.Run(s.ctx,
		"exec", "otel-app",
		"--agent-addr", s.agent0Addr(),
		"--container", "otel-app",
		"cat", "/etc/hostname",
	)
	result.MustSucceed(s.T())

	s.Require().NotEmpty(result.Output, "exec should produce output from the container")
	s.T().Logf("Container hostname: %s", result.Output)
	s.T().Log("✓ container exec with named target validated")
}

// TestContainerExecNotFound verifies that a non-existent container name
// returns a not_found error rather than silently targeting the wrong process.
func (s *CLIExecSuite) TestContainerExecNotFound() {
	s.T().Log("Testing 'coral exec' with a non-existent container name...")

	helpers.EnsureServicesConnected(s.T(), s.ctx, s.fixture, 0, []helpers.ServiceConfig{
		{Name: "otel-app", Port: 8090, HealthEndpoint: "/health"},
	})

	result := s.cliEnv.Run(s.ctx,
		"exec", "otel-app",
		"--agent-addr", s.agent0Addr(),
		"--container", "definitely-not-a-real-container-xyzzy",
		"cat", "/etc/hostname",
	)
	result.MustFail(s.T())

	s.Require().True(
		result.ContainsOutput("not_found") || result.ContainsOutput("not found"),
		"error output should indicate not_found, got: %s", result.Output,
	)
	s.T().Log("✓ non-existent container name correctly returns not_found")
}
