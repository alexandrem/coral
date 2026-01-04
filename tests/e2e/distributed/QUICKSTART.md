# E2E Tests - Quick Start

## TL;DR

```bash
cd tests/e2e/distributed

# Enable BuildKit for fast builds (one-time setup)
export DOCKER_BUILDKIT=1

# Run everything (build, test, cleanup)
make test-all
```

## What You Need

1. **Linux** - Required for eBPF and WireGuard
2. **Docker** - With BuildKit enabled
3. **Go 1.25+** - For running tests

## First Time Setup

### 1. Enable BuildKit (Recommended)

Add to your `~/.zshrc` or `~/.bashrc`:

```bash
export DOCKER_BUILDKIT=1
```

Then reload:
```bash
source ~/.zshrc
```

### 2. Verify Setup

```bash
# Should show "1"
echo $DOCKER_BUILDKIT

# Should show docker is running
docker ps
```

## Running Tests

### Option 1: Automated (Full Suite)

```bash
make test-all
```

This will:
1. Build all container images (~5-10 min first time, ~1 min with cache)
2. Start all services (discovery, colony, agents, test apps)
3. Run all E2E tests (~5-10 min)
4. Stop and cleanup services

**Total time:**
- First run: ~15-20 minutes
- Subsequent runs: ~6-11 minutes ⚡

### Option 2: Manual (For Development)

```bash
# 1. Build images (one time, or after code changes)
make build

# 2. Start services
make up

# 3. Run tests (can run multiple times)
make test

# 4. View logs while tests run (in another terminal)
make logs-colony
make logs-agent-0

# 5. Stop when done
make down
```

### Option 3: Run Specific Tests

```bash
# Make sure services are running first
make up

# Then run specific tests
go test -v -run TestMeshSuite
go test -v -run TestServiceRegistrationAndDiscovery
go test -v -run TestBeylaPassiveInstrumentation
go test -v -run TestOTLPIngestion
```

## Test Suites

| Suite | Tests | Description |
|-------|-------|-------------|
| MeshSuite | 7 | WireGuard mesh, discovery, registration |
| ServiceSuite | 4 | Service registry and discovery |
| TelemetrySuite | 8 | Beyla eBPF, OTLP, system metrics |
| ProfilingSuite | 2 | Continuous/on-demand CPU profiling |
| DebugSuite | 3 | Uprobe tracing (skipped - Level 3) |
| E2EOrchestrator | 4 | Full workflow orchestration |

## Service Endpoints

When running (`make up`), services are available at:

| Service | Endpoint | Purpose |
|---------|----------|---------|
| Discovery | http://localhost:8080 | Service registry |
| Colony | localhost:9000 | AI coordinator |
| Agent-0 | localhost:9001 | Primary agent |
| Agent-1 | localhost:9002 | Secondary agent |
| CPU App | http://localhost:8081 | CPU-intensive test app |
| OTEL App | http://localhost:8082 | OTLP instrumented app |
| SDK App | http://localhost:3001 | SDK app for uprobe tests |

## Troubleshooting

### Tests fail to connect

```bash
# Check if services are running
make status

# If not, start them
make up
```

### BuildKit errors

```bash
# Enable BuildKit
export DOCKER_BUILDKIT=1

# Rebuild
make build
```

### Out of memory

```bash
# If using Colima, increase memory
colima stop
colima start --cpu 4 --memory 8
```

### Services won't start

```bash
# Clean everything and start fresh
make clean
make build
make up
```

### View service logs

```bash
make logs              # All services
make logs-colony       # Just colony
make logs-agent-0      # Just agent-0
```

## Common Commands

```bash
make help        # Show all commands
make build       # Build container images
make up          # Start services
make down        # Stop services
make test        # Run tests
make test-all    # Build + test + cleanup
make logs        # View logs
make clean       # Stop and remove volumes
make status      # Show service status
```

## Performance Tips

1. **Use BuildKit** - 10x faster builds after first run
2. **Keep services running** - Use `make up` once, run tests many times
3. **Increase Colima resources** - 4 CPU / 8GB RAM recommended

## What's Different from Testcontainers

### Before (testcontainers):
- ❌ Fresh containers per test suite
- ❌ Slow (~30-45 minutes)
- ❌ High memory usage
- ❌ Complex BuildKit setup

### Now (docker-compose):
- ✅ Shared services for all tests
- ✅ Fast (~10-20 minutes)
- ✅ Low memory usage
- ✅ Simple `make test-all`

## Next Steps

1. Run your first test: `make test-all`
2. Try manual workflow: `make up && make test`
3. Explore logs: `make logs-colony`
4. Read full docs: See README.md and DOCKER_COMPOSE_MIGRATION.md
