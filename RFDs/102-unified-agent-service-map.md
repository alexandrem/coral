---
rfd: "102"
title: "Unified Agent Service Map"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "053", "084" ]
database_migrations: [ ]
areas: [ "agent", "cli", "service-discovery", "observability" ]
---

# RFD 102 - Unified Agent Service Map

**Status:** 🚧 Draft

## Summary

Rename `coral connect` to `coral watch` to better reflect its intent, feed
beyla-discovered processes back into the agent service map as lightweight
"observed" entries, and introduce a new `coral agent services` command that
gives a unified view of both explicitly watched and automatically observed
services on an agent.

## Problem

The agent currently maintains two disconnected service tracking systems that
never interact:

**1. Explicit service monitors** (`agent.monitors`)

- Created when a user runs `coral connect frontend:3000`
- Full feature set: health checks, SDK discovery, continuous profiling
- Named by the user, visible via `coral agent status`

**2. Beyla auto-discovered processes**

- Captured when `--monitor-all` is enabled
- Anonymous: identified only by port number, no human-readable name
- Not visible anywhere in the agent CLI or service map
- Completely separate from `agent.monitors`

This split creates several concrete problems:

**Misleading command name:** `coral connect` implies establishing a link between
two things. What it actually does is register a service for observation. Users
read the docs and run `coral connect`, which feels backwards — they are telling
coral to watch their service, not connecting to it.

**Invisible auto-discovery:** When running with `--monitor-all`, beyla captures
all listening processes and stores their RED metrics in DuckDB. But at the agent
level, these processes are invisible — there is no way to ask "what has beyla
found?" without querying DuckDB directly. The agent has no knowledge of what
beyla is observing.

**No reconciliation between modes:** A user running `--monitor-all` who then
runs `coral connect frontend:3000` to get health checks ends up with beyla
observing port 3000 AND a separate `ServiceMonitor` watching port 3000 — two
independent observers with no shared state and no way to tell they refer to the
same process.

**Ephemeral registrations:** Running `coral connect` registers services in
memory. Agent restart loses all registrations. Users must re-run `coral connect`
on every start or script it externally.

**Use cases affected:**

- "What processes is coral watching right now?" — Only shows explicitly connected
  services, not beyla-discovered ones
- "I'm using `--monitor-all`, which services has coral auto-discovered?" — No
  agent-level answer; requires querying DuckDB
- "I want to name the service beyla found on port 6379" — Must run the
  misleadingly named `coral connect` with no indication it links to the existing
  beyla entry

## Solution

Three coordinated changes, each independently shippable:

1. **Rename `coral connect` → `coral watch`** — accurate name, `coral connect`
   kept as a backward-compatible alias permanently.

2. **Beyla → agent service map feedback loop** — parse the `service.name`
   attribute from inbound OTLP metrics to build a lightweight map of
   beyla-observed services (port → service name). Store this as "observed"
   entries in the agent service map alongside the existing "watched" entries.

3. **`coral agent services` command** — new agent-local command that shows the
   complete picture: both observed (beyla-only) and watched (explicit)
   services, with source attribution per entry.

**Key Design Decisions:**

- **`coral connect` stays as a permanent alias** — not deprecated, not removed.
  Any existing scripts, CI configs, and docs links continue to work unchanged.

- **Observed entries are read-only and lightweight** — no `ServiceMonitor`, no
  health check loop, no SDK discovery. Beyla already handles the eBPF
  instrumentation; the agent just surfaces what beyla reports via OTLP metadata.

- **Running `coral watch` on an already-observed port promotes it** — if beyla
  already tracks port 3000, `coral watch frontend:3000` links the name and
  starts a `ServiceMonitor`. No duplicate eBPF observation occurs (RFD 053
  already handles this via `UpdateDiscovery`).

- **Colony-level view is unaffected** — `coral query services` (RFD 084) is the
  colony-wide aggregate view. `coral agent services` is the agent-local view.
  They are complementary, not competing.

**Architecture Overview:**

```
coral watch frontend:3000
        │
        ▼
┌───────────────────────────────────────────────────┐
│                  Agent Service Map                 │
│                                                    │
│  Watched (agent.monitors)                          │
│  ┌──────────────────────────────────────────────┐ │
│  │ frontend  port:3000  healthy  ServiceMonitor │ │
│  │ api       port:8080  healthy  ServiceMonitor │ │
│  └──────────────────────────────────────────────┘ │
│                                                    │
│  Observed (from beyla OTLP metadata)               │
│  ┌──────────────────────────────────────────────┐ │
│  │ port:6379  (beyla name: redis-svc)           │ │
│  │ port:9090  (beyla name: prometheus)          │ │
│  │ port:5432  (beyla name: postgresql)          │ │
│  └──────────────────────────────────────────────┘ │
└───────────────────────────────────────────────────┘
        │
        ▼
coral agent services
NAME       PORT   STATUS    SOURCE     PID
frontend   3000   healthy   watched    12345
api        8080   healthy   watched    12346
           6379   -         observed   12347   (redis-svc)
           9090   -         observed   12348   (prometheus)
           5432   -         observed   12349   (postgresql)
```

### Component Changes

1. **CLI** (`internal/cli/agent/`):
   - Add `coral watch` command (copy of current `coral connect` logic)
   - Register `coral connect` as a hidden alias pointing to the same `RunE`
   - Add `coral agent services` subcommand
   - Remove duplicate help text mentioning "connect" semantics

2. **Agent OTLP receiver** (`internal/agent/beyla/`):
   - After OTLP metrics are stored to DuckDB, extract unique
     `(service.name, net.host.port)` pairs from span/metric attributes
   - Notify agent via a callback (`onBeylaServiceObserved`) of new
     port → name mappings

3. **Agent service map** (`internal/agent/agent.go`):
   - Add `observedServices map[int32]*ObservedService` alongside `monitors`
   - Implement `onBeylaServiceObserved` callback to populate it
   - Extend `ListServices` handler to merge both maps into the response
   - When `ConnectService` is called for a port already in `observedServices`,
     link the `ServiceMonitor` to the existing entry

4. **Protobuf** (`proto/coral/agent/v1/agent.proto`):
   - Add `DiscoverySource` enum to agent proto
   - Extend `ServiceStatus` response message with `discovery_source` and
     `observed_name` fields
   - Extend `ListServicesRequest` with optional `source_filter`

## Implementation Plan

### Phase 1: Rename `coral connect` → `coral watch`

- [ ] Create `internal/cli/agent/watch.go` as the new command, moving all logic
      from `connect.go`
- [ ] Register `coral connect` as a hidden alias (same `RunE`, same flags) so
      existing scripts break nothing
- [ ] Update all output strings and help text: replace "connecting" with
      "watching", "connect" with "watch"
- [ ] Update `internal/cli/agent/startup/services.go` registration

### Phase 2: Beyla OTLP feedback loop

- [ ] Add `DiscoverySource` enum and extend `ServiceStatus`/`ListServicesRequest`
      in `proto/coral/agent/v1/agent.proto`; regenerate protobuf
- [ ] Define `ObservedService` struct in `internal/agent/agent.go` holding port,
      beyla-reported service name, last-seen timestamp, and PID when available
- [ ] Add `observedServices map[int32]*ObservedService` and
      `onBeylaServiceObserved` callback to `Agent`
- [ ] Wire callback into the OTLP ingest path in `internal/agent/beyla/` to
      extract `(service.name, net.host.port)` from incoming spans/metrics before
      DuckDB write
- [ ] Extend `ConnectService` handler: when the requested port already exists in
      `observedServices`, populate `ServiceMonitor` with the known PID/binary
      from the observed entry

### Phase 3: `coral agent services` command and unified `ListServices`

- [ ] Extend `ListServices` RPC handler (`internal/agent/service_handler.go`) to
      merge `monitors` (watched) and `observedServices` (observed) into a single
      response, with `discovery_source` set per entry
- [ ] Add `coral agent services` subcommand (`internal/cli/agent/services.go`)
      with tabular output showing NAME, PORT, STATUS, SOURCE, PID columns
- [ ] Support `--source watched|observed|all` flag on `coral agent services`
- [ ] Ensure `coral agent status` continues to work unchanged (it shows agent
      health, not the service list)

### Phase 4: Testing and Documentation

- [ ] Unit tests: `TestObservedServicesPopulatedFromOTLP`,
      `TestConnectServicePromotesObservedEntry`,
      `TestListServicesMergesBothSources`
- [ ] Integration test: start agent with `--monitor-all`, send synthetic OTLP
      spans for port 6379, assert `coral agent services` lists port 6379 as
      observed
- [ ] Update `docs/CLI.md` and `docs/CLI_REFERENCE.md`: add `coral watch`, add
      `coral agent services`, note `coral connect` alias
- [ ] Update `docs/AGENT.md`: explain unified service map, observed vs watched
      distinction
- [ ] Update `docs/SERVICE_DISCOVERY.md`: document the beyla feedback loop and
      how observed entries are populated

## API Changes

### New Protobuf Definitions

```protobuf
// DiscoverySource indicates how a service was added to the agent service map.
enum DiscoverySource {
    DISCOVERY_SOURCE_UNSPECIFIED = 0;

    // Explicitly registered via `coral watch` (or `coral connect`).
    // Has a ServiceMonitor: health checks, SDK discovery, profiling.
    DISCOVERY_SOURCE_WATCHED = 1;

    // Auto-discovered from beyla eBPF OTLP telemetry.
    // No ServiceMonitor; beyla handles instrumentation.
    DISCOVERY_SOURCE_OBSERVED = 2;

    // Both watched and observed; the service was explicitly registered
    // and beyla is also producing telemetry for it.
    DISCOVERY_SOURCE_BOTH = 3;
}

// Extended ServiceStatus (agent.proto)
message ServiceStatus {
    string name = 1;
    int32 port = 2;
    string health_endpoint = 3;
    string service_type = 4;
    map<string, string> labels = 5;
    string status = 6;
    google.protobuf.Timestamp last_check = 7;
    string error = 8;
    int32 process_id = 9;
    string binary_path = 10;
    string binary_hash = 11;

    // NEW: Which source(s) produced this entry.
    DiscoverySource discovery_source = 12;

    // NEW: The service.name attribute reported by beyla in OTLP spans/metrics.
    // Only set when discovery_source includes OBSERVED.
    string observed_name = 13;
}

// Extended ListServicesRequest
message ListServicesRequest {
    // NEW: Filter by discovery source. Unset returns all sources.
    optional DiscoverySource source_filter = 1;
}
```

### CLI Commands

**Renamed primary command:**

```bash
# Watch one or more services (replaces `coral connect`)
coral watch frontend:3000
coral watch frontend:3000 api:8080:/health redis:6379

# Backward-compatible alias (permanent, not deprecated)
coral connect frontend:3000    # identical behavior

# Promote a beyla-observed port to a watched service
# (if port 6379 is already in observedServices, links to the existing entry)
coral watch redis:6379

# Remote agent support (unchanged)
coral watch frontend:3000 --agent hostname-api-1
coral watch frontend:3000 --agent-url http://10.42.0.5:9001
coral watch frontend:3000 --wait
```

**New agent-local services view:**

```bash
# Show all services the agent is aware of (watched + observed)
coral agent services

# Example output:
NAME        PORT   STATUS     SOURCE     PID
frontend    3000   healthy    watched    12345
api         8080   healthy    watched    12346
            6379   -          observed   12347   redis-svc
            9090   -          observed   12348   prometheus
            5432   -          observed   12349   postgresql

# Filter by source
coral agent services --source watched
coral agent services --source observed
```

**Output columns:**

- `NAME` — user-provided name for watched services; empty for observed-only
- `PORT` — TCP port
- `STATUS` — `healthy`/`unhealthy`/`unknown` for watched; `-` for observed
- `SOURCE` — `watched`, `observed`, or `both`
- `PID` — process ID when available from process discovery
- Trailing column — beyla-reported `service.name` for observed entries, when it
  differs from the user-provided name

## Testing Strategy

### Unit Tests

- `TestListServices_WatchedOnly` — only explicit monitors, no observed entries
- `TestListServices_ObservedOnly` — agent with `--monitor-all`, only beyla
  entries, no explicit connects
- `TestListServices_MixedSources` — both watched and observed, assert correct
  `discovery_source` per entry
- `TestConnectService_PromotesObserved` — observed entry on port 6379; calling
  `ConnectService` for port 6379 sets `discovery_source = BOTH` and seeds the
  `ServiceMonitor` with known PID
- `TestOTLPFeedback_PopulatesObserved` — synthetic OTLP span with
  `net.host.port=6379`, assert `observedServices[6379]` is populated

### Integration Tests

- Start agent with `--monitor-all`; send synthetic OTLP spans; assert
  `coral agent services` lists the discovered ports as observed
- Run `coral watch redis:6379` after the observed entry exists; assert
  `discovery_source` becomes `BOTH` and a `ServiceMonitor` is started

## Implementation Status

**Core Capability:** ⏳ Not Started

Rename `coral connect` to `coral watch`, surface beyla-discovered processes in
the agent service map, and add `coral agent services` for a unified local view.

## Future Work

**Declarative service configuration** (Future - RFD TBD)

A `coral.yaml` file loaded at agent startup to make service definitions
persistent across restarts, eliminating the need to re-run `coral watch` after
each agent restart. This is a natural follow-on once the service map model
established here is stable.

**Beyla-reported service name as default watch name** (Future)

When a user runs `coral watch` against a port that beyla already knows by name
(`observed_name`), auto-suggest or default to that name rather than requiring
the user to specify one.

**Agent-side `coral services label` shorthand** (Future)

A lighter-weight command for users who only want to attach a name to an observed
port without starting a `ServiceMonitor` (no health checks, no SDK discovery).
Deferred until there is clear demand for a zero-overhead naming path distinct
from `coral watch`.
