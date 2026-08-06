---
rfd: "078"
title: "Trace-Driven Profiling - Core Infrastructure"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "036", "070", "072", "077" ]
database_migrations: [ ]
areas: [ "agent", "profiling", "tracing", "observability" ]
---

# RFD 078 - Trace-Driven Profiling - Core Infrastructure

**Status:** 🎉 Implemented

## Summary

Enable request-level performance correlation by linking distributed traces (RFD
036) with CPU/memory profiling data (RFD 070, 072, 077). This RFD implements the
core infrastructure for correlating profile samples with trace spans using
query-time joins, without requiring any changes to the eBPF profiler.

**Core capabilities:**

- **Process-Level Trace Correlation**: Correlate CPU/memory profile samples with
  the trace span that was active during their collection, using the process PID
  carried in Beyla's OTLP telemetry
- **Request-Level Flame Graphs**: Query CPU/memory profiles for specific trace
  IDs by joining on `(process_pid, time_window)`
- **Cross-Service Correlation**: Per-service flame graphs for multi-service
  traces
- **Storage & Query API**: Persistent storage and efficient retrieval by trace
  ID

This provides the foundation for request-level performance debugging. Advanced
features (triggered profiling, comparative analysis, LLM integration) are
deferred to RFD 080.

## Problem

### Current Limitations

Coral's existing observability tools operate at different granularities:

**Distributed Traces (RFD 036):**

- Show request flow across services (which services were called, in what order)
- Measure time spent in each service (span duration)
- Cannot explain **why** a service was slow (CPU? Database? Lock contention?)

**CPU/Memory Profiling (RFD 070, 072, 077):**

- Show aggregate CPU/memory consumption across all requests
- Identify hot functions in the codebase
- Cannot isolate profiling data for **specific slow requests**

**Example Problem:**

```
User: "Why did checkout request abc123 take 5 seconds?"

Current approach:
1. Query trace abc123 → spent 4.5s in payment-service
2. Run CPU profile on payment-service → many functions shown
3. Manual correlation: Which samples correspond to request abc123?
4. Guesswork: Hope the aggregate profile reflects the slow request

Problem: The aggregate profile shows ALL requests. If 99% of requests are fast
(50ms) and 1% are slow (5s), the profile is dominated by fast requests.
The slow request's bottleneck may not appear in top-5 hotspots.
```

### Why This Matters

**Production Debugging:**

- Operators need to debug **specific** slow requests reported by users
- Aggregate profiles are insufficient when the issue affects <1% of traffic
- Example: "User X's checkout failed—why?" requires request-level analysis

**Cross-Service Debugging:**

- A slow trace spans multiple services (frontend → API → database)
- Need: Per-service flame graphs correlated with the same trace ID
- Cannot answer: "Which service's code caused the slowdown?"

**Historical Analysis:**

- Continuous trace tagging enables historical debugging
- Debug slow requests after they occur using stored profiles
- Correlate performance issues with specific deployments or time periods

### Use Cases Addressed

1. **Single Request Diagnosis**
   - "Why did this specific checkout request (trace abc123) take 5 seconds?"
   - Current: Run aggregate profile, guess which function corresponds to the
     slow trace
   - Solution: Profile samples filtered to the span's process and time window

2. **Cross-Service Attribution**
   - "Trace abc123 spans 5 services—which service's code caused the 4s delay?"
   - Current: Run separate profiles on each service, manually correlate
     timestamps
   - Solution: Unified view showing per-service flame graphs for the same trace

3. **Historical Debugging**
   - "A slow request occurred 2 hours ago—what code was running?"
   - Current: No historical profiling data for specific traces
   - Solution: Query stored profiles by trace ID for post-hoc analysis

## Solution

### Actual Beyla Integration Architecture

Beyla runs as a standalone eBPF auto-instrumentation agent. Coral does not
share Beyla's BPF maps or inject into its trace context pipeline. Instead,
Beyla forwards finished spans to Coral's OTLP ingestion endpoint:

```
Beyla (eBPF)          Coral Agent
──────────────         ─────────────────────────────────────────
HTTP handler   →  OTLP/gRPC  →  OTLPReceiver  →  beyla_traces_local
(intercepts)       spans          (stores)          (DuckDB)
```

Each OTLP resource batch includes standard resource attributes, among them
`process.pid` — the OS-level PID (TGID) of the instrumented process. The
current transformer extracts only `service.name` from resource attributes and
discards the rest. This RFD adds extraction of `process.pid` to close the gap.

### Core Correlation Approach

The eBPF profiler captures `PID` (the OS process ID, TGID) in its BPF stack key
at sample time. Beyla spans carry `process.pid` (the same TGID) as an OTLP
resource attribute. These two facts enable query-time correlation **without any
changes to the eBPF program** — though `tgid` must be threaded through the
profile storage pipeline (currently dropped during aggregation), and
`process_pid` must be extracted from OTLP. Both are within scope of this RFD:

```sql
-- "Which profile samples ran during trace abc123 in payment-svc?"
SELECT p.stack_frame_ids, p.sample_count
FROM cpu_profile_summaries p
JOIN beyla_traces t
  ON p.tgid = t.process_pid
 AND p.timestamp BETWEEN t.start_time
                     AND t.start_time + (t.duration_us * INTERVAL '1 microsecond')
WHERE t.trace_id = 'abc123...'
  AND t.service_name = 'payment-svc'
```

This approach is correct when the span's duration is long relative to
concurrent request volume — which is exactly the case worth debugging.

### Ambiguity at High Concurrency

This correlation is inherently **process-scoped, not goroutine-scoped**. If
`payment-svc` is handling 100 concurrent requests under the same TGID, a
profile sample at time T cannot be definitively attributed to trace `abc123`
vs. another trace active at the same moment.

**In practice this matters less than it appears:**

- Long-running spans (>100ms) dominate profile weight; the bottleneck function
  accumulates many samples and floats to the top regardless of noise
- When a single request is anomalously slow (e.g., 5s vs. 50ms median), it
  accounts for a disproportionate share of samples in its time window
- The operator already knows which service and time window to focus on from the
  trace; the flame graph narrows it further to which function

**Goroutine-level precision** (tagging samples with the exact goroutine serving
the request) would eliminate ambiguity, but requires coupling with Beyla's
internal BPF maps or duplicating its HTTP interception kprobes — neither of
which fits Coral's current architecture. This is deferred to future work.

### Key Features

#### 1. Trace-Correlated Profile Query

**How it works:**

- Continuous profiling (19Hz, RFD 072) captures `PID` in the BPF stack key but
  currently drops it during aggregation — this RFD adds `tgid` as a stored
  field so the join key is available at query time
- Beyla spans now store `process_pid` (extracted from OTLP resource attributes)
- Colony joins the two tables at query time for each `trace_id` lookup

**Query:**

```bash
coral query trace-profile abc123def456
# Returns CPU flame graph for samples that ran during that trace's spans
```

#### 2. Cross-Service Trace Profiling

**How it works:**

- A single trace spans multiple services, each with its own Beyla span and
  `process_pid`
- The Colony queries each agent's profile data for its respective `process_pid`
  and time window, then assembles per-service flame graphs

**Example:**

```bash
coral query trace-profile --trace-id abc123def456 --all-services

# Returns:
# - frontend-svc (pid 1234): 200ms (10% of total trace time)
# - payment-svc  (pid 5678): 4.5s (90% of total trace time) ← bottleneck
#   └─ validateSignature: 4.2s (93% of payment-svc time)
```

#### 3. Memory Profiling Correlation (RFD 077 Integration)

The same `(process_pid, time_window)` join applies to memory allocation
profiling (RFD 077). No changes to the memory profiler are required.

```bash
coral query memory-profile --trace-id abc123def456

# Shows memory allocations during that specific request's time window:
# - 500MB in processOrder → unmarshaling large JSON payload
# - 200MB in validateInput → regex compilation on each request
```

## Component Changes

### 0. Profile Storage — `tgid` Addition

The continuous profiling pipeline (RFD 072) does not currently persist the OS
process ID through to storage. The BPF stack key captures `PID` (TGID) at
sample time, but it is dropped during aggregation into `cpu_profile_samples_local`.
This RFD adds `tgid` as a grouping dimension so the colony-side
`(tgid, time_window)` join is possible.

**Agent: `cpu_profile_samples_local` schema (`internal/agent/profiler/storage.go`):**

```sql
ALTER TABLE cpu_profile_samples_local ADD COLUMN tgid INTEGER NOT NULL DEFAULT 0;
-- Update PRIMARY KEY to include tgid:
-- PRIMARY KEY (timestamp, service_id, build_id, tgid, stack_hash)
CREATE INDEX IF NOT EXISTS idx_cpu_profile_samples_tgid
    ON cpu_profile_samples_local (tgid, timestamp);
```

**`ProfileSample` Go struct (`internal/agent/profiler/storage.go`):**

```go
type ProfileSample struct {
    // ... existing fields ...
    TGID uint32  // OS process ID (TGID) of the profiled process.
}
```

**`CPUProfileSample` proto (`proto/coral/agent/v1/debug.proto`):**

```protobuf
message CPUProfileSample {
  // ... existing fields (1–6) ...
  uint32 tgid = 7;  // OS process ID (TGID) at time of sample.
}
```

**Colony: `cpu_profile_summaries` table (`internal/colony/database/schema.go`):**

```sql
ALTER TABLE cpu_profile_summaries ADD COLUMN tgid INTEGER NOT NULL DEFAULT 0;
-- tgid becomes part of the composite key so per-process samples remain
-- distinct after aggregation.
CREATE INDEX IF NOT EXISTS idx_cpu_profile_summaries_tgid
    ON cpu_profile_summaries (tgid, timestamp DESC);
```

**`CPUProfileSummary` Go struct (`internal/colony/database/cpu_profiles.go`):**

```go
type CPUProfileSummary struct {
    // ... existing fields ...
    TGID uint32 `duckdb:"tgid,pk,immutable"`  // OS process ID (TGID).
}
```

### 1. Beyla Transformer (Agent)

**`internal/agent/beyla/transformer.go`:**

- Extract `process.pid` from OTLP resource attributes alongside `service.name`
- Pass `process_pid` through to the `BeylaTraceSpan` proto message

**`coral/mesh/v1/*.proto`:**

- Add `uint32 process_pid = N` field to `BeylaTraceSpan`

### 2. Storage Schema

**Extend `beyla_traces_local` Table (Agent):**

```sql
ALTER TABLE beyla_traces_local
    ADD COLUMN process_pid INTEGER;  -- OS PID (TGID) of instrumented process

CREATE INDEX IF NOT EXISTS idx_beyla_traces_process_pid
    ON beyla_traces_local (process_pid, start_time DESC);
```

**Extend `beyla_traces` Table (Colony):**

```sql
ALTER TABLE beyla_traces
    ADD COLUMN process_pid INTEGER;

CREATE INDEX IF NOT EXISTS idx_beyla_traces_process_pid
    ON beyla_traces (process_pid, start_time DESC);
```

**No changes to `cpu_profile_summaries` or `memory_profiles` tables.** Profile
samples already carry `tgid`; the join key is provided by the spans table.

### 3. Colony Query API

**New RPC: `QueryTraceProfile`**

```protobuf
// proto/coral/colony/v1/colony.proto

message QueryTraceProfileRequest {
    string trace_id = 1;  // Required: 32-char hex trace ID

    // Optional: Filter by service (for multi-service traces)
    string service_id = 2;

    // Profile type
    ProfileType profile_type = 3;  // CPU or MEMORY
}

enum ProfileType {
    PROFILE_TYPE_UNSPECIFIED = 0;
    PROFILE_TYPE_CPU = 1;
    PROFILE_TYPE_MEMORY = 2;
}

message QueryTraceProfileResponse {
    string trace_id = 1;

    // Per-service flame graphs
    repeated ServiceProfile service_profiles = 2;

    // Trace metadata (from beyla_traces table)
    TraceMetadata trace_metadata = 3;
}

message ServiceProfile {
    string service_id = 1;
    string service_name = 2;

    // Span metadata
    int64 span_duration_ms = 3;
    string span_name = 4;  // e.g., "POST /api/checkout"

    // Profiling data
    repeated Hotspot top_hotspots = 5;
    int64 total_samples = 6;
    int64 cpu_time_ms = 7;  // Estimated CPU time during span
}

message TraceMetadata {
    string trace_id = 1;
    google.protobuf.Timestamp start_time = 2;
    int64 total_duration_ms = 3;
    repeated string services = 4;
    int32 span_count = 5;
    int32 status_code = 6;
}
```

### 4. CLI Commands

**Query trace profile:**

```bash
coral query trace-profile <trace-id> [flags]

Flags:
  --service string       Filter by service name
  --profile-type string  cpu or memory (default: cpu)
  --format string        Output format: flamegraph, json, text (default: flamegraph)
```

## Implementation Plan

### Phase 1: Establish join keys in storage

**Goals:** Capture both sides of the join: `process_pid` in Beyla spans and
`tgid` in CPU profile samples.

**Profile storage — add `tgid`:**

- [x] Add `tgid` column to `cpu_profile_samples_local` schema; update PRIMARY
      KEY to include `tgid`
- [x] Add `tgid` field to `ProfileSample` struct and propagate from BPF stack
      key through `StoreSample`
- [x] Add `tgid` field (`uint32 tgid = 7`) to `CPUProfileSample` proto; expose
      via `QueryCPUProfileSamples` response
- [x] Update colony CPU profile poller (`cpu_profile_poller.go`) to propagate
      `tgid` into `CPUProfileSummary`; add `tgid` to colony schema and struct

**Beyla span — add `process_pid`:**

- [x] Add `process_pid` field to `BeylaTraceSpan` proto message
- [x] Extract `process.pid` from OTLP resource attributes in
      `transformer.go:TransformTraces`
- [x] Extract `process.pid` in `otlp_receiver.go:Export` for the gRPC path
      (Beyla's SpanHandler route)
- [x] Pass `process_pid` through `StoreOTLPSpan` and `StoreTrace` in
      `beyla/storage.go`
- [x] Store `process_pid` in `beyla_traces_local` schema

**Deliverable:** Both join keys present in storage — `tgid` in profile samples
and `process_pid` in Beyla spans.

### Phase 2: Colony — propagate `process_pid` and wire the join

**Goals:** Propagate `process_pid` to the Colony's `beyla_traces` table and
implement the join query. (Colony `tgid` propagation is already done in Phase 1.)

- [x] Add `process_pid` column to colony `beyla_traces` table
- [x] Create index on `(process_pid, start_time DESC)`
- [x] Update colony Beyla poller to propagate `process_pid` from agent spans
- [x] Implement `QueryTraceProfile` join query in Colony database layer joining
      `beyla_traces` with `cpu_profile_summaries` on `(tgid = process_pid,
      timestamp BETWEEN start_time AND start_time + duration_us)`

**Deliverable:** Colony can execute trace-to-profile joins efficiently.

### Phase 3: Query API Implementation

**Goals:** Expose trace-correlated profiles via RPC

- [x] Implement `QueryTraceProfile` RPC in Colony
- [x] Join `beyla_traces` with `cpu_profile_summaries` on `(process_pid, time_window)`
- [x] Implement per-service flame graph aggregation across agents
- [x] Add metadata enrichment (span duration, service names)

**Deliverable:** `coral query trace-profile <trace-id>` returns request-level
flame graphs

### Phase 4: CLI Integration & Testing

**Goals:** User-facing commands and validation

- [x] Implement `coral query trace-profile` command
- [x] Add text-based flame graph rendering with trace metadata
- [ ] Add JSON/CSV export formats (deferred to future work)
- [ ] Unit tests for `process_pid` extraction from OTLP resource attributes (deferred)
- [ ] Integration tests: multi-service trace with per-service profiles (deferred)
- [ ] E2E test: query trace profile and verify per-service attribution (deferred)
- [ ] Documentation: user guide for trace profiling workflows (deferred)

**Deliverable:** Production-ready core trace profiling

## Use Case Example

### Single Request Diagnosis

**Scenario:** User reports slow checkout (trace abc123def456)

```bash
# Query trace profile for the slow service
$ coral query trace-profile abc123def456 --service payment-svc

Top CPU Hotspots (payment-svc pid:5678, trace abc123def456, span: 4.5s):
  93.3%  4.2s  validateSignature → crypto/rsa.VerifyPKCS1v15
   3.1%  140ms runtime.gcBgMarkWorker
   1.8%  80ms  serializeResponse

Note: samples are process-scoped to pid 5678 during [T, T+4.5s].
      Concurrent requests in the same process contribute noise.

# Identified bottleneck: 4.2s spent in RSA signature validation
```

## Performance Considerations

### No Profiler Overhead

Unlike the previously considered approach of tagging each eBPF sample with a
trace ID in the kernel, this design adds zero overhead to the profiler. The
join happens at query time in DuckDB — infrequent, analyst-driven queries
against already-stored data.

### Query Performance

- **Trace lookup:** <10ms (indexed by `trace_id`)
- **Profile join:** <200ms for single span (indexed by `process_pid, timestamp`)
- **Multi-service join:** scales linearly with agent count, executed in parallel

### Storage Impact

No additional storage per sample. The only addition is a 4-byte `process_pid`
column in `beyla_traces_local` and `beyla_traces`.

## Limitations

This RFD implements a best-effort correlation, not a precise per-request
attribution system. Operators should understand these failure modes before
relying on results.

### 1. Clock Source Mismatch

The eBPF profiler timestamps samples using the kernel's perf event clock
(`CLOCK_MONOTONIC`). Beyla records span start/end times using Go's
`time.Now()` (`CLOCK_REALTIME`). These two clocks can diverge:

- On VMs: hypervisor clock adjustments introduce milliseconds of drift
- In containers: `CLOCK_REALTIME` is subject to NTP adjustments that do not
  affect the monotonic clock
- Typical drift: 1–10ms, but can exceed 50ms during NTP corrections

A 10ms drift on a 100ms span meaningfully shifts which samples fall inside
the join window, potentially missing or misattributing the bottleneck sample.

**Mitigation:** When displaying results, include a confidence note if
`span.duration_us < 200ms`. Clock drift is less significant for the spans
operators most need to debug (>500ms outliers).

### 2. Sampling Rate vs Span Duration

At 19Hz, the profiler fires every ~52ms. A span shorter than ~100ms will
capture 0–1 samples, making flame graph results unreliable. Short spans
also tend to be low-latency by definition — but services exhibiting
bimodal latency (fast 95th percentile, slow 99th) often have spans in the
50–200ms range where coverage is marginal.

**Mitigation:** Surface a `sample_count` field in `ServiceProfile` and warn
the operator when `sample_count < 3`. For triggered high-frequency profiling
(99Hz), see RFD 080.

### 3. Process-Scoped, Not Goroutine-Scoped

Profile samples are attributed to the entire process (TGID) during the span's
time window. For services handling many concurrent requests under the same
TGID, samples from other goroutines appear in the flame graph alongside the
target trace's goroutine. The flame graph answers "what was this process
doing during this time window?" not "what was this specific request doing?".

**Practical impact:**
- Long-running outlier spans (>500ms, median <50ms): high signal — the
  target request dominates the sample window
- High concurrency with short spans (<100ms): lower precision — concurrent
  request noise can bury the bottleneck
- Lock contention scenarios: the blocked goroutine consumes no CPU; the
  flame graph shows the lock holder, not the waiting request

**Future mitigation:** Goroutine-level sample tagging (see Future Work).

### 4. `process.pid` Availability

Beyla sends `process.pid` as a standard OTLP resource attribute today. This
field is not guaranteed by the OTLP spec and could be absent from:

- Non-Beyla OTLP producers
- Future Beyla versions that change resource attribute population
- Beyla operating in certain container runtimes where PID namespace remapping
  causes the reported PID to differ from the host PID the profiler observes

When `process_pid` is NULL, the join degrades to service-name + time window
matching across all agents, which may return results from the wrong process
if multiple agents observe services with the same name.

## Security Considerations

**Trace ID Exposure:**

- Trace IDs may reveal request patterns or user activity
- **Mitigation:** RBAC controls on trace profile queries (same as RFD 058)
- **Mitigation:** Audit logging for all trace profile queries

**Profile Data Sensitivity:**

- Profiles may reveal code structure and execution patterns
- **Mitigation:** Profiles contain function names, not source code or data
- **Mitigation:** Same access controls as existing profiling (RFD 070, 072)

## Testing Strategy

### Unit Tests

**`process.pid` Extraction:**

- Test extraction of `process.pid` from OTLP resource attributes
- Test fallback when `process.pid` is absent (NULL `process_pid`)
- Test correct propagation through transformer → storage → colony

**Query Join:**

- Test `(tgid, time_window)` join returns correct samples
- Test multi-service aggregation
- Test NULL `process_pid` handling (graceful degradation)

### Integration Tests

**End-to-End Trace Profiling:**

1. Configure Beyla to forward spans to Coral's OTLP endpoint
2. Verify `process_pid` is stored in `beyla_traces_local`
3. Generate load; query `coral query trace-profile <id>`
4. Verify flame graph contains samples from within the span's time window

### Performance Tests

**Query Benchmarks:**

- Insert 1M spans with `process_pid`, query by `trace_id` (target: <50ms)
- Join with 10M profile samples (target: <200ms)

## Future Work

### Goroutine-Level Precision (Path to Exact Attribution)

Eliminating the process-scoped ambiguity described in Limitations requires
associating each profile sample with the specific goroutine that handled the
request, not just the process and time window. Two concrete paths exist:

**Path A: Upstream Beyla contribution (preferred)**

Beyla already maintains an internal BPF map tracking goroutine state — it must
in order to correlate HTTP handler entry with completion across Go scheduler
migrations. Since uretprobes are unreliable for GC languages like Go (the
goroutine scheduler copies stack frames during growth and the GC can preempt
goroutines mid-frame, both of which invalidate the return address trampoline
that uretprobes depend on), Beyla instead places a second uprobe on a
designated completion function in the HTTP/gRPC stack (e.g.
`net/http.(*response).finishRequest`). The map key is effectively
`(tgid, goroutine_ptr)`.

The change needed: expose this map via a pinned BPF path (e.g.
`/sys/fs/bpf/beyla/<pid>/goroutine_trace_context`) so that Coral's profiler
can read it at sample time. When the perf event fires, the profiler reads
`R14` (amd64) or `X28` (arm64) to get the current goroutine pointer, looks up
`(tgid, g_ptr)` in Beyla's map, and tags the sample with the trace ID — zero
changes to the profiler's sampling logic.

This is a small, targeted Beyla change with value beyond Coral (Pyroscope,
Parca, and other continuous profilers face the same problem). The right next
step is an upstream issue/PR with the Beyla maintainers at Grafana, not a
fork. A fork would inherit Beyla's maintenance surface: eBPF program
compatibility across kernel versions, Go version support, architecture support
(amd64/arm64) — a significant ongoing burden for a targeted capability.

**Path B: Coral SDK injection (no Beyla dependency)**

For services instrumented with the Coral SDK that propagate `context.Context`,
the SDK can write `(tgid, goroutine_ptr) → trace_id` directly into a
Coral-owned pinned BPF map at request entry, and clear it at request exit:

```go
// coral/sdk/trace.go
func TraceContext(ctx context.Context) context.Context {
    traceID := trace.SpanFromContext(ctx).SpanContext().TraceID()
    coralBPF.SetGoroutineTrace(traceID)  // writes to pinned BPF map
    return ctx
}
```

The profiler reads from this map at sample time using the current goroutine
pointer. This sidesteps Beyla entirely for SDK users and provides exact
attribution. Beyla auto-instrumentation and SDK injection can coexist: the
profiler checks the SDK map first, falls back to Beyla's map if available,
and falls back to process-scoped correlation if neither is present.

### RFD 080: Advanced Trace Analysis & AI-Driven Diagnosis

**Trace-Triggered Profiling:**

- High-frequency profiling (99Hz) activated by trace criteria
- Profile only slow/failing requests to isolate outlier bottlenecks
- Automatic trigger expiration and cleanup

**Comparative Analysis:**

- Compare flame graphs between request cohorts (slow vs fast, success vs error)
- Differential flame graphs showing functions hotter in specific cohorts
- Statistical significance testing for differences

**LLM/MCP Integration:**

- Extend `coral_query_summary` with trace-correlated profiles
- New MCP tools: `coral_query_trace_profile`, `coral_profile_trace_trigger`
- Enable AI to diagnose specific slow requests with code-level attribution

## Appendix

### How Beyla Emits `process.pid`

Beyla populates standard OpenTelemetry resource attributes on every OTLP export
batch. For Go processes, the resource includes:

```
service.name  = "payment-svc"
process.pid   = 5678          ← OS PID of the instrumented process
process.runtime.name = "go"
telemetry.sdk.name   = "beyla"
```

The current `transformer.go` reads only `service.name` (line 33). This RFD
adds extraction of `process.pid` from the same resource attribute map.

### W3C Trace Context (reference)

Beyla automatically extracts and propagates the W3C `traceparent` header:

```
traceparent: 00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01
                  └─────── trace_id (32 hex) ─────┘
```

The `trace_id` stored in `beyla_traces_local` is this 32-character hex string.
Callers of `coral query trace-profile` pass it directly.

---

## Implementation Status

**Core Capability:** 🎉 Implemented

All four phases are complete. The trace-driven profiling infrastructure is operational end-to-end.

**Implemented Components:**

- ✅ `tgid` field added to `cpu_profile_samples_local` schema (agent) and `cpu_profile_summaries` (colony); `PRIMARY KEY` includes `tgid` so per-process profiles are tracked separately.
- ✅ `process_pid` field added to `beyla_traces_local` (agent) and `beyla_traces` (colony); extracted from OTLP `process.pid` resource attribute via both `transformer.go` and `otlp_receiver.go`.
- ✅ `QueryTraceProfileCPU` join query in `internal/colony/database/trace_profile.go` joining `beyla_traces` with `cpu_profile_summaries` on `(tgid = process_pid, timestamp BETWEEN start_time AND start_time + duration_us ± 1 minute)`.
- ✅ `QueryTraceProfile` RPC implemented in `internal/colony/server/query_service.go`; returns per-service `ServiceTraceProfile` with decoded stack frames, percentages, and coverage warnings.
- ✅ `coral query trace-profile <trace-id>` CLI command in `internal/cli/query/trace_profile.go`.

**Future Work:**
- JSON/CSV export formats for `trace-profile` command.
- Unit tests for `process.pid` extraction from OTLP resource attributes.
- Integration and E2E tests for multi-service trace correlation.
- User guide / documentation.

## Dependencies

**Pre-requisites:**

- ✅ RFD 036 (Distributed Tracing) - Provides trace IDs and span metadata
- ✅ RFD 070 (On-Demand CPU Profiling) - Foundation for profiling infrastructure
- ✅ RFD 072 (Continuous CPU Profiling) - Always-on profiling with low overhead
- ✅ RFD 077 (Memory Profiling) - Memory profiling correlation

**Enables:**

- RFD 080 (Advanced Trace Analysis & AI-Driven Diagnosis)
- Request-level performance debugging for distributed systems
- Foundation for surgical debugging of production issues
