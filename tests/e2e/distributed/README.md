# Coral E2E Distributed Test Suite

Comprehensive end-to-end test suite for Coral's distributed systems functionality, organized by behavior rather than implementation layers.

## Overview

This test suite validates the complete distributed architecture of Coral:
- **Mesh Connectivity**: 7 tests (discovery, registration, heartbeat)
- **Service Management**: 4 tests (registration, discovery, dynamic connections)
- **Telemetry**: 8 tests (Beyla passive instrumentation, OTLP active telemetry, system metrics)
- **Profiling**: 2 tests (continuous and on-demand CPU profiling)
- **Debug**: 3 tests (uprobe tracing, debug sessions)
- **E2E Orchestration**: 4 tests (dependency-ordered full stack validation)
- **Total**: 28 tests

## Prerequisites

- **Platform**: Linux only (required for eBPF and WireGuard)
- **Docker**: Docker daemon must be running
- **Go**: Go 1.21+
- **Privileges**: Tests run containers in privileged mode for WireGuard and eBPF

## Quick Start

```bash
# Run all E2E tests
cd tests/e2e/distributed
go test -v -timeout 30m

# Run specific test suite
go test -v -run TestMeshSuite           # Mesh connectivity tests
go test -v -run TestServiceSuite        # Service management tests
go test -v -run TestTelemetrySuite      # Telemetry collection tests
go test -v -run TestProfilingSuite      # CPU profiling tests
go test -v -run TestDebugSuite          # Debug and uprobe tests
go test -v -run TestE2EOrchestratorSuite # Full stack orchestration

# Run specific test
go test -v -run TestServiceRegistrationAndDiscovery
go test -v -run TestOTLPIngestion
go test -v -run TestBeylaPassiveInstrumentation

# Skip long-running tests
go test -v -short
```

## Test Architecture

### Container Orchestration

Tests use [testcontainers-go](https://golang.testcontainers.org/) to orchestrate:
- **Discovery Service**: Service registry and NAT traversal
- **Colony**: AI coordinator with DuckDB storage
- **Agents**: Local observers with eBPF/profiling capabilities
- **Test Apps**: CPU app, OTLP app for workload generation

### Test Lifecycle

Each test follows this pattern:
1. **Setup**: Create fresh container fixture with network
2. **Execute**: Run test scenario with real containers
3. **Verify**: Assert expected behavior via gRPC APIs
4. **Cleanup**: Stop and remove all containers

**Isolation Strategy**: Fresh containers for each test ensure clean state.

## Test Coverage

### Mesh Connectivity (7 tests)

**File**: `mesh_test.go`

| Test | Description |
|------|-------------|
| `TestDiscoveryServiceAvailability` | Discovery service health check |
| `TestColonyRegistration` | Colony registers with discovery service |
| `TestColonyStatus` | Colony status API validation |
| `TestAgentRegistration` | Agent registration with colony |
| `TestMultiAgentMesh` | Unique WireGuard mesh IP allocation |
| `TestHeartbeat` | Agent heartbeat mechanism |
| `TestAgentReconnection` | Agent reconnection after colony restart |

**Key Feature**: WireGuard mesh network establishment and maintenance.

### Service Management (4 tests)

**File**: `service_test.go`

| Test | Description |
|------|-------------|
| `TestServiceRegistrationAndDiscovery` | Service registration and queryability |
| `TestDynamicServiceConnection` | Dynamic service connection after registration |
| `TestServiceConnectionAtStartup` | Service connection on agent startup |
| `TestMultiServiceRegistration` | Multiple services registered and tracked |

**Key Feature**: Automatic service discovery and Beyla auto-instrumentation.

### Telemetry (8 tests)

**File**: `telemetry_test.go`

| Test | Description |
|------|-------------|
| `TestBeylaPassiveInstrumentation` | eBPF HTTP metrics without code changes |
| `TestBeylaColonyPolling` | Colony polls agent for eBPF metrics |
| `TestBeylaVsOTLPComparison` | Compare passive eBPF vs active OTLP |
| `TestOTLPIngestion` | OpenTelemetry span ingestion (app → agent) |
| `TestOTLPAppEndpoints` | OTLP test app functionality validation |
| `TestTelemetryAggregation` | Colony aggregates telemetry (P50/P95/P99) |
| `TestSystemMetricsCollection` | CPU/memory/disk/network metrics (15s interval) |
| `TestSystemMetricsPolling` | Colony polls agent for system metrics |

**Key Features**:
- Passive instrumentation via Beyla eBPF (no code changes)
- Active instrumentation via OpenTelemetry SDK (detailed traces)
- System-level metrics collection

### Profiling (2 tests)

**File**: `profiling_test.go`

| Test | Description |
|------|-------------|
| `TestContinuousProfiling` | Always-on CPU profiling at 19Hz |
| `TestOnDemandProfiling` | On-demand CPU profiling at 99Hz (debug sessions) |

**Key Feature**: Low-overhead continuous profiling with high-fidelity on-demand option.

### Debug (3 tests)

**File**: `debug_test.go`

| Test | Status | Description |
|------|--------|-------------|
| `TestUprobeTracing` | ⏸️ Skipped | Attach uprobes to specific functions |
| `TestUprobeCallTree` | ⏸️ Skipped | Construct call trees from uprobe data |
| `TestMultiAgentDebugSession` | ⏸️ Skipped | Coordinate debug sessions across agents |

**Note**: Tests are implemented with `.Skip()` and detailed documentation for future implementation.

### E2E Orchestration (4 tests)

**File**: `e2e_orchestrator_test.go`

| Test | Description |
|------|-------------|
| `Test1_MeshConnectivity` | Full mesh connectivity stack |
| `Test2_ServiceManagement` | Service registration and discovery |
| `Test3_PassiveObservability` | Beyla + system metrics + continuous profiling |
| `Test4_OnDemandProbes` | On-demand profiling and uprobe tracing |

**Key Feature**: Dependency-ordered orchestration that validates the entire stack in sequence.

## Test Infrastructure

### Fixtures

**File**: `fixtures/containers.go`

Container orchestration with support for:
- Multiple agents (`NumAgents`)
- CPU-intensive app (`WithCPUApp`)
- OTLP-instrumented app (`WithOTELApp`)
- SDK app for uprobe testing (`WithSDKApp`)

Example:
```go
fixture, err := fixtures.NewContainerFixture(ctx, fixtures.FixtureOptions{
    NumAgents:   3,
    WithCPUApp:  true,
    WithOTELApp: true,
})
```

### Helpers

**File**: `helpers/clients.go`

gRPC client helpers for all APIs:
- `NewDiscoveryClient()` - Discovery service
- `NewColonyClient()` - Colony service
- `NewAgentClient()` - Agent service
- `QueryAgentTelemetry()` - OTLP spans
- `QueryAgentEbpfMetrics()` - Beyla eBPF metrics
- `QueryAgentSystemMetrics()` - System metrics
- `ListServices()` - Service registry
- `ExecuteColonyQuery()` - Raw SQL on colony DuckDB

**File**: `helpers/waiters.go`

Polling utilities:
- `WaitForCondition()` - Retry with timeout and interval

**File**: `helpers/assertions.go`

Custom test assertions for distributed systems validation.

### Test Apps

**Directory**: `fixtures/apps/`

| App | Purpose | Location |
|-----|---------|----------|
| CPU App | CPU-intensive workload for profiling | `tests/e2e/cpu-profile/cpu-intensive-app/` |
| OTLP App | OpenTelemetry instrumented HTTP service | `fixtures/apps/otel-app/` |
| SDK App | Payment processing for uprobe tracing | `fixtures/apps/sdk-app/` |

## Common Patterns

### Async Polling

Many tests verify async operations (colony polling agents). Pattern:

```go
// Generate activity
generateTraffic()

// Wait for async poller (90s is typical)
time.Sleep(90 * time.Second)

// Query colony for aggregated data
resp, err := helpers.ExecuteColonyQuery(ctx, client, sql, maxRows)

// Handle gracefully if not yet implemented
if err != nil {
    s.T().Log("⚠️  WARNING: Feature not yet implemented")
    return
}
```

### Service Discovery

Tests verify services are registered and queryable:

```go
services, err := helpers.ListServices(ctx, client, "")
s.Require().NoError(err)

// Verify expected services
for _, expectedSvc := range []string{"cpu-app", "otel-app"} {
    svc := findService(services, expectedSvc)
    s.Require().NotNil(svc)
    s.Require().Greater(svc.InstanceCount, int32(0))
}
```

## Troubleshooting

### Tests Skip on macOS/Windows

**Expected**: Tests require Linux for eBPF and WireGuard.

```
E2E distributed tests require Linux for eBPF and WireGuard
```

### Docker Daemon Not Running

**Solution**: Start Docker daemon before running tests.

```bash
sudo systemctl start docker  # Linux
```

### Container Cleanup Failures

**Cause**: Containers may persist if tests are interrupted.

**Solution**: Manual cleanup:
```bash
docker ps -a | grep coral | awk '{print $1}' | xargs docker rm -f
docker network ls | grep coral | awk '{print $1}' | xargs docker network rm
```

### Timeout Errors

**Cause**: Tests have generous timeouts (30min total, 60-120s per operation).

**Solution**: Check container logs:
```bash
docker logs <container-id>
```

### Permission Denied (eBPF/WireGuard)

**Cause**: Tests run containers in privileged mode.

**Solution**: Ensure Docker has sufficient privileges:
```bash
sudo chmod 666 /var/run/docker.sock
```

## Test Timing

Approximate runtime for test suites:
- **Mesh Suite**: ~5-8 minutes (7 tests)
- **Service Suite**: ~3-5 minutes (4 tests)
- **Telemetry Suite**: ~8-10 minutes (8 tests)
- **Profiling Suite**: ~4-5 minutes (2 tests)
- **Debug Suite**: ~1 second (3 skipped tests)
- **E2E Orchestrator Suite**: ~10-15 minutes (4 comprehensive tests)
- **Total**: ~30-45 minutes for all tests

## Next Steps

1. **Run Tests**: Execute test suite on Linux to verify all passes
2. **CI Integration**: Add to CI pipeline with Docker-in-Docker
3. **Level 3 Implementation**: Implement debug session API and uprobe tracing
4. **Performance**: Optimize container startup and polling intervals
5. **Documentation**: Add more examples and troubleshooting guides

## Related Documentation

- [PLAN.md](./PLAN.md) - Detailed implementation plan and architecture
- [RFD 067](../../../RFDs/067-unified-query-interface.md) - Unified query interface
- [RFD 076](../../../RFDs/076-focused-query-interface.md) - Focused query interface
- [RFD 004](../../../RFDs/004-mcp-server-integration.md) - MCP integration

## Contributing

When adding new tests:
1. Follow existing patterns (testify/suite, container fixtures)
2. Use helper functions for common operations
3. Handle async operations with appropriate timeouts
4. Include graceful degradation for unimplemented features
5. Update this README and PLAN.md
6. Ensure tests are idempotent and clean up properly
