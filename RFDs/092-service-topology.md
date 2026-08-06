---
rfd: "092"
title: "Service Topology"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: ["084"]
database_migrations: []
areas: ["colony", "mcp", "cli", "ask"]
---

# RFD 092 - Service Topology

**Status:** ✅ Implemented

## Summary

Derive and expose a live service dependency graph by mining cross-service call relationships
from observed trace data. The graph is surfaced through the existing (but unimplemented)
`GetTopology` colony API, a new `coral_topology` MCP tool, a `coral query topology` CLI
command, and as a compact call-graph injected into the `coral ask` system prompt — giving
the LLM immediate awareness of how services relate before it has called a single tool.

## Problem

- **No service dependency context for the LLM.** When `coral ask` investigates an issue,
  it has no knowledge of which services call which. It must blindly call `coral_query_summary`
  on each service individually and infer relationships from trace data only after multiple
  round-trips. A single latency spike in `api-gateway` requires the LLM to discover that
  `api-gateway → user-service → postgres` before it can reason about root cause.

- **`GetTopology` RPC is unimplemented.** The API contract and generated code already exist
  in the colony proto, but the handler returns `CodeUnimplemented`.

- **`service_connections` table is never populated.** The DuckDB schema and table
  registration exist, but no code writes to or reads from this table.

- **No CLI visibility into service topology.** Operators have `coral colony agents` and
  `coral query summary` but no way to see the call graph their services form.

## Solution

Derive cross-service call relationships on demand from the existing `beyla_traces` table
using a parent-span join query, materialize results into `service_connections` with a short
cache TTL, and expose the graph through four surfaces: colony API, MCP tool, CLI command,
and the `coral ask` system prompt.

**Key Design Decisions:**

- **Derive from traces, not instrumentation.** Services emit spans with `parent_span_id`
  linking calls across service boundaries. A self-join on `beyla_traces` grouped by
  `(from_service, to_service)` recovers the call graph without requiring any agent-side
  changes or explicit dependency declaration.

- **Materialize with a short cache TTL.** The join query is moderately expensive on large
  trace volumes. Results are written to `service_connections` and re-derived only when the
  table is stale (≥30 s). This keeps `GetTopology` fast while ensuring the graph stays
  current.

- **Reuse existing proto.** `GetTopologyRequest`, `GetTopologyResponse`, and `Connection`
  are already generated. The `Connection` fields `{source_id, target_id, connection_type}`
  map directly to `{from_service, to_service, protocol}`. No proto changes are needed.

- **Compact system-prompt injection.** The full topology response is too verbose for a
  system prompt. A single line (`Call graph: api-gateway→user-service (HTTP), ...`) is
  sufficient for the LLM to reason about service relationships and is fetched in parallel
  with the existing health-alert and service-list calls.

**Architecture Overview:**

```
beyla_traces (DuckDB)
  └─ parent-span self-join ──► service_connections (materialized, TTL 30s)
                                       │
                              GetTopology RPC handler
                                       │
              ┌────────────────────────┼────────────────────────┐
              ▼                        ▼                         ▼
       coral_topology           coral query topology       coral ask
       MCP tool                 CLI command               system prompt
       (LLM on-demand)          (human output)            (compact call graph)
```

**Benefits:**

- LLM investigations start with the call graph already in context — fewer tool round-trips.
- Operators can inspect service dependencies with a single CLI command.
- Zero agent changes; fully derived from data already being collected.

### Component Changes

1. **Database** (`internal/colony/database/`):

   - Add a connection query that runs the parent-span self-join on `beyla_traces` and
     returns the discovered edges grouped by `(from_service, to_service, protocol)`.
   - Add materialization logic that upserts results into `service_connections`, refreshing
     `last_observed` and `connection_count`.
   - Add a cache-aware fetch that returns stored rows when fresh (TTL 30 s) and
     triggers re-materialization when stale.

2. **Colony server** (`internal/colony/server.go`):

   - Implement the `GetTopology` handler (currently returns `CodeUnimplemented`).
   - Merge connected agents from the registry with cached service connections from the DB.
   - Map `service_connections` rows to the existing `Connection` proto fields.

3. **MCP tool** (`internal/colony/mcp/tools_discovery.go`):

   - Register `coral_topology`: calls the `GetTopology` colony RPC and formats the result
     as human-readable text for LLM consumption.
   - Tool description directs the LLM to call this before investigating cross-service issues.

4. **CLI** (`internal/cli/query/topology.go`):

   - New `topology` subcommand under `coral query`.
   - Calls the `GetTopology` RPC and renders connections as an ASCII table (default) or JSON.

5. **Ask system prompt** (`internal/cli/ask/agent.go`):

   - New context fetch that calls `coral_topology` and formats the result as a compact
     one-line call graph for injection into the system prompt.
   - All three context fetches (service list, health alerts, topology) run in parallel
     using the buffered-channel goroutine pattern already established in the package.

## Implementation Plan

### Phase 1: Database

- [x] Add `MaterializeConnections` to `internal/colony/database/connections.go` — derives
  service edges from the `beyla_traces` parent-span self-join and upserts into
  `service_connections`.
- [x] Add `GetServiceConnections` to `internal/colony/database/connections.go` — cache-aware
  fetch with 30 s TTL; re-materializes when stale.
- [x] Added `connectionsMu` + `connectionsLastMaterialized` fields to `Database` struct in
  `internal/colony/database/database.go`.

### Phase 2: Colony API

- [x] Implemented `GetTopology` handler in `internal/colony/server/server.go` — merges
  agents from registry with connections from DB; maps `ServiceConnection` rows to
  `colonyv1.Connection` proto messages.

### Phase 3: MCP Tool

- [x] Added `TopologyInput` type to `internal/colony/mcp/types.go`.
- [x] Added `coral_topology` to `internal/colony/mcp/tools_discovery.go` — calls
  `GetServiceConnections`, formats output as readable call graph for LLM.
- [x] Registered in `internal/colony/mcp/server.go` (`registerTools`, `ExecuteTool` switch,
  `listToolNames`, `getToolDescriptions`, `getToolSchemas`).

### Phase 4: CLI Command

- [x] Added `NewTopologyCmd` in `internal/cli/query/topology.go` — calls `GetTopology` RPC,
  renders as ASCII table (default) or JSON.
- [x] Registered under `coral query` in `internal/cli/query/root.go`.

### Phase 5: Ask System Prompt

- [x] Added `fetchTopologyContext` and `formatCompactCallGraph` to `internal/cli/ask/agent.go`.
- [x] Parallelized all three context fetches in `buildSystemPrompt` using goroutines and
  buffered channels.
- [x] Compact call-graph line injected after ALERTS section in system prompt.

## API Changes

### Colony RPC (already generated — no proto changes)

```protobuf
// Already exists in coral/colony/v1
message GetTopologyRequest {}

message GetTopologyResponse {
  string colony_id = 1;
  repeated Agent agents = 2;         // From registry.ListAll()
  repeated Connection connections = 3; // From service_connections
}

message Connection {
  string source_id = 1;        // from_service
  string target_id = 2;        // to_service
  string connection_type = 3;  // protocol (http, grpc, sql)
}
```

### Database Query

```sql
-- Parent-span join: find calls that cross service boundaries.
SELECT
  parent.service_name  AS from_service,
  child.service_name   AS to_service,
  'http'               AS protocol,   -- refined from span attributes
  COUNT(*)             AS connection_count,
  MAX(child.start_time) AS last_observed,
  MIN(child.start_time) AS first_observed
FROM beyla_traces child
JOIN beyla_traces parent
  ON  child.trace_id       = parent.trace_id
  AND child.parent_span_id = parent.span_id
  AND child.service_name  != parent.service_name
WHERE child.start_time >= ?
GROUP BY parent.service_name, child.service_name
```

### CLI Commands

```bash
# Default: text table
coral query topology

# Example output:
Service Topology (last 1h, 3 connections):

FROM SERVICE      TO SERVICE        PROTOCOL   CALLS    LAST SEEN
api-gateway       user-service      HTTP       2341     2s ago
user-service      postgres          SQL        1823     5s ago
worker            queue             gRPC        234     1m ago

# JSON output
coral query topology --format json
```

### MCP Tool

```
Tool: coral_topology
Input: { "since": "1h" }  (optional, default 1h)
Output (text for LLM):
  Service call graph (last 1h):
  api-gateway → user-service (HTTP, 2341 calls, last: 2s ago)
  user-service → postgres (SQL, 1823 calls, last: 5s ago)
  worker → queue (gRPC, 234 calls, last: 1m ago)
```

### System Prompt Injection

The `buildSystemPrompt` call graph line (appended after the ALERTS section):

```
Call graph: api-gateway→user-service (HTTP), user-service→postgres (SQL), worker→queue (gRPC)
```

Empty if no cross-service calls observed in the last hour.

## Testing Strategy

### Unit Tests

- Connection query: in-memory DuckDB fixture with parent/child spans across two services —
  verify edge detection, deduplication, and correct call counts.
- Cache-aware fetch: verify cache hit/miss behaviour based on `last_observed` timestamps.
- Topology text formatter: verify compact call-graph string from a mocked `GetTopologyResponse`.

### Integration Tests

- Insert trace spans with cross-service parent links, call `GetTopology`, verify connections
  in response.
- Call `coral_topology` MCP tool, verify output format.

### E2E Tests

- `coral query topology` with a live colony — verify at least the CLI reaches the RPC
  without error (even with zero connections).

## Implementation Status

**Core Capability:** ✅ Complete

All five phases implemented. Build passes, all unit tests pass.

| Component                        | File                                      | Status |
|----------------------------------|-------------------------------------------|--------|
| Database materialization + cache | `internal/colony/database/connections.go` | ✅      |
| `GetTopology` RPC handler        | `internal/colony/server/server.go`        | ✅      |
| `coral_topology` MCP tool        | `internal/colony/mcp/tools_discovery.go`  | ✅      |
| `coral query topology` CLI       | `internal/cli/query/topology.go`          | ✅      |
| Ask system prompt injection      | `internal/cli/ask/agent.go`               | ✅      |

## Future Work

**Topology-aware routing** (Future RFD)
- Use the call graph to route `coral ask` queries automatically to the right upstream service.

**Cycle detection** (Future RFD)
- Flag circular dependencies in the call graph as a separate diagnostic.

**Historical topology diffing** (Future RFD)
- Alert when the call graph changes significantly between deployments.

**Infrastructure L4 Correlation (RFD 033)**
- Augment this trace-based topology with raw L4 network observation to discover
  non-instrumented dependencies (databases, external APIs) that traces don't see.
