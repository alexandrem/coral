# E2E Test Refactoring Map

## Goal
Refactor from **level-based organization** (L0, L1, L2, L3) to **behavior-based organization** (mesh, service, telemetry, profiling, debug).

## Current Structure (Level-Based)

### connectivity_test.go (E2EDistributedSuite)
- `TestDiscoveryServiceAvailability` → **mesh_test.go**
- `TestColonyRegistrationWithDiscovery` → **mesh_test.go**
- `TestColonyStatus` → **mesh_test.go**
- `TestAgentRegistration` → **mesh_test.go**
- `TestMultiAgentMeshAllocation` → **mesh_test.go**
- `TestHeartbeatMechanism` → **mesh_test.go**
- `TestServiceRegistration` → **service_test.go** (merge with existing placeholder)
- `TestAgentReconnectionAfterColonyRestart` → **mesh_test.go**

### beyla_test.go (BeylaSuite)
- `TestBeylaAutoInstrumentation` → **telemetry_test.go** (merge with Level0 tests)
- `TestBeylaColonyPolling` → **telemetry_test.go** (merge with Level0 tests)
- `TestBeylaVsOTLP` → **telemetry_test.go** (merge with Level0 tests)

### observability_l0_test.go (ObservabilityL0Suite)
- `TestLevel0_BeylaHTTPMetrics` → **telemetry_test.go** (rename to TestBeylaPassiveInstrumentation)
- `TestLevel0_BeylaColonyPolling` → **telemetry_test.go** (rename to TestBeylaColonyPolling)
- `TestLevel0_BeylaVsOTLP` → **telemetry_test.go** (rename to TestBeylaVsOTLPComparison)

### observability_l1_test.go (ObservabilityL1Suite)
- `TestLevel1_OTLPIngestion` → **telemetry_test.go** (rename to TestOTLPIngestion)
- `TestLevel1_OTELAppEndpoints` → **telemetry_test.go** (rename to TestOTLPAppEndpoints)
- `TestLevel1_ColonyAggregation` → **telemetry_test.go** (rename to TestTelemetryAggregation)

### observability_l2_test.go (ObservabilityL2Suite)
- `TestLevel2_SystemMetricsCollection` → **telemetry_test.go** (rename to TestSystemMetricsCollection)
- `TestLevel2_SystemMetricsPolling` → **telemetry_test.go** (rename to TestSystemMetricsPolling)
- `TestLevel2_ContinuousCPUProfiling` → **profiling_test.go** (rename to TestContinuousProfiling)

### observability_l3_test.go (ObservabilityL3Suite)
- `TestLevel3_OnDemandCPUProfiling` → **profiling_test.go** (rename to TestOnDemandProfiling)
- `TestLevel3_UprobeTracing` → **debug_test.go** (rename to TestUprobeTracing)
- `TestLevel3_UprobeCallTree` → **debug_test.go** (rename to TestUprobeCallTree)
- `TestLevel3_MultiAgentDebugSession` → **debug_test.go** (rename to TestMultiAgentDebugSession)

### service_test.go (ServiceSuite) - ALREADY EXISTS
- `TestServiceRegistrationAndDiscovery` → Keep (populate with implementation from connectivity_test.go)
- `TestDynamicServiceConnection` → Keep (already placeholder)
- `TestMultiServiceRegistration` → Keep (already placeholder)
- `TestServiceConnectionAtStartup` → Keep (already placeholder)

## New Structure (Behavior-Based)

### mesh_test.go (NEW - MeshSuite)
**Purpose**: Tests WireGuard mesh connectivity, agent registration, heartbeat, and reconnection.

Tests:
- `TestDiscoveryServiceAvailability` - Discovery service health checks
- `TestColonyRegistration` - Colony registers with discovery
- `TestColonyStatus` - Colony status API validation
- `TestAgentRegistration` - Agent registration flow
- `TestMultiAgentMesh` - Multiple agents with unique mesh IPs
- `TestHeartbeat` - Heartbeat mechanism
- `TestAgentReconnection` - Reconnection after colony restart

### service_test.go (EXISTS - ServiceSuite)
**Purpose**: Tests service registration, connection, and discovery.

Tests:
- `TestServiceRegistrationAndDiscovery` - Service registry and discovery (from connectivity_test.go:TestServiceRegistration)
- `TestDynamicServiceConnection` - Dynamic service connection via API (placeholder)
- `TestMultiServiceRegistration` - Multiple services per agent (placeholder)
- `TestServiceConnectionAtStartup` - Service connection at startup (placeholder)

### telemetry_test.go (NEW - TelemetrySuite)
**Purpose**: Tests passive (Beyla) and active (OTLP) telemetry collection, plus system metrics.

Tests:
- `TestBeylaPassiveInstrumentation` - Passive eBPF HTTP metrics via Beyla
- `TestBeylaColonyPolling` - Colony polls agent for eBPF metrics
- `TestBeylaVsOTLPComparison` - Compare passive vs active instrumentation
- `TestOTLPIngestion` - OTLP trace ingestion from app to agent
- `TestOTLPAppEndpoints` - OTLP test app functionality
- `TestTelemetryAggregation` - Agent → colony polling with P50/P95/P99
- `TestSystemMetricsCollection` - CPU/memory/disk/network metrics
- `TestSystemMetricsPolling` - Colony polls agent for system metrics

### profiling_test.go (NEW - ProfilingSuite)
**Purpose**: Tests continuous and on-demand CPU profiling.

Tests:
- `TestContinuousProfiling` - Always-on 19Hz CPU profiling
- `TestOnDemandProfiling` - On-demand 99Hz profiling (placeholder/skip for now)

### debug_test.go (NEW - DebugSuite)
**Purpose**: Tests deep introspection via uprobe tracing and debug sessions.

Tests:
- `TestUprobeTracing` - Uprobe-based function tracing (placeholder/skip)
- `TestUprobeCallTree` - Call tree construction (placeholder/skip)
- `TestMultiAgentDebugSession` - Multi-agent debug sessions (placeholder/skip)

## Orchestrator Integration

The `e2e_orchestrator_test.go` should be updated to:
1. Call the behavior-based test suites in dependency order
2. Use suite composition to run sub-tests
3. Maintain fail-fast behavior with dependency tracking

Structure:
```go
Test1_MeshConnectivity() {
    // Run MeshSuite tests
    suite.Run(s.T(), new(MeshSuite))
}

Test2_ServiceManagement() {
    if !s.meshPassed { skip }
    // Run ServiceSuite tests
    suite.Run(s.T(), new(ServiceSuite))
}

Test3_PassiveObservability() {
    if !s.meshPassed || !s.servicesPassed { skip }
    // Run TelemetrySuite tests
    suite.Run(s.T(), new(TelemetrySuite))
}

Test4_OnDemandProbes() {
    if prerequisites failed { skip }
    // Run ProfilingSuite + DebugSuite tests
    suite.Run(s.T(), new(ProfilingSuite))
    suite.Run(s.T(), new(DebugSuite))
}
```

## Migration Steps

1. ✅ Create this refactoring map
2. ⏳ Create new behavior-based test files (mesh_test.go, telemetry_test.go, profiling_test.go, debug_test.go)
3. ⏳ Copy test implementations from old files to new files (with renames)
4. ⏳ Update service_test.go placeholders with real implementations
5. ⏳ Update e2e_orchestrator_test.go to call behavior-based suites
6. ⏳ Verify tests compile and run
7. ⏳ Delete old level-based files (connectivity_test.go, beyla_test.go, observability_l*.go)
8. ⏳ Update README.md and PLAN.md with new structure
9. ⏳ Final test run to verify all passes

## Files to Delete After Migration
- `connectivity_test.go`
- `beyla_test.go`
- `observability_l0_test.go`
- `observability_l1_test.go`
- `observability_l2_test.go`
- `observability_l3_test.go`

## Files to Keep
- `suite.go` - Base suite infrastructure
- `e2e_orchestrator_test.go` - Orchestrator (update with real implementations)
- `service_test.go` - Service behavior tests (update with real implementations)
- `fixtures/` - Container fixtures and test apps
- `helpers/` - Helper functions

## Files to Create
- `mesh_test.go` - Mesh connectivity tests
- `telemetry_test.go` - Telemetry collection tests
- `profiling_test.go` - Profiling tests
- `debug_test.go` - Debug session and uprobe tests
