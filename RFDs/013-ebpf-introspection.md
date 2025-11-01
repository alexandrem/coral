---
rfd: "013"
title: "eBPF-Based Application Introspection"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: ["007", "011", "012"]
database_migrations: []
areas: ["observability", "profiling", "networking", "security"]
---

# RFD 013 - eBPF-Based Application Introspection

**Status:** 🚧 Draft

## Summary

Add an eBPF instrumentation subsystem that lets Coral observe, trace, and profile
application behavior directly from the host without modifying workloads. The
feature gives node agents (RFD 012) and multi-service agents (RFD 011) the ability
to capture high-fidelity telemetry—latency histograms, syscall stats, network
flows, CPU/memory hotspots—fueling Coral’s AI insights and remote debugging
workflows.

## Problem

**Current behavior/limitations**

- Passive monitoring relies on process polling, health endpoints, and coarse
  metrics; it cannot surface fine-grained performance or security anomalies.
- Profiling today requires in-process SDK hooks or manual tools (`pprof`,
  `perf`) that demand elevated access and human intervention.
- Network packet capture is heavy-handed; operators want intent-focused data
  (latency, error codes, TLS fingerprints) without storing entire payloads.
- AI-driven diagnostics need richer signals (queueing delay, lock contention,
  syscall spikes) to make precise recommendations.

**Why this matters**

- Distributed incident response hinges on quickly understanding where time is
  spent inside services, not just that a service is “slow”.
- Security and compliance teams want visibility into anomalous system calls,
  suspect network flows, or privilege escalation attempts.
- eBPF lets Coral deliver high-value telemetry with minimal overhead and without
  instrumenting application code—aligned with the “passive first” adoption path.

**Use cases affected**

- Remote tap sessions that should capture request latency distributions or CPU
  stacks alongside packet samples.
- AI queries like “Why is checkout slow?” or “What changed on payments?”
  requiring low-level evidence to justify actions.
- Observability in air-gapped or legacy workloads where SDK integration is not
  feasible.

## Solution

Embed an eBPF runtime inside Coral agents to attach kernel-level probes (kprobes,
uprobes, tracepoints, cgroup/bpf) that produce structured events streamed over
the WireGuard mesh to the colony. Expose CLI/MCP commands to start/stop eBPF
programs, collect summaries, and feed AI analyses. Focus on portable, safe
programs with guardrails for CPU/memory usage.

### Key Design Decisions

- **In-agent eBPF controller**: Agents manage eBPF programs so no external
  daemon (`bcc`, `tracee`) is required. Keeps telemetry inside Coral's trust
  boundary.
- **Library of curated programs**: Ship vetted BPF bytecode for common
  scenarios—HTTP latency, TCP retransmissions, syscall heatmaps, CPU flamegraphs.
  Avoid arbitrary user code execution initially.
- **Event streaming with summaries**: Raw events are aggregated into sketches
  (histograms, Top N stacks) before leaving the node, minimizing bandwidth.
- **Safety budget**: Limit sampling frequency, memory maps, and duration; fall
  back automatically if kernel/cgroup policies reject probes.
- **Unified control plane**: eBPF collectors integrated into existing workflows
  (`coral tap`, AI queries) and MCP tools. No separate `coral ebpf` command—eBPF
  is a data source, not a distinct operation.
- **AI-orchestrated collection**: Colony AI automatically selects appropriate
  eBPF collectors based on query context ("Why is X slow?" triggers HTTP latency
  + CPU profiling). Users don't need to know eBPF exists.
- **Background + on-demand modes**: Continuous lightweight collectors run via
  agent config; intensive collectors (CPU profiling, full syscall tracing) are
  on-demand via tap sessions.

### Benefits

- Deep visibility without code changes: near real-time CPU/memory/network
  insights for any Linux-based workload.
- More actionable AI answers—diagnostics include concrete evidence (hot
  functions, slow syscalls, congested sockets).
- Reduced reliance on packet captures; eBPF summaries are lighter and more
  privacy-preserving.
- Aligns with container/Kubernetes trends where eBPF is the default for
  observability and security.

### Architecture Overview

```
┌─────────────────────────────────────────────┐
│ Coral Agent (node or sidecar)               │
│                                             │
│  ┌────────────┐  ┌────────────┐             │
│  │ eBPF Loader│→ │ eBPF Progs │             │
│  └────────────┘  └────┬───────┘             │
│      │ Attach kprobes/ │ uprobes            │
│      ▼                 ▼                    │
│  Linux Kernel       Target Processes        │
│      │                 │                    │
│      └───── Events/Maps ───────────────────►│
│                 Aggregator                  │
│                     │                       │
│                     ▼                       │
│             Mesh Stream (WireGuard)         │
└─────────────────────────────────────────────┘
                      │
                      ▼
           ┌────────────────────────┐
           │ Colony / DuckDB        │
           │  • Store metrics       │
           │  • Serve AI queries    │
           └────────────────────────┘
```

### Component Changes

1. **Agent (node & sidecar)**
   - Embed eBPF loader/manager (using libbpf via cgo or CO-RE BPF object files).
   - Maintain catalog of approved programs (HTTP latency, syscall stats, CPU
     profiler, TCP metrics).
   - Stream aggregated results to colony; handle lifecycle (start/stop, errors).
   - Expose RPC endpoints for control commands.
   - Implement capability detection (kernel version, BPF features, CAP_BPF).
   - Support two modes: continuous (background) and on-demand (tap sessions).

2. **Colony**
   - Extend RPC service to request eBPF collectors, receive streams, store into
     DuckDB tables, and surface via CLI/MCP.
   - Add retention policies for eBPF artefacts.
   - Implement AI decision logic: map user queries to collector combinations.
   - Track collector resource usage and enforce quotas per agent.

3. **CLI / MCP**
   - Extend `coral tap` with eBPF data source flags (`--http-latency`,
     `--cpu-profile`, or smart `--analysis` mode).
   - Add `coral query ebpf` for historical data retrieval from colony storage.
   - Expose MCP tools: `coral_start_tap` (with eBPF options),
     `coral_get_ebpf_summary`, `coral_query_performance`.
   - NO separate `coral ebpf` command—eBPF is a data source, not a user-facing
     operation.

**Configuration Example**

```yaml
# agent-config.yaml excerpt
ebpf:
  enabled: true

  # Background collectors (always running, low overhead)
  continuous_collectors:
    - name: http_latency
      mode: continuous
      config:
        sampleRate: 10       # per second per CPU
        filter:
          podLabel: app=checkout-api
        includePayload: false
        retention: 24h

    - name: tcp_metrics
      mode: continuous
      config:
        sampleRate: 1        # very low overhead
        retention: 7d

  # On-demand collectors (triggered by tap or AI)
  on_demand_collectors:
    - name: cpu_profile
      config:
        maxDuration: 300s    # safety limit
        stackDepth: 127
        sampleRate: 99       # Hz

    - name: syscall_stats
      config:
        maxDuration: 60s
        filter:
          excludeSyscalls: [read, write, poll]  # reduce noise

  # Resource limits (per agent)
  limits:
    maxConcurrentCollectors: 3
    maxMemoryMB: 512
    maxEventBufferSize: 65536
    maxCPUPercent: 5
```

### Performance Overhead

eBPF instrumentation is efficient but not free. Expected overhead per collector:

| Collector | CPU Impact | Memory Footprint | Event Rate | Notes |
|-----------|------------|------------------|------------|-------|
| `http_latency` | 0.5-2% | 8-32 MB | 100-10K events/s | Depends on request rate |
| `tcp_metrics` | 0.1-0.5% | 4-16 MB | 10-1K events/s | Very lightweight |
| `syscall_stats` | 1-3% | 16-64 MB | 1K-100K events/s | High on I/O-heavy apps |
| `cpu_profile` | 2-5% | 32-128 MB | 1K-10K samples/s | Highest impact |

**Network bandwidth**: Aggregated summaries consume 1-10 KB/s per collector.
Raw event streaming (for debugging) can reach 1-10 MB/s—use sparingly.

**Mitigation strategies**:
- Start with continuous low-overhead collectors (`tcp_metrics`, sampled
  `http_latency`).
- Use on-demand mode for expensive collectors (`cpu_profile`, full
  `syscall_stats`).
- Enforce per-agent quotas (max 5% CPU, 512 MB memory).
- Automatically disable collectors if overhead threshold exceeded.

### Kernel Compatibility Matrix

eBPF features vary significantly by kernel version. Minimum requirements:

| Feature | Min Kernel | Notes |
|---------|------------|-------|
| Basic BPF | 3.18+ | Ancient; insufficient for Coral |
| kprobes/kretprobes | 4.1+ | Core tracing capability |
| BPF maps (hash, array) | 4.1+ | Required for aggregation |
| Perf event arrays | 4.4+ | For CPU profiling |
| BPF_PROG_TYPE_TRACEPOINT | 4.7+ | Efficient HTTP tracing |
| BTF (CO-RE support) | 5.2+ | **Recommended minimum** |
| BPF ring buffer | 5.8+ | Better than perf arrays |
| CAP_BPF capability | 5.8+ | Safer than CAP_SYS_ADMIN |

**Recommended**: Linux 5.8+ with BTF enabled (CO-RE portable bytecode).

**Detection approach**:
- Parse kernel version from `uname`
- Check BTF support via `/sys/kernel/btf/vmlinux` existence
- Verify `CAP_BPF` or `CAP_SYS_ADMIN` capabilities
- Map collectors to capability requirements based on kernel version

**Distro-specific considerations**:
- Ubuntu 20.04+ (5.4 kernel): Partial support; CO-RE backported in some cases.
- RHEL 8+ (4.18 kernel): Red Hat backports eBPF features; special detection needed.
- Alpine Linux: Stripped kernels may lack BTF; ship fallback non-CO-RE programs.

### Resource Limits & Safety Guarantees

Unbounded eBPF collection can destabilize hosts. Enforce strict limits:

**Per-collector limits**:
- **Duration**: Max 300s for on-demand collectors (auto-stop).
- **Sampling rate**: Bounded by collector type (e.g., CPU profiling ≤ 99 Hz).
- **Event buffer**: Max 64K events (older events dropped with metric increment).
- **BPF map sizes**: Max 10K entries for hash maps (prevent memory exhaustion).

**Per-agent limits** (colony-enforced):
- **Concurrent collectors**: Max 3 simultaneous active collectors.
- **Memory**: Max 512 MB total for all eBPF maps/buffers.
- **CPU**: Max 5% CPU time across all BPF programs (measured via cgroup stats).
- **Network**: Max 10 MB/s event streaming to colony (back-pressure applied).

**Failure handling**:
- If verifier rejects program: Log error, report to colony, continue without eBPF.
- If probe attachment fails: Retry 3x with exponential backoff, then disable collector.
- If event buffer overflows: Increment `ebpf_events_dropped` metric, sample less aggressively.
- If CPU/memory quota exceeded: Auto-disable lowest-priority collector, alert operator.

**Kernel-level safeguards**:
- BPF verifier ensures programs terminate (no unbounded loops).
- Instruction count limits (1M instructions on modern kernels).
- Stack size limits (512 bytes).
- No kernel pointer leaks to userspace.

### Dependencies Clarification

This RFD depends on RFDs 007, 011, and 012. Here's why:

**RFD 007 (CPU profiling)**:
- Establishes profiling infrastructure (RPC, storage, CLI patterns).
- eBPF CPU profiling extends this with kernel-level collection.
- Can be implemented independently, but better UX if integrated.

**RFD 011 (Multi-service agents)**:
- Defines agent architecture for observing multiple services.
- eBPF collectors need to multiplex across multiple processes/containers.
- **Not a hard blocker**: eBPF can work with single-service agents initially.

**RFD 012 (Kubernetes node agents)**:
- Node-level agents have full host privileges (required for eBPF).
- K8s API integration provides pod metadata (labels, namespaces) for filtering.
- **Not a hard blocker**: eBPF can work with sidecar agents (requires privileged
  mode), but node agents are the natural fit.

**Recommended implementation order**:
1. RFD 007 (profiling) → establishes patterns.
2. RFD 012 (node agents) → provides privileged execution context.
3. RFD 013 (eBPF) → leverages both.
4. RFD 011 (multi-service) → enhances eBPF with cross-service correlation.

**Alternative**: Implement eBPF in privileged sidecar mode first (simpler),
migrate to node agents later (better resource efficiency).

## Implementation Plan

### Phase 1: Foundations

- [ ] Package curated CO-RE eBPF programs and loader scaffolding.
- [ ] Define RPC messages for starting/stopping collectors and streaming results.
- [ ] Extend registry/storage schema for eBPF artefacts (DuckDB tables).

### Phase 2: Agent Integration

- [ ] Implement eBPF manager in agent (load/unload, map polling).
- [ ] Support capability checks (`CAP_BPF`, `CAP_SYS_ADMIN`, kernel version
      gating).
- [ ] Implement aggregation pipeline per collector (histograms, top stacks).
- [ ] Handle fallback to user-space sampling when eBPF unavailable.

### Phase 3: Colony & Control Plane

- [ ] Add colony RPC handlers (`StartEbpfCollector`, `StopEbpfCollector`,
      streaming `EbpfEvent`).
- [ ] Persist summaries in DuckDB (`ebpf_http_latency`, `ebpf_cpu_flamegraph`,
      etc.).
- [ ] Update AI analysis pipeline to reference eBPF datasets.

### Phase 4: CLI / MCP UX

- [ ] Extend `coral tap` with eBPF data source flags (`--http-latency`,
      `--cpu-profile`, `--tcp-metrics`, `--analysis`).
- [ ] Implement `coral query ebpf` for historical data retrieval.
- [ ] Add AI query pattern matching to auto-select eBPF collectors.
- [ ] Add MCP tool definitions: `coral_start_tap` (with eBPF options),
      `coral_get_ebpf_summary`, `coral_query_performance`.
- [ ] Update `coral ask` to automatically trigger eBPF collection for
      performance-related queries.

### Phase 5: Security & Hardening

- [ ] Enforce collector allowlist and duration limits.
- [ ] Add audit logging for collector lifecycle events.
- [ ] Support observe-only mode that excludes privileged collectors.
- [ ] Provide kernel compatibility matrix and detection.

### Phase 6: Testing & Documentation

- [ ] Unit tests: config parsing, aggregation math, error handling.
- [ ] Integration tests: run collectors in Kind/minikube, validate outputs.
- [ ] Performance tests: measure overhead on representative workloads.
- [ ] Documentation: README/USAGE updates, troubleshooting, kernel requirements.

## API Changes

### Protobuf (`proto/coral/mesh/v1/ebpf.proto`)

```protobuf
syntax = "proto3";
package coral.mesh.v1;

import "google/protobuf/duration.proto";
import "google/protobuf/timestamp.proto";

enum EbpfCollectorKind {
  EBPF_COLLECTOR_KIND_UNSPECIFIED = 0;
  EBPF_COLLECTOR_KIND_HTTP_LATENCY = 1;
  EBPF_COLLECTOR_KIND_CPU_PROFILE = 2;
  EBPF_COLLECTOR_KIND_SYSCALL_STATS = 3;
  EBPF_COLLECTOR_KIND_TCP_METRICS = 4;
}

message StartEbpfCollectorRequest {
  string agent_id = 1;
  string service_name = 2;  // optional; limit to specific workload
  EbpfCollectorKind kind = 3;
  map<string, string> config = 4;         // collector-specific options
  google.protobuf.Duration duration = 5;  // optional
}

message StartEbpfCollectorResponse {
  string collector_id = 1;
  google.protobuf.Timestamp expires_at = 2;
}

message StopEbpfCollectorRequest {
  string collector_id = 1;
}

message StopEbpfCollectorResponse {}

message EbpfEventStreamRequest {
  string collector_id = 1;
}

message HttpLatencyHistogram {
  repeated double buckets = 1;
  repeated uint64 counts = 2;
  string unit = 3; // milliseconds
  map<string, string> labels = 4; // method, status, pod, etc.
}

message CpuProfileSample {
  repeated string stack = 1; // symbolized stack frames
  uint64 count = 2;
  map<string, string> labels = 3;
}

message EbpfEvent {
  google.protobuf.Timestamp timestamp = 1;
  string collector_id = 2;
  oneof payload {
    HttpLatencyHistogram http_latency = 10;
    CpuProfileSample cpu_profile = 11;
    // future: syscall stats, tcp metrics
  }
}
```

### CLI Commands

eBPF is integrated into existing commands, not exposed as a separate operation:

**Option 1: Explicit data sources via `coral tap`**
```bash
$ coral tap payments-api \
    --http-latency \
    --cpu-profile \
    --duration 60s

🔍 Tap session started (id: tap-01H...)
📊 Data sources: packets, http-latency (eBPF), cpu-profile (eBPF)

[Live tail of aggregated results...]

Service: payments-api
HTTP Latency (last 60s):
  P50: 45ms  P95: 120ms  P99: 240ms
  Status codes: 200 (92%), 500 (6%), 429 (2%)

CPU Profile:
  Top functions:
    payment.ValidateCard: 34%
    json.Marshal: 18%
    http.ServeHTTP: 12%

✓ Session completed. Full data saved to: ./tap-sessions/tap-01H.../
```

**Option 2: Smart analysis mode (AI picks data sources)**
```bash
$ coral tap payments-api --analysis latency

🤖 AI selecting collectors: http-latency (eBPF), tcp-metrics (eBPF)
🔍 Tap session started...

[Results show breakdown of latency by layer]
```

**Option 3: AI-driven queries (most user-friendly)**
```bash
$ coral ask "Why is payments-api slow right now?"

🤖 Analyzing payments-api performance...
📊 Starting eBPF collectors: http-latency, cpu-profile
⏱️  Collecting data for 30s...

Analysis:
- P95 latency is 340ms (baseline: 80ms)
- 67% of time spent in payment.ValidateCard
- Function calls external API with 250ms average response time
- Recommendation: Check card validation service health

Evidence:
- eBPF HTTP latency histogram: ./evidence/http-latency-2025-10-31-14-30.json
- eBPF CPU profile: ./evidence/cpu-profile-2025-10-31-14-30.svg
```

**Querying historical eBPF data**
```bash
$ coral query ebpf http-latency payments-api --since 1h

Service: payments-api (last 1 hour)
P50: 42ms → 48ms (+14%)
P95: 95ms → 120ms (+26%)
P99: 180ms → 240ms (+33%)

Top routes by latency:
  POST /validate: P95 = 180ms
  POST /charge:   P95 = 140ms
  GET /status:    P95 = 8ms
```

### Rationale: No Separate `coral ebpf` Command

**Why NOT have a dedicated `coral ebpf` command?**

1. **Violates unified operations principle**: eBPF is a data source (like packet
   capture, logs, metrics), not a user operation. Users want "debug my app," not
   "attach eBPF probes."

2. **Fragments observability UX**: Having separate commands for each data source
   (`coral ebpf`, `coral packets`, `coral logs`) forces users to know
   implementation details. `coral tap` provides one interface for all debugging.

3. **Doesn't scale**: Future data sources (eBPF XDP, cgroup stats, kernel
   tracepoints) would each need their own commands. That's 5+ commands for
   observability.

4. **AI-first architecture**: `coral ask "Why slow?"` should transparently pick
   eBPF when useful. Users shouldn't need to know eBPF exists.

5. **MCP integration**: Claude Desktop already orchestrates via MCP tools. Exposing
   low-level `coral ebpf start/stop` commands is plumbing leakage.

**Counter-argument**: "Power users want direct control over eBPF collectors."

**Response**: Power users can:
- Use `coral tap` flags for explicit control (`--http-latency`).
- Configure continuous collectors in agent config.
- Query historical data via `coral query ebpf`.

This gives control without a separate command tree.

**Design decision**: eBPF is integrated into existing workflows (`tap`, `ask`,
`query`), not exposed as a standalone operation. This keeps the CLI simple and
aligned with Coral's "unified operations" philosophy.

### Configuration Changes

**Agent config** (`agent-config.yaml`):
- New `ebpf.enabled` flag.
- `ebpf.continuous_collectors` for always-on low-overhead collectors.
- `ebpf.on_demand_collectors` for intensive collectors triggered via tap/AI.
- `ebpf.limits` for safety bounds (CPU, memory, concurrency).

**Colony config** (`colony-config.yaml`):
```yaml
storage:
  ebpf:
    # Retention by collector type
    retention:
      http_latency: 7d
      tcp_metrics: 30d      # lightweight, keep longer
      cpu_profile: 24h      # expensive, short retention
      syscall_stats: 3d

    # Compression (DuckDB native)
    compression: zstd

    # Aggregation intervals for downsampling
    aggregate_after: 24h    # hourly rollups after 24h

ai:
  ebpf_collection:
    # AI decision-making for collector selection
    auto_select: true

    # Query patterns that trigger eBPF
    triggers:
      - pattern: "slow|latency|performance"
        collectors: ["http_latency", "cpu_profile"]
      - pattern: "network|timeout|connection"
        collectors: ["tcp_metrics"]
      - pattern: "security|anomaly|intrusion"
        collectors: ["syscall_stats"]

    # Default collection duration for AI-triggered sessions
    default_duration: 30s
    max_duration: 300s
```

### Integration with Existing Profiling (RFD 007)

eBPF complements but does not replace SDK-based profiling:

| Data Source | Use Case | Overhead | Requirements | Coverage |
|-------------|----------|----------|--------------|----------|
| **SDK pprof** (RFD 007) | Deep Go runtime insights (goroutines, heap) | 1-3% | SDK integration | Application internals |
| **eBPF CPU profile** | Cross-language CPU profiling | 2-5% | Kernel 5.8+, CAP_BPF | System-wide |
| **eBPF HTTP latency** | Protocol-level request timing | 0.5-2% | Kernel 4.7+ | Network layer |

**When to use each**:
- **SDK only**: For applications with SDK integration needing Go-specific data
  (heap profiles, goroutine stacks, block profiles).
- **eBPF only**: For legacy/third-party services without SDK, or multi-language
  stacks (Go + Python + Node.js).
- **Both simultaneously**: Maximum visibility—SDK for app internals, eBPF for
  system/network layer. Safe to run concurrently; combined overhead ~5-8%.

**Correlation example**:
```bash
$ coral tap payments-api --cpu-profile --pprof

🔍 Collecting: eBPF CPU profile + Go pprof heap/goroutines

Results:
eBPF shows: 34% CPU in payment.ValidateCard
pprof shows: ValidateCard allocates 2GB/s, causing GC pressure

Diagnosis: Memory allocation is causing CPU overhead via GC.
Recommendation: Reduce allocations in ValidateCard hot path.
```

### Symbolization & Stack Unwinding

eBPF provides raw instruction pointers; symbolization converts these to human-readable function names.

**Language support**:
- **Go/C/C++/Rust**: Parse ELF symbol tables and DWARF debug info from binaries
- **Python/Node.js/Ruby**: Interpreted languages require language-specific unwinders (Phase 2)
- **Stripped binaries**: Show module offsets (`/usr/bin/app+0x12af0`) when symbols unavailable

**Container environments**:
- Access container filesystems via runtime API (Docker, containerd)
- Parse symbols from container image layers
- Fallback: manual symbol upload via `coral symbols upload <service>`

**See `docs/EBPF_IMPLEMENTATION_GUIDE.md` for detailed symbolization implementation.**

### DuckDB Storage Schema

Agents stream aggregated eBPF data to colony; colony persists in DuckDB.

**HTTP Latency Table**:
```sql
CREATE TABLE ebpf_http_latency (
  timestamp TIMESTAMPTZ NOT NULL,
  agent_id VARCHAR NOT NULL,
  service_name VARCHAR NOT NULL,
  http_method VARCHAR(10),
  http_route VARCHAR(255),        -- extracted or hashed
  http_status SMALLINT,
  bucket_ms DOUBLE NOT NULL,       -- histogram bucket (milliseconds)
  count BIGINT NOT NULL,           -- events in this bucket
  labels MAP(VARCHAR, VARCHAR),    -- pod, namespace, etc.
  PRIMARY KEY (timestamp, agent_id, service_name, http_method, http_route, http_status, bucket_ms)
);

-- Indexes for common queries
CREATE INDEX idx_http_latency_service_time ON ebpf_http_latency (service_name, timestamp DESC);
CREATE INDEX idx_http_latency_status ON ebpf_http_latency (http_status, timestamp DESC);
```

**CPU Profile Table**:
```sql
CREATE TABLE ebpf_cpu_profile (
  timestamp TIMESTAMPTZ NOT NULL,
  agent_id VARCHAR NOT NULL,
  service_name VARCHAR NOT NULL,
  stack_hash UBIGINT NOT NULL,    -- fast grouping
  stack VARCHAR[] NOT NULL,        -- symbolized stack frames (top-to-bottom)
  sample_count BIGINT NOT NULL,
  labels MAP(VARCHAR, VARCHAR),
  PRIMARY KEY (timestamp, agent_id, service_name, stack_hash)
);

-- Index for flamegraph generation
CREATE INDEX idx_cpu_profile_service_time ON ebpf_cpu_profile (service_name, timestamp DESC);
```

**TCP Metrics Table**:
```sql
CREATE TABLE ebpf_tcp_metrics (
  timestamp TIMESTAMPTZ NOT NULL,
  agent_id VARCHAR NOT NULL,
  service_name VARCHAR NOT NULL,
  local_addr INET,
  remote_addr INET,
  retransmits BIGINT,
  rtt_us BIGINT,                   -- RTT in microseconds
  connection_resets BIGINT,
  labels MAP(VARCHAR, VARCHAR),
  PRIMARY KEY (timestamp, agent_id, service_name, local_addr, remote_addr)
);
```

**Syscall Stats Table**:
```sql
CREATE TABLE ebpf_syscall_stats (
  timestamp TIMESTAMPTZ NOT NULL,
  agent_id VARCHAR NOT NULL,
  service_name VARCHAR NOT NULL,
  syscall_name VARCHAR(32) NOT NULL,
  call_count BIGINT NOT NULL,
  error_count BIGINT,
  total_duration_us BIGINT,        -- total time in syscalls
  labels MAP(VARCHAR, VARCHAR),
  PRIMARY KEY (timestamp, agent_id, service_name, syscall_name)
);
```

**Retention policies** (by collector type):
- `ebpf_http_latency`: 7 days
- `ebpf_tcp_metrics`: 30 days (lightweight, longer retention)
- `ebpf_cpu_profile`: 24 hours (expensive, short retention)
- `ebpf_syscall_stats`: 3 days

**Data management**: Automated retention cleanup runs daily; detailed query patterns and downsampling strategies in `docs/EBPF_IMPLEMENTATION_GUIDE.md`.

### Rollout & Deployment Strategy

**Phase 1: Opt-in beta** (colony config):
```yaml
ebpf:
  rollout:
    mode: opt_in
    enabled_agents: ["agent-prod-01", "agent-staging-*"]
```

**Phase 2: Gradual rollout**:
- Enable continuous collectors (low overhead) for 10% of agents.
- Monitor CPU/memory metrics; expand to 50%, then 100%.
- On-demand collectors available to all agents from day one (user-triggered).

**Phase 3: Default-on with feature flags**:
```yaml
ebpf:
  enabled: true
  continuous_collectors:
    - name: tcp_metrics      # very low overhead, always on
      auto_enable: true
```

**Backwards compatibility**:
- Agents without eBPF support report `ebpf_supported: false` in heartbeat.
- Colony skips eBPF-related RPCs for these agents.
- CLI/MCP gracefully handle "eBPF not available" errors:
  ```
  ⚠️  payments-api: eBPF not supported (kernel 4.4.0)
  📊 Falling back to: packet capture only
  ```

**Mixed environment handling**:
- AI queries collect from subset of agents with eBPF support.
- Results annotated with coverage: "eBPF data from 12/20 agents".

## Testing Strategy

### Unit Tests

- Validate collector config parsing (duration, filters).
- Histogram and flamegraph aggregation logic.
- Error handling when kernel capabilities missing.

### Integration Tests

- Run `http_latency` collector against a test HTTP service, verify histograms.
- Run `cpu_profile` on CPU-bound workload, ensure top stacks match expectations.
- Ensure collectors stop automatically at duration expiry and release resources.

### E2E Tests

- Full CLI workflow: start collector via CLI, fetch results, query via MCP.
- Combine `coral tap` with `--ebpf` flags; validate outputs appear in audit logs.
- Failure scenarios: unsupported kernel, permission denied, service not found.

## Security Considerations

- eBPF requires elevated privileges; ensure node agents/sidecars only run
  collectors when authorized (capability checks, ACLs).
- Prevent unbounded data capture—enforce duration/timeouts and size budgets.
- Provide observe-only mode that excludes collectors needing `SYS_ADMIN`.
- audit logging for collector lifecycle; include operator identity and filters.
- Document kernel versions and hardenings (e.g., BPF LSM) that may block probes.

## Future Enhancements

- Dynamic collector marketplace with signed BPF objects.
- User-defined BPF (sandboxed) with policy enforcement.
- Continuous low-overhead collectors for anomaly detection feeding Reef.
- Integration with security detections (e.g., syscall anomaly alerts).

## Appendix

### Collector Catalogue (Initial)

| Collector        | Type          | Output                      | Permissions Required |
|------------------|---------------|-----------------------------|----------------------|
| `http_latency`   | tracepoint    | Latency histogram per route | `BPF`, `NET_ADMIN`   |
| `cpu_profile`    | perf event    | CPU stack samples           | `BPF`, `SYS_ADMIN`*  |
| `syscall_stats`  | kprobe        | Syscall counts per process  | `BPF`                |
| `tcp_metrics`    | kprobe/kretprobe | RTT, retransmits, resets | `BPF`, `NET_ADMIN`   |
| `file_io` (future) | tracepoint | Read/write latency          | `BPF`                |

\* observe-only mode disables collectors requiring `SYS_ADMIN`.

### Symbolization & Storage

- Agents perform stack unwinding and symbolization using DWARF from containers
  (if available) or `/proc/<pid>/maps`.
- Summaries stored in DuckDB tables:
  - `ebpf_http_latency (timestamp, service, route, status, bucket, count)`
  - `ebpf_cpu_profile (timestamp, service, stack_hash, stack, count)`
- Large artefacts (flamegraphs) optionally exported as JSON folded stacks.

### Failure Modes & Error Handling

Comprehensive error handling ensures eBPF failures don't block operations:

**Verifier rejection**: Log verifier output, disable collector, fall back to alternative data sources.

**Probe attachment failure**: Retry with exponential backoff, try alternative probe points (e.g., `__tcp_sendmsg` vs `tcp_sendmsg`), disable collector if all attempts fail.

**Symbolization failure**: Show module offsets for stripped binaries, provide `coral symbols upload` command for manual symbol upload.

**Event buffer overflow**: Increment dropped events metric, dynamically reduce sampling rate, alert if drop rate exceeds threshold.

**Resource quota exceeded**: Disable lowest-priority collectors (continuous > on-demand > user-triggered), re-enable when quota allows.

**Kernel version incompatibility**: Detect at startup, report capabilities to colony, provide clear CLI fallback messages.

**Detailed error handling patterns and code examples in `docs/EBPF_IMPLEMENTATION_GUIDE.md`.**

