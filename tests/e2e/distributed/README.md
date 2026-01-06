# E2E Distributed Tests

## Test Status (Last Run: 2026-01-05)

### Passing ✓
- **TestDebugSuite** - All tests pass (Level 3 features appropriately skipped)
- **TestMeshSuite** - All mesh connectivity tests pass
  - Discovery service availability
  - Colony registration
  - Agent registration
  - Multi-agent mesh IP allocation
  - Heartbeat mechanism
  - Agent reconnection
- **TestProfilingSuite** - Continuous CPU profiling verified

### Failing ✗
- **TestTelemetrySuite** - gRPC queries fail with "unexpected EOF"
  - OTLPIngestion, BeylaPassiveInstrumentation, System metrics tests

### Known Issues
1. **WireGuard mesh connectivity** - Agent not configuring mesh after failed initial registration
   - Agent `wg0` interface state is DOWN (colony is UP)
   - Causes all mesh-based communication to fail (service poller, telemetry poller, etc.)
   - Colony shows error: "Failed to send handshake initiation: no known endpoint for peer"
   - **Root cause** (confirmed via `/status` endpoint):
     - Initial registration fails (colony not in discovery yet)
     - Code returns early → `ConfigureMesh()` never called
     - Later, reconnection loop succeeds → but doesn't call `ConfigureMesh()` either
     - Result: `mesh_ip=""`, `peer_count=0`, wg0 stays DOWN
   - **Location**: `internal/cli/agent/startup/builder.go:270-274` (early return on failed registration)
   - **Fix needed**: Reconnection loop must call `ConfigureMesh()` after successful registration
   - Discovery service is working correctly - this is an agent startup sequencing bug

2. Agent gRPC endpoints returning "unavailable: unexpected EOF" for telemetry queries
   - Affects QueryTelemetry, QueryEbpfMetrics, QuerySystemMetrics RPCs

### Recently Fixed
- ✓ **Agent gRPC port configuration** (2026-01-06):
  - Fixed docker-compose port mapping: `9001:9001` (was `9001:9000`)
  - Added `CORAL_AGENT_BIND_ALL=true` to bind to all interfaces
  - Updated service_poller to use `DefaultAgentPort` constant (9001)

- ✓ **Added mesh configuration validation test** (2026-01-06):
  - New `TestAgentMeshConfiguration` queries agent `/status` endpoint
  - Validates: mesh_ip set, mesh_subnet set, wg0 UP, peer_count > 0
  - Helps diagnose WireGuard mesh setup issues in e2e tests
  - Currently failing due to known issue #1 above

- ✓ **Service registration** - Fixed service discovery architecture:
  - **Colony ListServices API** now queries `services` table instead of `beyla_http_metrics`
  - **Created ServicePoller** to sync services from agents to colony every 10 seconds
  - **Updated test** to verify both agent-side and colony-side service registration
  - Services are now discoverable immediately after ConnectService (no traffic needed)
  - **Note**: Colony-side registration blocked by WireGuard mesh issue above

## Port Configuration

**IMPORTANT:** E2E tests use **port 18080** for discovery (not 8080) to avoid conflicts with local dev services.

Before running tests, stop all local Coral services:
```bash
lsof -i :18080  # Check for conflicts
docker-compose up -d
go test -v ./...
```
