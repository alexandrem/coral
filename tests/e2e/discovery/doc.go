// Package discovery provides end-to-end tests for RFD 065 - Agentless Binary Scanning.
//
// # Overview
//
// This package tests the complete function discovery flow with real binaries and processes.
// Tests validate the priority fallback chain: SDK → pprof → Binary DWARF scanning.
//
// # Quick Start
//
//	# Build test applications
//	go generate
//
//	# Run E2E tests (Linux only, requires /proc)
//	go test -v
//
//	# Skip E2E tests
//	go test -short
//
// # Test Scenarios
//
// 1. SDK Discovery - Test with app_with_sdk_dwarf
//   - Validates SDK HTTP API integration
//   - Expected: Discovery via SDK method
//
// 2. Binary DWARF Scanning - Test with app_no_sdk_dwarf
//   - Validates direct binary analysis without SDK
//   - Expected: Discovery via binary scanning
//
// 3. Stripped Binary Failure - Test with app_no_sdk_stripped
//   - Validates graceful failure with helpful error
//   - Expected: Failure with actionable recommendations
//
// 4. Fallback Chain - Test with app_no_sdk_dwarf + wrong SDK addr
//   - Validates automatic fallback from SDK to binary scanning
//   - Expected: Fallback to binary scanning succeeds
//
// # Test Architecture
//
// Tests use a simple approach with subprocess execution:
//  1. Build test apps with go:generate (build.sh)
//  2. Start app as subprocess
//  3. Wait for app to write PID file
//  4. Create DiscoveryService with test config
//  5. Attempt function discovery
//  6. Verify correct method was used
//  7. Clean up (stop app, close service)
//
// # Requirements
//
//   - Linux (eBPF and /proc filesystem)
//   - Go 1.21+
//   - Test binaries built (go generate)
//
// # Troubleshooting
//
// Tests skipped: Run on Linux or Docker
// Binaries not found: Run 'go generate'
// Permission errors: Check eBPF capabilities
//
// For more details, see README.md
package discovery
