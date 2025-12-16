# Cross-Platform Support

## Summary

✅ **macOS**: Builds test apps, compiles tests, skips execution gracefully
✅ **Linux**: Builds test apps, compiles tests, runs full E2E validation

## What Works Where

### macOS (Development)

**✓ What works:**
- `go generate` - Builds all 4 test application variants
- Test compilation - All tests compile successfully
- Test execution - Tests skip with clear messages
- Quick validation - Verify code compiles and builds

**✗ What doesn't work:**
- Actual E2E test execution (requires Linux `/proc` and eBPF)

**Developer experience:**
```bash
# On macOS - everything compiles, tests skip gracefully
$ cd tests/e2e/discovery
$ go generate
Building test applications...
✓ All test binaries built successfully

$ go test -v
=== RUN   TestE2E_Discovery_WithSDK
    discovery_test.go:36: Skipping E2E test: /proc not available (not on Linux)
--- SKIP: TestE2E_Discovery_WithSDK (0.00s)
...
PASS
ok      github.com/coral-mesh/coral/tests/e2e/discovery    0.252s
```

### Linux (CI/Production)

**✓ What works:**
- Everything that works on macOS, plus:
- Full E2E test execution
- Real binary scanning with `/proc/<pid>/exe`
- eBPF capabilities testing
- Actual uprobe attachment (when run with proper permissions)

**Developer experience:**
```bash
# On Linux - full E2E validation
$ cd tests/e2e/discovery
$ go generate
Building test applications...
✓ All test binaries built successfully

$ go test -v
=== RUN   TestE2E_Discovery_WithSDK
    discovery_test.go:70: Test app started with PID: 12345
    discovery_test.go:113: ✓ Successfully discovered function via SDK
--- PASS: TestE2E_Discovery_WithSDK (2.34s)
...
PASS
ok      github.com/coral-mesh/coral/tests/e2e/discovery    8.456s
```

## CI/CD Integration

### GitHub Actions Example

```yaml
test-e2e:
  runs-on: ubuntu-latest  # Linux for E2E tests
  steps:
    - uses: actions/checkout@v3
    - uses: actions/setup-go@v4

    # Build test apps
    - name: Generate test binaries
      run: cd tests/e2e/discovery && go generate

    # Run E2E tests (will execute on Linux)
    - name: Run E2E tests
      run: go test -v ./tests/e2e/discovery/...
```

### Local Development

```yaml
test-unit:
  runs-on: ${{ matrix.os }}
  strategy:
    matrix:
      os: [ubuntu-latest, macos-latest]
  steps:
    # Tests compile on both, but E2E tests skip on macOS
    - name: Run all tests
      run: make test
```

## Build Output Comparison

### macOS Build
```
$ ls -lh testdata/bin/
-rwxr-xr-x  7.4M  app_no_sdk_dwarf
-rwxr-xr-x  5.1M  app_no_sdk_stripped
-rwxr-xr-x  8.3M  app_with_sdk_dwarf
-rwxr-xr-x  5.7M  app_with_sdk_stripped
```

### Linux Build
```
$ ls -lh testdata/bin/
-rwxr-xr-x  7.8M  app_no_sdk_dwarf
-rwxr-xr-x  5.3M  app_no_sdk_stripped
-rwxr-xr-x  8.7M  app_with_sdk_dwarf
-rwxr-xr-x  5.9M  app_with_sdk_stripped
```

*Binary sizes differ slightly due to platform differences, but functionality is identical.*

## Running Tests on macOS via Docker

For local macOS development, you can run E2E tests in Linux using Docker:

```bash
# Quick one-liner
docker run --rm -v $(pwd):/workspace -w /workspace/tests/e2e/discovery \
  golang:1.23 bash -c "go generate && go test -v"

# Or use docker-compose (if we add it later)
docker-compose run --rm e2e-tests
```

## Platform Detection

Tests automatically detect the platform:

```go
// Automatic skip on non-Linux platforms
if _, err := os.Stat("/proc"); os.IsNotExist(err) {
    t.Skip("Skipping E2E test: /proc not available (not on Linux)")
}
```

No manual configuration needed - tests "just work" on both platforms.

## Recommended Workflow

**On macOS (development):**
1. `go generate` - Build test apps
2. `go test` - Verify compilation (tests skip)
3. `git push` - CI runs full E2E on Linux

**On Linux (CI/servers):**
1. `go generate` - Build test apps
2. `go test` - Full E2E validation
3. Deploy with confidence

## Future: Multi-Platform E2E

If we need macOS E2E tests in the future, we could:
- Mock `/proc` filesystem
- Use platform-specific test builds
- Add dtrace-based discovery for macOS

For now, the Linux-only E2E approach is sufficient since:
- eBPF is Linux-only anyway
- Binary scanning targets Linux deployments
- macOS users can validate compilation
