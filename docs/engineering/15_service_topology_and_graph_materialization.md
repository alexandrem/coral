# Service Topology and Graph Materialization

Coral derives a live service dependency graph from distributed trace data with
zero agent-side instrumentation. The graph is materialized on demand at the
Colony, exposed through the gRPC API, and injected into every LLM context
window automatically.

## The Core Problem: Topology Without Instrumentation

Traditional service maps require explicit dependency declarations, service-mesh
sidecars, or manual configuration. Coral takes a different approach: because
Beyla's eBPF interceptor already captures every HTTP and gRPC span — including
the W3C `traceparent` header it injects into outgoing calls — the distributed
trace data already encodes the full call graph. Every cross-service HTTP call
produces two linked spans in `beyla_traces`:

- A **CLIENT** span on the calling service side (Beyla injects `span_id` into
  the outgoing `traceparent`).
- A **SERVER** span on the receiving service side (Beyla records the incoming
  `traceparent`'s `parent_span_id`).

Both spans share the same `trace_id`, and the server span's `parent_span_id`
equals the client span's `span_id`. This parent-child linkage is the only input
`MaterializeConnections` needs to reconstruct the dependency edge.

### Why Beyla Owns the Traceparent

Beyla operates at the socket level via eBPF uprobes on `net/http` internals. It
injects and extracts `traceparent` headers **before** any application code runs.
This is critical: it means the topology derivation works even when applications
do **not** use an OTel SDK. The full propagation chain lives inside
`beyla_traces`; `otel_summaries` is irrelevant to topology.

## Schema Design

Two tables in the Colony DuckDB database are involved:

### `beyla_traces` — Source of Truth

```sql
CREATE TABLE IF NOT EXISTS beyla_traces (
    trace_id       VARCHAR(32)  NOT NULL,
    span_id        VARCHAR(16)  NOT NULL,
    parent_span_id VARCHAR(16),          -- NULL for root spans
    agent_id       VARCHAR      NOT NULL,
    service_name   TEXT         NOT NULL,
    span_name      TEXT         NOT NULL,
    span_kind      VARCHAR(10),
    start_time     TIMESTAMPTZ  NOT NULL,
    duration_us    BIGINT       NOT NULL,
    status_code    SMALLINT,
    attributes     TEXT,
    PRIMARY KEY (trace_id, span_id)
)
```

Indexes on `(service_name, start_time DESC)`, `(trace_id, start_time DESC)`,
`(agent_id, start_time DESC)`, and `(duration_us DESC)` support the
materialization join and time-range filtering.

### `service_connections` — Materialized Topology Cache

```sql
CREATE TABLE IF NOT EXISTS service_connections (
    from_service     TEXT      NOT NULL,
    to_service       TEXT      NOT NULL,
    protocol         TEXT      NOT NULL,
    first_observed   TIMESTAMP NOT NULL,
    last_observed    TIMESTAMP NOT NULL,
    connection_count INTEGER   NOT NULL,
    PRIMARY KEY (from_service, to_service, protocol)
)
```

This table is a **derived, cached view** of `beyla_traces`. It is never written
by agents; the Colony derives it entirely through `MaterializeConnections`.
Indexes on `from_service` and `to_service` make `GetTopology` efficient at read
time.

## The Materialization Join

`MaterializeConnections` in `internal/colony/database/connections.go` performs a
self-join on `beyla_traces`:

```sql
INSERT INTO service_connections
    (from_service, to_service, protocol, first_observed, last_observed, connection_count)
SELECT
    parent.service_name   AS from_service,
    child.service_name    AS to_service,
    'http'                AS protocol,
    MIN(child.start_time) AS first_observed,
    MAX(child.start_time) AS last_observed,
    COUNT(*)              AS connection_count
FROM beyla_traces child
JOIN beyla_traces parent
    ON  child.trace_id       = parent.trace_id
    AND child.parent_span_id = parent.span_id
    AND child.service_name  != parent.service_name
WHERE child.start_time >= ?
GROUP BY parent.service_name, child.service_name
ON CONFLICT (from_service, to_service, protocol) DO UPDATE SET
    last_observed    = EXCLUDED.last_observed,
    connection_count = EXCLUDED.connection_count
```

**Key mechanics:**

- **Self-join on trace identity.** `child.trace_id = parent.trace_id AND
  child.parent_span_id = parent.span_id` finds exactly the parent-child span
  pairs that represent an RPC hop.
- **Cross-service filter.** `child.service_name != parent.service_name`
  eliminates intra-service calls (e.g., an internal goroutine calling a helper).
  Same-service parent-child spans are invisible to the topology graph.
- **Time window.** `child.start_time >= ?` limits the join to the requested
  window (default: last hour). This prevents stale services from permanently
  cluttering the topology.
- **Aggregation.** `GROUP BY (parent.service_name, child.service_name)`
  collapses thousands of individual calls into a single edge with a
  `connection_count`. `MIN/MAX(child.start_time)` tracks when the edge was first
  and last active.
- **Upsert semantics.** `ON CONFLICT DO UPDATE` means repeated materializations
  are idempotent. `connection_count` is **replaced** (not incremented) on each
  upsert because the query re-counts from scratch against the full window. This
  avoids double-counting on cache refreshes.

### What the Join Does NOT Capture

- **Async calls** (message queues, pubsub) where no synchronous HTTP/gRPC hop
  produces a traceparent propagation. Kafka, SQS, and similar async patterns
  produce no parent-child span link in `beyla_traces`.
- **Calls from un-monitored services.** If an external client or a service on a
  non-Beyla agent initiates a call, only the server-side span is captured; no
  parent span exists, so no edge is derived.
- **Protocol attribution beyond HTTP.** The current query hard-codes `'http'` as
  the protocol. gRPC and SQL edges require additional protocol detection from
  span attributes (see Future Engineering Notes).

## Cache-Aware Fetch (`GetServiceConnections`)

Running the self-join on every `GetTopology` call would be expensive on large
trace volumes. `GetServiceConnections` wraps `MaterializeConnections` with a
TTL-based in-process cache using a mutex-guarded timestamp field on the
`Database` struct:

```
Database.connectionsMu               sync.Mutex
Database.connectionsLastMaterialized time.Time
Database.connectionsCacheTTL         time.Duration  (default: 30s)
```

On every call to `GetServiceConnections`:

1. Lock `connectionsMu`, read `connectionsLastMaterialized`.
2. If `time.Since(connectionsLastMaterialized) >= connectionsCacheTTL`:
   - Call `MaterializeConnections` with the requested `since` window.
   - On success, update `connectionsLastMaterialized = time.Now()`.
   - On failure, log a warning and serve stale data from `service_connections`.
3. Query `service_connections` and return all rows ordered by
   `connection_count DESC`.

**Failure-tolerant:** a failed re-materialization does not surface as an error
to the caller. The handler returns whatever is already in `service_connections`.
This ensures `GetTopology` degrades gracefully if the trace table is temporarily
unavailable.

**TTL granularity:** 30 seconds is a deliberate balance. Topology graphs are
stable over minutes; sub-second freshness would be wasteful. 30 seconds ensures
the LLM context window is never more than one cache cycle stale.

## Colony API Handler (`GetTopology`)

`GetTopology` in `internal/colony/server/server.go` composes the topology
response from two sources:

1. **Registry agents** — `registry.ListAll()` returns all agents that have
   checked in recently, converted to `colonyv1.Agent` proto messages with live
   status (ACTIVE / UNHEALTHY / DISCONNECTED via `registry.DetermineStatus`).
2. **Materialized connections** — `database.GetServiceConnections(ctx, since)`
   with a default 1-hour window.

The `Connection` proto fields map directly to the `service_connections` columns:

```
ServiceConnection.FromService → Connection.SourceId
ServiceConnection.ToService   → Connection.TargetId
ServiceConnection.Protocol    → Connection.ConnectionType
```

Connection failure is non-fatal: if `GetServiceConnections` returns an error,
the handler logs a warning and returns agents with an empty connections list.
This prevents a DuckDB issue from blocking the operator's view of mesh health.

## Exposure Surfaces

The topology graph reaches operators and LLMs through four surfaces:

### 1. CLI: `coral query topology`

`internal/cli/query/topology.go` calls `GetTopology` via Connect RPC and
renders connections as either an ASCII table (default) or JSON.

Text output example:
```
Service Topology (last 1h, 2 connection(s)):

FROM SERVICE    TO SERVICE   PROTOCOL
------------    ----------   --------
otel-app        cpu-app      HTTP
api-gateway     user-service HTTP
```

Column widths are computed dynamically from service name lengths to ensure
alignment regardless of name length.

### 2. MCP Tool: `coral_topology`

`handleTopology` in `internal/colony/mcp/tools_discovery.go` wraps
`GetServiceConnections` directly (bypassing the gRPC hop used by the CLI) and
formats the result as a prose call graph for LLM consumption:

```
Service call graph (last 1h):
otel-app → cpu-app (HTTP, 42 calls, last: 3s ago)
```

The tool accepts an optional `since` parameter (any Go duration string, e.g.
`"30m"`, `"2h"`). Age labels (`3s ago`, `5m ago`) are computed by
`formatConnectionAge`.

The tool description explicitly instructs the LLM to call this **before**
investigating cross-service issues.

### 3. Ask System Prompt: Compact Call Graph

`fetchTopologyContext` in `internal/cli/ask/agent.go` calls `coral_topology`
via the MCP server and passes the result through `formatCompactCallGraph`, which
collapses the multi-line output into a single comma-separated line:

```
Call graph: otel-app→cpu-app (HTTP), api-gateway→user-service (HTTP)
```

This is injected after the ALERTS section in `buildSystemPrompt`. All three
context fetches (service list, health alerts, topology) run concurrently via
goroutines writing into buffered channels, so the topology fetch adds no serial
latency to prompt construction. An empty string is injected (no line added) when
there are no observed connections.

### 4. gRPC API (`GetTopologyResponse`)

Exposes both agents and connections for programmatic consumers. The proto was
already generated prior to RFD 092; the handler simply implemented the
previously-stub endpoint.

## Data Flow Diagram

```
Beyla eBPF (agent)
  │  injects traceparent headers, captures both sides of each RPC hop
  ▼
beyla_traces (agent DuckDB, 1h retention)
  │  sequence-based polling (RFD 089)
  ▼
beyla_traces (colony DuckDB)
  │  parent-span self-join (MaterializeConnections, TTL 30s)
  ▼
service_connections (colony DuckDB)
  │  GetServiceConnections
  ▼
GetTopology RPC handler
  │
  ├─► coral query topology   (CLI, ASCII table or JSON)
  ├─► coral_topology MCP     (LLM on-demand, prose call graph)
  └─► coral ask system prompt (compact one-liner, parallel fetch)
```

## Testing Strategy

### Unit Tests (`internal/colony/database/connections_test.go`)

- **`TestMaterializeConnections_DetectsEdge`**: single cross-service span pair →
  one edge in `service_connections`.
- **`TestMaterializeConnections_SameServiceSpansExcluded`**: intra-service
  parent-child spans produce zero edges.
- **`TestMaterializeConnections_AggregatesMultipleCalls`**: three separate traces
  between the same pair collapse into one edge with `connection_count = 3`.
- **`TestMaterializeConnections_MultipleDistinctEdges`**: two distinct service
  pairs produce two independent edges.
- **`TestMaterializeConnections_SinceFiltersOldData`**: spans older than the
  window are excluded from edge derivation.
- **`TestGetServiceConnections_CacheHitSkipsMaterialization`**: a second call
  within the TTL window returns the same result even after new spans are inserted.
- **`TestGetServiceConnections_StaleCache`**: manually expiring
  `connectionsLastMaterialized` forces re-materialization and picks up new edges.

### E2E Tests (`tests/e2e/distributed/cli_query_test.go`)

`TestCLIQueryTopology` drives real cross-service traffic through `otel-app`'s
`/chain` endpoint, which calls `cpu-app` over plain HTTP with no SDK
instrumentation. Beyla captures both spans and links them via `traceparent`.
The test polls `coral query topology` until the `otel-app → cpu-app` edge
appears or a 320-second deadline is reached, continuously generating `/chain`
requests to ensure Beyla's eBPF uprobes have live traffic to intercept if the
target process restarted during the suite.

Both `otel-app` (port 8090) and `cpu-app` (port 8080) must be connected to the
agent via `ConnectService` before the test runs — this is handled in
`ensureServicesConnected`. Without this, Beyla does not attach uprobes to the
target processes and no spans appear in `beyla_traces`.

## Future Engineering Notes

**Protocol detection from span attributes.** The join currently hard-codes
`'http'` as the protocol. `beyla_traces.attributes` (stored as JSON) carries
span kind and protocol hints from Beyla. Parsing `span_kind = 'CLIENT'` plus
`db.system` attributes would allow the materialization to distinguish HTTP,
gRPC, and SQL edges automatically.

**Async topology.** Pub/sub and message-queue calls produce no synchronous
parent-child link. A future approach could use baggage-propagated correlation
IDs or a Beyla map-probe plugin to link producer and consumer spans.

**Historical topology diffing.** Storing topology snapshots by timestamp would
enable alerting when the call graph changes between deployments — a strong signal
for accidental dependency introductions.

**Cycle detection.** A recursive CTE over `service_connections` could detect
circular call chains (A→B→C→A) and surface them as a separate diagnostic
category.

**Incremental materialization.** The current full-window re-join is O(N) in the
trace table size. For large deployments, an incremental approach — joining only
spans inserted since the last materialization checkpoint — would keep the
operation O(new-spans) instead.

## Related Design Documents (RFDs)

- [**RFD 092**: Service Topology](../../RFDs/092-service-topology.md)
- [**RFD 089**: Sequence-Based Polling Checkpoints](../../RFDs/089-sequence-based-polling-checkpoints.md)
- [**RFD 084**: Dual-Source Service Discovery](../../RFDs/084-dual-source-service-discovery.md)
- [**RFD 036**: Beyla Distributed Tracing](../../RFDs/036-beyla-distributed-traces.md)
