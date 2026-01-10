---
rfd: "080"
title: "Advanced Trace Analysis & AI-Driven Diagnosis"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "036", "070", "072", "074", "078" ]
database_migrations: [ ]
areas: [ "agent", "profiling", "tracing", "observability", "ai", "mcp" ]
---

# RFD 080 - Advanced Trace Analysis & AI-Driven Diagnosis

**Status:** 🚧 Draft

## Summary

Build on RFD 078's trace-tagged profiling infrastructure to enable advanced
performance analysis and AI-driven root cause diagnosis. This RFD adds
on-demand profiling, cohort comparison, and LLM integration for automated
debugging of distributed system performance issues.

**Core capabilities:**

- **Trace-Triggered Profiling**: High-frequency profiling activated by criteria
  (e.g., duration >1s, status 5xx)
- **Comparative Analysis**: Differential flame graphs comparing request cohorts
  (slow vs fast, success vs error)
- **LLM/MCP Integration**: AI-powered diagnosis with code-level attribution
- **Interactive Debugging**: Conversational interface for performance
  investigation

This completes Coral's observability stack with surgical, AI-assisted debugging
capabilities.

## Problem

### Current Limitations

RFD 078 provides request-level profiling but lacks advanced analysis features:

**No Outlier Isolation:**

- Cannot profile only slow/failing requests (1% of traffic)
- Aggregate profiles dominated by fast requests
- Example: P99 latency 5s, median 50ms—need to profile only P99

**No Comparative Analysis:**

- Cannot compare slow vs fast requests
- Cannot identify functions unique to slow requests
- Example: "What's different about failed requests?"

**Manual Analysis Required:**

- Operators manually interpret flame graphs
- No automated root cause identification
- No integration with AI assistants for diagnosis

### Why This Matters

**Intermittent Issues:**

- Slow requests occur rarely (< 1% of traffic)
- Need high-frequency profiling (99Hz) but only for slow requests
- Avoid overhead on fast path

**Root Cause Isolation:**

- Slow requests may have different code paths than fast requests
- Need differential analysis to identify unique bottlenecks
- Example: Cache miss → expensive computation vs cache hit → fast

**AI-Assisted Debugging:**

- LLMs can correlate traces, metrics, logs, and code
- Need profiling data in LLM context for complete analysis
- Enable "AI, debug this slow request" workflow

## Solution

### Core Architecture

Extend RFD 078's always-on profiling with on-demand triggers and AI integration.

**Trace-Triggered Profiling:**

- Start high-frequency profiling (99Hz) when requests match criteria
- Criteria: duration threshold, status code, URL pattern
- Automatically stop after N requests or timeout
- Store profiles for later comparison

**Comparative Analysis:**

- Define request cohorts (slow vs fast, success vs error)
- Generate differential flame graphs (cohort A - cohort B)
- Highlight functions significantly hotter in one cohort

**LLM Integration:**

- Extend `coral_query_summary` with trace-correlated profiles
- New MCP tools for trace profiling and triggers
- Enable AI to diagnose specific requests with code-level evidence

### Component Changes

#### 1. Storage Schema

**New Table: `trace_profile_triggers`**

```sql
CREATE TABLE trace_profile_triggers
(
    trigger_id          UUID PRIMARY KEY,
    service_id          UUID NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL,
    expires_at          TIMESTAMPTZ NOT NULL,
    status              VARCHAR NOT NULL, -- 'active', 'completed', 'expired'

    -- Trigger criteria
    min_duration_ms     INTEGER,
    max_duration_ms     INTEGER,
    status_code_pattern VARCHAR,
    url_pattern         VARCHAR,

    -- Profiling config
    frequency_hz        INTEGER NOT NULL,
    max_requests        INTEGER NOT NULL,

    -- Results
    collected_requests  INTEGER DEFAULT 0,
    trace_ids           TEXT[]
);
```

#### 2. Colony API

**New RPC: `StartTraceProfileTrigger`**

```protobuf
message StartTraceProfileTriggerRequest {
    string service_id = 1;
    int32 min_duration_ms = 2;
    string status_code_pattern = 3;
    int32 frequency_hz = 4;  // Default: 99Hz
    int32 max_requests = 5;  // Stop after N requests
    google.protobuf.Duration timeout = 6;
}

message StartTraceProfileTriggerResponse {
    string trigger_id = 1;
    string status = 2;
}
```

**New RPC: `CompareTraceProfiles`**

```protobuf
message CompareTraceProfilesRequest {
    string service_id = 1;
    string cohort_a_filter = 2;  // e.g., "duration > 1000"
    string cohort_b_filter = 3;  // e.g., "duration < 100"
    google.protobuf.Duration time_range = 4;
}

message CompareTraceProfilesResponse {
    repeated DifferentialHotspot hotspots = 1;
    int32 cohort_a_sample_count = 2;
    int32 cohort_b_sample_count = 3;
}

message DifferentialHotspot {
    string function = 1;
    float delta_percentage = 2;  // +ve = hotter in A, -ve = hotter in B
    int64 cohort_a_cpu_ms = 3;
    int64 cohort_b_cpu_ms = 4;
    string significance = 5;  // 'high', 'medium', 'low'
}
```

#### 3. CLI Commands

**Start triggered profiling:**

```bash
coral profile trace-trigger [flags]

Flags:
  --service string        Service name (required)
  --min-duration string   Min request duration (e.g., 1s)
  --status-code string    Status code pattern (e.g., 5xx)
  --frequency int         Sampling frequency in Hz (default: 99)
  --count int             Number of requests to collect (default: 10)
  --timeout duration      Max time to wait (default: 5m)
```

**Compare profiles:**

```bash
coral query profile-compare [flags]

Flags:
  --service string       Service name (required)
  --cohort-a string      Filter for cohort A (e.g., "duration > 1s")
  --cohort-b string      Filter for cohort B (e.g., "duration < 100ms")
  --since duration       Time range (default: 1h)
```

#### 4. MCP Tools

**New Tool: `coral_query_trace_profile`**

```json
{
    "name": "coral_query_trace_profile",
    "description": "Get CPU or memory profile for a specific distributed trace ID",
    "inputSchema": {
        "type": "object",
        "properties": {
            "trace_id": {"type": "string"},
            "profile_type": {"type": "string", "enum": ["cpu", "memory"], "default": "cpu"}
        },
        "required": ["trace_id"]
    }
}
```

**New Tool: `coral_profile_trace_trigger`**

```json
{
    "name": "coral_profile_trace_trigger",
    "description": "Start high-frequency profiling for requests matching criteria",
    "inputSchema": {
        "type": "object",
        "properties": {
            "service": {"type": "string"},
            "min_duration_ms": {"type": "integer"},
            "count": {"type": "integer", "default": 10}
        },
        "required": ["service", "min_duration_ms"]
    }
}
```

**New Tool: `coral_query_profile_compare`**

```json
{
    "name": "coral_query_profile_compare",
    "description": "Compare flame graphs between request cohorts to identify performance differences",
    "inputSchema": {
        "type": "object",
        "properties": {
            "service": {"type": "string"},
            "cohort_a_filter": {"type": "string"},
            "cohort_b_filter": {"type": "string"},
            "since": {"type": "string", "default": "1h"}
        },
        "required": ["service", "cohort_a_filter", "cohort_b_filter"]
    }
}
```

**Extend `coral_query_summary`:**

Include trace-correlated profiles in responses when querying slow traces.

## Implementation Plan

### Phase 1: Trace-Triggered Profiling

**Goals:** On-demand high-frequency profiling

- [ ] Implement `trace_profile_triggers` table and CRUD operations
- [ ] Add trigger matching logic in Agent profiler
- [ ] Implement `StartTraceProfileTrigger` RPC
- [ ] Add automatic trigger expiration and cleanup
- [ ] Implement `coral profile trace-trigger` CLI command
- [ ] Unit and integration tests

**Deliverable:** Trigger-based profiling for matching requests

### Phase 2: Comparative Analysis

**Goals:** Compare profiles between cohorts

- [ ] Implement cohort filtering logic (SQL WHERE clauses)
- [ ] Implement differential flame graph calculation
- [ ] Add statistical significance testing
- [ ] Implement `CompareTraceProfiles` RPC
- [ ] Implement `coral query profile-compare` CLI command
- [ ] Unit and integration tests

**Deliverable:** Differential flame graph analysis

### Phase 3: MCP Tools & LLM Integration

**Goals:** AI-powered diagnosis

- [ ] Implement `coral_query_trace_profile` MCP tool
- [ ] Implement `coral_profile_trace_trigger` MCP tool
- [ ] Implement `coral_query_profile_compare` MCP tool
- [ ] Extend `coral_query_summary` with trace profiles
- [ ] Update LLM system prompts with usage guidance
- [ ] E2E tests with AI workflows

**Deliverable:** LLM can diagnose slow requests with code-level attribution

### Phase 4: Testing & Documentation

**Goals:** Production readiness

- [ ] Performance tests (trigger overhead, comparison speed)
- [ ] E2E tests (AI-driven diagnosis scenarios)
- [ ] Documentation and runbooks
- [ ] User guide for advanced profiling workflows

**Deliverable:** Production-ready advanced trace analysis

## Use Case Example

### AI-Assisted Outlier Diagnosis

**Scenario:** P99 latency is 5s, median is 50ms

```
User: "P99 latency is 5s but median is 50ms—what's wrong?"

AI: I'll investigate the performance difference between slow and fast requests.

[Calls coral_profile_trace_trigger(service="payment-svc", min_duration_ms=1000, count=10)]

AI: Collected 10 slow requests. Let me compare them with fast requests.

[Calls coral_query_profile_compare(
  service="payment-svc",
  cohort_a_filter="duration > 1000",
  cohort_b_filter="duration < 100"
)]

AI: Root Cause Found:

Slow requests (P99) spend 88% of time in validateSignature, while fast
requests spend 0% (they skip validation).

Evidence:
- Differential flame graph shows +88.2% difference in validateSignature
- Slow requests: 4.2s avg in validateSignature
- Fast requests: validateSignature not in top 10 hotspots

Hypothesis: Cache miss on signature validation triggers expensive RSA verification.

Recommendation: Increase signature cache capacity or TTL to reduce cache misses.
```

## Testing Strategy

### Integration Tests

- Start trigger, generate matching requests, verify profiling activated
- Verify trigger stops after max_requests reached
- Test comparative analysis with two request cohorts
- Verify differential flame graph calculation

### E2E Tests

- AI diagnoses slow requests using trace profiling tools
- AI identifies outlier bottlenecks using comparative analysis
- AI provides code-level root cause with evidence

## Implementation Status

**Core Capability:** ⏳ Not Started

This RFD depends on RFD 078 (Core Trace Profiling Infrastructure). Implementation
will begin after RFD 078 is complete.

## Dependencies

**Pre-requisites:**

- ✅ RFD 036 (Distributed Tracing)
- ✅ RFD 070 (On-Demand CPU Profiling)
- ✅ RFD 072 (Continuous CPU Profiling)
- ✅ RFD 074 (LLM-Driven RCA)
- ✅ RFD 078 (Trace-Driven Profiling - Core Infrastructure)

**Enables:**

- Complete AI-powered observability stack
- Automated diagnosis of distributed system performance issues
- Interactive debugging workflows

## Future Work

### Advanced Features (Future RFDs)

**Automatic Anomaly Detection:**
- ML-based detection of anomalous flame graphs
- Proactive alerting on performance regressions
- Baseline comparison for each service

**Trace Replay with Profiling:**
- Capture and replay slow requests
- Debug without waiting for issue to recur
- High-resolution profiling in isolated environment

**Line-Level Attribution:**
- Extend eBPF to capture instruction pointers
- Map to source code line numbers via DWARF
- Show line-level CPU time in flame graphs

**Cross-Layer Correlation:**
- Network I/O correlation (eBPF kprobes on send/recv)
- Database query profiling (SQL text + execution time)
- Lock contention analysis
