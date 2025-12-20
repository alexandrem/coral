---
rfd: "078"
title: "Error Log Aggregation for LLM-Driven Debugging"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "025", "036", "067", "074" ]
database_migrations: [ ]
areas: [ "agent", "colony", "observability", "logging", "ai" ]
---

# RFD 078 - Error Log Aggregation for LLM-Driven Debugging

**Status:** 🚧 Draft

## Summary

Implement targeted error log aggregation via OTLP to provide LLMs and operators with critical debugging context during incident response. Unlike traditional log aggregation platforms (Elasticsearch, Loki), Coral focuses on **actionable error logs only** with aggressive filtering, short retention, and trace correlation to minimize storage overhead while maximizing diagnostic value.

Key capabilities:

- **Selective Ingestion**: Only ERROR and WARN level logs (filter at source)
- **OTLP-Based**: Leverage existing OTLP receiver (RFD 025), no new protocols
- **Trace Correlation**: Link logs to distributed traces via trace_id (RFD 036)
- **Storage Efficient**: De-duplication, pattern aggregation, short retention (7 days)
- **LLM Integration**: Enrich `coral_query_summary` (RFD 074) with recent error context
- **Query Patterns**: Recent errors, trace-correlated logs, error rate trends

This completes the "three pillars of observability" (metrics, traces, logs) within Coral's lightweight, debugging-focused architecture.

## Problem

### Current Gaps

**Incomplete Diagnostics:**

Coral can answer:
- ✅ "Which requests are slow?" → Distributed traces (RFD 036)
- ✅ "Which functions consume CPU?" → Profiling (RFD 070, 072, 076)
- ✅ "What are the error rates?" → Metrics (RFD 032, 071)

But **cannot** answer:
- ❌ "What error message appeared for this slow trace?"
- ❌ "Why did this request fail?" (need exception stack trace)
- ❌ "Are errors increasing?" (need error log time-series)
- ❌ "What's the most common error in the last hour?"

**Example Problem:**

```
User: "Why is the payment service failing?"

Current approach:
1. Check metrics: Error rate is 5.2%
2. Check traces: Some traces have status 500
3. Missing: What error message/exception caused the failure?

Gap: No access to application error logs. Must SSH to pods and grep logs manually.
```

### Why Traditional Log Platforms Don't Fit

**Storage Overhead:**
- Elasticsearch/Loki index all logs (INFO, DEBUG, TRACE)
- High-throughput services: 10GB+ logs/day per service
- Coral uses DuckDB (embedded), not designed for full-text search at scale

**Query Patterns:**
- Traditional: Compliance, audit trails, full-text search
- Coral: Debugging, error correlation, trace context

**Retention:**
- Traditional: 30-90 days (compliance requirements)
- Coral: 7 days (debugging recent issues)

**Cost:**
- Traditional: Expensive (Elasticsearch cluster, SSD storage)
- Coral: Lightweight (DuckDB, aggressive filtering)

### What Coral Needs

A **targeted log aggregation system** optimized for debugging, not compliance:

1. **Errors Only**: Ingest ERROR and WARN logs, discard INFO/DEBUG (95% reduction)
2. **Trace Correlation**: Link logs to distributed traces for context
3. **Pattern Aggregation**: Group identical errors to reduce storage
4. **Short Retention**: 7 days (recent debugging, not long-term audit)
5. **LLM Integration**: Provide error context to AI for root cause analysis

### Use Cases

**1. Trace-Correlated Error Logs**

```
User: "Why did trace abc123 fail?"

Coral shows:
- Trace: abc123 (5.2s, HTTP 500)
- Span: payment-svc (4.8s)
- Error Log: "Database connection timeout after 5s (connection pool exhausted)"
```

**2. Recent Error Summary**

```
User: "What errors happened in payment-svc in the last hour?"

Coral shows:
- 23 errors in last hour
- Top 3 error patterns:
  1. "Database connection timeout" (15 occurrences)
  2. "Payment gateway returned 503" (5 occurrences)
  3. "Invalid card number format" (3 occurrences)
```

**3. LLM-Driven Diagnosis**

```
User: "Why is payment-svc degraded?"

LLM queries coral_query_summary:
{
  "error_rate": 5.2%,
  "recent_errors": [
    "Database connection timeout after 5s (15 occurrences in last 5m)",
    "Payment gateway returned 503 (5 occurrences)"
  ],
  "diagnosis": "payment-svc is failing due to database connection pool exhaustion"
}
```

**4. Error Rate Trends**

```
coral query logs --service payment-svc --level error --since 24h --group-by-hour

Error Rate Trend (last 24h):
Hour 00:00 - 01:00: 12 errors
Hour 01:00 - 02:00: 8 errors
...
Hour 14:00 - 15:00: 145 errors  ← SPIKE
Hour 15:00 - 16:00: 152 errors
```

## Solution

### Architecture Overview

Extend Coral's OTLP receiver (RFD 025) to accept log records, filter aggressively at ingestion, and store only ERROR/WARN logs in DuckDB with trace correlation.

```
┌─────────────────────────────────────────────────────────────────┐
│ Application (Go/Python/Java/etc.)                               │
│                                                                  │
│  logger.Error("Database connection timeout", {                  │
│      "trace_id": ctx.TraceID(),                                 │
│      "timeout_seconds": 5,                                      │
│      "pool_size": 10                                            │
│  })                                                             │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         │ OTLP/gRPC (logs)
                         ↓
┌─────────────────────────────────────────────────────────────────┐
│ Agent: OTLP Receiver (RFD 025)                                  │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │ ConsumeLogs(ctx, logs)                                   │   │
│  │                                                          │   │
│  │  FOR EACH log IN logs:                                  │   │
│  │    IF log.severity < WARN:                              │   │
│  │      DISCARD  // Filter INFO/DEBUG at source            │   │
│  │                                                          │   │
│  │    EXTRACT:                                             │   │
│  │      - trace_id (from log attributes)                   │   │
│  │      - message (log body)                               │   │
│  │      - attributes (structured fields)                   │   │
│  │      - severity (ERROR/WARN)                            │   │
│  │                                                          │   │
│  │    STORE in DuckDB (error_logs_local table)             │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │ DuckDB: error_logs_local                                 │   │
│  │                                                          │   │
│  │  Retention: 24 hours                                     │   │
│  │  Indexed by: timestamp, service_name, trace_id           │   │
│  └──────────────────────────────────────────────────────────┘   │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         │ Colony polls via QueryErrorLogs RPC
                         ↓
┌─────────────────────────────────────────────────────────────────┐
│ Colony: Error Log Aggregation                                   │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │ LogPoller (similar to BeylaPoller, RFD 036)             │   │
│  │                                                          │   │
│  │  EVERY 30 seconds:                                      │   │
│  │    FOR EACH agent:                                      │   │
│  │      QueryErrorLogs(since: last_poll_time)              │   │
│  │      INSERT INTO error_logs (with agent_id)             │   │
│  │      UPDATE pattern_aggregates                          │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │ DuckDB: error_logs                                       │   │
│  │                                                          │   │
│  │  Retention: 7 days                                       │   │
│  │  Indexed by: timestamp, service_name, trace_id,          │   │
│  │              message_hash, severity                      │   │
│  │                                                          │   │
│  │  Table: error_log_patterns (aggregated)                 │   │
│  │    - message_template (e.g., "DB timeout after ? sec")  │   │
│  │    - occurrence_count                                    │   │
│  │    - first_seen, last_seen                              │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │ MCP Tool: coral_query_logs (RFD 067)                     │   │
│  │                                                          │   │
│  │  LLM can query:                                         │   │
│  │    - Recent errors for service                          │   │
│  │    - Logs for specific trace_id                         │   │
│  │    - Error patterns (grouped by message template)       │   │
│  │    - Time-series error rates                            │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

### Key Design Decisions

**1. Aggressive Filtering at Source**

Only ingest ERROR and WARN severity logs:

```go
// In OTLP receiver
func (r *Receiver) ConsumeLogs(ctx context.Context, logs plog.Logs) error {
    for _, logRecord := range logs.ResourceLogs().LogRecords() {
        severity := logRecord.SeverityNumber()

        // Filter: Only ERROR (17-20) and WARN (13-16)
        if severity < plog.SeverityNumberWarn {
            continue  // Discard INFO/DEBUG/TRACE
        }

        // Store in DuckDB
        r.storage.StoreErrorLog(logRecord)
    }
}
```

**Storage Reduction:**
- Typical application: 90-95% of logs are INFO/DEBUG
- Filtering reduces ingestion volume by 10-20x

**2. Pattern Aggregation for De-Duplication**

Group identical error messages to reduce storage:

```sql
-- Table: error_log_patterns
CREATE TABLE error_log_patterns (
    pattern_id UUID PRIMARY KEY,
    service_id UUID NOT NULL,
    message_template TEXT NOT NULL,  -- "DB timeout after ? sec"
    message_hash VARCHAR(64) NOT NULL,  -- Hash of template
    severity VARCHAR(10) NOT NULL,

    -- Aggregation
    occurrence_count INTEGER NOT NULL,
    first_seen TIMESTAMPTZ NOT NULL,
    last_seen TIMESTAMPTZ NOT NULL,

    -- Example parameters (JSON array)
    example_attributes JSON,  -- [{"timeout_sec": 5}, {"timeout_sec": 10}]

    INDEX idx_patterns_service (service_id, last_seen DESC),
    INDEX idx_patterns_hash (message_hash, last_seen DESC)
);
```

**Example:**

```
Raw logs (1000 occurrences):
  "Database connection timeout after 5 seconds"
  "Database connection timeout after 10 seconds"
  "Database connection timeout after 3 seconds"

Pattern aggregation (1 row):
  message_template: "Database connection timeout after ? seconds"
  occurrence_count: 1000
  example_attributes: [{"timeout_sec": 5}, {"timeout_sec": 10}, {"timeout_sec": 3}]
```

**Storage Reduction:** 100-1000x for repeated errors

**3. Trace Correlation (Zero-Config)**

Link logs to distributed traces automatically:

```go
// Application code (using OpenTelemetry SDK)
ctx := trace.SpanFromContext(ctx).SpanContext()

logger.Error("Payment failed",
    slog.String("trace_id", ctx.TraceID().String()),
    slog.String("error_code", "PAYMENT_GATEWAY_TIMEOUT"),
)

// OTLP exporter automatically includes trace_id in log attributes
```

**Query:**

```sql
-- Get logs for specific trace
SELECT * FROM error_logs
WHERE trace_id = 'abc123def456'
ORDER BY timestamp ASC;

-- Join with traces
SELECT
    t.trace_id,
    t.duration_ms,
    t.status_code,
    l.message,
    l.severity,
    l.attributes
FROM beyla_traces t
LEFT JOIN error_logs l ON t.trace_id = l.trace_id
WHERE t.trace_id = 'abc123def456';
```

**4. Short Retention for Efficient Storage**

- **Agent:** 24 hours (recent errors for immediate debugging)
- **Colony:** 7 days (historical error patterns, trend analysis)

**Rationale:**
- Debugging focuses on recent errors (<24h)
- Historical compliance logs belong in dedicated log platforms (e.g., S3 + Athena)
- 7 days balances storage cost with utility

**5. Structured Attributes (JSON)**

Store log attributes as JSON for flexible querying:

```json
{
  "message": "Database connection timeout",
  "severity": "ERROR",
  "attributes": {
    "timeout_seconds": 5,
    "pool_size": 10,
    "database_host": "postgres-primary.prod",
    "error_code": "CONNECTION_TIMEOUT"
  }
}
```

**Query:**

```sql
-- Find all connection timeout errors
SELECT * FROM error_logs
WHERE message LIKE '%connection timeout%'
  AND severity = 'ERROR'
  AND timestamp > NOW() - INTERVAL '1 hour';

-- Extract specific attribute
SELECT
    message,
    json_extract(attributes, '$.timeout_seconds') AS timeout,
    count(*) AS occurrences
FROM error_logs
WHERE service_name = 'payment-svc'
  AND severity = 'ERROR'
GROUP BY message, timeout
ORDER BY occurrences DESC;
```

**6. No Full-Text Search (Intentional Limitation)**

DuckDB is not optimized for full-text search:

**What Coral Supports:**
- ✅ Pattern matching: `message LIKE '%timeout%'`
- ✅ Attribute filtering: `json_extract(attributes, '$.error_code') = 'TIMEOUT'`
- ✅ Time-range queries: `timestamp > NOW() - INTERVAL '1h'`
- ✅ Trace correlation: `trace_id = 'abc123'`

**What Coral Does NOT Support:**
- ❌ Full-text search: "Find all logs mentioning user@example.com"
- ❌ Complex regex: "Find logs matching /[A-Z]{3}-\d{4}/"
- ❌ Fuzzy search: "Find logs similar to 'connection refused'"

**Recommendation:** For advanced log search, use dedicated platforms (Elasticsearch, Loki, CloudWatch Logs). Coral focuses on **error correlation for debugging**, not comprehensive log search.

## Component Changes

### 1. Agent (OTLP Receiver Extension)

**Extend `internal/agent/otlp/receiver.go`:**

```go
// Implement ConsumeLogs (already defined in RFD 025, now populate)
func (r *Receiver) ConsumeLogs(ctx context.Context, logs plog.Logs) error {
    for i := 0; i < logs.ResourceLogs().Len(); i++ {
        resourceLogs := logs.ResourceLogs().At(i)
        serviceName := extractServiceName(resourceLogs.Resource())

        for j := 0; j < resourceLogs.ScopeLogs().Len(); j++ {
            scopeLogs := resourceLogs.ScopeLogs().At(j)

            for k := 0; k < scopeLogs.LogRecords().Len(); k++ {
                logRecord := scopeLogs.LogRecords().At(k)

                // Filter: Only ERROR and WARN
                if logRecord.SeverityNumber() < plog.SeverityNumberWarn {
                    continue
                }

                // Extract trace context
                traceID := logRecord.TraceID().String()

                // Store in local DuckDB
                r.logStorage.StoreErrorLog(ctx, &ErrorLog{
                    Timestamp:   logRecord.Timestamp().AsTime(),
                    ServiceName: serviceName,
                    TraceID:     traceID,
                    Severity:    logRecord.SeverityText(),
                    Message:     logRecord.Body().AsString(),
                    Attributes:  convertAttributes(logRecord.Attributes()),
                })
            }
        }
    }
    return nil
}
```

**New Storage Interface (`internal/agent/storage/log_storage.go`):**

```go
type LogStorage interface {
    StoreErrorLog(ctx context.Context, log *ErrorLog) error
    QueryErrorLogs(ctx context.Context, filter *LogFilter) ([]*ErrorLog, error)
    CleanupOldLogs(ctx context.Context, olderThan time.Time) error
}

type ErrorLog struct {
    ID          string
    Timestamp   time.Time
    ServiceName string
    TraceID     string
    SpanID      string
    Severity    string  // "ERROR" or "WARN"
    Message     string
    Attributes  map[string]interface{}
}

type LogFilter struct {
    ServiceName string
    TraceID     string
    Severity    string
    Since       time.Time
    Until       time.Time
    Limit       int
}
```

### 2. Storage Schema

**Agent Table: `error_logs_local`**

```sql
CREATE TABLE error_logs_local (
    log_id UUID PRIMARY KEY,
    timestamp TIMESTAMPTZ NOT NULL,
    service_name TEXT NOT NULL,
    trace_id VARCHAR(32),  -- Nullable (not all logs have trace context)
    span_id VARCHAR(16),
    severity VARCHAR(10) NOT NULL,  -- 'ERROR' or 'WARN'
    message TEXT NOT NULL,
    attributes JSON,  -- Structured fields

    -- Indexed for fast queries
    INDEX idx_error_logs_timestamp (timestamp DESC),
    INDEX idx_error_logs_service (service_name, timestamp DESC),
    INDEX idx_error_logs_trace (trace_id, timestamp DESC)
);

-- Retention: 24 hours
-- Cleanup query: DELETE FROM error_logs_local WHERE timestamp < NOW() - INTERVAL '24 hours';
```

**Colony Table: `error_logs`**

```sql
CREATE TABLE error_logs (
    log_id UUID PRIMARY KEY,
    agent_id UUID NOT NULL,  -- Which agent collected this log
    timestamp TIMESTAMPTZ NOT NULL,
    service_id UUID NOT NULL,
    service_name TEXT NOT NULL,
    trace_id VARCHAR(32),
    span_id VARCHAR(16),
    severity VARCHAR(10) NOT NULL,
    message TEXT NOT NULL,
    message_hash VARCHAR(64) NOT NULL,  -- Hash for pattern grouping
    attributes JSON,

    INDEX idx_error_logs_timestamp (timestamp DESC),
    INDEX idx_error_logs_service (service_id, timestamp DESC),
    INDEX idx_error_logs_trace (trace_id, timestamp DESC),
    INDEX idx_error_logs_hash (message_hash, timestamp DESC),

    FOREIGN KEY (service_id) REFERENCES services(service_id)
);

-- Retention: 7 days
```

**Colony Table: `error_log_patterns`**

```sql
CREATE TABLE error_log_patterns (
    pattern_id UUID PRIMARY KEY,
    service_id UUID NOT NULL,
    message_template TEXT NOT NULL,  -- "DB timeout after ? sec"
    message_hash VARCHAR(64) NOT NULL UNIQUE,
    severity VARCHAR(10) NOT NULL,

    -- Aggregation stats
    occurrence_count INTEGER NOT NULL DEFAULT 1,
    first_seen TIMESTAMPTZ NOT NULL,
    last_seen TIMESTAMPTZ NOT NULL,

    -- Sample attributes (JSON array, max 10 examples)
    example_attributes JSON,

    INDEX idx_patterns_service (service_id, last_seen DESC),
    INDEX idx_patterns_count (occurrence_count DESC)
);
```

### 3. Colony API Extensions

**New RPC: `QueryErrorLogs`**

```protobuf
// proto/coral/colony/v1/colony.proto

message QueryErrorLogsRequest {
    string service_id = 1;  // Optional: filter by service
    string trace_id = 2;    // Optional: filter by trace
    string severity = 3;    // Optional: "ERROR" or "WARN"
    google.protobuf.Timestamp since = 4;
    google.protobuf.Timestamp until = 5;
    int32 limit = 6;  // Default: 100, max: 1000
    bool group_by_pattern = 7;  // Return patterns instead of individual logs
}

message QueryErrorLogsResponse {
    repeated ErrorLog logs = 1;
    repeated ErrorLogPattern patterns = 2;  // If group_by_pattern=true
    int32 total_count = 3;
}

message ErrorLog {
    string log_id = 1;
    google.protobuf.Timestamp timestamp = 2;
    string service_name = 3;
    string trace_id = 4;
    string severity = 5;
    string message = 6;
    map<string, string> attributes = 7;
}

message ErrorLogPattern {
    string pattern_id = 1;
    string service_name = 2;
    string message_template = 3;
    string severity = 4;
    int32 occurrence_count = 5;
    google.protobuf.Timestamp first_seen = 6;
    google.protobuf.Timestamp last_seen = 7;
    repeated map<string, string> example_attributes = 8;  // Max 10 examples
}
```

**Extend `QueryUnifiedSummary` (RFD 067):**

```protobuf
message QueryUnifiedSummaryResponse {
    ServiceHealthSummary health = 1;
    ProfilingSummary profiling = 2;
    DeploymentContext deployment = 3;
    repeated RegressionIndicator regressions = 4;

    // NEW: Recent error logs
    RecentErrorsSummary recent_errors = 5;
}

message RecentErrorsSummary {
    int32 error_count_last_5m = 1;
    int32 warn_count_last_5m = 2;
    repeated ErrorLogPattern top_error_patterns = 3;  // Top 5
    repeated ErrorLog recent_errors = 4;  // Last 10
}
```

### 4. CLI Commands

**Query error logs:**

```bash
coral query logs [flags]

Flags:
  --service string      Service name or ID (required)
  --trace-id string     Filter by trace ID
  --severity string     Filter by severity: error, warn (default: both)
  --since duration      Time range (default: 1h)
  --limit int           Max logs to return (default: 100)
  --group-by-pattern    Group by error pattern (default: false)
  --format string       Output format: table, json, csv (default: table)
```

**Examples:**

```bash
# Recent errors for payment-svc
$ coral query logs --service payment-svc --severity error --since 1h

Recent Error Logs (payment-svc, last 1 hour):

[2025-12-20 14:32:15] ERROR: Database connection timeout after 5 seconds
  trace_id: abc123def456
  attributes: {"pool_size": 10, "database": "postgres-primary"}

[2025-12-20 14:31:48] ERROR: Payment gateway returned 503
  trace_id: def456abc789
  attributes: {"gateway": "stripe", "status_code": 503}

[2025-12-20 14:30:22] ERROR: Invalid card number format
  trace_id: ghi789def012
  attributes: {"card_type": "visa", "validation_error": "invalid_luhn"}

Total: 23 errors in last hour

# Group by error pattern
$ coral query logs --service payment-svc --group-by-pattern --since 24h

Error Patterns (payment-svc, last 24 hours):

Pattern 1: "Database connection timeout after ? seconds"
  Occurrences: 145
  First seen: 2025-12-19 18:00:00
  Last seen:  2025-12-20 14:32:15

Pattern 2: "Payment gateway returned ?"
  Occurrences: 34
  First seen: 2025-12-20 10:15:00
  Last seen:  2025-12-20 14:31:48

Pattern 3: "Invalid card number format"
  Occurrences: 12
  First seen: 2025-12-20 08:00:00
  Last seen:  2025-12-20 14:30:22

# Logs for specific trace
$ coral query logs --trace-id abc123def456

Logs for trace abc123def456:

[2025-12-20 14:32:10] WARN: Database connection pool at 90% capacity
  service: payment-svc
  attributes: {"pool_size": 10, "active_connections": 9}

[2025-12-20 14:32:15] ERROR: Database connection timeout after 5 seconds
  service: payment-svc
  attributes: {"pool_size": 10, "database": "postgres-primary"}
```

### 5. MCP Tool Implementation (RFD 067)

**Implement `coral_query_logs` (already defined in RFD 067):**

```json
{
  "name": "coral_query_logs",
  "description": "Query application error and warning logs. Use this to understand what errors are happening in a service, get error messages for failed traces, or analyze error patterns. Only ERROR and WARN level logs are stored (INFO/DEBUG are not available).",
  "inputSchema": {
    "type": "object",
    "properties": {
      "service": {
        "type": "string",
        "description": "Service name (required)"
      },
      "trace_id": {
        "type": "string",
        "description": "Optional: Filter by trace ID to see logs for specific request"
      },
      "severity": {
        "type": "string",
        "enum": ["error", "warn", "both"],
        "default": "both",
        "description": "Log severity level"
      },
      "since": {
        "type": "string",
        "default": "1h",
        "description": "Time range (e.g., 5m, 1h, 24h)"
      },
      "limit": {
        "type": "integer",
        "default": 100,
        "description": "Max logs to return (max: 1000)"
      },
      "group_by_pattern": {
        "type": "boolean",
        "default": false,
        "description": "Group identical errors into patterns"
      }
    },
    "required": ["service"]
  }
}
```

**Example LLM Usage:**

```
User: "Why is payment-svc failing?"

LLM Action 1: Get health summary with error context
Tool: coral_query_summary
Input: { "service": "payment-svc", "since": "5m" }
Output: {
  "error_rate": 0.052,
  "recent_errors": {
    "error_count_last_5m": 23,
    "top_error_patterns": [
      {
        "message_template": "Database connection timeout after ? seconds",
        "occurrence_count": 15
      }
    ]
  }
}

LLM Action 2: Get detailed error logs
Tool: coral_query_logs
Input: { "service": "payment-svc", "severity": "error", "since": "1h" }
Output: {
  "logs": [
    {
      "timestamp": "2025-12-20T14:32:15Z",
      "message": "Database connection timeout after 5 seconds",
      "trace_id": "abc123def456",
      "attributes": { "pool_size": 10, "database": "postgres-primary" }
    }
  ]
}

LLM Response:
"payment-svc is experiencing a 5.2% error rate due to database connection pool
exhaustion. In the last hour, there were 15 'Database connection timeout' errors.
The connection pool size is 10, which appears insufficient for current load.

Root Cause: Database connection pool too small for traffic volume.

Recommendation:
1. Increase pool_size from 10 to 20-30 connections
2. Investigate slow queries that hold connections longer than expected
3. Check if database is under heavy load (use coral query metrics)"
```

## Implementation Plan

### Phase 1: Agent Log Ingestion

**Goals:** Receive and store ERROR/WARN logs via OTLP

- [ ] Implement `ConsumeLogs` in OTLP receiver
- [ ] Add severity filtering (discard INFO/DEBUG at ingestion)
- [ ] Implement `LogStorage` interface with DuckDB backend
- [ ] Create `error_logs_local` table and indexes
- [ ] Add log cleanup loop (24-hour retention)
- [ ] Extract trace_id from log attributes

**Deliverable:** Agents ingest and store error logs

### Phase 2: Colony Log Aggregation

**Goals:** Pull logs from agents, store in colony

- [ ] Create `error_logs` and `error_log_patterns` tables in colony
- [ ] Implement `LogPoller` (similar to `BeylaPoller` from RFD 036)
- [ ] Add `QueryErrorLogs` RPC to agent
- [ ] Implement pattern detection and aggregation
- [ ] Add retention cleanup (7-day retention)

**Deliverable:** Colony aggregates error logs from all agents

### Phase 3: Query API

**Goals:** Query error logs via RPC and CLI

- [ ] Implement `QueryErrorLogs` RPC in colony
- [ ] Add filtering: service, trace_id, severity, time range
- [ ] Implement pattern grouping (group_by_pattern)
- [ ] Add `coral query logs` CLI command
- [ ] Add text-based table rendering for logs

**Deliverable:** `coral query logs` returns error logs

### Phase 4: Trace Correlation

**Goals:** Link logs to distributed traces

- [ ] Index error_logs by trace_id
- [ ] Implement join query: traces + logs
- [ ] Add `--trace-id` filter to `coral query logs`
- [ ] Extend `coral query traces` to show correlated logs

**Deliverable:** Logs linked to traces for complete request context

### Phase 5: LLM Integration

**Goals:** Enrich AI diagnostics with error logs

- [ ] Extend `coral_query_summary` with `recent_errors` field
- [ ] Implement `coral_query_logs` MCP tool
- [ ] Add error pattern detection to summary
- [ ] Update LLM prompts with log querying guidance

**Deliverable:** LLM can query and interpret error logs

### Phase 6: Pattern Aggregation

**Goals:** De-duplicate repeated errors

- [ ] Implement message template extraction (replace numbers/IDs with `?`)
- [ ] Compute message hash for grouping
- [ ] Store in `error_log_patterns` table
- [ ] Add `--group-by-pattern` flag to CLI
- [ ] Display pattern occurrences and examples

**Deliverable:** Efficient storage via pattern aggregation

### Phase 7: Testing & Documentation

**Goals:** Validate end-to-end functionality

- [ ] Unit tests: OTLP log ingestion, filtering, storage
- [ ] Integration tests: Agent → Colony log aggregation
- [ ] E2E test: Application logs → OTLP → Agent → Colony → Query
- [ ] Performance test: 10,000 logs/sec ingestion (verify filtering reduces load)
- [ ] Documentation: User guide for error log querying
- [ ] Runbook: Debugging with error logs

**Deliverable:** Production-ready error log aggregation

## Storage Efficiency Analysis

### Assumptions

- **Service:** payment-svc
- **Traffic:** 1,000 req/s
- **Error Rate:** 2% (20 errors/sec)
- **Log Entry Size:** ~500 bytes average (message + attributes)

### Without Filtering (All Logs)

- **Log Rate:** 1,000 req/s * 10 logs/req = 10,000 logs/sec
- **Daily Volume:** 10,000 * 86,400 = 864 million logs/day
- **Storage:** 864M * 500 bytes = 432 GB/day
- **Retention (7 days):** 432 GB * 7 = 3 TB

**Not feasible for DuckDB.**

### With ERROR/WARN Filtering Only

- **Error Rate:** 2% of requests fail
- **Logs per error:** 2 logs (WARN + ERROR)
- **Error Log Rate:** 20 errors/sec * 2 logs = 40 logs/sec
- **Daily Volume:** 40 * 86,400 = 3.46 million logs/day
- **Storage:** 3.46M * 500 bytes = 1.73 GB/day
- **Retention (7 days):** 1.73 GB * 7 = 12 GB

**Reduction:** 250x (from 3 TB to 12 GB)

### With Pattern Aggregation

- **Unique Error Patterns:** ~50 (most errors repeat)
- **Pattern Table:** 50 rows * 1 KB = 50 KB
- **Raw Logs (for trace correlation):** Keep last 24h only (247 MB)
- **Total Storage:** 50 KB + 247 MB = 247 MB

**Reduction:** 12,000x (from 3 TB to 247 MB)

## Security Considerations

**Log Data Sensitivity:**

- Error logs may contain PII (user IDs, email addresses, IP addresses)
- **Mitigation:** Application-side log scrubbing (use structured logging, avoid logging PII)
- **Mitigation:** RBAC controls on log queries (same as RFD 058)

**Attribute Exposure:**

- Structured attributes may reveal system internals (database hosts, API keys)
- **Mitigation:** Applications should avoid logging secrets in error messages
- **Mitigation:** Colony can implement attribute redaction (e.g., mask `api_key` fields)

**Storage Access:**

- DuckDB files contain plaintext logs
- **Mitigation:** Encrypt DuckDB files at rest (filesystem encryption)
- **Mitigation:** Restrict file permissions (0600, owner read/write only)

**Audit Logging:**

- Log all `QueryErrorLogs` RPC calls
- **Mitigation:** Record requester identity, service queried, time range

## Testing Strategy

### Unit Tests

**OTLP Log Ingestion:**
- Test severity filtering (discard INFO/DEBUG)
- Test trace_id extraction from log attributes
- Test attribute parsing (JSON conversion)

**Storage:**
- Test log insertion, querying, deletion
- Test pattern aggregation (message template extraction)
- Test retention cleanup (delete logs older than 24h/7d)

### Integration Tests

**Agent → Colony Polling:**
1. Agent receives OTLP logs
2. Colony polls agent via `QueryErrorLogs` RPC
3. Verify logs stored in colony with agent_id

**Pattern Aggregation:**
1. Insert 1000 identical errors with different timestamps
2. Verify pattern table has 1 row with occurrence_count = 1000

**Trace Correlation:**
1. Insert trace with trace_id = abc123
2. Insert error log with trace_id = abc123
3. Query logs by trace_id, verify joined result

### E2E Tests

**Scenario 1: Application Error → Query**
1. Application logs error via OTLP
2. Agent ingests and stores log
3. Colony polls and aggregates log
4. User runs `coral query logs --service app --severity error`
5. Verify error appears in output

**Scenario 2: LLM Diagnosis**
1. Application experiences errors
2. User: "Why is app failing?"
3. LLM calls `coral_query_summary` → sees high error rate
4. LLM calls `coral_query_logs` → sees specific error messages
5. LLM diagnoses root cause

**Scenario 3: Trace-Correlated Logs**
1. Slow trace with trace_id = abc123
2. Error log with same trace_id
3. `coral query logs --trace-id abc123` shows correlated logs

### Performance Tests

**Ingestion Throughput:**
- Send 10,000 logs/sec to OTLP receiver
- Verify filtering reduces storage rate to ~200 logs/sec (2% error rate)
- Verify no dropped logs

**Query Performance:**
- Insert 10M error logs over 7 days
- Query recent errors (last 1h): target <200ms
- Query by trace_id: target <50ms (indexed)
- Query patterns: target <500ms (aggregation)

## Future Work

### Advanced Log Features (Future RFDs)

**1. Log Sampling (Future Enhancement)**
- For very high error rates (>1000 errors/sec), sample logs
- Keep 100% of unique error patterns, sample duplicates (e.g., keep 1 in 10)
- Reduces storage while preserving error diversity

**2. Structured Log Parsing (Future RFD)**
- Parse common log formats (JSON, logfmt, syslog)
- Extract structured fields automatically (no manual instrumentation)
- Example: Parse stack traces into structured format

**3. Log-Based Alerting (Future RFD)**
- Alert when error rate exceeds threshold
- Alert on new error patterns (not seen in last 7 days)
- Integration with RFD 074 for automatic LLM diagnosis

**4. Log Export (Future RFD)**
- Export logs to long-term storage (S3, GCS) for compliance
- Integration with external log platforms (Elasticsearch, Loki)
- OTLP forwarding to centralized collector

**5. Log Metrics Derivation (Future RFD)**
- Derive metrics from logs (error rate, latency distribution)
- Compare log-derived metrics with instrumentation metrics
- Detect discrepancies (e.g., errors logged but not in metrics)

## Appendix

### OTLP Log Format

**Example OTLP Log Record:**

```json
{
  "resourceLogs": [{
    "resource": {
      "attributes": [
        { "key": "service.name", "value": { "stringValue": "payment-svc" }}
      ]
    },
    "scopeLogs": [{
      "logRecords": [{
        "timeUnixNano": "1609459200000000000",
        "severityNumber": 17,  // ERROR
        "severityText": "ERROR",
        "body": { "stringValue": "Database connection timeout after 5 seconds" },
        "attributes": [
          { "key": "trace_id", "value": { "stringValue": "abc123def456..." }},
          { "key": "timeout_seconds", "value": { "intValue": 5 }},
          { "key": "pool_size", "value": { "intValue": 10 }}
        ],
        "traceId": "0af7651916cd43dd8448eb211c80319c",
        "spanId": "b7ad6b7169203331"
      }]
    }]
  }]
}
```

### DuckDB Query Examples

**Recent errors with attributes:**

```sql
SELECT
    timestamp,
    service_name,
    message,
    json_extract(attributes, '$.timeout_seconds') AS timeout,
    json_extract(attributes, '$.pool_size') AS pool_size
FROM error_logs
WHERE service_name = 'payment-svc'
  AND severity = 'ERROR'
  AND timestamp > NOW() - INTERVAL '1 hour'
ORDER BY timestamp DESC
LIMIT 100;
```

**Error rate time-series:**

```sql
SELECT
    date_trunc('minute', timestamp) AS minute,
    count(*) AS error_count
FROM error_logs
WHERE service_name = 'payment-svc'
  AND severity = 'ERROR'
  AND timestamp > NOW() - INTERVAL '24 hours'
GROUP BY minute
ORDER BY minute;
```

**Top error patterns:**

```sql
SELECT
    message_template,
    occurrence_count,
    last_seen,
    example_attributes
FROM error_log_patterns
WHERE service_id = (SELECT service_id FROM services WHERE service_name = 'payment-svc')
  AND severity = 'ERROR'
ORDER BY occurrence_count DESC
LIMIT 10;
```

**Trace-correlated logs:**

```sql
SELECT
    t.trace_id,
    t.duration_ms,
    t.status_code,
    l.timestamp AS log_timestamp,
    l.severity,
    l.message,
    l.attributes
FROM beyla_traces t
JOIN error_logs l ON t.trace_id = l.trace_id
WHERE t.trace_id = 'abc123def456'
ORDER BY l.timestamp;
```

---

## Dependencies

**Pre-requisites:**
- ✅ RFD 025 (OTLP Receiver) - Infrastructure for log ingestion
- ✅ RFD 036 (Distributed Tracing) - Trace correlation via trace_id
- ✅ RFD 067 (Unified Query Interface) - `coral_query_logs` tool definition
- ✅ RFD 074 (LLM-Driven RCA) - Integration point for error context

**Enables:**
- Complete observability stack (metrics + traces + logs)
- LLM-powered error diagnosis with full context
- Trace-correlated debugging workflows
