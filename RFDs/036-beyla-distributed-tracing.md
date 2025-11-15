---
rfd: "036"
title: "Beyla Distributed Tracing Collection"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: ["025", "032"]
database_migrations: ["beyla_traces"]
areas: ["observability", "tracing", "beyla"]
---

# RFD 036 - Beyla Distributed Tracing Collection

**Status:** 🚧 Draft

## Summary

Extend Beyla integration (RFD 032) to collect and store distributed traces from
instrumented applications, enabling end-to-end request flow visualization and
AI-driven root cause analysis. Traces are currently received via the OTLP
receiver (RFD 025) but stored generically without Beyla-specific querying,
retention policies, or integration with RED metrics.

## Problem

**Current behavior/limitations**:

- RFD 032 implemented RED metrics (Rate, Errors, Duration) for HTTP, gRPC, and
  SQL protocols, but deferred trace collection to keep scope manageable.
- Beyla emits distributed traces via OTLP containing span data with trace IDs,
  parent-child relationships, timing information, and protocol-specific
  attributes.
- These traces are received by the agent's OTLP receiver (RFD 025) and stored
  in the generic `telemetry_spans` table alongside manually instrumented traces.
- There is no Beyla-specific trace handling: no dedicated storage, no
  specialized queries, no retention policies tuned for eBPF-collected traces, no
  correlation with Beyla RED metrics.
- The `QueryBeylaMetrics` RPC returns only HTTP/gRPC/SQL metrics—traces cannot
  be queried via the Beyla-specific API.
- Colony's `BeylaPoller` does not collect or store traces, only RED metrics.

**Why this matters**:

- **Incomplete observability**: RED metrics show *that* a service is slow, but
  traces show *why* by revealing the request path, external dependencies,
  database queries, and time spent in each component.
- **AI diagnosis requires context**: When users ask "Why is payments-api slow?",
  the AI needs both metrics (P95 latency is 450ms) and traces (80% of time spent
  in card-validator-svc) for accurate root cause analysis.
- **Cross-service correlation**: Distributed traces link requests across
  microservices, showing cascading failures, retry storms, and dependency
  bottlenecks that metrics alone cannot reveal.
- **eBPF advantage wasted**: Beyla captures traces without SDK integration or
  code changes, but without proper storage and querying, this data is
  underutilized.

**Use cases affected**:

- **Latency investigation**: "Why did this specific checkout request take 5
  seconds?" requires trace-level detail showing each hop in the request flow.
- **Dependency mapping**: Understanding service relationships requires analyzing
  traces to see which services call which other services.
- **Error propagation**: When a payment fails, traces show whether the error
  originated in payments-api, card-validator-svc, or an external payment gateway.
- **Performance profiling**: Identifying slow database queries, external API
  calls, or message queue operations within a request flow.

## Solution

Extend the Beyla integration to collect, store, and query distributed traces
using the existing infrastructure established by RFD 025 (OTLP receiver) and RFD
032 (Beyla process management and pull-based architecture).

**How Beyla traces work**:

- Beyla's eBPF instrumentation captures distributed trace spans automatically by
  intercepting HTTP/gRPC/SQL calls at the kernel level.
- Each span contains: trace ID, span ID, parent span ID, service name, operation
  name, start time, duration, status code, and protocol-specific attributes.
- Beyla propagates W3C Trace Context headers (HTTP) and gRPC metadata to
  maintain trace continuity across service boundaries.
- Spans are exported via OTLP (OpenTelemetry Protocol) to the agent's OTLP
  receiver.

**Coral integration approach**:

1. **Agent-side**: Transform OTLP traces from Beyla into Beyla-specific format
   and store in local DuckDB (`beyla_traces` table) with configurable retention.
2. **RPC extension**: Extend `QueryBeylaMetrics` RPC to include trace query
   capabilities (by trace ID, time range, service name).
3. **Colony-side**: Extend `BeylaPoller` to periodically query agents for new
   traces and store them in Colony DuckDB with longer retention (default: 7
   days).
4. **Correlation**: Enable queries that join traces with RED metrics (e.g.,
   "Show me traces for HTTP requests with P95 latency >500ms").

### Key Design Decisions

- **Leverage existing OTLP receiver**: Beyla traces are already received via the
  RFD 025 OTLP receiver infrastructure. This RFD focuses on Beyla-specific
  storage, querying, and retention—not on building a new trace ingestion
  pipeline.
- **Separate storage from generic telemetry**: While the OTLP receiver stores
  all traces in `telemetry_spans`, Beyla traces are *also* stored in
  `beyla_traces` with Beyla-specific indexing, retention, and query patterns.
  This enables efficient Beyla-specific queries without mixing manually
  instrumented traces with auto-instrumented eBPF traces.
- **Pull-based architecture** (consistent with RFD 032): Colony queries agents
  for traces via `QueryBeylaMetrics` RPC rather than agents pushing traces. This
  maintains consistency with Beyla RED metrics collection and avoids central
  bottlenecks.
- **Shorter retention than metrics**: Traces are high-volume data (especially in
  high-throughput services), so default retention is 7 days (vs. 30 days for
  HTTP/gRPC metrics). Users can configure retention per deployment needs.
- **Sampling support**: Beyla supports trace sampling (e.g., sample 10% of
  requests). Agents store sampled traces, and Colony inherits the sampling
  decision—no additional sampling at storage time.
- **OpenTelemetry compatibility**: Store traces in OpenTelemetry-compatible
  format (trace ID, span ID, parent span ID) to enable future integrations with
  Jaeger, Tempo, or other trace backends.

### Benefits

- **Complete observability stack**: Coral now provides both RED metrics (what's
  slow) and distributed traces (why it's slow) from a single eBPF-based
  instrumentation layer.
- **Zero-code instrumentation**: Applications get distributed tracing without
  SDK integration, manual instrumentation, or code changes—Beyla handles
  everything via eBPF.
- **AI-powered analysis**: Coral AI can correlate metrics and traces to provide
  actionable insights ("Payments-api is slow because 80% of requests wait for
  card-validator-svc, which is experiencing database connection pool
  exhaustion").
- **Service dependency mapping**: Analyze traces to build service dependency
  graphs showing which services communicate and how often.
- **Production-ready tracing**: Leverage Beyla's battle-tested trace propagation
  (W3C Trace Context, gRPC metadata) used across the OpenTelemetry ecosystem.

### Architecture Overview

```
┌───────────────────────────────────────────────────────────────┐
│ Agent                                                         │
│                                                               │
│  ┌─────────────────┐         ┌──────────────────┐            │
│  │ Beyla Process   │         │ OTLP Receiver    │            │
│  │ (eBPF)          │         │ (RFD 025)        │            │
│  │                 │  OTLP   │                  │            │
│  │ • HTTP traces   ├────────>│ ConsumeTraces()  │            │
│  │ • gRPC traces   │         │                  │            │
│  │ • SQL traces    │         └────────┬─────────┘            │
│  └─────────────────┘                  │                      │
│                                       │                      │
│                          ┌────────────▼──────────┐           │
│                          │ Beyla Transformer     │           │
│                          │ • Extract trace spans │           │
│                          │ • Filter by source    │           │
│                          └────────────┬──────────┘           │
│                                       │                      │
│                          ┌────────────▼──────────┐           │
│                          │ BeylaStorage (DuckDB) │           │
│                          │ • beyla_traces table  │           │
│                          │ • 1 hour retention    │           │
│                          └────────────┬──────────┘           │
│                                       │                      │
│                                       │ QueryBeylaMetrics    │
│                                       │ RPC (extended)       │
└───────────────────────────────────────┼──────────────────────┘
                                        │
                                        │ gRPC/WireGuard mesh
                                        ▼
                          ┌──────────────────────────┐
                          │ Colony                   │
                          │                          │
                          │ ┌────────────────────┐   │
                          │ │ BeylaPoller        │   │
                          │ │ • Polls agents     │   │
                          │ │ • Queries traces   │   │
                          │ └────────┬───────────┘   │
                          │          │               │
                          │ ┌────────▼───────────┐   │
                          │ │ DuckDB             │   │
                          │ │ • beyla_traces     │   │
                          │ │ • 7 day retention  │   │
                          │ └────────────────────┘   │
                          │                          │
                          │ Future: MCP/CLI/AI query │
                          └──────────────────────────┘
```

### Component Changes

1. **Agent** (`internal/agent/beyla/`):
   - Identify Beyla-originated traces in OTLP receiver (via resource attributes
     or instrumentation scope)
   - Transform and store traces in `beyla_traces` table (separate from generic
     telemetry)
   - Extend `QueryBeylaMetrics` RPC handler to support trace queries (by trace
     ID, time range, service name)
   - Configurable retention for traces (default: 1 hour)

2. **Colony** (`internal/colony/beyla_poller.go`,
   `internal/colony/database/beyla.go`):
   - Extend `BeylaPoller` to query traces from agents via `QueryBeylaMetrics`
     RPC
   - Store traces in Colony DuckDB with configurable retention (default: 7 days)
   - Implement trace cleanup based on retention policy

3. **Protobuf API** (`proto/coral/agent/v1/agent.proto`):
   - Extend `QueryBeylaMetricsRequest` with trace-specific filters (trace ID,
     span ID, max spans)
   - Extend `QueryBeylaMetricsResponse` to include `repeated BeylaTraceSpan
     trace_spans`
   - `BeylaTraceSpan` message already defined (RFD 032)—no new messages needed

4. **Configuration**:
   - Agent: `beyla.storage_retention_hours_traces` (default: 1 hour)
   - Colony: `beyla.trace_retention_days` (default: 7 days)
   - Colony: `beyla.trace_sampling_rate` (optional, default: 1.0 = 100%)

**Configuration Example:**

```yaml
# agent-config.yaml
beyla:
  enabled: true

  # Local storage retention
  storage_retention_hours: 1          # RED metrics retention
  storage_retention_hours_traces: 1   # Trace retention (separate setting)

  # Sampling (applied by Beyla process)
  sampling:
    rate: 1.0                         # 100% sampling (adjust for high throughput)

  discovery:
    services:
      - name: "payments-api"
        open_port: 8080

  protocols:
    http:
      enabled: true
    grpc:
      enabled: true
    sql:
      enabled: true
```

```yaml
# colony-config.yaml (future enhancement, currently hardcoded)
storage:
  beyla:
    retention:
      http_metrics: 30d
      grpc_metrics: 30d
      sql_metrics: 14d
      traces: 7d                      # Shorter retention for high-volume traces

    # Optional: reduce storage by sampling traces at Colony level
    # (in addition to Beyla's sampling)
    trace_sampling_rate: 1.0          # Keep 100% of traces received from agents
```

## Implementation Plan

### Phase 1: Agent Storage and Querying

- [ ] Identify Beyla traces in OTLP receiver (check instrumentation scope or
  resource attributes)
- [ ] Store Beyla traces in `beyla_traces` table (schema already exists)
- [ ] Implement trace cleanup loop with configurable retention
- [ ] Add storage methods: `StoreTrace()`, `QueryTraces()`,
  `QueryTraceByID()`
- [ ] Extend `QueryBeylaMetrics` RPC handler to support trace queries

### Phase 2: Colony Collection and Storage

- [ ] Extend `QueryBeylaMetricsRequest` protobuf with trace filters
- [ ] Extend `QueryBeylaMetricsResponse` to include `trace_spans` field
- [ ] Update `BeylaPoller` to query traces from agents
- [ ] Implement Colony database methods: `InsertBeylaTraces()`,
  `CleanupOldBeylaTraces()`
- [ ] Add trace retention configuration to `BeylaPoller`

### Phase 3: Testing and Validation

- [ ] Unit tests for trace storage and querying
- [ ] Integration tests with multi-service trace propagation
- [ ] Verify trace sampling is respected throughout pipeline
- [ ] Test retention cleanup for both agent and colony
- [ ] Validate trace correlation with RED metrics

### Phase 4: CLI and Querying (Deferred to RFD 037 or later)

- [ ] CLI command: `coral query beyla traces --trace-id <id>`
- [ ] CLI command: `coral query beyla traces --service <name> --since <time>`
- [ ] Trace visualization (tree view, flamegraph)
- [ ] Join traces with metrics (e.g., "show traces for slow requests")

## API Changes

### Protobuf Extensions

Extend `QueryBeylaMetrics` RPC to support trace queries:

```protobuf
// In proto/coral/agent/v1/agent.proto

// QueryBeylaMetricsRequest (extend existing message)
message QueryBeylaMetricsRequest {
  // Existing fields for metrics
  int64 start_time = 1;
  int64 end_time = 2;
  repeated string service_names = 3;
  repeated BeylaMetricType metric_types = 4;

  // New fields for trace queries
  // Filter by specific trace ID (returns all spans in that trace)
  string trace_id = 5;

  // Limit number of traces returned (default: 100, max: 1000)
  int32 max_traces = 6;

  // Include traces in response (default: false for backward compatibility)
  bool include_traces = 7;
}

// QueryBeylaMetricsResponse (extend existing message)
message QueryBeylaMetricsResponse {
  // Existing fields
  repeated BeylaHttpMetric http_metrics = 1;
  repeated BeylaGrpcMetric grpc_metrics = 2;
  repeated BeylaSqlMetric sql_metrics = 3;
  int32 total_metrics = 4;

  // New field for traces
  repeated BeylaTraceSpan trace_spans = 5;
  int32 total_traces = 6;
}

// BeylaTraceSpan (already defined in RFD 032, shown for completeness)
message BeylaTraceSpan {
  string trace_id = 1;            // 32-char hex string
  string span_id = 2;             // 16-char hex string
  string parent_span_id = 3;      // Empty if root span

  string service_name = 4;
  string span_name = 5;           // e.g., "GET /api/v1/users/:id"
  string span_kind = 6;           // "server", "client", "producer", "consumer"

  google.protobuf.Timestamp start_time = 7;
  google.protobuf.Duration duration = 8;

  uint32 status_code = 9;         // HTTP/gRPC status
  map<string, string> attributes = 10;
}
```

### Database Schema

The `beyla_traces` table schema already exists (created in RFD 032):

```sql
-- Agent local storage (internal/agent/beyla/storage.go)
CREATE TABLE IF NOT EXISTS beyla_traces_local (
  trace_id VARCHAR(32) NOT NULL,
  span_id VARCHAR(16) NOT NULL,
  parent_span_id VARCHAR(16),
  service_name TEXT NOT NULL,
  span_name TEXT NOT NULL,
  span_kind VARCHAR(10),
  start_time TIMESTAMPTZ NOT NULL,
  duration_us BIGINT NOT NULL,
  status_code SMALLINT,
  attributes TEXT,                -- JSON-encoded attributes
  PRIMARY KEY (trace_id, span_id)
);

CREATE INDEX idx_beyla_traces_local_service_time
  ON beyla_traces_local(service_name, start_time DESC);
CREATE INDEX idx_beyla_traces_local_trace_id
  ON beyla_traces_local(trace_id, start_time DESC);
CREATE INDEX idx_beyla_traces_local_start_time
  ON beyla_traces_local(start_time DESC);

-- Colony aggregated storage (internal/colony/database/schema.go)
-- Same schema as agent, but with agent_id column
CREATE TABLE IF NOT EXISTS beyla_traces (
  trace_id VARCHAR(32) NOT NULL,
  span_id VARCHAR(16) NOT NULL,
  parent_span_id VARCHAR(16),
  agent_id VARCHAR NOT NULL,      -- Which agent collected this span
  service_name TEXT NOT NULL,
  span_name TEXT NOT NULL,
  span_kind VARCHAR(10),
  start_time TIMESTAMPTZ NOT NULL,
  duration_us BIGINT NOT NULL,
  status_code SMALLINT,
  attributes TEXT,
  PRIMARY KEY (trace_id, span_id)
);

CREATE INDEX idx_beyla_traces_service_time
  ON beyla_traces(service_name, start_time DESC);
CREATE INDEX idx_beyla_traces_trace_id
  ON beyla_traces(trace_id, start_time DESC);
CREATE INDEX idx_beyla_traces_duration
  ON beyla_traces(duration_us DESC);
```

### CLI Commands (Deferred to RFD 037)

Future CLI commands for trace querying:

```bash
# Query specific trace by ID
$ coral query beyla traces --trace-id abc123def456

Trace ID: abc123def456
Duration: 1.2s
Spans: 8
Services: frontend-api, payments-api, card-validator-svc, postgres

Span Tree:
frontend-api (1.2s, GET /checkout)
├─ payments-api (450ms, POST /api/v1/payments)
│  ├─ card-validator-svc (380ms, POST /validate)
│  │  └─ postgres (12ms, SELECT from cards)
│  └─ fraud-detector (35ms, gRPC Check)
├─ inventory-api (180ms, POST /api/v1/reserve)
│  └─ redis (2ms, SET order:lock:12345)
└─ email-svc (15ms, Kafka publish to notifications)

# Query recent traces for a service
$ coral query beyla traces --service payments-api --since 1h --limit 10

Recent Traces (last 1 hour, payments-api):

Trace ID         | Duration | Spans | Status | Root Operation
-----------------|----------|-------|--------|---------------------------
abc123def456     | 1.2s     | 8     | 200    | GET /checkout
def456abc789     | 850ms    | 6     | 200    | POST /api/v1/payments
789abc123def     | 5.2s     | 12    | 500    | GET /checkout (ERROR)
...

# Find slow traces (P95+)
$ coral query beyla traces --service payments-api --min-duration 500ms --limit 5

Slow Traces (duration ≥500ms, payments-api):

Trace ID         | Duration | Root Cause
-----------------|----------|------------------------------------------
789abc123def     | 5.2s     | card-validator-svc timeout (4.8s)
abc789def123     | 1.5s     | Database connection pool exhaustion
def123abc456     | 850ms    | Retry storm to fraud-detector (3 retries)
...

# Correlate traces with metrics
$ coral ask "Why is payments-api slow?"

🤖 Analyzing payments-api performance...

📊 RED Metrics (last 1 hour):
  - P95 latency: 450ms (baseline: 150ms, +200%)
  - Request rate: 120 req/s
  - Error rate: 2.3%

🔍 Trace Analysis:
  - Analyzed 5,000 traces from last hour
  - 80% of slow requests (>500ms) wait for card-validator-svc
  - card-validator-svc P95: 380ms (baseline: 50ms, +660%)
  - Database query time in card-validator-svc: 12ms (normal)

🎯 Root Cause:
  card-validator-svc is experiencing high latency (not database-related).
  Likely CPU saturation or external API timeout.

💡 Recommendation:
  1. Check card-validator-svc CPU and memory usage
  2. Review external payment gateway latency
  3. Consider adding request timeout and circuit breaker
```

## Testing Strategy

### Unit Tests

- Trace storage and retrieval in agent DuckDB
- Trace filtering by trace ID, service name, time range
- Trace cleanup based on retention policy
- Protobuf serialization/deserialization for `BeylaTraceSpan`

### Integration Tests

- Multi-service trace propagation (HTTP → gRPC → SQL)
- Verify W3C Trace Context headers are propagated
- Test agent → colony trace polling via `QueryBeylaMetrics` RPC
- Validate sampling is respected (Beyla samples 10% → agent stores 10% →
  colony receives 10%)

### E2E Tests

- Full trace collection pipeline: Beyla eBPF → OTLP receiver → agent storage
  → colony polling → DuckDB storage
- Verify traces correlate with RED metrics (same trace ID appears in both
  metrics and traces)
- Test retention cleanup (traces older than retention period are deleted)

## Security Considerations

- **Privacy**: Trace spans may contain sensitive data in attributes (e.g., user
  IDs, email addresses). Beyla is configured by default to not capture HTTP
  headers or request bodies. SQL queries are obfuscated (literals replaced with
  `?`).
- **Storage limits**: High-throughput services can generate millions of traces
  per day. Configurable retention (7 days default) and sampling (e.g., 10%)
  prevent storage exhaustion.
- **Access control**: Future enhancement: Add RBAC to control which users can
  query traces from which services.
- **Audit logging**: Colony logs all trace queries including requester identity,
  time range, and services queried.

## Migration Strategy

**No breaking changes**:

- Existing `QueryBeylaMetrics` RPC remains backward compatible. Traces are only
  included if `include_traces=true` is set in the request.
- Agents without trace support will return `trace_spans=[]` and
  `total_traces=0`.
- Colony gracefully handles mixed deployments (some agents with trace support,
  some without).

**Deployment steps**:

1. Deploy agent changes (trace storage and extended RPC handler)
2. Deploy colony changes (extended BeylaPoller and database methods)
3. Traces start flowing automatically—no configuration changes required
4. Optional: Tune retention and sampling based on storage capacity

## Future Enhancements

- **Trace sampling at colony level**: In addition to Beyla's sampling, allow
  colony to further sample traces for long-term storage (e.g., keep only 10% of
  traces received from agents).
- **Trace-based alerting**: Alert when trace duration exceeds threshold (e.g.,
  "Alert if any checkout trace >5s").
- **Service dependency mapping**: Analyze traces to build directed graph of
  service dependencies showing call frequency and latency.
- **Trace export to Jaeger/Tempo**: Export traces to external trace backends in
  OpenTelemetry format for visualization in industry-standard UIs.
- **Correlation with logs**: Join traces with log entries (matching trace ID)
  for unified debugging.
- **Trace flamegraphs**: Visualize trace spans as flamegraphs showing time
  distribution across services.
- **Anomaly detection**: Use ML to detect anomalous traces (e.g., unusual span
  patterns, unexpected service calls).

## Appendix

### Trace ID Propagation

Beyla automatically propagates trace context using industry standards:

**HTTP (W3C Trace Context)**:

```
GET /api/v1/payments HTTP/1.1
Host: payments-api.example.com
traceparent: 00-abc123def456789012345678901234-abcdef1234567890-01
tracestate: coral=region:us-west-2
```

**gRPC (gRPC Metadata)**:

```
grpc-trace-bin: <binary trace context>
```

**SQL**: No trace propagation (SQL queries are leaf spans).

### Example Trace Flow

```
Request: POST /checkout
├─ Span 1 (frontend-api): HTTP Server "POST /checkout" (1.2s)
│  └─ Span 2 (payments-api): HTTP Client → "POST /api/v1/payments" (450ms)
│     ├─ Span 3 (card-validator): HTTP Client → "POST /validate" (380ms)
│     │  └─ Span 4 (postgres): SQL "SELECT * FROM cards WHERE ..." (12ms)
│     └─ Span 5 (fraud-detector): gRPC Client → "Check" (35ms)
├─ Span 6 (inventory-api): HTTP Client → "POST /api/v1/reserve" (180ms)
│  └─ Span 7 (redis): Redis "SET order:lock:12345" (2ms)
└─ Span 8 (email-svc): Kafka Producer → "notifications" (15ms)

Total trace duration: 1.2s (frontend-api)
Critical path: frontend → payments → card-validator → postgres (1.2s)
Parallelized: inventory-api and email-svc run concurrently
```

### OpenTelemetry Compatibility

Coral's trace format is fully compatible with OpenTelemetry:

- **Trace ID**: 128-bit, encoded as 32-char hex string
- **Span ID**: 64-bit, encoded as 16-char hex string
- **Span kinds**: `INTERNAL`, `SERVER`, `CLIENT`, `PRODUCER`, `CONSUMER`
- **Attributes**: OpenTelemetry semantic conventions (e.g.,
  `http.method=POST`, `http.status_code=200`)

Future integration: Export Coral traces to Jaeger, Tempo, or Zipkin using OTLP
exporters.

---

## References

- **RFD 025**: Basic OpenTelemetry Ingestion (OTLP receiver infrastructure)
- **RFD 032**: Beyla Integration for RED Metrics Collection (foundation)
- **W3C Trace Context**: https://www.w3.org/TR/trace-context/
- **OpenTelemetry Tracing**: https://opentelemetry.io/docs/concepts/signals/traces/
- **Beyla Documentation**: https://github.com/grafana/beyla/tree/main/docs
