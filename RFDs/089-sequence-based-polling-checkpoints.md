---
rfd: "089"
title: "Sequence-Based Polling Checkpoints"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ ]
database_migrations: [ ]
areas: [ "colony", "agent", "polling", "data-ingestion" ]
---

# RFD 089 - Sequence-Based Polling Checkpoints

**Status:** 🎉 Implemented

## Summary

Replace time-based polling with a Kafka-style sequence ID checkpoint system to
eliminate data loss from clock skew, network delays, and polling failures. Each
data item gets a monotonically increasing sequence ID, and the colony tracks the
last successfully processed sequence ID per agent per data type.

## Problem

The colony currently polls agents at regular intervals (30-60s) using time-based
queries (`WHERE timestamp >= ? AND timestamp < ?`). This approach is fragile and
risks data loss:

**Current Limitations:**

- **Clock skew**: Different system clocks between colony and agents can cause
  data to be missed or duplicated
- **Network delays**: Polling delays can create gaps where data ages out before
  being fetched (agents only keep ~1hr of data)
- **No gap detection**: No mechanism to detect when data has been lost
- **Polling failures**: If a poll fails and the next poll uses a new time
  window, data in the failed window is lost forever
- **Time zone issues**: Timestamp handling across different time zones adds
  complexity

**Why This Matters:**

- **Data loss**: Critical telemetry, traces, and profiling data can be
  permanently lost
- **Debugging difficulty**: Incomplete data makes root cause analysis unreliable
- **No observability**: No way to know if data is being lost or how much

**Use Cases Affected:**

- **High-frequency telemetry**: 1000+ spans/sec can be lost during polling
  delays
- **Colony restarts**: Data generated during downtime can age out before colony
  catches up
- **Network partitions**: Temporary network issues can cause permanent data loss
- **Multi-colony environments**: Clock synchronization across colonies and
  agents is error-prone

## Solution

Implement a sequence ID-based checkpoint system similar to Kafka consumer
offsets:

**Key Design Decisions:**

1. **DuckDB sequences for ID generation**:
    - Leverage DuckDB's built-in `CREATE SEQUENCE` functionality (already used
      for `debug_events` and `profile_frame_dictionary`)
    - Monotonic, thread-safe, persists across restarts (persistent DB files)
    - Per-table sequences for independent tracking per data type

2. **Session ID for database identity**:
    - Each agent generates a unique `session_id` (UUID) when its database is
      created or recreated
    - The session_id is returned in every query response
    - Colony checkpoint key is `(agent_id, data_type, session_id)`
    - If the session_id changes (database wipe, disk loss, agent
      reinstallation), the colony resets its checkpoint to 0 for that agent
    - Prevents the colony from being "stuck" at a high seq_id that no longer
      exists after a database reset

3. **Checkpoint-commit pattern**:
    - Colony reads checkpoint (last processed seq_id) from database
    - Queries agent with `start_seq_id` parameter
    - Stores fetched data and updates checkpoint in a **single transaction**
      to ensure atomicity (prevents data duplication on retry if checkpoint
      update alone fails)
    - Only commits transaction AFTER successful storage (idempotent)
    - Follows same pattern as existing `CPUProfilePoller` in-memory tracking

4. **Gap detection and recovery with grace period**:
    - Detect non-consecutive sequence IDs during processing
    - **Visibility delay**: Only record a gap if the records following the
      missing seq_id are older than 10 seconds, to avoid false gaps from
      concurrent transactions (e.g., DuckDB assigns seq_id=5 to Tx A and
      seq_id=6 to Tx B, but B commits before A)
    - Store confirmed gaps in tracking table with recovery status
    - Attempt automatic recovery for small gaps
    - Alert on permanent data loss (data aged out before recovery)

5. **Backward compatible API changes**:
    - Add optional `start_seq_id` field to existing Query* RPCs
    - Keep time-based fields for backward compatibility during migration
    - Agent handlers support both query methods

**Benefits:**

- **Zero data loss**: Guaranteed ordering eliminates clock skew and time zone
  issues
- **Gap detection**: Identifies and recovers from missing data automatically
- **Efficient queries**: `WHERE seq_id > ?` is faster than timestamp range
  queries in DuckDB
- **Resilient**: Survives agent/colony restarts without data loss
- **Idempotent**: Failed polls don't update checkpoints, safe to retry
- **Observable**: Gap tracking provides visibility into data quality

**Architecture Overview:**

```
Agent Side:
  Data Generation → DuckDB (with seq_id) → RPC Handler
                    seq_id = nextval('seq_table')
                    session_id = DB UUID (stable per DB lifetime)

Colony Side:
  Poller → Read Checkpoint (agent_id, data_type, session_id)
        → Query Agent (start_seq_id)
        → Response includes (data, max_seq_id, session_id)
        → If session_id changed: reset checkpoint to 0
        → Store Data + Update Checkpoint (single transaction)
        → Gap Detection (with 10s grace period)
                          ↓
          Gap Recovery Job (background, every 5 min)
```

### Component Changes

1. **Agent Storage Components**:

    - Add `seq_id BIGINT DEFAULT nextval('seq_table')` column to all local data
      tables
    - Create DuckDB sequences for each table (e.g., `seq_otel_spans`,
      `seq_beyla_http_metrics`)
    - Generate and persist a `session_id` (UUID) per database instance, stored
      in a metadata table; regenerated only when the database is created fresh
    - DuckDB optimization: since data is inserted in seq_id order, DuckDB's
      natural row group ordering already provides efficient range scans; add
      an explicit index as a safety net for post-cleanup ordering
    - Update Query* methods to support sequence-based filtering

   Files: `internal/agent/telemetry/storage.go`,
   `internal/agent/beyla/storage.go`, `internal/agent/collector/storage.go`,
   `internal/agent/profiler/storage.go`,
   `internal/cli/agent/startup/storage.go` (session_id generation)

2. **Agent RPC Handlers**:

    - Update `QueryTelemetry`, `QueryEbpfMetrics`, `QuerySystemMetrics`,
      `QueryCPUProfileSamples`, `QueryMemoryProfileSamples` to accept
      `start_seq_id` parameter
    - Maintain backward compatibility with time-based queries
    - Return `max_seq_id` and `session_id` in response for checkpoint updates
    - Query logic: `WHERE seq_id > ? ORDER BY seq_id ASC LIMIT ?`

   Files: `internal/agent/service_handler.go`,
   `internal/agent/system_metrics_handler.go`,
   `internal/agent/debug_service.go`,
   `internal/agent/beyla/manager.go` (forwarding methods),
   `internal/agent/telemetry_receiver.go` (forwarding methods)

3. **Colony Database**:

    - New table: `polling_checkpoints` to track (agent_id, data_type,
      session_id, last_seq_id)
    - New table: `sequence_gaps` to track detected gaps and recovery status
    - Add checkpoint CRUD methods: `GetPollingCheckpoint`,
      `UpdatePollingCheckpoint`, `ResetPollingCheckpoint`
    - Add gap tracking methods: `RecordSequenceGap`, `MarkGapRecovered`,
      `MarkGapPermanent`
    - Session reset logic: if agent returns a different session_id, delete
      old checkpoint and create a new one starting at 0

   Files: `internal/colony/database/schema.go`,
   `internal/colony/database/checkpoints.go`

4. **Colony Pollers**:

    - Replace time-based queries with sequence-based queries
    - Remove in-memory checkpoint tracking (e.g., `lastPollTime` maps in
      BeylaPoller, `lastPollTimes` in CPUProfilePoller)
    - Use database checkpoints for all pollers
    - Update checkpoint only after successful data storage (checkpoint-commit
      pattern)
    - Add gap detection logic during response processing

   Files: `internal/colony/telemetry_poller.go`,
   `internal/colony/beyla_poller.go`,
   `internal/colony/system_metrics_poller.go`,
   `internal/colony/cpu_profile_poller.go`,
   `internal/colony/memory_profile_poller.go`

5. **Gap Recovery Service**:

    - Background goroutine running every 5 minutes
    - Queries `sequence_gaps` table for `detected` or `recovering` status
    - Attempts to re-query specific sequence ranges from agents
    - Marks gaps as `recovered` or `permanent` (if data aged out)
    - Max 3 retry attempts per gap

   Files: `internal/colony/gap_recovery.go` (new)

## Implementation Plan

### Phase 1: Database Schema Changes ✅

- [x] Create agent-side DuckDB sequences for all 8 data tables
- [x] Add `seq_id UBIGINT` columns to all agent local tables
- [x] Create `db_metadata` table with `session_id` UUID on agent side
- [x] Create indexes for efficient seq_id queries
- [x] Create colony-side `polling_checkpoints` table
- [x] Create colony-side `sequence_gaps` table
- [x] Add checkpoint CRUD methods to colony database package
  (`internal/colony/database/checkpoints.go`)

### Phase 2: gRPC API Updates ✅

- [x] Update proto definitions with optional `start_seq_id`, `max_records`
  fields in all Query RPCs (`agent.proto`, `debug.proto`)
- [x] Add `max_seq_id` and `session_id` to all response messages
- [x] Add `seq_id` field to all data messages (TelemetrySpan, SystemMetric,
  EbpfHttpMetric, EbpfGrpcMetric, EbpfSqlMetric, EbpfTraceSpan,
  CPUProfileSample, MemoryProfileSample)
- [x] Generate updated protobuf code
- [x] Add `SeqID` to internal domain structs (`telemetry.Span`,
  `collector.Metric`, `profiler.ProfileSample`, `profiler.MemoryProfileSample`)
- [x] Add `QueryBySeqID` methods to all agent storage layers:
  - `telemetry.Storage.QuerySpansBySeqID`
  - `collector.Storage.QueryMetricsBySeqID`
  - `beyla.BeylaStorage.QueryHTTPMetricsBySeqID`
  - `beyla.BeylaStorage.QueryGRPCMetricsBySeqID`
  - `beyla.BeylaStorage.QuerySQLMetricsBySeqID`
  - `beyla.BeylaStorage.QueryTracesBySeqID`
  - `profiler.Storage.QuerySamplesBySeqID`
  - `profiler.Storage.QueryMemorySamplesBySeqID`
- [x] Add forwarding methods to `beyla.Manager` and `agent.TelemetryReceiver`
- [x] Update all agent RPC handlers to support both time-based and seq-based
  queries with backward compatibility:
  - `ServiceHandler.QueryTelemetry`
  - `ServiceHandler.QueryEbpfMetrics`
  - `SystemMetricsHandler.QuerySystemMetrics`
  - `DebugService.QueryCPUProfileSamples`
  - `DebugService.QueryMemoryProfileSamples`
- [x] Wire `sessionID` from `StorageResult` through `ServiceRegistry` to all
  handlers via `SetSessionID()`

### Phase 3: Poller Implementation ✅

- [x] Update TelemetryPoller to use checkpoint-based polling
- [x] Update BeylaPoller to use 4 separate checkpoints (HTTP/gRPC/SQL/traces)
- [x] Update SystemMetricsPoller to use checkpoint-based polling
- [x] Update CPUProfilePoller to replace in-memory tracking with DB checkpoints
- [x] Update MemoryProfilePoller to replace in-memory tracking with DB
  checkpoints
- [x] Add gap detection logic to all pollers

### Phase 4: Gap Recovery ✅

- [x] Implement gap detection logic (done in Phase 3: `poller.DetectGaps()`)
- [x] Implement gap recovery background service
  (`internal/colony/gap_recovery.go`)
- [x] Add gap tracking database methods (`RecordSequenceGap`,
  `GetPendingSequenceGaps`, `MarkGapRecovered`, `MarkGapPermanent`,
  `IncrementGapRecoveryAttempt`, `CleanupOldSequenceGaps`)
- [x] Add structured logging for gap recovery lifecycle (recovery succeeded,
  failed/will retry, permanent data loss) — metrics deferred until OTLP
  infrastructure is set up
- [x] Add logging for permanent data loss (`PERMANENT DATA LOSS` error-level
  log when gap recovery exhausts all attempts)

### Phase 5: Testing & Rollout

- [x] Add unit tests for gap detection (`TestDetectGaps_*` in
  `internal/colony/poller/base_test.go`)
- [x] Add unit tests for checkpoint database operations
- [x] Add integration tests for E2E polling with checkpoints
- [x] Deprecate time-based queries

## API Changes

### Protobuf Messages

```protobuf
// Updated QueryTelemetryRequest
message QueryTelemetryRequest {
    // Existing fields (backward compatible)
    int64 start_time = 1;
    int64 end_time = 2;
    repeated string service_names = 3;

    // NEW: Sequence-based querying
    uint64 start_seq_id = 4;  // Query records with seq_id > start_seq_id
    int32 max_records = 5;    // Limit result size (default: 10000, max: 50000)
}

// Updated QueryTelemetryResponse
message QueryTelemetryResponse {
    repeated TelemetrySpan spans = 1;
    int32 total_spans = 2;

    // NEW: Checkpoint state for colony tracking
    uint64 max_seq_id = 3;    // Highest seq_id in this batch (0 if no data)
    string session_id = 4;    // Agent database session UUID; changes on DB recreation
}

// Updated TelemetrySpan
message TelemetrySpan {
    int64 timestamp = 1;
    string trace_id = 2;
    string span_id = 3;
    string service_name = 4;
    string span_kind = 5;
    double duration_ms = 6;
    bool is_error = 7;
    int32 http_status = 8;
    string http_method = 9;
    string http_route = 10;
    map<string, string> attributes = 11;

    // NEW: Sequence ID for checkpoint tracking
    uint64 seq_id = 12;
}

// Apply similar changes to:
// - QueryEbpfMetricsRequest/Response (add session_id, max_seq_id per metric type)
// - EbpfHttpMetric, EbpfGrpcMetric, EbpfSqlMetric, EbpfTraceSpan
// - QuerySystemMetricsRequest/Response
// - SystemMetric
// - QueryCPUProfileSamplesRequest/Response
// - QueryMemoryProfileSamplesRequest/Response
```

**Note on `uint64`**: Using unsigned integers avoids signed-integer headaches at
high scale and aligns with DuckDB's UBIGINT type. Protobuf `uint64` maps to
`uint64` in Go natively.

### Database Schema

```sql
-- Colony-side checkpoint tracking
CREATE TABLE IF NOT EXISTS polling_checkpoints (
    agent_id TEXT NOT NULL,
    data_type TEXT NOT NULL,
    session_id TEXT NOT NULL,      -- Agent DB session UUID; reset triggers checkpoint reset
    last_seq_id UBIGINT NOT NULL,
    last_poll_time TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (agent_id, data_type)
);

CREATE INDEX IF NOT EXISTS idx_polling_checkpoints_agent ON polling_checkpoints(agent_id);
CREATE INDEX IF NOT EXISTS idx_polling_checkpoints_updated ON polling_checkpoints(updated_at DESC);

-- Colony-side gap tracking
CREATE TABLE IF NOT EXISTS sequence_gaps (
    id INTEGER PRIMARY KEY,
    agent_id TEXT NOT NULL,
    data_type TEXT NOT NULL,
    start_seq_id UBIGINT NOT NULL,
    end_seq_id UBIGINT NOT NULL,
    detected_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    recovered_at TIMESTAMP,
    status TEXT NOT NULL DEFAULT 'detected',
    recovery_attempts INTEGER DEFAULT 0,
    last_recovery_attempt TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_sequence_gaps_agent ON sequence_gaps(agent_id, data_type);
CREATE INDEX IF NOT EXISTS idx_sequence_gaps_status ON sequence_gaps(status);

-- Agent-side session metadata (generated once per DB creation)
CREATE TABLE IF NOT EXISTS db_metadata (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Insert session_id on fresh DB creation only
INSERT OR IGNORE INTO db_metadata (key, value)
VALUES ('session_id', gen_random_uuid()::TEXT);

-- Agent-side sequences (example for telemetry)
CREATE SEQUENCE IF NOT EXISTS seq_otel_spans START 1;

-- Agent-side schema changes (example for telemetry)
CREATE TABLE otel_spans_local (
    seq_id UBIGINT DEFAULT nextval('seq_otel_spans'),
    -- ... existing columns ...
);

-- Index as safety net (DuckDB natural insertion order already optimizes seq_id scans)
CREATE INDEX IF NOT EXISTS idx_otel_spans_seq_id ON otel_spans_local(seq_id);
CREATE INDEX IF NOT EXISTS idx_otel_spans_service_seq ON otel_spans_local(service_name, seq_id);
```

### Query Examples

**Agent-side query (sequence-based):**

```sql
-- Query telemetry spans with seq_id > checkpoint
SELECT timestamp, trace_id, span_id, service_name, span_kind, duration_ms, is_error, http_status, http_method, http_route, CAST (attributes AS TEXT) as attributes, seq_id
FROM otel_spans_local
WHERE seq_id > ?
ORDER BY seq_id ASC
    LIMIT 10000;
```

**Colony-side checkpoint operations:**

```sql
-- Get checkpoint for agent/data_type (includes session_id for validation)
SELECT last_seq_id, session_id
FROM polling_checkpoints
WHERE agent_id = ?
  AND data_type = ?;

-- Update checkpoint after successful storage (single transaction with data insert)
INSERT INTO polling_checkpoints (agent_id, data_type, session_id, last_seq_id, updated_at)
VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT (agent_id, data_type)
DO UPDATE SET
    session_id = EXCLUDED.session_id,
    last_seq_id = EXCLUDED.last_seq_id,
    updated_at = CURRENT_TIMESTAMP;

-- Session reset: if agent's session_id differs from stored, reset checkpoint
-- (handled in application logic before querying)
```

## Testing Strategy

### Unit Tests

**Sequence ID Generation:**

- Test that sequence IDs are monotonically increasing
- Test that sequence survives database restart
- Test concurrent inserts generate unique IDs
- Test sequence behavior across DuckDB checkpoint/WAL

**Checkpoint Operations:**

- Test `GetPollingCheckpoint` with no checkpoint (returns 0)
- Test `UpdatePollingCheckpoint` creates and updates correctly
- Test concurrent checkpoint updates
- Test checkpoint persistence across restarts

**Gap Detection:**

- Test gap detection with missing seq IDs: [1,2,3,5,6] → gap at 4
- Test gap detection with continuous seq IDs: [1,2,3,4,5] → no gaps
- Test gap between checkpoint and first received ID
- Test gap detection across multiple batches
- Test false gap avoidance: concurrent transactions with out-of-order commits
  should not trigger gap detection within the 10s grace period
- Test session_id change triggers checkpoint reset to 0

### Integration Tests

**E2E Polling Flow:**

- Start agent with sample data
- Colony polls with checkpoint = 0
- Verify all data fetched and checkpoint updated to max_seq_id
- Add more data to agent
- Colony polls again with updated checkpoint
- Verify only new data fetched
- Verify checkpoint updated correctly

**Gap Recovery:**

- Insert data with gap: seq_ids [1,2,3,5,6,7] (missing 4)
- Colony polls and detects gap
- Verify gap recorded in `sequence_gaps` table
- Verify gap recovery attempted
- Verify gap marked as recovered or permanent

**Agent Restart:**

- Insert data with seq_ids 1-100
- Restart agent
- Insert more data
- Verify sequence continues from 101+ (not reset)
- Verify colony can query across restart boundary

**Colony Restart:**

- Poll data from agents
- Verify checkpoints stored
- Restart colony
- Poll again
- Verify polling resumes from stored checkpoints

### Load Tests

**High-Volume Polling:**

- 100 agents
- 1000 spans/sec per agent
- 60s poll interval
- Expected: 60K spans per poll, < 5s query time per agent

**Catch-Up Scenario:**

- Colony down for 30 minutes
- Agents accumulate 30 minutes of data (~30K+ spans per agent)
- Restart colony
- Measure catch-up time
- Expected: Multiple batched polls, full catch-up within 5 minutes

### Chaos Tests

**Clock Skew:**

- Set agent clock 1 hour ahead/behind colony
- Verify seq-based polling is unaffected (time-based would fail)

**Network Partition:**

- Simulate network failure during poll
- Verify checkpoint NOT updated
- Verify next poll retries from old checkpoint (idempotent)

**Polling Failure:**

- Force storage failure after query
- Verify checkpoint NOT updated
- Verify next poll re-queries same data

## Performance Considerations

### DuckDB Sequence Performance

**Sequence ID queries are highly efficient:**

- DuckDB's columnar storage keeps `seq_id` column contiguous
- `WHERE seq_id > ?` uses min/max statistics to skip entire row groups
- Single column scan faster than timestamp range queries
- Indexes on BIGINT smaller than TIMESTAMP indexes

**Comparison:**

```sql
-- Old: Time-based query (requires timestamp index scan + filter)
SELECT *
FROM otel_spans_local
WHERE timestamp >= ? AND timestamp < ?
ORDER BY timestamp DESC LIMIT 10000;

-- New: Sequence-based query (single column scan, more efficient)
SELECT *
FROM otel_spans_local
WHERE seq_id > ?
ORDER BY seq_id ASC LIMIT 10000;
```

### Batch Size Limits

| Data Type       | Max Records | Reasoning                                 |
|-----------------|-------------|-------------------------------------------|
| Telemetry       | 10,000      | High volume, prevents memory bloat        |
| Beyla HTTP/gRPC | 10,000      | Histogram buckets expand data size        |
| Beyla SQL       | 5,000       | Less frequent, larger payloads            |
| Beyla Traces    | 1,000       | Already limited in current implementation |
| System Metrics  | 10,000      | High frequency, small payloads            |
| CPU Profiles    | 5,000       | Large stack traces                        |
| Memory Profiles | 5,000       | Large stack traces                        |

**gRPC message size:** Configure `MaxCallRecvMsgSize` to handle these
batches. A 5,000-sample CPU profile with full stack traces can exceed the
default 4MB gRPC limit. Recommend setting to 16MB for profile pollers.

**Catch-up handling:**

- If backlog > max_records, multiple polls fetch data in batches
- Agent's 1hr retention ensures data doesn't age out during multi-poll catch-up

### Memory Implications

**Sequence ID overhead:**

- BIGINT = 8 bytes per row
- For 1M rows: 8MB additional storage
- Negligible compared to existing data columns

**Colony checkpoint table:**

- ~8 data types × ~100 agents = 800 rows max
- Each row: ~100 bytes
- Total: ~80KB (negligible)

## Migration Strategy

Ignore migration since system is still experimental.

## Monitoring & Observability

### Metrics

```go
// Checkpoint lag per agent/data_type
checkpoint_lag_seconds{agent_id, data_type}

// Sequence gaps detected
sequence_gaps_detected_total{agent_id, data_type}

// Sequence gaps recovered
sequence_gaps_recovered_total{agent_id, data_type}

// Sequence gaps permanent
sequence_gaps_permanent_total{agent_id, data_type}

// Poll query duration
poll_query_duration_seconds{poller_type, agent_id}

// Checkpoint update failures
checkpoint_update_failures_total{poller_type, agent_id}
```

### Alerting

**Critical:**

- `sequence_gaps_permanent_total > 0` - Permanent data loss detected
- `checkpoint_lag_seconds > 300` - Checkpoint lag > 5 minutes

**Warning:**

- `sequence_gaps_detected_total rate > 10/hour` - Frequent gaps
- `poll_query_duration_seconds > 5` - Slow queries

## Future Work

**Advanced Gap Recovery** (Future - RFD XXX)

- Prioritize gap recovery by data criticality
- Multi-agent gap correlation (detect widespread issues)
- Automatic backpressure on data generation during recovery

**Checkpoint Compaction** (Low Priority)

- Archive old checkpoints for audit trail
- Checkpoint history for debugging polling issues

## Appendix

### DuckDB Sequence Internals

DuckDB sequences are:

- **Persistent**: Stored in the database file, survive restarts. Note: this
  only applies to persistent DB files (e.g., `coral.db`), not in-memory
  databases. All Coral agent storage uses persistent files.
- **Thread-safe**: Concurrent `nextval()` calls guaranteed unique
- **Transactional**: Sequence values are committed with transaction
- **Efficient**: O(1) operation, no table scan
- **Non-rollback**: Sequence values are NOT rolled back on transaction abort,
  which can create "natural gaps" in the sequence. This is expected and handled
  by the grace period in gap detection.

**Example usage:**

```sql
CREATE SEQUENCE seq_test START 1;

-- Get next value
SELECT nextval('seq_test');
-- Returns 1

-- Use in INSERT
INSERT INTO test_table (id, data)
VALUES (nextval('seq_test'), 'value');

-- Check current value
SELECT currval('seq_test');
-- Returns current value (no increment)

-- Reset sequence
ALTER SEQUENCE seq_test RESTART WITH 100;
```

### Session ID and Database Reset Handling

If an agent's disk is wiped or the database is recreated while the `agent_id`
remains the same, the DuckDB sequence restarts from 1. Without session tracking,
the colony would be "stuck" at a high `last_seq_id` that no longer exists,
effectively blocking all future data ingestion for that agent.

**Solution:** The agent generates a UUID (`session_id`) stored in a `db_metadata`
table during database initialization. This UUID is returned in every query
response. The colony compares the returned `session_id` against the stored one:

- **Match**: Normal operation, query with `start_seq_id` from checkpoint.
- **Mismatch**: Database was recreated. Colony resets `last_seq_id` to 0 and
  stores the new `session_id`. First poll fetches all available data.

This is analogous to Kafka's `group.instance.id` combined with offset reset
policies.

### Checkpoint Atomicity

To prevent data duplication on retry, the colony must store fetched data and
update the checkpoint in a **single DuckDB transaction**. If the data insert
succeeds but the checkpoint update fails (e.g., due to a crash), the next poll
would re-fetch and re-insert the same data. DuckDB's single-writer model makes
this straightforward: both operations execute sequentially within the same
connection's transaction.

### Kafka Consumer Offset Comparison

This design is directly inspired by Kafka consumer offsets:

| Kafka                          | Coral                                 |
|--------------------------------|---------------------------------------|
| Topic partition offset         | Sequence ID per data type             |
| Consumer group offset commit   | Checkpoint update per agent           |
| Offset commit after processing | Checkpoint update after storage       |
| Lag monitoring                 | Checkpoint lag metrics                |
| Offset reset                   | Checkpoint reset method               |
| group.instance.id              | session_id (DB UUID)                  |
| auto.offset.reset              | Session mismatch → reset to 0         |

**Key difference**: Kafka offsets are sequential within a partition, while Coral
uses per-table DuckDB sequences. This allows independent evolution of different
data types without coordination.
