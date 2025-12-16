# E2E Discovery Tests

End-to-end tests for validating the function discovery flow (RFD 065).

## Overview

These tests validate the complete discovery fallback chain:
1. **SDK discovery** (Priority 1) - Using SDK HTTP API
2. **Binary DWARF scanning** (Priority 3) - Direct binary analysis
3. **Fallback behavior** - Automatic fallback when methods fail

## Test Scenarios

### 1. Discovery with SDK (`TestE2E_Discovery_WithSDK`)
- **Binary**: `app_with_sdk_dwarf` (SDK integrated + Full DWARF)
- **Build**: `go build -o app_with_sdk_dwarf`
- **Expected**: Discovery succeeds via SDK method
- **Validates**: SDK integration works correctly with full symbols

### 2. SDK Symbol Table Fallback (`TestE2E_Discovery_WithSDK_SymbolTableFallback`)
- **Binary**: `app_with_sdk_symtab_only` (SDK integrated + Symbol table only)
- **Build**: `go build -ldflags="-w" -o app_with_sdk_symtab_only`
- **Expected**: Discovery succeeds via SDK method (using symbol table fallback)
- **Validates**: SDK works with `-w` stripped binaries (DWARF stripped, symbols intact)

### 3. Binary Scanning with DWARF (`TestE2E_Discovery_BinaryScanning_WithDWARF`)
- **Binary**: `app_no_sdk_dwarf` (No SDK + Full DWARF)
- **Build**: `go build -o app_no_sdk_dwarf`
- **Expected**: Discovery succeeds via binary scanning
- **Validates**: DWARF parsing works without SDK (agentless mode)

### 4. Agentless Symbol Table Fallback (`TestE2E_Discovery_BinaryScanning_SymbolTableFallback`)
- **Binary**: `app_no_sdk_symtab_only` (No SDK + Symbol table only)
- **Build**: `go build -ldflags="-w" -o app_no_sdk_symtab_only`
- **Expected**: Discovery succeeds via binary scanning (using symbol table fallback)
- **Validates**: Agentless mode works with `-w` stripped binaries (production use case)

### 5. Fully Stripped Binary Failure (`TestE2E_Discovery_BinaryScanning_Stripped`)
- **Binary**: `app_no_sdk_stripped` (No SDK + Fully stripped)
- **Build**: `go build -ldflags="-w -s" -o app_no_sdk_stripped`
- **Expected**: Discovery fails with helpful error message
- **Validates**: Graceful failure when both DWARF and symbol table are removed

### 6. Fallback from SDK to Binary (`TestE2E_Discovery_Fallback`)
- **Binary**: `app_no_sdk_dwarf` (No SDK + DWARF symbols)
- **Expected**: SDK fails, automatically falls back to binary scanning
- **Validates**: Fallback chain works as designed

## Running the Tests

### Prerequisites
- Linux system (eBPF requirements)
- Go 1.21+

### Quick Start

```bash
# Build test applications
cd tests/e2e/discovery
go generate

# Run all E2E tests
go test -v

# Run specific test
go test -v -run TestE2E_Discovery_WithSDK

# Skip E2E tests (for fast iteration)
go test -short
```

### Build Test Applications Manually

```bash
cd testdata
./build.sh
```

This creates 6 test binaries:
- `app_with_sdk_dwarf` - SDK + Full DWARF
- `app_with_sdk_symtab_only` - SDK + Symbol table only (`-w`)
- `app_with_sdk_stripped` - SDK + Fully stripped (`-w -s`)
- `app_no_sdk_dwarf` - No SDK + Full DWARF
- `app_no_sdk_symtab_only` - No SDK + Symbol table only (`-w`)
- `app_no_sdk_stripped` - No SDK + Fully stripped (`-w -s`)

## Test Architecture

### Test Apps
Simple Go applications that:
- Expose HTTP health endpoint
- Contain target functions for tracing (`main.TargetFunction`)
- Optionally integrate Coral SDK debug server
- Write PID to file for test coordination

### Discovery Service
Tests create a real `DiscoveryService` with configurable:
- SDK discovery (enabled/disabled)
- Binary scanning (enabled/disabled)
- Cache settings
- Access method (direct, nsenter, cri)

### Test Flow
1. Start test application as subprocess
2. Wait for app to be ready (PID file written)
3. Create discovery service with test configuration
4. Attempt to discover target function
5. Verify correct discovery method was used
6. Verify function metadata is accurate
7. Clean up (stop app, close discovery service)

## Troubleshooting

### Tests skipped on macOS
**Issue**: `/proc not available (not on Linux)`
**Solution**: E2E tests require Linux for eBPF. Run in Docker or VM.

### Test binaries not found
**Issue**: `Test binary not found: testdata/bin/app_*`
**Solution**: Run `go generate` to build test applications.

### Permission denied errors
**Issue**: Binary scanning requires certain permissions
**Solution**:
- For `direct` access: Ensure shared PID namespace
- For `nsenter` access: Run with `CAP_SYS_ADMIN`

## Future Enhancements

- [ ] Add Docker Compose setup for isolated testing
- [ ] Add testcontainers for parallel test execution
- [ ] Test pprof discovery (Priority 2) when implemented
- [ ] Test CRI access method when implemented
- [ ] Add performance benchmarks for discovery methods
