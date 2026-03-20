---
rfd: "033"
title: "Infrastructure & L4 Topology Discovery"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "013", "025", "092" ]
database_migrations: [ "topology_connections" ]
areas: [ "observability", "networking", "topology" ]
---

# RFD 033 - Infrastructure & L4 Topology Discovery

**Status:** 🚧 Draft

## Summary

Enable automatic discovery of infrastructure dependencies that produce no
OpenTelemetry spans — raw databases, external APIs, and legacy systems — by
passively observing L4 TCP connections at the agent level via eBPF. Connection
events are streamed to the colony, correlated against the agent registry, and
merged with the trace-derived topology from RFD 092 to give operators and the
LLM a complete dependency picture.

## Problem

**Current limitations**

- **Trace Blindness**: RFD 092 derives topology by mining `beyla_traces`. If a
  connection produces no OTel span (raw TCP to a sensor, legacy DB client,
  non-instrumented process), it is invisible to Coral.
- **Missing Infrastructure**: Common components like Redis, Memcached, or
  Postgres may not be instrumented on the server side, and client-side
  instrumentation can be partial or disabled.
- **Network Metadata**: Trace data provides latency and status codes but lacks
  transport-level health indicators such as TCP retransmits, throughput, or
  connection churn.
- **External Dependencies**: Outbound calls to external SaaS providers or cloud
  services are often not traced when the client library lacks middleware support.

**Use cases addressed**

- **"What else is this service talking to?"**: Discovering undocumented
  dependencies that traces miss.
- **Network Health**: Detecting high retransmit rates between specific agents
  that indicate localised network issues.
- **Complete Blast Radius**: Understanding the impact of a database outage even
  when not all clients emit traces.

## Solution

Implement continuous network observation using **custom eBPF connection
tracking** and a **netstat/ss fallback** to report all active outbound L4
connections from each agent to the colony.

**Key Design Decisions:**

- **Passive L4 observation.** Hook `tcp_v4_connect` to capture every outbound
  TCP connection attempt without inspecting payloads. Only outbound connections
  are reported; inbound connections are implicit (if Agent B has an inbound from
  Agent A, Agent A will report an outbound to Agent B). This eliminates the
  duplicate-edge problem that would arise from reporting both directions.

- **Outbound-only reporting eliminates deduplication complexity.** Because only
  the initiating side reports the edge, the colony sees each connection exactly
  once. The `source_agent_id` in `topology_connections` is always the initiator,
  and `dest_agent_id` is resolved by IP lookup against the registry.

- **Streaming edge events, not raw packet events.** Agents aggregate connections
  into per-edge summaries (keyed on `dest_ip:dest_port:protocol`) and stream
  batches on a configurable interval (default 30 s). This bounds colony ingest
  rate regardless of connection churn volume.

- **Upsert semantics in `topology_connections`.** The table holds one row per
  unique directed edge `(source_agent_id, dest_ip, dest_port, protocol)`. Each
  incoming batch from an agent performs an upsert that refreshes `last_observed`
  and accumulates `bytes_sent`, `bytes_received`, and `retransmits`. This gives
  a current-state view rather than an append-only event log.

- **Colony correlates IP → agent identity.** When Agent A reports a connection
  to IP X, the colony checks whether IP X belongs to a registered agent (internal
  edge) or not (external edge). External edges are stored with `dest_agent_id`
  NULL and the raw `dest_ip`.

- **Merged topology response.** The existing `GetTopology` RPC merges L7 edges
  from `service_connections` (RFD 092) with L4 edges from `topology_connections`.
  Where the same logical edge appears in both layers the L7 edge is preferred
  (richer metadata), but the L4 edge's transport metrics are attached. A new
  `evidence_layer` field on `Connection` lets consumers distinguish the source.

- **Netstat/ss fallback for non-Linux.** On macOS/Windows the eBPF probe is
  unavailable. A background goroutine polls `netstat -n` or `ss -tnp` every 30 s
  and synthesises the same connection events. Transport metrics (retransmits,
  RTT) are omitted on the fallback path.

**Benefits:**

- The call graph shown by `coral query topology` and injected into `coral ask`
  now includes dependencies that emit no traces at all.
- Operators gain TCP-level health context (retransmit rate, RTT) alongside the
  existing call-count and latency data.
- Zero change to application code; fully passive observation.

**Architecture Overview:**

```
Agent (Linux)                     Agent (macOS/Win)
  eBPF probe → Aggregator            netstat poller → Aggregator
                    │                                      │
                    └──────────── gRPC stream ─────────────┘
                                        │
                              Colony: Correlator
                                (IP → agent registry)
                                        │
                              DuckDB: topology_connections
                                        │
                    ┌───────────────────┴────────────────────┐
                    ▼                                         ▼
          GetTopology RPC                          (future) anomaly alerts
          (merges L4 + L7 edges)
                    │
        ┌───────────┼──────────────┐
        ▼           ▼              ▼
 coral_topology  coral query    coral ask
 MCP tool        topology CLI   system prompt
```

### Component Changes

1. **Agent** (`internal/agent/ebpf/`, `internal/agent/netobs/`):

   - New eBPF module hooking `tcp_v4_connect` to capture outbound TCP events
     (source IP/port, dest IP/port, PID, TCP state).
   - Netstat/ss fallback poller for non-Linux platforms; same output shape,
     no transport metrics.
   - Connection aggregator: deduplicates events by `(dest_ip, dest_port,
     protocol)`, accumulates metrics, and flushes batches to the colony
     streamer on the reporting interval.
   - gRPC client keeping a long-lived `ReportConnections` stream to the colony.

2. **Colony** (`internal/colony/server/`, `internal/colony/database/`):

   - New `ReportConnections` bidirectional-stream RPC handler in
     `internal/colony/server/server.go`.
   - Identity correlator: maps `dest_ip` to `agent_id` via the agent registry
     on each received batch.
   - Upsert logic writing to `topology_connections` (one row per directed edge,
     refreshing metrics on each batch).
   - Updated `GetTopology` handler: queries both `service_connections` (L7) and
     `topology_connections` (L4), merges them, and sets `evidence_layer`
     on each `Connection` message.

3. **CLI / MCP / Ask** (`internal/cli/query/topology.go`,
   `internal/colony/mcp/tools_discovery.go`, `internal/cli/ask/agent.go`):

   - `coral query topology` gains an `--include-l4` flag (default on) and a
     `LAYER` column in its output table.
   - `coral_topology` MCP tool output annotates each edge with its evidence
     layer.
   - `coral ask` system-prompt call graph already uses `GetTopology`; no
     additional changes required — it will surface L4 edges automatically.

## Implementation Plan

### Phase 1: Protocol & Storage

- [ ] Add `ReportConnectionsRequest`, `ReportConnectionsResponse`,
      `L4ConnectionEntry`, and `ConnectionDirection` to `colony.proto`.
- [ ] Add `evidence_layer` field to the existing `Connection` message in
      `colony.proto`.
- [ ] Regenerate protobuf Go bindings.
- [ ] Create `topology_connections` table migration in DuckDB.
- [ ] Implement `ReportConnections` stream handler in the colony (receive,
      correlate IP → agent, upsert rows).

### Phase 2: Agent Observation

- [ ] Implement eBPF probe for `tcp_v4_connect` in `internal/agent/ebpf/`.
- [ ] Implement netstat/ss fallback poller for non-Linux in
      `internal/agent/netobs/`.
- [ ] Implement connection aggregator (deduplication + metric accumulation).
- [ ] Implement gRPC streaming client sending batches to colony.

### Phase 3: Correlation & Topology Merge

- [ ] Implement IP → agent identity lookup in colony correlator.
- [ ] Update `GetTopology` handler to merge L4 and L7 edges and set
      `evidence_layer`.
- [ ] Update `coral query topology` to show `LAYER` column and accept
      `--include-l4` flag.
- [ ] Update `coral_topology` MCP tool output to annotate edges by layer.

### Phase 4: Testing & Documentation

- [ ] Unit tests: eBPF aggregator deduplication and metric accumulation.
- [ ] Unit tests: netstat parser on fixture output.
- [ ] Unit tests: IP → agent correlation logic.
- [ ] Unit tests: `GetTopology` merge logic (L4-only, L7-only, overlap cases).
- [ ] Integration tests: insert synthetic connection batches, verify upsert
      semantics and `last_observed` refresh.
- [ ] E2E test: `coral query topology` with a live colony — verify L4 edges
      appear after agent reports connections.

## API Changes

### Protobuf (`coral/colony/v1/colony.proto`)

```protobuf
service ColonyService {
  // Existing — augmented to merge L4 edges.
  rpc GetTopology(GetTopologyRequest) returns (GetTopologyResponse);

  // New — long-lived stream from agent to colony.
  rpc ReportConnections(stream ReportConnectionsRequest)
      returns (ReportConnectionsResponse);
}

// Existing message — one new field added.
message Connection {
  string source_id      = 1;  // from_service (L7) or source_agent_id (L4)
  string target_id      = 2;  // to_service (L7) or dest_agent_id / dest_ip (L4)
  string connection_type = 3; // protocol: http, grpc, sql, tcp

  // Added by RFD 033.
  EvidenceLayer evidence_layer = 4;
}

enum EvidenceLayer {
  EVIDENCE_LAYER_UNSPECIFIED = 0;
  EVIDENCE_LAYER_L7_TRACE    = 1;  // Derived from beyla_traces (RFD 092)
  EVIDENCE_LAYER_L4_NETWORK  = 2;  // Observed via eBPF / netstat (RFD 033)
  EVIDENCE_LAYER_BOTH        = 3;  // Edge present in both layers
}

message ReportConnectionsRequest {
  string agent_id                  = 1;
  repeated L4ConnectionEntry connections = 2;
}

message ReportConnectionsResponse {}

message L4ConnectionEntry {
  string remote_ip   = 1;
  uint32 remote_port = 2;
  string protocol    = 3;  // "tcp", "udp"

  // Accumulated since last report batch.
  uint64 bytes_sent     = 4;
  uint64 bytes_received = 5;
  uint32 retransmits    = 6;
  uint32 rtt_us         = 7;  // 0 on netstat fallback path

  google.protobuf.Timestamp last_observed = 8;
}
```

### Database Schema

```sql
-- One row per directed edge. Upserted on each agent batch.
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

### CLI Commands

```bash
# Default: text table, both L4 and L7 edges
coral query topology

# Example output (merged L4 + L7):
Service Topology (last 1h, 4 connections):

FROM SERVICE    TO SERVICE       PROTOCOL  LAYER  CALLS   LAST SEEN
api-gateway     user-service     HTTP      L7     2341    2s ago
user-service    postgres         SQL       L7     1823    5s ago
worker          queue            gRPC      L7      234    1m ago
api-gateway     redis            TCP       L4     —       8s ago  retx=0

# L4-only edges show "—" for call count (no span data available).
# Retransmit count is appended when non-zero.

# Suppress L4 edges
coral query topology --include-l4=false

# JSON output
coral query topology --format json
```

### MCP Tool Output

```
Tool: coral_topology
Input: { "since": "1h" }  (optional, default 1h)

Output (text for LLM):
  Service call graph (last 1h):
  api-gateway → user-service (HTTP, L7, 2341 calls, last: 2s ago)
  user-service → postgres (SQL, L7, 1823 calls, last: 5s ago)
  worker → queue (gRPC, L7, 234 calls, last: 1m ago)
  api-gateway → redis:6379 (TCP, L4, last: 8s ago)
```

## Testing Strategy

### Unit Tests

- **Aggregator**: verify that multiple events for the same `(dest_ip, dest_port,
  protocol)` within one flush window are collapsed into a single entry with
  accumulated `bytes_sent`, `bytes_received`, and `retransmits`.
- **Netstat parser**: verify correct edge extraction from fixture `netstat -n`
  and `ss -tnp` output; verify `rtt_us = 0` on this path.
- **IP correlator**: verify internal edge (`dest_agent_id` set) vs. external
  edge (`dest_agent_id` NULL) resolution given a mock agent registry.
- **Topology merge**: cover all three cases — L7-only edge, L4-only edge, and
  an edge present in both layers (expect `EVIDENCE_LAYER_BOTH`).

### Integration Tests

- Insert synthetic `ReportConnectionsRequest` batches via the stream RPC;
  verify `topology_connections` rows are upserted correctly and `last_observed`
  is refreshed on subsequent batches.
- Insert overlapping L4 and L7 data; call `GetTopology` and verify merged
  `Connection` list with correct `evidence_layer` values.

### E2E Tests

- Run `coral query topology` against a live colony with at least one active
  agent; verify the command exits cleanly and the `LAYER` column is present.
- Verify `coral_topology` MCP tool output includes the layer annotation string.

## Security Considerations

- **Privacy**: L4 observation reveals destination IPs and ports. While payloads
  are never inspected, the metadata can be sensitive (e.g., connections to
  internal APIs or third-party SaaS endpoints). The `topology_connections` table
  should be subject to the same access controls as other colony data.
- **Overhead**: Aggregation at the agent (one row per unique edge per interval)
  is mandatory. Raw-event streaming would be unacceptably noisy on busy hosts.
- **Privilege**: eBPF requires `CAP_NET_ADMIN` / `CAP_BPF` or root. The netstat
  fallback may require similar privileges on some systems. Agents should log a
  clear error and fall back gracefully rather than failing silently.

## Implementation Status

**Core Capability:** ⏳ Not Started

## Future Work

**Traffic-type inference** (Future RFD)
- Infer application protocol from connection patterns (e.g., Redis-like traffic
  on a non-standard port) using payload-size distributions without inspecting
  content.

**Anomaly detection** (Future RFD)
- Alert when a service suddenly connects to a new unrecognised external IP or
  port not seen in its recent history.

**VPC Flow Log integration** (Future RFD)
- Import topology data from cloud provider flow logs to surface edges outside
  the Coral WireGuard mesh.
