---
rfd: "033"
title: "Service Topology Discovery via eBPF"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "013", "025" ]
database_migrations: [ "topology_connections", "topology_services" ]
areas: [ "observability", "networking", "topology" ]
---

# RFD 033 - Service Topology Discovery via eBPF

**Status:** 🚧 Draft

## Summary

Enable automatic discovery of service-to-service communication topology by
combining eBPF connection tracking with existing OpenTelemetry span data. Agents
observe network connections and HTTP/gRPC requests, reporting them to the colony
which correlates this data to build a live topology graph showing which services
communicate with each other, at what rates, and with what protocols.

## Problem

**Current behavior/limitations**

- Coral's user-facing documentation (USER-EXPERIENCE.md) describes topology
  discovery, but this feature is **not yet implemented**.
- Current observability focuses on individual service metrics (RED metrics via
  eBPF, OTLP ingestion) but doesn't capture **service relationships**.
- Operators cannot visualize dependencies between services (frontend → api →
  database).
- Debugging cross-service issues requires manual correlation of logs and metrics
  from multiple components.
- No automated detection of new service connections or changes in communication
  patterns.

**Why this matters**

- **Dependency visibility**: Understanding which services depend on each other
  is critical for impact analysis during incidents ("if this database goes down,
  what breaks?").
- **Root cause analysis**: Many production issues involve cross-service
  communication (network timeouts, API version mismatches, cascading failures).
  Topology graphs make these relationships explicit.
- **AI-driven insights**: Coral's LLM integration (RFD 014, 030) needs topology
  context to answer questions like "Why is checkout slow?" (answer: checkout →
  payments → external card validator with 5s timeout).
- **Change impact**: When deploying a new version, topology shows blast
  radius ("this API change affects 7 downstream services").
- **Security**: Detect unexpected connections that may indicate compromised
  services or misconfigurations.

**Use cases affected**

- `coral topology` command (documented but unimplemented)
- Dashboard topology visualization (documented but unimplemented)
- AI queries requiring service relationship context
- Incident response workflows needing dependency mapping
- Capacity planning based on traffic patterns between services

## Solution

Implement topology discovery using a **hybrid approach** that combines multiple
data sources:

1. **Primary: OpenTelemetry eBPF spans** (HTTP/gRPC service-level topology)
2. **Secondary: Custom eBPF connection tracking** (infrastructure-level
   connections)
3. **Fallback: Periodic netstat/ss polling** (when eBPF unavailable)

Agents observe connections locally and report them to the colony. The colony
correlates connection data with agent identity to infer service relationships
and stores the topology graph in DuckDB.

### Key Design Decisions

- **Agent-local observation**: Each agent observes its own outbound connections
  and inbound listeners. No centralized packet capture or network tap required.
- **Colony-side correlation**: Agents report "connected to 10.42.0.5:8080",
  colony knows agent B is at 10.42.0.5, infers "agent A → agent B".
- **Protocol-aware topology**: eBPF HTTP/gRPC tracing provides service-level
  topology (request rates, endpoints). Custom connection tracking provides
  infrastructure topology (all TCP/UDP connections).
- **Passive observation**: Coral is never in the request path. Topology is built
  from observation only.
- **Incremental updates**: Agents stream connection events (new connection,
  closed connection) for real-time topology updates, not periodic full
  snapshots.
- **Multi-source fusion**: Combine OpenTelemetry eBPF (already implemented for
  RED metrics per RFD 025) with custom connection tracking for comprehensive
  coverage.

### Benefits

- **Zero-configuration topology**: Works automatically without service
  instrumentation or DNS inspection.
- **Real-time updates**: Topology reflects current state, not stale snapshots.
- **Multi-layer visibility**: See both application-layer (HTTP services) and
  infrastructure-layer (databases, caches, message queues) connections.
- **Low overhead**: Leverage existing OpenTelemetry eBPF probes; add minimal
  custom connection tracking.
- **Cross-platform support**: eBPF for Linux, fallback to netstat/ss for
  macOS/Windows agents.
- **AI context**: Provides LLM with relationship graph for answering dependency
  questions.

### Architecture Overview

```
┌───────────────────────────────────────────────────────┐
│ Agent A (frontend)                                     │
│                                                        │
│  ┌──────────────────────────────────────────────────┐ │
│  │ Topology Observer                                │ │
│  │                                                  │ │
│  │  1. OpenTelemetry eBPF (HTTP/gRPC spans)        │ │
│  │     → Sees: POST /api/checkout → api:8080      │ │
│  │                                                  │ │
│  │  2. Custom eBPF (connection tracking)            │ │
│  │     → Sees: tcp_connect to 10.42.0.5:8080       │ │
│  │                                                  │ │
│  │  3. Fallback (netstat/ss periodic)               │ │
│  │     → Sees: ESTABLISHED to 10.42.0.5:8080       │ │
│  └──────────────────────────────────────────────────┘ │
│                          │                            │
│                          ▼                            │
│              Report connections to colony             │
└───────────────────────────────────────────────────────┘
                          │
                          ▼
           ┌──────────────────────────────┐
           │ Colony / Topology Correlator │
           │                              │
           │  Receives from agents:       │
           │    Agent A → 10.42.0.5:8080  │
           │    Agent B at 10.42.0.5      │
           │                              │
           │  Infers:                     │
           │    frontend → api            │
           │    (HTTP, 200 req/min)       │
           │                              │
           │  Stores in DuckDB:           │
           │    topology_connections      │
           └──────────────────────────────┘
                          │
                          ▼
           ┌──────────────────────────────┐
           │ CLI / Dashboard / MCP        │
           │                              │
           │  coral topology              │
           │  Topology graph display      │
           │  LLM queries with context    │
           └──────────────────────────────┘
```

### Component Changes

1. **Agent (Topology Observer)**
    - **OpenTelemetry eBPF Integration**: Extract topology from existing
      HTTP/gRPC spans (already collected for RED metrics per RFD 025).
        - Parse `net.peer.ip`, `net.peer.name`, `http.target` from spans
        - Map to service names when available (via DNS, K8s API, or manual
          config)
    - **Custom eBPF Connection Tracker**: New eBPF program to track TCP
      connection lifecycle.
        - Hook `tcp_v4_connect`, `tcp_v6_connect` for outbound connections
        - Hook `inet_csk_accept` for inbound connections
        - Track connection state changes via `inet_sock_set_state` tracepoint
    - **Fallback Observer**: Periodic `netstat`/`ss` execution when eBPF
      unavailable (macOS, Windows, old Linux).
    - **Connection Reporting**: Stream connection events to colony via gRPC.
    - **Rate limiting**: Aggregate connection events (avoid spamming colony with
      every packet).

2. **Colony (Topology Correlator)**
    - **Agent Registry**: Track which agent IDs map to which mesh IPs.
    - **Correlation Engine**: Match outbound connections from agent A to inbound
      listeners on agent B.
    - **Service Name Resolution**: Map agent IDs to service names (from agent
      metadata, K8s labels, config).
    - **Graph Builder**: Construct directed graph of service → service edges.
    - **Storage**: Persist topology in DuckDB (`topology_connections`,
      `topology_services` tables).
    - **API Handlers**: Expose topology via gRPC (for CLI) and MCP (for LLM
      integration).
    - **Change Detection**: Detect new connections, broken connections, topology
      changes over time.

3. **CLI / Dashboard**
    - **`coral topology` command**: Display service topology as ASCII tree or
      export as GraphViz DOT.
    - **Dashboard topology view**: Interactive graph visualization (D3.js or
      similar).
    - **Filtering**: Show topology for specific service, environment, or time
      range.
    - **Export**: JSON, DOT, or PNG formats for external tools.

4. **MCP Integration (RFD 004)**
    - New MCP tool: `coral_get_topology` for LLM queries.
    - Include topology context in LLM responses ("frontend depends on api and
      cache").
    - Enable queries like "What services depend on database?" or "Show me the
      call path from frontend to postgres".

**Configuration Example:**

```yaml
# agent-config.yaml
topology:
    enabled: true

    # Data sources (in priority order)
    sources:
        -   type: opentelemetry_ebpf
            enabled: true
            # Already configured for RED metrics (RFD 025)

        -   type: custom_ebpf
            enabled: true
            config:
                # Track all TCP connections
                protocols: [ tcp ]
                # Sample rate (connections/sec per process)
                sampleRate: 100
                # Exclude noisy connections
                excludePorts: [ 22, 2049 ]  # SSH, NFS

        -   type: netstat_fallback
            enabled: true
            config:
                # Poll interval when eBPF unavailable
                pollInterval: 30s

    # Service name mapping
    serviceMapping:
        # Map ports to service names
        8080: api
        3000: frontend
        5432: postgres
        6379: redis

    # Report to colony
    reportInterval: 10s
    batchSize: 100  # batch connection events
```

## Implementation Plan

### Phase 1: Foundation

- [ ] Define protobuf messages for topology data (`TopologyConnection`,
  `TopologyGraph`)
- [ ] Create DuckDB schema for topology storage (`topology_connections`,
  `topology_services`)
- [ ] Define agent → colony streaming RPC for connection events
- [ ] Implement agent registry in colony (track agent IDs → mesh IPs mapping)

### Phase 2: OpenTelemetry eBPF Integration (Quickest Win)

- [ ] Extract topology from existing OpenTelemetry eBPF spans (RFD 025)
- [ ] Parse `net.peer.ip`, `net.peer.name`, `http.target` from spans
- [ ] Report HTTP/gRPC connections to colony
- [ ] Implement colony-side correlation for service-level topology
- [ ] Store topology in DuckDB
- [ ] Implement basic `coral topology` CLI command

### Phase 3: Custom eBPF Connection Tracking

- [ ] Implement eBPF program for TCP connection tracking (RFD 013 dependency)
- [ ] Hook `tcp_v4_connect`, `tcp_v6_connect`, `inet_csk_accept`
- [ ] Track connection state changes (ESTABLISHED, FIN_WAIT, CLOSE)
- [ ] Report non-HTTP connections to colony (databases, caches, message queues)
- [ ] Enhance topology graph with infrastructure-layer connections

### Phase 4: Fallback & Cross-Platform Support

- [ ] Implement netstat/ss fallback for macOS/Windows agents
- [ ] Periodic polling logic with configurable interval
- [ ] Parse netstat output to extract connections
- [ ] Ensure consistent connection event format across all sources

### Phase 5: Correlation & Intelligence

- [ ] Implement service name resolution (port mapping, DNS, K8s API)
- [ ] Add connection metadata (protocol, request rate, bytes transferred)
- [ ] Detect topology changes (new connections, broken paths)
- [ ] Implement graph queries (dependencies, reverse dependencies, paths)
- [ ] Add retention and cleanup for stale topology data

### Phase 6: Visualization & Integration

- [ ] Implement `coral topology` command with multiple output formats
- [ ] Add dashboard topology visualization (interactive graph)
- [ ] Expose topology via MCP for LLM integration (RFD 004, 030)
- [ ] Add topology context to `coral ask` queries
- [ ] Export topology as GraphViz DOT, JSON, or PNG

### Phase 7: Testing & Documentation

- [ ] Unit tests: connection parsing, correlation logic
- [ ] Integration tests: multi-agent topology discovery
- [ ] E2E tests: full topology workflow (agent → colony → CLI)
- [ ] Update USER-EXPERIENCE.md with actual implementation details
- [ ] Add topology troubleshooting guide

## API Changes

### New Protobuf Messages

```protobuf
syntax = "proto3";
package coral.mesh.v1;

import "google/protobuf/timestamp.proto";

// Connection event reported by agent
message TopologyConnection {
    string agent_id = 1;
    string service_name = 2;        // optional, if agent knows its service name

    string source_ip = 3;
    uint32 source_port = 4;
    string dest_ip = 5;
    uint32 dest_port = 6;

    Protocol protocol = 7;
    ConnectionState state = 8;

    google.protobuf.Timestamp timestamp = 9;

    // Metadata from different sources
    oneof metadata {
        HttpConnectionMetadata http = 10;
        TcpConnectionMetadata tcp = 11;
    }
}

enum Protocol {
    PROTOCOL_UNSPECIFIED = 0;
    PROTOCOL_TCP = 1;
    PROTOCOL_UDP = 2;
    PROTOCOL_HTTP = 3;
    PROTOCOL_GRPC = 4;
}

enum ConnectionState {
    CONNECTION_STATE_UNSPECIFIED = 0;
    CONNECTION_STATE_ESTABLISHED = 1;
    CONNECTION_STATE_CLOSED = 2;
    CONNECTION_STATE_LISTEN = 3;  // for inbound listeners
}

message HttpConnectionMetadata {
    string http_method = 1;
    string http_target = 2;
    int32 http_status = 3;
    uint64 request_count = 4;      // for aggregated connections
    double avg_latency_ms = 5;
}

message TcpConnectionMetadata {
    uint64 bytes_sent = 1;
    uint64 bytes_received = 2;
    uint64 retransmits = 3;
    uint64 rtt_us = 4;              // RTT in microseconds
}

// Request full topology graph from colony
message GetTopologyRequest {
    string service_name = 1;        // optional: filter by service
    google.protobuf.Timestamp since = 2;  // optional: time range
    int32 max_depth = 3;            // optional: limit graph depth
}

message GetTopologyResponse {
    repeated TopologyNode nodes = 1;
    repeated TopologyEdge edges = 2;
}

message TopologyNode {
    string service_name = 1;
    repeated string agent_ids = 2;  // agents running this service
    string service_type = 3;        // http, database, cache, queue, etc.
    map<string, string> labels = 4; // k8s labels, custom tags
}

message TopologyEdge {
    string source_service = 1;
    string dest_service = 2;
    Protocol protocol = 3;
    uint64 request_rate = 4;        // requests per minute
    double avg_latency_ms = 5;
    double error_rate = 6;          // percentage
    google.protobuf.Timestamp last_seen = 7;
}

// Streaming RPC for agents to report connections
message ReportConnectionsRequest {
    string agent_id = 1;
    repeated TopologyConnection connections = 2;
}

message ReportConnectionsResponse {
    uint32 accepted_count = 1;
}
```

### New RPC Endpoints

```protobuf
service ColonyService {
    // Existing RPCs...

    // Topology discovery
    rpc GetTopology(GetTopologyRequest) returns (GetTopologyResponse);
    rpc ReportConnections(stream ReportConnectionsRequest) returns (ReportConnectionsResponse);
}
```

### CLI Commands

```bash
# Display topology for current application
$ coral topology

SERVICE TOPOLOGY
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

frontend (10.100.0.7)
  → api (10.100.0.5)              [45 req/min, HTTP]
  → cdn.cloudflare.com            [static assets, HTTPS]

api (10.100.0.5)
  → worker (10.100.0.6)           [18 req/min, HTTP]
  → cache (10.100.0.9)            [156 ops/min, Redis]
  → db-proxy (10.100.0.8)         [42 queries/min, PostgreSQL]

worker (10.100.0.6)
  → db-proxy (10.100.0.8)         [12 queries/min, PostgreSQL]
  → queue (10.100.0.10)           [8 jobs/min, RabbitMQ]
  → s3.amazonaws.com              [3 uploads/min, HTTPS]

db-proxy (10.100.0.8)
  → postgres.internal.db          [54 queries/min, PostgreSQL]

queue (10.100.0.10)
  → redis.internal.cache          [persistent queue, Redis]

Detected Services: 7
External Dependencies: 3 (CDN, S3, internal DB)

# Filter by service
$ coral topology --service api

API DEPENDENCIES
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Upstream (calling api):
  frontend → api [45 req/min, HTTP]

Downstream (api calls):
  api → worker [18 req/min, HTTP]
  api → cache [156 ops/min, Redis]
  api → db-proxy [42 queries/min, PostgreSQL]

# Export topology as GraphViz DOT
$ coral topology --export topology.dot
✓ Topology exported to: topology.dot

# Convert to PNG (requires graphviz)
$ dot -Tpng topology.dot -o topology.png

# Export as JSON for external tools
$ coral topology --format json > topology.json

# Show topology at specific time
$ coral topology --time "2025-11-14 10:00:00"

# Watch topology in real-time
$ coral topology --watch

# Show dependencies for specific service
$ coral topology --upstream database
  # Shows all services that call database

$ coral topology --downstream frontend
  # Shows all services that frontend calls
```

### Configuration Changes

**Agent config** (`agent-config.yaml`):

- New `topology.enabled` flag
- `topology.sources` list (opentelemetry_ebpf, custom_ebpf, netstat_fallback)
- `topology.serviceMapping` for port → service name resolution
- `topology.reportInterval` for batching connection reports

**Colony config** (`colony-config.yaml`):

```yaml
storage:
    topology:
        # Retention for topology connections
        retention: 30d

        # How often to clean up stale connections
        staleness_threshold: 5m

        # Aggregate connection data older than this
        aggregate_after: 24h

ai:
    topology:
        # Include topology context in LLM queries
        enabled: true

        # Max graph depth to include in context
        max_depth: 3
```

## Testing Strategy

### Unit Tests

- Connection event parsing (eBPF output, netstat output, OTLP spans)
- Topology correlation logic (IP → agent → service mapping)
- Graph queries (dependencies, reverse dependencies, shortest path)
- Service name resolution (port mapping, DNS, K8s API)

### Integration Tests

- Multi-agent topology discovery (3+ agents in test environment)
- Connection lifecycle (new connection → established → closed)
- Topology changes over time (service added, service removed)
- Data source fallback (eBPF → netstat when eBPF unavailable)

### E2E Tests

- Full workflow: start agents → make HTTP requests → verify topology graph
- CLI commands (`coral topology`, filtering, export formats)
- Dashboard visualization (ensure graph renders correctly)
- MCP integration (LLM queries with topology context)

## Security Considerations

- **Connection visibility**: Agents can see all connections on their host (
  privileged operation). Ensure agents run with appropriate trust boundaries.
- **Data privacy**: Connection metadata may contain sensitive endpoints.
  Implement filtering to exclude internal IPs or specific ports.
- **Topology leakage**: Full topology graph reveals service architecture.
  Restrict access via RBAC or authentication.
- **Rate limiting**: Prevent agents from overwhelming colony with connection
  spam. Enforce batching and rate limits.
- **Audit logging**: Log topology queries and changes for compliance.

## Migration Strategy

**Deployment Steps**:

1. Deploy colony changes (new RPC handlers, DuckDB schema)
2. Deploy agent changes (topology observer)
3. Enable topology discovery via feature flag (opt-in initially)
4. Verify topology data appears in colony storage
5. Deploy CLI changes (`coral topology` command)
6. Enable dashboard topology view
7. Integrate with MCP for LLM queries

**Rollback Plan**:

- Disable topology discovery via feature flag
- No breaking changes to existing agent/colony communication
- Topology tables can remain in DuckDB (no migration required)

**Backward Compatibility**:

- Older agents without topology support: no-op (colony ignores missing topology
  data)
- CLI gracefully handles empty topology ("No topology data available")

## Future Enhancements

- **Service mesh integration**: Import topology from Istio, Linkerd, Consul
  service graphs
- **Cloud provider integration**: Augment topology with AWS VPC flow logs, GCP
  VPC logs
- **Anomaly detection**: Alert on unexpected connections (new service
  dependencies)
- **Topology-based routing**: Use topology for intelligent traffic routing or
  failover
- **Historical topology**: Compare topology over time ("how did topology change
  after deploy?")
- **External service discovery**: Resolve external IPs to service names (S3,
  CloudFlare, etc.)
- **Container-native topology**: Extract pod-to-pod communication from K8s
  network policies
- **Protocol detection**: Auto-detect protocols (HTTP, gRPC, Redis, PostgreSQL)
  from traffic patterns

## Appendix

### Data Source Comparison

| Data Source            | Coverage        | Overhead | Latency   | Cross-Platform | Service Names      |
|------------------------|-----------------|----------|-----------|----------------|--------------------|
| **OpenTelemetry eBPF** | HTTP/gRPC only  | 0.5-2%   | Real-time | Linux only     | Yes (via spans)    |
| **Custom eBPF**        | All TCP/UDP     | 0.1-0.5% | Real-time | Linux only     | No (needs mapping) |
| **netstat/ss**         | All connections | <0.1%    | 30s poll  | All platforms  | No (needs mapping) |

**Recommended approach**: Use all three in priority order:

1. OpenTelemetry eBPF for service-level topology (already implemented)
2. Custom eBPF for infrastructure connections (databases, caches)
3. netstat/ss fallback for non-Linux platforms

### DuckDB Storage Schema

```sql
-- Services in the topology
CREATE TABLE topology_services
(
    service_name VARCHAR PRIMARY KEY,
    service_type VARCHAR,               -- http, database, cache, queue, etc.
    agent_ids    VARCHAR[],             -- agents running this service
    labels       MAP(VARCHAR, VARCHAR), -- k8s labels, custom tags
    first_seen   TIMESTAMPTZ NOT NULL,
    last_seen    TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_topology_services_last_seen ON topology_services (last_seen DESC);

-- Connections between services
CREATE TABLE topology_connections
(
    id              BIGSERIAL PRIMARY KEY,
    timestamp       TIMESTAMPTZ NOT NULL,

    source_service  VARCHAR     NOT NULL,
    source_agent_id VARCHAR     NOT NULL,
    source_ip       VARCHAR     NOT NULL,
    source_port     INTEGER     NOT NULL,

    dest_service    VARCHAR,              -- nullable if unknown
    dest_agent_id   VARCHAR,              -- nullable if external
    dest_ip         VARCHAR     NOT NULL,
    dest_port       INTEGER     NOT NULL,

    protocol        VARCHAR     NOT NULL, -- tcp, udp, http, grpc, redis, etc.
    state           VARCHAR     NOT NULL, -- established, closed, listen

    -- Metadata (protocol-specific)
    http_method     VARCHAR,
    http_target     VARCHAR,
    http_status     SMALLINT,

    request_count   BIGINT,
    avg_latency_ms DOUBLE,
    bytes_sent      BIGINT,
    bytes_received  BIGINT,
    retransmits     BIGINT,

    -- Timestamps
    first_seen      TIMESTAMPTZ NOT NULL,
    last_seen       TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_topology_connections_timestamp ON topology_connections (timestamp DESC);
CREATE INDEX idx_topology_connections_source ON topology_connections (source_service, timestamp DESC);
CREATE INDEX idx_topology_connections_dest ON topology_connections (dest_service, timestamp DESC);
CREATE INDEX idx_topology_connections_pair ON topology_connections (source_service, dest_service, timestamp DESC);

-- Aggregated topology edges (for efficient queries)
CREATE TABLE topology_edges
(
    source_service VARCHAR     NOT NULL,
    dest_service   VARCHAR     NOT NULL,
    protocol       VARCHAR     NOT NULL,

    request_rate_per_min DOUBLE, -- requests per minute
    avg_latency_ms DOUBLE,
    error_rate DOUBLE,           -- percentage

    first_seen     TIMESTAMPTZ NOT NULL,
    last_seen      TIMESTAMPTZ NOT NULL,

    PRIMARY KEY (source_service, dest_service, protocol)
);

CREATE INDEX idx_topology_edges_last_seen ON topology_edges (last_seen DESC);
```

**Retention policy**: Raw connections retained for 30 days, aggregated edges
retained indefinitely (small data size).

### eBPF Connection Tracking Implementation

```c
// Pseudocode for eBPF connection tracking (RFD 013 dependency)

#include <linux/bpf.h>
#include <linux/tcp.h>
#include <linux/ip.h>

struct connection_event {
    u64 timestamp;
    u32 pid;
    u32 saddr;      // source IP (IPv4)
    u32 daddr;      // dest IP (IPv4)
    u16 sport;      // source port
    u16 dport;      // dest port
    u8 protocol;    // IPPROTO_TCP, IPPROTO_UDP
    u8 state;       // TCP_ESTABLISHED, TCP_FIN_WAIT, etc.
};

// BPF map to track active connections
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, struct sock *);
    __type(value, struct connection_event);
    __uint(max_entries, 10240);
} connection_map SEC(".maps");

// BPF perf event array to send events to userspace
struct {
    __uint(type, BPF_MAP_TYPE_PERF_EVENT_ARRAY);
} connection_events SEC(".maps");

// Attach to tcp_v4_connect (outbound IPv4 connections)
SEC("kprobe/tcp_v4_connect")
int trace_tcp_connect(struct pt_regs *ctx) {
    struct sock *sk = (struct sock *)PT_REGS_PARM1(ctx);
    struct connection_event event = {};

    // Extract connection details from socket
    event.timestamp = bpf_ktime_get_ns();
    event.pid = bpf_get_current_pid_tgid() >> 32;

    // Read socket addresses (simplified)
    bpf_probe_read(&event.saddr, sizeof(event.saddr), &sk->__sk_common.skc_rcv_saddr);
    bpf_probe_read(&event.daddr, sizeof(event.daddr), &sk->__sk_common.skc_daddr);
    bpf_probe_read(&event.sport, sizeof(event.sport), &sk->__sk_common.skc_num);
    bpf_probe_read(&event.dport, sizeof(event.dport), &sk->__sk_common.skc_dport);

    event.protocol = IPPROTO_TCP;
    event.state = TCP_SYN_SENT;

    // Send event to userspace
    bpf_perf_event_output(ctx, &connection_events, BPF_F_CURRENT_CPU, &event, sizeof(event));

    return 0;
}

// Attach to inet_csk_accept (inbound connections)
SEC("kretprobe/inet_csk_accept")
int trace_tcp_accept(struct pt_regs *ctx) {
    struct sock *sk = (struct sock *)PT_REGS_RC(ctx);
    // Similar to above, but for inbound connections
    // ...
    return 0;
}

// Attach to inet_sock_set_state (connection state changes)
SEC("tracepoint/sock/inet_sock_set_state")
int trace_state_change(struct trace_event_raw_inet_sock_set_state *ctx) {
    // Track state changes: ESTABLISHED, FIN_WAIT, CLOSE
    // ...
    return 0;
}
```

**Userspace agent integration**:

- Load BPF program using libbpf
- Read events from perf event array
- Aggregate connections (avoid sending every packet)
- Report to colony via gRPC stream

### Example MCP Tool Integration

```json
{
    "name": "coral_get_topology",
    "description": "Get service topology graph showing dependencies between services",
    "inputSchema": {
        "type": "object",
        "properties": {
            "service_name": {
                "type": "string",
                "description": "Optional: filter topology to this service and its dependencies"
            },
            "max_depth": {
                "type": "integer",
                "description": "Maximum graph depth (default: 3)"
            }
        }
    }
}
```

**LLM query example**:

```
User: "What services depend on the database?"

Claude: [Uses coral_get_topology tool]
Based on your topology:

Services that depend on database (postgres.internal.db):
- api (42 queries/min)
- worker (12 queries/min)

Both services connect through db-proxy (10.100.0.8) as an intermediary.

If the database goes down, this will impact:
- API service (potential 500 errors on /checkout, /orders endpoints)
- Worker service (background job processing will fail)
- Frontend (indirectly, via API failures)
```

### Topology Correlation Algorithm

```
Input: Connection events from all agents
Output: Service topology graph

For each connection event:
  1. Lookup source agent ID → source service name
  2. Lookup dest IP → dest agent ID → dest service name
     - If dest IP is external (not in agent registry): mark as external service
  3. Create edge: (source_service → dest_service, protocol, metadata)
  4. Update request_rate, avg_latency from metadata
  5. Store in topology_edges table

Periodically (every 5m):
  1. Clean up stale connections (last_seen > 5m ago)
  2. Recompute aggregated edge metrics
  3. Detect topology changes (new edges, removed edges)
  4. Notify subscribers (dashboard, AI, alerts)
```

### Service Name Resolution Strategy

**Priority order** (first match wins):

1. **Agent metadata**: Agent reports its service name during registration
2. **K8s labels**: For node agents, extract pod labels via K8s API (
   `app=frontend`)
3. **Port mapping**: Use configured port → service mapping (`8080 → api`)
4. **DNS reverse lookup**: Query DNS for dest IP (if available)
5. **Unknown**: Label as `unknown-<ip>:<port>`

**Configuration example**:

```yaml
topology:
    serviceMapping:
        # Static port mappings
        8080: api
        3000: frontend
        5432: postgres
        6379: redis

    # DNS resolution
    enableDnsLookup: true

    # Kubernetes integration (for node agents)
    kubernetes:
        enabled: true
        labelSelector: "app"  # use "app" label as service name
```

---

## Related RFDs

- **RFD 013**: eBPF-Based Application Introspection (dependency for custom
  connection tracking)
- **RFD 025**: Basic OTLP Ingestion (dependency for OpenTelemetry eBPF spans)
- **RFD 004**: MCP Server Integration (topology exposure to LLMs)
- **RFD 030**: Coral Ask with Local Genkit (AI queries using topology context)
- **RFD 014**: Colony LLM Integration (topology as context for AI insights)
