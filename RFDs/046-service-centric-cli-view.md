---
rfd: "046"
title: "Service-Centric CLI View"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: ["011", "044"]
related_rfds: ["006"]
database_migrations: []
areas: ["cli", "colony", "ux"]
---

# RFD 046 - Service-Centric CLI View

**Status:** 🚧 Draft

## Summary

Introduce a `coral service list` command that provides a service-centric view of
the colony, displaying all services as primary entities with their associated
agents. This inverts the existing `coral colony agents` perspective, enabling
operators to quickly answer questions like "which agents run Redis?" or "how
many instances of the API service are deployed?" without manually parsing
agent-by-agent output.

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

- **Aggregation from agent registry**: No new storage required. The command
  queries the existing colony agent registry (RFD 006, RFD 011) and aggregates
  `ServiceInfo` entries across all agents.

- **Service uniqueness by name**: Services are identified by `component_name`
  field from `ServiceInfo`. Multiple agents with the same service name are
  grouped together.

- **Per-agent service details**: For each agent running a service, show:
    - Agent ID (for targeting with RFD 044)
    - Health status (healthy/degraded/unhealthy)
    - Mesh IP (for direct connectivity scenarios)
    - Service-specific port (from `ServiceInfo`)
    - Health endpoint (if configured)

- **Filtering support**: Add `--service` flag to filter output to a specific
  service name, enabling queries like `coral service list --service redis`.

- **Consistent output modes**: Support both human-readable table format (
  default) and JSON output (`--json`) for programmatic access, matching UX
  patterns from `coral colony agents`.

**Benefits:**

- ✅ Service-first mental model aligns with operator workflows
- ✅ Instant service inventory and instance counts
- ✅ Quick agent discovery for service-based targeting
- ✅ Better UX for AI operators and MCP tool integration
- ✅ No new storage or protocol changes - pure aggregation layer
- ✅ Complements existing `coral colony agents` without replacing it

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
│    3. Group by service component_name                      │
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
│  Services (5):                                             │
│                                                            │
│  SERVICE      INSTANCES  AGENTS                            │
│  ─────────────────────────────────────────────────────────│
│  frontend     2          agent-1 (10.42.0.10, healthy)     │
│                          agent-2 (10.42.0.11, healthy)     │
│  redis        2          agent-1 (10.42.0.10, healthy)     │
│                          agent-3 (10.42.0.12, degraded)    │
│  metrics      1          agent-2 (10.42.0.11, healthy)     │
│  postgres     1          agent-3 (10.42.0.12, degraded)    │
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

2. **Protobuf** (no changes required):
    - Reuse existing `ListAgentsRequest/Response` from RFD 006
    - Use `ServiceInfo` message from RFD 011
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

Services (5):

SERVICE          INSTANCES  AGENTS
────────────────────────────────────────────────────────────────────
frontend         2          agent-1-frontend (10.42.0.10, healthy)
                            agent-2-frontend (10.42.0.11, healthy)

redis            2          agent-1-cache (10.42.0.10, healthy)
                            agent-3-db (10.42.0.12, degraded)

api              3          agent-4-api (10.42.0.13, healthy)
                            agent-5-api (10.42.0.14, healthy)
                            agent-6-api (10.42.0.15, unhealthy)

postgres         1          agent-3-db (10.42.0.12, degraded)

metrics          1          agent-2-frontend (10.42.0.11, healthy)
```

**Example: Filtered by service**

```bash
$ coral service list --service redis

Service: redis (2 instances)

AGENT ID            MESH IP        PORT   HEALTH    STATUS
────────────────────────────────────────────────────────────
agent-1-cache       10.42.0.10     6379   -         healthy
agent-3-db          10.42.0.12     6379   -         degraded
```

**Example: Verbose output**

```bash
$ coral service list --service redis -v

Service: redis
  Type: redis
  Instances: 2

  Agent: agent-1-cache
    Mesh IP: 10.42.0.10
    Status: healthy
    Port: 6379
    Last Seen: 5s ago

  Agent: agent-3-db
    Mesh IP: 10.42.0.12
    Status: degraded
    Port: 6379
    Last Seen: 1m ago
```

**Example: JSON output**

```bash
$ coral service list --json
```

```json
{
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
          "last_seen": "2025-11-18T12:34:56Z"
        },
        {
          "agent_id": "agent-2-frontend",
          "mesh_ipv4": "10.42.0.11",
          "status": "healthy",
          "port": 3000,
          "health_endpoint": "/health",
          "last_seen": "2025-11-18T12:34:58Z"
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
          "last_seen": "2025-11-18T12:34:55Z"
        },
        {
          "agent_id": "agent-3-db",
          "mesh_ipv4": "10.42.0.12",
          "status": "degraded",
          "port": 6379,
          "health_endpoint": "",
          "last_seen": "2025-11-18T12:33:30Z"
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
            if _, exists := serviceMap[service.ComponentName]; !exists {
                serviceMap[service.ComponentName] = &ServiceView{
                    ServiceName: service.ComponentName,
                    ServiceType: service.ServiceType,
                    Agents:      []AgentInstance{},
                }
            }

            serviceMap[service.ComponentName].Agents = append(
                serviceMap[service.ComponentName].Agents,
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

- [ ] Create `internal/cli/colony/service.go` with `newServiceCmd()` function
- [ ] Add `newServiceListCmd()` for the `list` subcommand
- [ ] Integrate into `NewColonyCmd()` in `internal/cli/colony/colony.go`
- [ ] Add basic RPC client setup (reuse existing colony connection patterns)
- [ ] Add `--service`, `--json`, `--verbose` flags

### Phase 2: Service Aggregation Logic

- [ ] Implement `ListAgents` RPC call using existing protobuf definitions
- [ ] Build service aggregation algorithm:
    - Extract all `ServiceInfo` entries from agents
    - Group by `component_name`
    - Collect agent metadata per service
- [ ] Handle edge cases: agents with no services, empty colony
- [ ] Add sorting: services alphabetically, agents by ID within each service

### Phase 3: Output Formatting

- [ ] Implement human-readable table output:
    - Service name, instance count, agent list
    - Status indicators (✓ healthy, ⚠️ degraded, ✗ unhealthy)
    - Aligned columns with proper spacing
- [ ] Implement JSON output format matching API schema
- [ ] Implement verbose output with detailed per-agent information
- [ ] Add filtering by `--service` flag

### Phase 4: Testing & Documentation

- [ ] Unit tests: Service aggregation logic
- [ ] Unit tests: Service filtering by name
- [ ] Unit tests: JSON output format validation
- [ ] Integration test: List services with multiple agents per service
- [ ] Integration test: Filter by service name
- [ ] Integration test: Empty colony (no agents)
- [ ] Update CLI help documentation
- [ ] Add examples to user documentation

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
- Filter matching one service
- Filter matching multiple agents
- Filter with no matches
- Case-sensitive vs case-insensitive matching

**Output formatting:**
- Table format with various service counts
- JSON output structure validation
- Verbose output completeness
- Status indicator rendering

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
coral service list --service redis --json

# Step 2: Parse JSON to get agent_id
# Example: agent-3-cache

# Step 3: Use agent ID for targeting (RFD 044)
coral shell --agent agent-3-cache
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

| Question                             | `coral colony agents`           | `coral service list`   |
|--------------------------------------|---------------------------------|------------------------|
| "What agents are connected?"         | ✅ Primary view                 | ❌ Requires inversion  |
| "What services exist?"               | ❌ Requires manual aggregation  | ✅ Primary view        |
| "Which agents run Redis?"            | ❌ Manual scanning              | ✅ Direct lookup       |
| "How many API instances?"            | ❌ Manual counting              | ✅ Instance count      |
| "Is agent-5 healthy?"                | ✅ Direct lookup                | ❌ Requires filtering  |
| "Are all Redis instances healthy?"   | ❌ Requires manual checking     | ✅ Service-level view  |

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
        key := svc.ComponentName

        if serviceMap[key] == nil {
            serviceMap[key] = &ServiceView{
                ServiceName: svc.ComponentName,
                ServiceType: svc.ServiceType,
                Agents:      []AgentInstance{},
            }
        }

        // Determine agent status
        status := determineStatus(agent.LastSeen)

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

**Core Capability:** ⏳ Not Started

This RFD is in draft state. Implementation will begin after approval.

**Planned Work:**

- CLI command structure
- Service aggregation logic
- Output formatting (table, JSON, verbose)
- Service filtering
- Integration with existing colony connectivity

**Dependencies:**

- RFD 011: Multi-service agent support (implemented)
- RFD 044: Agent ID standardization (implemented)
- RFD 006: Colony RPC handlers (implemented)
