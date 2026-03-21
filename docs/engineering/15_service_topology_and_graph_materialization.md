# Service Topology and Graph Materialization

Coral derives a live service dependency graph from two complementary observation
layers and exposes it through the gRPC API, CLI, and every LLM context window.

- **L7 layer** (RFD 092) — derived from distributed trace spans captured by
  Beyla's eBPF interceptor. High-fidelity edges with call counts and protocol
  attribution; limited to services that produce spans.
- **L4 layer** (RFD 033) — derived from raw TCP connections observed at the
  agent via `ss`/`netstat` (with an eBPF `tcp_v4_connect` probe planned). Covers
  every outbound TCP connection, including databases, external APIs, and legacy
  services that emit no traces at all.

The two layers are merged in `GetTopology`: where the same logical edge exists in
both, it is promoted to `EVIDENCE_LAYER_BOTH` and the L7 edge is returned (richer
metadata). L4-only edges are returned with `EVIDENCE_LAYER_L4_NETWORK`.

---

## L7 Layer: Trace-Derived Topology (RFD 092)

### The Core Problem: Topology Without Instrumentation

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

#### Why Beyla Owns the Traceparent

Beyla operates at the socket level via eBPF uprobes on `net/http` internals. It
injects and extracts `traceparent` headers **before** any application code runs.
This is critical: it means the topology derivation works even when applications
do **not** use an OTel SDK. The full propagation chain lives inside
`beyla_traces`; `otel_summaries` is irrelevant to topology.

### Schema: L7 Tables

#### `beyla_traces` — Source of Truth

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

#### `service_connections` — Materialized Topology Cache

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

### The Materialization Join

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
- **Time window.** `child.start_time >= ?` limits the join to the requested
  window (default: last hour).
- **Aggregation.** `GROUP BY (parent.service_name, child.service_name)` collapses
  thousands of calls into a single edge. `connection_count` is replaced (not
  incremented) on each upsert to avoid double-counting on cache refreshes.

#### What the L7 Join Does NOT Capture

- **Async calls** (message queues, pubsub) where no synchronous HTTP/gRPC hop
  produces a traceparent propagation.
- **Calls from un-monitored services.** If an external client initiates a call,
  only the server-side span is captured.
- **Non-instrumented TCP connections** — raw database clients, Redis, Memcached,
  or any service that does not emit HTTP/gRPC spans. These are covered by the L4
  layer.

### Cache-Aware Fetch (`GetServiceConnections`)

Running the self-join on every `GetTopology` call would be expensive on large
trace volumes. `GetServiceConnections` wraps `MaterializeConnections` with a
TTL-based in-process cache (mutex-guarded `connectionsLastMaterialized` field,
default TTL 30 s). A failed re-materialization is non-fatal: the handler returns
stale data from `service_connections` and logs a warning.

---

## L4 Layer: Network-Observed Topology (RFD 033)

### The Core Problem: Trace Blindness

`MaterializeConnections` is blind to connections that produce no OTel span: raw
TCP to a Postgres or Redis instance, calls from a legacy service without Beyla,
outbound connections to external SaaS APIs. The L4 layer addresses this by
passively observing every outbound TCP connection at the agent.

### Agent-Side Observation

Each agent runs `internal/agent/netobs`, which contains four components:

| Component | Role |
|---|---|
| `Poller` | Polls active connections via `ss -tnp` or `netstat -n` every 30 s (non-Linux fallback; an eBPF `tcp_v4_connect` probe is planned for Linux) |
| `Aggregator` | Deduplicates events by `(dest_ip, dest_port, protocol)` within the flush window; accumulates `bytes_sent`, `bytes_received`, `retransmits`, `rtt_us` |
| `Streamer` | Opens a `ReportConnections` client-streaming RPC per batch, sends one `ReportConnectionsRequest`, then calls `CloseAndReceive` |
| `Manager` | Lifecycle orchestration; wired into `startup.ServiceRegistry` |

Only **outbound** connections are reported. Inbound connections are implicit: if
agent B has an inbound from agent A, agent A will report an outbound to agent B.
This eliminates the duplicate-edge problem that would arise from reporting both
directions.

### Schema: `topology_connections`

```sql
CREATE TABLE topology_connections (
    source_agent_id VARCHAR     NOT NULL,
    dest_agent_id   VARCHAR,              -- NULL for external destinations
    dest_ip         VARCHAR     NOT NULL,
    dest_port       INTEGER     NOT NULL,
    protocol        VARCHAR     NOT NULL,

    bytes_sent      BIGINT      NOT NULL DEFAULT 0,
    bytes_received  BIGINT      NOT NULL DEFAULT 0,
    retransmits     INTEGER     NOT NULL DEFAULT 0,
    rtt_us          INTEGER,              -- NULL on netstat fallback path

    first_observed  TIMESTAMPTZ NOT NULL,
    last_observed   TIMESTAMPTZ NOT NULL,

    PRIMARY KEY (source_agent_id, dest_ip, dest_port, protocol)
);
```

**Upsert semantics.** The table holds one row per unique directed edge
`(source_agent_id, dest_ip, dest_port, protocol)`. Each incoming batch performs
an upsert: `bytes_sent`, `bytes_received`, and `retransmits` are **accumulated**
(not replaced); `last_observed` advances to the newer timestamp;
`rtt_us` uses `COALESCE(EXCLUDED.rtt_us, existing.rtt_us)` to preserve the last
non-NULL RTT measurement.

### Colony: `ReportConnections` Handler

`ReportConnections` in `internal/colony/server/server.go`:

1. Receives a client-streaming RPC from each agent.
2. For each `L4ConnectionEntry` in the batch, calls
   `registry.FindAgentByIP(lc.RemoteIp)` to resolve the destination IP to a
   registered agent ID (internal edge) or leave `dest_agent_id` NULL (external
   edge).
3. Calls `database.UpsertTopologyConnections` to persist the batch inside a
   transaction.
4. Continues reading batches until the stream closes.

No authentication is required beyond the transport (the internal gRPC port is
not exposed publicly). Empty `agent_id` fields are silently skipped.

### Merge in `GetTopology`

`GetTopology` queries both tables and merges:

```
L7 edges  (service_connections via GetServiceConnections)
     +
L4 edges  (topology_connections via GetL4Connections, last 1h)
     ↓
Build L7 edge set: map[(source, target)] → Connection
For each L4 edge:
  - target = dest_agent_id if set, else dest_ip
  - If (source, target) in L7 set → promote to EVIDENCE_LAYER_BOTH
  - Else → append new Connection with EVIDENCE_LAYER_L4_NETWORK
```

The resulting `Connection` list is returned in `GetTopologyResponse.Connections`.
The `EvidenceLayer` enum on each connection lets consumers distinguish origin:

```protobuf
enum EvidenceLayer {
  EVIDENCE_LAYER_UNSPECIFIED = 0;
  EVIDENCE_LAYER_L7_TRACE    = 1;  // Derived from beyla_traces (RFD 092)
  EVIDENCE_LAYER_L4_NETWORK  = 2;  // Observed via ss/netstat (RFD 033)
  EVIDENCE_LAYER_BOTH        = 3;  // Edge present in both layers
}
```

---

## Exposure Surfaces

### 1. CLI: `coral query topology`

`internal/cli/query/topology.go` calls `GetTopology` via Connect RPC and renders
connections as either an ASCII table (default) or JSON.

Text output example:
```
Service Topology (last 1h, 4 connection(s)):

FROM SERVICE    →  TO SERVICE       PROTOCOL  LAYER
------------       ----------       --------  -----
otel-app        →  cpu-app          HTTP      L7
user-service    →  postgres         SQL       L7
api-gateway     →  redis            TCP       L4
api-gateway     →  user-service     HTTP      BOTH
```

The `LAYER` column shows `L7`, `L4`, or `BOTH`. Column widths are computed
dynamically from the longest values in each column.

**`--include-l4` flag** (default `true`): pass `--include-l4=false` to suppress
L4-only edges and show only trace-derived edges.

JSON output includes a `layer` field per connection:
```json
{
  "colony_id": "my-colony",
  "connections": [
    {"from": "api-gateway", "to": "redis", "protocol": "TCP", "layer": "L4"},
    {"from": "otel-app",    "to": "cpu-app", "protocol": "HTTP", "layer": "L7"}
  ]
}
```

### 2. MCP / `coral ask`

Per RFD 100, individual per-operation MCP tools have been retired in favour of
the single `coral_cli` meta-tool. MCP clients call:

```json
{"name": "coral_cli", "arguments": {"args": ["query", "topology"]}}
```

The JSON output (auto-appended `--format json`) includes the `layer` field on
every connection, so the LLM can distinguish L4-only edges from trace-derived
ones.

`coral ask` (MCP dispatch mode) calls `coral_query_summary` and
`coral_list_services` for system-prompt context. Topology is available on-demand
via `coral_cli`.

### 3. gRPC API (`GetTopologyResponse`)

Exposes agents and merged L4+L7 connections for programmatic consumers. The
`Connection.evidence_layer` field (added by RFD 033) carries the `EvidenceLayer`
enum value for each edge.

---

## Data Flow Diagram

```
Beyla eBPF (agent)                    ss/netstat Poller (agent)
  │  captures HTTP/gRPC spans           │  polls active TCP connections
  ▼                                     ▼
beyla_traces (agent DuckDB)          netobs.Aggregator
  │  sequence-based polling (RFD 089)   │  dedup + accumulate metrics
  ▼                                     ▼
beyla_traces (colony DuckDB)         ReportConnections RPC stream
  │  parent-span self-join               │  IP → agent correlation
  │  MaterializeConnections (TTL 30s)    ▼
  ▼                                  topology_connections (colony DuckDB)
service_connections (colony DuckDB)    │
  │                                     │
  └──────────────┬──────────────────────┘
                 ▼
         GetTopology handler
         (merges L4 + L7 edges, sets EvidenceLayer)
                 │
     ┌───────────┼──────────────┐
     ▼           ▼              ▼
coral query  coral_cli MCP   gRPC API
topology     tool           consumers
(LAYER col)  (layer field)
```

---

## Testing Strategy

### Unit Tests

- **`internal/colony/database/connections_test.go`** — L7 materialization: edge
  detection, same-service exclusion, aggregation, multi-edge, time-window filter,
  cache hit/miss.
- **`internal/colony/database/topology_connections_test.go`** — L4 upsert
  semantics: insert, metric accumulation across batches, `last_observed`
  filtering, external-edge (NULL `dest_agent_id`), empty-batch no-op.
- **`internal/colony/server/topology_merge_test.go`** — merge logic: L4-only
  edge, external edge (raw IP target), empty result when no data.
- **`internal/cli/query/topology_test.go`** — `evidenceLayerLabel` mapping,
  `filterConnections` with `includeL4=true/false`.
- **`internal/agent/netobs/`** — aggregator deduplication, netstat/ss parser on
  fixture output.

### Integration Tests

- **`tests/integration/topology_test.go`** — L7 OTel leak fix: validates that
  broken `parent_span_id` references (from OTel SDK context leaking into the HTTP
  request) are correctly excluded from materialization while clean Beyla-owned
  traces produce edges.

### E2E Tests (`tests/e2e/distributed/`)

- **`cli_query_test.go::TestCLIQueryTopology`** — drives real `otel-app →
  cpu-app` traffic, polls until the L7 edge appears, asserts `LAYER` column
  header and JSON `layer` field.
- **`topology_l4_test.go::L4TopologySuite`** — injects synthetic L4 edges via
  `ReportConnections` RPC and validates:
  - `TestL4EdgesAppearInTopology`: L4 edge appears in text output with `LAYER=L4`.
  - `TestIncludeL4FalseFiltersL4Edges`: `--include-l4=false` suppresses L4-only
    edges.
  - `TestL4JSONLayerField`: JSON output contains `layer: "L4"` per connection.
  - `TestL4InternalEdgeResolution`: dest IP matching a registered agent's mesh IP
    resolves to agent ID instead of raw IP.

---

## Future Engineering Notes

**eBPF `tcp_v4_connect` probe.** The current Linux path still falls back to
`ss`/`netstat`. A `tcp_v4_connect` kprobe would give sub-second latency and
accurate per-connection byte counts without polling overhead.

**Protocol detection from span attributes.** The L7 join currently hard-codes
`'http'` as the protocol. `beyla_traces.attributes` carries span kind and
protocol hints; parsing `db.system` attributes would allow automatic distinction
of HTTP, gRPC, and SQL edges.

**Traffic-type inference.** Infer application protocol from L4 connection
patterns (payload-size distributions) without inspecting content — e.g.,
identify Redis-like traffic on a non-standard port.

**Anomaly detection.** Alert when a service suddenly connects to a new
unrecognised external IP or port not seen in its recent history.

**Async topology.** Pub/sub and message-queue calls produce no synchronous
parent-child link in `beyla_traces`. A future approach could use
baggage-propagated correlation IDs to link producer and consumer spans.

**Incremental materialization.** The current full-window re-join is O(N) in the
trace table size. An incremental approach — joining only spans inserted since the
last materialization checkpoint — would keep the operation O(new-spans).

---

## Related Design Documents

- [**RFD 033**: Infrastructure & L4 Topology Discovery](../../RFDs/033-service-topology-discovery.md)
- [**RFD 092**: Service Topology](../../RFDs/092-service-topology.md)
- [**RFD 089**: Sequence-Based Polling Checkpoints](../../RFDs/089-sequence-based-polling-checkpoints.md)
- [**RFD 084**: Dual-Source Service Discovery](../../RFDs/084-dual-source-service-discovery.md)
- [**RFD 036**: Beyla Distributed Tracing](../../RFDs/036-beyla-distributed-tracing.md)
- [**RFD 100**: CLI Dispatch Mode](../../RFDs/100-cli-dispatch-mode.md)
