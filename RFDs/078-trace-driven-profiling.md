---
rfd: "078"
title: "Trace-Driven Profiling - Core Infrastructure"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "036", "070", "072", "077" ]
database_migrations: [ ]
areas: [ "agent", "profiling", "tracing", "observability" ]
---

# RFD 078 - Trace-Driven Profiling - Core Infrastructure

**Status:** 🚧 Draft

## Summary

Enable request-level performance correlation by linking distributed traces (RFD
036) with CPU/memory profiling data (RFD 070, 072, 077). This RFD implements the
core infrastructure for tagging profile samples with trace IDs and querying
profiles for specific requests.

**Core capabilities:**

- **Always-On Trace Tagging**: eBPF profiler tags all samples with active trace
  ID (<0.1% overhead)
- **Request-Level Flame Graphs**: Query CPU/memory profiles for specific trace
  IDs
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
    - Solution: Request-level flame graph showing exactly what ran during that
      trace

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

### Core Architecture

Extend Coral's profiling infrastructure to tag CPU/memory samples with the
active trace ID from the current execution context, enabling request-level
correlation.

**eBPF Enhancement:**

```c
// Extend sample key to include trace ID
struct sample_key {
    u32 pid;
    u32 tid;
    u64 trace_id_high;  // NEW: First 64 bits of 128-bit trace ID
    u64 trace_id_low;   // NEW: Last 64 bits
};

// Read trace ID from per-thread BPF map and tag each sample
// Implementation: internal/agent/profiling/cpu_profiler.go
```

**Trace Context Injection:**

For SDK-instrumented apps, trace ID is written to thread-local storage:

```go
// Extract trace ID from context and store for eBPF profiler
traceID := trace.SpanFromContext(ctx).SpanContext().TraceID()
setThreadTraceContext(traceID)
defer clearThreadTraceContext()
```

For Beyla auto-instrumentation, eBPF kprobes extract trace ID from HTTP/gRPC
headers and store in per-thread BPF map (see Appendix for protocol details).

### Key Features

#### 1. Trace-Tagged Profiling (Always-On)

**How it works:**

- Continuous profiling (RFD 072) now tags each sample with the active trace ID
- Zero additional overhead (trace ID read from TLS/BPF map is <10ns)
- Samples without trace context (background jobs) are tagged with trace_id=0

**Storage:**

- Extend `cpu_profile_summaries` table with optional `trace_id` column
- Index by trace_id for fast lookup: `SELECT * WHERE trace_id = 'abc123...'`

**Query:**

```bash
coral query trace-profile abc123def456
# Returns CPU flame graph for only the samples from that trace
```

#### 2. Cross-Service Trace Profiling

**How it works:**

- A single trace spans multiple services (frontend → API → database)
- Each service's eBPF profiler tags samples with the same trace ID
- Query aggregates per-service flame graphs for the same trace

**Example:**

```bash
coral query trace-profile --trace-id abc123def456 --all-services

# Returns:
# - frontend-svc: 200ms (10% of total trace time)
# - payment-svc: 4.5s (90% of total trace time) ← bottleneck identified
#   └─ validateSignature: 4.2s (93% of payment-svc time)
```

**Query Output:** Returns per-service CPU attribution with top hotspots, enabling
bottleneck identification across the distributed trace.

#### 3. Memory Profiling Correlation (RFD 077 Integration)

**How it works:**

- Same trace ID tagging applies to memory allocation profiling (RFD 077)
- Correlate memory allocations with specific requests
- Identify memory leaks or excessive allocations in slow requests

**Example:**

```bash
coral query memory-profile --trace-id abc123def456

# Shows memory allocations during that specific request:
# - 500MB allocated in processOrder → unmarshaling large JSON payload
# - 200MB allocated in validateInput → regex compilation on each request
```

### Implementation Approach

**Always-On Trace Tagging:**

- Continuous profiling (19Hz, RFD 072) tags all samples with active trace ID
- Zero additional overhead (trace ID read from thread context is <10ns)
- Enables historical request-level analysis
- Profiles stored with trace_id column, indexed for fast retrieval
- Query-time joins between traces and profiles for cross-service correlation

Advanced features (triggered profiling, comparative analysis, LLM integration)
are deferred to RFD 080.

## Component Changes

### 1. eBPF Profiler (Agent)

**CPU Profiler Extension** (`internal/agent/profiling/cpu_profiler.go`):

- Extend sample key struct to include `trace_id` (128-bit)
- Read trace ID from thread-local storage or BPF map
- Tag all samples with active trace ID (or 0 if no active trace)

**Memory Profiler Extension** (`internal/agent/profiling/memory_profiler.go`):

- Same trace ID tagging for memory allocation samples
- Correlate allocations with specific requests

**Beyla Integration** (`internal/agent/beyla/trace_context.go`):

- eBPF kprobe on HTTP/gRPC entry points to extract trace ID from headers
- Store in per-thread BPF map: `thread_trace_context`
- Clear on request completion (kretprobe)

### 2. Storage Schema

**Extend `cpu_profile_summaries` Table:**

```sql
ALTER TABLE cpu_profile_summaries
    ADD COLUMN trace_id VARCHAR(32); -- 32-char hex string (128-bit trace ID)

CREATE INDEX idx_cpu_profile_summaries_trace_id
    ON cpu_profile_summaries (trace_id, bucket_time DESC);
```

**Extend `memory_profiles` Table (RFD 077):**

```sql
ALTER TABLE memory_profiles
    ADD COLUMN trace_id VARCHAR(32);

CREATE INDEX idx_memory_profiles_trace_id
    ON memory_profiles (trace_id, timestamp DESC);
```

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

### Phase 1: eBPF Trace Context Integration

**Goals:** Tag profile samples with trace IDs

- [ ] Extend eBPF CPU profiler to read trace ID from thread-local storage
- [ ] Add `trace_id_high` and `trace_id_low` fields to sample key struct
- [ ] Implement BPF map `thread_trace_context` for per-thread trace storage
- [ ] Add eBPF kprobes for Beyla trace context extraction (HTTP/gRPC entry
  points)
- [ ] Extend memory profiler with same trace ID tagging (RFD 077 integration)

**Deliverable:** eBPF profilers tag samples with trace IDs (0 if no active
trace)

### Phase 2: Storage Schema Extensions

**Goals:** Store trace-tagged profiles

- [ ] Add `trace_id` column to `cpu_profile_summaries` table
- [ ] Add `trace_id` column to `memory_profiles` table
- [ ] Create indexes on trace_id columns for fast lookups
- [ ] Implement storage methods: `StoreProfileWithTraceID()`
- [ ] Add data migration for existing profiles (backfill trace_id = NULL)

**Deliverable:** Profiles stored with trace IDs, queryable by trace_id

### Phase 3: Query API Implementation

**Goals:** Query profiles by trace ID

- [ ] Implement `QueryTraceProfile` RPC in Colony
- [ ] Join `beyla_traces` with `cpu_profile_summaries` on trace_id
- [ ] Implement per-service flame graph aggregation
- [ ] Add metadata enrichment (span duration, service names)
- [ ] Implement DuckDB query functions for trace-profile joins

**Deliverable:** `coral query trace-profile <trace-id>` returns request-level
flame graphs

### Phase 4: CLI Integration & Testing

**Goals:** User-facing commands and validation

- [ ] Implement `coral query trace-profile` command
- [ ] Add text-based flame graph rendering with trace metadata
- [ ] Implement trace ID autocomplete (recent slow traces)
- [ ] Add JSON/CSV export formats
- [ ] Unit tests for trace ID tagging, storage, querying
- [ ] Integration tests: multi-service trace with per-service profiles
- [ ] E2E test: Query trace profile and verify per-service attribution
- [ ] Performance tests: Measure overhead of trace ID tagging (<1%)
- [ ] Documentation: User guide for trace profiling workflows

**Deliverable:** Production-ready core trace profiling with `coral query
trace-profile` command

## Use Case Example

### Single Request Diagnosis

**Scenario:** User reports slow checkout (trace abc123def456)

```bash
# Query trace profile for the slow service
$ coral query trace-profile abc123def456 --service payment-svc

Top CPU Hotspots (payment-svc, trace abc123def456):
  93.3%  4.2s  validateSignature → crypto/rsa.VerifyPKCS1v15
   3.1%  140ms runtime.gcBgMarkWorker
   1.8%  80ms  serializeResponse

# Identified bottleneck: 4.2s spent in RSA signature validation
```

**Result:** Operator quickly identifies that 93% of the slow request's time was
spent in `validateSignature`, enabling targeted investigation and fix.

## Performance Considerations

### Overhead Analysis

**Trace ID Tagging (Always-On):**

- **CPU Overhead:** <0.1% (reading 128-bit trace ID from TLS/BPF map adds
  5-10ns per sample)
- **Memory Overhead:** +16 bytes per sample (trace_id_high + trace_id_low)
- **Storage Overhead:** +32 bytes per row in `cpu_profile_summaries` (VARCHAR(32))

**Query Performance:**

- **Trace Profile Query:** <200ms for single trace (indexed by trace_id)
- **Join Cost:** Minimal (trace_id indexed in both tables)

### Storage Scaling

**Continuous Profiling with Trace Tagging:**

- **Assumption:** 1,000 req/s per service, 19Hz profiling, 5 unique stacks per
  request
- **Samples per day:** 1,000 req/s _ 86,400s _ 0.05 samples/req = 4.3M
  samples/day
- **Storage (with compression):** ~200MB/day per service (same as RFD 072)
- **Trace ID overhead:** +16 bytes/sample \* 4.3M = 69MB/day (34% increase)

**Retention Strategy:**

- **Agent:** 1 hour (same as RFD 072)
- **Colony:** 7 days for trace-tagged profiles (high-value data for debugging)
- **Colony:** 30 days for aggregate profiles (trace_id = NULL)

### Optimization Strategies

**1. Trace ID Sampling:**

- Only tag samples with trace ID for traced requests (not background jobs)
- Background jobs: trace_id = NULL, saved space

**2. Partial Trace Profiling:**

- Profile only services where span_duration > threshold (e.g., >100ms)
- Skip profiling for sub-millisecond spans (not actionable)

**3. Deferred Correlation:**

- Store profiles with trace ID, but don't join with traces until query time
- Avoid pre-computing correlations (storage-intensive)

## Security Considerations

**Trace ID Exposure:**

- Trace IDs may reveal request patterns or user activity
- **Mitigation:** RBAC controls on trace profile queries (same as RFD 058)
- **Mitigation:** Audit logging for all trace profile queries

**Profile Data Sensitivity:**

- Profiles may reveal code structure and execution patterns
- **Mitigation:** Profiles contain function names, not source code or data
- **Mitigation:** Same access controls as existing profiling (RFD 070, 072)

**Trigger Abuse:**

- Malicious users could start many high-frequency triggers to DoS the service
- **Mitigation:** Rate limit triggers (max 3 active triggers per service)
- **Mitigation:** Automatic expiration (max 10 minutes)
- **Mitigation:** RBAC controls on trigger creation

**Cross-Tenant Isolation:**

- Ensure trace profiles from one tenant are not visible to other tenants
- **Mitigation:** Service-level isolation (profiles scoped to service_id)
- **Mitigation:** Future: Tenant ID field in profiles

## Testing Strategy

### Unit Tests

**eBPF Trace Tagging:**

- Test trace ID extraction from thread-local storage
- Test trace ID extraction from HTTP headers (W3C traceparent)
- Test sample collection with and without active trace context
- Verify trace_id = 0 for background jobs

**Storage:**

- Test profile insertion with trace_id
- Test trace_id indexing and query performance
- Test NULL trace_id handling (backward compatibility)

### Integration Tests

**End-to-End Trace Profiling:**

1. Start mock service with traced requests
2. Verify eBPF profiler tags samples with trace IDs
3. Query profile by trace_id, verify flame graph matches
4. Test multi-service trace with per-service profiles

### Performance Tests

**Overhead Measurement:**

- Baseline: Service without trace profiling (0% overhead)
- With trace tagging: Measure CPU overhead (target: <0.1%)

**Storage Benchmarks:**

- Insert 1M profiles with trace IDs
- Query by trace_id (target: <50ms)
- Join with traces table (target: <200ms)

**Scalability Tests:**

- 10,000 req/s per service
- Verify profiler keeps up (no dropped samples)
- Verify query performance degrades gracefully

## Future Work

### RFD 080: Advanced Trace Analysis & AI-Driven Diagnosis

The following features are out of scope for this RFD and will be addressed in
RFD 080:

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
- Automatic hypothesis testing and interactive debugging

**Advanced Features (Future RFDs):**
- Automatic root cause detection using ML
- Trace replay with profiling
- Line-level CPU attribution (DWARF symbols)
- Network I/O and database query correlation

## Appendix

### Trace Context Propagation

**W3C Trace Context (HTTP):**

```
GET /api/checkout HTTP/1.1
Host: payment-svc.example.com
traceparent: 00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01
```

**Parsing in eBPF:**

eBPF kprobes extract trace ID from W3C traceparent header (format:
`00-<trace-id>-<parent-id>-<flags>`) by parsing the 32-character hex trace ID
and storing it as two 64-bit values (trace_id_high, trace_id_low) in a per-thread
BPF map.

---

## Implementation Status

**Core Capability:** ⏳ Not Started

This RFD is in draft state. Implementation will begin after approval.

**Planned Milestones:**
- eBPF trace context integration
- Storage schema extensions with trace_id columns
- Query API (`QueryTraceProfile` RPC)
- CLI command (`coral query trace-profile`)

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
