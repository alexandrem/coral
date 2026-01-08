# Distributed E2E Tests

These tests validate the Coral distributed system in a realistic environment
using Docker Compose. They cover connectivity, service discovery, telemetry (
Beyla/OTLP), system metrics, and profiling.

## Test Categories

### 1. Initialization Tests (`init_test.go`)
Tests `coral init` command in isolation (no docker-compose needed):
- Colony config generation
- WireGuard key generation
- Certificate Authority setup
- Config validation

```bash
# Run init tests standalone
go test -v -run TestInitSuite
```

### 2. Runtime Tests (all others)
Tests requiring docker-compose infrastructure:
- Mesh connectivity
- Service management
- Passive observability (Beyla, OTLP, system metrics)
- On-demand probes (CPU profiling, debugging)

See [TESTING.md](TESTING.md) for detailed documentation.

## Quick Start

```bash
cd tests/e2e/distributed

# Enable BuildKit for fast builds (one-time setup)
export DOCKER_BUILDKIT=1

# Run everything (build, test, cleanup)
make test-all
```

## Prerequisites

1. **Linux OS**: eBPF and WireGuard require a Linux kernel.
    - Native Linux, or
    - Linux VM (Colima, Multipass, etc.)
2. **Docker**: With BuildKit enabled.
3. **Go 1.25+**: For running tests.
4. **Hardware (Recommended)**:
    - 4 CPU cores
    - 8GB RAM
    - 10GB free disk space

### For Colima Users

```bash
# Start Colima with appropriate resources
colima start --cpu 4 --memory 8 --disk 100
```

## Running Tests

### Option 1: Full Automated Run (Recommended)

```bash
make test-all
```

This will:

1. Build all container images (BuildKit recommended).
2. Start all services (Discovery, Colony, Agents, Apps).
3. Run all E2E tests.
4. Stop and cleanup services.

**Time**: ~15-20 minutes (first run), ~6-11 minutes (subsequent runs with
cache).

### Option 2: Manual Development Workflow

Useful for running specific tests repeatedly without restarting services.

```bash
cd tests/e2e/distributed

# 1. Build images (one time, or after code changes)
DOCKER_BUILDKIT=1 make build

# 2. Start services
make up

# 3. Run all tests (can run multiple times)
make test

# 4. Or run specific tests
go test -v -run TestMeshSuite
go test -v -run TestOTLPIngestion

# 5. Stop services when done
make down
```

### Useful Commands

```bash
make logs              # View logs for all services
make logs-colony       # View Colony logs
make logs-agent-0      # View Agent-0 logs
make status            # Check service status
make clean             # Stop and remove volumes
```

## Test Structure

The tests are organized into suites based on functionality.

### mesh_test.go (MeshSuite)

**Purpose**: Tests WireGuard mesh connectivity, agent registration, heartbeat,
and reconnection.

- `TestDiscoveryServiceAvailability`: Discovery service health checks
- `TestColonyRegistration`: Colony registers with discovery
- `TestColonyStatus`: Colony status API validation
- `TestAgentRegistration`: Agent registration flow
- `TestMultiAgentMesh`: Multiple agents with unique mesh IPs
- `TestHeartbeat`: Heartbeat mechanism
- `TestAgentReconnection`: Reconnection after colony restart

### service_test.go (ServiceSuite)

**Purpose**: Tests service registration, connection, and discovery.

- `TestServiceRegistrationAndDiscovery`: Service registry and discovery
- `TestDynamicServiceConnection`: Dynamic service connection via API (
  placeholder)
- `TestMultiServiceRegistration`: Multiple services per agent (placeholder)
- `TestServiceConnectionAtStartup`: Service connection at startup (placeholder)

### telemetry_test.go (TelemetrySuite)

**Purpose**: Tests passive (Beyla) and active (OTLP) telemetry collection, plus
system metrics.

- `TestBeylaPassiveInstrumentation`: Passive eBPF HTTP metrics via Beyla
- `TestBeylaColonyPolling`: Colony polls agent for eBPF metrics
- `TestBeylaVsOTLPComparison`: Compare passive vs active instrumentation
- `TestOTLPIngestion`: OTLP trace ingestion from app to agent
- `TestOTLPAppEndpoints`: OTLP test app functionality
- `TestTelemetryAggregation`: Agent → colony polling with P50/P95/P99
- `TestSystemMetricsCollection`: CPU/memory/disk/network metrics
- `TestSystemMetricsPolling`: Colony polls agent for system metrics

### profiling_test.go (ProfilingSuite)

**Purpose**: Tests continuous and on-demand CPU profiling.

- `TestContinuousProfiling`: Always-on 19Hz CPU profiling
- `TestOnDemandProfiling`: On-demand 99Hz profiling (placeholder)

### debug_test.go (DebugSuite)

**Purpose**: Tests deep introspection via uprobe tracing and debug sessions.

- `TestUprobeTracing`: Uprobe-based function tracing (placeholder)
- `TestUprobeCallTree`: Call tree construction (placeholder)
- `TestMultiAgentDebugSession`: Multi-agent debug sessions (placeholder)

### cli_mesh_test.go (CLIMeshSuite)

**Purpose**: Tests user-facing CLI commands for colony and agent management.

CLI tests validate output formatting, error handling, and flag validation - not infrastructure behavior (covered by API tests).

**Tests**:
- `TestColonyStatusCommand`: Validates `coral colony status` (table + JSON)
- `TestColonyAgentsCommand`: Validates `coral colony agents` (table + JSON)
- `TestAgentListCommand`: Validates `coral agent list` (table + JSON)
- `TestServiceListCommand`: Validates `coral service list` (table + JSON)
- `TestInvalidColonyEndpoint`: Error handling for invalid endpoints
- `TestTableOutputFormatting`: Table structure validation
- `TestJSONOutputValidity`: JSON parsing and structure validation

**Running CLI Tests**:
```bash
# Run all tests (includes CLI tests)
make test-all

# Run CLI tests standalone
go test -v -run TestCLIMeshSuite -tags=standalone

# Skip CLI tests
go test -v -run TestE2EOrchestrator -skip CLI_

# Run just CLI tests from orchestrator
go test -v -run TestE2EOrchestrator/Test5_CLICommands
```

**Prerequisites**:
- `coral` binary must be built: Run `make build` in project root
- CLI tests look for binary in `bin/coral` (relative to project root)
- Falls back to PATH if `bin/coral` doesn't exist

**What CLI Tests Validate**:
- ✅ Output formatting (table vs JSON)
- ✅ Flag combinations and validation
- ✅ Error messages and exit codes
- ✅ User experience concerns

**What CLI Tests DON'T Validate**:
- ❌ Infrastructure behavior (covered by API tests)
- ❌ Data accuracy (covered by API tests)
- ❌ Performance benchmarks (separate suite)

### cli_query_test.go (CLIQuerySuite)

**Purpose**: Tests user-facing CLI query commands for observability data.

CLI tests validate output formatting, flag combinations, and error handling - not query accuracy (covered by API tests).

**Tests**:
- `TestQuerySummaryCommand`: Validates `coral query summary` (table + JSON, with --service and --time flags)
- `TestQueryServicesCommand`: Validates `coral query services` (table + JSON)
- `TestQueryTracesCommand`: Validates `coral query traces` (with --service, --time, --limit flags)
- `TestQueryMetricsCommand`: Validates `coral query metrics` (with --service, --time flags)
- `TestQueryFlagCombinations`: Tests various flag combinations (time ranges, limits, service filters)
- `TestQueryInvalidFlags`: Error handling for invalid time ranges and parameters
- `TestQueryJSONOutputValidity`: JSON parsing and structure validation
- `TestQueryTableOutputFormatting`: Table structure validation

**Running CLI Query Tests**:
```bash
# Run all tests (includes CLI query tests)
make test-all

# Run CLI query tests standalone
go test -v -run TestCLIQuerySuite -tags=standalone

# Run just query tests from orchestrator
go test -v -run TestE2EOrchestrator/Test5_CLICommands/CLI_Query
```

**What Query CLI Tests Validate**:
- ✅ Command syntax and flag parsing
- ✅ Output formatting (table vs JSON)
- ✅ Flag combinations (--service, --time, --limit)
- ✅ Time range handling (1m, 5m, 1h, etc.)
- ✅ Error messages for invalid inputs

**What Query CLI Tests DON'T Validate**:
- ❌ Query result accuracy (covered by API tests)
- ❌ Data aggregation logic (covered by TelemetrySuite)
- ❌ Query performance (separate benchmarks)

**Future Phases**:
- Phase 3: `cli_config_test.go` - Config commands (`coral config get-contexts/current-context/use-context`)

### Observability Layers

- **Level 0**: Beyla eBPF (TelemetrySuite)
- **Level 1**: OTLP Telemetry (TelemetrySuite)
- **Level 2**: System Metrics & Profiling (TelemetrySuite, ProfilingSuite)
- **Level 3**: Deep Introspection (ProfilingSuite, DebugSuite)

## Service Endpoints

When services are running (`make up`), they are accessible at:

| Service       | Endpoint                 | Purpose                                          |
|:--------------|:-------------------------|:-------------------------------------------------|
| **Discovery** | `http://localhost:18080` | Service registry (Port 18080 to avoid conflicts) |
| **Colony**    | `localhost:9000`         | gRPC endpoint                                    |
| **Agent-0**   | `localhost:9001`         | gRPC endpoint                                    |
| **Agent-1**   | `localhost:9002`         | gRPC endpoint                                    |
| **CPU App**   | `http://localhost:8081`  | CPU-intensive test app (Health: `/health`)       |
| **OTEL App**  | `http://localhost:8082`  | OTLP instrumented app (Health: `/health`)        |
| **SDK App**   | `http://localhost:3001`  | SDK app for uprobe tests                         |

## BuildKit Configuration

The E2E tests use Docker BuildKit's cache mounts to dramatically speed up Go
module downloads.

**Enable BuildKit (Recommended):**

Add to your shell profile (`~/.zshrc`, `~/.bashrc`):

```bash
export DOCKER_BUILDKIT=1
```

Verification:

```bash
echo $DOCKER_BUILDKIT  # Should show "1"
```

**Performance Impact:**

- Without BuildKit: ~5-10 mins build time.
- With BuildKit: ~30-60 secs build time (after first run).

## Troubleshooting

### Services won't start

```bash
docker ps          # Check what's running
make logs          # Check logs
make clean         # Clean up volumes
make build         # Rebuild images
make up            # Start again
```

### Tests fail with "services not ready"

```bash
make status        # Check status
make down && make up # Restart
```

### BuildKit errors

```bash
export DOCKER_BUILDKIT=1
make build
```

### Out of memory

If using Colima, you might need to increase resources:

```bash
colima stop
colima start --cpu 4 --memory 10
```

### Tests hang or timeout

You can increase the timeout:

```bash
go test -v -timeout 60m ./...
```
