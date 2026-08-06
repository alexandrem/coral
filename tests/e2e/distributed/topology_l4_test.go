package distributed

import (
	"encoding/json"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// L4TopologySuite tests RFD 033 — L4 network topology discovery.
//
// The suite injects synthetic connection data via the ReportConnections RPC and
// then validates that:
//   - L4 edges appear in `coral query topology` output with a LAYER=L4 column.
//   - `--include-l4=false` suppresses L4-only edges from the output.
//   - JSON topology output includes the `layer` field per connection.
//   - An L4 edge whose destination IP matches a registered agent is resolved to
//     an internal edge (LAYER=L4, target=agent ID instead of raw IP).
type L4TopologySuite struct {
	E2EDistributedSuite

	colonyClient colonyv1connect.ColonyServiceClient
	cliEnv       *helpers.CLITestEnv

	// Unique source agent ID used across all tests — not a real agent, so it
	// never produces L7 traces, keeping all injected edges L4-only.
	testAgentID string

	// External destination IP (TEST-NET-3, RFC 5737) — guaranteed never to
	// match a registered agent's mesh IP.
	testDestIP string
}

func (s *L4TopologySuite) SetupSuite() {
	s.E2EDistributedSuite.SetupSuite()

	s.testAgentID = "e2e-l4-test-agent"
	s.testDestIP = "203.0.113.42"

	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	s.colonyClient = helpers.NewColonyClient(colonyEndpoint)

	s.cliEnv, err = helpers.SetupCLIEnv(s.ctx, s.fixture.ColonyID, colonyEndpoint)
	s.Require().NoError(err, "Failed to setup CLI environment")

	s.T().Logf("L4TopologySuite ready: colony=%s testAgent=%s destIP=%s",
		colonyEndpoint, s.testAgentID, s.testDestIP)
}

func (s *L4TopologySuite) TearDownSuite() {
	if s.cliEnv != nil {
		_ = s.cliEnv.Cleanup()
	}
	s.E2EDistributedSuite.TearDownSuite()
}

// reportL4Connections sends a batch of L4 connection entries to the colony via
// the ReportConnections client-streaming RPC and waits for the stream to close.
// The data is committed to the DB before CloseAndReceive returns, so the
// subsequent GetTopology call will include it without further polling.
func (s *L4TopologySuite) reportL4Connections(agentID string, entries []*colonyv1.L4ConnectionEntry) {
	s.T().Helper()
	stream := s.colonyClient.ReportConnections(s.ctx)
	err := stream.Send(&colonyv1.ReportConnectionsRequest{
		AgentId:     agentID,
		Connections: entries,
	})
	s.Require().NoError(err, "Failed to send ReportConnections batch")
	_, err = stream.CloseAndReceive()
	s.Require().NoError(err, "Failed to close ReportConnections stream")
}

// testEntry returns a single L4ConnectionEntry for the suite's test agent and
// destination, using the current time as last_observed.
func (s *L4TopologySuite) testEntry() *colonyv1.L4ConnectionEntry {
	return &colonyv1.L4ConnectionEntry{
		RemoteIp:      s.testDestIP,
		RemotePort:    443,
		Protocol:      "tcp",
		BytesSent:     2048,
		BytesReceived: 512,
		LastObserved:  timestamppb.Now(),
	}
}

// TestL4EdgesAppearInTopology verifies that L4 connections reported via
// ReportConnections appear in 'coral query topology' with a LAYER column
// showing "L4" (RFD 033).
func (s *L4TopologySuite) TestL4EdgesAppearInTopology() {
	s.T().Log("Testing L4 edges appear in coral query topology (RFD 033)...")

	s.reportL4Connections(s.testAgentID, []*colonyv1.L4ConnectionEntry{s.testEntry()})

	// Poll topology output until the injected L4 edge appears.  The RPC
	// commits the data synchronously so this normally succeeds on the first
	// attempt; the short timeout guards against transient process delays.
	const (
		l4Timeout  = 30 * time.Second
		l4Interval = 2 * time.Second
	)
	var result *helpers.CLIResult
	err := helpers.WaitForCondition(s.ctx, func() bool {
		result = s.cliEnv.Run(s.ctx, "query", "topology")
		return result.Err == nil && strings.Contains(result.Output, s.testAgentID)
	}, l4Timeout, l4Interval)

	if err != nil {
		if result != nil {
			s.T().Logf("coral query topology last output:\n%s", result.Output)
		}
		s.Require().Fail("timed out waiting for L4 edge in topology output")
		return
	}

	result.MustSucceed(s.T())
	s.T().Logf("coral query topology output:\n%s", result.Output)

	s.Require().Contains(result.Output, "LAYER",
		"Output should include LAYER column header (RFD 033)")
	s.Require().Contains(result.Output, s.testAgentID,
		"Output should name the reporting agent")
	s.Require().Contains(result.Output, s.testDestIP,
		"Output should include the destination IP for external edges")
	s.Require().Contains(result.Output, "L4",
		"Output should show L4 evidence layer for network-observed edges")

	s.T().Log("✓ L4 edges appear in coral query topology with LAYER column")
}

// TestIncludeL4FalseFiltersL4Edges verifies that --include-l4=false suppresses
// L4-only edges while leaving L7 edges intact (RFD 033).
func (s *L4TopologySuite) TestIncludeL4FalseFiltersL4Edges() {
	s.T().Log("Testing --include-l4=false filters L4-only edges (RFD 033)...")

	// Ensure the L4 edge is present (idempotent upsert).
	s.reportL4Connections(s.testAgentID, []*colonyv1.L4ConnectionEntry{s.testEntry()})

	// Default (--include-l4=true): the test agent edge should appear.
	defaultResult := s.cliEnv.Run(s.ctx, "query", "topology")
	defaultResult.MustSucceed(s.T())
	s.Require().Contains(defaultResult.Output, s.testAgentID,
		"Default output should include the L4 test edge")

	// With --include-l4=false: the L4-only test agent edge should be absent.
	noL4Result := s.cliEnv.Run(s.ctx, "query", "topology", "--include-l4=false")
	noL4Result.MustSucceed(s.T())
	s.T().Logf("topology --include-l4=false output:\n%s", noL4Result.Output)

	s.Require().NotContains(noL4Result.Output, s.testAgentID,
		"--include-l4=false should suppress L4-only edges from the output")

	s.T().Log("✓ --include-l4=false correctly suppresses L4-only edges")
}

// TestL4JSONLayerField verifies that JSON topology output includes a 'layer'
// field on each connection entry (RFD 033).
func (s *L4TopologySuite) TestL4JSONLayerField() {
	s.T().Log("Testing coral query topology --format json includes layer field (RFD 033)...")

	// Ensure the L4 edge is present (idempotent upsert).
	s.reportL4Connections(s.testAgentID, []*colonyv1.L4ConnectionEntry{s.testEntry()})

	result := s.cliEnv.Run(s.ctx, "query", "topology", "--format", "json")
	result.MustSucceed(s.T())
	s.T().Logf("JSON output:\n%s", result.Output)

	var parsed map[string]interface{}
	s.Require().NoError(json.Unmarshal([]byte(result.Output), &parsed),
		"JSON output must be valid JSON")

	s.Require().Contains(parsed, "connections", "JSON must include 'connections' field")
	conns, ok := parsed["connections"].([]interface{})
	s.Require().True(ok, "'connections' must be a JSON array")

	// Find the injected L4 connection and verify its layer value.
	found := false
	for _, c := range conns {
		conn, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if conn["from"] == s.testAgentID {
			s.Require().Contains(conn, "layer",
				"Every connection in JSON output must have a 'layer' field (RFD 033)")
			s.Require().Equal("L4", conn["layer"],
				"L4-only connection must have layer=L4 in JSON output")
			found = true
			break
		}
	}
	s.Require().True(found, "Injected L4 test connection should appear in JSON output")

	s.T().Log("✓ JSON topology output includes layer=L4 for network-observed edges")
}

// TestL4InternalEdgeResolution verifies that when a reported remote IP matches a
// registered agent's mesh IP, the topology edge uses the agent ID as the target
// rather than the raw IP address (RFD 033 — IP → agent identity correlation).
func (s *L4TopologySuite) TestL4InternalEdgeResolution() {
	s.T().Log("Testing L4 internal edge IP→agent resolution (RFD 033)...")

	// Retrieve the list of registered agents to find a mesh IP.
	agentsResp, err := helpers.ListAgents(s.ctx, s.colonyClient)
	s.Require().NoError(err, "Failed to list agents")
	s.Require().NotEmpty(agentsResp.Agents, "At least one agent must be registered")

	// Pick any agent that has a mesh IPv4 address.
	var targetAgent *colonyv1.Agent
	for _, a := range agentsResp.Agents {
		if a.MeshIpv4 != "" {
			targetAgent = a
			break
		}
	}
	s.Require().NotNil(targetAgent, "At least one agent must have a mesh IPv4 address")

	s.T().Logf("Target agent: id=%s meshIP=%s", targetAgent.AgentId, targetAgent.MeshIpv4)

	// Report an L4 edge from the test agent to the target agent's mesh IP.
	internalEntry := &colonyv1.L4ConnectionEntry{
		RemoteIp:     targetAgent.MeshIpv4,
		RemotePort:   9000,
		Protocol:     "tcp",
		LastObserved: timestamppb.Now(),
	}
	s.reportL4Connections(s.testAgentID, []*colonyv1.L4ConnectionEntry{internalEntry})

	// Poll until the internal edge appears in topology output.
	const (
		resolveTimeout  = 30 * time.Second
		resolveInterval = 2 * time.Second
	)
	var result *helpers.CLIResult
	errWait := helpers.WaitForCondition(s.ctx, func() bool {
		result = s.cliEnv.Run(s.ctx, "query", "topology", "--format", "json")
		if result.Err != nil {
			return false
		}
		return strings.Contains(result.Output, targetAgent.AgentId)
	}, resolveTimeout, resolveInterval)

	if errWait != nil {
		if result != nil {
			s.T().Logf("coral query topology --format json last output:\n%s", result.Output)
		}
		s.Require().Fail("timed out waiting for internal L4 edge with resolved agent ID")
		return
	}

	// Parse JSON and find the internal edge.
	var parsed map[string]interface{}
	s.Require().NoError(json.Unmarshal([]byte(result.Output), &parsed))

	conns, ok := parsed["connections"].([]interface{})
	s.Require().True(ok)

	found := false
	for _, c := range conns {
		conn, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if conn["from"] == s.testAgentID && conn["to"] == targetAgent.AgentId {
			s.Require().Equal("L4", conn["layer"],
				"Internal L4 edge must have layer=L4")
			found = true
			break
		}
	}
	s.Require().True(found,
		"Internal L4 edge (test-agent→%s) should appear with agent ID as target, not raw IP",
		targetAgent.AgentId)

	s.T().Log("✓ L4 internal edge resolved: dest IP → agent ID in topology output")
}
