---
rfd: "083"
title: "RCA Context Metrics - Selective Custom OTLP Metrics Storage"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "025", "032" ]
database_migrations: [ "beyla_context_metrics table" ]
areas: [ "telemetry", "beyla", "rca" ]
---

# RFD 083 - RCA Context Metrics - Selective Custom OTLP Metrics Storage

**Status:** 🚧 Draft

## Summary

Enable selective storage of custom OTLP metrics that provide root cause context
(cache hit rates, connection pool metrics, circuit breaker state, queue depth,
etc.) to enhance LLM-assisted RCA. Unlike general-purpose APM, this focuses on
infrastructure and dependency metrics that explain *why* requests fail, not just
*that* they failed.

## Problem

**Current behavior:**

Coral's OTLP receiver accepts all metrics but Beyla's transformer only
recognizes three semantic conventions:

- `http.server.duration` → HTTP metrics
- `rpc.server.duration` → gRPC metrics
- `db.client.operation.duration` → SQL metrics

Any other OTLP metric (e.g., `cache.miss_rate`, `db.connection_pool.active`,
`circuit_breaker.state`) is:

1. ✅ Accepted by OTLP receiver (port 4317/4318)
2. ✅ Stored in 100-batch in-memory buffer
3. ✅ Polled by Beyla every 5 seconds
4. ❌ **Rejected by transformer** ("Skipping unknown metric")
5. 🗑️ **Discarded** (logged at DEBUG, never stored)

**Why this matters:**

During RCA, request-level telemetry (HTTP/gRPC/SQL) shows **symptoms** but
often lacks **causal data**:

```
SYMPTOM (Current Coral)          CAUSE (Missing)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
HTTP 500 errors            ←     external_api.rate_limited
SQL timeout                ←     connection_pool.exhausted
High latency              ←     cache.miss_rate spike (99%)
gRPC failures             ←     circuit_breaker.state = "open"
```

**LLM RCA effectiveness:**

An LLM querying Coral can see "SQL queries timing out" but cannot determine if
the root cause is:

- Connection pool saturation (`db.connection_pool.active = max`)
- Cache failure cascading load (`cache.miss_rate = 95%`)
- External API rate limiting (`stripe.rate_limit_remaining = 0`)
- Circuit breaker activation (`circuit_breaker.orders.state = "open"`)

**Use cases affected:**

1. **Dependency failures**: External API errors, rate limits
2. **Resource exhaustion**: Connection pools, memory, threads
3. **Cache incidents**: Miss rate spikes, eviction storms
4. **Async failures**: Queue backlog, message lag
5. **Deployment correlation**: Version changes, config reloads, feature flags

## Solution

**High-level approach:**

Store a **curated subset** of custom OTLP metrics that provide RCA context,
while rejecting business KPIs and low-value metrics. Use allow-list patterns to
prevent unbounded storage growth.

**Key Design Decisions:**

1. **Selective, not universal**: Only infrastructure/dependency metrics, not all
   custom metrics
2. **Separate storage**: `beyla_context_metrics` table, distinct from
   request-level metrics
3. **Allow-list configuration**: Explicit patterns (e.g., `cache.*`,
   `*.connection_pool.*`)
4. **Cardinality limits**: Reject metrics exceeding max unique label
   combinations
5. **RCA-optimized schema**: Simple time-series (timestamp, service, name,
   value, attributes)

**Benefits:**

- **Enhanced RCA**: LLMs can correlate symptoms with infrastructure state
- **Unified timeline**: All telemetry in one system, easier correlation
- **Focused scope**: Not a general APM, only RCA-relevant metrics
- **Resource-bounded**: Cardinality limits prevent storage explosion

**Architecture Overview:**

```
User App (OTel SDK)
    ↓
OTLP Metrics:
  - http.server.duration      (semantic convention)
  - cache.miss_rate           (custom - RCA context)
  - db.connection_pool.active (custom - RCA context)
  - revenue.total             (custom - business KPI, rejected)
    ↓
Shared OTLP Receiver (4317)
    ↓
In-Memory Buffer
    ↓
Beyla Polls (5s interval)
    ↓
┌─────────────────────────────────────────────────┐
│         Beyla Transformer (Enhanced)            │
│                                                 │
│  Semantic Convention Metrics:                   │
│  ✅ http.server.duration → beyla_http_metrics   │
│  ✅ rpc.server.duration  → beyla_grpc_metrics   │
│  ✅ db.client.operation  → beyla_sql_metrics    │
│                                                 │
│  RCA Context Metrics (NEW):                     │
│  ✅ cache.*              → beyla_context_metrics│
│  ✅ *.connection_pool.*  → beyla_context_metrics│
│  ✅ circuit_breaker.*    → beyla_context_metrics│
│  ✅ queue.*              → beyla_context_metrics│
│  ❌ revenue.total        → Rejected (not in     │
│                              allow-list)        │
└─────────────────────────────────────────────────┘
    ↓
DuckDB Storage
    ↓
QueryRCAContext RPC (NEW)
```

### Component Changes

1. **Beyla Transformer** (`internal/agent/beyla/transformer.go`):

    - Add `transformContextMetric()` for allow-listed patterns
    - Check metric name against configured patterns
    - Enforce cardinality limits (reject if > max unique labels)
    - Store in separate `ContextMetric` events

2. **Beyla Storage** (`internal/agent/beyla/storage.go`):

    - New table: `beyla_context_metrics`
    - Schema:
      `(timestamp, service, metric_name, metric_type, value, attributes)`
    - TTL: 24 hours (configurable, RCA window)
    - Indexes: `(service, metric_name, timestamp)` for fast queries

3. **Beyla Manager** (`internal/agent/beyla/manager.go`):

    - Load RCA context configuration (`rca_context.allow_patterns`)
    - Pass allow-list to transformer
    - Track cardinality per metric (reject if exceeded)

4. **Agent gRPC Handler** (`internal/agent/service_handler.go`):

    - New RPC: `QueryRCAContext`
    - Returns both traces AND context metrics for time range
    - Correlates by service name and timestamp

5. **Configuration** (`internal/config/schema.go`):
    - New section: `beyla.rca_context`
    - Fields: `enabled`, `allow_patterns`, `max_cardinality`,
      `retention_hours`

**Configuration Example:**

```yaml
beyla:
    enabled: true

    # Existing eBPF configuration
    discovery:
        open_ports: [ ]
        monitor_all: true

    # NEW: RCA context metrics
    rca_context:
        enabled: true

        # Only store metrics matching these patterns
        allow_patterns:
            - "cache.*" # cache.miss_rate, cache.hit_rate, cache.evictions
            - "*.connection_pool.*" # db.connection_pool.active, redis.connection_pool.idle
            - "circuit_breaker.*" # circuit_breaker.state, circuit_breaker.failure_count
            - "queue.*" # queue.depth, queue.lag, queue.processing_time
            - "*.rate_limit*" # stripe.rate_limit_remaining, github.rate_limit_reset
            - "external_api.*" # external_api.errors, external_api.latency
            - "deployment.*" # deployment.version, deployment.timestamp
            - "feature_flag.*" # feature_flag.checkout_v2.enabled
            - "runtime.memory.*" # runtime.memory.heap_used (language-specific)
            - "runtime.goroutines" # runtime.goroutines.count (Go)
            - "runtime.threads" # runtime.threads.count (JVM)

        # Prevent unbounded storage growth
        max_cardinality: 1000 # Max unique (service, metric_name, label_set) combinations
        reject_on_limit: true # Drop metrics exceeding limit (vs. evict oldest)

        # Storage retention
        retention_hours: 24 # Keep for 24h RCA window

        # Resource limits
        max_metrics_per_poll: 10000 # Prevent buffer overflow from single app
```

## Implementation Plan

### Phase 1: Database Schema & Configuration

- [ ] Add `beyla_context_metrics` table schema to `beyla/storage.go`
- [ ] Create migration: `CREATE TABLE beyla_context_metrics (...)`
- [ ] Add configuration struct: `RCAContextConfig` in `beyla/manager.go`
- [ ] Add config validation: pattern syntax, cardinality limits
- [ ] Load configuration in Beyla manager initialization

### Phase 2: Transformer Enhancement

- [ ] Add pattern matching function: `matchesAllowPattern(metricName, patterns)`
- [ ] Implement cardinality tracking: `map[string]int` for unique label sets
- [ ] Add `transformContextMetric()` method to transformer
- [ ] Extract metric type (Counter, Gauge, Histogram, Summary) from OTLP
- [ ] Convert OTLP attributes to JSON for storage
- [ ] Add transformer test: verify allow-list, cardinality enforcement

### Phase 3: Storage & Query

- [ ] Implement `StoreContextMetrics()` in Beyla storage
- [ ] Add indexes: `(service, metric_name, timestamp)`, `(timestamp)`
- [ ] Implement TTL cleanup for context metrics table
- [ ] Add cardinality query: count unique (service, metric_name, labels)
- [ ] Test storage: verify inserts, TTL, cardinality limits

### Phase 4: Query API

- [ ] Define protobuf: `QueryRCAContextRequest/Response`
- [ ] Implement `QueryRCAContext` RPC handler
- [ ] Return structure: `{ traces: [...], context_metrics: {...} }`
- [ ] Add filtering: by service, metric names, time range
- [ ] Test query API: verify data retrieval, time range filtering

### Phase 5: Testing & Documentation

- [ ] Add unit tests: pattern matching, cardinality tracking
- [ ] Add integration test: send custom metrics via OTLP, verify storage
- [ ] Add E2E test: simulate RCA scenario with context metrics
- [ ] Update `docs/OTLP_INGESTION.md` with RCA context metrics section
- [ ] Add example: instrumenting app with RCA context metrics
- [ ] Document troubleshooting: cardinality limits, rejected metrics

## API Changes

### New Protobuf Messages

```protobuf
// RCA context metric data point.
message ContextMetricDataPoint {
    // Timestamp (Unix milliseconds).
    int64 timestamp = 1;

    // Service name.
    string service_name = 2;

    // Metric name (e.g., "cache.miss_rate", "db.connection_pool.active").
    string metric_name = 3;

    // Metric type (counter, gauge, histogram, summary).
    string metric_type = 4;

    // Metric value (for counter/gauge).
    // For histograms: use histogram_buckets/counts.
    double value = 5;

    // Histogram buckets (for histogram metrics).
    repeated double histogram_buckets = 6;
    repeated uint64 histogram_counts = 7;

    // Metric attributes/labels (JSON encoded).
    // Example: {"cache_name": "user_sessions", "region": "us-west-2"}
    string attributes = 8;
}

// Query RCA context (traces + context metrics) for a service.
message QueryRCAContextRequest {
    // Service name filter.
    string service_name = 1;

    // Time range (Unix seconds).
    int64 start_time = 2;
    int64 end_time = 3;

    // Optional: filter by specific metric names.
    repeated string metric_names = 4;

    // Optional: include traces (default: true).
    bool include_traces = 5;

    // Optional: include context metrics (default: true).
    bool include_context_metrics = 6;
}

message QueryRCAContextResponse {
    // Filtered spans (if include_traces = true).
    repeated TelemetrySpan traces = 1;
    int32 total_traces = 2;

    // Context metrics grouped by metric name.
    // Key: metric_name, Value: list of data points.
    map<string, ContextMetricTimeSeries> context_metrics = 3;
    int32 total_context_metrics = 4;
}

// Time-series data for a single context metric.
message ContextMetricTimeSeries {
    string metric_name = 1;
    string metric_type = 2;
    repeated ContextMetricDataPoint data_points = 3;
}
```

### New RPC Endpoints

```protobuf
service AgentService {
    // Existing RPCs...
    rpc QueryTelemetry(QueryTelemetryRequest) returns (QueryTelemetryResponse);
    rpc QueryEbpfMetrics(QueryEbpfMetricsRequest) returns (QueryEbpfMetricsResponse);

    // NEW: Query RCA context (traces + context metrics).
    rpc QueryRCAContext(QueryRCAContextRequest) returns (QueryRCAContextResponse);
}
```

### CLI Commands

```bash
# Query RCA context for a service
coral agent query rca-context \
  --service payment-service \
  --start "2026-01-11T10:00:00Z" \
  --end "2026-01-11T11:00:00Z" \
  --metrics "cache.miss_rate,db.connection_pool.active"

# Example output:
RCA Context for payment-service (2026-01-11 10:00-11:00):

Traces: 1,234 spans
  - 156 errors (12.6%)
  - P99 latency: 2,345ms

Context Metrics:
  cache.miss_rate:
    10:00:00  2.1%
    10:05:00  5.3%
    10:10:00  94.7%  ← Spike!
    10:15:00  95.2%
    10:20:00  3.8%   ← Recovered

  db.connection_pool.active:
    10:00:00  45/100
    10:05:00  67/100
    10:10:00  100/100 ← Saturated!
    10:15:00  100/100
    10:20:00  52/100  ← Recovered

Correlation:
  ⚠️  Cache miss spike at 10:10:00 caused connection pool saturation
  ⚠️  Error rate increased from 2% → 85% during this window
```

### Configuration Changes

New configuration section: `beyla.rca_context`

- `enabled` (bool, default: false) - Enable RCA context metrics storage
- `allow_patterns` ([]string, required if enabled) - Metric name patterns to
  store
- `max_cardinality` (int, default: 1000) - Max unique metric label combinations
- `reject_on_limit` (bool, default: true) - Reject new metrics when limit
  reached
- `retention_hours` (int, default: 24) - How long to keep context metrics
- `max_metrics_per_poll` (int, default: 10000) - Max metrics processed per poll
  cycle

## Testing Strategy

### Unit Tests

**Pattern Matching**:

- Test allow-list matching: `cache.miss_rate` matches `cache.*`
- Test glob patterns: `db.connection_pool.active` matches `*.connection_pool.*`
- Test rejection: `revenue.total` does not match any pattern

**Cardinality Tracking**:

- Test cardinality calculation: same metric + different labels = multiple
  entries
- Test limit enforcement: reject when > `max_cardinality`
- Test cardinality reset: verify cleanup when metrics expire

**Metric Transformation**:

- Test Counter conversion: `cache.hits` → `{type: "counter", value: 12345}`
- Test Gauge conversion: `queue.depth` → `{type: "gauge", value: 523}`
- Test Histogram conversion: `cache.latency` → buckets/counts arrays

### Integration Tests

**End-to-End OTLP Ingestion**:

1. Configure agent with RCA context patterns
2. Send OTLP metrics via gRPC:
    - `http.server.duration` (semantic convention)
    - `cache.miss_rate` (RCA context)
    - `revenue.total` (should be rejected)
3. Query `QueryRCAContext` after 6 seconds (polling delay)
4. Verify:
    - HTTP metric in `beyla_http_metrics`
    - Cache metric in `beyla_context_metrics`
    - Revenue metric NOT stored
    - Query returns both HTTP traces + cache context metrics

**Cardinality Limit Test**:

1. Configure `max_cardinality: 10`
2. Send 15 unique `cache.operations` metrics (different cache names)
3. Verify:
    - First 10 accepted and stored
    - Last 5 rejected
    - Log message: "Exceeded cardinality limit"

**TTL Cleanup Test**:

1. Insert context metrics with `retention_hours: 1`
2. Wait 65 minutes
3. Query older metrics
4. Verify: metrics older than 1 hour are deleted

### E2E Tests

**Simulated RCA Scenario**:

Simulate a cache failure cascade:

1. App sends normal traffic: `cache.miss_rate = 2%`, `db.pool.active = 30`
2. Cache fails: `cache.miss_rate → 95%`
3. DB pool saturates: `db.pool.active → 100/100`
4. Errors spike: HTTP 500 errors increase
5. Query RCA context for incident window
6. Verify LLM receives correlated timeline:
    - Cache failure timestamp
    - Connection pool saturation timestamp
    - Error rate spike timestamp
7. Validate LLM can infer: "Cache failure → DB overload → Errors"

## Database Changes

### New Table: `beyla_context_metrics`

```sql
CREATE TABLE beyla_context_metrics
(
    timestamp        TIMESTAMP NOT NULL,
    service_name     VARCHAR   NOT NULL,
    metric_name      VARCHAR   NOT NULL,
    metric_type      VARCHAR   NOT NULL, -- 'counter', 'gauge', 'histogram', 'summary'
    value DOUBLE,                        -- For counter/gauge
    histogram_buckets DOUBLE[],          -- For histogram metrics
    histogram_counts BIGINT[],           -- For histogram metrics
    attributes       JSON,               -- Metric labels as JSON object
    PRIMARY KEY (timestamp, service_name, metric_name, attributes)
);

-- Index for fast time-range queries
CREATE INDEX idx_context_metrics_time
    ON beyla_context_metrics (timestamp);

-- Index for fast service + metric lookup
CREATE INDEX idx_context_metrics_service_metric
    ON beyla_context_metrics (service_name, metric_name, timestamp);

-- Index for cardinality queries (count unique label sets)
CREATE INDEX idx_context_metrics_cardinality
    ON beyla_context_metrics (service_name, metric_name, attributes);
```

### Migration Strategy

**Upgrade Path**:

1. Feature is opt-in (`rca_context.enabled: false` by default)
2. Table created on first agent start with RCA context enabled
3. No impact on existing Beyla tables (`beyla_http_metrics`, etc.)

**Rollback Path**:

1. Disable feature: `rca_context.enabled: false`
2. Table remains but no new writes
3. TTL cleanup will eventually remove all data
4. Optional: `DROP TABLE beyla_context_metrics` to reclaim space

## Security Considerations

**Metric Content Exposure**:

- Context metrics may contain sensitive values (e.g., `queue.messages = 12345`
  order IDs)
- **Mitigation**: Only store aggregated metrics (counts, rates), not individual
  events
- **Recommendation**: Users should avoid exporting PII in metric labels

**Cardinality as DoS Vector**:

- Malicious app could export metrics with unbounded label cardinality
- **Mitigation**: `max_cardinality` limit enforced, excess metrics rejected
- **Monitoring**: Log rejected metrics, alert on sustained rejections

**Storage Growth**:

- Unbounded allow-list could fill disk
- **Mitigation**: TTL cleanup (default 24h), `max_metrics_per_poll` limit
- **Recommendation**: Start with conservative allow-list, expand as needed

## Future Work

**Advanced Pattern Matching** (Future - RFD XXX)

- Regex support: `allow_patterns: ["cache\\.(hit|miss)_rate"]`
- Negative patterns: `deny_patterns: ["*.debug.*"]`
- Dynamic patterns: LLM suggests patterns based on RCA needs

**Metric Aggregation** (Future - RFD XXX)

- Pre-aggregate high-cardinality metrics (e.g., per-cache → total)
- Downsampling: store 1-second data for 1h, 1-minute averages for 24h
- Percentile calculation: P50/P95/P99 for gauge metrics

**Cross-Service Correlation** (Blocked by RFD XXX - Colony MCP)

- Query context metrics across multiple services
- Correlate: "payment-service cache failure → order-service errors"
- Dependency graph integration

**LLM-Guided Metric Discovery** (Future - RFD XXX)

- LLM analyzes RCA gaps: "I see SQL timeouts - do you have connection pool
  metrics?"
- Suggest allow-patterns based on observed incidents
- Auto-enable useful context metrics

**Metric Sampling** (Low Priority)

- Sample high-frequency metrics: store 1/10 data points
- Preserve spikes: always store values > 2× moving average
- Reduces storage while maintaining RCA value

## Appendix

### Example: Instrumenting an App for RCA

**Go Application with OpenTelemetry:**

```go
import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/metric"
)

// Create meter
meter := otel.Meter("payment-service")

// RCA Context Metrics (will be stored by Coral)
cacheMissRate, _ := meter.Float64ObservableGauge(
    "cache.miss_rate",
    metric.WithDescription("Cache miss rate percentage"),
)

connectionPoolActive, _ := meter.Int64ObservableGauge(
    "db.connection_pool.active",
    metric.WithDescription("Active database connections"),
)

circuitBreakerState, _ := meter.Int64ObservableGauge(
    "circuit_breaker.state",
    metric.WithDescription("Circuit breaker state (0=closed, 1=open)"),
)

// Register callbacks to report current values
meter.RegisterCallback(func (ctx context.Context, observer metric.Observer) error {
    observer.ObserveFloat64(cacheMissRate, cache.GetMissRate())
    observer.ObserveInt64(connectionPoolActive, db.ActiveConnections())
    observer.ObserveInt64(circuitBreakerState, breaker.IsOpen() ? 1: 0)
    return nil
}, cacheMissRate, connectionPoolActive, circuitBreakerState)
```

### Semantic Conventions for RCA Context Metrics

**Cache Metrics:**

- `cache.hit_rate` (Gauge, 0.0-1.0) - Cache hit ratio
- `cache.miss_rate` (Gauge, 0.0-1.0) - Cache miss ratio
- `cache.evictions` (Counter) - Number of cache evictions
- `cache.size` (Gauge) - Current cache size in bytes

**Connection Pool Metrics:**

- `{component}.connection_pool.active` (Gauge) - Active connections
- `{component}.connection_pool.idle` (Gauge) - Idle connections
- `{component}.connection_pool.waiting` (Gauge) - Requests waiting for
  connection
- `{component}.connection_pool.max` (Gauge) - Maximum pool size

**Circuit Breaker Metrics:**

- `circuit_breaker.{name}.state` (Gauge, 0=closed/1=open) - Circuit state
- `circuit_breaker.{name}.failure_count` (Counter) - Consecutive failures
- `circuit_breaker.{name}.success_count` (Counter) - Consecutive successes
- `circuit_breaker.{name}.timeout_count` (Counter) - Timeout events

**Queue Metrics:**

- `queue.{name}.depth` (Gauge) - Current queue depth
- `queue.{name}.lag` (Gauge) - Consumer lag (messages behind)
- `queue.{name}.processing_time` (Histogram) - Message processing duration

**External API Metrics:**

- `external_api.{service}.errors` (Counter) - API error count
- `external_api.{service}.latency` (Histogram) - API response time
- `external_api.{service}.rate_limit_remaining` (Gauge) - Remaining quota
- `external_api.{service}.rate_limit_reset` (Gauge) - Reset timestamp

### Reference: Current OTLP Metrics Behavior (RFD 025, 032)

**Before RFD 085:**

```
Custom Metric → OTLP Receiver → Buffer → Beyla Poll → Transformer
                                                           ↓
                                                    "Skipping unknown metric"
                                                           ↓
                                                        Discarded
```

**After RFD 085:**

```
Custom Metric → OTLP Receiver → Buffer → Beyla Poll → Transformer
                                                           ↓
                                          ┌────────────────┴────────────────┐
                                          │                                 │
                                 Allow-list match?                   Not in list?
                                          │                                 │
                                          ↓                                 ↓
                              beyla_context_metrics                    Discarded
                                    (stored)                         (logged DEBUG)
```
