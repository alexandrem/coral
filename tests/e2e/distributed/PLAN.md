# E2E Test Suite for Coral Distributed Systems

## Objective

Build a comprehensive E2E test suite for Coral's distributed systems functionality, focusing initially on **node connectivity and discovery** (discovery service → WireGuard mesh → agent registration → heartbeat), then expanding to cover all **observability layers (0-3)**.

## Scope

**Phase 1 (Initial Focus)**: Connectivity & Discovery
- Discovery service registration and lookup
- WireGuard mesh establishment (colony ↔ agents)
- Agent-to-colony registration flow
- Heartbeat mechanism and reconnection

**Phase 2 (Follow-up)**: Observability Layers
- Level 0: Passive RED Metrics (Beyla eBPF)
- Level 1: External Telemetry (OTLP)
- Level 2: Continuous Intel (system metrics + continuous profiling)
- Level 3: Deep Introspection (on-demand profiling + uprobe tracing)

**Out of Scope**: LLM/MCP components (focus on core distributed systems value proposition)

## Architecture

### Test Suite Structure

```
tests/e2e/
├── distributed/              # NEW: Main E2E suite
│   ├── suite.go             # Base test suite with testify/suite
│   ├── connectivity_test.go # Discovery + WireGuard + Registration
│   ├── observability_*.go   # Observability layer tests (Phase 2)
│   ├── fixtures/
│   │   ├── containers.go    # Testcontainer builders
│   │   └── apps/            # Dockerfiles for test apps
│   └── helpers/
│       ├── waiters.go       # Polling utilities
│       └── assertions.go    # Custom assertions
```

### Testcontainer Strategy

Use **testcontainers-go** to orchestrate multi-component distributed system tests:

1. **Discovery Service Container**
   - Built from `cmd/discovery`
   - Exposes HTTP gRPC endpoint
   - Health check: `/health`

2. **Colony Container**
   - Built from `cmd/coral`
   - Command: `colony start`
   - Privileged mode (for WireGuard)
   - Mounts: config dir, DuckDB storage

3. **Agent Container(s)**
   - Built from `cmd/coral`
   - Command: `agent start`
   - Privileged mode + capabilities (CAP_NET_ADMIN, CAP_SYS_ADMIN, CAP_BPF)
   - Multiple agents for multi-node scenarios

4. **Test Application Containers**
   - **OTLP App**: `tests/e2e/distributed/fixtures/apps/otel-app/main.go` (NEW: OTLP-instrumented HTTP service, based on examples/otel-go-app but isolated for testing)
   - **SDK App**: `tests/e2e/distributed/fixtures/apps/sdk-app/main.go` (NEW: payment processing app with SDK, based on examples/sdk-go for uprobe tracing tests)
   - **CPU App**: `tests/e2e/cpu-profile/cpu-intensive-app/main.go` (EXISTING: CPU-intensive app for profiling tests)
   - Run alongside agents to generate observable workload

### Test Lifecycle (testify/suite)

```go
type E2EDistributedSuite struct {
    suite.Suite
    fixture *ContainerFixture  // Manages all containers
    clients map[string]Client  // gRPC clients for testing
}

// SetupSuite: One-time setup (check platform, build images)
// TearDownSuite: Cleanup (remove network, volumes)
// SetupTest: Per-test container startup
// TearDownTest: Per-test container cleanup
```

**Isolation Strategy**: Fresh containers for each test to ensure clean state.

## Implementation Plan

### Phase 1: Foundation & Connectivity (Weeks 1-3)

#### Step 1.1: Base Suite Infrastructure
**Files to create:**
- `tests/e2e/distributed/suite.go` - Base test suite with testify/suite integration
- `tests/e2e/distributed/fixtures/containers.go` - Container builders and orchestration
- `tests/e2e/distributed/fixtures/apps/otel-app/main.go` - OTLP test app (based on examples/otel-go-app)
- `tests/e2e/distributed/fixtures/apps/otel-app/Dockerfile` - Dockerfile for OTLP app
- `tests/e2e/distributed/fixtures/apps/sdk-app/main.go` - SDK test app (based on examples/sdk-go)
- `tests/e2e/distributed/fixtures/apps/sdk-app/Dockerfile` - Dockerfile for SDK app
- `tests/e2e/distributed/helpers/waiters.go` - Polling utilities (wait for health, registration, etc.)

**Key Implementation Details:**
```go
// fixtures/containers.go
type ContainerFixture struct {
    Network   testcontainers.Network
    Discovery testcontainers.Container
    Colony    testcontainers.Container
    Agents    []testcontainers.Container
}

func NewContainerFixture(ctx context.Context) (*ContainerFixture, error) {
    // 1. Create Docker network
    // 2. Start discovery service container
    // 3. Wait for discovery health check
    // 4. Start colony container (with discovery endpoint config)
    // 5. Wait for colony WireGuard ready
    // 6. Start agent containers
    // 7. Wait for agent registration
}
```

**Platform Requirements:**
- Linux only (eBPF requirement)
- Check in `SetupSuite`: skip on non-Linux platforms
- Verify Docker availability and kernel features

#### Step 1.2: Discovery Service Tests
**File:** `tests/e2e/distributed/connectivity_test.go`

**Test Cases:**
1. `TestDiscoveryServiceRegistration`
   - Colony registers with discovery service
   - Verify registry entry with endpoints, public key, mesh IPs
   - Verify TTL refresh mechanism

2. `TestAgentDiscoveryLookup`
   - Agent queries discovery for colony info
   - Verify endpoints, STUN-discovered IPs, mesh configuration returned
   - Test lookup with invalid mesh_id (error handling)

**Reference Files:**
- `internal/discovery/server/server.go` - Discovery service implementation
- `internal/discovery/client/client.go` - Client library
- `proto/coral/discovery/v1/discovery.proto` - Protocol definitions

#### Step 1.3: WireGuard Mesh Tests

**Test Cases:**
1. `TestWireGuardMeshEstablishment`
   - Colony creates WireGuard device (10.42.0.1)
   - Agent creates WireGuard device, configures colony as peer
   - Verify tunnel is established (ping test from container)
   - Verify mesh subnet configuration (10.42.0.0/16)

2. `TestWireGuardIPAllocation`
   - Start multiple agents (3+)
   - Verify unique IP allocation (.2, .3, .4, ...)
   - Verify WireGuard peer list is correct on colony
   - Test IP allocation persistence (agent reconnect gets same IP)

**Reference Files:**
- `internal/wireguard/device.go` - WireGuard device management
- `internal/colony/wireguard/setup.go` - Colony-side setup
- `internal/cli/agent/startup/network.go` - Agent-side network init

#### Step 1.4: Agent Registration Flow Tests

**Test Cases:**
1. `TestAgentRegistrationFlow`
   - Agent calls `MeshService.Register` over WireGuard tunnel
   - Verify colony validates credentials (colony_id, colony_secret)
   - Verify mesh IP assignment in response
   - Verify WireGuard peer added dynamically
   - Verify agent configures mesh IP on interface
   - Test communication over mesh (gRPC call from colony → agent)

2. `TestAgentAuthenticationFailure`
   - Test invalid colony_secret (expect registration rejection)
   - Test invalid colony_id (expect rejection)
   - Verify failed registration doesn't add WireGuard peer

**Reference Files:**
- `internal/colony/mesh/handler.go` - Registration handler
- `internal/cli/agent/startup/helpers.go` - `registerWithColony()`
- `proto/coral/mesh/v1/auth.proto` - Protocol definitions

#### Step 1.5: Heartbeat & Reconnection Tests

**Test Cases:**
1. `TestHeartbeatMechanism`
   - Register agent
   - Verify heartbeat loop starts (15-second interval)
   - Check heartbeat updates in colony registry
   - Monitor heartbeat timestamps in DuckDB

2. `TestReconnectionAfterColonyRestart`
   - Establish agent-colony connection
   - Stop colony container
   - Verify agent detects disconnect (3 failed heartbeats)
   - Restart colony container
   - Verify agent reconnects with exponential backoff
   - Verify mesh re-establishment (WireGuard peers restored)

3. `TestReconnectionAfterDiscoveryDown`
   - Establish full system
   - Stop discovery service
   - New agent tries to start (should use cached endpoints or fail gracefully)
   - Restart discovery
   - Verify agent can now register

**Reference Files:**
- `internal/cli/agent/startup/connection_manager.go` - Connection manager with state machine
- `internal/colony/mesh/handler.go` - Heartbeat handler

#### Step 1.6: Helper Functions

**Files:**
- `tests/e2e/distributed/helpers/waiters.go`

**Functions to implement:**
```go
// Wait for container health check to pass
func WaitForContainerHealth(ctx, container, timeout) error

// Wait for agent registration in colony registry
func WaitForAgentRegistration(ctx, colonyClient, agentID, timeout) error

// Wait for WireGuard peer to appear
func WaitForWireGuardPeer(ctx, device, pubkey, timeout) error

// Poll for metric/telemetry data
func WaitForCondition(ctx, predicate func() bool, timeout) error
```

- `tests/e2e/distributed/helpers/assertions.go`

**Functions to implement:**
```go
// Assert WireGuard peer exists with correct endpoint
func AssertWireGuardPeerExists(t, device, pubkey string)

// Assert agent is registered in colony
func AssertAgentRegistered(t, registry, agentID string)

// Assert mesh IP is allocated
func AssertMeshIPAllocated(t, allocator, agentID string, expectedIP net.IP)
```

### Phase 2: Observability Layers (Weeks 4-6)

#### Step 2.1: Level 0 - Beyla eBPF Metrics
**File:** `tests/e2e/distributed/observability_l0_test.go`

**Test Cases:**
1. `TestLevel0_BeylaHTTPMetrics`
   - Start agent with Beyla enabled
   - Start `cpu-intensive-app` container (HTTP server on :8080)
   - Generate HTTP traffic: `curl http://agent-container:8080/`
   - Wait for Beyla to capture spans (OTLP receiver on agent)
   - Query agent's local DuckDB: verify `beyla_http_metrics` table
   - Verify RED metrics: request count, error rate, latency buckets

2. `TestLevel0_BeylaColonyPolling`
   - Agent collects Beyla metrics locally
   - Colony polls agent via `AgentService.QueryEbpfMetrics`
   - Verify metrics aggregation in colony DuckDB
   - Verify 1-minute time-series buckets

**Reference Files:**
- `internal/agent/beyla/manager.go` - Beyla subprocess manager
- `internal/agent/beyla/storage.go` - Local DuckDB storage
- `internal/colony/beyla_poller.go` - Colony poller

**Note:** Run actual Beyla subprocess (user preference). Requires Beyla binary in container image.

#### Step 2.2: Level 1 - OTLP Telemetry
**File:** `tests/e2e/distributed/observability_l1_test.go`

**Test Cases:**
1. `TestLevel1_OTLPIngestion`
   - Start OTLP test app container (based on examples/otel-go-app)
   - Configure app to send OTLP to agent (env: `OTEL_EXPORTER_OTLP_ENDPOINT=agent:4317`)
   - Generate HTTP traffic to app endpoints (/api/users, /api/products, /api/checkout)
   - Verify traces ingested in agent's `telemetry` storage
   - Verify span attributes: db.system, cache.key, http.method, etc.
   - Verify error spans captured (checkout endpoint has ~15% error rate)

2. `TestLevel1_TelemetryAggregation`
   - Full flow from agent → colony
   - Colony polls agent via `AgentService.QueryTelemetry`
   - Colony aggregates into 1-minute summaries (P50/P95/P99)
   - Verify `otel_summaries` table in colony DuckDB
   - Query and verify summary retrieval
   - Verify multi-step trace correlation (checkout has 5 steps)

**Reference Files:**
- `internal/agent/telemetry/otlp_receiver.go` - OTLP receiver
- `internal/colony/telemetry_poller.go` - Colony poller
- `internal/colony/telemetry_aggregator.go` - Aggregation logic
- `internal/colony/telemetry_e2e_test.go` - Existing pattern to adapt

#### Step 2.3: Level 2 - Continuous Intelligence
**File:** `tests/e2e/distributed/observability_l2_test.go`

**Test Cases:**
1. `TestLevel2_SystemMetricsCollection`
   - Agent's `SystemCollector` runs automatically (15-second interval)
   - Verify CPU, memory, disk, network metrics in agent storage
   - Colony polls via `AgentService.QuerySystemMetrics`
   - Verify aggregated metrics in colony DuckDB (`system_metrics_summaries`)

2. `TestLevel2_ContinuousCPUProfiling`
   - Start agent with continuous profiling enabled
   - Start `cpu-intensive-app` to generate CPU load
   - Agent profiles at 19Hz
   - Verify profile samples in agent's local storage
   - Colony queries profile data
   - Verify stack traces captured and symbolized

**Reference Files:**
- `internal/agent/collector/system_collector.go` - System metrics
- `internal/agent/profiler/continuous_cpu.go` - Continuous profiler
- `internal/colony/system_metrics_poller.go` - Colony poller
- `internal/colony/cpu_profile_poller.go` - Profile poller

#### Step 2.4: Level 3 - Deep Introspection
**File:** `tests/e2e/distributed/observability_l3_test.go`

**Test Cases:**
1. `TestLevel3_OnDemandCPUProfiling`
   - Colony triggers on-demand profile via debug session
   - Agent runs high-frequency (99Hz) profiling
   - Verify profile collection for specified duration (30s)
   - Verify flame graph data generation

2. `TestLevel3_UprobeTracing`
   - Use test SDK app (`tests/e2e/distributed/fixtures/apps/sdk-app/`)
   - Functions: `ProcessPayment`, `ValidateCard`, `CalculateTotal` (similar to examples/sdk-go)
   - Colony discovers function offsets via SDK
   - Colony starts uprobe collector for `ProcessPayment`
   - Trigger function calls (workload running every 2s)
   - Verify uprobe events captured: entry/exit, duration, args
   - Verify call tree construction

**Reference Files:**
- `internal/agent/debug/cpu_profiler.go` - On-demand profiler
- `internal/agent/debug/uprobe.go` - Uprobe attachment
- `internal/colony/debug/orchestrator.go` - Debug session orchestrator
- `pkg/sdk/debug/` - SDK integration

## Critical Files Reference

### Connectivity & Discovery
- `internal/discovery/server/server.go` - Discovery service
- `internal/discovery/client/client.go` - Discovery client
- `internal/wireguard/device.go` - WireGuard device management
- `internal/colony/wireguard/setup.go` - Colony WireGuard setup
- `internal/cli/agent/startup/network.go` - Agent WireGuard setup
- `internal/colony/mesh/handler.go` - Registration & heartbeat handler
- `internal/cli/agent/startup/connection_manager.go` - Agent connection manager
- `proto/coral/discovery/v1/discovery.proto` - Discovery protocol
- `proto/coral/mesh/v1/auth.proto` - Mesh auth protocol

### Observability Layer 0 (Beyla)
- `internal/agent/beyla/manager.go` - Beyla manager
- `internal/agent/beyla/storage.go` - Agent storage
- `internal/colony/beyla_poller.go` - Colony poller
- `internal/colony/database/beyla.go` - Colony storage schema

### Observability Layer 1 (OTLP)
- `internal/agent/telemetry/otlp_receiver.go` - OTLP receiver
- `internal/colony/telemetry_poller.go` - Colony poller
- `internal/colony/telemetry_aggregator.go` - Aggregation
- `internal/colony/database/telemetry.go` - Storage schema
- `internal/colony/telemetry_e2e_test.go` - Existing pattern

### Observability Layer 2 (Continuous Intel)
- `internal/agent/collector/system_collector.go` - System metrics
- `internal/agent/profiler/continuous_cpu.go` - Continuous profiling
- `internal/colony/system_metrics_poller.go` - System metrics poller
- `internal/colony/cpu_profile_poller.go` - Profile poller
- `internal/colony/database/system_metrics.go` - Schema
- `internal/colony/database/cpu_profiles.go` - Profile schema

### Observability Layer 3 (Deep Introspection)
- `internal/agent/debug/cpu_profiler.go` - On-demand profiler
- `internal/agent/debug/uprobe.go` - Uprobe attachment
- `internal/agent/debug/session.go` - Debug session
- `internal/colony/debug/orchestrator.go` - Session orchestrator
- `internal/colony/debug/session_manager.go` - Session management
- `pkg/sdk/debug/` - SDK integration

### Test Applications (to create/use)
- `tests/e2e/distributed/fixtures/apps/otel-app/main.go` - NEW: OTLP-instrumented HTTP service (based on examples/otel-go-app, isolated for Level 1 testing)
- `tests/e2e/distributed/fixtures/apps/sdk-app/main.go` - NEW: Payment app with SDK (based on examples/sdk-go, isolated for Level 3 uprobe testing)
- `tests/e2e/cpu-profile/cpu-intensive-app/main.go` - EXISTING: CPU-intensive app (for Level 2 profiling)

### Existing Test Patterns to Reuse
- `tests/e2e/discovery/discovery_test.go` - Discovery E2E pattern
- `internal/colony/telemetry_e2e_test.go` - Telemetry E2E pattern
- `internal/agent/telemetry/integration_test.go` - Integration test pattern

## Testing Strategy

### Isolation
- Fresh containers for each test (avoid state leakage)
- Unique colony IDs per test
- In-memory DuckDB where possible for speed
- t.TempDir() for file-based storage

### Platform Awareness
```go
func (suite *E2EDistributedSuite) SetupSuite() {
    if runtime.GOOS != "linux" {
        suite.T().Skip("E2E distributed tests require Linux for eBPF")
    }
    // Check Docker availability
    // Verify kernel features (WireGuard, eBPF)
}
```

### CI Integration (Later Phase)
- For now: Simple `make test-e2e` target
- Future: Add quick mode, parallel execution, Docker-in-Docker for CI
- Current focus: Get tests working reliably first, optimize later

## Success Criteria

### Phase 1 Completion
- [x] All connectivity tests pass (discovery, WireGuard, registration, heartbeat)
- [x] Reconnection scenarios implemented (colony restart)
- [x] Multi-agent coordination tested (3 agents with unique mesh IPs)
- [x] Tests compile successfully and run on Linux with proper cleanup
- [x] Foundation ready for observability layer tests

**Status**: ✅ **Phase 1 COMPLETE** - All connectivity and discovery tests implemented and working

### Phase 2 Completion
- [x] All 4 observability layers have E2E test coverage (12 tests total)
- [x] Level 1 (OTLP): Data flow from app → agent verified end-to-end
- [ ] Level 1 (OTLP): Colony aggregation verified (pending colony polling)
- [ ] Level 2: System metrics collection verified (backend ready, query API needed)
- [ ] Level 0 (Beyla): Real Beyla subprocess integration (binary not in image yet)
- [ ] Level 3: Test SDK app successfully traced with uprobes (uprobe API needed)
- [ ] Level 2/3: CPU profiling (continuous + on-demand) verified (profiling API needed)

**Status**: 🟢 **Phase 2 Test Infrastructure Complete** - Level 1 E2E working!

## Implementation Notes

- **Dependencies**: Add `testcontainers-go` to go.mod
- **Container Images**: Build from Dockerfiles in `tests/e2e/distributed/fixtures/apps/`
- **Test OTLP App**: Create version of examples/otel-go-app in tests/e2e for isolation
  - Multiple HTTP endpoints: /api/users, /api/products, /api/checkout
  - Full OTLP trace instrumentation
  - Variable latency patterns (simulated DB, cache)
  - Error injection (5-15% error rate for testing error capture)
- **Test SDK App**: Create version of examples/sdk-go in tests/e2e for isolation
  - Include functions: ProcessPayment, ValidateCard, CalculateTotal
  - HTTP server for health checks
  - SDK integration for uprobe tracing
- **Beyla Binary**: Include in agent container image for Level 0 tests
- **Privileged Mode**: Required for WireGuard and eBPF (security acceptable for E2E tests)
- **Network**: Containers share a Docker network for connectivity
- **Timeouts**: Use generous timeouts initially (30s-60s), optimize after stability

## Next Steps

1. Create base suite structure and container fixtures
2. Implement connectivity tests (discovery → WireGuard → registration)
3. Test with single agent, then expand to multi-agent
4. Add observability layer tests incrementally (L0 → L1 → L2 → L3)
5. Refine helper functions and assertions based on common patterns
6. Document test suite usage and troubleshooting
