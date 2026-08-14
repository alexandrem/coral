package distributed

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// ContainerNamespaceSuite tests RFD 112: namespace-aware process discovery.
//
// netns-app (tests/e2e/distributed/fixtures/apps/netns-app) shares agent-0's
// PID namespace (docker-compose `pid: "service:agent-0"`) but runs in its
// own, separate network namespace — no `network_mode` override, no Docker
// socket, no `network_mode: host`. This is the same visibility gap a
// privileged host Coral agent has against an ordinary Docker Compose
// application container. Under plain MonitorAll (no explicit `coral
// connect`, no static config — see docker-compose.e2e.yml), DiscoveryManager
// must still find netns-app's listener via ProcFSProvider's namespace-aware
// socket-table scan, and Beyla must actually attach and capture HTTP traffic
// across that namespace boundary.
type ContainerNamespaceSuite struct {
	E2EDistributedSuite
}

// NewContainerNamespaceSuite instantiates a ContainerNamespaceSuite.
func NewContainerNamespaceSuite(suite E2EDistributedSuite, t *testing.T) *ContainerNamespaceSuite {
	s := &ContainerNamespaceSuite{
		E2EDistributedSuite: suite,
	}
	s.SetT(t)
	return s
}

// TestNetnsAppAutoDiscovered verifies DiscoveryManager (via ProcFSProvider)
// automatically discovers netns-app's PID and its namespace-local listening
// port 8080, without any explicit `coral connect` or static config, and
// emits an open_ports Beyla rule for it — proving the namespace-aware scan
// (RFD 112) found a listener that a host-namespace-only scan would have
// missed entirely.
func (s *ContainerNamespaceSuite) TestNetnsAppAutoDiscovered() {
	s.T().Log("Testing automatic discovery of a container-namespaced listener (RFD 112)...")

	const (
		discoveryTimeout  = 60 * time.Second
		discoveryInterval = 5 * time.Second
	)

	var beylaConfig string
	err := helpers.WaitForCondition(s.ctx, func() bool {
		cfg, err := s.fixture.GetBeylaConfig(s.ctx, "agent-0")
		if err != nil {
			return false
		}
		beylaConfig = cfg
		return strings.Contains(cfg, "netns-app")
	}, discoveryTimeout, discoveryInterval)

	if err != nil {
		s.T().Logf("Beyla config on agent-0 (last seen):\n%s", beylaConfig)
		s.Require().Fail("timed out waiting for netns-app's open_ports rule to appear in Beyla config")
		return
	}

	s.T().Logf("Beyla config on agent-0:\n%s", beylaConfig)

	var cfg struct {
		Discovery struct {
			Services []struct {
				Name      string `yaml:"name"`
				OpenPorts string `yaml:"open_ports"`
			} `yaml:"services"`
		} `yaml:"discovery"`
	}
	s.Require().NoError(yaml.Unmarshal([]byte(beylaConfig), &cfg), "Beyla config must be valid YAML")

	var found bool
	for _, svc := range cfg.Discovery.Services {
		if svc.Name != "netns-app" {
			continue
		}
		found = true
		s.Require().Contains(svc.OpenPorts, "8080",
			"netns-app must get an open_ports rule for its namespace-local port 8080 "+
				"(not the host-published port 18085)")
	}
	s.Require().True(found, "expected a Beyla service rule named %q for the container-namespaced listener", "netns-app")

	s.T().Log("✓ netns-app discovered automatically via namespace-aware ProcFSProvider")
}

// TestNetnsAppBeylaCapturesTraffic verifies Beyla actually attaches to and
// captures HTTP traffic for a process whose listening socket lives outside
// agent-0's own network namespace — the end-to-end proof that namespace-
// aware discovery (RFD 112) results in working instrumentation, not just a
// correctly-named rule.
func (s *ContainerNamespaceSuite) TestNetnsAppBeylaCapturesTraffic() {
	s.T().Log("Testing Beyla eBPF instrumentation across a network namespace boundary (RFD 112)...")

	netnsAppEndpoint, err := s.fixture.GetNetnsAppEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get netns-app endpoint")

	// Wait for auto-discovery to land before generating traffic, mirroring
	// TestNetnsAppAutoDiscovered but without re-asserting config details.
	err = helpers.WaitForCondition(s.ctx, func() bool {
		cfg, err := s.fixture.GetBeylaConfig(s.ctx, "agent-0")
		return err == nil && strings.Contains(cfg, "netns-app")
	}, 60*time.Second, 5*time.Second)
	s.Require().NoError(err, "netns-app was not auto-discovered in time")

	agentEndpoint, err := s.fixture.GetAgentGRPCEndpoint(s.ctx, 0)
	s.Require().NoError(err, "Failed to get agent gRPC endpoint")
	agentClient := helpers.NewAgentClient(agentEndpoint)

	s.T().Log("Generating HTTP traffic to netns-app (its own network namespace)...")
	client := &http.Client{Timeout: 5 * time.Second}
	requestCount := 0
	for i := 0; i < 20; i++ {
		resp, err := client.Get(fmt.Sprintf("http://%s/", netnsAppEndpoint))
		if err != nil {
			s.T().Logf("Request %d failed: %v", i+1, err)
			continue
		}
		_ = resp.Body.Close()
		requestCount++
		time.Sleep(100 * time.Millisecond)
	}
	s.Require().Greater(requestCount, 0, "at least one HTTP request to netns-app must succeed")
	s.T().Logf("Generated %d HTTP requests", requestCount)

	s.T().Log("Waiting for Beyla to capture and process eBPF metrics...")
	time.Sleep(3 * time.Second)

	ebpfResp, err := helpers.QueryAgentEbpfMetrics(s.ctx, agentClient, []string{"netns-app"})
	s.Require().NoError(err, "Failed to query eBPF metrics from agent")

	s.T().Logf("Agent returned %d total eBPF metrics for netns-app", ebpfResp.TotalMetrics)
	s.Require().Greater(len(ebpfResp.HttpMetrics), 0,
		"expected Beyla to capture HTTP metrics via eBPF for a listener in a namespace "+
			"other than agent-0's own (RFD 112 acceptance criteria)")

	s.T().Log("✓ Beyla captured HTTP traffic for a process in a separate network namespace")
}
