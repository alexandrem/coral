package distributed

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

// DebugSuite tests deep introspection capabilities (uprobe tracing, debug sessions).
//
// This suite covers Level 3 observability features:
// - Uprobe-based function tracing with entry/exit events
// - Call tree construction from uprobe events
// - Multi-agent debug session coordination
type DebugSuite struct {
	E2EDistributedSuite
}

// TestDebugSuite runs the debug introspection test suite.
func TestDebugSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping debug tests in short mode")
	}

	suite.Run(t, new(DebugSuite))
}

// TestUprobeTracing verifies uprobe-based function tracing.
//
// Test flow:
// 1. Start colony, agent, and SDK test app
// 2. Connect SDK app to agent
// 3. Attach uprobe to ProcessPayment function
// 4. Trigger workload via /trigger endpoint
// 5. Verify uprobe events captured (entry/exit, duration)
// 6. Detach uprobe and verify event retrieval
//
// Note: Uses SDK test app with known functions for testing.
func (s *DebugSuite) TestUprobeTracing() {
	s.T().Skip("SKIPPED: Uprobe tracing requires debug session API and SDK integration (Level 3 feature)")
	// Test implementation will be added when AttachUprobe, QueryUprobeEvents, and DetachUprobe APIs are implemented.
}

// TestUprobeCallTree verifies uprobe call tree construction.
//
// This test validates that uprobes can track call chains and build call trees
// showing parent-child relationships, call depth, and execution time.
func (s *DebugSuite) TestUprobeCallTree() {
	s.T().Skip("SKIPPED: Call tree construction requires multi-function uprobe support (Level 3 feature)")
	// Test implementation will be added when GetDebugResults API is implemented.
}

// TestMultiAgentDebugSession verifies debug sessions across multiple agents.
//
// Test flow:
// 1. Start colony with multiple agents and CPU apps
// 2. Connect services to each agent
// 3. Start CPU profiling on multiple agents
// 4. Verify profiling works independently on each agent
// 5. Verify colony can collect results from all agents
func (s *DebugSuite) TestMultiAgentDebugSession() {
	s.T().Skip("SKIPPED: Multi-agent debug sessions require debug session API and coordination (Level 3 feature)")
	// Test implementation will be added when ProfileCPU API is implemented.
}
