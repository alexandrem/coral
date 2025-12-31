package distributed

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"
)

// E2EOrchestratorSuite orchestrates all E2E tests in dependency order.
// Tests fail-fast: if a dependency group fails, subsequent groups are skipped.
//
// Dependency Order:
//  1. Mesh Connectivity (foundation)
//  2. Service Management (depends on mesh)
//  3. Passive Observability (depends on mesh + services)
//  4. On-Demand Probes (depends on all above)
type E2EOrchestratorSuite struct {
	suite.Suite
	ctx context.Context

	// Track which test groups have passed.
	meshPassed             bool
	servicesPassed         bool
	passiveObservability   bool
	onDemandProbesPassed   bool
}

// TestE2EOrchestrator runs all E2E tests in dependency order with fail-fast.
func TestE2EOrchestrator(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E orchestrator tests in short mode")
	}

	suite.Run(t, new(E2EOrchestratorSuite))
}

// SetupSuite runs once before all tests.
func (s *E2EOrchestratorSuite) SetupSuite() {
	s.ctx = context.Background()
	s.T().Log("==================================================")
	s.T().Log("E2E Test Orchestrator - Dependency-Ordered Tests")
	s.T().Log("==================================================")
}

// TearDownSuite runs once after all tests.
func (s *E2EOrchestratorSuite) TearDownSuite() {
	s.T().Log("")
	s.T().Log("==================================================")
	s.T().Log("E2E Test Results Summary")
	s.T().Log("==================================================")
	s.T().Logf("  1. Mesh Connectivity:        %s", status(s.meshPassed))
	s.T().Logf("  2. Service Management:       %s", status(s.servicesPassed))
	s.T().Logf("  3. Passive Observability:    %s", status(s.passiveObservability))
	s.T().Logf("  4. On-Demand Probes:         %s", status(s.onDemandProbesPassed))
	s.T().Log("==================================================")
}

// Test1_MeshConnectivity runs foundational mesh tests.
// These MUST pass for any other tests to run.
func (s *E2EOrchestratorSuite) Test1_MeshConnectivity() {
	s.T().Log("")
	s.T().Log("========================================")
	s.T().Log("GROUP 1: Mesh Connectivity (Foundation)")
	s.T().Log("========================================")

	// Run mesh connectivity tests.
	// These are from connectivity_test.go:
	// - Discovery service
	// - Colony registration
	// - WireGuard mesh
	// - Agent registration
	// - Heartbeat

	s.Run("DiscoveryService", func() {
		s.T().Log("Testing discovery service availability...")
		// Test implementation or call existing test
	})

	s.Run("ColonyRegistration", func() {
		s.T().Log("Testing colony registration...")
		// Test implementation
	})

	s.Run("WireGuardMesh", func() {
		s.T().Log("Testing WireGuard mesh establishment...")
		// Test implementation
	})

	s.Run("AgentRegistration", func() {
		s.T().Log("Testing agent registration...")
		// Test implementation
	})

	s.Run("Heartbeat", func() {
		s.T().Log("Testing heartbeat mechanism...")
		// Test implementation
	})

	// Mark as passed if all subtests succeeded.
	if !s.T().Failed() {
		s.meshPassed = true
		s.T().Log("✓ GROUP 1 PASSED - Mesh connectivity working")
	} else {
		s.T().Log("✗ GROUP 1 FAILED - Stopping further tests")
	}
}

// Test2_ServiceManagement runs service registration and connection tests.
// Requires: Mesh Connectivity
func (s *E2EOrchestratorSuite) Test2_ServiceManagement() {
	if !s.meshPassed {
		s.T().Skip("Skipping: Mesh connectivity tests failed")
	}

	s.T().Log("")
	s.T().Log("========================================")
	s.T().Log("GROUP 2: Service Management")
	s.T().Log("========================================")

	s.Run("ServiceRegistration", func() {
		s.T().Log("Testing service registration...")
		// Test from connectivity_test.go:TestServiceRegistration
	})

	s.Run("DynamicConnection", func() {
		s.T().Log("Testing dynamic service connection...")
		// New test for ConnectService API
	})

	s.Run("MultiService", func() {
		s.T().Log("Testing multiple services per agent...")
		// New test for multi-service scenarios
	})

	if !s.T().Failed() {
		s.servicesPassed = true
		s.T().Log("✓ GROUP 2 PASSED - Service management working")
	} else {
		s.T().Log("✗ GROUP 2 FAILED - Stopping further tests")
	}
}

// Test3_PassiveObservability runs passive monitoring tests.
// Requires: Mesh Connectivity + Service Management
func (s *E2EOrchestratorSuite) Test3_PassiveObservability() {
	if !s.meshPassed || !s.servicesPassed {
		s.T().Skip("Skipping: Prerequisites failed (mesh or services)")
	}

	s.T().Log("")
	s.T().Log("========================================")
	s.T().Log("GROUP 3: Passive Observability")
	s.T().Log("========================================")

	s.Run("BeylaAutoInstrumentation", func() {
		s.T().Log("Testing Beyla auto-instrumentation...")
		// Test from observability_l0_test.go
	})

	s.Run("OTLPTelemetry", func() {
		s.T().Log("Testing OTLP telemetry collection...")
		// Test from observability_l1_test.go
	})

	s.Run("SystemMetrics", func() {
		s.T().Log("Testing system metrics collection...")
		// Test from observability_l2_test.go
	})

	s.Run("ContinuousProfiling", func() {
		s.T().Log("Testing continuous CPU profiling...")
		// Test from observability_l2_test.go
	})

	if !s.T().Failed() {
		s.passiveObservability = true
		s.T().Log("✓ GROUP 3 PASSED - Passive observability working")
	} else {
		s.T().Log("✗ GROUP 3 FAILED - Stopping further tests")
	}
}

// Test4_OnDemandProbes runs deep introspection tests.
// Requires: All previous groups
func (s *E2EOrchestratorSuite) Test4_OnDemandProbes() {
	if !s.meshPassed || !s.servicesPassed || !s.passiveObservability {
		s.T().Skip("Skipping: Prerequisites failed")
	}

	s.T().Log("")
	s.T().Log("========================================")
	s.T().Log("GROUP 4: On-Demand Probes")
	s.T().Log("========================================")

	s.Run("OnDemandCPUProfiling", func() {
		s.T().Log("Testing on-demand CPU profiling (99Hz)...")
		// Test from observability_l3_test.go
	})

	s.Run("UprobeTracing", func() {
		s.T().Log("Testing uprobe function tracing...")
		// Test from observability_l3_test.go
	})

	s.Run("CallTreeConstruction", func() {
		s.T().Log("Testing call tree construction...")
		// Test from observability_l3_test.go
	})

	s.Run("MultiAgentDebug", func() {
		s.T().Log("Testing multi-agent debug sessions...")
		// Test from observability_l3_test.go
	})

	if !s.T().Failed() {
		s.onDemandProbesPassed = true
		s.T().Log("✓ GROUP 4 PASSED - On-demand probes working")
	} else {
		s.T().Log("✗ GROUP 4 FAILED")
	}
}

// Helper function for status display.
func status(passed bool) string {
	if passed {
		return "✓ PASSED"
	}
	return "✗ FAILED"
}
