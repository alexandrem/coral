---
rfd: "077"
title: "Trace-Driven Profiling and Request-Level Performance Correlation"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "036", "070", "072", "074", "076" ]
database_migrations: [ ]
areas: [ "agent", "profiling", "tracing", "observability", "ai" ]
---

# RFD 077 - Trace-Driven Profiling and Request-Level Performance Correlation

**Status:** 🚧 Draft

## Summary

Enable request-level performance correlation by linking distributed traces (RFD 036) with CPU/memory profiling data (RFD 070, 072, 076). This allows operators and AI assistants to answer "Why is **this specific request** slow?" by showing exactly which functions consumed CPU/memory during that request's execution, enabling surgical debugging of production issues.

Key capabilities:

- **Trace-Triggered Profiling**: Automatically profile requests matching criteria (e.g., duration >1s, status 5xx)
- **Request-Level Flame Graphs**: Show CPU/memory consumption for individual trace IDs
- **Comparative Analysis**: Compare profiles between slow vs fast requests
- **Cross-Service Correlation**: Follow trace across services with per-service flame graphs
- **LLM-Driven Diagnosis**: Enable AI to provide code-level root cause for specific slow requests

This bridges the gap between "what happened" (distributed traces) and "why it was slow" (profiling), completing Coral's observability stack with request-level granularity.

## Problem

### Current Limitations

Coral's existing observability tools operate at different granularities:

**Distributed Traces (RFD 036):**
- Show request flow across services (which services were called, in what order)
- Measure time spent in each service (span duration)
- Cannot explain **why** a service was slow (CPU? Database? Lock contention?)

**CPU/Memory Profiling (RFD 070, 072, 076):**
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

**AI-Driven Root Cause Analysis:**
- RFD 074 enables LLM to correlate metrics, but lacks request-level code attribution
- LLM sees: "Trace abc123 spent 4.5s in payment-service" but not which function
- Missing link: Connect trace span duration to profiling flame graph

**Intermittent Issues:**
- "Slow requests to /api/checkout happen randomly, 1 in 100 requests"
- Aggregate profiling averages out the slow requests
- Need: Profile **only** the slow requests to isolate the bottleneck

**Cross-Service Debugging:**
- A slow trace spans multiple services (frontend → API → database)
- Need: Per-service flame graphs correlated with the same trace ID
- Cannot answer: "Which service's code caused the slowdown?"

### Use Cases Affected

1. **Single Request Diagnosis**
   - "Why did this specific checkout request (trace abc123) take 5 seconds?"
   - Current: Run aggregate profile, guess which function corresponds to the slow trace
   - Need: Request-level flame graph showing exactly what ran during that trace

2. **Outlier Analysis**
   - "99th percentile checkout latency is 5s, but median is 50ms—why?"
   - Current: Aggregate profile dominated by fast requests (99% of samples)
   - Need: Profile only the slow outliers (P99) to see their unique bottlenecks

3. **Error Correlation**
   - "Requests with HTTP 500 errors are slow—what's different about them?"
   - Current: No way to profile only failed requests
   - Need: Compare flame graphs between successful vs failed requests

4. **Cross-Service Attribution**
   - "Trace abc123 spans 5 services—which service's code caused the 4s delay?"
   - Current: Run separate profiles on each service, manually correlate timestamps
   - Need: Unified view showing per-service flame graphs for the same trace

5. **Request-Specific LLM Diagnosis**
   - "AI, debug this slow checkout request (trace abc123)"
   - Current: LLM sees trace spans but not code-level execution
   - Need: LLM receives trace + embedded flame graphs for complete analysis

## Solution

### Core Architecture

Extend Coral's profiling infrastructure to tag CPU/memory samples with the active trace ID from the current execution context, enabling request-level correlation.

**eBPF Enhancement:**

```c
// In eBPF CPU profiling program (RFD 072)
struct sample_key {
    u32 pid;
    u32 tid;
    // NEW: Optional trace context
    u64 trace_id_high;  // First 64 bits of 128-bit trace ID
    u64 trace_id_low;   // Last 64 bits
};

// Sample collection logic
int on_cpu_sample(struct bpf_perf_event_data *ctx) {
    u64 pid_tgid = bpf_get_current_pid_tgid();
    u32 pid = pid_tgid >> 32;
    u32 tid = pid_tgid;

    // Read trace context from thread-local storage (TLS)
    struct trace_context *tctx = get_trace_context(tid);

    struct sample_key key = {
        .pid = pid,
        .tid = tid,
        .trace_id_high = tctx ? tctx->trace_id_high : 0,
        .trace_id_low = tctx ? tctx->trace_id_low : 0,
    };

    // Collect stack trace and increment sample count
    collect_stack_sample(&key, ctx);
}
```

**Trace Context Injection:**

The SDK (or eBPF for auto-instrumented apps) writes the active trace ID to thread-local storage when processing a request:

```go
// In Coral SDK or eBPF tracepoint
func ProcessRequest(ctx context.Context) {
    traceID := trace.SpanFromContext(ctx).SpanContext().TraceID()

    // Write to TLS for eBPF to read
    setThreadTraceContext(traceID)
    defer clearThreadTraceContext()

    // Process request...
}
```

**For Beyla Auto-Instrumentation:**

eBPF kprobes on HTTP/gRPC entry points extract trace ID from headers and store in per-thread BPF map:

```c
// eBPF kprobe on HTTP request entry
int kprobe__http_server_handler(struct pt_regs *ctx) {
    u32 tid = bpf_get_current_pid_tgid();

    // Parse W3C traceparent header from request
    struct trace_id tid_parsed = parse_traceparent_header(ctx);

    // Store in per-thread map for profiler to read
    bpf_map_update_elem(&thread_trace_context, &tid, &tid_parsed, BPF_ANY);
}

int kretprobe__http_server_handler(struct pt_regs *ctx) {
    u32 tid = bpf_get_current_pid_tgid();

    // Clear trace context on request completion
    bpf_map_delete_elem(&thread_trace_context, &tid);
}
```

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
coral query profile --trace-id abc123def456
# Returns CPU flame graph for only the samples from that trace
```

#### 2. Trace-Triggered Profiling (On-Demand)

**How it works:**
- Start high-frequency profiling (99Hz) for requests matching criteria
- Criteria: duration threshold, status code, URL pattern, service name
- Automatically stop after N matching requests collected

**Example:**
```bash
coral profile trace-trigger \
  --service payment-service \
  --min-duration 1s \
  --count 10 \
  --frequency 99

# Profiles the next 10 requests to payment-service that take >1s
# Returns per-request flame graphs
```

**Use case:**
- Intermittent slow requests (1% of traffic)
- Need high-resolution profiling (99Hz) but only for slow requests
- Avoid profiling overhead on fast requests

#### 3. Comparative Flame Graph Analysis

**How it works:**
- Compare flame graphs between different request cohorts
- Cohorts: slow vs fast, success vs error, before/after deployment

**Example:**
```bash
coral query profile-compare \
  --service payment-service \
  --cohort-a "duration > 1s" \
  --cohort-b "duration < 100ms" \
  --since 1h

# Shows differential flame graph:
# - Functions hotter in slow requests (red)
# - Functions hotter in fast requests (blue)
```

**Output:**
```
Differential Flame Graph (Slow vs Fast Requests)

HOTTER in Slow Requests:
  +42.5%  validateSignature → crypto/rsa.VerifyPKCS1v15 (4096-bit key)
  +12.0%  runtime.gcBgMarkWorker (GC pressure from allocations)

HOTTER in Fast Requests:
  -15.3%  serializeResponse → proto.Marshal (less data in fast path)
```

#### 4. Cross-Service Trace Profiling

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

**LLM Integration (RFD 074):**
```json
{
  "trace_id": "abc123def456",
  "total_duration_ms": 5000,
  "services": [
    {
      "service": "frontend-svc",
      "span_duration_ms": 200,
      "cpu_profile": {
        "top_hotspots": [
          { "function": "renderTemplate", "percentage": 45.0, "cpu_ms": 90 }
        ]
      }
    },
    {
      "service": "payment-svc",
      "span_duration_ms": 4500,
      "cpu_profile": {
        "top_hotspots": [
          { "function": "validateSignature", "percentage": 93.3, "cpu_ms": 4200 }
        ]
      }
    }
  ],
  "diagnosis": "Trace abc123 is slow (5s) because payment-svc spent 4.2s (84% of total) in validateSignature using 4096-bit RSA keys. Recommendation: Reduce key size to 2048-bit or cache validation results."
}
```

#### 5. Memory Profiling Correlation (RFD 076 Integration)

**How it works:**
- Same trace ID tagging applies to memory allocation profiling (RFD 076)
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

**Three-Tier Strategy:**

1. **Automatic Trace Tagging (Low Overhead, Always-On)**
   - Continuous profiling (19Hz, RFD 072) tags samples with trace ID
   - Zero additional overhead (trace ID already in thread context)
   - Enables historical request-level analysis

2. **Trace-Triggered Profiling (High Overhead, On-Demand)**
   - High-frequency profiling (99Hz) activated by trace criteria
   - Automatically stops after collecting N matching requests
   - Provides surgical, high-resolution profiling

3. **LLM-Powered Correlation (Zero Overhead, Query-Time)**
   - Join traces (RFD 036) with profiles (RFD 072) at query time
   - Enrich `coral_query_summary` with trace-correlated profiles
   - Enable AI to diagnose specific slow requests

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
ADD COLUMN trace_id VARCHAR(32);  -- 32-char hex string (128-bit trace ID)

CREATE INDEX idx_cpu_profile_summaries_trace_id
ON cpu_profile_summaries(trace_id, bucket_time DESC);
```

**Extend `memory_profiles` Table (RFD 076):**

```sql
ALTER TABLE memory_profiles
ADD COLUMN trace_id VARCHAR(32);

CREATE INDEX idx_memory_profiles_trace_id
ON memory_profiles(trace_id, timestamp DESC);
```

**New Table: `trace_profile_triggers`**

```sql
CREATE TABLE trace_profile_triggers (
    trigger_id UUID PRIMARY KEY,
    service_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    status VARCHAR NOT NULL,  -- 'active', 'completed', 'expired'

    -- Trigger criteria
    min_duration_ms INTEGER,
    max_duration_ms INTEGER,
    status_code_pattern VARCHAR,  -- e.g., '5xx', '200'
    url_pattern VARCHAR,           -- e.g., '/api/checkout%'

    -- Profiling config
    frequency_hz INTEGER NOT NULL,  -- e.g., 99
    max_requests INTEGER NOT NULL,  -- Stop after N matching requests

    -- Results
    collected_requests INTEGER DEFAULT 0,
    trace_ids TEXT[]  -- Array of collected trace IDs
);
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

**New RPC: `StartTraceProfileTrigger`**

```protobuf
message StartTraceProfileTriggerRequest {
    string service_id = 1;

    // Trigger criteria
    int32 min_duration_ms = 2;
    int32 max_duration_ms = 3;
    string status_code_pattern = 4;
    string url_pattern = 5;

    // Profiling config
    int32 frequency_hz = 6;      // Default: 99Hz
    int32 max_requests = 7;      // Stop after N requests
    google.protobuf.Duration timeout = 8;  // Max 600s
}

message StartTraceProfileTriggerResponse {
    string trigger_id = 1;
    string status = 2;  // 'active', 'no_matching_requests', etc.
}

message GetTraceProfileTriggerRequest {
    string trigger_id = 1;
}

message GetTraceProfileTriggerResponse {
    string trigger_id = 1;
    string status = 2;
    int32 collected_requests = 3;
    repeated string trace_ids = 4;  // Collected trace IDs
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
  --compare-with string  Compare with another trace ID (differential)
```

**Start trace-triggered profiling:**

```bash
coral profile trace-trigger [flags]

Flags:
  --service string        Service name or ID (required)
  --min-duration string   Min request duration (e.g., 1s)
  --max-duration string   Max request duration (optional)
  --status-code string    Status code pattern (e.g., 5xx, 200)
  --url-pattern string    URL pattern (e.g., /api/checkout%)
  --frequency int         Sampling frequency in Hz (default: 99)
  --count int             Number of requests to collect (default: 10)
  --timeout duration      Max time to wait (default: 5m)
```

**Compare profiles:**

```bash
coral query profile-compare [flags]

Flags:
  --service string       Service name or ID (required)
  --cohort-a string      Filter for cohort A (e.g., "duration > 1s")
  --cohort-b string      Filter for cohort B (e.g., "duration < 100ms")
  --since duration       Time range (default: 1h)
  --profile-type string  cpu or memory (default: cpu)
```

### 5. MCP Tool Extensions (RFD 074 Integration)

**Extend `coral_query_summary`:**

```json
{
  "trace_id": "abc123def456",
  "services": [
    {
      "service": "payment-svc",
      "span_duration_ms": 4500,
      "cpu_profile_summary": {
        "top_hotspots": [
          {
            "function": "validateSignature",
            "percentage": 93.3,
            "cpu_ms": 4200,
            "category": "CRYPTO"
          }
        ]
      }
    }
  ],
  "diagnosis": "Request abc123 is slow (5s) because payment-svc spent 93% of its time (4.2s) in validateSignature. This is 10x slower than typical requests (400ms). Root cause: 4096-bit RSA key validation introduced in recent deployment."
}
```

**New Tool: `coral_query_trace_profile`**

```json
{
  "name": "coral_query_trace_profile",
  "description": "Get CPU or memory profile for a specific distributed trace ID. Use this to understand WHY a particular slow request was slow by seeing which functions consumed CPU/memory during that request's execution.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "trace_id": {
        "type": "string",
        "description": "Trace ID (32-char hex string from distributed trace)"
      },
      "profile_type": {
        "type": "string",
        "enum": ["cpu", "memory"],
        "default": "cpu"
      }
    },
    "required": ["trace_id"]
  }
}
```

**New Tool: `coral_profile_trace_trigger`**

```json
{
  "name": "coral_profile_trace_trigger",
  "description": "Start high-frequency profiling for requests matching criteria (e.g., duration >1s). Use this when you need to profile ONLY the slow/failing requests, not all traffic. Returns trace IDs of profiled requests for detailed analysis.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "service": {
        "type": "string",
        "description": "Service name"
      },
      "min_duration_ms": {
        "type": "integer",
        "description": "Minimum request duration in milliseconds"
      },
      "count": {
        "type": "integer",
        "default": 10,
        "description": "Number of matching requests to profile"
      }
    },
    "required": ["service", "min_duration_ms"]
  }
}
```

## Implementation Plan

### Phase 1: eBPF Trace Context Integration

**Goals:** Tag profile samples with trace IDs

- [ ] Extend eBPF CPU profiler to read trace ID from thread-local storage
- [ ] Add `trace_id_high` and `trace_id_low` fields to sample key struct
- [ ] Implement BPF map `thread_trace_context` for per-thread trace storage
- [ ] Add eBPF kprobes for Beyla trace context extraction (HTTP/gRPC entry points)
- [ ] Extend memory profiler with same trace ID tagging (RFD 076 integration)

**Deliverable:** eBPF profilers tag samples with trace IDs (0 if no active trace)

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

**Deliverable:** `coral query trace-profile <trace-id>` returns request-level flame graphs

### Phase 4: CLI Integration

**Goals:** User-facing trace profiling commands

- [ ] Implement `coral query trace-profile` command
- [ ] Add text-based flame graph rendering with trace metadata
- [ ] Implement trace ID autocomplete (recent slow traces)
- [ ] Add `--compare-with` flag for differential flame graphs
- [ ] Add JSON/CSV export formats

**Deliverable:** CLI commands for trace-level profiling queries

### Phase 5: Trace-Triggered Profiling

**Goals:** On-demand profiling for matching requests

- [ ] Implement `trace_profile_triggers` table and CRUD operations
- [ ] Implement trigger matching logic in profiler (check criteria before profiling)
- [ ] Add `StartTraceProfileTrigger` RPC and agent handler
- [ ] Implement automatic trigger expiration and cleanup
- [ ] Add `coral profile trace-trigger` CLI command

**Deliverable:** Trigger-based profiling for slow/failing requests

### Phase 6: Comparative Analysis

**Goals:** Compare profiles between request cohorts

- [ ] Implement cohort filtering (SQL WHERE clauses from user input)
- [ ] Implement differential flame graph calculation (cohort A - cohort B)
- [ ] Add statistical significance testing (is difference meaningful?)
- [ ] Implement `coral query profile-compare` CLI command
- [ ] Add visualization (red = hotter in cohort A, blue = hotter in cohort B)

**Deliverable:** Comparative flame graph analysis

### Phase 7: LLM Integration

**Goals:** Enrich AI diagnostics with trace-level profiling

- [ ] Extend `coral_query_summary` MCP tool with trace profile data
- [ ] Implement `coral_query_trace_profile` MCP tool
- [ ] Implement `coral_profile_trace_trigger` MCP tool
- [ ] Update LLM system prompts with trace profiling usage guidance
- [ ] Add trace-profile examples to tool descriptions

**Deliverable:** LLM can diagnose specific slow requests with code-level attribution

### Phase 8: Testing & Documentation

**Goals:** Validate end-to-end functionality

- [ ] Unit tests for trace ID tagging, storage, querying
- [ ] Integration tests: multi-service trace with per-service profiles
- [ ] E2E test: Trigger profiling on slow requests, verify profiles collected
- [ ] Performance tests: Measure overhead of trace ID tagging (<1%)
- [ ] Documentation: User guide for trace profiling workflows
- [ ] Runbook: Debugging slow requests with trace profiling

**Deliverable:** Production-ready trace profiling with comprehensive tests

## Use Case Examples

### Example 1: Single Request Diagnosis

**Scenario:** User reports: "Checkout failed for order #12345 (trace abc123)"

**Workflow:**

```bash
# Step 1: Query trace to see request flow
$ coral query traces --trace-id abc123def456

Trace: abc123def456 (Duration: 5.2s, Status: 500 ERROR)
├─ frontend-svc (200ms)
├─ payment-svc (4.8s) ← SLOW
│  ├─ card-validator-svc (4.5s) ← BOTTLENECK
│  │  └─ postgres (12ms)
│  └─ fraud-detector (300ms)
└─ notification-svc (200ms)

# Step 2: Profile the slow service for this specific request
$ coral query trace-profile abc123def456 --service payment-svc

Request-Level CPU Profile (trace abc123def456, payment-svc):

Top CPU Hotspots:
  93.3%  4.2s  validateSignature → crypto/rsa.VerifyPKCS1v15
   3.1%  140ms runtime.gcBgMarkWorker
   1.8%  80ms  serializeResponse → proto.Marshal

Flame Graph:
  validateSignature (4.2s, 93.3%)
  └─ crypto/rsa.VerifyPKCS1v15 (4.2s)
     └─ crypto/rsa.decrypt (4.1s)
        └─ crypto/internal/bigmod.(*Nat).Mul (4.0s) ← BOTTLENECK

Analysis:
- This request spent 4.2s (93% of payment-svc time) in validateSignature
- 10x slower than typical requests (avg: 400ms)
- Root cause: RSA 4096-bit key validation (recent deployment changed key size)
```

**AI Diagnosis (via `coral_query_trace_profile` MCP tool):**

```
User: "Why did order #12345 fail?"

LLM: Analyzing trace abc123def456...

Root Cause:
The checkout failed because payment-svc spent 4.2 seconds (93% of its execution time)
performing RSA signature validation with 4096-bit keys. This is 10x slower than
typical requests.

Evidence:
- Trace shows payment-svc took 4.8s (92% of total 5.2s)
- CPU profile for this specific request reveals validateSignature consumed 4.2s
- Bottleneck is in crypto/rsa.VerifyPKCS1v15 → big integer multiplication

Recent Deployment Correlation:
- Current build (abc123, deployed 2h ago) uses 4096-bit RSA keys
- Previous build (def456) used 2048-bit keys with avg validation time of 400ms

Recommendation:
1. Immediate: Rollback to 2048-bit keys (industry standard for most use cases)
2. Long-term: Cache signature verification results to avoid redundant validations
3. Alternative: Switch to Ed25519 signatures (faster and more secure)
```

### Example 2: Outlier Analysis

**Scenario:** P99 latency is 5s, but median is 50ms (1% of requests are slow)

**Workflow:**

```bash
# Step 1: Start trace-triggered profiling for slow requests
$ coral profile trace-trigger \
    --service payment-svc \
    --min-duration 1s \
    --count 10 \
    --frequency 99

Started trace profiling trigger (trigger_id: trigger-xyz789)
Waiting for 10 requests with duration >1s...

Collected requests:
  1. trace abc123 (5.2s)
  2. trace def456 (4.8s)
  3. trace ghi789 (3.1s)
  ...
  10. trace xyz999 (2.5s)

# Step 2: Compare slow requests vs fast requests
$ coral query profile-compare \
    --service payment-svc \
    --cohort-a "duration > 1s" \
    --cohort-b "duration < 100ms" \
    --since 1h

Differential Flame Graph (Slow vs Fast):

SIGNIFICANTLY HOTTER in Slow Requests:
  +88.2%  validateSignature → crypto/rsa.VerifyPKCS1v15
  +12.0%  runtime.gcBgMarkWorker (GC pressure)
   +5.3%  parseRequestBody → json.Unmarshal (large payloads)

SIGNIFICANTLY HOTTER in Fast Requests:
  -22.1%  serializeResponse → proto.Marshal (less data to serialize)

Analysis:
Slow requests (P99) are dominated by validateSignature (88% vs 0% in fast requests).
Fast requests skip signature validation (likely cached or different code path).

Root Cause:
Slow requests have cache misses for signature validation, triggering expensive
4096-bit RSA verification. Fast requests hit cache.

Recommendation:
Increase signature cache TTL or capacity to reduce cache misses.
```

### Example 3: Cross-Service Attribution

**Scenario:** Trace spans 5 microservices—which one is slow?

**Workflow:**

```bash
# Query trace with per-service profiling
$ coral query trace-profile abc123def456 --all-services

Cross-Service Trace Profile (trace abc123def456):

Total Duration: 5.2s
Services: 5
Status: 500 ERROR

Per-Service Breakdown:

1. frontend-svc (200ms, 3.8% of total)
   Top CPU: renderTemplate (120ms, 60%)

2. payment-svc (4.8s, 92.3% of total) ← BOTTLENECK
   Top CPU: validateSignature (4.2s, 87.5%) ← ROOT CAUSE

3. card-validator-svc (4.5s, 86.5% of total, called by payment-svc)
   Top CPU: queryDatabase (12ms, 0.3%)
   Note: 4.5s span duration but only 12ms CPU → WAITING, not CPU-bound

4. fraud-detector (300ms, 5.8% of total)
   Top CPU: runMLModel (250ms, 83.3%)

5. notification-svc (200ms, 3.8% of total)
   Top CPU: sendEmail (180ms, 90%)

Attribution:
- payment-svc is the bottleneck (4.8s, 92% of total trace time)
- Within payment-svc, validateSignature consumed 4.2s CPU (87% of service time)
- card-validator-svc had high span duration (4.5s) but low CPU (12ms) → I/O wait

Root Cause:
payment-svc's validateSignature function is CPU-bound due to 4096-bit RSA validation.
```

## Performance Considerations

### Overhead Analysis

**Trace ID Tagging (Always-On):**
- **CPU Overhead:** <0.1% (reading 128-bit trace ID from TLS/BPF map adds ~5-10ns per sample)
- **Memory Overhead:** +16 bytes per sample (trace_id_high + trace_id_low)
- **Storage Overhead:** +32 bytes per row in `cpu_profile_summaries` (VARCHAR(32))

**Trace-Triggered Profiling (On-Demand):**
- **CPU Overhead:** 2-5% during profiling window (99Hz sampling, same as RFD 070)
- **Duration:** Limited by trigger criteria (max 10 minutes, default 5 minutes)
- **Scope:** Only services/requests matching trigger criteria

**Query Performance:**
- **Trace Profile Query:** <200ms for single trace (indexed by trace_id)
- **Comparative Analysis:** <2s for 1 hour time range (aggregates samples across cohorts)
- **Join Cost:** Minimal (trace_id is indexed in both tables)

### Storage Scaling

**Continuous Profiling with Trace Tagging:**
- **Assumption:** 1,000 req/s per service, 19Hz profiling, 5 unique stacks per request
- **Samples per day:** 1,000 req/s * 86,400s * 0.05 samples/req = 4.3M samples/day
- **Storage (with compression):** ~200MB/day per service (same as RFD 072)
- **Trace ID overhead:** +16 bytes/sample * 4.3M = 69MB/day (34% increase)

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

**Trace-Triggered Profiling:**
1. Start trigger with criteria (duration >1s)
2. Generate slow and fast requests
3. Verify only slow requests are profiled
4. Verify trigger stops after max_requests reached

**Comparative Analysis:**
1. Generate two cohorts of requests (slow vs fast)
2. Profile both cohorts
3. Query differential flame graph
4. Verify statistical significance of differences

### E2E Tests

**Scenario 1: Single Request Diagnosis**
1. User: "Debug trace abc123"
2. LLM calls `coral_query_trace_profile(trace_id=abc123)`
3. Verify LLM receives per-service flame graphs
4. Verify LLM diagnoses correct bottleneck

**Scenario 2: Outlier Analysis**
1. User: "P99 latency is 5s, but median is 50ms—why?"
2. LLM calls `coral_profile_trace_trigger(min_duration_ms=1000, count=10)`
3. LLM analyzes collected profiles
4. LLM calls `coral_query_profile_compare(cohort_a="slow", cohort_b="fast")`
5. Verify LLM identifies unique hotspots in slow requests

**Scenario 3: Cross-Service Attribution**
1. User: "Which service is slow in trace abc123?"
2. LLM calls `coral_query_trace_profile(trace_id=abc123, all_services=true)`
3. Verify LLM receives per-service CPU attribution
4. Verify LLM identifies bottleneck service and function

### Performance Tests

**Overhead Measurement:**
- Baseline: Service without trace profiling (0% overhead)
- With trace tagging: Measure CPU overhead (target: <0.1%)
- With triggered profiling: Measure CPU overhead (target: <5% during profiling)

**Storage Benchmarks:**
- Insert 1M profiles with trace IDs
- Query by trace_id (target: <50ms)
- Join with traces table (target: <200ms)

**Scalability Tests:**
- 10,000 req/s per service
- Verify profiler keeps up (no dropped samples)
- Verify query performance degrades gracefully

## Future Work

### Advanced Trace-Profiling Features (Future RFDs)

**1. Automatic Root Cause Detection (Future RFD)**
- Machine learning to detect anomalous flame graphs automatically
- Compare each trace's profile against baseline (P50 requests)
- Alert: "Trace abc123 has anomalous hotspot in function X (not present in typical requests)"

**2. Trace Replay with Profiling (Future RFD)**
- Capture request payload for slow traces
- Replay request in isolated environment with high-frequency profiling
- Debug without waiting for issue to recur in production

**3. Line-Level Attribution (Future RFD)**
- Extend eBPF profiler to capture instruction pointer (IP)
- Map IP to source code line number using DWARF symbols
- Show line-level CPU time in flame graph: "Line 42 in validateSignature consumed 4.2s"

**4. Network I/O Correlation (Future RFD)**
- Correlate network I/O events (eBPF kprobes on send/recv) with trace IDs
- Identify slow requests caused by network latency vs CPU
- Example: "Trace abc123 spent 4.5s waiting for network I/O (external payment gateway)"

**5. Database Query Profiling (Future RFD)**
- Correlate SQL query execution with trace IDs
- Show SQL query text and execution time within trace flame graph
- Example: "Trace abc123 spent 3.2s in SELECT query (slow due to missing index)"

### LLM Enhancements

**1. Automatic Hypothesis Testing**
- LLM generates hypotheses for slow request ("RSA key size changed?")
- LLM queries historical profiles to test hypothesis ("Compare with previous build")
- LLM presents evidence-based root cause

**2. Interactive Debugging**
- User: "Why is this slow?"
- LLM: "Trace abc123 is slow due to validateSignature. Shall I compare with fast requests?"
- User: "Yes"
- LLM: Runs comparative analysis and explains differences

**3. Proactive Anomaly Detection**
- LLM continuously monitors traces and profiles
- LLM alerts: "Detected anomalous trace abc123 (5x slower than baseline, bottleneck in new function)"

## Appendix

### Trace Context Propagation

**W3C Trace Context (HTTP):**

```
GET /api/checkout HTTP/1.1
Host: payment-svc.example.com
traceparent: 00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01
```

**Parsing in eBPF:**

```c
// Extract trace ID from traceparent header
// Format: 00-<trace-id>-<parent-id>-<flags>
int parse_traceparent(char *header, struct trace_id *tid) {
    // Skip version and dash: "00-"
    char *trace_id_start = header + 3;

    // Parse first 16 hex chars → trace_id_high (64 bits)
    tid->trace_id_high = hex_to_u64(trace_id_start);

    // Parse next 16 hex chars → trace_id_low (64 bits)
    tid->trace_id_low = hex_to_u64(trace_id_start + 16);

    return 0;
}
```

### Example LLM Interaction

**User:** "Investigate slow checkout request (order #12345)"

**LLM Action 1: Find Trace ID**
```
Tool: coral_query_traces
Input: { "service": "checkout", "search": "order_id:12345" }
Output: { "trace_id": "abc123def456", "duration_ms": 5200, "status": 500 }
```

**LLM Action 2: Get Trace Profile**
```
Tool: coral_query_trace_profile
Input: { "trace_id": "abc123def456", "profile_type": "cpu" }
Output: {
  "services": [
    { "service": "payment-svc", "top_hotspots": [
      { "function": "validateSignature", "cpu_ms": 4200, "percentage": 93.3 }
    ]}
  ]
}
```

**LLM Action 3: Compare with Fast Requests**
```
Tool: coral_query_profile_compare
Input: {
  "service": "payment-svc",
  "cohort_a": "duration > 1000",
  "cohort_b": "duration < 100"
}
Output: {
  "differential": [
    { "function": "validateSignature", "delta_percentage": +88.2 }
  ]
}
```

**LLM Response:**

```
Order #12345 failed due to a performance regression in payment-svc:

Root Cause:
- The checkout request (trace abc123) took 5.2 seconds
- 93% of this time (4.2s) was spent in validateSignature
- This function is 88% hotter in slow requests vs fast requests
- Fast requests either skip validation or hit cache

Evidence:
- CPU profile for trace abc123 shows validateSignature at 4.2s
- Comparative analysis: slow requests spend 88% in validateSignature, fast requests 0%
- Recent deployment (2h ago) changed RSA key size from 2048 to 4096 bits

Recommendation:
1. Immediate: Rollback to 2048-bit RSA keys (reduces validation time by 10x)
2. Long-term: Implement signature cache to avoid redundant validations
3. Monitor: Set up alert for checkout latency P95 >1s

Would you like me to:
a) Check if other recent checkouts have the same issue?
b) Profile memory usage for this trace (check for allocation spikes)?
c) Create a trace trigger to capture more slow checkouts?
```

---

## Dependencies

**Pre-requisites:**
- ✅ RFD 036 (Distributed Tracing) - Provides trace IDs and span metadata
- ✅ RFD 070 (On-Demand CPU Profiling) - Foundation for profiling infrastructure
- ✅ RFD 072 (Continuous CPU Profiling) - Always-on profiling with low overhead
- ✅ RFD 074 (LLM-Driven RCA) - Integration point for AI-powered diagnosis
- ✅ RFD 076 (Memory Profiling) - Memory profiling correlation (parallel integration)

**Enables:**
- Future: Trace replay with profiling
- Future: Automatic root cause detection
- Future: Line-level performance attribution
- Complete observability stack with request-level granularity
