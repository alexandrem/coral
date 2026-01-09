# E2E Test Fixtures

This directory contains configuration files and fixtures for E2E tests.

## Test Applications

The `apps/` directory contains test applications used across E2E test suites:

### `cpu-app`

A CPU-intensive test application for profiling tests. Simple HTTP server that
performs SHA-256 hashing on each request to generate CPU load.

- **Port**: 8080
- **Endpoints**:
  - `GET /` - CPU-intensive workload (10k SHA-256 iterations)
  - `GET /health` - Health check
- **Used by**: Profiling tests (continuous and on-demand)

### `otel-app`

OpenTelemetry instrumented application that emits traces and metrics via OTLP.
Tests the combination of Beyla (passive eBPF instrumentation) and OTLP SDK
(active instrumentation).

- **Port**: 8090
- **Endpoints**: Various API endpoints with trace/metric instrumentation
- **Used by**: Telemetry tests

### `sdk-app`

Application with Coral SDK integration for uprobe testing and SDK functionality.

- **Port**: 3001
- **Used by**: SDK integration tests

## Colony Configuration

### `colony-config-template.yaml`

A complete colony configuration template that can be used as a reference for E2E
test colonies. This file documents the full structure of a colony config with
e2e-optimized settings.

### `e2e-config-overlay.yaml`

E2E-specific configuration overlay that is automatically applied to generated
colony configs in docker-compose. This file contains optimizations for faster,
more reliable E2E testing:

- **Faster poll intervals**: All pollers (Beyla, system metrics, continuous
  profiling) use 15-second intervals instead of production defaults (30-60
  seconds)
- **Standard retention**: Maintains production-like retention settings for
  testing data lifecycle

#### Why Faster Poll Intervals?

E2E tests need to verify that data flows correctly through the system within a
reasonable test execution time. Using production poll intervals (60 seconds)
would make tests:

1. **Unreliable**: Tests might miss poll cycles due to timing
2. **Slow**: Waiting 60+ seconds per test significantly increases total test
   time
3. **Flaky**: Race conditions between test timing and poll timing

With 15-second intervals, tests can:

- Reliably wait 30 seconds and guarantee at least one poll cycle
- Complete faster (50s total vs 110s+ total per test)
- Avoid race conditions with deterministic timing

## Usage in Docker Compose

The `e2e-config-overlay.yaml` file is mounted read-only in the colony container
and automatically appended to the generated colony config during startup. A
marker file (`.e2e_applied`) ensures the overlay is only applied once.

## Modifying E2E Configuration

To change E2E-specific settings:

1. Edit `e2e-config-overlay.yaml`
2. Rebuild the colony container: `make down && make up`
3. The new settings will be applied on next startup

Do NOT modify these files for production - they are optimized for fast E2E
testing only.

## Manual Validation Scripts

The `../scripts/` directory contains shell scripts for manual testing and
validation of the E2E infrastructure:

- **test_cpu_profile.sh**: Test on-demand CPU profiling (RFD 070)
- **test_continuous_profiling.sh**: Test continuous CPU profiling (RFD 072)
- **test_agent_endpoint.sh**: Verify agent's CPU profile endpoint
- **generate_load.sh**: Generate HTTP load on test applications

These scripts work with the same docker-compose setup as automated tests,
allowing manual validation and debugging of E2E infrastructure.
