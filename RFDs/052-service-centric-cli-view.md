---
rfd: "052"
title: "Service-Centric CLI View"
state: "implemented"
breaking_changes: true
testing_required: true
database_changes: false
api_changes: true
dependencies: ["011", "044"]
related_rfds: ["006"]
database_migrations: []
areas: ["cli", "colony", "ux", "protobuf"]
---

# RFD 052 - Service-Centric CLI View

**Status:** ✅ Implemented

## Summary

Introduce a `coral service list` command that provides a service-centric view of
the colony, displaying all services as primary entities with their associated
agents. This inverts the existing `coral colony agents` perspective, enabling
operators to quickly answer questions like "which agents run Redis?" or "how
many instances of the API service are deployed?" without manually parsing
agent-by-agent output.

**Breaking Change**: This RFD includes renaming `ServiceInfo.component_name` to
`ServiceInfo.name` in the protobuf schema. This simplifies the field naming
and requires updating all agent implementations. No backward compatibility is
maintained.

## Problem

**Current behavior/limitations:**

- **Agent-centric view only**: `coral colony agents` shows agents first, then
  their services. To find all instances of a specific service (e.g., "redis"),
  users must scan through all agent entries and manually collect service
  information.

- **No service inventory**: No quick way to list all unique services running in
  the colony or get a count of service instances.

- **Poor service discovery UX**: Questions like "how many frontend instances are
  running?" or "which agents provide the payments-api service?" require manual
  inspection of agent output or custom JSON parsing with `--json` flag.

- **Inconsistent mental model**: Operators often think in terms of services
  first ("I need to check the health of all Redis instances"), but the CLI
  forces agent-first navigation.

**Why this matters:**

- **Service-oriented operations**: In distributed systems, operators frequently
  need service-level visibility. "Show me all instances of the payments service"
  is a more natural query than "show me all agents and their services, then
  filter mentally."

- **Multi-agent services**: With replicated deployments (e.g., 5 replicas of an
  API service across different nodes), tracking which agents serve a particular
  service becomes tedious without service-first views.

- **Capacity planning**: Understanding service distribution ("how many agents
  run the cache service?") helps with scaling decisions and resource allocation.

- **AI operator workflows**: When Claude asks "what services are available?" or
  "which agents run PostgreSQL?", the current agent-centric view requires
  additional parsing and aggregation.

**Use cases affected:**

1. **Service inventory**: "List all services in the colony" - currently requires
   parsing all agents and deduplicating service names.

2. **Instance enumeration**: "How many instances of the frontend service are
   running?" - requires counting agents that list "frontend" in their services.

3. **Agent targeting**: "I need to debug one of the Redis instances, which
   agents have it?" - requires scanning all agent entries for Redis service.

4. **Health monitoring**: "Show me all unhealthy instances of the api service" -
   requires filtering agents by service name, then checking health status.

5. **MCP tool integration**: AI operators using MCP tools need to discover which
   agents provide specific services for targeted operations (RFD 044).

## Solution

Add a `coral service list` command that aggregates services across all agents in
the colony registry and displays them in a service-first format. Each service
entry shows the service name, type, and a list of agents running that service,
including per-agent health status and mesh IP.

**Key Design Decisions:**

- **Service as primary entity**: Output groups by unique service name first,
  then lists agents as children. This mirrors how operators mentally model
  distributed services.

  queries the existing colony agent registry (RFD 006, RFD 011) and aggregates
  `ServiceInfo` entries across all agents. To ensure data freshness, the
  `ListAgents` RPC performs a real-time, synchronous fan-out query to all
  connected agents to fetch their latest service status. This ensures that
  services added dynamically via `coral connect` are immediately visible.

- **Service uniqueness by name**: Services are identified by `name`
  field from `ServiceInfo`. Multiple agents with the same service name are
  grouped together. Service names are case-insensitive for grouping purposes
  (normalized to lowercase internally), but the original casing from the first
  occurrence is preserved for display. Services are uniquely identified by the
  tuple `(name, service_type)` to handle cases where different
  service types share the same name.

- **Per-agent service details**: For each agent running a service, show:
    - Agent ID (for targeting with RFD 044)
    - Health status (healthy/degraded/unhealthy)
    - Mesh IP (for direct connectivity scenarios)
    - Service-specific port (from `ServiceInfo`)
    - Health endpoint (if configured)

- **Status determination logic**: Agent health status is determined using the
  following priority order:
    1. If the service has a `health_endpoint` configured, use HTTP check result
    2. If the agent reports a status field, use that value
    3. Otherwise, compute from `last_seen` timestamp:
       - `< 30s`: healthy
       - `30s - 2m`: degraded
       - `> 2m`: unhealthy
    - Color coding: ✓ (green) for healthy, ⚠️ (yellow) for degraded, ✗ (red)
      for unhealthy. In non-color terminals, plain symbols are used.

- **Filtering support**: Add `--service` flag to filter output to a specific
  service name, enabling queries like `coral service list --service redis`.
  Filtering uses case-insensitive exact matching. If the specified service
  name does not exist, the command displays an error message: "Service
  '<name>' not found in colony. Use 'coral service list' to see all services."
  Future enhancements may add pattern matching (wildcards, regex).

- **Consistent output modes**: Support both human-readable table format (
  default) and JSON output (`--json`) for programmatic access, matching UX
  patterns from `coral colony agents`.

- **Data consistency and snapshots**: Since aggregation happens client-side,
  the data represents a snapshot at the time of the `ListAgents` RPC call.
  Agents may join/leave or service states may change during processing. To
  provide clarity, all output includes a snapshot timestamp showing when the
  data was collected. The timestamp is displayed in both human-readable and
  JSON formats. For operations taking longer than 500ms, a progress spinner
  is shown to indicate the command is working.

**Benefits:**

- ✅ Service-first mental model aligns with operator workflows
- ✅ Instant service inventory and instance counts
- ✅ Quick agent discovery for service-based targeting
- ✅ Better UX for AI operators and MCP tool integration
- ✅ No new storage or protocol changes - pure aggregation layer
- ✅ Complements existing `coral colony agents` without replacing it

## Tradeoffs

The decision to use synchronous, real-time querying of agents involves the
following tradeoffs:

- **Latency**: The command's execution time is bound by the network latency to
  the agents. To mitigate this, a short timeout (500ms) is applied to the
  fan-out queries. If an agent is slow, the command proceeds with the cached
  registry data for that agent.
- **Availability Dependency**: The accuracy of the "real-time" view depends on
  the agents being online and reachable. Unreachable agents will fall back to
  the last known state stored in the registry.
- **Scalability**: For very large colonies (thousands of agents), a synchronous
  fan-out query might become a bottleneck or trigger rate limits.

## Future Enhancements

To address the tradeoffs and improve scalability in the future, we may consider:

- **Event-Driven Updates**: Instead of polling, agents could push service
  changes to the colony via a new RPC or event stream. This would allow the
  colony to maintain an up-to-date cache without polling.
- **Server-Side Caching**: The colony could cache the results of the `ListServices`
  calls with a short TTL (e.g., 5-10 seconds) to reduce the load on agents
  during frequent CLI usage.
- **Pagination**: For large agent counts, the `ListAgents` RPC could support
  pagination, allowing the CLI to fetch and display results in chunks.

**Architecture Overview:**

```
┌────────────────────────────────────────────────────────────┐
│  Colony Registry (internal/colony/registry)                │
│                                                            │
│  Agents:                                                   │
│    agent-1:                                                │
│      - frontend:3000:/health                               │
│      - redis:6379                                          │
│    agent-2:                                                │
│      - frontend:3000:/health                               │
│      - metrics:9090:/metrics                               │
│    agent-3:                                                │
│      - redis:6379                                          │
│      - postgres:5432                                       │
└────────────────┬───────────────────────────────────────────┘
                 │
                 │ ListAgents RPC
                 ▼
┌────────────────────────────────────────────────────────────┐
│  coral service list (CLI aggregation layer)                │
│                                                            │
│  Algorithm:                                                │
│    1. Fetch all agents from colony registry                │
│    2. Extract all ServiceInfo entries                      │
│    3. Group by service name                                │
│    4. For each unique service:                             │
│         - Collect all agents running that service          │
│         - Include agent health, mesh IP, port, endpoint    │
│    5. Format output (table or JSON)                        │
└────────────────┬───────────────────────────────────────────┘
                 │
                 ▼
┌────────────────────────────────────────────────────────────┐
│  Output: Service-Centric View                              │
│                                                            │
│  Services (5) at 2025-11-19 12:34:56 UTC:                  │
│                                                            │
│  SERVICE      INSTANCES  AGENTS                            │
│  ──────────────────────────────────────────────────────────│
│  frontend     2          agent-1 (10.42.0.10, ✓ healthy)   │
│                          agent-2 (10.42.0.11, ✓ healthy)   │
│  redis        2          agent-1 (10.42.0.10, ✓ healthy)   │
│                          agent-3 (10.42.0.12, ⚠️ degraded) │
│  metrics      1          agent-2 (10.42.0.11, ✓ healthy)   │
│  postgres     1          agent-3 (10.42.0.12, ⚠️ degraded) │
└────────────────────────────────────────────────────────────┘
```

### Component Changes

1. **CLI** (`internal/cli/colony/colony.go` or new file `internal/cli/colony/service.go`):
    - Add `newServiceCmd()` function that creates `coral service` subcommand
    - Add `newServiceListCmd()` for the `list` subcommand
    - Implement service aggregation logic from agent registry
    - Support `--service <name>` flag for filtering
    - Support `--json` flag for JSON output
    - Format service-first table output with instance counts

2. **Protobuf** (breaking change):
    - Reuse existing `ListAgentsRequest/Response` from RFD 006
    - **BREAKING**: Rename `ServiceInfo.component_name` to `ServiceInfo.name` in RFD 011
    - This is a breaking change to the protobuf schema that will require:
      - Updating the `ServiceInfo` message definition
      - Regenerating protobuf code (`buf generate`)
      - Updating all agent implementations to use the new field name
      - No backward compatibility maintained (clean break)
    - No new RPC endpoints needed - pure client-side aggregation

3. **Colony Registry** (no changes required):
    - Existing `ListAgents` RPC provides all needed data
    - Services already tracked in `Agent.services` field (RFD 011)

**Configuration Example:**

No new configuration required. The command uses existing colony connectivity:

```bash
# List all services (human-readable table)
coral service list

# List all services (JSON output)
coral service list --json

# Filter to specific service
coral service list --service redis

# Specify colony (uses existing --colony flag pattern)
coral service list --colony my-production-colony
```

## API Changes

### CLI Commands

**New command structure:**

```bash
# Primary command: list all services
coral service list [--service NAME] [--json] [--colony ID]

# Flags:
#   --service <name>  Filter to show only specified service
#   --json            Output JSON format
#   --colony <id>     Target specific colony (inherited pattern)
#   -v, --verbose     Show detailed per-service information
```

**Example: Default table output**

```bash
$ coral service list

Services (5) at 2025-11-19 12:34:56 UTC:

SERVICE          INSTANCES  AGENTS
────────────────────────────────────────────────────────────────────
frontend         2          agent-1-frontend (10.42.0.10, ✓ healthy)
                            agent-2-frontend (10.42.0.11, ✓ healthy)

redis            2          agent-1-cache (10.42.0.10, ✓ healthy)
                            agent-3-db (10.42.0.12, ⚠️ degraded)

api              3          agent-4-api (10.42.0.13, ✓ healthy)
                            agent-5-api (10.42.0.14, ✓ healthy)
                            agent-6-api (10.42.0.15, ✗ unhealthy)

postgres         1          agent-3-db (10.42.0.12, ⚠️ degraded)

metrics          1          agent-2-frontend (10.42.0.11, ✓ healthy)
```

**Example: Filtered by service**

```bash
$ coral service list --service redis

Service: redis (2 instances) at 2025-11-19 12:34:56 UTC:

AGENT ID            MESH IP        PORT   HEALTH    STATUS
────────────────────────────────────────────────────────────
agent-1-cache       10.42.0.10     6379   -         ✓ healthy
agent-3-db          10.42.0.12     6379   -         ⚠️ degraded
```

**Example: Verbose output**

```bash
$ coral service list --service redis -v

Service: redis at 2025-11-19 12:34:56 UTC:
  Type: redis
  Instances: 2

  Agent: agent-1-cache
    Mesh IP: 10.42.0.10
    Status: ✓ healthy
    Port: 6379
    Last Seen: 5s ago

  Agent: agent-3-db
    Mesh IP: 10.42.0.12
    Status: ⚠️ degraded
    Port: 6379
    Last Seen: 1m ago
```

**Example: Service not found error**

```bash
$ coral service list --service mysql

Error: Service 'mysql' not found in colony

Available services (5):
  • api
  • frontend
  • metrics
  • postgres
  • redis

Use 'coral service list' to see all services with their agents.
```

**Example: JSON output**

```bash
$ coral service list --json
```

```json
{
  "version": "1.0",
  "snapshot_time": "2025-11-19T12:34:56Z",
  "total_services": 2,
  "total_instances": 4,
  "services": [
    {
      "service_name": "frontend",
      "service_type": "http",
      "instance_count": 2,
      "agents": [
        {
          "agent_id": "agent-1-frontend",
          "mesh_ipv4": "10.42.0.10",
          "status": "healthy",
          "port": 3000,
          "health_endpoint": "/health",
          "last_seen": "2025-11-19T12:34:56Z"
        },
        {
          "agent_id": "agent-2-frontend",
          "mesh_ipv4": "10.42.0.11",
          "status": "healthy",
          "port": 3000,
          "health_endpoint": "/health",
          "last_seen": "2025-11-19T12:34:58Z"
        }
      ]
    },
    {
      "service_name": "redis",
      "service_type": "redis",
      "instance_count": 2,
      "agents": [
        {
          "agent_id": "agent-1-cache",
          "mesh_ipv4": "10.42.0.10",
          "status": "healthy",
          "port": 6379,
          "health_endpoint": "",
          "last_seen": "2025-11-19T12:34:55Z"
        },
        {
          "agent_id": "agent-3-db",
          "mesh_ipv4": "10.42.0.12",
          "status": "degraded",
          "port": 6379,
          "health_endpoint": "",
          "last_seen": "2025-11-19T12:33:30Z"
        }
      ]
    }
  ]
}
```

### RPC Usage

The command reuses existing `ListAgents` RPC from RFD 006:

```go
// Client-side aggregation logic (pseudo-code)
func listServices(ctx context.Context, client colonyv1connect.ColonyServiceClient) ([]ServiceView, error) {
    // Fetch all agents
    resp, err := client.ListAgents(ctx, connect.NewRequest(&colonyv1.ListAgentsRequest{}))
    if err != nil {
        return nil, err
    }

    // Aggregate services
    serviceMap := make(map[string]*ServiceView)
    for _, agent := range resp.Msg.Agents {
        for _, service := range agent.Services {
            if _, exists := serviceMap[service.Name]; !exists {
                serviceMap[service.Name] = &ServiceView{
                    ServiceName: service.Name,
                    ServiceType: service.ServiceType,
                    Agents:      []AgentInstance{},
                }
            }

            serviceMap[service.Name].Agents = append(
                serviceMap[service.Name].Agents,
                AgentInstance{
                    AgentID:         agent.AgentId,
                    MeshIPv4:        agent.MeshIpv4,
                    Status:          agent.Status,
                    Port:            service.Port,
                    HealthEndpoint:  service.HealthEndpoint,
                    LastSeen:        agent.LastSeen,
                },
            )
        }
    }

    // Convert map to sorted list
    services := make([]ServiceView, 0, len(serviceMap))
    for _, svc := range serviceMap {
        svc.InstanceCount = len(svc.Agents)
        services = append(services, *svc)
    }

    // Sort by service name
    sort.Slice(services, func(i, j int) bool {
        return services[i].ServiceName < services[j].ServiceName
    })

    return services, nil
}
```

## Implementation Plan

### Phase 1: Core Command Structure

- [x] Create `internal/cli/colony/service.go` with `newServiceCmd()` function
- [x] Add `newServiceListCmd()` for the `list` subcommand
- [x] Integrate into `NewColonyCmd()` in `internal/cli/colony/colony.go`
- [x] Add basic RPC client setup (reuse existing colony connection patterns)
- [x] Add `--service`, `--json`, `--verbose` flags

### Phase 2: Protobuf Schema Update (Breaking Change)

- [x] Update `ServiceInfo` message in protobuf definition
    - Rename `component_name` field to `name`
    - Update field documentation
- [x] Regenerate protobuf code with `buf generate`
- [x] Update all agent implementations to use `ServiceInfo.name`
- [x] Update colony registry code to use new field name
- [x] Update any existing code referencing `component_name`

### Phase 3: Service Aggregation Logic

- [x] Implement `ListAgents` RPC call using updated protobuf definitions
- [x] Build service aggregation algorithm:
    - Extract all `ServiceInfo` entries from agents
    - Group by `name` field
    - Collect agent metadata per service
- [x] Handle edge cases: agents with no services, empty colony
- [x] Add sorting: services alphabetically, agents by ID within each service

### Phase 4: Output Formatting

- [x] Implement human-readable table output:
    - Service name, instance count, agent list
    - Status indicators (✓ healthy, ⚠️ degraded, ✗ unhealthy)
    - Aligned columns with proper spacing
- [x] Implement JSON output format matching API schema
- [x] Implement verbose output with detailed per-agent information
- [x] Add filtering by `--service` flag

### Phase 5: Error Handling & User Experience

- [x] Implement error handling for network failures (connection, timeout, auth)
- [x] Implement error handling for data errors (empty colony, no services, stale data)
- [x] Add retry logic with exponential backoff for transient failures
- [x] Implement progress spinner for operations > 500ms
- [x] Add snapshot timestamp to all output formats
- [x] Implement service not found error with helpful suggestions

### Phase 6: Performance & Observability

- [x] Implement status determination logic with priority order
- [x] Add telemetry collection (execution metrics, usage patterns)
- [x] Add debug logging support with `-v` flag
- [x] Implement performance benchmarks for various colony sizes
- [x] Add warnings for large colonies (> 1000 agents)
- [x] Optimize aggregation algorithm for memory efficiency

### Phase 7: Testing & Documentation

- [x] Unit tests: Service aggregation logic
- [x] Unit tests: Service filtering by name (case-insensitive)
- [x] Unit tests: Status determination logic (all priority paths)
- [x] Unit tests: JSON output format validation with schema
- [x] Unit tests: Error handling scenarios
- [x] Integration test: List services with multiple agents per service
- [x] Integration test: Filter by service name
- [x] Integration test: Empty colony (no agents)
- [x] Integration test: Stale data warnings
- [x] Integration test: Service not found error
- [x] Performance benchmarks: 10, 100, 1000, 5000 agents
- [x] Update CLI help documentation
- [x] Add examples to user documentation
- [x] Document error codes and troubleshooting

## Testing Strategy

### Unit Tests

**Service aggregation:**
- Multiple agents with same service name
- Agents with different services
- Agents with multiple services each (multi-service agents from RFD 011)
- Single agent with single service
- Empty colony (no agents)
- Agent with no services

**Service filtering:**
- Filter matching one service (exact match)
- Filter matching multiple agents for same service
- Filter with no matches (verify error message with suggestions)
- Case-insensitive matching ("Redis" matches "redis")
- Service name normalization for grouping

**Output formatting:**
- Table format with various service counts
- JSON output structure validation (schema compliance)
- JSON includes version, snapshot_time, total counts
- Verbose output completeness
- Status indicator rendering (✓, ⚠️, ✗)
- Snapshot timestamp display in all formats
- Color vs non-color terminal output

**Status determination:**
- Priority 1: Health endpoint check (HTTP 200 = healthy, other = unhealthy)
- Priority 2: Agent reported status (use if set)
- Priority 3: Computed from last_seen (< 30s = healthy, < 2m = degraded, > 2m = unhealthy)
- Health endpoint failure falls through to next priority
- Missing or empty status fields handled correctly

**Error handling:**
- Network errors: connection refused, timeout, auth failure
- Data errors: empty colony, no services, stale data (> 5m)
- Filtered service not found with helpful suggestions
- Incomplete agent data (missing fields marked as "N/A")
- Mixed agent versions (skip incompatible, show warning)
- Invalid flags and user input errors
- Retry logic with exponential backoff (3 attempts)

### Integration Tests

**Test 1: Basic Service Listing**

```bash
# Setup: Deploy 3 agents with mixed services
coral connect frontend:3000 redis:6379  # agent-1
coral connect frontend:3000 postgres:5432  # agent-2
coral connect redis:6379 postgres:5432  # agent-3

# Execute
coral service list

# Verify:
# - 3 unique services listed: frontend, redis, postgres
# - frontend: 2 instances (agent-1, agent-2)
# - redis: 2 instances (agent-1, agent-3)
# - postgres: 2 instances (agent-2, agent-3)
# - All instance counts correct
```

**Test 2: Service Filtering**

```bash
# Same setup as Test 1

# Execute
coral service list --service redis

# Verify:
# - Only redis service shown
# - 2 agents listed: agent-1, agent-3
# - Mesh IPs and status displayed
# - No other services in output
```

**Test 3: JSON Output**

```bash
# Same setup as Test 1

# Execute
coral service list --json | jq '.services[] | .service_name'

# Verify:
# - Valid JSON output
# - 3 services in array
# - Each service has instance_count and agents array
# - Agent objects contain all required fields
```

**Test 4: Degraded Agent Handling**

```bash
# Setup: Deploy agents, then stop one service
coral connect api:8080  # agent-1
coral connect api:8080  # agent-2
# Stop agent-2's service (simulate degraded state)

# Execute
coral service list --service api

# Verify:
# - Both agents listed
# - agent-1: status = healthy
# - agent-2: status = degraded or unhealthy
# - Status indicators displayed correctly
```

### E2E Tests

**Scenario: Multi-Service Kubernetes Environment**

```yaml
# Deploy 3 Kubernetes pods:
# - frontend pod: frontend:3000, metrics:9090
# - api pod: api:8080, redis:6379, metrics:9090
# - worker pod: worker:9000, redis:6379

# Execute from CLI
coral service list

# Verify:
# - 5 unique services (frontend, api, worker, redis, metrics)
# - metrics: 2 instances (frontend pod, api pod)
# - redis: 2 instances (api pod, worker pod)
# - All agent IDs match pod names
# - All health statuses correct
```

**Scenario: Service Discovery Workflow (AI Operator)**

```bash
# AI wants to execute command on Redis instance

# Step 1: Discover Redis agents
$ coral service list --service redis --json

{
  "version": "1.0",
  "snapshot_time": "2025-11-19T12:34:56Z",
  "total_services": 1,
  "total_instances": 2,
  "services": [
    {
      "service_name": "redis",
      "service_type": "redis",
      "instance_count": 2,
      "agents": [
        {
          "agent_id": "agent-1-cache",
          "mesh_ipv4": "10.42.0.10",
          "status": "healthy",
          "port": 6379,
          "health_endpoint": "",
          "last_seen": "2025-11-19T12:34:55Z"
        },
        {
          "agent_id": "agent-3-db",
          "mesh_ipv4": "10.42.0.12",
          "status": "degraded",
          "port": 6379,
          "health_endpoint": "",
          "last_seen": "2025-11-19T12:33:30Z"
        }
      ]
    }
  ]
}

# Step 2: Parse JSON to select healthy agent
# AI selects: agent-1-cache (status: healthy)

# Step 3: Use agent ID for targeted operations (RFD 044)
$ coral shell --agent agent-1-cache
# Opens shell session on selected agent

# Alternative: Direct MCP tool integration
# MCP tool 'coral_shell' can programmatically:
# 1. Query services via 'coral service list --service redis --json'
# 2. Filter for healthy instances
# 3. Execute commands via 'coral shell --agent <selected-agent-id>'

# Example AI workflow with MCP:
# User: "Check Redis memory usage"
# AI:
#   1. Calls coral_service_list(service="redis")
#   2. Selects healthy instance: agent-1-cache
#   3. Calls coral_shell(agent="agent-1-cache", command="redis-cli INFO memory")
#   4. Returns memory stats to user
```

## Security Considerations

**No new security surface:**

- Command uses existing `ListAgents` RPC with existing authentication
- Read-only operation - no writes to colony registry
- No new service exposure - aggregates existing data only
- Same access control as `coral colony agents` command

**Data privacy:**

- Service names and ports already exposed via `coral colony agents`
- No additional sensitive information revealed
- Mesh IPs are internal to WireGuard network (RFD 007)

**DoS considerations:**

- Aggregation happens client-side, not in colony server
- Performance scales with agent count (O(n) where n = number of agents)
- For very large colonies (1000+ agents), may add pagination in future

## Error Handling

The command implements comprehensive error handling for common failure scenarios:

### Network Errors

**Connection failures:**
- **Error**: Unable to connect to colony server
- **Message**: "Failed to connect to colony '<colony-id>': connection refused. Check that the colony is running and accessible."
- **Recovery**: Suggest checking colony connectivity with `coral colony status`
- **Exit code**: 1

**Timeout errors:**
- **Error**: RPC call exceeds timeout threshold
- **Message**: "Request timed out after 30s. The colony may be overloaded or unreachable."
- **Recovery**: Retry with exponential backoff (3 attempts: 1s, 2s, 4s delays)
- **Exit code**: 1

**Authentication failures:**
- **Error**: Invalid credentials or unauthorized access
- **Message**: "Authentication failed. Verify your colony credentials."
- **Recovery**: Suggest re-running `coral colony connect` or checking credentials
- **Exit code**: 1

### Data Errors

**Empty colony:**
- **Condition**: No agents registered in colony
- **Message**: "No agents found in colony. Use 'coral connect <service>' to register agents."
- **Output**: Display message with helpful next steps
- **Exit code**: 0 (not an error, just empty state)

**No services found:**
- **Condition**: Agents exist but none report services
- **Message**: "No services detected across <N> agents. Agents may not have service definitions configured."
- **Output**: Display message with agent count
- **Exit code**: 0

**Filtered service not found:**
- **Condition**: `--service` flag specifies non-existent service
- **Message**: "Service 'redis' not found in colony. Available services: frontend, api, postgres"
- **Output**: List available services to help user
- **Exit code**: 1

**Stale data warning:**
- **Condition**: Any agent's `last_seen` timestamp is older than 5 minutes
- **Warning**: "⚠️  Warning: Some agents haven't reported in over 5 minutes. Data may be stale."
- **Behavior**: Display warning but proceed with showing data
- **Exit code**: 0

### Partial Failures

**Incomplete agent data:**
- **Condition**: Some agents missing expected fields (mesh IP, service info)
- **Behavior**: Display available data, mark missing fields as "N/A"
- **Warning**: "⚠️  Some agents have incomplete data"
- **Exit code**: 0

**Mixed agent versions:**
- **Condition**: Agents report different protocol versions
- **Behavior**: Attempt to parse all versions, skip incompatible entries
- **Warning**: "⚠️  Some agents are running incompatible versions"
- **Exit code**: 0

### User Input Errors

**Invalid flags:**
- **Error**: Unsupported flag combination or invalid flag value
- **Message**: "Error: flag '--invalid' not recognized. See 'coral service list --help'"
- **Exit code**: 2

**Colony not specified:**
- **Condition**: Multiple colonies configured, none selected
- **Message**: "Multiple colonies found. Specify one with --colony flag: <list>"
- **Exit code**: 1

### Error Display Format

All errors follow consistent formatting:

```bash
# Network error example
$ coral service list
Error: Failed to connect to colony 'prod-colony': connection refused

Troubleshooting:
  • Check colony is running: coral colony status
  • Verify connectivity: ping <colony-host>
  • Review colony configuration: coral colony list

# Service not found example
$ coral service list --service invalid-service
Error: Service 'invalid-service' not found in colony

Available services (5):
  • api
  • frontend
  • postgres
  • redis
  • worker

# Stale data warning
$ coral service list
⚠️  Warning: Some agents haven't reported in over 5 minutes. Data may be stale.

Services (3) at 2025-11-19 12:34:56 UTC:
[... output continues ...]
```

### Retry Logic

**Automatic retries** for transient failures:
- Network timeouts: 3 attempts with exponential backoff
- Colony temporarily unavailable: 3 attempts
- Rate limiting: Retry after delay specified in response

**No retries** for:
- Authentication failures (requires user intervention)
- Invalid input (flag errors, bad service names)
- Empty results (not an error condition)

## Performance Characteristics

The command is designed for responsive operation across various colony sizes:

### Performance Targets

**Colony Size Categories:**

| Agents   | Services (est.) | Target Latency | Behavior                    |
|----------|-----------------|----------------|-----------------------------|
| 1-50     | 1-100           | < 100ms        | Instant display             |
| 51-500   | 100-1,000       | < 500ms        | Direct display              |
| 501-1000 | 1,000-5,000     | < 2s           | Show spinner                |
| 1000+    | 5,000+          | < 5s           | Spinner + pagination option |

**Operation complexity:**
- Time complexity: O(n × m) where n = agents, m = avg services per agent
- Space complexity: O(s) where s = unique services (typically s << n × m)
- Network: Single RPC call regardless of colony size

### User Feedback

**Progress indicators:**
- Operations < 500ms: No indicator, display results immediately
- Operations 500ms-2s: Show spinner: "Fetching services..."
- Operations > 2s: Show spinner with progress: "Fetching services (1,234 agents)..."

**Example spinner output:**
```bash
$ coral service list
⠋ Fetching services from colony (523 agents)...
```

### Optimization Strategies

**Current implementation:**
- Single `ListAgents` RPC call (minimizes round trips)
- Client-side aggregation (offloads work from colony server)
- Efficient map-based grouping (O(1) lookups per service)
- Sorted output for consistent UX

**Future optimizations** (if needed for 1000+ agents):
- Streaming RPC responses to process agents incrementally
- Pagination support with `--limit` and `--offset` flags
- Server-side aggregation option (RPC extension)
- Caching with TTL for frequently-run queries

### Large Colony Handling

**Automatic adjustments** for large colonies (> 1000 agents):

```bash
# Display shows hint for pagination
$ coral service list
Services (1,234) at 2025-11-19 12:34:56 UTC:
⚠️  Large colony detected. Consider using --limit flag for faster results.

# Future: Pagination support
$ coral service list --limit 50
Services (1,234) showing 1-50 at 2025-11-19 12:34:56 UTC:
[... first 50 services ...]

Use 'coral service list --limit 50 --offset 50' to see more.
```

**Memory considerations:**
- Average memory per agent entry: ~500 bytes
- 1,000 agents: ~500 KB
- 10,000 agents: ~5 MB
- Safe for typical CLI environments

### Benchmarking

**Performance testing requirements:**
- Test with synthetic colonies: 10, 100, 1000, 5000 agents
- Measure end-to-end latency (RPC + aggregation + rendering)
- Verify spinner appears for operations > 500ms
- Ensure consistent performance across runs

**Example benchmark results** (target):
```
BenchmarkServiceList/10agents      1000000  95 ms/op   0.5 MB/op
BenchmarkServiceList/100agents     100000   450 ms/op  1.2 MB/op
BenchmarkServiceList/1000agents    10000    1.8 s/op   5.0 MB/op
```

## Observability

The command includes telemetry to improve reliability and user experience:

### Metrics Collected

**Execution metrics:**
- Command invocation count
- Execution time (P50, P95, P99)
- Colony size at execution time (agent count)
- Error rate by error type
- Retry attempts and success rates

**Usage patterns:**
- Flag usage frequency (`--service`, `--json`, `--verbose`)
- Filtered service queries (which services are queried most)
- Output mode distribution (table vs JSON)
- Time-of-day usage patterns

### Logging

**Debug logging** (enabled with `-v` flag):
```bash
$ coral service list -v
DEBUG: Connecting to colony 'prod-colony' at colony.example.com:8443
DEBUG: Fetching agents via ListAgents RPC...
DEBUG: Received 234 agents in 345ms
DEBUG: Aggregating services across agents...
DEBUG: Found 12 unique services with 234 total instances
DEBUG: Rendering table output...

Services (12) at 2025-11-19 12:34:56 UTC:
[... output ...]
```

**Error logging:**
- All errors logged with full context (timestamps, colony ID, error details)
- Network errors include retry attempts and backoff timing
- Authentication errors include credential source information

### Telemetry Privacy

**Data collected** (anonymous):
- Command name and flags (no values)
- Execution time and error types
- Colony size (agent/service counts only)
- Client version

**Data NOT collected:**
- Service names or types
- Agent IDs or IP addresses
- Colony names or endpoints
- User identity or credentials

**Opt-out:**
Users can disable telemetry with:
```bash
export CORAL_TELEMETRY=false
```

## Future Enhancements

### Service Health Summary

Add aggregate health metrics per service:

```bash
$ coral service list --health-summary

SERVICE      INSTANCES  HEALTHY  DEGRADED  UNHEALTHY
─────────────────────────────────────────────────────
frontend     5          4        1         0
api          10         8        0         2
redis        3          3        0         0
```

### Service Metadata Filtering

Filter by service type, labels, or health status:

```bash
# Show only unhealthy services
coral service list --status unhealthy

# Show only specific service type
coral service list --type redis

# Show services with specific label
coral service list --label tier=backend
```

### Service Dependency Graph

Visualize service dependencies discovered from traffic flows:

```bash
$ coral service graph --service frontend

frontend
├─→ api (3 instances)
│   └─→ postgres (1 instance)
└─→ redis (2 instances)
```

### Historical Service Inventory

Track service deployment history over time:

```bash
# Show service count trends
coral service list --history --since 7d

# Output: Chart showing service instance counts over past 7 days
```

### Interactive Service Browser

Add interactive TUI for exploring services:

```bash
coral service browse

# Shows:
# - Tree view of services
# - Arrow keys to navigate
# - Enter to show agent details
# - Filter and search capabilities
```

## Appendix

### Comparison: Agent-Centric vs Service-Centric Views

| Question                           | `coral colony agents`         | `coral service list` |
|------------------------------------|-------------------------------|----------------------|
| "What agents are connected?"       | ✅ Primary view                | ❌ Requires inversion |
| "What services exist?"             | ❌ Requires manual aggregation | ✅ Primary view       |
| "Which agents run Redis?"          | ❌ Manual scanning             | ✅ Direct lookup      |
| "How many API instances?"          | ❌ Manual counting             | ✅ Instance count     |
| "Is agent-5 healthy?"              | ✅ Direct lookup               | ❌ Requires filtering |
| "Are all Redis instances healthy?" | ❌ Requires manual checking    | ✅ Service-level view |

**Conclusion**: Both commands are complementary and serve different query patterns.

### Service Aggregation Algorithm Details

**Step 1: Fetch agents**

```go
resp, err := client.ListAgents(ctx, &colonyv1.ListAgentsRequest{})
// Returns all agents with their Services[] arrays
```

**Step 2: Extract and group services**

```go
serviceMap := make(map[string]*ServiceView)

for _, agent := range resp.Msg.Agents {
    for _, svc := range agent.Services {
        key := svc.Name

        if serviceMap[key] == nil {
            serviceMap[key] = &ServiceView{
                ServiceName: svc.Name,
                ServiceType: svc.ServiceType,
                Agents:      []AgentInstance{},
            }
        }

        // Determine agent status using priority order:
        // 1. Health endpoint check (if configured)
        // 2. Agent reported status (if set)
        // 3. Computed from last_seen timestamp
        status := determineStatus(svc, agent)

        serviceMap[key].Agents = append(serviceMap[key].Agents, AgentInstance{
            AgentID:        agent.AgentId,
            MeshIPv4:       agent.MeshIpv4,
            Status:         status,
            Port:           svc.Port,
            HealthEndpoint: svc.HealthEndpoint,
            LastSeen:       agent.LastSeen,
        })
    }
}
```

**Step 3: Sort and format**

```go
// Convert to slice
services := make([]ServiceView, 0, len(serviceMap))
for _, svc := range serviceMap {
    svc.InstanceCount = len(svc.Agents)
    services = append(services, *svc)
}

// Sort by service name
sort.Slice(services, func(i, j int) bool {
    return services[i].ServiceName < services[j].ServiceName
})

// Within each service, sort agents by ID
for i := range services {
    sort.Slice(services[i].Agents, func(a, b int) bool {
        return services[i].Agents[a].AgentID < services[i].Agents[b].AgentID
    })
}
```

**Status Determination Function**

```go
// determineStatus calculates agent health status using priority order.
func determineStatus(svc *ServiceInfo, agent *Agent) string {
    // Priority 1: If service has health endpoint, use HTTP check
    if svc.HealthEndpoint != "" {
        if healthCheck, err := checkHealthEndpoint(agent.MeshIpv4, svc.Port, svc.HealthEndpoint); err == nil {
            if healthCheck.StatusCode == 200 {
                return "healthy"
            }
            return "unhealthy"
        }
        // If health check failed, fall through to next priority
    }

    // Priority 2: If agent reports status, use that
    if agent.Status != "" {
        return agent.Status
    }

    // Priority 3: Compute from last_seen timestamp
    timeSinceLastSeen := time.Since(agent.LastSeen)

    switch {
    case timeSinceLastSeen < 30*time.Second:
        return "healthy"
    case timeSinceLastSeen < 2*time.Minute:
        return "degraded"
    default:
        return "unhealthy"
    }
}
```

### Related Work

**Similar patterns in other systems:**

- **Kubernetes**: `kubectl get pods -l app=frontend` - service-based filtering
- **Consul**: `consul catalog services` - service inventory listing
- **Prometheus**: Service discovery and targets grouped by job name
- **Datadog**: Service catalog with instance counts

**Industry best practices:**

- Service-first views essential for microservice architectures
- Instance counts help with capacity planning
- Health aggregation enables quick service-level SLO checks

---

## Implementation Status

**Core Capability:** ✅ Implemented

All planned features have been implemented:

- ✅ CLI command structure (`coral service list`)
- ✅ Service aggregation logic with case-insensitive grouping
- ✅ Output formatting (table, JSON, verbose)
- ✅ Service filtering (`--service`, `--type` flags)
- ✅ Per-agent health status indicators (✓ healthy, ⚠ degraded, ✗ unhealthy)
- ✅ Snapshot timestamp in output
- ✅ JSON output with version, snapshot_time, totals wrapper
- ✅ Service-not-found error with helpful suggestions
- ✅ Real-time fan-out queries to agents for fresh service data
- ✅ Breaking protobuf change: `ServiceInfo.component_name` → `ServiceInfo.name`
- ✅ Integration with existing colony connectivity
