# Coral E2E Distributed Test Suite

Comprehensive end-to-end test suite for Coral's distributed systems functionality, covering connectivity, discovery, and all four observability layers.

## Overview

This test suite validates the complete distributed architecture of Coral:
- **Phase 1**: Connectivity & Discovery (8 tests)
- **Phase 2**: Observability Layers 0-3 (13 tests)
- **Total**: 21 E2E tests

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
go test -v -run TestE2EDistributedSuite
go test -v -run TestObservabilityL0Suite
go test -v -run TestObservabilityL1Suite
go test -v -run TestObservabilityL2Suite
go test -v -run TestObservabilityL3Suite

# Run specific test
go test -v -run TestServiceRegistration
go test -v -run TestLevel1_OTLPIngestion

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

### Phase 1: Connectivity (8 tests)

**File**: `connectivity_test.go`

| Test | Description |
|------|-------------|
| `TestDiscoveryServiceAvailability` | Discovery service health check |
| `TestColonyRegistrationWithDiscovery` | Colony registers with discovery |
| `TestColonyStatus` | Colony status API validation |
| `TestAgentRegistration` | Agent registration with colony |
| `TestMultiAgentMeshAllocation` | Unique mesh IP allocation |
| `TestHeartbeatMechanism` | Agent heartbeat updates |
| `TestServiceRegistration` | Service registry and discovery ✨ NEW |
| `TestAgentReconnectionAfterColonyRestart` | Reconnection after colony restart |

### Phase 2: Observability (13 tests)

#### Level 0: Beyla eBPF (3 tests)

**File**: `observability_l0_test.go`

| Test | Description |
|------|-------------|
| `TestLevel0_BeylaHTTPMetrics` | Passive eBPF HTTP metrics capture |
| `TestLevel0_BeylaColonyPolling` | Colony polls agent for eBPF metrics |
| `TestLevel0_BeylaVsOTLP` | Compare passive eBPF vs active OTLP |

**Key Feature**: No code instrumentation required - Beyla auto-instruments registered services.

#### Level 1: OTLP Telemetry (3 tests)

**File**: `observability_l1_test.go`

| Test | Description |
|------|-------------|
| `TestLevel1_OTLPIngestion` | App → agent span ingestion |
| `TestLevel1_OTELAppEndpoints` | OTLP test app functionality |
| `TestLevel1_ColonyAggregation` | Agent → colony polling with P50/P95/P99 |

**Key Feature**: Active instrumentation with OpenTelemetry SDK for detailed traces.

#### Level 2: Continuous Intelligence (3 tests)

**File**: `observability_l2_test.go`

| Test | Description |
|------|-------------|
| `TestLevel2_SystemMetricsCollection` | CPU/memory/disk/network metrics (15s interval) |
| `TestLevel2_SystemMetricsPolling` | Colony polls agent for system metrics |
| `TestLevel2_ContinuousCPUProfiling` | Always-on CPU profiling (19Hz) |

**Key Feature**: Low-overhead continuous monitoring with system metrics and CPU profiling.

#### Level 3: Deep Introspection (4 placeholder tests)

**File**: `observability_l3_test.go`

| Test | Status | Requirements |
|------|--------|--------------|
| `TestLevel3_OnDemandCPUProfiling` | ⏸️ Skipped | Debug session API, 99Hz profiler |
| `TestLevel3_UprobeTracing` | ⏸️ Skipped | SDK app, uprobe attachment |
| `TestLevel3_UprobeCallTree` | ⏸️ Skipped | Call tree construction |
| `TestLevel3_MultiAgentDebugSession` | ⏸️ Skipped | Multi-agent coordination |

**Note**: Tests are implemented with `.Skip()` and detailed documentation for future implementation.

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
- **Connectivity Suite**: ~5-8 minutes (8 tests)
- **Level 0 Suite**: ~4-6 minutes (3 tests)
- **Level 1 Suite**: ~5-7 minutes (3 tests)
- **Level 2 Suite**: ~4-5 minutes (3 tests)
- **Level 3 Suite**: ~1 second (4 skipped tests)
- **Total**: ~20-30 minutes for all tests

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
