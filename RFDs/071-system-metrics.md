---
rfd: "071"
title: "Host System Metrics Collection"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "025", "067" ]
database_migrations: [ "system_metrics_local", "system_metrics_summaries" ]
areas: [ "agent", "telemetry", "observability" ]
---

# RFD 071 - Host System Metrics Collection

**Status:** 🎉 Implemented

## Summary

Implement a native system metrics collector within the Coral Agent to capture
host-level resource usage (CPU, Memory, Disk, Network). These metrics will be
converted internally to OTLP format and fed into the agent's existing OTLP
pipeline, providing out-of-the-box node observability without external
exporters.

## Problem

- **Current limitations**: The Agent receives application telemetry (
  traces/metrics from SDKs/Beyla) but is blind to the underlying host's health.
  It doesn't know if the CPU is saturated, memory is leaking, or disk I/O is
  blocked.
- **Why this matters**: Application performance issues are often caused by
  resource contention. Without host metrics, users cannot correlate "slow API
  requests" with "high CPU load" or "swap usage".
- **Use cases affected**: Infrastructure monitoring, capacity planning, root
  cause analysis of performance degradation.

## Solution

Integrate `github.com/shirou/gopsutil` into the Agent to periodically sample
system stats and emit them as OTLP metrics.

**Key Design Decisions:**

- **Native Collection**: Use `gopsutil` (standard Go library) instead of
  shelling out to `top`/`free` or requiring a separate binary (like
  node_exporter).
- **OTLP Pipeline Reuse**: Do *not* create a new protocol. Convert sampled data
  into `pdata.Metrics` and push them into the existing `OTLPReceiver` buffer.
  This treats the Agent itself as an "instrumented application".
- **Standard Metrics**: Follow OpenTelemetry Semantic Conventions for host
  metrics (`system.cpu.utilization`, `system.memory.usage`, etc.).
- **Query Integration**: System metrics are automatically included in
  `coral query summary` (RFD 067) to correlate application issues with host
  resource constraints.

**Architecture Overview:**

```
[System Collector Loop]
       ↓ (gopsutil)
   Raw Stats
       ↓
[OTLP Converter] → pdata.Metrics
       ↓
[OTLP Receiver Buffer] → Batching & Sending → Colony
```

### Component Changes

1. **Agent (Go)**:
    - Add `internal/agent/collector` package.
    - Implement a ticker-based loop (default 15s, configurable).
    - Add configuration section: `system_metrics` (see Configuration section
      below).

2. **Dependencies**:
    - Add `github.com/shirou/gopsutil/v4`.

3. **Metrics Schema**:
    - CPU: `system.cpu.time` (cumulative seconds, counter),
      `system.cpu.utilization` (percentage 0-100, gauge).
    - Memory: `system.memory.usage` (gauge), `system.memory.limit` (gauge).
    - Disk: `system.disk.io` (counter), `system.disk.usage` (gauge).
    - Network: `system.network.io` (counter), `system.network.errors` (counter).
    - All metrics
      follow [OTel System Metrics Semantic Conventions](https://opentelemetry.io/docs/specs/semconv/system/system-metrics/).

### Configuration

The system metrics collector will be configurable through the agent
configuration:

```yaml
system_metrics:
    enabled: true              # Master switch for all system metrics
    interval: 15s              # Sampling interval
    container_mode: auto       # auto|host|cgroup - metric scope for containers
    collectors:
        cpu: true                # CPU time and utilization
        memory: true             # Memory usage and limits
        disk: true               # Disk I/O and usage
        network: true            # Network I/O and errors
        agent_process: true      # Agent's own runtime metrics (Phase 3)
```

**Configuration Notes:**

- Default interval is 15s to balance observability with overhead.
- `container_mode: auto` detects containerization automatically; use `host` for
  node agents or `cgroup` for sidecar deployments.
- Individual collectors can be disabled to reduce cardinality or overhead.
- All collectors default to enabled when `system_metrics.enabled: true`.

### Error Handling & Degradation

The collector must handle failures gracefully:

- **Initialization Failure**: If `gopsutil` cannot initialize (e.g., restricted
  `/proc` access), log a warning and disable the collector. Do not block agent
  startup.
- **Sampling Errors**: If a specific metric collection fails (e.g., disk stats
  unavailable), skip that sample and log at debug level. Retry on next interval.
- **Partial Failures**: If some collectors work but others fail, emit available
  metrics and continue. Track failure counts internally.
- **Persistent Failures**: If a collector fails consistently (>10 consecutive
  attempts), disable it and log an error with diagnostics.

**Error Metrics**: The collector should emit internal metrics:

- `coral.agent.collector.errors{collector="cpu|memory|disk|network"}` (counter)
- `coral.agent.collector.samples{collector="cpu|memory|disk|network"}` (counter)

### Metric Cardinality

To prevent cardinality explosion:

- **CPU**: Emit aggregate utilization across all cores. Per-core metrics are
  excessive for debugging distributed apps.
- **Disk**: Emit total I/O across all devices, not per-device stats (Phase 2 may
  add per-device if needed).
- **Network**: Emit total I/O across all interfaces, excluding loopback.
- **Labels**: Minimal labeling - only add dimensions necessary for
  troubleshooting (e.g., `device_type` for disk, `state` for memory).

**Cardinality Estimate**: ~10-15 unique metric names × 1 resource instance =
10-15 time series per agent.

### Query Integration (RFD 067)

System metrics are automatically integrated into the unified query interface to
provide infrastructure context during diagnostics.

**`coral query summary` Integration:**

The `coral_query_summary` tool (RFD 067) will include host metrics in the health
overview to correlate application performance with resource constraints:

```
Service Health Summary (last 5m)

┌─────────────────┬────────┬──────────┬─────────┬──────────┬──────────────────┐
│ Service         │ Status │ Requests │ Errors  │ P95      │ Host Resources   │
├─────────────────┼────────┼──────────┼─────────┼──────────┼──────────────────┤
│ api-gateway     │ ✅     │ 12.5k    │ 0.2%    │ 45ms     │ CPU: 25% Mem: 2GB│
│ payment-service │ ⚠️     │ 3.2k     │ 2.8% ⬆  │ 234ms ⬆  │ CPU: 89% Mem: 7GB│
│ auth-service    │ ✅     │ 8.1k     │ 0.1%    │ 12ms     │ CPU: 15% Mem: 1GB│
└─────────────────┴────────┴──────────┴─────────┴──────────┴──────────────────┘

⚠️ Issues Detected:

[payment-service]
• Error rate elevated: 2.8% (baseline: 0.5%)
• P95 latency spike: 234ms (baseline: 89ms)
• ⚠️ High CPU utilization: 89% (threshold: 80%)
• ⚠️ High memory usage: 7GB / 8GB (87% utilization)
• Recent errors (3):
  - [OTLP] 21:14:32 ERROR: Database connection timeout
  - [eBPF] 21:14:28 ERROR: HTTP 503 from /api/charge
  - [OTLP] 21:14:15 ERROR: Payment gateway unavailable

Root Cause Indicator: High CPU and memory suggest resource exhaustion.
Database timeouts likely caused by insufficient resources.
```

**System Metrics in Query Summary:**

- **CPU Utilization**: Show current CPU% alongside service health status
- **Memory Usage**: Show memory usage (current / limit) to detect memory
  pressure
- **Disk I/O**: Flag high disk wait times that may slow application I/O
- **Network Errors**: Highlight packet loss or network errors affecting
  connectivity
- **Thresholds**: Warn when system resources exceed safe thresholds (CPU >80%,
  Memory >85%, Disk >90%)

**Benefits:**

- **Immediate Context**: Operators see "payment service is slow *because* CPU is
  maxed out"
- **Root Cause Acceleration**: Distinguishes application bugs from
  infrastructure
  constraints
- **Proactive Diagnosis**: Warns about resource saturation before it causes
  outages
- **Multi-Service Hosts**: Shows host-level constraints affecting multiple
  co-located services

**Implementation Requirements:**

- Colony must query system metrics alongside application metrics when generating
  summaries
- System metric thresholds must be configurable (default: CPU >80%, Memory >85%,
  Disk >90%)
- Summary output must clearly distinguish host-level vs service-level issues

### Live Query Feature

For live debugging scenarios requiring 15s precision, add a direct agent query
command that bypasses Colony aggregation.

**Command:**
`coral query metrics --live --agent <agent-id> --metric <name> --since <duration>`

**Purpose:**

- Debug transient spikes (e.g., 30-second CPU burst)
- Analyze memory allocation patterns during load tests
- Correlate disk I/O with application operations in real-time
- Troubleshoot active incidents where sub-minute precision is critical

**Behavior:**

- Queries agent's local DuckDB directly via RPC (bypasses Colony)
- Returns full 15s resolution data (up to 1-hour window)
- Shows individual data points, not aggregates
- Only available for agents within 1-hour retention window

**Example Output:**

```
$ coral query metrics --live --agent agent-xyz --metric system.cpu.utilization --since 5m

Timestamp            Value   Unit
2025-12-12 14:23:45  23.5%   percent
2025-12-12 14:24:00  45.2%   percent
2025-12-12 14:24:15  89.1%   percent  ← Spike detected
2025-12-12 14:24:30  91.3%   percent  ← Sustained high
2025-12-12 14:24:45  87.4%   percent
2025-12-12 14:25:00  34.2%   percent
...

Summary: Peak 91.3% at 14:24:30, Average 61.8% over 5m window
```

**Implementation:**

- Add `QuerySystemMetrics` RPC to `proto/agent.proto`
- Implement agent-side handler in
  `internal/agent/server/system_metrics_handlers.go`
- Add CLI command in `internal/cli/query/metrics_live.go`
- Support filtering by metric name, agent ID, and time range

**Benefits:**

- Complements Colony aggregates with on-demand high-precision data
- No storage cost (uses existing 1-hour agent retention)
- Enables rapid diagnosis during active incidents
- Preserves 15s precision without bloating Colony storage

## Implementation Plan

### Phase 1: Core Collector

- [x] Add `gopsutil` dependency.
- [x] Create `SystemCollector` struct in `internal/agent/collector`.
- [x] Implement sampling logic for CPU and Memory.
- [x] Implement agent-side DuckDB storage (`system_metrics_local` table).
- [x] Add cleanup loop for 1-hour retention.

### Phase 2: Agent Integration

- [x] Add `SystemMetricsConfig` to agent configuration schema.
- [x] Wire up collector in agent initialization (`internal/cli/agent/start.go`).
- [x] Create `SystemMetricsHandler` for RPC queries.
- [x] Start collector and cleanup goroutines on agent startup.

### Phase 3: Enhanced Metrics

- [x] Add Disk I/O and Network I/O collectors.
- [x] Implement all four metric categories (CPU, Memory, Disk, Network).
- [ ] Add process-level metrics for the Agent itself (`process.runtime.go.*`) -
  **Deferred to Future Work**.

### Phase 4: Colony-Side Storage & Aggregation

- [x] Create `system_metrics_summaries` table in Colony schema.
- [x] Implement `SystemMetricsPoller` with 1-minute aggregation logic.
- [x] Add database methods for storing and querying summaries (
  `internal/colony/database/system_metrics.go`).
- [x] Implement aggregation: min/max/avg/p95 for gauges, delta for counters.
- [x] Add 30-day retention cleanup.
- [x] Write unit tests for aggregation logic.

### Phase 5: Query Summary Integration (RFD 067)

- [x] Add `QuerySystemMetrics` RPC to `proto/coral/agent/v1/agent.proto`.
- [x] Implement agent-side RPC handler (
  `internal/agent/system_metrics_handler.go`).
- [x] Update Colony's `QueryUnifiedSummary` to include system metrics.
- [x] Implement system metric threshold checks (CPU >80%, Memory >85%).
- [x] Add "Host Resources" section to CLI summary output.
- [x] Add host resource fields to protobuf schema.
- [x] Correlate system resource issues with service degradation in summary
  output.

### Phase 6: Live Query Command

- [ ] Add CLI command `coral query metrics --live` in
  `internal/cli/query/metrics_live.go` - **Deferred to Future Work**.
- [ ] Support filtering by metric name, agent ID, and time range.
- [ ] Add time-series table formatter for live query output.

### Testing Strategy

**Unit Tests:**

- Mock `gopsutil` interfaces to test metric conversion and error handling.
- Verify OTLP metric format compliance (use `pmetric.Metrics` test fixtures).
- Test configuration parsing and validation.

**Integration Tests:**

- Run collector against real system (CI environment) and validate metric
  emission.
- Test collector behavior under simulated failures (restricted `/proc`, missing
  devices).
- Verify metrics flow through the full OTLP pipeline to DuckDB.

**Performance Tests:**

- Benchmark collector overhead (CPU/memory impact of 15s sampling).
- Ensure collector doesn't introduce latency to main agent operations.

## Storage & Retention

**Storage Impact:**

- At 15s intervals: 240 samples/hour × ~12 metric types = 2,880 rows/hour/agent.
- With default ~1hr retention (per CLAUDE.md), this is ~3k rows in DuckDB per
  agent.
- Colony storage depends on aggregation strategy (see downsampling concern
  below).

**Retention Policy:**

- **Agent**: Keep raw samples for 1 hour, then purge (consistent with existing
  telemetry).
- **Colony**: Store aggregated summaries (1-minute rollups) for longer-term
  analysis.

**Downsampling:**

The storage strategy uses a tiered approach following Coral's existing
OTLP/Beyla patterns:

**Agent-Side (High Precision):**

- Store raw 15s samples in local DuckDB
- Retention: 1 hour (matches existing telemetry retention)
- Purpose: Live debugging and sub-minute precision analysis
- Storage: ~2,880 rows/hour, ~10KB compressed per agent

**Colony-Side (Aggregated):**

- 1-minute bucket aggregation (aligns with OTLP summaries pattern)
- Aggregates per bucket: min, max, avg, p95 (for gauges)
- Delta calculations for counters (rate per minute)
- Retention: 30 days (enables capacity planning and trend analysis)
- Storage reduction: 75% (4 samples → 1 summary)
- Storage: ~90MB/month for 10 agents

**Rationale:**

- Query summary uses 5m-1h time ranges - 1-minute granularity sufficient
- Captures both transient spikes (max) and sustained baselines (avg)
- P95 percentile enables outlier detection
- 15s precision available via live query for active incidents
- Follows proven OTLP aggregation pattern (RFD 025)

**Storage Comparison:**

- OTLP Summaries: ~3MB/day (24hr retention)
- Beyla HTTP: ~500MB/month (30-day retention, high cardinality)
- **System Metrics: ~90MB/month (30-day retention, 18% of Beyla)**

## Multi-Service Agent Considerations

In multi-service deployments (per RFD 011), system metrics are **host-level**,
not per-service:

- **Metric Attribution**: System metrics are tagged with the agent ID, not
  individual service IDs.
- **Resource Sharing**: Multiple services on the same host share
  CPU/memory/disk. The collector cannot attribute resource usage to specific
  services.
- **Use Case**: System metrics answer "is the host healthy?" not "which service
  is consuming resources?". For per-service attribution, use application-level
  metrics (RFD 060 SDK) or process-level eBPF (future work).

**Recommendation**: Document clearly that system metrics are
infrastructure-level, and direct users to application metrics for
service-specific observability.

**Query Summary Behavior**: When `coral query summary` displays multiple
services on the same host, the "Host Resources" column will show identical
values for all services (since they share the same host). The summary should
indicate shared resource constraints with a visual indicator (e.g., "⚠️ Shared
host: CPU 89%") to clarify that resource issues affect all co-located services.

## Container & Kubernetes Considerations

**Container Detection:**

- In containerized environments, `gopsutil` reads **host-level** stats by
  default, not container-specific cgroups.
- For Kubernetes node agents (RFD 012), this is correct - we want node health,
  not pod-level metrics.
- For containerized agents running as sidecars, we need cgroup-aware collection.

**Implementation:**

- Detect if running in a container (check `/.dockerenv`, `/proc/1/cgroup`).
- If containerized, read cgroup stats from `/sys/fs/cgroup/` instead of global
  `/proc/stat`.
- Use `gopsutil`'s cgroup support where available, or implement custom parsers.

**Multi-Tenancy:**

- On shared hosts (multi-tenant K8s), exposing system-wide metrics may leak
  information.
- **Solution**: In container mode, only expose cgroup-scoped metrics for the
  container's namespace.
- Add configuration flag: `container_mode: auto|host|cgroup` (default:
  auto-detect).

## Security Considerations

- **Privileges**: Reading `/proc` (Linux) usually requires no special
  privileges, but some hardened environments may restrict access. The collector
  handles this gracefully (see Error Handling & Degradation section).
- **Resource Usage**: Analyzing metrics costs CPU/Mem. The collector itself must
  be lightweight (sampling every 15s is negligible). Performance testing will
  validate overhead is <1% CPU/memory.
- **Information Disclosure**: In multi-tenant environments, system-wide metrics
  may reveal neighboring workloads. Use container mode (cgroup-scoped metrics)
  in shared environments.
- **Sensitive Data**: System metrics do not contain user data, but may reveal
  infrastructure topology (e.g., number of CPUs, total memory). This is
  generally
  acceptable for internal observability tools.

## Implementation Status

**Core Capability:** ✅ Complete

The Host System Metrics Collection feature is fully implemented and operational.
Agents collect CPU, memory, disk, and network metrics at 15-second intervals,
store them locally for 1 hour, and Colony aggregates them into 1-minute
summaries with 30-day retention. System metrics are integrated into
`coral query summary` to provide infrastructure context during diagnostics.

**Operational Components:**

- ✅ **Agent-Side Collection**:
    - `SystemCollector` in `internal/agent/collector/system_collector.go`
      samples host metrics using `gopsutil/v4`
    - Four metric categories: CPU utilization/time, Memory
      usage/limit/utilization, Disk I/O/usage, Network I/O/errors
    - 15-second sampling interval (configurable)
    - Local DuckDB storage in `system_metrics_local` table
    - Automatic cleanup every 10 minutes (1-hour retention)
    - Configuration via `system_metrics` section in agent.yaml

- ✅ **Colony-Side Aggregation**:
    - `SystemMetricsPoller` in `internal/colony/system_metrics_poller.go` polls
      agents every minute
    - Aggregates 4 samples (15s × 4 = 60s) into 1-minute summaries
    - Statistics: min/max/avg/p95 for gauges, delta for counters
    - Storage in `system_metrics_summaries` table with 30-day retention
    - 75% storage reduction through aggregation
    - Database methods in `internal/colony/database/system_metrics.go`

- ✅ **Query Integration (RFD 067)**:
    - `QueryUnifiedSummary` includes host resource metrics alongside application
      metrics
    - CLI output (`coral query summary`) displays "Host Resources" section with
      CPU and Memory
    - Threshold-based warnings: CPU >80%, Memory >85%
    - Automatic service status degradation when resource thresholds exceeded
    - Correlation of application performance issues with infrastructure
      constraints

- ✅ **RPC Interface**:
    - `QuerySystemMetrics` RPC defined in `proto/coral/agent/v1/agent.proto`
    - Agent-side handler in `internal/agent/system_metrics_handler.go`
    - Supports querying by time range and metric name filters
    - Returns raw 15-second precision data for live debugging

- ✅ **Testing**:
    - Comprehensive unit tests for aggregation logic in
      `internal/colony/system_metrics_poller_test.go`
    - Tests cover percentile calculations, gauge vs counter handling, edge cases
    - All tests passing in CI

**What Works Now:**

```bash
# Start agent with system metrics collection (enabled by default)
coral agent start

# View service health with host resource context
coral query summary --since 5m

# Example output:
Service Health Summary:

✅ api-gateway (eBPF)
   Status: healthy
   Requests: 12500
   Error Rate: 0.20%
   Avg Latency: 45.00ms
   Host Resources:
     CPU: 25% (avg: 22%)
     Memory: 2.0GB/8.0GB (25%)

⚠️  payment-service (eBPF)
   Status: degraded
   Requests: 3200
   Error Rate: 2.80%
   Avg Latency: 234.00ms
   Host Resources:
     CPU: 89% (avg: 82%)
     Memory: 7.0GB/8.0GB (88%)
   Issues:
     - ⚠️  High CPU: 89% (threshold: 80%)
     - ⚠️  High Memory: 7.0GB/8.0GB (88%, threshold: 85%)
```

**Configuration:**

```yaml
# agent.yaml - System metrics configuration
system_metrics:
    enabled: true              # Default: true
    interval: 15s              # Default: 15s
    retention: 1h              # Default: 1h
    cpu_enabled: true          # Default: true
    memory_enabled: true       # Default: true
    disk_enabled: true         # Default: true
    network_enabled: true      # Default: true
```

**Storage Metrics:**

- **Agent**: ~10KB/hour/agent (compressed), ~2,880 rows/hour
- **Colony**: ~90MB/month for 10 agents (30-day retention), 75% reduction vs raw
  data
- **Query Performance**: <100ms for summary queries with system metrics

**Integration Status:**

All core components are integrated and operational. The feature is
production-ready with the following minor items deferred to future work:

- Live query CLI command (`coral query metrics --live`) - RPC interface exists,
  CLI wrapper pending
- Agent process-level metrics (`process.runtime.go.*`) - low priority, host
  metrics sufficient for diagnostics

## Future Work

The following features are deferred to future RFDs or intentionally out of
scope:

**Live Query CLI Command** (Low Priority)

- `coral query metrics --live --agent <id> --metric <name> --since <duration>`
- RPC interface (`QuerySystemMetrics`) already implemented
- CLI wrapper in `internal/cli/query/metrics_live.go` pending
- Use case: Sub-minute precision debugging during active incidents
- Workaround: Query agent DuckDB directly via `/duckdb/` HTTP endpoint

**Agent Process Metrics** (Low Priority)

- Process-level metrics for the Agent itself (`process.runtime.go.*`)
- CPU/memory/goroutines/GC stats for Agent process
- Rationale: Host-level metrics sufficient for current diagnostics needs

**Process Monitoring** (Future RFD)

- Detailed metrics for specific target processes (not just global host)
- Enables per-service resource attribution on multi-service hosts
- Requires integration with RFD 011 (Multi-Service Agents)

**Extended I/O** (Enhancement)

- Per-device disk stats and per-interface network stats
- Advanced diagnostics for storage and network bottlenecks
- Cardinality concerns - need careful label design

**GPU Metrics** (ML/GPU Workloads)

- Extend collector to capture GPU utilization
- Requires additional dependencies like `nvml` bindings
- Out of scope for general observability

**Windows/macOS Support** (Cross-Platform)

- Validate `gopsutil` cross-platform behavior
- Adjust cgroup logic for Linux-only environments
- Current implementation Linux-focused

**Advanced Alerting** (Future Enhancement)

- Trend-based anomaly detection using historical data
- Capacity planning alerts (e.g., "CPU growing 10% daily")
- Requires 30-day historical data analysis capabilities
