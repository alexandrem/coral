---
rfd: "006"
title: "Colony RPC Handler Implementation"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ ]
database_migrations: [ ]
areas: [ "colony", "rpc", "observability" ]
---

# RFD 006 - Colony RPC Handler Implementation

**Status:** 🎉 Implemented

## Summary

Implement the three core Colony RPC handlers (`GetStatus`, `ListAgents`,
`GetTopology`) that are currently defined in protobuf but unimplemented. This
enables CLI commands, dashboard integration, and provides essential
observability into colony operations by tracking connected agents, operational
metrics, and service topology.

## Problem

**Current behavior/limitations:**

- The colony service has three gRPC endpoints defined in
  `proto/coral/colony/v1/colony.proto` but only unimplemented stub handlers
  exist.
- No infrastructure to track connected agents or their status.
- CLI commands (`coral colony status`, `coral colony list`) output hardcoded
  placeholder data marked with TODOs.
- Dashboard cannot query real colony state or display agent information.
- No visibility into colony health, uptime, or connected components.

**Why this matters:**

- Users cannot monitor colony health or see which agents are connected.
- Debugging connection issues is difficult without visibility into the mesh.
- CLI commands are effectively non-functional for operational tasks.
- Dashboard cannot provide real-time operational insights.
- No way to verify agent registration or troubleshoot mesh connectivity.

**Use cases affected:**

- Operators checking colony health: "Is my colony running? How many agents are
  connected?"
- Developers debugging agent connections: "Did my agent successfully register?"
- Dashboard users wanting real-time visibility into distributed app topology.
- Troubleshooting degraded components or connection issues.

## Solution

Create a colony server package that implements the `ColonyServiceHandler`
interface with agent registry infrastructure to track connected agents and
provide operational metrics.

**Key Design Decisions:**

- **In-memory agent registry**: Fast lookups, similar to discovery service
  registry pattern.
    - Thread-safe with mutex protection.
    - Tracks agent metadata (ID, component name, mesh IPs, last seen).
    - Status determination based on last_seen timestamps.
    - No persistence needed (agents re-register on colony restart).

- **Status-based health model**: Derive colony status from agent health.
    - `healthy`: All agents seen recently (< 30s).
    - `degraded`: Some agents delayed (30s-2min).
    - `unhealthy`: No agents or critical timeouts (> 2min).

- **Follow discovery service patterns**: Use existing codebase patterns from
  `internal/discovery/server`.
    - Similar server structure with registry, config, logger.
    - Connect RPC framework integration.
    - Consistent error handling and validation.

- **Deferred topology discovery**: Return empty connections list initially.
    - Service topology auto-discovery is complex (network analysis or SDK
      instrumentation).
    - Focus on core agent tracking first.
    - Topology connections can be populated in future enhancement.

**Benefits:**

- Enables functional CLI commands for operational visibility.
- Provides foundation for dashboard real-time agent monitoring.
- Establishes agent registry pattern that other components can leverage.
- Enables troubleshooting and debugging of mesh connectivity.
- Low complexity (in-memory, no database changes).

**Architecture Overview:**

```
┌──────────────────────────────────────────────────────┐
│ Colony RPC Server (internal/colony/server)          │
│                                                      │
│  ┌─────────────┐  ┌──────────────┐  ┌─────────────┐│
│  │ GetStatus   │  │ ListAgents   │  │ GetTopology ││
│  │ Handler     │  │ Handler      │  │ Handler     ││
│  └──────┬──────┘  └──────┬───────┘  └──────┬──────┘│
│         │                │                  │        │
│         └────────────────┼──────────────────┘        │
│                          ▼                           │
│              ┌────────────────────────┐              │
│              │  Agent Registry        │              │
│              │  (in-memory, mutex)    │              │
│              └────────────────────────┘              │
└──────────────────────────────────────────────────────┘
                         ▲
                         │ Register/Heartbeat
                         │
              ┌──────────┴──────────┐
              │  MeshService        │
              │  (auth.proto)       │
              └─────────────────────┘
```

### Component Changes

1. **Colony Server** (new):
    - Implements `ColonyServiceHandler` interface from generated Connect code.
    - Tracks colony start time for uptime calculations.
    - Queries agent registry for connected agents.
    - Determines colony status based on agent health.
    - Calculates storage usage from DuckDB path.

2. **Agent Registry** (new):
    - Thread-safe in-memory map of agent entries.
    - Stores agent metadata: ID, component name, mesh IPs, registration time,
      last seen.
    - Provides methods: Register, UpdateHeartbeat, Get, ListAll, CountActive.
    - Status determination logic based on last_seen timestamps.

3. **Storage Monitor** (new):
    - Calculates total size of files in colony storage path.
    - Handles missing or empty directories gracefully.

4. **CLI Commands** (updated):
    - Replace hardcoded status output with RPC calls to colony.
    - Add new `agents` subcommand to list connected agents (note: `list`
      already exists for listing colonies).
    - Add proper error handling and formatting.

**Configuration Example:**

```yaml
# Colony config (~/.coral/colonies/my-colony.yaml)
colony_id: my-app-prod
application_name: MyApp
environment: production

rpc:
    port: 9090  # Colony RPC server

dashboard:
    port: 3000

storage_path: ~/.coral/storage/my-app-prod
```

## Implementation Plan

### Phase 1: Core Infrastructure

- [ ] Create `internal/colony/registry/registry.go` with Registry struct.
- [ ] Implement thread-safe agent registration and tracking.
- [ ] Add status determination logic (healthy/degraded/unhealthy).
- [ ] Create `internal/colony/storage/monitor.go` for storage size calculation.
- [ ] Write unit tests for registry and monitor packages.

### Phase 2: RPC Server & Handlers

- [ ] Create `internal/colony/server/server.go` with Server struct.
- [ ] Implement `GetStatus` handler with status calculation.
- [ ] Implement `ListAgents` handler querying registry.
- [ ] Implement `GetTopology` handler (empty connections list initially).
- [ ] Write unit tests for all three handlers.

### Phase 3: Integration

- [ ] Wire up RPC server to colony startup code.
- [ ] Hook registry into agent registration flow (`MeshService.Register`).
- [ ] Add mesh IP assignment logic during agent registration.
- [ ] Update CLI status command to query RPC endpoint.
- [ ] Add new CLI agents command to query ListAgents RPC.
- [ ] Test end-to-end: agent registers → appears in CLI.

### Phase 4: Testing & Documentation

- [ ] Add integration tests for full registration flow.
- [ ] Test status calculation with multiple agents.
- [ ] Test degraded/unhealthy scenarios.
- [ ] Update `docs/IMPLEMENTATION.md` with registry details.
- [ ] Add code comments following Go Doc Comments style.
- [ ] Validate all tests pass (`make test`).

## API Changes

### Existing Protobuf Definitions

**File: `proto/coral/colony/v1/colony.proto`**

The service and messages are already defined:

```protobuf
service ColonyService {
    rpc GetStatus(GetStatusRequest) returns (GetStatusResponse);
    rpc ListAgents(ListAgentsRequest) returns (ListAgentsResponse);
    rpc GetTopology(GetTopologyRequest) returns (GetTopologyResponse);
}

message GetStatusRequest {}

message GetStatusResponse {
    string colony_id = 1;
    string app_name = 2;
    string environment = 3;
    string status = 4;  // "running", "degraded", "unhealthy"
    google.protobuf.Timestamp started_at = 5;
    int64 uptime_seconds = 6;
    int32 agent_count = 7;
    string dashboard_url = 8;
    int64 storage_bytes = 9;
}

message ListAgentsRequest {}

message ListAgentsResponse {
    repeated Agent agents = 1;
}

message Agent {
    string agent_id = 1;
    string component_name = 2;
    string mesh_ipv4 = 3;
    string mesh_ipv6 = 4;
    google.protobuf.Timestamp last_seen = 5;
    string status = 6;  // "healthy", "degraded", "unhealthy"
}

message GetTopologyRequest {}

message GetTopologyResponse {
    string colony_id = 1;
    repeated Agent agents = 2;
    repeated Connection connections = 3;
}

message Connection {
    string source_id = 1;
    string target_id = 2;
    string connection_type = 3;  // "http", "grpc", "database", etc.
}
```

**No protobuf changes required** - only implementing the handlers.

### CLI Commands

**Select colony (prerequisite):**

```bash
# Set the active colony
coral colony use my-app-prod

# Check current colony
coral colony current
```

**Status command:**

```bash
coral colony status

# Example output:
Colony: my-app-prod (running)
App: MyApp [production]
Uptime: 2h 15m 30s
Agents: 3 connected
Storage: 45.2 MB
Dashboard: http://localhost:3000
```

**List agents command:**

```bash
coral colony agents

# Example output:
AGENT ID          COMPONENT    MESH IP          STATUS    LAST SEEN
frontend-001      frontend     10.42.0.2        healthy   2s ago
api-001           api          10.42.0.3        healthy   1s ago
worker-001        worker       10.42.0.4        degraded  45s ago

# Note: 'coral colony list' already exists and lists configured colonies
```

**Topology command (future):**

```bash
coral colony topology

# Example output:
Colony: my-app-prod (3 agents, 2 connections)

Agents:
  frontend-001 (frontend) - healthy
  api-001 (api) - healthy
  worker-001 (worker) - degraded

Connections:
  frontend-001 → api-001 (http)
  api-001 → database (database)
```

### Configuration Changes

No new configuration fields required. Existing colony configuration already
includes:

- `colony_id` - Used in status response.
- `application_name` - Used in status response.
- `environment` - Used in status response.
- `dashboard.port` - Used to construct dashboard URL.
- `storage_path` - Used to calculate storage bytes.

## Testing Strategy

### Unit Tests

**Registry Tests (`internal/colony/registry/registry_test.go`):**

- Agent registration with unique IDs.
- Duplicate registration handling (update existing entry).
- Heartbeat updates (UpdateHeartbeat).
- Status determination logic (healthy → degraded → unhealthy transitions).
- Concurrent access with race detector.
- CountActive with multiple agents.

**Server Tests (`internal/colony/server/server_test.go`):**

- GetStatus with zero agents (unhealthy status).
- GetStatus with all healthy agents (running status).
- GetStatus with degraded agents (degraded status).
- ListAgents with empty registry.
- ListAgents with multiple agents.
- GetTopology response structure (empty connections).

**Monitor Tests (`internal/colony/storage/monitor_test.go`):**

- Storage size calculation with real files.
- Handling of missing storage directory.
- Handling of empty storage directory.

### Integration Tests

**End-to-End Flow:**

- Start colony server.
- Simulate agent registration via MeshService.
- Call GetStatus and verify response includes agent count.
- Call ListAgents and verify agent appears.
- Simulate agent timeout (no heartbeat).
- Call GetStatus and verify status changes to unhealthy.

## Security Considerations

**RPC Authentication:**

- Colony RPC endpoints run on control plane (over WireGuard mesh).
- Authentication handled by WireGuard tunnel security.
- No additional auth required for Phase 1.
- Future: Add API tokens for dashboard/CLI access over untrusted networks.

**Input Validation:**

- Validate agent IDs and component names during registration.
- Prevent injection attacks via metadata fields.
- Sanitize mesh IP addresses.

**Rate Limiting:**

- Protect registry from DoS via excessive heartbeats.
- Limit registration requests per agent ID.

## Future Enhancements

**Service Topology Auto-Discovery:**

- Parse network traffic to populate connections list.
- Integrate with SDK to report service dependencies.
- Visualize service call graphs in dashboard.

**DuckDB Integration:**

- Store agent metadata history in DuckDB.
- Query historical agent connections and uptime.
- Persist registry across colony restarts.

**Agent Health Checks:**

- Active health probes beyond last_seen timestamps.
- Component-specific health endpoints.
- Custom health check definitions.

**Dashboard Real-Time Updates:**

- WebSocket connection for live agent status updates.
- Real-time topology graph rendering.
- Agent connection/disconnection notifications.

**AI Health Analysis:**

- Correlate agent health with metrics for anomaly detection.
- Predict agent failures based on patterns.
- Recommend remediation actions.

## Appendix

### Agent Status Logic

**Determination based on last_seen timestamp:**

```
healthy:    last_seen < 30 seconds ago
degraded:   last_seen between 30 seconds and 2 minutes ago
unhealthy:  last_seen > 2 minutes ago
```

**Colony status derived from agent health:**

- **running**: All agents are healthy.
- **degraded**: At least one agent is degraded, none unhealthy.
- **unhealthy**: At least one agent is unhealthy, or no agents connected.

### Integration with Agent Registration

Agents register via `MeshService.Register` (defined in
`proto/coral/mesh/v1/auth.proto`).

**Registration flow:**

1. Agent sends Register request with: agent_id, component_name, colony_id,
   colony_secret, wireguard_pubkey.
2. Colony authenticates agent (verifies colony_id and colony_secret match).
3. Colony assigns mesh IP addresses (IPv4 and IPv6 from pool).
4. Colony registers agent in registry:
   `registry.Register(agentID, componentName, meshIPv4, meshIPv6)`.
5. Colony returns mesh configuration to agent (IP addresses, colony pubkey,
   endpoints).

**Heartbeat mechanism:**

- Agents periodically call Register again (acts as keepalive).
- Registry updates last_seen timestamp via `UpdateHeartbeat(agentID)`.
- Alternative: Add dedicated Heartbeat RPC (future enhancement).

### Reference Implementations

**Discovery Service Patterns:**

- Server structure: `internal/discovery/server/server.go`
- Registry pattern: `internal/discovery/registry/registry.go`
- Connect RPC integration and error handling.

**File Structure:**

```
internal/colony/
├── registry/
│   ├── registry.go       # Agent registry implementation
│   └── registry_test.go
├── storage/
│   ├── monitor.go        # Storage size monitoring
│   └── monitor_test.go
└── server/
    ├── server.go         # RPC handler implementations
    └── server_test.go
```

---

## Notes

**Design Philosophy:**

- Start simple: In-memory registry, no persistence.
- Follow existing patterns: Model after discovery service.
- Fail gracefully: Handle missing agents, timeout scenarios.
- Enable iteration: Foundation for future enhancements (DuckDB, topology, AI).

**Why In-Memory Registry:**

- Fast lookups (O(1) agent retrieval).
- No database dependency for Phase 1.
- Sufficient for operational visibility (agents re-register on restart).
- Can add persistence layer later without API changes.

**Why Deferred Topology:**

- Service topology auto-discovery is complex (separate RFD recommended).
- Options: Network traffic analysis, SDK instrumentation, manual configuration.
- Returning empty connections list allows API to remain unchanged.
- Dashboard can show agents immediately, connections later.
