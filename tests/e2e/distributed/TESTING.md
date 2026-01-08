# E2E Testing Guide

## Overview

The E2E test suite is organized into two categories:

### 1. **Initialization Tests** (`init_test.go`)

Tests the `coral init` command in isolation without requiring docker-compose
infrastructure.

**What it tests:**

- Colony configuration generation
- WireGuard key generation
- Certificate Authority setup (RFD 047)
- Config file structure validation
- Custom storage paths and discovery URLs
- Default colony setting

**How to run:**

```bash
# Run init tests only
go test -v -run TestInitSuite

# Run with standalone tag
go test -v -tags=standalone -run TestInitSuite
```

**Why separate?**

- Doesn't need docker-compose (faster, simpler)
- Tests the init command in isolation
- Can run on any machine without containers
- Validates config generation before testing runtime

### 2. **Runtime Tests** (all other test files)

Tests that require running colony/agents via docker-compose.

**Test Groups (dependency-ordered):**

1. **Mesh Connectivity** - WireGuard mesh, discovery, agent registration
2. **Service Management** - Service connection, health checks, lifecycle
3. **Passive Observability** - Beyla eBPF, OTLP ingestion, system metrics
4. **On-Demand Probes** - CPU profiling, debugging, SDK integration

**How to run:**

```bash
# Start infrastructure
cd tests/e2e/distributed
make up

# Run all tests (via orchestrator)
go test -v -run TestE2EOrchestrator

# Run specific suite
go test -v -run TestMeshSuite
go test -v -run TestTelemetrySuite

# With standalone tag (individual suites)
go test -v -tags=standalone -run TestMeshSuite

# Stop infrastructure
make down
```

## E2E Configuration

### Faster Poll Intervals

The E2E environment uses **faster poll intervals** than production for reliable,
fast tests:

- **System metrics**: 15s (production: 60s)
- **Beyla metrics**: 15s (production: 60s)
- **CPU profiling**: 15s (production: 30s)

**Why?**

- Tests wait 30s → guaranteed 2x poll cycles
- Reduces test time from 110s+ to ~50s per test
- Eliminates race conditions between polling and test timing

**Configuration:**

- See `fixtures/e2e-config-overlay.yaml` for E2E-specific settings
- Automatically applied during colony startup in docker-compose
- Does NOT affect production defaults

### Config Files

```
tests/e2e/distributed/fixtures/
├── e2e-config-overlay.yaml      # E2E-specific poll intervals (applied automatically)
├── colony-config-template.yaml  # Full config reference template
└── README.md                    # Detailed config documentation
```

## Running Tests

### Full E2E Suite

```bash
# Start services, run all tests, cleanup
make test-all

# Or manually:
make build   # Build container images
make up      # Start services
make test    # Run tests
make down    # Stop services
```

### Individual Test Suites

```bash
# Assuming services are already running (make up)

# Init tests (no docker-compose needed)
go test -v -run TestInitSuite

# Mesh tests
go test -v -run TestMeshSuite

# Service tests
go test -v -run TestServiceSuite

# Telemetry tests (Beyla, OTLP, system metrics)
go test -v -run TestTelemetrySuite

# Profiling tests
go test -v -run TestProfilingSuite

# Debug tests
go test -v -run TestDebugSuite
```

### Test Organization

```
init_test.go                  # Initialization tests (standalone)
init_standalone_test.go       # Standalone runner

suite.go                      # Base suite (docker-compose integration)
e2e_orchestrator_test.go      # Orchestrates all runtime tests

mesh_test.go                  # Mesh connectivity tests
mesh_standalone_test.go       # Standalone runner

service_test.go               # Service management tests
service_standalone_test.go    # Standalone runner

telemetry_test.go             # Observability tests
telemetry_standalone_test.go  # Standalone runner

profiling_test.go             # Profiling tests
profiling_standalone_test.go  # Standalone runner

debug_test.go                 # Debug probes tests
debug_standalone_test.go      # Standalone runner
```

## Writing New Tests

### For Init/Config Testing

Add tests to `init_test.go`:

```go
func (s *InitSuite) TestNewFeature() {
// Set isolated HOME for test
testHome := filepath.Join(s.tempDir, "home-test")
s.Require().NoError(os.MkdirAll(testHome, 0755))
s.T().Setenv("HOME", testHome)

// Run init
colonyID, err := helpers.RunCoralInit(s.ctx, "test-app", "e2e", filepath.Join(s.tempDir, "storage"))
s.Require().NoError(err)

// Validate generated config
loader, _ := config.NewLoader()
cfg, _ := loader.LoadColonyConfig(colonyID)

// Your assertions here
}
```

### For Runtime Testing

Add tests to appropriate suite (`mesh_test.go`, `service_test.go`, etc.):

```go
func (s *MeshSuite) TestNewFeature() {
// Use shared fixture
fixture := s.fixture

// Get endpoints
colonyEndpoint, err := fixture.GetColonyEndpoint(s.ctx)
s.Require().NoError(err)

// Your test logic here
}
```

## Debugging Tests

```bash
# Show service logs
make logs

# Show specific service logs
make logs-colony
make logs-agent-0

# Check service status
make status

# Run test with verbose output
go test -v -run TestSpecificTest

# Run test with trace logging
go test -v -run TestSpecificTest 2>&1 | grep component
```

## CI/CD Integration

```yaml
# Example GitHub Actions workflow
jobs:
    e2e-init:
        runs-on: ubuntu-latest
        steps:
            -   uses: actions/checkout@v4
            -   uses: actions/setup-go@v5
            -   name: Test Init
                run: go test -v ./tests/e2e/distributed -run TestInitSuite

    e2e-runtime:
        runs-on: ubuntu-latest
        steps:
            -   uses: actions/checkout@v4
            -   uses: actions/setup-go@v5
            -   name: Start E2E Infrastructure
                run: cd tests/e2e/distributed && make up
            -   name: Run E2E Tests
                run: cd tests/e2e/distributed && make test
            -   name: Cleanup
                if: always()
                run: cd tests/e2e/distributed && make down
```

## Troubleshooting

### Tests are flaky / timing out

- Check poll intervals in `fixtures/e2e-config-overlay.yaml`
- Verify wait times in tests are >= 2x poll interval
- Check docker-compose logs for errors

### Init tests fail

- Ensure `coral` binary is in PATH
- Check file permissions in temp directory
- Verify no leftover config from previous tests

### Services won't start

- Check if ports are already in use
- Verify Docker has enough resources
- Check docker-compose logs: `make logs`

### CA generation fails

- Check disk space
- Verify OpenSSL is available
- Check permissions on colony directory

## Best Practices

1. **Init tests**: Always use isolated temp directories and set HOME
2. **Runtime tests**: Use shared fixture, clean up in TearDownTest
3. **Wait times**: Use 2x poll interval minimum for reliable tests
4. **Assertions**: Use descriptive messages for better debugging
5. **Cleanup**: Always clean up resources in defer or TearDown
6. **Logging**: Use `s.T().Log()` for test progress visibility
