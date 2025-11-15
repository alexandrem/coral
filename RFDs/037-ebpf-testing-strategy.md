---
rfd: "037"
title: "Production Testing Strategy for eBPF Integration"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: false
dependencies: ["013", "032"]
database_migrations: []
areas: ["testing", "ebpf", "reliability"]
---

# RFD 037 - Production Testing Strategy for eBPF Integration

**Status:** 🚧 Draft

## Summary

Define comprehensive testing strategy for eBPF-based observability features (Beyla
integration, custom eBPF collectors) to ensure reliability across diverse kernel
versions, CPU architectures, and production workloads. This RFD establishes testing
frameworks, performance benchmarks, and validation criteria for eBPF features
before production deployment.

## Problem

**Current behavior/limitations:**

- Beyla integration (RFD 032) has basic unit tests but no comprehensive testing
- No validation across kernel version matrix (4.18+, 5.8+, 6.x)
- No performance benchmarking to ensure <2% CPU overhead target
- No multi-protocol workload testing (HTTP + gRPC + SQL simultaneously)
- No kernel compatibility testing (eBPF verifier, BTF availability)
- No long-running stability tests (memory leaks, file descriptor exhaustion)

**Why this matters:**

- eBPF programs interact directly with kernel, bugs can cause panics or crashes
- Kernel version incompatibilities can silently fail instrumentation
- Performance regressions can make production systems unusable
- Different CPU architectures (x86_64, ARM64) have different eBPF capabilities
- Production failures are expensive and damage user trust

**Use cases affected:**

- Deploying Beyla to production RHEL 8 (kernel 4.18) clusters
- Running Coral agents on ARM64 Graviton instances
- Instrumenting high-throughput services (>100k req/s)
- Long-running agents on VMs with limited resources

## Solution

Establish multi-layered testing strategy covering unit, integration, performance,
compatibility, and production validation testing.

**Key Design Decisions:**

- **Kernel matrix testing**: Test against kernel 4.18, 5.8, 5.15, 6.1, 6.5 in CI
- **Architecture matrix**: Test x86_64 and ARM64 in parallel
- **Multi-protocol workloads**: Test HTTP, gRPC, PostgreSQL, Redis simultaneously
- **Performance benchmarks**: Measure CPU, memory, network overhead continuously
- **Production canary**: Deploy to 1% of fleet before full rollout
- **Regression detection**: Alert on performance degradation >5%

**Benefits:**

- Catch kernel compatibility issues before production
- Ensure performance targets (<2% CPU overhead) are met
- Validate reliability on long-running workloads
- Build confidence in eBPF feature deployments
- Enable fast rollback with clear regression metrics

**Architecture Overview:**

```
┌─────────────────────────────────────────────────────────┐
│ CI Pipeline (GitHub Actions)                           │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  Unit Tests          Integration Tests    E2E Tests    │
│  ↓                   ↓                    ↓             │
│  eBPF logic          Multi-protocol       CLI workflow │
│  Transformers        workloads            coral ask    │
│  Storage             Kernel matrix        Full stack   │
│                                                         │
└─────────────────────────────────────────────────────────┘
                          ↓
┌─────────────────────────────────────────────────────────┐
│ Performance Testing (Dedicated Infra)                  │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  CPU Overhead        Memory Footprint    Network       │
│  < 2% target         50-150 MB target    No impact     │
│                                                         │
│  Latency Impact      Throughput          Stability     │
│  < 1ms P99          No degradation      24hr runtime   │
│                                                         │
└─────────────────────────────────────────────────────────┘
                          ↓
┌─────────────────────────────────────────────────────────┐
│ Production Canary (1% Fleet)                           │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  Error Rate          CPU Usage           Memory        │
│  Monitoring          Monitoring          Monitoring    │
│                                                         │
│  Automatic Rollback on:                                │
│  - Error rate > 1%                                     │
│  - CPU spike > 5%                                      │
│  - Memory leak detected                                │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

### Component Changes

1. **Test Infrastructure** (`test/ebpf/`):
    - Docker images for each kernel version (4.18, 5.8, 5.15, 6.1, 6.5)
    - Multi-architecture builds (x86_64, ARM64)
    - Test workload generators (HTTP, gRPC, SQL, Redis)
    - Performance benchmarking harness

2. **Unit Tests** (`internal/agent/beyla/*_test.go`):
    - Beyla configuration parsing and validation
    - OTLP transformation logic (histogram bucketing, attribute extraction)
    - Storage queries and retention cleanup
    - Mock Beyla process lifecycle

3. **Integration Tests** (`test/integration/beyla/`):
    - Beyla process startup and shutdown
    - Multi-protocol workload instrumentation
    - End-to-end data flow (Beyla → OTLP → Storage → Query)
    - Kernel version compatibility matrix

4. **Performance Tests** (`test/performance/`):
    - CPU overhead measurement under load
    - Memory footprint tracking (baseline vs instrumented)
    - Latency impact on instrumented services
    - Long-running stability tests (24 hours)

5. **Production Validation** (`deploy/canary/`):
    - Canary deployment configurations
    - Automated rollback triggers
    - Monitoring dashboards and alerts
    - Runbook for manual validation

## Implementation Plan

### Phase 1: Test Infrastructure

- [ ] Create Docker images for kernel matrix (4.18, 5.8, 5.15, 6.1, 6.5)
- [ ] Set up multi-arch builds (x86_64, ARM64) in CI
- [ ] Create test workload generators:
  - HTTP server with configurable routes and latencies
  - gRPC server with streaming and unary calls
  - PostgreSQL workload with SELECT/INSERT/UPDATE
  - Redis workload with GET/SET/DEL
- [ ] Configure GitHub Actions workflow for matrix testing

### Phase 2: Integration Tests

- [ ] Test Beyla process lifecycle (start, monitor, stop, cleanup)
- [ ] Test single-protocol instrumentation (HTTP only)
- [ ] Test multi-protocol instrumentation (HTTP + gRPC + SQL)
- [ ] Test data flow from Beyla → OTLP → Storage
- [ ] Test QueryBeylaMetrics RPC end-to-end
- [ ] Validate histogram bucketing and percentile calculations

### Phase 3: Kernel Compatibility Tests

- [ ] Test on kernel 4.18 (RHEL 8, CentOS 8)
- [ ] Test on kernel 5.8 (Ubuntu 20.04)
- [ ] Test on kernel 5.15 (Ubuntu 22.04)
- [ ] Test on kernel 6.1 (Debian 12)
- [ ] Test on kernel 6.5 (Ubuntu 23.10)
- [ ] Test BTF availability detection and fallback
- [ ] Test eBPF verifier compatibility

### Phase 4: Performance Benchmarking

- [ ] Baseline measurements (no instrumentation):
  - CPU usage under 10k, 50k, 100k req/s
  - Memory footprint
  - P99 latency
- [ ] Instrumented measurements (Beyla enabled):
  - CPU overhead delta
  - Memory overhead delta
  - Latency impact
- [ ] Long-running stability tests:
  - 24-hour run with memory leak detection
  - File descriptor leak detection
  - Connection pool exhaustion testing
- [ ] Performance regression detection:
  - Alert if CPU overhead > 2%
  - Alert if memory > 150 MB
  - Alert if P99 latency impact > 1ms

### Phase 5: Production Canary

- [ ] Define canary deployment criteria:
  - 1% of production fleet
  - Mix of high-traffic and low-traffic services
  - Geographic distribution
- [ ] Implement automated rollback triggers:
  - Error rate increase > 1%
  - CPU usage spike > 5%
  - Memory leak detected (>10% growth over 1 hour)
  - Agent crash rate > 0.1%
- [ ] Create monitoring dashboards:
  - CPU usage (baseline vs canary)
  - Memory usage (baseline vs canary)
  - Error rates (baseline vs canary)
  - Beyla process health
- [ ] Define validation checklist:
  - Verify Beyla metrics appear in Colony
  - Verify no increase in error logs
  - Verify performance within SLOs
  - Wait 24 hours before full rollout

## Test Scenarios

### Scenario 1: Multi-Protocol Workload

**Setup:**
- HTTP server (10k req/s, mixed routes, 10% 5xx errors)
- gRPC server (5k req/s, streaming + unary)
- PostgreSQL (1k queries/s, SELECT/INSERT/UPDATE)

**Validation:**
- All protocols instrumented correctly
- HTTP metrics show correct error rate (10%)
- gRPC metrics show both streaming and unary calls
- SQL metrics show operation breakdown
- CPU overhead < 2%
- No memory leaks over 1 hour

### Scenario 2: Kernel Version Matrix

**Setup:**
- Test on each kernel version in matrix
- Run same multi-protocol workload

**Validation:**
- Beyla starts successfully on all kernels
- Metrics collected on all kernels
- BTF detection works correctly
- No kernel panics or crashes
- Graceful degradation on older kernels (4.18)

### Scenario 3: High-Throughput Service

**Setup:**
- HTTP server (100k req/s, simple GET endpoint)
- 8 CPU cores, 16 GB RAM

**Validation:**
- CPU overhead < 2% (< 0.16 cores)
- Memory footprint < 150 MB
- P99 latency impact < 1ms
- No dropped requests
- Sustained for 24 hours

### Scenario 4: Resource-Constrained Environment

**Setup:**
- VM with 1 CPU core, 2 GB RAM
- HTTP server (1k req/s)

**Validation:**
- Beyla runs without OOM
- Metrics collected correctly
- No significant CPU contention
- Local storage cleanup works (1-hour retention)

## Performance Targets

| Metric | Target | Measurement Method |
|--------|--------|-------------------|
| CPU Overhead | < 2% | `perf stat` comparison |
| Memory Footprint | 50-150 MB | RSS monitoring |
| P99 Latency Impact | < 1ms | Load testing comparison |
| Startup Time | < 5 seconds | Process monitoring |
| Storage Size | < 100 MB/hour | DuckDB file size |
| Agent Crash Rate | < 0.01% | Production telemetry |

## Rollback Criteria

Automatic rollback if any of:
- Error rate increase > 1% compared to baseline
- CPU usage increase > 5% compared to baseline
- Memory usage increase > 20% compared to baseline
- Agent crash rate > 0.1%
- Kernel panics detected
- Beyla process failures > 5% of starts

## Testing Tools

- **Kernel matrix**: Docker with custom kernel versions
- **Load generation**: `wrk`, `ghz` (gRPC), `pgbench` (PostgreSQL)
- **CPU profiling**: `perf`, `pprof`
- **Memory profiling**: `valgrind`, `pprof --alloc_space`
- **eBPF debugging**: `bpftool`, `bpftrace`
- **Performance tracking**: Prometheus + Grafana dashboards

## Future Work

- Chaos engineering tests (kill Beyla, simulate kernel issues)
- Fuzz testing for eBPF programs
- Property-based testing for metric aggregation
- Production traffic replay for validation
- Automated performance regression detection in CI

## Dependencies

- **RFD 013**: Custom eBPF collector framework
- **RFD 032**: Beyla integration implementation
- CI/CD infrastructure (GitHub Actions)
- Performance testing infrastructure (dedicated VMs/containers)

## References

- eBPF Kernel Compatibility: https://ebpf.io/infrastructure/
- Linux Kernel Testing Project: https://linux-test-project.github.io/
- Beyla Testing: https://github.com/grafana/beyla/tree/main/test
- Cilium eBPF Testing: https://github.com/cilium/ebpf/blob/main/TESTING.md
