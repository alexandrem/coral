# Coral Mesh E2E Test Suite

End-to-end tests for the Coral Mesh distributed debugging platform using [testify/suite](https://github.com/stretchr/testify).

## Quick Start

```bash
# Build and run E2E tests
make test-e2e

# Run only unit tests
make test-unit

# Run everything
make test-all
```

## Directory Structure

```
tests/
├── e2e/                    # E2E test suites
│   └── discovery_test.go   # Discovery service tests (✅ WORKING)
├── helpers/                # Test utilities
│   ├── suite.go           # Base test suite with port management
│   ├── process.go         # Process lifecycle management
│   ├── config.go          # Configuration builders
│   └── database.go        # Database test helpers
└── fixtures/              # Test fixtures (empty for now)
```

## Current Test Coverage

### ✅ Discovery Service (`discovery_test.go`)

**Status**: Fully functional and passing

The Discovery service is production-ready and fully tested. Tests cover:

1. **Service Startup** - Binary starts and listens on port
2. **Health Check** - Service health endpoint returns status
3. **Colony Registration** - Colonies can register with mesh metadata
4. **Colony Lookup** - Registered colonies can be discovered
5. **Agent Registration** - Agents can register under a colony
6. **Agent Lookup** - Registered agents can be discovered
7. **Multiple Colonies** - Concurrent colony registrations
8. **Colony Updates** - Updating existing registrations
9. **Relay Requests** - Requesting NAT traversal relays
10. **TTL Expiration** - Registrations expire after configured TTL

**Run discovery tests:**
```bash
go test -v ./tests/e2e -run TestDiscoveryE2E
```

**Individual test:**
```bash
go test -v ./tests/e2e -run TestDiscoveryE2E/TestColonyRegistration
```

## Test Infrastructure

### Base Suite (`helpers/suite.go`)

Provides common test functionality:

- **Port Management**: `GetFreePort()` - Allocates available ports
- **Wait Utilities**: `WaitForPort()` - Waits for services to start
- **Eventually Assertions**: `Eventually()` - Retries conditions
- **Temp Directories**: Automatic cleanup
- **Context Management**: Timeout handling per test

### Process Manager (`helpers/process.go`)

Manages test service lifecycles:

- Start/stop binaries with arguments
- Capture stdout/stderr for debugging
- Graceful shutdown with timeouts
- Context-aware termination

### Config Builder (`helpers/config.go`)

Generates test configurations (for future use):

- Colony configurations
- Agent configurations
- Discovery configurations

### Database Helper (`helpers/database.go`)

DuckDB test utilities (for future use):

- Database creation
- Query execution
- Table operations

## Writing New Tests

### Example Test

```go
func (s *DiscoveryE2ESuite) TestNewFeature() {
    // 1. Get a free port
    port := s.GetFreePort()

    // 2. Start service
    s.procMgr.Start(
        s.Ctx,
        "discovery",
        "./bin/coral-discovery",
        "--port", fmt.Sprintf("%d", port),
    )

    // 3. Wait for service
    s.Require().True(
        s.WaitForPort("127.0.0.1", port, 30*time.Second),
    )

    // 4. Create client and test
    client := discoveryv1connect.NewDiscoveryServiceClient(
        nil,
        fmt.Sprintf("http://127.0.0.1:%d", port),
    )

    // 5. Make assertions
    resp, err := client.SomeMethod(s.Ctx, connect.NewRequest(&req))
    s.Require().NoError(err)
    s.Equal("expected", resp.Msg.Field)
}
```

## Development Roadmap

### Phase 1: Foundation (✅ Complete)
- [x] E2E test infrastructure with testify suites
- [x] Process management for services
- [x] Port allocation and waiting
- [x] Discovery service full E2E coverage

### Phase 2: Colony Tests (Planned)
- [ ] Colony service startup tests
- [ ] Colony status endpoint tests
- [ ] Agent registry tests
- [ ] gRPC communication tests

### Phase 3: Integration Tests (Planned)
- [ ] Discovery → Colony integration
- [ ] Colony → Agent communication
- [ ] Agent heartbeat mechanism
- [ ] Telemetry collection flow

### Phase 4: Advanced Tests (Future)
- [ ] WireGuard mesh connectivity
- [ ] NAT traversal (STUN/relay)
- [ ] OpenTelemetry ingestion
- [ ] eBPF data collection
- [ ] Multi-agent scenarios

## Test Guidelines

### When to Add E2E Tests

Add E2E tests when:
- A new service becomes functional
- New RPC endpoints are implemented
- Critical user workflows are complete
- Integration points between services work

### When NOT to Add E2E Tests

Don't add E2E tests for:
- Incomplete/stub implementations
- Internal implementation details (use unit tests)
- Features that depend on unimplemented dependencies

### Best Practices

1. **Test Real Behavior**: Start actual binaries, don't mock
2. **Isolate Tests**: Each test should be independent
3. **Clean Up**: Use defer or TearDown for cleanup
4. **Clear Names**: `TestFeatureName_Scenario`
5. **Log Progress**: Use `s.T().Log()` for debugging
6. **Handle Async**: Use `Eventually()` or `WaitForPort()`
7. **Skip Gracefully**: Use `s.T().Skip()` for unimplemented features

## Debugging

### View Test Output

```bash
# Verbose output
go test -v ./tests/e2e/...

# Force re-run (no cache)
go test -v -count=1 ./tests/e2e/...

# With coverage
go test -v -coverprofile=coverage.out ./tests/e2e/...
```

### Check Process Output

Test helpers capture stdout/stderr from services:

```go
proc := s.procMgr.Get("discovery")
stdout := proc.StdoutLines()  // Get captured stdout
stderr := proc.StderrLines()  // Get captured stderr
```

### Common Issues

**Port Already in Use**
- Tests use `GetFreePort()` to avoid conflicts
- If issues persist, check for stale processes: `ps aux | grep coral`

**Service Fails to Start**
- Check if binary exists: `ls -la bin/`
- Run `make build` first
- Check service logs via process output

**Test Timeouts**
- Increase timeout: `go test -timeout 20m ./tests/e2e/...`
- Default test timeout is 5 minutes per test
- Service startup timeout is 30 seconds

**Binary Not Found**
- Run `make build` before `make test-e2e`
- Or use `make test-e2e` which builds automatically

## CI/CD Integration

Tests are designed for automated testing:

```yaml
# Example GitHub Actions
- name: Build
  run: make build

- name: Run E2E Tests
  run: make test-e2e
  timeout-minutes: 15
```

## Implementation Notes

### Why Start with Discovery?

The Discovery service was chosen as the first E2E test because:

1. **✅ Fully Implemented** - Production-ready code
2. **✅ Well Tested** - Strong unit test coverage
3. **✅ No Dependencies** - Standalone service
4. **✅ Simple API** - HTTP/2 Connect-based RPCs
5. **✅ Fast Tests** - Sub-second execution

This provides a solid foundation and template for future E2E tests.

### Connect vs gRPC

Coral uses **Buf Connect** (HTTP/2) instead of vanilla gRPC:

```go
// Using Connect client
client := discoveryv1connect.NewDiscoveryServiceClient(
    nil,  // Uses default HTTP client
    fmt.Sprintf("http://127.0.0.1:%d", port),
)

resp, err := client.RegisterColony(
    ctx,
    connect.NewRequest(&discoveryv1.RegisterColonyRequest{...}),
)
```

### Proto Definitions

- Located in: `/proto/coral/*/`
- Generated code: `/coral/*/` (committed to repo)
- Import path: `github.com/coral-io/coral/coral/discovery/v1`

## Resources

- [Testify Suite Documentation](https://github.com/stretchr/testify#suite-package)
- [Buf Connect Documentation](https://connectrpc.com/docs/go/getting-started)
- [Project Architecture](../ARCHITECTURE.MD)
- [Implementation Guide](../docs/IMPLEMENTATION.md)

## Contributing

When adding new E2E tests:

1. Ensure the feature is actually implemented and working
2. Follow existing test patterns in `discovery_test.go`
3. Use the test helpers in `tests/helpers/`
4. Add test documentation to this README
5. Verify all tests pass: `make test-all`

## Questions?

- Check test output for detailed error messages
- Review existing test patterns
- Consult architecture docs
- Check RFDs for feature specifications
