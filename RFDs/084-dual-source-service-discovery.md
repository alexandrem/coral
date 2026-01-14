---
rfd: "084"
title: "Dual-Source Service Discovery"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "067", "064" ]
database_migrations: [ ]
areas: [ "colony", "query", "observability", "cli", "mcp" ]
---

# RFD 084 - Dual-Source Service Discovery

**Status:** 🎉 Implemented

## Summary

Enhance the `ListServices` RPC to support dual-source service discovery, showing
both explicitly registered services (via `ConnectService` API) and
auto-discovered services (from telemetry data). This unifies service visibility
across all query interfaces, eliminating the current inconsistency where
services appear in `coral query summary` but not in `coral query services`.

## Problem

The current service discovery implementation has two independent discovery paths
that create inconsistent behavior:

**Current Behavior:**

1. **Registry-Based Discovery** (`ListServices` RPC)
    - Queries the `services` table directly
    - Only shows services explicitly connected via `ConnectService` API
    - Services disappear when health checks fail or connections are lost

2. **Telemetry-Based Discovery** (`QueryUnifiedSummary` RPC)
    - Queries telemetry data tables directly (e.g., `beyla_http_metrics`)
    - Shows any service with telemetry data in the time range
    - Independent of service registration status
    - **Already provides historical data access** regardless of service health

**Important Note:**

Historical telemetry data IS currently accessible via `QueryUnifiedSummary`
(`coral query summary`) even when services are unhealthy or disconnected. The
problem is NOT lost data, but rather inconsistent service discovery between
`ListServices` and `QueryUnifiedSummary`, which confuses users.

**Current Limitations:**

- **Inconsistent visibility**: `coral query services` and `coral query summary`
  return different service lists
- **Fragmented query experience**: Historical data IS accessible via
  `QueryUnifiedSummary`, but users must know to use `coral query summary`
  instead of `coral query services` to see all services with telemetry
- **Confusion for users**: Users don't understand why services appear in
  summaries but not in service lists, or why they need two different commands
- **No unified service discovery**: Cannot get a complete list of all services
  (both registered and telemetry-discovered) from a single query

**Why This Matters:**

**Critical Use Case - Flaky Service Debugging:**

```
1. Service starts experiencing intermittent failures
2. Health check begins failing → Service unregistered from services table
3. Operator runs `coral query services` → Service not listed
4. Operator must know to use `coral query summary` instead to see the service
5. Confusion: Why do two different commands show different service lists?
```

While historical data IS accessible via `coral query summary`, the fragmented
experience creates confusion and requires users to understand internal
implementation details about two separate query paths.

**Real-World Impact:**

- **Inconsistent CLI experience**: `coral query services` shows different
  results than `coral query summary`, confusing users
- **Intermittent failures**: Services that flap between healthy/unhealthy
  disappear from `ListServices` but remain visible in `QueryUnifiedSummary`
- **Auto-discovered services**: Services discovered only through telemetry (no
  explicit registration) remain hidden from `ListServices` query
- **LLM-driven debugging**: AI assistants using `coral_query_services` MCP tool
  cannot discover all services with available telemetry data (must use separate
  summary query)

**Use Cases Affected:**

- "Show me all services with telemetry data in the last hour" → Currently
  requires `QueryUnifiedSummary` (`coral query summary`), not `ListServices`
  (`coral query services`)
- "What services were running when the incident occurred?" → Must use
  `coral query summary` to see unhealthy/disconnected services; `coral query
  services` only shows currently registered ones
- "List all services Coral has ever seen" → `ListServices` doesn't support
  this; must query telemetry tables directly
- E2E tests expecting consistent service discovery → Tests fail because
  `CLIQuerySuite` doesn't call `ensureServicesConnected()`, so services only
  appear in summary queries, not service list queries

## Solution

Enhance `ListServices` to be aware of both service sources (registry and
telemetry), providing a unified view with clear source attribution. This brings
`ListServices` in line with `QueryUnifiedSummary`, which already queries
telemetry data. The query will show services from either source, with
additional metadata indicating registration status and last activity.

**Key Design Decisions:**

1. **Non-breaking enhancement** - Existing behavior preserved by default, new
   fields are optional additions
2. **Time-bounded telemetry discovery** - Use `last_seen` timestamp to limit
   telemetry-discovered services to recent activity
3. **Source transparency** - Always indicate where service information came from
4. **Opt-in filtering** - Allow filtering by source type for specialized use
   cases

**Benefits:**

- **Unified service visibility** - Single API shows all services from both
  registry and telemetry sources, eliminating the current split between
  `ListServices` and `QueryUnifiedSummary`
- **Consistent query experience** - Telemetry-discovered services visible in
  both `coral query services` and `coral query summary`, matching user
  expectations
- **Better debugging experience** - Operators don't need to know which query
  command to use; all services appear in standard service list
- **Consistent CLI/MCP behavior** - `coral query services` and
  `coral query summary` show aligned service lists
- **Improved E2E test reliability** - Tests no longer fail due to inconsistent
  service discovery between the two query paths

**Architecture Overview:**

```
┌─────────────────────────────────────────────────────────────┐
│ ListServices RPC (Enhanced)                                  │
├─────────────────────────────────────────────────────────────┤
│                                                               │
│  ┌─────────────────────┐    ┌────────────────────────────┐ │
│  │  Registry Source    │    │  Telemetry Source          │ │
│  │  (services table)   │    │  (beyla_http_metrics, etc) │ │
│  │                     │    │                             │ │
│  │  • Explicit connect │    │  • Auto-discovered         │ │
│  │  • Health status    │    │  • Historical data         │ │
│  │  • Agent ID         │    │  • Active in time range    │ │
│  └──────────┬──────────┘    └──────────┬─────────────────┘ │
│             │                           │                    │
│             └───────────┬───────────────┘                    │
│                         │                                    │
│              ┌──────────▼──────────┐                        │
│              │  FULL OUTER JOIN    │                        │
│              │  (Union of both)    │                        │
│              └──────────┬──────────┘                        │
│                         │                                    │
│              ┌──────────▼──────────────────┐                │
│              │  Enriched Service List      │                │
│              │  • Source attribution       │                │
│              │  • Registration status      │                │
│              │  • Last seen timestamp      │                │
│              │  • Instance count (if reg)  │                │
│              └─────────────────────────────┘                │
└─────────────────────────────────────────────────────────────┘
```

### Component Changes

1. **Colony Server** (`internal/colony/server/query_service.go`):

    - Enhance `ListServices` RPC handler to query both sources
    - Implement FULL OUTER JOIN logic to combine registry and telemetry data
    - Add configurable time range for telemetry discovery (default: 1 hour)
    - Enrich response with source attribution and status metadata

2. **Protobuf API** (`coral/colony/v1/queries.proto`):

    - Extend `ServiceSummary` message with new optional fields
    - Add `ServiceSource` enum for source attribution
    - Add `ServiceStatus` enum for registration status
    - Add `ListServicesRequest.time_range` field for telemetry lookback

3. **CLI** (`internal/cli/query/services.go`):

    - Update output formatting to show source and status
    - Add visual indicators (icons/colors) for different sources
    - Support filtering by source type via flags

4. **MCP Tools** (`internal/colony/mcp/tools_discovery.go`):
    - Update `coral_query_services` tool to expose new metadata
    - Ensure consistent behavior with CLI commands

**Configuration Example:**

The time range for telemetry-based discovery is configurable in colony settings:

```yaml
colony:
    service_discovery:
        # How far back to look for telemetry-discovered services
        # Services with telemetry data newer than this are included
        telemetry_lookback: "1h" # default: 1 hour
```

## API Changes

### Enhanced Protobuf Messages

```protobuf
// ServiceSource indicates where service information originated.
enum ServiceSource {
    SERVICE_SOURCE_UNSPECIFIED = 0;

    // Service is explicitly registered via ConnectService API
    SERVICE_SOURCE_REGISTERED = 1;

    // Service is auto-discovered from telemetry data only
    SERVICE_SOURCE_DISCOVERED = 2;

    // Service is both registered AND has telemetry data
    SERVICE_SOURCE_BOTH = 3;
}

// ServiceStatus indicates the current registration and health status.
enum ServiceStatus {
    SERVICE_STATUS_UNSPECIFIED = 0;

    // Service is registered and passing health checks
    SERVICE_STATUS_ACTIVE = 1;

    // Service is registered but health checks are failing
    SERVICE_STATUS_UNHEALTHY = 2;

    // Service is no longer registered but has recent telemetry
    SERVICE_STATUS_DISCONNECTED = 3;

    // Service is only known from telemetry, never explicitly registered
    SERVICE_STATUS_DISCOVERED_ONLY = 4;
}

// ServiceSummary represents a discovered service.
message ServiceSummary {
    string name = 1;
    string namespace = 2;
    int32 instance_count = 3;
    google.protobuf.Timestamp last_seen = 4;

    // NEW: Indicates where this service information came from
    ServiceSource source = 5;

    // NEW: Current registration and health status (if applicable)
    // Only set when source includes SERVICE_SOURCE_REGISTERED
    optional ServiceStatus status = 6;

    // NEW: Agent ID where service is registered (if applicable)
    // Only set when source includes SERVICE_SOURCE_REGISTERED
    optional string agent_id = 7;
}

// ListServicesRequest retrieves all known services.
message ListServicesRequest {
    // Filter by namespace
    string namespace = 1;

    // NEW: Time range for telemetry-based discovery (e.g., "1h", "24h")
    // Defaults to "1h" if not specified
    // Only affects telemetry-discovered services
    string time_range = 2;

    // NEW: Filter by service source
    // If unspecified, returns all services regardless of source
    optional ServiceSource source_filter = 3;
}
```

### Enhanced RPC Endpoint

The existing RPC signature remains unchanged (backward compatible):

```protobuf
service ColonyService {
    // Lists all known services (registry + telemetry discovered)
    rpc ListServices(ListServicesRequest) returns (ListServicesResponse);
}
```

### CLI Commands

**Enhanced command with new flags:**

```bash
# List all services (default: includes both sources)
coral query services

# Example output:
Found 3 service(s):

● otel-app (default) - 1 instance(s) [ACTIVE]
  Source: BOTH (registered + telemetry)
  Last seen: 14:23:45

◐ flaky-api (default) - 0 instance(s) [DISCONNECTED]
  Source: DISCOVERED (telemetry only)
  Last seen: 14:20:12 (3 minutes ago)

○ legacy-service (default) - 1 instance(s) [UNHEALTHY]
  Source: REGISTERED (no recent telemetry)
  Last seen: 14:22:30

# Filter to only registered services
coral query services --source registered

# Filter to only telemetry-discovered services
coral query services --source discovered

# Extend telemetry lookback window
coral query services --since 24h
```

**Visual Indicators:**

- `●` (solid circle) - Active and healthy (BOTH source, ACTIVE status)
- `◐` (half circle) - Discovered from telemetry only (DISCOVERED source)
- `○` (hollow circle) - Registered but unhealthy/no telemetry (REGISTERED
  source)

### Database Query Changes

The enhanced `ListServices` implementation will use a FULL OUTER JOIN:

```sql
-- Enhanced query combining both sources
SELECT COALESCE(s.name, t.service_name) as name,
       ''                               as namespace, -- TODO: namespace support

       -- Source attribution
       CASE
           WHEN s.name IS NOT NULL AND t.service_name IS NOT NULL THEN 'BOTH'
           WHEN s.name IS NOT NULL THEN 'REGISTERED'
           ELSE 'DISCOVERED'
           END                          as source,

       -- Registration status (only for registered services)
       s.status                         as registration_status,

       -- Instance count (only for registered services)
       COUNT(DISTINCT s.agent_id)       as instance_count,

       -- Last seen (prefer registry heartbeat, fall back to telemetry)
       COALESCE(
           MAX(h.last_seen),
           MAX(s.registered_at),
           MAX(t.last_timestamp)
       )                                as last_seen,

       -- Agent ID (only for registered services)
       s.agent_id                       as agent_id

FROM services s

-- FULL OUTER JOIN with telemetry-discovered services
         FULL OUTER JOIN (SELECT DISTINCT service_name,
                                          MAX(timestamp) as last_timestamp
                          FROM beyla_http_metrics
                          WHERE timestamp > ? -- Time range parameter
                          GROUP BY service_name) t ON s.name = t.service_name

         LEFT JOIN service_heartbeats h ON s.id = h.service_id

GROUP BY s.name, t.service_name, s.status, s.agent_id
ORDER BY last_seen DESC
```

**Query Parameters:**

- Time range for telemetry lookback (e.g., `NOW() - INTERVAL '1 hour'`)
- Optional namespace filter
- Optional source filter

## Testing Strategy

### Unit Tests

**Colony Server (`query_service_test.go`):**

- `TestListServices_RegisteredOnly` - Services only in registry, no telemetry
- `TestListServices_DiscoveredOnly` - Services only in telemetry, not registered
- `TestListServices_Both` - Services in both registry and telemetry
- `TestListServices_UnhealthyWithTelemetry` - Registered but unhealthy service
  with telemetry data
- `TestListServices_DisconnectedWithTelemetry` - Disconnected service with
  recent telemetry
- `TestListServices_TimeRangeFilter` - Telemetry lookback window filtering
- `TestListServices_SourceFilter` - Filter by REGISTERED/DISCOVERED/BOTH

**Test Data Setup:**

```go
// Setup: Create services in different states
//
// 1. "active-service": Registered + healthy + telemetry
// 2. "unhealthy-service": Registered + unhealthy + telemetry
// 3. "disconnected-service": Not registered + recent telemetry
// 4. "registry-only": Registered + no telemetry
// 5. "old-telemetry": Not registered + old telemetry (outside time range)
//
// Verify each appears with correct source, status, and last_seen
```

### Integration Tests

**E2E Test Enhancement (`cli_query_test.go`):**

- Update `TestQueryServicesCommand` to verify dual-source discovery
- Add `TestQueryServicesWithUnhealthyService` - Verify unhealthy services remain
  visible
- Add `TestQueryServicesSourceFilter` - Test filtering by source type
- Ensure `ensureServicesConnected()` is called in SetupSuite (already
  implemented)

**Test Scenario:**

```go
// 1. Connect otel-app via ConnectService
// 2. Generate telemetry data for otel-app
// 3. Generate telemetry for "auto-discovered-app" (no explicit connection)
// 4. Query services → Should see both
// 5. Disconnect otel-app
// 6. Query services → Should still see otel-app (via telemetry) + auto-discovered-app
```

### E2E Tests

**Flaky Service Scenario:**

```bash
# 1. Start service, connect to agent
coral agent connect otel-app --port 8080

# 2. Verify service appears as ACTIVE + BOTH
coral query services | grep otel-app
# Expected: ● otel-app ... [ACTIVE] Source: BOTH

# 3. Make service unhealthy (stop health endpoint)
docker-compose stop otel-app-health-proxy

# 4. Wait for health check to fail
sleep 30

# 5. Verify service still appears, status changed
coral query services | grep otel-app
# Expected: ○ otel-app ... [UNHEALTHY] Source: BOTH

# 6. Verify telemetry is still queryable
coral query summary otel-app --since 5m
# Expected: Shows historical metrics despite unhealthy status
```

## Security Considerations

**Data Exposure:**

- Telemetry-discovered services may include services users didn't explicitly
  register
- This is acceptable because telemetry data is already stored in the colony
  database
- No new data is exposed; only the discovery mechanism is enhanced

**Access Control:**

- Same RBAC applies as existing `ListServices` and `QueryUnifiedSummary` RPCs
- No additional authorization changes required

## Migration Strategy

### Deployment Steps

1. **Phase 1: Add optional fields to protobuf**
    - Deploy updated protobuf definitions
    - Clients ignore new fields (backward compatible)

2. **Phase 2: Implement enhanced server logic**
    - Deploy colony server with dual-source query
    - Old clients continue to work (new fields are optional)

3. **Phase 3: Update CLI and MCP tools**
    - Deploy updated CLI with enhanced output formatting
    - Deploy updated MCP tools with new metadata

4. **Phase 4: Update documentation**
    - Update user docs to explain source types
    - Add examples for filtering by source

### Rollback Plan

- All changes are additive and non-breaking
- Rolling back to old server version simply ignores new fields
- No database migrations required
- No data loss on rollback

## Implementation Status

**Core Capability:** ✅ Implemented (2026-01-14)

All components have been implemented and tested. All tests pass.

**Implementation Details:**

- ✅ Protobuf message enhancements
  - Added `ServiceSource` enum (REGISTERED, DISCOVERED, BOTH)
  - Added `ServiceStatus` enum (ACTIVE, UNHEALTHY, DISCONNECTED, DISCOVERED_ONLY)
  - Extended `ServiceSummary` with source, status, and agent_id fields
  - Extended `ListServicesRequest` with time_range and source_filter fields
  - File: `proto/coral/colony/v1/queries.proto`

- ✅ Colony server query logic
  - Implemented FULL OUTER JOIN between services registry and telemetry data
  - Added time-bounded telemetry discovery (default: 1 hour)
  - Added source attribution and status enrichment
  - Added source filtering support
  - File: `internal/colony/server/query_service.go`

- ✅ CLI output formatting
  - Added visual indicators (●/◐/○) for service sources
  - Added `--since` flag for time range control
  - Added `--source` flag for filtering by source type
  - Enhanced output to show source, status, agent ID, and last seen
  - File: `internal/cli/query/services.go`

- ✅ MCP tool updates
  - Updated `coral_list_services` tool to use dual-source discovery
  - Exposed source and status metadata in JSON output
  - Maintained backward compatibility with port/type/labels
  - File: `internal/colony/mcp/tools_discovery.go`

- ✅ Unit tests
  - Comprehensive test suite for dual-source discovery
  - Tests for registered-only, discovered-only, and both sources
  - Tests for time range filtering
  - Tests for source filtering
  - File: `internal/colony/server/query_service_test.go`

- ⏳ Documentation updates (deferred)
  - User documentation to be updated in separate PR
  - API documentation generated from protobuf comments

## Future Work

The following enhancements are out of scope for this RFD but may be addressed in
future work:

**Service Activity Metrics** (Future - Low Priority)

- Track "first seen" timestamp for telemetry-discovered services
- Count total requests/traces seen for auto-discovered services
- Add "confidence score" for auto-discovered services based on data volume

**Namespace Support** (Blocked by RFD XXX)

- Currently `namespace` field is always empty string
- Full namespace filtering requires Kubernetes integration work
- Telemetry-based discovery should respect namespace boundaries

**Service Lifecycle Events** (Future - Medium Priority)

- Emit events when services transition between states (ACTIVE → UNHEALTHY →
  DISCONNECTED)
- Enable alerting on service disappearance or health degradation
- Track service restart detection (PID changes)

**Advanced Filtering** (Future - Low Priority)

- Filter by agent ID
- Filter by last_seen time range
- Combine multiple filters (AND/OR logic)

## Appendix

### Related RFDs

- **RFD 067 (Unified Query Interface)**: Established the principle of combining
  eBPF and OTLP data sources
- **RFD 064 (Service Registry Process Info)**: Defined service registry
  structure and metadata
- **RFD 076 (Sandboxed TypeScript Execution)**: Defines focused query APIs for
  scriptable access

### Implementation Notes

**DuckDB FULL OUTER JOIN:**

DuckDB supports standard SQL FULL OUTER JOIN syntax. The query optimizer
efficiently handles the union of registry and telemetry tables.

**Telemetry Table Selection:**

Currently using `beyla_http_metrics` as the primary telemetry discovery source.
Future enhancements may also check:

- `otlp_spans` table (OTLP traces)
- `otlp_metrics` table (OTLP metrics)
- Future log tables

For MVP, `beyla_http_metrics` provides sufficient coverage as it captures
HTTP-level service activity.

**Performance Considerations:**

- Telemetry table scan is limited by time range index (efficient)
- Registry table is small (<100 services typically)
- FULL OUTER JOIN with DISTINCT is performant at this scale
- If needed, add materialized view for frequently accessed union

### Test Configuration Examples

**E2E Test Setup:**

```yaml
# docker-compose test environment
services:
    # Registered + healthy + telemetry
    otel-app:
        image: coral/test-otel-app
        labels:
            coral.auto_connect: "true"

    # Auto-discovered (no explicit registration)
    auto-discovered-app:
        image: coral/test-app
        # No coral labels, will be discovered via telemetry only

    # Flaky service for testing unhealthy scenarios
    flaky-app:
        image: coral/test-flaky-app
        labels:
            coral.auto_connect: "true"
        environment:
            HEALTH_CHECK_FAIL_AFTER: "30s"
```
