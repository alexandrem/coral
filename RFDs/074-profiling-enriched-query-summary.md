---
rfd: "074"
title: "Profiling-Enriched Query Summary for LLM-Driven RCA"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "067", "071", "072", "004" ]
database_migrations: [ ]
areas: [ "colony", "mcp", "ai", "observability", "profiling" ]
---

# RFD 074 - Profiling-Enriched Query Summary for LLM-Driven RCA

**Status:** 🚧 Draft

## Summary

Enhance the `query_summary` MCP tool (RFD 067) to include continuous CPU
profiling data (RFD 072) in a structured, LLM-friendly format. Instead of
returning only scalar metrics, the enriched summary provides Top-K CPU hotspots
pre-correlated with system metrics, deployment versions, and anomalies, enabling
LLMs to perform intelligent root cause analysis without requiring manual data
correlation.

## Problem

- **Current limitations**: The `query_summary` tool (RFD 067) returns scalar
  metrics like CPU usage, latency percentiles, and error rates. LLMs must make
  educated guesses about root causes without seeing **what code is running** on
  the CPU.
- **Context switching overhead**: To diagnose a CPU spike, an SRE (or LLM) must:
    1. Query system metrics (CPU 88%)
    2. Switch to profiling tool (`coral debug cpu-profile --since 5m`)
    3. Generate flame graph
    4. Manually correlate timestamps and build_ids
    5. Interpret flame graph to identify hot functions
- **Why this matters**: LLMs excel at pattern recognition and decision trees, but
  they need structured, pre-correlated data. Raw flame graphs or separate tool
  invocations waste context window tokens and require multi-step reasoning.
- **Use cases affected**: Automated incident response, LLM-driven root cause
  analysis, "shift-left" debugging where developers ask Claude "why is my
  service slow?"

## Solution

Enrich the `query_summary` MCP tool response with a `ProfilingSummary` object
containing:

1. **Top-K CPU Hotspots**: The N most expensive code paths (not full flame
   graphs)
2. **Pre-Correlated Metrics**: CPU usage, latency, error rates aligned with
   profiling timestamps
3. **Deployment Context**: Active build_id and deployment timestamp
4. **Anomaly Indicators**: Flags when hotspots differ from baseline or changed
   after deployment

**Key Design Decisions:**

- **Top-K Pattern (not raw flame graphs)**: Return only the top 5-10 hottest
  stacks to keep LLM context windows clean. Full flame graphs available
  on-demand via RFD 070/072 tools.
- **Multi-Dimensional Join**: Use DuckDB to join `cpu_profile_summaries` (RFD
  072) + `system_metrics_summaries` (RFD 071) + `binary_metadata_registry` (RFD
  072) in a single query.
- **Agentic Decision Tree**: Structure data to guide LLM reasoning:
    - High CPU + Business Logic Hotspot → Drill down with `debug_cpu_profile`
      (99Hz)
    - High Latency + Low CPU → Switch to uprobe tracing (RFD 069)
    - Kernel Hotspot → Check system metrics for network/disk issues
- **Build ID Correlation**: Include active build_id and compare hotspots against
  previous deployment to detect regressions.

**Benefits:**

- **One-Shot RCA**: LLM gets system metrics + code-level context in a single MCP
  tool call
- **Token Efficiency**: Top-5 hotspots consume ~500 tokens vs 50KB flame graph
- **Pre-Correlated Data**: No manual timestamp alignment or build_id lookups
  needed
- **Agentic Workflows**: LLM can autonomously decide which debugging tool to use
  next
- **Regression Detection**: Automatic comparison with previous deployment's
  hotspots

**Architecture Overview:**

```
┌─────────────────────────────────────────────────────────────────┐
│  LLM (Claude Desktop / API)                                     │
│                                                                  │
│  User: "Why is order-processor slow?"                           │
│                                                                  │
│  LLM calls MCP tool: coral_query_summary({                      │
│      service: "order-processor",                                │
│      since: "5m",                                               │
│      include_profiling: true                                    │
│  })                                                             │
└─────────────────────────────────────────────────────────────────┘
                              │
                              │ MCP Call
                              ↓
┌─────────────────────────────────────────────────────────────────┐
│  Colony: Enriched Query Engine                                  │
│                                                                  │
│  [Multi-Dimensional Join in DuckDB]                             │
│                                                                  │
│  SELECT                                                         │
│      -- System metrics (RFD 071)                                │
│      avg(sm.cpu_utilization) as avg_cpu,                        │
│      max(sm.cpu_utilization) as max_cpu,                        │
│                                                                  │
│      -- Top-K CPU hotspots (RFD 072)                            │
│      (SELECT top_stacks FROM get_top_k_hotspots(...)) as        │
│        profiling_summary,                                       │
│                                                                  │
│      -- Deployment context (RFD 072)                            │
│      bm.build_id,                                               │
│      bm.first_seen as deployed_at                               │
│                                                                  │
│  FROM system_metrics_summaries sm                               │
│  JOIN cpu_profile_summaries ps ON ...                           │
│  JOIN binary_metadata_registry bm ON ...                        │
│  WHERE service_id = 'order-processor'                           │
│    AND bucket_time > NOW() - INTERVAL 5 MINUTES;                │
└─────────────────────────────────────────────────────────────────┘
                              │
                              │ Enriched Summary
                              ↓
┌─────────────────────────────────────────────────────────────────┐
│  MCP Tool Response (JSON)                                       │
│                                                                  │
│  {                                                              │
│    "service": "order-processor",                                │
│    "status": "degraded",                                        │
│    "metrics": {                                                 │
│      "cpu_utilization": { "avg": 0.88, "max": 0.94 },          │
│      "p99_latency_ms": 450,                                     │
│      "error_rate": 0.028                                        │
│    },                                                           │
│    "profiling_summary": {                                       │
│      "top_cpu_hotspots": [                                      │
│        {                                                        │
│          "rank": 1,                                             │
│          "frames": ["main", "processOrder",                     │
│                     "validateSignature", "rsa.Verify"],         │
│          "percentage": 42.5,                                    │
│          "sample_count": 2834,                                  │
│          "category": "business_logic"                           │
│        },                                                       │
│        {                                                        │
│          "rank": 2,                                             │
│          "frames": ["runtime", "gcBgMarkWorker"],               │
│          "percentage": 12.0,                                    │
│          "sample_count": 800,                                   │
│          "category": "gc"                                       │
│        }                                                        │
│      ],                                                         │
│      "total_samples": 6667,                                     │
│      "sampling_period": "5m"                                    │
│    },                                                           │
│    "deployment": {                                              │
│      "build_id": "sha256:abc123def456...",                      │
│      "deployed_at": "2025-12-16T10:00:00Z",                     │
│      "age": "2h15m"                                             │
│    },                                                           │
│    "regression_indicators": [                                   │
│      {                                                          │
│        "type": "new_hotspot",                                   │
│        "message": "validateSignature (42.5%) was not in top-5  │
│                    before this deployment",                     │
│        "baseline_percentage": 8.2,                              │
│        "current_percentage": 42.5,                              │
│        "delta": "+34.3%"                                        │
│      }                                                          │
│    ]                                                            │
│  }                                                              │
└─────────────────────────────────────────────────────────────────┘
                              │
                              │ LLM Interpretation
                              ↓
┌─────────────────────────────────────────────────────────────────┐
│  LLM Reasoning (Agentic Decision Tree)                          │
│                                                                  │
│  Analysis:                                                      │
│  • High CPU (88% avg, 94% max) → CPU-bound workload            │
│  • Top hotspot: validateSignature (42.5%)                       │
│  • Category: business_logic → Not GC or I/O issue              │
│  • Regression indicator: +34.3% increase since deployment      │
│                                                                  │
│  Decision:                                                      │
│  "The service is CPU-bound due to excessive signature           │
│   validation (42.5% of CPU time). This is a NEW hotspot        │
│   (+34.3%) since build abc123 was deployed 2h ago.             │
│                                                                  │
│   Root Cause: Recent deployment introduced performance          │
│   regression in validateSignature function.                     │
│                                                                  │
│   Next Steps:                                                   │
│   1. Run high-frequency profiling on validateSignature:        │
│      coral debug cpu-profile --service order-processor \        │
│             --duration 30s --frequency 99                       │
│   2. Compare implementation with previous build                 │
│   3. Check if signature algorithm changed or key size increased"│
└─────────────────────────────────────────────────────────────────┘
```

## Component Changes

1. **Colony (DuckDB Query Engine)**:
    - New stored function: `get_enriched_summary(service_id, time_range)`
    - Multi-dimensional join across:
        - `system_metrics_summaries` (RFD 071)
        - `cpu_profile_summaries` (RFD 072)
        - `binary_metadata_registry` (RFD 072)
    - Top-K aggregation: `get_top_k_hotspots(service_id, time_range, k=5)`
    - Regression detection: `compare_hotspots_with_baseline(current_build_id,
      previous_build_id)`

2. **Colony (MCP Server)**:
    - Extend `coral_query_summary` tool in `internal/colony/mcp/tools.go`
    - Add optional parameter: `include_profiling: bool` (default: true)
    - Add optional parameter: `top_k_hotspots: int` (default: 5)
    - Serialize profiling data into structured JSON format
    - **New tool**: `coral_debug_cpu_profile` in `internal/colony/mcp/tools.go`
    - Wrapper around RFD 070's `ProfileCPU` RPC
    - Convert profiling results to JSON format for LLM consumption

3. **Colony (RPC Handler)**:
    - Update `QueryUnifiedSummary` in `proto/coral/colony/v1/colony.proto`
    - Add `ProfilingSummary` message to response

4. **CLI (Optional)**:
    - Update `coral query summary --include-profiling` flag
    - Pretty-print profiling hotspots in terminal output

## Implementation Plan

### Phase 1: DuckDB Query Functions

- [ ] Create `get_top_k_hotspots` DuckDB function
    - [ ] Join `cpu_profile_summaries` with `profile_frame_dictionary`
    - [ ] Aggregate by stack (sum sample_count across time range)
    - [ ] Decode integer arrays to frame names
    - [ ] ORDER BY sample_count DESC LIMIT k
    - [ ] Return as JSON array

- [ ] Create `get_enriched_summary` DuckDB function
    - [ ] Join system metrics + profiling + binary metadata
    - [ ] Calculate avg/max/p95 for metrics
    - [ ] Call `get_top_k_hotspots` for profiling data
    - [ ] Include active build_id and deployment timestamp
    - [ ] Return as structured JSON

- [ ] Create `compare_hotspots_with_baseline` DuckDB function
    - [ ] Query hotspots for current build_id
    - [ ] Query hotspots for previous build_id
    - [ ] Calculate deltas (percentage change)
    - [ ] Detect new hotspots (not in baseline top-K)
    - [ ] Return regression indicators as JSON

### Phase 2: Protobuf & API Changes

- [ ] Define `ProfilingSummary` message in
  `proto/coral/colony/v1/colony.proto`
- [ ] Define `CPUHotspot` message with rank, frames, percentage, category
- [ ] Define `RegressionIndicator` message for deployment comparisons
- [ ] Update `QueryUnifiedSummaryResponse` to include `ProfilingSummary`
- [ ] Update `QueryUnifiedSummaryRequest` to include `include_profiling` flag

### Phase 3: Colony Integration

- [ ] Implement `GetEnrichedSummary` in
  `internal/colony/database/profiling_summary.go`
- [ ] Wire up to `QueryUnifiedSummary` RPC handler
- [ ] Add configuration: `profiling.enable_summary_enrichment` (default: true)
- [ ] Add configuration: `profiling.top_k_hotspots` (default: 5, max: 20)
- [ ] Add caching for baseline hotspots (avoid re-querying old build_ids)

### Phase 4: MCP Tool Enhancement

**coral_query_summary (Enhanced):**

- [ ] Update `coral_query_summary` tool definition in MCP server
- [ ] Add `include_profiling` parameter (boolean, default: true)
- [ ] Add `top_k` parameter (integer, default: 5)
- [ ] Serialize `ProfilingSummary` into MCP tool response
- [ ] Add examples to MCP tool description for LLM guidance

**coral_debug_cpu_profile (New):**

- [ ] Create `coral_debug_cpu_profile` tool in MCP server
- [ ] Wrap RFD 070's `ProfileCPU` RPC call
- [ ] Add parameters: service, pod, duration_seconds, frequency_hz, format
- [ ] Convert ProfileCPUResponse to JSON format for LLM
- [ ] Add "insights" field: hottest_function, percentage, unique_stacks
- [ ] Support both "json" and "folded" output formats
- [ ] Add usage guidance in tool description for LLM

### Phase 5: Testing & Validation

- [ ] Unit tests for `get_top_k_hotspots` query
- [ ] Unit tests for `compare_hotspots_with_baseline` query
- [ ] Integration tests: Full enriched summary generation
- [ ] E2E test: Deploy two versions, verify regression detection
- [ ] MCP tool test: Call from Claude Desktop, verify LLM can interpret
- [ ] Performance test: Measure query latency (target: <200ms for 5m time range)

## API Changes

### Protobuf Messages

```protobuf
// Enhanced query summary with profiling data
message QueryUnifiedSummaryRequest {
    string service_id = 1;
    google.protobuf.Duration time_range = 2;
    bool include_profiling = 3;  // Default: true
    int32 top_k_hotspots = 4;    // Default: 5, max: 20
}

message QueryUnifiedSummaryResponse {
    ServiceHealthSummary health = 1;
    ProfilingSummary profiling = 2;  // New field
    DeploymentContext deployment = 3; // New field
    repeated RegressionIndicator regressions = 4; // New field
}

message ProfilingSummary {
    repeated CPUHotspot top_cpu_hotspots = 1;
    uint64 total_samples = 2;
    string sampling_period = 3;  // e.g., "5m"
    string build_id = 4;
}

message CPUHotspot {
    int32 rank = 1;                 // 1-based rank (hottest first)
    repeated string frames = 2;     // Stack frames (root to leaf)
    double percentage = 3;          // Percentage of total CPU time
    uint64 sample_count = 4;        // Raw sample count
    HotspotCategory category = 5;   // Categorized for LLM interpretation
}

enum HotspotCategory {
    UNKNOWN = 0;
    BUSINESS_LOGIC = 1;
    GC = 2;
    SERIALIZATION = 3;
    NETWORK = 4;
    DISK_IO = 5;
    LOCKS = 6;
    CRYPTO = 7;
}

message DeploymentContext {
    string build_id = 1;
    google.protobuf.Timestamp deployed_at = 2;
    string age = 3;  // Human-readable age (e.g., "2h15m")
}

message RegressionIndicator {
    RegressionType type = 1;
    string message = 2;
    double baseline_percentage = 3;
    double current_percentage = 4;
    double delta = 5;  // Percentage point change
}

enum RegressionType {
    NEW_HOTSPOT = 0;        // Function not in baseline top-K
    INCREASED_HOTSPOT = 1;  // Function increased >10% points
    DECREASED_HOTSPOT = 2;  // Function decreased >10% points (optimization)
}
```

### MCP Tool Definitions

**Tool 1: coral_query_summary (Enhanced)**

```json
{
  "name": "coral_query_summary",
  "description": "Get an enriched health summary for a service including system metrics, CPU profiling hotspots, and deployment context. Use this as the FIRST tool when diagnosing performance issues to get a complete picture before drilling down with specialized tools.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "service": {
        "type": "string",
        "description": "Service name (e.g., 'order-processor')"
      },
      "since": {
        "type": "string",
        "description": "Time range (e.g., '5m', '1h', '24h')",
        "default": "5m"
      },
      "include_profiling": {
        "type": "boolean",
        "description": "Include CPU profiling hotspots in summary (default: true)",
        "default": true
      },
      "top_k": {
        "type": "integer",
        "description": "Number of top CPU hotspots to include (default: 5, max: 20)",
        "default": 5,
        "minimum": 1,
        "maximum": 20
      }
    },
    "required": ["service"]
  }
}
```

**Tool 2: coral_debug_cpu_profile (New)**

This tool exposes RFD 070's on-demand CPU profiling to LLMs for detailed
analysis.

```json
{
  "name": "coral_debug_cpu_profile",
  "description": "Collect a high-frequency CPU profile (99Hz) for detailed analysis of specific functions. Use this AFTER coral_query_summary identifies a CPU hotspot that needs line-level investigation. Returns flame graph in collapsed stack format.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "service": {
        "type": "string",
        "description": "Service name (e.g., 'order-processor')"
      },
      "pod": {
        "type": "string",
        "description": "Optional: Specific pod name if service has multiple instances"
      },
      "duration_seconds": {
        "type": "integer",
        "description": "Profiling duration in seconds (default: 30, max: 300)",
        "default": 30,
        "minimum": 10,
        "maximum": 300
      },
      "frequency_hz": {
        "type": "integer",
        "description": "Sampling frequency in Hz (default: 99, max: 999)",
        "default": 99,
        "minimum": 10,
        "maximum": 999
      },
      "format": {
        "type": "string",
        "enum": ["folded", "json"],
        "description": "Output format: 'folded' for flame graphs, 'json' for programmatic access",
        "default": "json"
      }
    },
    "required": ["service"]
  }
}
```

**Tool Response (JSON format):**

```json
{
  "service": "order-processor",
  "pod": "order-processor-7d8f9c",
  "duration_seconds": 30,
  "frequency_hz": 99,
  "total_samples": 2970,
  "lost_samples": 0,
  "build_id": "sha256:abc123def456...",
  "stacks": [
    {
      "frames": [
        "main",
        "processOrder",
        "validateSignature",
        "crypto/rsa.VerifyPKCS1v15",
        "crypto/rsa.decrypt",
        "crypto/internal/bigmod.(*Nat).Mul"
      ],
      "sample_count": 1263,
      "percentage": 42.5
    },
    {
      "frames": ["runtime", "gcBgMarkWorker", "runtime.gcDrain"],
      "sample_count": 356,
      "percentage": 12.0
    }
  ],
  "insights": {
    "hottest_function": "crypto/internal/bigmod.(*Nat).Mul",
    "hottest_function_percentage": 42.5,
    "total_unique_stacks": 247
  }
}
```

**Tool Response (Folded format for flame graphs):**

```
main;processOrder;validateSignature;crypto/rsa.VerifyPKCS1v15;crypto/rsa.decrypt;crypto/internal/bigmod.(*Nat).Mul 1263
runtime;gcBgMarkWorker;runtime.gcDrain 356
main;processOrder;saveOrder;proto.Marshal;proto.(*Buffer).EncodeVarint 178
...
```

**Usage in LLM Decision Tree:**

```
When coral_query_summary shows:
  - top_hotspot.category == BUSINESS_LOGIC
  - top_hotspot.percentage > 30%

→ Call coral_debug_cpu_profile to get detailed stack traces
→ Analyze returned stacks to identify specific expensive operations
→ Recommend code optimization or configuration changes
```

### coral_query_summary Response Example (Enhanced)

```json
{
  "service": "order-processor",
  "status": "degraded",
  "metrics": {
    "cpu_utilization": {
      "avg": 0.88,
      "max": 0.94,
      "p95": 0.91
    },
    "memory_utilization": {
      "avg": 0.72,
      "max": 0.78
    },
    "p50_latency_ms": 125,
    "p95_latency_ms": 380,
    "p99_latency_ms": 450,
    "request_rate": 1250.5,
    "error_rate": 0.028
  },
  "profiling_summary": {
    "top_cpu_hotspots": [
      {
        "rank": 1,
        "frames": [
          "main",
          "processOrder",
          "validateSignature",
          "crypto/rsa.VerifyPKCS1v15"
        ],
        "percentage": 42.5,
        "sample_count": 2834,
        "category": "CRYPTO"
      },
      {
        "rank": 2,
        "frames": ["runtime", "gcBgMarkWorker"],
        "percentage": 12.0,
        "sample_count": 800,
        "category": "GC"
      },
      {
        "rank": 3,
        "frames": [
          "main",
          "processOrder",
          "saveOrder",
          "proto.Marshal"
        ],
        "percentage": 8.5,
        "sample_count": 567,
        "category": "SERIALIZATION"
      },
      {
        "rank": 4,
        "frames": [
          "main",
          "httpHandler",
          "net/http.readRequest"
        ],
        "percentage": 6.2,
        "sample_count": 413,
        "category": "NETWORK"
      },
      {
        "rank": 5,
        "frames": [
          "main",
          "processOrder",
          "validateInput",
          "regexp.Match"
        ],
        "percentage": 4.8,
        "sample_count": 320,
        "category": "BUSINESS_LOGIC"
      }
    ],
    "total_samples": 6667,
    "sampling_period": "5m",
    "build_id": "sha256:abc123def456..."
  },
  "deployment": {
    "build_id": "sha256:abc123def456...",
    "deployed_at": "2025-12-16T10:00:00Z",
    "age": "2h15m"
  },
  "regression_indicators": [
    {
      "type": "NEW_HOTSPOT",
      "message": "crypto/rsa.VerifyPKCS1v15 (42.5%) was not in top-5 before this deployment",
      "baseline_percentage": 8.2,
      "current_percentage": 42.5,
      "delta": 34.3
    },
    {
      "type": "INCREASED_HOTSPOT",
      "message": "runtime.gcBgMarkWorker increased from 4.5% to 12.0%",
      "baseline_percentage": 4.5,
      "current_percentage": 12.0,
      "delta": 7.5
    }
  ],
  "issues_detected": [
    "⚠️  High CPU utilization: 88% avg, 94% max (threshold: 80%)",
    "⚠️  P99 latency spike: 450ms (baseline: 89ms)",
    "⚠️  Performance regression: validateSignature consuming 42.5% CPU (was 8.2%)"
  ],
  "root_cause_hypothesis": "Recent deployment (build abc123, 2h15m ago) introduced CPU-bound performance regression in signature validation. The validateSignature function now consumes 42.5% of CPU time (up from 8.2% baseline), likely due to increased RSA key size or more frequent validation calls."
}
```

## DuckDB Query Implementation

### Top-K Hotspots Query

```sql
-- Function: get_top_k_hotspots(service_id, time_range, k)
CREATE OR REPLACE FUNCTION get_top_k_hotspots(
    p_service_id TEXT,
    p_since INTERVAL,
    p_top_k INTEGER DEFAULT 5
) RETURNS JSON AS $$
WITH aggregated_stacks AS (
    -- Aggregate samples across time range
    SELECT
        p.stack_frame_ids,
        SUM(p.sample_count) as total_samples
    FROM cpu_profile_summaries p
    WHERE p.service_id = p_service_id
      AND p.bucket_time > NOW() - p_since
    GROUP BY p.stack_frame_ids
),
total_samples_count AS (
    SELECT SUM(total_samples) as total FROM aggregated_stacks
),
ranked_stacks AS (
    SELECT
        ROW_NUMBER() OVER (ORDER BY a.total_samples DESC) as rank,
        -- Decode frame IDs to frame names
        ARRAY(
            SELECT d.frame_name
            FROM unnest(a.stack_frame_ids) WITH ORDINALITY AS u(frame_id, ord)
            JOIN profile_frame_dictionary d ON d.frame_id = u.frame_id
            ORDER BY u.ord
        ) as frames,
        a.total_samples as sample_count,
        (a.total_samples * 100.0 / t.total) as percentage
    FROM aggregated_stacks a
    CROSS JOIN total_samples_count t
    ORDER BY a.total_samples DESC
    LIMIT p_top_k
)
SELECT json_group_array(
    json_object(
        'rank', rank,
        'frames', json_array_elements(frames),
        'percentage', ROUND(percentage, 1),
        'sample_count', sample_count,
        'category', categorize_hotspot(frames)
    )
) FROM ranked_stacks;
$$;

-- Function: categorize_hotspot(frames)
-- Pattern matching to categorize stack traces
CREATE OR REPLACE FUNCTION categorize_hotspot(frames TEXT[])
RETURNS TEXT AS $$
    CASE
        WHEN frames::TEXT LIKE '%runtime.gc%'
          OR frames::TEXT LIKE '%runtime.scan%'
          OR frames::TEXT LIKE '%runtime.mark%'
        THEN 'GC'

        WHEN frames::TEXT LIKE '%proto.Marshal%'
          OR frames::TEXT LIKE '%proto.Unmarshal%'
          OR frames::TEXT LIKE '%json.Marshal%'
          OR frames::TEXT LIKE '%json.Unmarshal%'
        THEN 'SERIALIZATION'

        WHEN frames::TEXT LIKE '%kernel%tcp%'
          OR frames::TEXT LIKE '%kernel%ip%'
          OR frames::TEXT LIKE '%kernel%net%'
          OR frames::TEXT LIKE '%net/http%'
        THEN 'NETWORK'

        WHEN frames::TEXT LIKE '%kernel%read%'
          OR frames::TEXT LIKE '%kernel%write%'
          OR frames::TEXT LIKE '%kernel%block%'
        THEN 'DISK_IO'

        WHEN frames::TEXT LIKE '%sync.Mutex%'
          OR frames::TEXT LIKE '%sync.RWMutex%'
        THEN 'LOCKS'

        WHEN frames::TEXT LIKE '%crypto/%'
          OR frames::TEXT LIKE '%rsa.%'
          OR frames::TEXT LIKE '%aes.%'
        THEN 'CRYPTO'

        ELSE 'BUSINESS_LOGIC'
    END
$$;
```

### Regression Detection Query

```sql
-- Function: compare_hotspots_with_baseline(current_build_id, previous_build_id)
CREATE OR REPLACE FUNCTION compare_hotspots_with_baseline(
    p_current_build_id TEXT,
    p_previous_build_id TEXT,
    p_top_k INTEGER DEFAULT 5
) RETURNS JSON AS $$
WITH current_hotspots AS (
    SELECT
        stack_frame_ids,
        SUM(sample_count) as samples,
        (SUM(sample_count) * 100.0 / SUM(SUM(sample_count)) OVER ()) as pct
    FROM cpu_profile_summaries
    WHERE build_id = p_current_build_id
    GROUP BY stack_frame_ids
    ORDER BY samples DESC
    LIMIT p_top_k
),
baseline_hotspots AS (
    SELECT
        stack_frame_ids,
        SUM(sample_count) as samples,
        (SUM(sample_count) * 100.0 / SUM(SUM(sample_count)) OVER ()) as pct
    FROM cpu_profile_summaries
    WHERE build_id = p_previous_build_id
    GROUP BY stack_frame_ids
),
regression_analysis AS (
    SELECT
        c.stack_frame_ids,
        COALESCE(b.pct, 0) as baseline_pct,
        c.pct as current_pct,
        (c.pct - COALESCE(b.pct, 0)) as delta,
        CASE
            WHEN b.pct IS NULL THEN 'NEW_HOTSPOT'
            WHEN (c.pct - b.pct) > 10 THEN 'INCREASED_HOTSPOT'
            WHEN (b.pct - c.pct) > 10 THEN 'DECREASED_HOTSPOT'
            ELSE NULL
        END as regression_type
    FROM current_hotspots c
    LEFT JOIN baseline_hotspots b ON c.stack_frame_ids = b.stack_frame_ids
    WHERE regression_type IS NOT NULL
)
SELECT json_group_array(
    json_object(
        'type', regression_type,
        'message', format_regression_message(regression_type, stack_frame_ids,
baseline_pct, current_pct),
        'baseline_percentage', ROUND(baseline_pct, 1),
        'current_percentage', ROUND(current_pct, 1),
        'delta', ROUND(delta, 1)
    )
) FROM regression_analysis;
$$;
```

### Enriched Summary Query

```sql
-- Function: get_enriched_summary(service_id, time_range)
CREATE OR REPLACE FUNCTION get_enriched_summary(
    p_service_id TEXT,
    p_since INTERVAL
) RETURNS JSON AS $$
WITH system_metrics AS (
    SELECT
        AVG(cpu_utilization) as avg_cpu,
        MAX(cpu_utilization) as max_cpu,
        percentile_cont(0.95) WITHIN GROUP (ORDER BY cpu_utilization) as p95_cpu,
        AVG(memory_usage) as avg_memory,
        MAX(memory_usage) as max_memory
    FROM system_metrics_summaries
    WHERE service_id = p_service_id
      AND bucket_time > NOW() - p_since
),
current_deployment AS (
    SELECT
        build_id,
        first_seen as deployed_at
    FROM binary_metadata_registry
    WHERE service_id = p_service_id
    ORDER BY first_seen DESC
    LIMIT 1
),
previous_deployment AS (
    SELECT build_id
    FROM binary_metadata_registry
    WHERE service_id = p_service_id
    ORDER BY first_seen DESC
    LIMIT 1 OFFSET 1
)
SELECT json_object(
    'service', p_service_id,
    'metrics', (
        SELECT json_object(
            'cpu_utilization', json_object(
                'avg', ROUND(avg_cpu, 2),
                'max', ROUND(max_cpu, 2),
                'p95', ROUND(p95_cpu, 2)
            ),
            'memory_utilization', json_object(
                'avg', ROUND(avg_memory / max_memory, 2)
            )
        ) FROM system_metrics
    ),
    'profiling_summary', json_object(
        'top_cpu_hotspots', get_top_k_hotspots(p_service_id, p_since, 5),
        'build_id', (SELECT build_id FROM current_deployment)
    ),
    'deployment', (
        SELECT json_object(
            'build_id', build_id,
            'deployed_at', deployed_at,
            'age', format_age(NOW() - deployed_at)
        ) FROM current_deployment
    ),
    'regression_indicators', compare_hotspots_with_baseline(
        (SELECT build_id FROM current_deployment),
        (SELECT build_id FROM previous_deployment),
        5
    )
);
$$;
```

## Agentic LLM Decision Tree

**How LLMs should interpret enriched summaries:**

### Decision Tree Logic

```
IF high_cpu (>80%) AND top_hotspot.category == BUSINESS_LOGIC:
    → Root Cause: CPU-bound application code
    → Action: Use debug_cpu_profile (99Hz) to get line-level detail
    → Command: coral debug cpu-profile --service X --duration 30s --frequency 99

ELSE IF high_cpu AND top_hotspot.category == GC:
    → Root Cause: Garbage collection pressure
    → Action: Check memory allocation patterns
    → Command: coral query summary --service X --metrics memory
    → Hypothesis: Memory leak or excessive allocations

ELSE IF high_cpu AND top_hotspot.category == CRYPTO:
    → Root Cause: Cryptographic operations consuming CPU
    → Action: Investigate algorithm choice, key size, or call frequency
    → Hypothesis: Recent change to crypto library or key size

ELSE IF high_latency AND low_cpu (<30%):
    → Root Cause: I/O or locking bottleneck
    → Action: Use uprobe tracing to measure database/API call durations
    → Command: coral debug uprobe --service X --function saveOrder

ELSE IF top_hotspot.category == NETWORK:
    → Root Cause: Network stack bottleneck
    → Action: Check system metrics for packet drops, connection churn
    → Command: coral query summary --service X --metrics network

ELSE IF regression_indicators.length > 0:
    → Root Cause: Recent deployment introduced regression
    → Action: Compare code changes between build_ids
    → Hypothesis: Performance regression in changed functions
```

### Example LLM Prompts

**Prompt for LLM System Message:**

```
When diagnosing performance issues, ALWAYS start with the coral_query_summary
tool (include_profiling: true). Interpret the response as follows:

1. Check metrics.cpu_utilization:
   - >80%: CPU-bound workload
   - <30% with high latency: I/O or locking bottleneck

2. Analyze profiling_summary.top_cpu_hotspots:
   - BUSINESS_LOGIC: Application code issue → Use debug_cpu_profile (99Hz)
   - GC: Memory pressure → Check allocation patterns
   - CRYPTO: Expensive cryptography → Review algorithm/key size
   - NETWORK/DISK_IO: Kernel bottleneck → Check system metrics
   - LOCKS: Contention → Use uprobe tracing on mutex operations

3. Check regression_indicators:
   - NEW_HOTSPOT or INCREASED_HOTSPOT → Recent deployment caused regression
   - Compare build_ids to identify code changes

4. Form a hypothesis and recommend next diagnostic steps.
```

## Performance Considerations

**Query Latency:**

- Target: <200ms for 5-minute time range
- Optimization: Pre-aggregate hotspots in background job (every 1 minute)
- Caching: Cache baseline hotspots (previous build_id) for 1 hour

**Token Efficiency:**

- Top-5 hotspots: ~500-800 tokens
- Full flame graph (50KB): ~15,000-20,000 tokens
- **Savings: 95% token reduction**

**Storage Overhead:**

- Enriched summary query is read-only (no additional storage)
- Leverages existing data from RFD 071 and RFD 072

## Security Considerations

**Code Exposure:**

- Stack traces may reveal internal function names and call paths
- Risk: Leak intellectual property or architecture details
- Mitigation: RBAC controls on MCP tool access (per RFD 058)

**Deployment Context:**

- Build IDs may reveal deployment timing or version numbers
- Risk: Timing attacks or version enumeration
- Mitigation: Restrict MCP tool access to authorized users only

## Testing Strategy

### Unit Tests

- Test `get_top_k_hotspots` query with mock profiling data
- Test `categorize_hotspot` function with various frame patterns
- Test `compare_hotspots_with_baseline` with regression scenarios

### Integration Tests

- Full enriched summary generation with real profiling data
- Deploy two versions, verify regression detection
- Test with missing baseline (first deployment)

### E2E Tests

- Call MCP tool from Claude Desktop
- Verify LLM can parse and interpret response
- Test agentic decision tree with various scenarios

### Performance Tests

- Measure query latency for 5m, 1h, 24h time ranges
- Verify <200ms target for 5m range
- Test with 10,000+ unique stacks (stress test)

## Future Work

The following features are out of scope for this RFD and may be addressed in
future enhancements:

**Automatic Hotspot Categorization** (Future - RFD TBD)

- Pattern-based categorization of stack traces into semantic categories
- Categories: BUSINESS_LOGIC, GC, SERIALIZATION, CRYPTO, NETWORK, DISK_IO,
  LOCKS
- Regex rules on frame names (e.g., `runtime\.(gc|scan|mark)` → GC category)
- Benefits: Simplify LLM decision tree, reduce manual frame inspection
- Blocked by: Need to validate categorization accuracy across multiple languages
  (Go, Python, Java, Rust, etc.)
- Initial version: LLMs manually inspect frame names to determine hotspot type

**Anomaly Detection** (Future - RFD TBD)

- Machine learning to detect abnormal hotspot patterns
- Baseline learning from historical data
- Automatic alerting when hotspots deviate from baseline

**Comparative Analysis** (Enhancement)

- Compare hotspots across multiple time ranges
- Trend analysis: "This function's CPU usage increased 5% per day over the last
  week"
- Seasonal pattern detection

**Multi-Service Correlation** (Future - RFD TBD)

- Correlate hotspots across service boundaries
- Example: "Service A is slow because Service B's database calls increased"
- Distributed tracing integration (RFD 036)

**Interactive Drill-Down** (Enhancement)

- LLM can request more detail on specific hotspot
- Expand single hotspot to full call tree
- Line-level profiling for specific function

**Memory Profiling Enrichment** (Future - RFD TBD)

- Add memory allocation hotspots to summary
- Correlate memory allocations with GC pressure
- Requires memory profiling implementation (RFD 070 future work)

## Appendix

### Example LLM Interaction

**User Query:**
"Why is order-processor slow?"

**LLM Action 1: Get Enriched Summary**
```
Tool: coral_query_summary
Input: { "service": "order-processor", "since": "5m", "include_profiling": true
}
```

**Tool Response:**
```json
{
  "service": "order-processor",
  "metrics": { "cpu_utilization": { "avg": 0.88 }, "p99_latency_ms": 450 },
  "profiling_summary": {
    "top_cpu_hotspots": [
      {
        "rank": 1,
        "frames": ["main", "processOrder", "validateSignature",
"crypto/rsa.VerifyPKCS1v15"],
        "percentage": 42.5,
        "category": "CRYPTO"
      }
    ]
  },
  "regression_indicators": [
    {
      "type": "NEW_HOTSPOT",
      "message": "validateSignature (42.5%) was not in top-5 before deployment",
      "delta": 34.3
    }
  ]
}
```

**LLM Analysis:**

"The order-processor service is experiencing high CPU usage (88% avg) and
elevated latency (450ms P99). The root cause is excessive CPU time spent in
signature validation:

**Root Cause:**
- The `validateSignature` function (via `crypto/rsa.VerifyPKCS1v15`) is
  consuming 42.5% of CPU time
- This is a NEW hotspot that appeared after the recent deployment (build abc123,
  deployed 2h ago)
- Before this deployment, signature validation consumed only 8.2% of CPU time (
  +34.3% regression)

**Hypothesis:**
The recent deployment likely introduced:
1. Increased RSA key size (e.g., 2048 → 4096 bits), or
2. More frequent signature validation calls, or
3. A change in the signature validation logic

**Recommended Next Steps:**
1. Collect detailed CPU profile to get line-level detail on validateSignature"

**LLM Action 2: Detailed Profiling**
```
Tool: coral_debug_cpu_profile
Input: {
  "service": "order-processor",
  "duration_seconds": 30,
  "frequency_hz": 99,
  "format": "json"
}
```

**Tool Response:**
```json
{
  "service": "order-processor",
  "total_samples": 2970,
  "stacks": [
    {
      "frames": [
        "main",
        "processOrder",
        "validateSignature",
        "crypto/rsa.VerifyPKCS1v15",
        "crypto/rsa.decrypt",
        "crypto/internal/bigmod.(*Nat).Mul"
      ],
      "sample_count": 1263,
      "percentage": 42.5
    }
  ],
  "insights": {
    "hottest_function": "crypto/internal/bigmod.(*Nat).Mul",
    "hottest_function_percentage": 42.5
  }
}
```

**LLM Final Analysis:**

"Detailed profiling confirms the issue is in RSA signature verification. The
bottleneck is specifically in `crypto/internal/bigmod.(*Nat).Mul`, which
performs big integer multiplication during RSA operations.

**Root Cause Confirmed:**
The deployment increased RSA key size from 2048 to 4096 bits, which
significantly increased the cost of signature verification (big integer
operations scale with O(n²) for key size).

**Recommendations:**
1. If 4096-bit keys are not required, revert to 2048-bit keys (industry standard
   for most use cases)
2. Cache signature verification results to reduce redundant validations
3. Consider using Ed25519 signatures (25519-bit elliptic curve) which are faster
   and more secure
4. Profile the previous build to confirm 2048-bit key hypothesis:
   ```
   coral debug cpu-profile --service order-processor --build-id
<previous_build_id> --since 1h
   ```"

**Why This Works:**
- **One Tool Call**: LLM got system metrics + profiling context in a single call
- **Pre-Correlated**: Build ID, timestamps, and metrics already aligned
- **Structured Data**: Regression indicator explicitly highlighted the issue
- **Actionable**: LLM can immediately recommend next diagnostic steps

---

## References

- RFD 004: MCP Server
- RFD 067: Unified Query Interface
- RFD 071: System Metrics Collection
- RFD 072: Continuous CPU Profiling
- MCP Protocol Specification: https://modelcontextprotocol.io/
