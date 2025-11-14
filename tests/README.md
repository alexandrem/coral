# Coral Mesh E2E Test Suite

This directory contains the end-to-end (E2E) test suite for the Coral Mesh project. The tests are built using [testify/suite](https://github.com/stretchr/testify) and validate critical system components and their interactions.

## Directory Structure

```
tests/
├── e2e/                    # E2E test suites
│   ├── discovery_test.go   # Discovery service E2E tests
│   ├── colony_agent_test.go # Colony-Agent communication tests
│   └── duckdb_test.go      # DuckDB data collection tests
├── helpers/                # Test utilities and helpers
│   ├── suite.go           # Base E2E test suite
│   ├── process.go         # Process management utilities
│   ├── config.go          # Configuration builders
│   └── database.go        # Database test helpers
├── fixtures/              # Test fixtures and data
└── README.md             # This file
```

## Running Tests

### Prerequisites

Before running E2E tests, ensure you have:

1. Built the project binaries:
   ```bash
   make build
   ```

2. Go 1.21+ installed
3. DuckDB dependencies available

### Running E2E Tests

```bash
# Run all E2E tests
make test-e2e

# Run E2E tests with verbose output
make test-e2e-verbose

# Run all tests (unit + E2E)
make test-all

# Run only unit tests
make test-unit
```

### Running Individual Test Suites

```bash
# Run only discovery tests
go test -v ./tests/e2e -run TestDiscoveryE2E

# Run only colony-agent tests
go test -v ./tests/e2e -run TestColonyAgentE2E

# Run only DuckDB tests
go test -v ./tests/e2e -run TestDuckDBE2E
```

### Running Individual Tests

```bash
# Run a specific test
go test -v ./tests/e2e -run TestDiscoveryE2E/TestPeerRegistration

# Run with timeout
go test -v -timeout 15m ./tests/e2e -run TestDuckDBE2E
```

### Skip E2E Tests

E2E tests are automatically skipped when using the `-short` flag:

```bash
go test -short ./...
```

## Test Suites

### 1. Discovery Service E2E Tests (`discovery_test.go`)

Tests the discovery service functionality including:

- **Service Startup**: Validates discovery service starts successfully
- **Peer Registration**: Tests peer registration and discovery
- **Heartbeat Mechanism**: Validates peer heartbeat and TTL expiration
- **Multiple Peers**: Tests handling of multiple concurrent peers
- **STUN Discovery**: Tests STUN-based endpoint discovery (pending implementation)

**Key Test Cases:**
- `TestDiscoveryServiceStartup`
- `TestPeerRegistration`
- `TestPeerHeartbeat`
- `TestMultiplePeers`

### 2. Colony-Agent Communication Tests (`colony_agent_test.go`)

Tests communication between colony and agents:

- **Colony Startup**: Validates colony service starts successfully
- **Agent Registration**: Tests agent registration with colony
- **Status Queries**: Tests colony status and agent listing
- **gRPC Communication**: Validates concurrent client connections
- **Agent Lifecycle**: Tests agent connection, disconnection, and reconnection (pending)
- **Metrics Collection**: Tests agent metrics reporting (pending)

**Key Test Cases:**
- `TestColonyStartup`
- `TestAgentRegistration`
- `TestColonyStatus`
- `TestColonyAgentGRPCCommunication`

### 3. DuckDB Data Collection Tests (`duckdb_test.go`)

Tests DuckDB data storage and retrieval:

- **Database Initialization**: Tests DuckDB initialization and schema creation
- **Data Ingestion**: Validates data insertion and querying
- **Time-Series Data**: Tests time-series data storage and aggregation
- **Data Retention**: Tests retention policies and data cleanup
- **JSON Storage**: Tests JSON data storage and querying
- **Data Export**: Tests exporting data to CSV and Parquet formats
- **OTEL Integration**: Tests OpenTelemetry data ingestion (pending)

**Key Test Cases:**
- `TestDuckDBInitialization`
- `TestDataIngestion`
- `TestTimeSeriesData`
- `TestDataRetention`
- `TestJSONDataStorage`
- `TestDataExport`

## Test Helpers

The `helpers/` directory provides reusable utilities for E2E tests:

### E2ETestSuite (`suite.go`)

Base test suite providing:
- Automatic setup/teardown
- Temporary directory management
- Context with timeout
- Port allocation utilities
- Eventually assertions for async operations

**Example Usage:**
```go
type MyE2ESuite struct {
    helpers.E2ETestSuite
}

func (s *MyE2ESuite) TestSomething() {
    port := s.GetFreePort()
    s.WaitForPort("127.0.0.1", port, 30*time.Second)
    s.Eventually(condition, timeout, tick, "message")
}
```

### ProcessManager (`process.go`)

Manages test processes with:
- Process lifecycle management
- Output capturing
- Graceful shutdown
- Context-aware termination

**Example Usage:**
```go
procMgr := helpers.NewProcessManager(s.T())
proc := procMgr.Start(ctx, "colony", "./bin/coral", "colony", "start")
defer procMgr.StopAll(10 * time.Second)
```

### ConfigBuilder (`config.go`)

Generates test configurations for:
- Colony configurations
- Agent configurations
- Discovery service configurations

**Example Usage:**
```go
configBuilder := helpers.NewConfigBuilder(s.T(), tempDir)
configPath := configBuilder.WriteColonyConfig("colony1", apiPort, grpcPort)
```

### DatabaseHelper (`database.go`)

DuckDB test utilities:
- Database creation and management
- Query execution
- Table operations
- Row counting

**Example Usage:**
```go
dbHelper := helpers.NewDatabaseHelper(s.T(), tempDir)
db := dbHelper.CreateDB("test-db")
dbHelper.Exec(db, "CREATE TABLE test (id INTEGER)")
count := dbHelper.CountRows(db, "test")
```

## Writing New E2E Tests

### 1. Create a New Test Suite

```go
package e2e

import (
    "testing"
    "github.com/coral-io/coral/tests/helpers"
    "github.com/stretchr/testify/suite"
)

type MyE2ESuite struct {
    helpers.E2ETestSuite
    procMgr *helpers.ProcessManager
}

func TestMyE2E(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping E2E tests in short mode")
    }
    suite.Run(t, new(MyE2ESuite))
}

func (s *MyE2ESuite) SetupSuite() {
    s.E2ETestSuite.SetupSuite()
    s.procMgr = helpers.NewProcessManager(s.T())
}

func (s *MyE2ESuite) TearDownSuite() {
    s.procMgr.StopAll(10 * time.Second)
    s.E2ETestSuite.TearDownSuite()
}
```

### 2. Add Test Cases

```go
func (s *MyE2ESuite) TestMyFeature() {
    // Arrange
    port := s.GetFreePort()

    // Act
    proc := s.procMgr.Start(s.Ctx, "service", "./bin/service", "--port", fmt.Sprint(port))

    // Assert
    s.Require().True(s.WaitForPort("127.0.0.1", port, 30*time.Second))
    s.T().Log("Test passed!")
}
```

### 3. Best Practices

- **Use descriptive test names**: `TestFeatureName_Scenario`
- **Clean up resources**: Use defer or TearDown methods
- **Set appropriate timeouts**: Default is 5 minutes per test
- **Log progress**: Use `s.T().Log()` for debugging
- **Handle async operations**: Use `Eventually` or `WaitForPort`
- **Skip unimplemented tests**: Use `s.T().Skip()` with a TODO comment

## Debugging E2E Tests

### View Test Output

```bash
# Verbose output
go test -v ./tests/e2e/...

# Even more verbose
go test -v -count=1 ./tests/e2e/...
```

### Test Artifacts

E2E tests create temporary directories for test artifacts:
- Configuration files
- Database files
- Logs (captured from processes)

These are automatically cleaned up after tests complete.

### Common Issues

**Issue**: Tests timeout
- **Solution**: Increase timeout with `-timeout` flag: `go test -timeout 20m ./tests/e2e/...`

**Issue**: Port already in use
- **Solution**: Tests use `GetFreePort()` which should avoid conflicts. If issues persist, ensure no services are running on common ports.

**Issue**: Binary not found
- **Solution**: Run `make build` before E2E tests or use `make test-e2e` which builds automatically.

**Issue**: Database locked
- **Solution**: Ensure previous test runs completed properly. Clean temp directories: `rm -rf /tmp/coral-e2e-*`

## CI/CD Integration

E2E tests are designed for CI/CD integration:

```yaml
# Example GitHub Actions workflow
- name: Run E2E Tests
  run: |
    make build
    make test-e2e
  timeout-minutes: 15
```

## Test Coverage

Track E2E test coverage with:

```bash
go test -v -coverprofile=coverage-e2e.out ./tests/e2e/...
go tool cover -html=coverage-e2e.out
```

## Future Test Coverage

Planned E2E tests for upcoming features:

- [ ] WireGuard mesh connectivity E2E tests
- [ ] OpenTelemetry ingestion E2E tests
- [ ] eBPF data collection E2E tests
- [ ] LLM integration E2E tests
- [ ] MCP server/client E2E tests
- [ ] Multi-agent mesh communication tests
- [ ] Failover and recovery tests
- [ ] Performance and load tests

## Contributing

When adding new features:

1. **Write E2E tests first** (TDD approach)
2. **Update existing tests** if behavior changes
3. **Add test documentation** in this README
4. **Ensure all tests pass**: `make test-all`
5. **Follow existing patterns** in test structure

## Resources

- [testify documentation](https://github.com/stretchr/testify)
- [DuckDB Go driver](https://github.com/marcboeker/go-duckdb)
- [gRPC testing guide](https://grpc.io/docs/languages/go/basics/)
- Project documentation in `docs/`

## Support

For questions or issues with E2E tests:
1. Check test output and logs
2. Review this documentation
3. Check existing test patterns
4. Consult `docs/IMPLEMENTATION.md` for architecture details
