package distributed

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// E2EOrchestratorSuite orchestrates all E2E tests in dependency order.
// Tests fail-fast: if a dependency group fails, subsequent groups are skipped.
//
// Dependency Order:
//  1. Mesh Connectivity (foundation)
//  2. Service Management (depends on mesh)
//  3. Passive Observability (depends on mesh + services)
//  4. On-Demand Probes (depends on all above)
//  5. CLI Commands (tests user-facing CLI)
type E2EOrchestratorSuite struct {
	E2EDistributedSuite

	// Track which test groups have passed.
	meshPassed           bool
	servicesPassed       bool
	passiveObservability bool
	onDemandProbesPassed bool
	cliCommandsPassed    bool
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
	s.T().Log("==================================================")
	s.T().Log("E2E Test Orchestrator - Dependency-Ordered Tests")
	s.T().Log("==================================================")

	// Call parent SetupSuite to initialize fixture.
	s.E2EDistributedSuite.SetupSuite()
}

// TearDownSuite runs once after all tests.
func (s *E2EOrchestratorSuite) TearDownSuite() {
	// Call parent TearDownSuite to clean up fixture.
	s.E2EDistributedSuite.TearDownSuite()

	s.T().Log("")
	s.T().Log("==================================================")
	s.T().Log("E2E Test Results Summary")
	s.T().Log("==================================================")
	s.T().Logf("  1. Mesh Connectivity:        %s", status(s.meshPassed))
	s.T().Logf("  2. Service Management:       %s", status(s.servicesPassed))
	s.T().Logf("  3. Passive Observability:    %s", status(s.passiveObservability))
	s.T().Logf("  4. On-Demand Probes:         %s", status(s.onDemandProbesPassed))
	s.T().Logf("  5. CLI Commands:             %s", status(s.cliCommandsPassed))
	s.T().Log("==================================================")
}

// Test1_MeshConnectivity runs foundational mesh tests.
// These MUST pass for any other tests to run.
func (s *E2EOrchestratorSuite) Test1_MeshConnectivity() {
	s.T().Log("")
	s.T().Log("========================================")
	s.T().Log("GROUP 1: Mesh Connectivity (Foundation)")
	s.T().Log("========================================")

	// Run MeshSuite tests with shared fixture.
	meshSuite := &MeshSuite{
		E2EDistributedSuite: s.E2EDistributedSuite,
	}
	meshSuite.SetT(s.T())
	defer meshSuite.TearDownTest()

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

	// Run ServiceSuite tests with shared fixture.
	serviceSuite := &ServiceSuite{
		E2EDistributedSuite: s.E2EDistributedSuite,
	}
	serviceSuite.SetT(s.T())

	s.Run("ServiceRegistrationAndDiscovery", func() {
		serviceSuite.TestServiceRegistrationAndDiscovery()
		serviceSuite.TearDownTest() // Clean up services after test
	})
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

	// Run TelemetrySuite tests (Beyla, OTLP, system metrics) with shared fixture.
	telemetrySuite := &TelemetrySuite{
		E2EDistributedSuite: s.E2EDistributedSuite,
	}
	telemetrySuite.SetT(s.T())

	// Beyla tests.
	s.Run("BeylaPassiveInstrumentation", func() {
		telemetrySuite.TestBeylaPassiveInstrumentation()
		telemetrySuite.TearDownTest() // Clean up services after test
	})
	s.Run("BeylaColonyPolling", func() {
		telemetrySuite.TestBeylaColonyPolling()
		telemetrySuite.TearDownTest() // Clean up services after test
	})
	s.Run("BeylaVsOTLPComparison", func() {
		telemetrySuite.TestBeylaVsOTLPComparison()
		telemetrySuite.TearDownTest() // Clean up services after test
	})

	// OTLP tests.
	s.Run("OTLPIngestion", telemetrySuite.TestOTLPIngestion)
	s.Run("OTLPMetricsIngestion", telemetrySuite.TestOTLPMetricsIngestion)
	s.Run("OTLPAppEndpoints", telemetrySuite.TestOTLPAppEndpoints)
	s.Run("TelemetryAggregation", telemetrySuite.TestTelemetryAggregation)

	// System metrics tests.
	s.Run("SystemMetricsCollection", telemetrySuite.TestSystemMetricsCollection)
	s.Run("SystemMetricsPolling", telemetrySuite.TestSystemMetricsPolling)

	// Run ProfilingSuite tests (continuous profiling) with shared fixture.
	profilingSuite := &ProfilingSuite{
		E2EDistributedSuite: s.E2EDistributedSuite,
	}
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

	// Clean up all services from previous phases to prevent "already connected" errors.
	s.T().Log("Cleaning up services from previous test phases...")
	_ = helpers.CleanupAllServices(s.ctx, s.fixture.GetAgentGRPCEndpoint)
	s.T().Log("  ✓ All services disconnected from all agents")

	// Run ProfilingSuite tests (on-demand profiling) with shared fixture.
	profilingSuite := &ProfilingSuite{
		E2EDistributedSuite: s.E2EDistributedSuite,
	}
	profilingSuite.SetT(s.T())

	s.Run("OnDemandProfiling", profilingSuite.TestOnDemandProfiling)

	// Run DebugSuite tests (uprobe tracing, debug sessions) with shared fixture.
	debugSuite := &DebugSuite{
		E2EDistributedSuite: s.E2EDistributedSuite,
	}
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

// Test5_CLICommands runs CLI command tests.
// Requires: Mesh Connectivity + Service Management + Passive Observability
//
// This test group validates user-facing CLI commands for:
// - Colony status and agent management (Phase 1)
// - Query commands (Phase 2)
// - Config commands (Phase 3)
func (s *E2EOrchestratorSuite) Test5_CLICommands() {
	// if !s.meshPassed || !s.servicesPassed || !s.passiveObservability {
	// 	s.T().Skip("Skipping: Prerequisites failed (mesh, services, or observability)")
	// }

	s.T().Log("")
	s.T().Log("========================================")
	s.T().Log("GROUP 5: CLI Commands")
	s.T().Log("========================================")

	// Run CLIMeshSuite (colony and agent commands - Phase 1)
	cliMeshSuite := &CLIMeshSuite{
		E2EDistributedSuite: s.E2EDistributedSuite,
	}
	cliMeshSuite.SetT(s.T())
	cliMeshSuite.SetupSuite() // Initialize cliEnv
	defer cliMeshSuite.TearDownSuite()

	s.Run("CLI_ColonyStatus", cliMeshSuite.TestColonyStatusCommand)
	s.Run("CLI_ColonyAgents", cliMeshSuite.TestColonyAgentsCommand)
	s.Run("CLI_AgentList", cliMeshSuite.TestAgentListCommand)
	s.Run("CLI_ServiceList", cliMeshSuite.TestServiceListCommand)
	// Skip CLI_ErrorHandling - we don't have a colony endpoint env var yet
	// s.Run("CLI_ErrorHandling", cliMeshSuite.TestInvalidColonyEndpoint)
	s.Run("CLI_TableFormatting", cliMeshSuite.TestTableOutputFormatting)
	s.Run("CLI_JSONValidity", cliMeshSuite.TestJSONOutputValidity)

	// Run CLIQuerySuite (query commands - Phase 2)
	cliQuerySuite := &CLIQuerySuite{
		E2EDistributedSuite: s.E2EDistributedSuite,
	}
	cliQuerySuite.SetT(s.T())
	cliQuerySuite.SetupSuite() // Initialize cliEnv
	defer cliQuerySuite.TearDownSuite()

	s.Run("CLI_QuerySummary", cliQuerySuite.TestQuerySummaryCommand)
	s.Run("CLI_QueryServices", cliQuerySuite.TestQueryServicesCommand)
	s.Run("CLI_QueryTraces", cliQuerySuite.TestQueryTracesCommand)
	s.Run("CLI_QueryMetrics", cliQuerySuite.TestQueryMetricsCommand)
	s.Run("CLI_QueryFlagCombinations", cliQuerySuite.TestQueryFlagCombinations)
	s.Run("CLI_QueryInvalidFlags", cliQuerySuite.TestQueryInvalidFlags)
	s.Run("CLI_QueryJSONValidity", cliQuerySuite.TestQueryJSONOutputValidity)
	s.Run("CLI_QueryTableFormatting", cliQuerySuite.TestQueryTableOutputFormatting)

	// Run CLIConfigSuite (config commands - Phase 3)
	cliConfigSuite := &CLIConfigSuite{
		E2EDistributedSuite: s.E2EDistributedSuite,
	}
	cliConfigSuite.SetT(s.T())
	cliConfigSuite.SetupSuite() // Initialize cliEnv
	defer cliConfigSuite.TearDownSuite()

	s.Run("CLI_ConfigGetContexts", cliConfigSuite.TestConfigGetContextsCommand)
	s.Run("CLI_ConfigCurrentContext", cliConfigSuite.TestConfigCurrentContextCommand)
	s.Run("CLI_ConfigUseContext", cliConfigSuite.TestConfigUseContextCommand)
	s.Run("CLI_ConfigInvalidContext", cliConfigSuite.TestConfigInvalidContext)
	s.Run("CLI_ConfigOutputFormats", cliConfigSuite.TestConfigOutputFormats)
	s.Run("CLI_ConfigWithoutColony", cliConfigSuite.TestConfigCommandsWithoutColony)
	s.Run("CLI_ConfigHelpText", cliConfigSuite.TestConfigHelpText)

	if !s.T().Failed() {
		s.cliCommandsPassed = true
		s.T().Log("✓ GROUP 5 PASSED - CLI commands working")
	} else {
		s.T().Log("✗ GROUP 5 FAILED")
	}
}

// Helper function for status display.
func status(passed bool) string {
	if passed {
		return "✓ PASSED"
	}
	return "✗ FAILED"
}
