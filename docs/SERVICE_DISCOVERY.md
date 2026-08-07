# Service Discovery Architecture

**Implementation:** RFD 084 - Dual-Source Service Discovery

## Overview

Coral uses a **dual-source service discovery** system that combines explicit
service registration with automatic discovery from telemetry data. This provides
complete visibility into your distributed system, showing both services you've
explicitly connected and services that are automatically observed from
observability data.

## Architecture

### Two Independent Sources

```
┌───────────────────────────────────────────────────────────────────┐
│                       Service Discovery System                    │
├───────────────────────────────────────────────────────────────────┤
│                                                                   │
│  ┌───────────────────────────┐    ┌────────────────────────────┐  │
│  │   Source 1: Registry      │    │  Source 2: Telemetry       │  │
│  │   (Explicit Registration) │    │  (Auto-Observation)          │  │
│  ├───────────────────────────┤    ├────────────────────────────┤  │
│  │                           │    │                            │  │
│  │ • ConnectService API      │    │ • eBPF HTTP/gRPC metrics   │  │
│  │ • agent --connect flag    │    │ • OTLP traces              │  │
│  │ • coral connect command   │    │ • OTLP metrics             │  │
│  │                           │    │ • beyla_http_metrics table │  │
│  │ Storage:                  │    │                            │  │
│  │ • services table          │    │ Storage:                   │  │
│  │ • service_heartbeats      │    │ • beyla_http_metrics       │  │
│  │                           │    │ • otlp_spans               │  │
│  │ Status:                   │    │ • otlp_metrics             │  │
│  │ • active                  │    │                            │  │
│  │ • unhealthy               │    │ Time-bounded:              │  │
│  │ • disconnected            │    │ • Default: 1 hour          │  │
│  │                           │    │ • Configurable via query   │  │
│  └───────────┬───────────────┘    └──────────┬─────────────────┘  │
│              │                               │                    │
│              └───────────┬───────────────────┘                    │
│                          │                                        │
│                 ┌─────────▼──────────┐                            │
│                 │  FULL OUTER JOIN   │                            │
│                 │  (Union of both)   │                            │
│                 └─────────┬──────────┘                            │
│                           │                                       │
│                 ┌─────────▼──────────────────┐                    │
│                 │  Unified Service List      │                    │
│                 │  • Source attribution      │                    │
│                 │  • Status enrichment       │                    │
│                 │  • Last seen timestamp     │                    │
│                 │  • Instance count          │                    │
│                 │  • Agent ID (if registered)│                    │
│                 └────────────────────────────┘                    │
│                                                                   │
└───────────────────────────────────────────────────────────────────┘
```

## Service Sources

### Source 1: Registry (Explicit Registration)

Services are explicitly registered when:

**Via Agent Startup:**

```bash
coral agent start --connect api:8080 --connect frontend:3000
```

**Via Connect Command:**

```bash
coral connect api:8080
```

**Via SDK:**

```go
client.ConnectService(ctx, &agentv1.ConnectServiceRequest{
    ServiceName: "api",
    Port: 8080,
})
```

**Characteristics:**

- Service appears immediately in registry
- Tracked in `services` table in DuckDB
- Health status monitored via heartbeats
- Instance count aggregated across agents
- Persists until explicitly disconnected or health checks fail

### Source 2: Telemetry (Auto-Observation)

Services are automatically observed when they:

**Generate HTTP/gRPC Traffic:**

- eBPF observes HTTP requests/responses
- Metrics stored in `beyla_http_metrics` table
- Service name extracted from traffic

**Send OTLP Data:**

- Applications instrumented with OTLP
- Traces in `otlp_spans` table
- Metrics in `otlp_metrics` table

**Characteristics:**

- Service appears when first telemetry data arrives
- No explicit connection needed
- Time-bounded (default: 1 hour lookback)
- No health status (just presence in data)
- Useful for discovering unregistered services

## Service Lifecycle States

### State Diagram

```
                    ┌──────────────────┐
                    │  Not Observed    │
                    └────────┬─────────┘
                             │
                ┌────────────┴────────────┐
                │                         │
         [Connect API]            [Telemetry Data]
                │                         │
                ▼                         ▼
    ┌───────────────────┐     ┌───────────────────┐
    │    REGISTERED     │     │     OBSERVED      │
    │                   │     │                   │
    │ Source: REGISTERED│     │ Source: OBSERVED│
    │ Status: ACTIVE    │     │ Status: OBSERVED  │
    │                   │     │       _ONLY       │
    └─────────┬─────────┘     └─────────┬─────────┘
              │                         │
       [Telemetry arrives]      [Service connects]
              │                         │
              └──────────┬──────────────┘
                         ▼
                ┌──────────────────┐
                │    VERIFIED      │
                │                  │
                │ Source: VERIFIED │
                │ Status: ACTIVE   │
                │ (healthy)        │
                └─────────┬────────┘
                          │
           ┌──────────────┼──────────────┐
           │              │              │
    [Health fails]  [Disconnect]  [All good]
           │              │              │
           ▼              ▼              ▼
    ┌──────────┐   ┌──────────┐   ┌──────────┐
    │UNHEALTHY │   │DISCONNECT│   │  ACTIVE  │
    │          │   │   ED     │   │          │
    │Source:   │   │          │   │Source:   │
    │VERIFIED/ │   │Source:   │   │VERIFIED  │
    │REGISTERED│   │OBSERVED  │   │          │
    │Status:   │   │Status:   │   │Status:   │
    │UNHEALTHY │   │DISCONNECT│   │ACTIVE    │
    │          │   │   ED     │   │          │
    └──────────┘   └──────────┘   └──────────┘
```

## Service Status Types

| Status            | Source                 | Meaning                                                                 | CLI Indicator                                     |
|-------------------|------------------------|-------------------------------------------------------------------------|---------------------------------------------------|
| `ACTIVE`          | REGISTERED or VERIFIED | Service registered and passing health checks                            | ● (solid) if VERIFIED<br>○ (hollow) if REGISTERED |
| `UNHEALTHY`       | REGISTERED or VERIFIED | Service registered but health checks failing                            | ○ (hollow)                                        |
| `DISCONNECTED`    | OBSERVED               | Service was registered but now disconnected; still has recent telemetry | ◐ (half)                                          |
| `OBSERVED_ONLY`   | OBSERVED               | Service never registered; only known from telemetry                     | ◐ (half)                                          |

## Query Implementation

### SQL Query Structure

The `ListServices` RPC uses a FULL OUTER JOIN to combine both sources:

```sql
SELECT COALESCE(s.name, t.service_name)        as name,
       ''                                      as namespace,

       -- Source attribution
       CASE
           WHEN s.name IS NOT NULL AND t.service_name IS NOT NULL THEN 3 -- VERIFIED
           WHEN s.name IS NOT NULL THEN 1 -- REGISTERED
           ELSE 2 -- OBSERVED
           END                                 as source,

       -- Registration status (only for registered services)
       s.status                                as registration_status,

       -- Instance count (only for registered services)
       COALESCE(COUNT(DISTINCT s.agent_id), 0) as instance_count,

       -- Last seen (prefer registry heartbeat, fall back to telemetry)
       COALESCE(
           MAX(h.last_seen),
           MAX(s.registered_at),
           MAX(t.last_timestamp)
       )                                       as last_seen,

       -- Agent ID (only for registered services)
       MIN(s.agent_id)                         as agent_id

FROM services s

-- FULL OUTER JOIN with telemetry-observed services
         FULL OUTER JOIN (SELECT DISTINCT service_name,
                                          MAX(timestamp) as last_timestamp
                          FROM beyla_http_metrics
                          WHERE timestamp
                              > ? -- Time range parameter (default: NOW() - 1 hour)
                          GROUP BY service_name) t ON s.name = t.service_name

         LEFT JOIN service_heartbeats h ON s.id = h.service_id

GROUP BY s.name, t.service_name, s.status
ORDER BY last_seen DESC
```

### Time Range Filtering

**Default Behavior:**

```bash
coral service list  # Uses 1 hour lookback for telemetry
```

**How It Works:**

- Time range is fixed at 1 hour and only affects telemetry-observed services
- Registry services always appear regardless of time range
- Cutoff: `WHERE timestamp > NOW() - INTERVAL '1 hour'`

## Use Cases & Examples

### Case 1: Finding All Active Services

**Goal:** See what's currently running and healthy

```bash
coral service list
```

**Output:**

```
Found 3 service(s):

● api-service (default) - 2 instance(s) [ACTIVE]
  Source: VERIFIED (registered + telemetry) | Last seen: 14:23:45 | Agent: agent-1

● frontend (default) - 1 instance(s) [ACTIVE]
  Source: VERIFIED (registered + telemetry) | Last seen: 14:23:40 | Agent: agent-2

◐ background-worker (default) - 0 instance(s) [DISCOVERED]
  Source: OBSERVED (telemetry only) | Last seen: 14:20:15
```

**Analysis:**

- `api-service` and `frontend`: Explicitly connected and sending telemetry ✅
- `background-worker`: Auto-discovered from telemetry, not explicitly connected

### Case 2: Debugging Flaky Service

**Scenario:** Service intermittently fails health checks

```bash
# Check service status
coral service list

# Output shows:
○ payment-api (default) - 1 instance(s) [UNHEALTHY]
  Source: VERIFIED (registered + telemetry) | Last seen: 14:22:30 | Agent: agent-3
```

**What This Tells You:**

- Service is registered (explicitly connected)
- Health checks are failing (UNHEALTHY status)
- Still has recent telemetry data (BOTH source)
- Data is still queryable via `coral query summary payment-api`

### Case 3: Finding Unregistered Services

**Goal:** Discover services sending traffic but not explicitly monitored

```bash
coral service list --source observed
```

**Output:**

```
Found 2 service(s):

◐ redis-proxy (default) - 0 instance(s) [OBSERVED_ONLY]
  Source: OBSERVED (telemetry only) | Last seen: 14:18:22

◐ kafka-consumer (default) - 0 instance(s) [OBSERVED_ONLY]
  Source: OBSERVED (telemetry only) | Last seen: 14:15:10
```

**Action:**

```bash
# Explicitly connect them for better monitoring
coral connect redis-proxy:6379
coral connect kafka-consumer:9092
```

### Case 4: Historical Analysis

**Goal:** See what services were running during an incident

```bash
coral service list
```

The telemetry lookback is 1 hour. For incidents older than that, query the raw
telemetry tables directly with `coral query sql`.

### Case 5: Production vs Development

**Filter by explicitly connected services only:**

```bash
coral service list --source registered
```

**Why:** In production, you may want to only see services you've explicitly
configured, ignoring auto-discovered development services.

## Integration Points

### CLI Commands

```bash
# Basic discovery
coral service list

# With filters
coral service list --source verified   # Only verified services
coral service list --source observed   # Only telemetry-observed

# JSON output for automation
coral service list --format json
```

### AI Integration

AI assistants (Claude Desktop, Cursor, `coral terminal`) query services via
`coral_cli`:

```json
["service", "list"]
```

Returns JSON with each service's `name`, `port`, `source`, `status`, and
`instance_count`. The AI filters for `source: VERIFIED` and `status: ACTIVE`
to identify services currently in use.

**Example:**
> "What services are currently running?"

Claude calls `coral_cli(["service", "list"])` → Filters for `source: VERIFIED`
and `status: ACTIVE` → Presents results.

### TypeScript SDK

```typescript
import {CoralClient} from '@coral-mesh/sdk';

const client = new CoralClient();

// List all services
const response = await client.query.listServices({
    timeRange: "1h",
    sourceFilter: "VERIFIED"
});

for (const service of response.services) {
    console.log(`${service.name}: ${service.source} [${service.status}]`);
}
```

### gRPC API

```go
import colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"

resp, err := client.ListServices(ctx, &colonyv1.ListServicesRequest{
    TimeRange: "1h",
    SourceFilter: colonyv1.ServiceSource_SERVICE_SOURCE_VERIFIED.Enum(),
})
```

## Troubleshooting

### Service Not Appearing

**Symptom:** Expected service doesn't show in `coral service list`

**Possible Causes:**

1. **Not registered and no recent telemetry:**
    - Service hasn't been explicitly connected
    - No HTTP/gRPC traffic observed in last hour
    - **Solution:** `coral connect <service>` or extend time range

2. **Outside time range:**
    - Telemetry data is older than the 1-hour lookback window
    - **Solution:** Query raw telemetry with `coral query sql`

3. **No telemetry generation:**
    - Service not making HTTP/gRPC calls
    - No OTLP instrumentation
    - **Solution:** Explicitly connect: `coral agent start --connect <service>`

### Service Shows as OBSERVED_ONLY

**Is this a problem?** No, this is expected behavior.

**What it means:**

- Coral detected HTTP/gRPC traffic from this service
- Service was not explicitly connected
- Telemetry data is still fully accessible

**When to explicitly connect:**

- You want health check monitoring
- You need instance count tracking
- You want better control over service lifecycle

**How to connect:**

```bash
coral connect <service-name>:<port>
```

### Service Shows as UNHEALTHY

**Meaning:**

- Service is registered (explicitly connected)
- Health checks are failing
- Telemetry data may still be arriving

**Immediate actions:**

```bash
# Check if data is still accessible
coral query summary <service-name> --since 10m

# Check agent status
coral agent status

# Reconnect if needed
coral connect <service-name>:<port>
```

### Inconsistent Service Lists

**Pre-RFD 084 Issue (RESOLVED):**

Before dual-source discovery, users experienced:

- `coral service list` → Shows service A
- `coral query summary` → Shows services A, B, C

**Root Cause:** Separate query paths (registry-only vs telemetry-only)

**Post-RFD 084 Solution:**

Both commands now use unified discovery:

- `coral service list` → Shows A, B, C with source attribution
- `coral query summary` → Shows A, B, C (consistent)

If you still see inconsistencies, check:

1. Time range parameters
2. Namespace filters
3. Agent connectivity

## Performance Considerations

### Query Efficiency

**Registry Table:** O(n) where n = number of registered services (~10-100
typically)

**Telemetry Query:** O(m) where m = number of services with telemetry in time
range

- Indexed by timestamp → Efficient time range filtering
- GROUP BY service_name → Aggregation is fast with small result sets

**FULL OUTER JOIN:** O(n + m) → Linear complexity, very efficient at expected
scale

### Caching Strategy

**Current Implementation:** No caching (always fresh data)

**Why:**

- Service status changes rapidly (health checks)
- Telemetry arrives continuously
- Query is already fast (<100ms typically)

**Future Optimization (if needed):**

- Materialized view for frequently accessed union
- Redis cache with TTL for read-heavy workloads

## Security & Access Control

### Data Visibility

**Registry Services:**

- Visible to all users with colony access
- RBAC controlled at colony level

**Telemetry Services:**

- Same visibility as registry services
- No additional exposure of data

**Agent IDs:**

- Exposed in service summaries
- Required for debugging and tracing service instances

### Privacy Considerations

**Auto-Observation Implications:**

- Services may appear without explicit registration
- This is by design: provides complete observability
- If a service sends HTTP traffic through eBPF-monitored paths, it will be
  discovered

**Opt-Out:**

- Use `--source registered` filter to hide auto-discovered services
- Disable eBPF monitoring on specific agents

## Migration & Compatibility

### Backward Compatibility

**Old Clients:**

- Continue to work unchanged
- Ignore new optional fields (source, status, agent_id)
- Receive all services (same as before)

**New Clients:**

- Can use new fields for enhanced UX
- Can filter by source
- Can display richer status information

### Migrating from Pre-RFD 084

**No action required.** The enhancement is:

- Non-breaking API change (optional fields only)
- No database migrations
- No configuration changes

**Optional Enhancements:**

- Update CLI scripts to use new `--source` filter
- Update dashboards to show source indicators
- Update runbooks to reference new troubleshooting approaches

## References

- **RFD 084:** [Dual-Source Service Discovery](../RFDs/084-dual-source-service-discovery.md)
- **RFD 092:** [Service Topology](../RFDs/092-service-topology.md)
- **RFD 067:** [Unified Query Interface](../RFDs/067-unified-query-interface.md)
- **RFD 064:** [Service Registry Process Info](../RFDs/064-service-registry-process-info.md)
