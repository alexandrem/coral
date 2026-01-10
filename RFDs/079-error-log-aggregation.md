---
rfd: "079"
title: "Error Log Aggregation with Pattern Detection"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "025" ]
database_migrations: [ ]
areas: [ "agent", "colony", "observability", "logging" ]
---

# RFD 079 - Error Log Aggregation with Pattern Detection

**Status:** 🚧 Draft

## Summary

Implement targeted error log aggregation via OTLP to provide operators with
critical debugging context during incident response. Unlike traditional log
aggregation platforms (Elasticsearch, Loki), Coral focuses on **actionable error
logs only** with aggressive filtering, pattern detection, and short retention to
minimize storage overhead while maximizing diagnostic value.

Key capabilities:

- **Selective Ingestion**: Only ERROR and WARN level logs (filter at source)
- **OTLP-Based**: Leverage existing OTLP receiver (RFD 025), no new protocols
- **Pattern Detection**: Group similar errors to identify common failure modes
- **Storage Efficient**: De-duplication, pattern aggregation, short retention (7 days)
- **Query APIs**: Recent errors, error patterns, error rate trends
- **Rate Limiting**: Protect storage from error storms

This provides the foundation for error log observability. Trace correlation (RFD
082) and LLM integration (RFD 081) will be added in follow-up RFDs.

## Problem

### Current Gaps

**No Error Log Visibility:**

Coral can measure error rates via metrics, but **cannot** answer:

- ❌ "What error messages are applications logging?"
- ❌ "Why did this request fail?" (need exception stack trace)
- ❌ "What's the most common error in the last hour?"
- ❌ "Are errors increasing?" (need error log time-series)

**Example Problem:**

```
User: "Why is the payment service failing?"

Current approach:
1. Check metrics: Error rate is 5.2%
2. Missing: What error message/exception caused the failure?

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
2. **Pattern Aggregation**: Group identical errors to reduce storage
3. **Short Retention**: 7 days (recent debugging, not long-term audit)
4. **Query APIs**: Recent errors, error patterns, time-series trends
5. **Rate Limiting**: Protect storage from error storms

### Use Cases

**1. Recent Error Summary**

```
User: "What errors happened in payment-svc in the last hour?"

Coral shows:
- 23 errors in last hour
- Top 3 error patterns:
  1. "Database connection timeout after ? seconds" (15 occurrences)
  2. "Payment gateway returned ?" (5 occurrences)
  3. "Invalid card number format" (3 occurrences)
```

**2. Error Pattern Analysis**

```
coral query patterns --service payment-svc --since 24h

Top Error Patterns:
1. "Database connection timeout after ? seconds"
   - 145 occurrences
   - First seen: 2025-12-19 18:00:00
   - Last seen: 2025-12-20 14:32:15

2. "Payment gateway returned ?"
   - 34 occurrences
   - Last seen: 2025-12-20 14:31:48
```

**3. Individual Error Details**

```
coral query logs --service payment-svc --level error --since 5m --limit 10

Recent Errors:
[2025-12-20 14:32:15] ERROR: Database connection timeout after 5 seconds
  attributes: {"pool_size": 10, "database": "postgres-primary"}

[2025-12-20 14:31:48] ERROR: Payment gateway returned 503
  attributes: {"gateway": "stripe", "status_code": 503}
```

## Solution

### Architecture Overview

Extend Coral's OTLP receiver (RFD 025) to accept log records, filter
aggressively at ingestion, and store only ERROR/WARN logs in DuckDB with trace
correlation.

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
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │ LogPoller (similar to BeylaPoller, RFD 036 )             │   │
│  │                                                          │   │
│  │  EVERY 30 seconds:                                       │   │
│  │    FOR EACH agent:                                       │   │
│  │      QueryErrorLogs(since: last_poll_time)               │   │
│  │      INSERT INTO error_logs (with agent_id)              │   │
│  │      UPDATE pattern_aggregates                           │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │ DuckDB: error_logs                                       │   │
│  │                                                          │   │
│  │  Retention: 7 days                                       │   │
│  │  Indexed by: timestamp, service_id, message_hash,        │   │
│  │              severity                                    │   │
│  │                                                          │   │
│  │  Table: error_log_patterns (aggregated)                  │   │
│  │    - message_template (e.g., "DB timeout after ? sec")   │   │
│  │    - occurrence_count                                    │   │
│  │    - first_seen, last_seen                               │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │ Query APIs: QueryErrorLogs, QueryErrorPatterns           │   │
│  │                                                          │   │
│  │  Operators can query:                                    │   │
│  │    - Recent errors for service                           │   │
│  │    - Error patterns (grouped by message template)        │   │
│  │    - Time-series error rates                             │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

### Key Design Decisions

**1. Aggressive Filtering at Source**

Only ingest ERROR and WARN severity logs at the OTLP receiver level.

**Filter Logic:**

- **Keep**: ERROR (severity 17-20) and WARN (severity 13-16)
- **Discard**: INFO, DEBUG, TRACE (severity < 13)

Applied during `ConsumeLogs` processing before storage.

**Storage Reduction:**

- Typical application: 90-95% of logs are INFO/DEBUG
- Filtering reduces ingestion volume by 10-20x

**2. Pattern Aggregation for De-Duplication**

Group identical error messages to reduce storage:

```sql
-- Table: error_log_patterns
CREATE TABLE error_log_patterns
(
    pattern_id         UUID PRIMARY KEY,
    service_id         UUID        NOT NULL,
    message_template   TEXT        NOT NULL, -- "DB timeout after ? sec"
    message_hash       VARCHAR(64) NOT NULL, -- Hash of template
    severity           VARCHAR(10) NOT NULL,

    -- Aggregation
    occurrence_count   INTEGER     NOT NULL,
    first_seen         TIMESTAMPTZ NOT NULL,
    last_seen          TIMESTAMPTZ NOT NULL,

    -- Example parameters (JSON array)
    example_attributes JSON,                 -- [{"timeout_sec": 5}, {"timeout_sec": 10}]

    INDEX              idx_patterns_service (service_id, last_seen DESC),
    INDEX              idx_patterns_hash (message_hash, last_seen DESC)
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

**Pattern Extraction Algorithm:**

Coral uses a rule-based regex approach to normalize error messages into
templates. The algorithm applies transformations in order:

1. Replace numeric values with `?` (e.g., `timeout after 5 seconds` → `timeout after ? seconds`)
2. Replace UUIDs with `?` (standard UUID format)
3. Replace hex IDs with `?` (trace/span IDs, 16-32 chars)
4. Replace quoted strings with `?` (both single and double quotes)
5. Replace IP addresses with `?` (IPv4 format)
6. Replace timestamps with `?` (ISO8601 format)
7. Normalize whitespace (collapse multiple spaces, trim)

**Pattern Hash:** SHA256 hash of the template for deduplication.

**Example transformations:**

```
"Database timeout after 5 seconds"
→ "Database timeout after ? seconds"

"Error code 404 not found for user 'john@example.com'"
→ "Error code ? not found for user ?"

"Request abc123def456 failed with status 500"
→ "Request ? failed with status ?"

"Connection to 192.168.1.100:5432 refused"
→ "Connection to ?:? refused"
```

**Stack Trace Handling:**

Multi-line error messages (stack traces) use only the first line for pattern
extraction, appending `[stack trace]` marker. Full stack traces are preserved in
`attributes.stack_trace` for detailed inspection.

**3. Short Retention for Efficient Storage**

- **Agent:** 24 hours (recent errors for immediate debugging)
- **Colony:** 7 days (historical error patterns, trend analysis)

**Rationale:**

- Debugging focuses on recent errors (<24h)
- Historical compliance logs belong in dedicated log platforms (e.g., S3 + Athena)
- 7 days balances storage cost with utility

**4. Structured Attributes (JSON)**

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

**Query Examples:**

```sql
-- Find all connection timeout errors
SELECT * FROM error_logs
WHERE message LIKE '%connection timeout%'
  AND severity = 'ERROR'
  AND timestamp > NOW() - INTERVAL '1 hour';

-- Extract specific attribute
SELECT message,
       json_extract(attributes, '$.timeout_seconds') AS timeout,
       count(*) AS occurrences
FROM error_logs
WHERE service_id = (SELECT service_id FROM services WHERE service_name = 'payment-svc')
  AND severity = 'ERROR'
GROUP BY message, timeout
ORDER BY occurrences DESC;
```

**5. Log Sampling for High Error Rates**

During severe incidents, error rates can spike to thousands/sec, overwhelming
storage even with filtering. Coral implements adaptive sampling:

**Sampling Strategy:**

- **Below threshold** (200/sec): Keep 100% of error logs
- **Above threshold**: Progressive sampling - keep `threshold / current_rate`
  - 400/sec → Keep 50%
  - 2000/sec → Keep 10%

**Pattern Preservation:**

Sampling is applied **after** pattern extraction:

1. Extract pattern from log message
2. Check if pattern hash is new (never seen before)
3. If new pattern: **Always keep** (ensures all unique errors captured)
4. If known pattern: Apply sampling based on current error rate

This ensures:
- All unique error patterns are captured (first occurrence kept)
- Duplicate errors are sampled when rate is high
- Storage bounded even during severe incidents

**6. No Full-Text Search (Intentional Limitation)**

DuckDB is not optimized for full-text search:

**What Coral Supports:**

- ✅ Pattern matching: `message LIKE '%timeout%'`
- ✅ Attribute filtering: `json_extract(attributes, '$.error_code') = 'TIMEOUT'`
- ✅ Time-range queries: `timestamp > NOW() - INTERVAL '1h'`
- ✅ Service filtering: `service_id = 'uuid'`

**What Coral Does NOT Support:**

- ❌ Full-text search: "Find all logs mentioning user@example.com"
- ❌ Complex regex: "Find logs matching /[A-Z]{3}-\d{4}/"
- ❌ Fuzzy search: "Find logs similar to 'connection refused'"

**Recommendation:** For advanced log search, use dedicated platforms (Elasticsearch, Loki, CloudWatch Logs). Coral focuses on **error pattern detection for debugging**, not comprehensive log search.

## Component Changes

### 1. Agent (OTLP Receiver Extension)

**Extend `internal/agent/otlp/receiver.go`:**

Implement `ConsumeLogs` method (already defined in RFD 025, now add logic):

1. Iterate through OTLP log records
2. Extract service name from resource attributes
3. Filter by severity (discard if < WARN)
4. Apply rate limiting (max 1000 logs/min per service)
5. Extract log body and attributes
6. Store in local DuckDB via `LogStorage` interface

**New Storage Interface (`internal/agent/storage/log_storage.go`):**

Defines operations for local error log storage:

- `StoreErrorLog`: Insert error log into local DuckDB
- `QueryErrorLogs`: Query logs with filters (service, severity, time range)
- `CleanupOldLogs`: Delete logs older than retention period (24 hours)

**Error Log Data Model:**

- Timestamp, service name
- Severity (ERROR or WARN)
- Message text (full error message)
- Structured attributes (JSON)

### 2. Storage Schema

**Agent Table: `error_logs_local`**

```sql
CREATE TABLE error_logs_local
(
    log_id       UUID PRIMARY KEY,
    timestamp    TIMESTAMPTZ NOT NULL,
    service_name TEXT        NOT NULL,
    severity     VARCHAR(10) NOT NULL, -- 'ERROR' or 'WARN'
    message      TEXT        NOT NULL,
    attributes   JSON,                 -- Structured fields

    -- Indexed for fast queries
    INDEX        idx_error_logs_timestamp (timestamp DESC),
    INDEX        idx_error_logs_service (service_name, timestamp DESC)
);

-- Retention: 24 hours
-- Cleanup query: DELETE FROM error_logs_local WHERE timestamp < NOW() - INTERVAL '24 hours';
```

**Colony Table: `error_logs`**

```sql
CREATE TABLE error_logs
(
    log_id       UUID PRIMARY KEY,
    agent_id     UUID        NOT NULL, -- Which agent collected this log
    timestamp    TIMESTAMPTZ NOT NULL,
    service_id   UUID        NOT NULL,
    severity     VARCHAR(10) NOT NULL,
    message      TEXT        NOT NULL,
    message_hash VARCHAR(64) NOT NULL, -- Hash for pattern grouping
    attributes   JSON,

    INDEX        idx_error_logs_timestamp (timestamp DESC),
    INDEX        idx_error_logs_service (service_id, timestamp DESC),
    INDEX        idx_error_logs_hash (message_hash, timestamp DESC),

    FOREIGN KEY (service_id) REFERENCES services (service_id)
);

-- Retention: 7 days
-- Note: trace_id/span_id will be added in RFD 082 (Trace Correlation)
```

**Colony Table: `error_log_patterns`**

```sql
CREATE TABLE error_log_patterns
(
    pattern_id         UUID PRIMARY KEY,
    service_id         UUID        NOT NULL,
    message_template   TEXT        NOT NULL, -- "DB timeout after ? sec"
    message_hash       VARCHAR(64) NOT NULL,
    severity           VARCHAR(10) NOT NULL,

    -- Aggregation stats
    occurrence_count   INTEGER     NOT NULL DEFAULT 1,
    first_seen         TIMESTAMPTZ NOT NULL,
    last_seen          TIMESTAMPTZ NOT NULL,

    -- Sample attributes (JSON array, max 10 examples)
    example_attributes JSON,

    UNIQUE             (service_id, message_hash), -- Patterns scoped to service
    INDEX              idx_patterns_service (service_id, last_seen DESC),
    INDEX              idx_patterns_count (occurrence_count DESC),

    FOREIGN KEY (service_id) REFERENCES services (service_id)
);
```

**Storage Strategy:**

Coral uses a **dual-storage approach** to balance query flexibility with storage
efficiency:

1. **Individual Logs (`error_logs`)**: Stores recent raw logs (7 days) for:
   - Trace correlation (linking errors to specific requests)
   - Time-series queries (error rate over time)
   - Detailed attribute inspection

2. **Aggregated Patterns (`error_log_patterns`)**: Stores patterns indefinitely
   for:
   - Identifying most common errors
   - Tracking occurrence trends
   - Reducing storage for repeated errors

**Update Flow:**

When a new error log arrives at the colony:

1. **Extract pattern** from log message using pattern extraction algorithm
2. **Compute hash** of pattern for deduplication
3. **Store individual log** in `error_logs` table
4. **Upsert pattern** in `error_log_patterns`:
   - If pattern exists: increment `occurrence_count`, update `last_seen`, append to `example_attributes` (max 10)
   - If pattern new: insert new row with `occurrence_count = 1`

This ensures both tables stay synchronized while maintaining the pattern aggregation.

**Retention:**

- **error_logs**: 7 days (individual logs for recent debugging)
- **error_log_patterns**: No automatic deletion (patterns useful for long-term
  trends)
  - Manual cleanup option: Delete patterns not seen in last 90 days

### 3. Colony API Extensions

**New RPCs for Error Log Queries:**

```protobuf
// proto/coral/colony/v1/colony.proto

// Query individual error logs
message QueryErrorLogsRequest {
    string service_id = 1;  // Optional: filter by service
    string severity = 2;    // Optional: "ERROR" or "WARN" or "BOTH" (default)
    google.protobuf.Timestamp since = 3;
    google.protobuf.Timestamp until = 4;
    int32 limit = 5;  // Default: 100, max: 1000
}

message QueryErrorLogsResponse {
    repeated ErrorLog logs = 1;
    int32 total_count = 2;  // Total matching logs (may exceed limit)
    bool has_more = 3;      // True if total_count > limit
}

// Query aggregated error patterns
message QueryErrorPatternsRequest {
    string service_id = 1;  // Optional: filter by service
    string severity = 2;    // Optional: "ERROR" or "WARN" or "BOTH" (default)
    google.protobuf.Timestamp since = 3;  // Filter by last_seen timestamp
    int32 limit = 4;  // Default: 50, max: 100

    enum SortBy {
        OCCURRENCE_COUNT = 0;  // Most common patterns first
        LAST_SEEN = 1;         // Most recent patterns first
    }
    SortBy sort_by = 5;  // Default: OCCURRENCE_COUNT
}

message QueryErrorPatternsResponse {
    repeated ErrorLogPattern patterns = 1;
    int32 total_count = 2;
}

message ErrorLog {
    string log_id = 1;
    google.protobuf.Timestamp timestamp = 2;
    string service_id = 3;
    string severity = 4;
    string message = 5;
    google.protobuf.Struct attributes = 6;  // Structured attributes
}

message ErrorLogPattern {
    string pattern_id = 1;
    string service_id = 2;
    string message_template = 3;
    string severity = 4;
    int32 occurrence_count = 5;
    google.protobuf.Timestamp first_seen = 6;
    google.protobuf.Timestamp last_seen = 7;
    repeated google.protobuf.Struct example_attributes = 8;  // Max 10 examples
}
```

### 4. CLI Commands

**Query individual error logs:**

```bash
coral query logs [flags]

Flags:
  --service string      Service name or ID (required)
  --level string        Filter by level: error, warn, both (default: both)
  --since duration      Time range (default: 1h)
  --limit int           Max logs to return (default: 100)
  --format string       Output format: table, json, csv (default: table)
```

**Query error patterns:**

```bash
coral query patterns [flags]

Flags:
  --service string      Service name or ID (required)
  --level string        Filter by level: error, warn, both (default: both)
  --since duration      Filter patterns by last_seen (default: 7d)
  --limit int           Max patterns to return (default: 50)
  --sort-by string      Sort by: count, recent (default: count)
  --format string       Output format: table, json, csv (default: table)
```

**Examples:**

```bash
# Recent errors for payment-svc
$ coral query logs --service payment-svc --level error --since 1h

Recent Error Logs (payment-svc, last 1 hour):

[2025-12-20 14:32:15] ERROR: Database connection timeout after 5 seconds
  attributes: {"pool_size": 10, "database": "postgres-primary"}

[2025-12-20 14:31:48] ERROR: Payment gateway returned 503
  attributes: {"gateway": "stripe", "status_code": 503}

[2025-12-20 14:30:22] ERROR: Invalid card number format
  attributes: {"card_type": "visa", "validation_error": "invalid_luhn"}

Total: 23 errors in last hour

# View error patterns (grouped/aggregated)
$ coral query patterns --service payment-svc --since 24h

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
```

## Observability Metrics

Coral should expose metrics to monitor the error log aggregation system itself:

**Agent Metrics:**

```
# Log ingestion
coral_error_logs_received_total{service, severity}        # Counter: Total logs received
coral_error_logs_filtered_total{service, severity}        # Counter: Logs filtered (INFO/DEBUG)
coral_error_logs_stored_total{service, severity}          # Counter: Logs stored locally
coral_error_logs_dropped_total{service, reason}           # Counter: Logs dropped (rate limit, errors)

# Storage
coral_error_logs_storage_bytes                            # Gauge: DuckDB storage size
coral_error_logs_local_count{service}                     # Gauge: Number of logs in local DB

# Performance
coral_error_log_ingestion_duration_seconds{quantile}     # Histogram: Time to process log
```

**Colony Metrics:**

```
# Log aggregation
coral_error_logs_polled_total{agent}                      # Counter: Logs pulled from agents
coral_error_patterns_total{service}                       # Gauge: Number of unique patterns
coral_error_pattern_matches_total{service, pattern_id}    # Counter: Pattern match count

# Storage
coral_error_logs_storage_bytes                            # Gauge: Total storage size
coral_error_logs_count{service}                           # Gauge: Total logs stored
coral_error_logs_retention_deleted_total                  # Counter: Logs deleted by retention cleanup

# Query performance
coral_error_log_query_duration_seconds{operation}         # Histogram: Query latency
coral_error_log_query_total{operation, status}            # Counter: Query count
```

**Example Alerting Rules:**

```yaml
# High error log rate (potential attack or incident)
- alert: HighErrorLogRate
  expr: rate(coral_error_logs_stored_total[5m]) > 100
  annotations:
    summary: "High error log ingestion rate for {{ $labels.service }}"

# Rate limiting triggered (investigate)
- alert: ErrorLogsRateLimited
  expr: rate(coral_error_logs_dropped_total{reason="rate_limit"}[5m]) > 0
  annotations:
    summary: "Error logs being dropped due to rate limiting for {{ $labels.service }}"

# Storage growing too fast
- alert: ErrorLogStorageGrowth
  expr: delta(coral_error_logs_storage_bytes[1h]) > 1e9  # 1GB/hour
  annotations:
    summary: "Error log storage growing rapidly"
```

## Implementation Plan

### Phase 1: Agent Log Ingestion

**Goals:** Receive and store ERROR/WARN logs via OTLP

- [ ] Implement `ConsumeLogs` in OTLP receiver
- [ ] Add severity filtering (discard INFO/DEBUG at ingestion)
- [ ] Add rate limiting (1000 logs/min per service)
- [ ] Add adaptive sampling (for high error rates >200/sec)
- [ ] Implement `LogStorage` interface with DuckDB backend
- [ ] Create `error_logs_local` table and indexes
- [ ] Add log cleanup loop (24-hour retention)
- [ ] Add ingestion metrics (received, filtered, stored, dropped, sampled)

**Deliverable:** Agents ingest and store error logs with rate limiting and sampling

### Phase 2: Pattern Extraction (Critical for Storage Efficiency)

**Goals:** Implement pattern aggregation to limit storage growth

- [ ] Implement `ExtractMessagePattern` function with regex rules
- [ ] Add stack trace detection and handling
- [ ] Compute message hash for deduplication
- [ ] Add unit tests for pattern extraction edge cases
- [ ] Validate pattern extraction reduces cardinality (target: 100-500 patterns)

**Deliverable:** Pattern extraction works correctly and reduces storage

### Phase 3: Colony Log Aggregation

**Goals:** Pull logs from agents, store in colony with patterns

- [ ] Create `error_logs` and `error_log_patterns` tables in colony
- [ ] Implement `LogPoller` (similar to `BeylaPoller` from RFD 036)
- [ ] Add `QueryErrorLogs` RPC to agent
- [ ] Implement dual-storage: raw logs + pattern aggregation
- [ ] Add pattern upsert logic (increment occurrence_count)
- [ ] Add retention cleanup (7-day retention for logs)
- [ ] Add storage metrics (bytes, count, patterns)

**Deliverable:** Colony aggregates error logs with pattern deduplication

### Phase 4: Query APIs

**Goals:** Query error logs and patterns via RPC and CLI

- [ ] Implement `QueryErrorLogs` RPC in colony
- [ ] Implement `QueryErrorPatterns` RPC in colony
- [ ] Add filtering: service, severity, time range
- [ ] Add sorting for patterns (by count or recency)
- [ ] Add `coral query logs` CLI command
- [ ] Add `coral query patterns` CLI command
- [ ] Add text-based table rendering for logs and patterns
- [ ] Add query performance metrics

**Deliverable:** `coral query logs` and `coral query patterns` work

### Phase 5: Testing & Documentation

**Goals:** Validate end-to-end functionality

- [ ] Unit tests: OTLP log ingestion, filtering, storage
- [ ] Integration tests: Agent → Colony log aggregation
- [ ] E2E test: Application logs → OTLP → Agent → Colony → Query
- [ ] Performance test: 10,000 logs/sec ingestion (verify filtering reduces
  load)
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

- **Log Rate:** 1,000 req/s \* 10 logs/req = 10,000 logs/sec
- **Daily Volume:** 10,000 \* 86,400 = 864 million logs/day
- **Storage:** 864M \* 500 bytes = 432 GB/day
- **Retention (7 days):** 432 GB \* 7 = 3 TB

**Not feasible for DuckDB.**

### With ERROR/WARN Filtering Only

- **Error Rate:** 2% of requests fail
- **Logs per error:** 2 logs (WARN + ERROR)
- **Error Log Rate:** 20 errors/sec \* 2 logs = 40 logs/sec
- **Daily Volume:** 40 \* 86,400 = 3.46 million logs/day
- **Storage:** 3.46M \* 500 bytes = 1.73 GB/day
- **Retention (7 days):** 1.73 GB \* 7 = 12 GB

**Reduction:** 250x (from 3 TB to 12 GB)

### With Pattern Aggregation

**Conservative Estimates:**

- **Unique Error Patterns:** ~500 (realistic for production systems)
  - Stack traces with varying line numbers create unique patterns
  - Errors with embedded IDs/timestamps increase cardinality
  - Multiple error types across different code paths
- **Pattern Table:** 500 rows \* 1 KB = 500 KB
- **Raw Logs:** Keep last 7 days (12 GB)
- **Total Storage:** 500 KB + 12 GB ≈ **12 GB**

**Reduction:** 250x (from 3 TB to 12 GB)

**Optimistic Estimates (with aggressive pattern extraction):**

- **Unique Error Patterns:** ~100 (if pattern extraction works well)
- **Pattern Table:** 100 rows \* 1 KB = 100 KB
- **Raw Logs:** 7 days (12 GB)
- **Total Storage:** ~12 GB

**Reduction:** Still 250x (pattern table is negligible compared to raw logs)

**Key Insight:**

Pattern aggregation provides storage savings primarily by avoiding duplicate
storage in queries and summaries, but raw logs are still needed for:
- Time-series analysis (error rate over time)
- Detailed debugging with full attributes
- Individual error investigation

The 7-day retention on raw logs is the primary storage optimization, not
pattern aggregation. Trace correlation (linking errors to specific requests)
will be added in RFD 082.

## Security Considerations

**Log Data Sensitivity:**

- Error logs may contain PII (user IDs, email addresses, IP addresses)
- **Mitigation:** Application-side log scrubbing (use structured logging, avoid
  logging PII)
- **Mitigation:** RBAC controls on log queries (same as RFD 058)

**Attribute Exposure:**

- Structured attributes may reveal system internals (database hosts, API keys)
- **Mitigation:** Applications should avoid logging secrets in error messages
- **Mitigation:** Colony can implement attribute redaction (e.g., mask `api_key`
  fields)

**Storage Access:**

- DuckDB files contain plaintext logs
- **Mitigation:** Encrypt DuckDB files at rest (filesystem encryption)
- **Mitigation:** Restrict file permissions (0600, owner read/write only)

**Audit Logging:**

- Log all `QueryErrorLogs` and `QueryErrorPatterns` RPC calls
- **Mitigation:** Record requester identity, service queried, time range

**Log Injection Attacks:**

- Malicious error messages containing shell commands, SQL, XSS payloads
- Example: `logger.error(userInput)` where `userInput = "; DROP TABLE users;"`
- **Mitigation:** Logs are stored as-is but never executed; display tools should
  escape/sanitize for rendering
- **Mitigation:** Applications should sanitize user input before logging

**Rate Limiting (Storage DoS Protection):**

- Attacker triggers thousands of errors/sec to fill storage
- Example: Repeatedly hitting error endpoint to generate logs
- **Mitigation:** Agent-side rate limiting per service:
  - Max 1000 error logs/minute per service (configurable)
  - Excess logs counted in metrics but not stored
  - Alert when rate limit triggered (indicates issue or attack)
- **Mitigation:** Pattern aggregation naturally limits storage growth
- **Mitigation:** Colony enforces 7-day retention regardless of volume

**Rate Limiting Algorithm:**

Uses token bucket algorithm (Go's `rate.Limiter`) with per-service buckets:
- Rate: 16.67 logs/sec (1000/min)
- Burst: 100 logs
- Dropped logs increment `coral_error_logs_dropped_total{reason="rate_limit"}` metric

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
- Query patterns: target <500ms (aggregation)
- Query by service and time range: target <100ms

## Future Work

### Follow-up RFDs

**RFD 081: Error Log LLM Integration**

Deferred features for AI-powered diagnostics:
- MCP tools: `coral_query_logs`, `coral_query_error_patterns`
- Extend `coral_query_summary` with error context
- LLM diagnostic workflows and prompts
- Automatic root cause analysis from error patterns

**Dependencies:** RFD 079 (this RFD), RFD 067 (MCP Tools), RFD 074 (LLM-driven RCA)

**RFD 082: Error Log Trace Correlation**

Deferred features for linking logs to distributed traces:
- Add `trace_id` and `span_id` columns to error_logs schema
- Extract trace context from OTLP log records
- Query logs by trace_id (e.g., `coral query logs --trace-id abc123`)
- Extend trace queries to show correlated error logs
- Join queries across traces and logs tables

**Dependencies:** RFD 079 (this RFD), RFD 036 (Distributed Tracing)

### Advanced Log Features (Future RFDs)

**1. Structured Log Parsing (Future RFD)**

- Parse common log formats (JSON, logfmt, syslog)
- Extract structured fields automatically (no manual instrumentation)
- Example: Parse stack traces into structured format

**2. Log-Based Alerting (Future RFD)**

- Alert when error rate exceeds threshold
- Alert on new error patterns (not seen in last 7 days)
- Integration with RFD 074 for automatic LLM diagnosis

**3. Log Export (Future RFD)**

- Export logs to long-term storage (S3, GCS) for compliance
- Integration with external log platforms (Elasticsearch, Loki)
- OTLP forwarding to centralized collector

**4. Log Metrics Derivation (Future RFD)**

- Derive metrics from logs (error rate, latency distribution)
- Compare log-derived metrics with instrumentation metrics
- Detect discrepancies (e.g., errors logged but not in metrics)

## Appendix

### OTLP Log Format

**Example OTLP Log Record:**

```json
{
    "resourceLogs": [
        {
            "resource": {
                "attributes": [
                    {
                        "key": "service.name",
                        "value": {
                            "stringValue": "payment-svc"
                        }
                    }
                ]
            },
            "scopeLogs": [
                {
                    "logRecords": [
                        {
                            "timeUnixNano": "1609459200000000000",
                            "severityNumber": 17,
                            // ERROR
                            "severityText": "ERROR",
                            "body": {
                                "stringValue": "Database connection timeout after 5 seconds"
                            },
                            "attributes": [
                                {
                                    "key": "trace_id",
                                    "value": {
                                        "stringValue": "abc123def456..."
                                    }
                                },
                                {
                                    "key": "timeout_seconds",
                                    "value": {
                                        "intValue": 5
                                    }
                                },
                                {
                                    "key": "pool_size",
                                    "value": {
                                        "intValue": 10
                                    }
                                }
                            ],
                            "traceId": "0af7651916cd43dd8448eb211c80319c",
                            "spanId": "b7ad6b7169203331"
                        }
                    ]
                }
            ]
        }
    ]
}
```

### DuckDB Query Examples

**Recent errors with attributes:**

```sql
SELECT
    timestamp, service_name, message, json_extract(attributes, '$.timeout_seconds') AS timeout, json_extract(attributes, '$.pool_size') AS pool_size
FROM error_logs
WHERE service_name = 'payment-svc'
  AND severity = 'ERROR'
  AND timestamp
    > NOW() - INTERVAL '1 hour'
ORDER BY timestamp DESC
    LIMIT 100;
```

**Error rate time-series:**

```sql
SELECT date_trunc('minute', timestamp) AS minute,
    count(*) AS error_count
FROM error_logs
WHERE service_name = 'payment-svc'
  AND severity = 'ERROR'
  AND timestamp
    > NOW() - INTERVAL '24 hours'
GROUP BY minute
ORDER BY minute;
```

**Top error patterns:**

```sql
SELECT message_template,
       occurrence_count,
       last_seen,
       example_attributes
FROM error_log_patterns
WHERE service_id =
      (SELECT service_id FROM services WHERE service_name = 'payment-svc')
  AND severity = 'ERROR'
ORDER BY occurrence_count DESC LIMIT 10;
```

**Trace-correlated logs:**

```sql
SELECT t.trace_id,
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
