---
rfd: "071"
title: "Host System Metrics Collection"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: false
dependencies: [ "025" ]
database_migrations: [ ]
areas: [ "agent", "telemetry", "observability" ]
---

# RFD 071 - Host System Metrics Collection

**Status:** 🚧 Draft

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
    - Implement a ticker-based loop (default 15s).
    - Add configuration: `SystemMetricsEnabled` (default true).

2. **Dependencies**:
    - Add `github.com/shirou/gopsutil/v4`.

3. **Metrics Schema**:
    - CPU: `system.cpu.time` (counter), `system.cpu.utilization` (gauge).
    - Memory: `system.memory.usage` (gauge), `system.memory.limit` (gauge).
    - Disk: `system.disk.io` (counter), `system.disk.usage` (gauge).
    - Network: `system.network.io` (counter), `system.network.errors` (counter).

## Implementation Plan

### Phase 1: Core Collector

- [ ] Add `gopsutil` dependency.
- [ ] Create `SystemCollector` struct in `internal/agent/collector`.
- [ ] Implement sampling logic for CPU and Memory.

### Phase 2: OTLP Integration

- [ ] Map raw stats to `pmetric.Metrics`.
- [ ] Inject `OTLPReceiver` (or a metric sink interface) into `SystemCollector`.
- [ ] Wire up in `agent.New()`.

### Phase 3: Enhanced Metrics

- [ ] Add Disk I/O and Network I/O.
- [ ] Add process-level metrics for the Agent itself (`process.runtime.go.*`).

## Security Considerations

- **Privileges**: Reading `/proc` (Linux) usually requires no special
  privileges, but some hardened environments may restrict access. `gopsutil`
  typically handles this gracefully or returns error.
- **Resource Usage**: Analyzing metrics costs CPU/Mem. The collector itself must
  be lightweight (sampling every 15s is negligible).

## Future Work

- **Process Monitoring**: detailed metrics for specific target processes (not
  just global host).
- **Extended I/O**: Access per-device disk stats.
