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
	meshPassed           bool
	servicesPassed       bool
	passiveObservability bool
	onDemandProbesPassed bool
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

	// Run MeshSuite tests.
	meshSuite := &MeshSuite{}
	meshSuite.ctx = s.ctx
	meshSuite.SetT(s.T())

	// Run individual tests in order.
	s.Run("DiscoveryService", meshSuite.TestDiscoveryServiceAvailability)
	s.Run("ColonyRegistration", meshSuite.TestColonyRegistration)
	s.Run("ColonyStatus", meshSuite.TestColonyStatus)
	s.Run("AgentRegistration", meshSuite.TestAgentRegistration)
	s.Run("MultiAgentMesh", meshSuite.TestMultiAgentMesh)
	s.Run("Heartbeat", meshSuite.TestHeartbeat)
	s.Run("AgentReconnection", meshSuite.TestAgentReconnection)

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

	// Run ServiceSuite tests.
	serviceSuite := &ServiceSuite{}
	serviceSuite.ctx = s.ctx
	serviceSuite.SetT(s.T())

	s.Run("ServiceRegistrationAndDiscovery", serviceSuite.TestServiceRegistrationAndDiscovery)
	s.Run("DynamicServiceConnection", serviceSuite.TestDynamicServiceConnection)
	s.Run("ServiceConnectionAtStartup", serviceSuite.TestServiceConnectionAtStartup)
	s.Run("MultiServiceRegistration", serviceSuite.TestMultiServiceRegistration)

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

	// Run TelemetrySuite tests (Beyla, OTLP, system metrics).
	telemetrySuite := &TelemetrySuite{}
	telemetrySuite.ctx = s.ctx
	telemetrySuite.SetT(s.T())

	// Beyla tests.
	s.Run("BeylaPassiveInstrumentation", telemetrySuite.TestBeylaPassiveInstrumentation)
	s.Run("BeylaColonyPolling", telemetrySuite.TestBeylaColonyPolling)
	s.Run("BeylaVsOTLPComparison", telemetrySuite.TestBeylaVsOTLPComparison)

	// OTLP tests.
	s.Run("OTLPIngestion", telemetrySuite.TestOTLPIngestion)
	s.Run("OTLPAppEndpoints", telemetrySuite.TestOTLPAppEndpoints)
	s.Run("TelemetryAggregation", telemetrySuite.TestTelemetryAggregation)

	// System metrics tests.
	s.Run("SystemMetricsCollection", telemetrySuite.TestSystemMetricsCollection)
	s.Run("SystemMetricsPolling", telemetrySuite.TestSystemMetricsPolling)

	// Run ProfilingSuite tests (continuous profiling).
	profilingSuite := &ProfilingSuite{}
	profilingSuite.ctx = s.ctx
	profilingSuite.SetT(s.T())

	s.Run("ContinuousProfiling", profilingSuite.TestContinuousProfiling)

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

	// Run ProfilingSuite tests (on-demand profiling).
	profilingSuite := &ProfilingSuite{}
	profilingSuite.ctx = s.ctx
	profilingSuite.SetT(s.T())

	s.Run("OnDemandProfiling", profilingSuite.TestOnDemandProfiling)

	// Run DebugSuite tests (uprobe tracing, debug sessions).
	debugSuite := &DebugSuite{}
	debugSuite.ctx = s.ctx
	debugSuite.SetT(s.T())

	s.Run("UprobeTracing", debugSuite.TestUprobeTracing)
	s.Run("UprobeCallTree", debugSuite.TestUprobeCallTree)
	s.Run("MultiAgentDebugSession", debugSuite.TestMultiAgentDebugSession)

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
