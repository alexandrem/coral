package distributed

import (
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"gopkg.in/yaml.v3"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// ExePatternSuite tests RFD 111: connecting a portless process (one that
// never binds a listening socket, e.g. a queue consumer or batch worker) by
// executable pattern instead of name:port.
//
// worker-app (tests/e2e/distributed/fixtures/apps/worker-app) is reused as
// the portless fixture: it never binds a listening socket, runs in agent-0's
// PID namespace, and is already relied on by RFD 102's client-only
// auto-discovery test (TestClientOnlyWorkerDiscovery in cli_query_test.go).
// That test exercises the *automatic* discovery path (EnvVarProvider +
// ProcFSProvider); this suite exercises the *explicit* `coral connect
// --exe-pattern` path against the same binary, under a different service
// name so the two don't collide in the agent's service map.
type ExePatternSuite struct {
	E2EDistributedSuite
}

// NewExePatternSuite instantiates an ExePatternSuite.
func NewExePatternSuite(suite E2EDistributedSuite, t *testing.T) *ExePatternSuite {
	s := &ExePatternSuite{
		E2EDistributedSuite: suite,
	}
	s.SetT(t)
	return s
}

const (
	exePatternServiceName = "worker-explicit"
	// worker-app's binary is named "worker-app" (10 bytes), so it fits
	// within the 15-byte /proc/<pid>/comm field untruncated — an exact
	// match is safe and unambiguous among the other e2e fixture binaries.
	exePatternRegex = "worker-app"
)

// TearDownSuite disconnects the explicit connect so it doesn't leak into
// other suites sharing the same agent-0 fixture.
func (s *ExePatternSuite) TearDownSuite() {
	agentEndpoint, err := s.fixture.GetAgentGRPCEndpoint(s.ctx, 0)
	if err == nil {
		agentClient := helpers.NewAgentClient(agentEndpoint)
		_, _ = helpers.DisconnectService(s.ctx, agentClient, exePatternServiceName)
	}
}

// TestExePatternConnection verifies the full RFD 111 flow: connecting a
// portless process by executable pattern resolves a PID, is monitored by
// process-liveness instead of a network health check, and produces a Beyla
// exe_path rule carrying the literal pattern (not a name-derived one).
func (s *ExePatternSuite) TestExePatternConnection() {
	s.T().Log("Testing coral connect --exe-pattern (RFD 111)...")

	fixture := s.fixture

	agentEndpoint, err := fixture.GetAgentGRPCEndpoint(s.ctx, 0)
	s.Require().NoError(err, "Failed to get agent-0 endpoint")
	agentClient := helpers.NewAgentClient(agentEndpoint)

	// Best-effort: make sure a stale connection from a previous run isn't
	// still registered under this name.
	_, _ = helpers.DisconnectService(s.ctx, agentClient, exePatternServiceName)

	// Step 1: connect worker-app by executable pattern instead of port.
	s.T().Logf("Connecting %q by exe pattern %q...", exePatternServiceName, exePatternRegex)
	connectResp, err := helpers.ConnectServiceByExePattern(s.ctx, agentClient, exePatternServiceName, exePatternRegex)
	s.Require().NoError(err, "ConnectService with exe_pattern should succeed")
	s.Require().True(connectResp.Success)
	s.T().Logf("✓ %s connected: %s", exePatternServiceName, connectResp.ServiceName)

	// Step 2: verify it appears in ListServices as portless (Port == 0) and
	// carries the pattern back, with a monitor running immediately (RFD 111
	// fixes the agent so a portless connect starts a monitor even without a
	// health endpoint — see readiness gap #2 in the RFD).
	var worker *agentv1.ServiceStatus
	listResp, err := agentClient.ListServices(s.ctx, connect.NewRequest(&agentv1.ListServicesRequest{}))
	s.Require().NoError(err, "Failed to list services")
	for _, svc := range listResp.Msg.Services {
		if svc.Name == exePatternServiceName {
			worker = svc
			break
		}
	}
	s.Require().NotNil(worker, "%s should appear in agent's service list", exePatternServiceName)
	s.Require().Zero(worker.Port, "portless service should report port 0")
	s.Require().Equal(exePatternRegex, worker.ExePattern)
	s.Require().True(worker.HasMonitor, "a portless connect must start a monitor")
	s.T().Logf("✓ %s found in agent service list (exe_pattern: %s)", exePatternServiceName, worker.ExePattern)

	// Step 3: wait for the process-liveness check to resolve a PID and
	// report healthy (worker-app is a long-running loop, so this should
	// settle quickly).
	s.T().Log("Waiting for process-liveness check to resolve PID and report healthy...")
	var resolved *agentv1.ServiceStatus
	err = helpers.WaitForCondition(s.ctx, func() bool {
		resp, listErr := agentClient.ListServices(s.ctx, connect.NewRequest(&agentv1.ListServicesRequest{}))
		if listErr != nil {
			return false
		}
		for _, svc := range resp.Msg.Services {
			if svc.Name == exePatternServiceName && svc.ProcessId != 0 && svc.Status == "healthy" {
				resolved = svc
				return true
			}
		}
		return false
	}, 30*time.Second, 2*time.Second)
	s.Require().NoError(err, "%s should resolve a PID and report healthy within 30s", exePatternServiceName)
	s.T().Logf("✓ %s resolved to PID %d, status: %s", exePatternServiceName, resolved.ProcessId, resolved.Status)

	// Step 4: verify Beyla's generated config carries the literal exe
	// pattern as the rule's exe_path (RFD 111 readiness gap #3 — previously
	// generateBeylaConfig always derived exe_path from the service name,
	// discarding any pattern the caller supplied).
	s.T().Log("Verifying Beyla config carries the literal exe_pattern...")
	var beylaConfig string
	err = helpers.WaitForCondition(s.ctx, func() bool {
		cfg, cfgErr := fixture.GetBeylaConfig(s.ctx, "agent-0")
		if cfgErr != nil {
			return false
		}
		beylaConfig = cfg
		return strings.Contains(cfg, exePatternServiceName)
	}, 60*time.Second, 5*time.Second)
	if err != nil {
		s.T().Logf("Beyla config on agent-0 (last seen):\n%s", beylaConfig)
		s.Require().Fail("timed out waiting for the explicit exe_pattern rule to appear in Beyla config")
		return
	}

	var cfg struct {
		Discovery struct {
			Services []struct {
				Name      string `yaml:"name"`
				OpenPorts string `yaml:"open_ports"`
				ExePath   string `yaml:"exe_path"`
			} `yaml:"services"`
		} `yaml:"discovery"`
	}
	s.Require().NoError(yaml.Unmarshal([]byte(beylaConfig), &cfg), "Beyla config must be valid YAML")

	var found bool
	for _, svc := range cfg.Discovery.Services {
		if svc.Name != exePatternServiceName {
			continue
		}
		found = true
		s.Require().Empty(svc.OpenPorts, "portless connect must not get an open_ports rule")
		s.Require().Equal(exePatternRegex, svc.ExePath,
			"exe_path must be the literal exe_pattern, not a name-derived .*name.* rule")
	}
	s.Require().True(found, "expected a Beyla service rule named %q", exePatternServiceName)

	s.T().Log("✓ Explicit exe_pattern connect verified end to end")
	s.T().Log("  - Connected without a port via --exe-pattern")
	s.T().Log("  - Monitor started and resolved a PID via process-liveness")
	s.T().Log("  - Beyla config carries the literal pattern, not a name-derived one")
}

// TestExePatternRecoversAfterRestart verifies that when the matched process
// is replaced (container restart -> new PID under the same name), the
// monitor re-resolves the pattern and recovers to healthy rather than
// getting stuck on the old, now-dead PID. Depends on
// TestExePatternConnection having already connected exePatternServiceName;
// run after it in Test2_ServiceManagement.
func (s *ExePatternSuite) TestExePatternRecoversAfterRestart() {
	s.T().Log("Testing process-liveness recovery after worker-app restart (RFD 111)...")

	fixture := s.fixture

	agentEndpoint, err := fixture.GetAgentGRPCEndpoint(s.ctx, 0)
	s.Require().NoError(err, "Failed to get agent-0 endpoint")
	agentClient := helpers.NewAgentClient(agentEndpoint)

	// Make sure the service is connected and has a resolved PID to compare
	// against, in case this test runs standalone rather than after
	// TestExePatternConnection.
	_, _ = helpers.ConnectServiceByExePattern(s.ctx, agentClient, exePatternServiceName, exePatternRegex)

	var before *agentv1.ServiceStatus
	err = helpers.WaitForCondition(s.ctx, func() bool {
		resp, listErr := agentClient.ListServices(s.ctx, connect.NewRequest(&agentv1.ListServicesRequest{}))
		if listErr != nil {
			return false
		}
		for _, svc := range resp.Msg.Services {
			if svc.Name == exePatternServiceName && svc.ProcessId != 0 {
				before = svc
				return true
			}
		}
		return false
	}, 30*time.Second, 2*time.Second)
	s.Require().NoError(err, "%s should have a resolved PID before restarting", exePatternServiceName)
	s.T().Logf("Current PID before restart: %d", before.ProcessId)

	// Restart worker-app's container. It shares agent-0's PID namespace
	// (docker-compose.e2e.yml: pid: "service:agent-0"), so this replaces
	// the process the pattern matches with a new PID, same as a real
	// deploy restarting a portless worker.
	s.T().Log("Restarting worker-app container...")
	s.Require().NoError(fixture.RestartService(s.ctx, "worker-app"), "Failed to restart worker-app")

	// The monitor re-resolves the pattern on its check interval; expect it
	// to settle on a new (different) PID and report healthy again. No
	// fixed miss-threshold window is asserted here — only the end state,
	// since the exact number of misses during the restart window depends
	// on container restart timing.
	s.T().Log("Waiting for the monitor to recover with a new PID...")
	var after *agentv1.ServiceStatus
	err = helpers.WaitForCondition(s.ctx, func() bool {
		resp, listErr := agentClient.ListServices(s.ctx, connect.NewRequest(&agentv1.ListServicesRequest{}))
		if listErr != nil {
			return false
		}
		for _, svc := range resp.Msg.Services {
			if svc.Name == exePatternServiceName && svc.ProcessId != 0 &&
				svc.ProcessId != before.ProcessId && svc.Status == "healthy" {
				after = svc
				return true
			}
		}
		return false
	}, 90*time.Second, 5*time.Second)
	s.Require().NoError(err, "%s should recover to healthy on a new PID within 90s of restart", exePatternServiceName)
	s.T().Logf("✓ Recovered: PID %d -> %d, status: %s", before.ProcessId, after.ProcessId, after.Status)
}

// TestExePatternRejectsPortAndPattern verifies the mutual-exclusion
// validation added in RFD 111: a spec with both port and exe_pattern set is
// rejected by the agent, not silently accepted with one side ignored.
func (s *ExePatternSuite) TestExePatternRejectsPortAndPattern() {
	agentEndpoint, err := s.fixture.GetAgentGRPCEndpoint(s.ctx, 0)
	s.Require().NoError(err, "Failed to get agent-0 endpoint")
	agentClient := helpers.NewAgentClient(agentEndpoint)

	resp, err := agentClient.ConnectService(s.ctx, connect.NewRequest(&agentv1.ConnectServiceRequest{
		Name:       "bad-both-port-and-pattern",
		Port:       9999,
		ExePattern: "anything",
	}))
	s.Require().NoError(err, "the RPC itself should succeed; validation failure is reported in the response")
	s.Require().False(resp.Msg.Success, "a spec with both port and exe_pattern must be rejected")
	s.T().Logf("✓ Rejected as expected: %s", resp.Msg.Error)
}
