---
rfd: "082"
title: "Error Log Trace Correlation"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "036", "079" ]
database_migrations: [ "add_trace_context_to_error_logs" ]
areas: [ "agent", "colony", "observability", "logging", "tracing" ]
---

# RFD 082 - Error Log Trace Correlation

**Status:** 🚧 Draft

## Summary

Link error logs to distributed traces to provide complete request context during
debugging. This enables operators to see exactly what errors occurred during a
slow or failed trace, combining the "what happened" (traces) with "why it
failed" (error logs).

Key capabilities:

- **Trace Context Extraction**: Capture trace_id and span_id from OTLP log records
- **Schema Extension**: Add trace_id/span_id columns to error log tables
- **Correlation Queries**: Query logs by trace_id, query traces with error logs
- **CLI Integration**: `coral query logs --trace-id abc123`
- **Unified View**: See errors alongside trace spans

## Problem

### Current Gaps

With RFD 079 and RFD 036 implemented separately, Coral has:

- ✅ Distributed trace collection (RFD 036)
- ✅ Error log collection (RFD 079)
- ❌ **No link between traces and logs**

**Example Problem:**

```
User: "Why did trace abc123def456 fail?"

Current workflow:
1. coral query traces --trace-id abc123def456
   → Shows trace took 5.2s, returned HTTP 500
   → Shows spans: gateway (0.1s), payment-svc (5.0s)

2. No way to see what error occurred in payment-svc span

3. Must manually query logs:
   coral query logs --service payment-svc --since 10m
   → Returns 50 errors, operator must manually find the relevant one

Gap: No automatic correlation between trace and error logs
```

### Use Cases

**1. Trace-Correlated Error Lookup**

```
User: "Why did trace abc123 fail?"

Coral shows:
- Trace: abc123 (5.2s, HTTP 500)
- Span: payment-svc (4.8s, error)
- Error Log: "Database connection timeout after 5s (connection pool exhausted)"
  └─ Logged at: 14:32:15 (within span duration)
  └─ Attributes: {"pool_size": 10, "database": "postgres-primary"}
```

**2. Error-to-Trace Lookup**

```
User: "What traces failed with this error pattern?"

coral query patterns --service payment-svc --since 1h

Pattern: "Database connection timeout after ? seconds" (15 occurrences)

Show me traces:
- trace abc123 (5.2s, HTTP 500) - affected by this error
- trace def456 (4.8s, HTTP 500) - affected by this error
- trace ghi789 (6.1s, HTTP 500) - affected by this error
```

**3. Complete Request Context**

```
User investigating slow trace:

coral query traces --trace-id abc123 --show-logs

Trace Timeline:
00:00.000 → gateway span starts
00:00.100 → payment-svc span starts
00:00.150 → [WARN] Database pool at 90% capacity
00:05.050 → [ERROR] Database connection timeout after 5 seconds
00:05.100 → payment-svc span ends (5.0s)
00:05.200 → gateway span ends (5.2s)

Complete picture: Request failed due to DB timeout in payment-svc
```

## Solution

### Architecture Overview

Extend error log schema to capture trace context:

```
┌─────────────────────────────────────────────────────────────────┐
│ Application (Go/Python/Java/etc.)                               │
│                                                                 │
│  span, ctx := tracer.Start(ctx, "payment")                      │
│  defer span.End()                                               │
│                                                                 │
│  if err := processPayment(ctx); err != nil {                    │
│      logger.ErrorContext(ctx, "Payment failed", err)            │
│      // OTLP log includes ctx.TraceID() and ctx.SpanID()        │
│      span.RecordError(err)                                      │
│  }                                                              │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         │ OTLP (logs with trace context)
                         ↓
┌─────────────────────────────────────────────────────────────────┐
│ Agent: OTLP Receiver                                            │
│                                                                 │
│  ConsumeLogs():                                                 │
│    - Extract log.TraceID() (native OTLP field)                  │
│    - Extract log.SpanID()                                       │
│    - Fallback to attributes if native fields empty              │
│    - Store in error_logs_local with trace context               │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         │ Colony polls
                         ↓
┌─────────────────────────────────────────────────────────────────┐
│ Colony: Error Logs + Traces                                     │
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │ error_logs (with trace_id, span_id)                      │   │
│  │                                                          │   │
│  │  - log_id, timestamp, message                            │   │
│  │  - trace_id VARCHAR(32) ← NEW                            │   │
│  │  - span_id VARCHAR(16)  ← NEW                            │   │
│  │  - service_id, severity, attributes                      │   │
│  │                                                          │   │
│  │  INDEX: idx_error_logs_trace (trace_id)                  │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │ Query: Logs for Trace                                    │   │
│  │                                                          │   │
│  │  SELECT * FROM error_logs                                │   │
│  │  WHERE trace_id = 'abc123'                               │   │
│  │  ORDER BY timestamp                                      │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │ Query: Traces with Errors                                │   │
│  │                                                          │   │
│  │  SELECT t.*, l.message, l.severity                       │   │
│  │  FROM beyla_traces t                                     │   │
│  │  LEFT JOIN error_logs l ON t.trace_id = l.trace_id       │   │
│  │  WHERE t.service_name = 'payment-svc'                    │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

### Component Changes

#### 1. Agent Schema Update

**Extend `error_logs_local` Table:**

```sql
-- Migration: Add trace context columns
ALTER TABLE error_logs_local
ADD COLUMN trace_id VARCHAR(32),
ADD COLUMN span_id VARCHAR(16);

-- Add index for trace lookups
CREATE INDEX idx_error_logs_trace ON error_logs_local(trace_id, timestamp DESC);
```

#### 2. Colony Schema Update

**Extend `error_logs` Table:**

```sql
-- Migration: Add trace context columns
ALTER TABLE error_logs
ADD COLUMN trace_id VARCHAR(32),
ADD COLUMN span_id VARCHAR(16);

-- Add index for trace correlation
CREATE INDEX idx_error_logs_trace ON error_logs(trace_id, timestamp DESC);
```

#### 3. OTLP Receiver Enhancement

**Extract Trace Context (`internal/agent/otlp/receiver.go`):**

Update `ConsumeLogs` to extract trace context:

1. Check native OTLP fields: `logRecord.TraceID()` and `logRecord.SpanID()`
2. If empty, fallback to log attributes: `trace_id` and `span_id`
3. Store both fields in `error_logs_local`

**Priority:**
- Native OTLP fields (preferred - automatically populated by OpenTelemetry SDK)
- Attribute fallback (for legacy instrumentation)

#### 4. API Changes

**Extend `QueryErrorLogsRequest`:**

```protobuf
message QueryErrorLogsRequest {
    string service_id = 1;
    string severity = 2;
    google.protobuf.Timestamp since = 3;
    google.protobuf.Timestamp until = 4;
    int32 limit = 5;

    // NEW: Filter by trace
    string trace_id = 6;  // Optional: return only logs for this trace
}
```

**Extend `ErrorLog` message:**

```protobuf
message ErrorLog {
    string log_id = 1;
    google.protobuf.Timestamp timestamp = 2;
    string service_id = 3;
    string severity = 4;
    string message = 5;
    google.protobuf.Struct attributes = 6;

    // NEW: Trace context
    string trace_id = 7;
    string span_id = 8;
}
```

**New RPC: `QueryTraceWithLogs`:**

```protobuf
message QueryTraceWithLogsRequest {
    string trace_id = 1;  // Required
}

message QueryTraceWithLogsResponse {
    Trace trace = 1;                    // From RFD 036
    repeated Span spans = 2;            // From RFD 036
    repeated ErrorLog correlated_logs = 3;  // Logs with matching trace_id
}
```

#### 5. CLI Commands

**Extend `coral query logs`:**

```bash
coral query logs --trace-id abc123def456

# Show logs for specific trace
Logs for trace abc123def456:

[2025-01-10 14:32:10] WARN: Database connection pool at 90% capacity
  service: payment-svc
  span: payment-processing
  attributes: {"pool_size": 10, "active_connections": 9}

[2025-01-10 14:32:15] ERROR: Database connection timeout after 5 seconds
  service: payment-svc
  span: payment-processing
  attributes: {"pool_size": 10, "database": "postgres-primary"}
```

**Extend `coral query traces`:**

```bash
coral query traces --trace-id abc123def456 --show-logs

# Show trace with correlated error logs
Trace: abc123def456
  Duration: 5.2s
  Status: HTTP 500 (error)

Spans:
  gateway (0.2s)
  └─ payment-svc (5.0s) ← ERROR

Correlated Error Logs:
  [14:32:10] WARN: Database connection pool at 90% capacity
  [14:32:15] ERROR: Database connection timeout after 5 seconds
```

## Implementation Plan

### Phase 1: Schema Migration

**Goals:** Add trace context to error log tables

- [ ] Create migration: add `trace_id`, `span_id` columns to `error_logs_local`
- [ ] Create migration: add `trace_id`, `span_id` columns to `error_logs`
- [ ] Add indexes: `idx_error_logs_trace` on both tables
- [ ] Run migrations on test databases
- [ ] Verify backward compatibility (nullable columns)

**Deliverable:** Schema supports trace correlation

### Phase 2: Trace Context Extraction

**Goals:** Capture trace context from OTLP logs

- [ ] Update `ConsumeLogs` to extract `logRecord.TraceID()`
- [ ] Update `ConsumeLogs` to extract `logRecord.SpanID()`
- [ ] Add fallback to attributes (`trace_id`, `span_id`)
- [ ] Store trace context in `error_logs_local`
- [ ] Add unit tests for extraction logic

**Deliverable:** Agent captures trace context

### Phase 3: Colony Aggregation Update

**Goals:** Propagate trace context to colony

- [ ] Update `LogPoller` to pull `trace_id` and `span_id`
- [ ] Store trace context in colony `error_logs` table
- [ ] Verify data integrity (trace IDs match)
- [ ] Add integration test: agent → colony with trace context

**Deliverable:** Colony has trace context for all logs

### Phase 4: Query API Updates

**Goals:** Enable trace-based log queries

- [ ] Add `trace_id` parameter to `QueryErrorLogsRequest`
- [ ] Implement trace_id filtering in SQL queries
- [ ] Add `trace_id`/`span_id` to `ErrorLog` response message
- [ ] Implement `QueryTraceWithLogs` RPC
- [ ] Add query performance tests (trace_id index)

**Deliverable:** APIs support trace correlation queries

### Phase 5: CLI Integration

**Goals:** CLI commands for trace correlation

- [ ] Add `--trace-id` flag to `coral query logs`
- [ ] Add `--show-logs` flag to `coral query traces`
- [ ] Implement trace timeline view (spans + logs)
- [ ] Add formatting for correlated logs
- [ ] Add examples to help text

**Deliverable:** CLI shows trace-correlated logs

### Phase 6: Testing & Documentation

**Goals:** Validate end-to-end trace correlation

- [ ] E2E test: App logs with trace context → Agent → Colony → Query
- [ ] Integration test: Query logs by trace_id
- [ ] Integration test: Query traces with logs
- [ ] Performance test: Join query traces+logs
- [ ] Documentation: Trace correlation guide
- [ ] Runbook: Debugging failed traces with logs

**Deliverable:** Production-ready trace correlation

## Testing Strategy

### Unit Tests

**Trace Context Extraction:**

- Extract from native OTLP fields
- Fallback to attributes when native empty
- Handle missing trace context gracefully

### Integration Tests

**Trace Correlation:**

1. Simulate request with trace context
2. Log errors during request
3. Verify logs have matching trace_id
4. Query logs by trace_id
5. Verify correct logs returned

**Join Queries:**

1. Insert trace with trace_id
2. Insert error logs with same trace_id
3. Query trace with logs
4. Verify logs correlated correctly

### E2E Tests

**Full Workflow:**

1. Application makes traced request
2. Request encounters error
3. Error logged with trace context (via OTLP)
4. Agent captures log with trace_id
5. Colony aggregates with trace_id preserved
6. Operator queries by trace_id
7. Verify logs shown for trace

## Performance Considerations

**Index Strategy:**

- Index on `trace_id` critical for trace lookup queries
- Combined index `(trace_id, timestamp DESC)` for timeline queries
- Monitor index size growth

**Query Optimization:**

```sql
-- Efficient trace lookup (uses idx_error_logs_trace)
SELECT * FROM error_logs
WHERE trace_id = 'abc123'
ORDER BY timestamp;

-- Join with traces (requires indexes on both tables)
SELECT t.trace_id, t.duration_ms, l.message
FROM beyla_traces t
LEFT JOIN error_logs l ON t.trace_id = l.trace_id
WHERE t.service_id = ?
  AND t.timestamp > ?
LIMIT 100;
```

## Security Considerations

**Same as RFD 079:**

- Trace IDs may reveal request patterns
- RBAC controls on trace correlation queries
- Audit logging for cross-table queries

## Future Work

**Advanced Trace Features (Future RFDs):**

- Span-level log attachment (logs grouped by span_id)
- Distributed trace flame graphs with error annotations
- Trace sampling with guaranteed error capture
- Cross-service error propagation tracking

---

## Dependencies

**Pre-requisites:**

- ✅ RFD 036 (Distributed Tracing) - Trace collection infrastructure
- ✅ RFD 079 (Error Log Aggregation) - Error log storage and query APIs

**Enables:**

- Complete request debugging (traces + logs)
- Root cause analysis with full context
- Error pattern analysis per trace
- RFD 081 can query logs by trace_id via MCP (LLM integration)
