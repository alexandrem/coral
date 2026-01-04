# How to Run E2E Tests

## Quick Reference

```bash
# From project root - automated full suite
make test-e2e

# From tests/e2e/distributed - automated full suite
make test-all

# Manual workflow (from tests/e2e/distributed)
make up          # Start services
make test        # Run tests (can run multiple times)
make down        # Stop services
```

## From Project Root

### Option 1: Full Automated Run (Recommended)

```bash
# Build, start services, run tests, cleanup
make test-e2e
```

This will:
1. Run `generate` to ensure code is generated
2. Check you're on Linux (required for eBPF/WireGuard)
3. Build all container images with BuildKit
4. Start all services via docker-compose
5. Run all E2E tests
6. Stop and cleanup services

**Time**: ~15-20 minutes first run, ~6-11 minutes with cache

### Option 2: Manual Control

```bash
# Start services
make test-e2e-up

# In another terminal: view logs
make test-e2e-logs

# Run tests (from project root)
cd tests/e2e/distributed && go test -v -timeout 30m

# Stop services when done
make test-e2e-down
```

## From tests/e2e/distributed Directory

### Option 1: Full Automated Run (Recommended)

```bash
cd tests/e2e/distributed

# Build, start services, run tests, cleanup
make test-all
```

### Option 2: Manual Development Workflow

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
go test -v -run TestBeylaPassiveInstrumentation

# 5. View logs while tests run (in another terminal)
make logs              # All services
make logs-colony       # Just colony
make logs-agent-0      # Just agent-0

# 6. Check service status
make status

# 7. Stop services when done
make down

# 8. Clean everything (including volumes)
make clean
```

## Prerequisites

### Required

1. **Linux OS** - eBPF and WireGuard require Linux kernel
   - Native Linux, or
   - Linux VM (Colima, Multipass, etc.)

2. **Docker** - With BuildKit enabled
   ```bash
   # Add to ~/.zshrc or ~/.bashrc
   export DOCKER_BUILDKIT=1
   ```

3. **Docker Compose** - Usually included with Docker Desktop/Colima

### Recommended

- **4 CPU cores** minimum
- **8GB RAM** minimum (more is better)
- **10GB free disk** for images and build cache

### For Colima Users

```bash
# Start Colima with appropriate resources
colima start --cpu 4 --memory 8 --disk 50

# Verify
colima status
```

## Test Suite Structure

### Available Test Suites

Run specific suites by name:

```bash
# Mesh connectivity (7 tests)
go test -v -run TestMeshSuite

# Service management (4 tests)
go test -v -run TestServiceSuite

# Telemetry - Beyla, OTLP, metrics (8 tests)
go test -v -run TestTelemetrySuite

# CPU profiling (2 tests)
go test -v -run TestProfilingSuite

# Debug introspection (3 skipped tests)
go test -v -run TestDebugSuite

# Full orchestration (4 groups)
go test -v -run TestE2EOrchestrator
```

### Run Specific Tests

```bash
# Single test
go test -v -run TestOTLPIngestion

# Pattern matching
go test -v -run ".*Beyla.*"
go test -v -run ".*OTLP.*"
```

## Service Endpoints

When services are running (`make up` or `make test-e2e-up`):

| Service | Endpoint | Purpose |
|---------|----------|---------|
| Discovery | http://localhost:8080 | Health: http://localhost:8080/health |
| Colony | localhost:9000 | gRPC endpoint |
| Agent-0 | localhost:9001 | gRPC endpoint |
| Agent-1 | localhost:9002 | gRPC endpoint |
| CPU App | http://localhost:8081 | Test app: http://localhost:8081/health |
| OTEL App | http://localhost:8082 | Test app: http://localhost:8082/health |
| SDK App | http://localhost:3001 | Test app: http://localhost:3001/health |

## Troubleshooting

### Services won't start

```bash
# Check what's running
docker ps

# Check logs
cd tests/e2e/distributed
make logs

# Clean and restart
make clean
make build
make up
```

### Tests fail with "services not ready"

```bash
# Make sure services are running
make status

# Or restart them
make down
make up
```

### BuildKit errors

```bash
# Verify BuildKit is enabled
echo $DOCKER_BUILDKIT  # Should show "1"

# If not, enable it
export DOCKER_BUILDKIT=1

# Rebuild
make build
```

### Out of memory

```bash
# If using Colima, increase memory
colima stop
colima start --cpu 4 --memory 10  # Increase to 10GB

# Restart services
make down
make up
```

### Tests hang or timeout

```bash
# Increase timeout
go test -v -timeout 60m

# Check if services are responsive
curl http://localhost:8080/health  # Discovery
curl http://localhost:8081/health  # CPU app
curl http://localhost:8082/health  # OTEL app
```

## Advanced Usage

### Run tests with verbose output

```bash
go test -v -timeout 30m ./...
```

### Run tests in short mode (skip some)

```bash
go test -short ./...
```

### Run with race detector

```bash
go test -race -timeout 60m ./...
```

### Keep services running between test runs

```bash
# Start once
make up

# Run tests multiple times
make test
make test
make test

# Stop when done
make down
```

### Debug a failing test

```bash
# Start services
make up

# In another terminal, watch logs
make logs-colony

# In another terminal, run specific test
go test -v -run TestOTLPIngestion

# Check logs for errors
make logs
```

## Performance Tips

1. **Use BuildKit** - 10x faster builds after first run
   ```bash
   export DOCKER_BUILDKIT=1
   ```

2. **Keep services running** - Avoid restart overhead
   ```bash
   make up    # Once
   make test  # Many times
   ```

3. **Use cache** - Don't rebuild if code hasn't changed
   ```bash
   # Only rebuild when needed
   make build  # After code changes
   ```

4. **Run specific tests** - Don't run full suite if testing one thing
   ```bash
   go test -v -run TestOTLPIngestion  # Fast
   make test-all                       # Slow
   ```

## CI Integration

For CI pipelines:

```bash
# Full automated run (recommended for CI)
make test-e2e
```

This ensures:
- ✅ Clean build every time
- ✅ Services start fresh
- ✅ Tests run
- ✅ Cleanup happens (even on failure)

## What's Different from Testcontainers

**Before (testcontainers):**
```bash
# Just run tests - containers created/destroyed per suite
go test -v -timeout 30m ./...
```

**Now (docker-compose):**
```bash
# Start services, run tests, stop services
make test-all

# Or manually
make up && make test && make down
```

**Why?** 3-5x faster, better resource usage, easier debugging!
