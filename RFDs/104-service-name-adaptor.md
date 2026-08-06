---
rfd: "104"
title: "ServiceNameAdaptor and Unified Service Map"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: false
dependencies: [ "064", "102", "103" ]
database_migrations: [ ]
areas: [ "agent", "service-discovery", "observability" ]
---

# RFD 104 - ServiceNameAdaptor and Unified Service Map

**Status:** 🚧 Draft

## Summary

Replace the agent's `monitors` map with a unified `services` map keyed by
port. Introduce a pluggable `ServiceNameAdaptor` chain that derives a stable
human-readable name for each observed process. Make function indexing lazy so
it fires on first explicit need rather than at discovery time.

## Problem

**Current behavior/limitations:**

- Auto-discovered processes (via `--monitor-all`) are anonymous — identifiable
  only by port number. There is no agent-level record of what process owns a
  port.
- `agent.monitors` only contains services that were explicitly connected via
  `coral connect`. Default-on observation (RFD 104) produces no entry in
  `monitors`.
- `FunctionCache.DiscoverAndCache` fires eagerly at service connect time,
  spending 100–500 ms of DWARF parsing per binary even when no debug session
  will ever be started.

**Why this matters:**

- With default-on observation there is no place to record auto-discovered
  services, so `coral services` (RFD 107) has no data to display.
- Eager function indexing at scale (tens of processes on a host) produces
  noticeable startup latency and wasted work.
- Naming is the prerequisite for topology: `coral query topology` joins on
  `service_name`; anonymous services produce no edges.

## Solution

A pluggable `ServiceNameAdaptor` interface allows the agent to derive a stable
name from process metadata. The built-in `ProcessNameAdaptor` uses binary name
from RFD 064 process info; it appends `-<port>` when two processes share a
binary name. The agent's unified `services` map records every observed process
regardless of whether it has been explicitly watched.

**Key Design Decisions:**

- **Adaptor chain, not a single resolver.** Future adaptors (Kubernetes pod
  labels, Docker Compose service labels) slot in at the front of the chain
  without modifying agent core.
- **`ProcessDiscoverer` (RFD 102) as the data source.** The adaptor reads
  process name from `/proc/<pid>/comm` via the shared `ProcessDiscoverer`
  component rather than duplicating `/proc` parsing.
- **Lazy `FunctionCache`.** Remove `DiscoverAndCache` from the service
  discovery path; add `EnsureIndexed` called only at debug session start or
  explicit function query.
- **`ServiceEntry` is the single record of truth** for a port. Auto and watched
  services differ only in which fields are populated, not in which map they
  live.

**Benefits:**

- Every listening process has a name immediately after first OTLP observation.
- No duplicate `/proc` parsing — `ProcessDiscoverer` (RFD 102) is the shared
  source.
- Function indexing cost moves from eager-at-discovery to lazy-at-need.
- RFD 106 (proto) and RFD 107 (CLI) have a well-defined data model to expose.

**Architecture Overview:**

```
onBeylaServiceObserved(port, pid, observedName)   ← RFD 104
        │
        ▼
ServiceNameAdaptor chain
  1. KubernetesAdaptor  (future)
  2. DockerAdaptor      (future)
  3. ProcessNameAdaptor (built-in)
        │  name = "node"  (unique binary name)
        │  name = "node-3000"  (conflict resolution)
        ▼
agent.services map[int32]*ServiceEntry
  ┌──────────────────────────────────────────────────────┐
  │  port:3000  autoName:"node"   tier:0  PID:12345      │
  │  port:6379  autoName:"redis"  tier:0  PID:12347      │
  └──────────────────────────────────────────────────────┘
        │
        │  coral services watch frontend:3000:/health
        ▼
  ┌──────────────────────────────────────────────────────┐
  │  port:3000  authName:"frontend"  tier:0+1  ✓monitor  │
  │  port:6379  autoName:"redis"     tier:0              │
  └──────────────────────────────────────────────────────┘
```

### Component Changes

1. **`ServiceNameAdaptor` interface** (`internal/agent/naming/`):
   - Define `ServiceNameAdaptor` interface:
     `Resolve(port int32, pid int32, binaryPath string) (name string, ok bool)`
   - Implement `ProcessNameAdaptor`: read binary name from `binaryPath`; append
     `-<port>` suffix when two entries share the same binary name.
   - Wire chain in agent constructor; `ProcessNameAdaptor` is the only
     built-in.

2. **`ServiceEntry`** (`internal/agent/`):
   - New struct with fields: `port`, `autoName`, `authoritativeName`,
     `namingSource`, `tier`, `pid`, `binaryPath`, `binaryHash`, `monitor`
     pointer.
   - `NamingSource` type: `Auto`, `Authoritative`.

3. **Agent service map** (`internal/agent/agent.go`):
   - Replace `monitors map[string]*ServiceMonitor` with
     `services map[int32]*ServiceEntry` keyed by port.
   - `onBeylaServiceObserved`: create `ServiceEntry` if absent; run adaptor
     chain on first observation; store `autoName`.
   - `ConnectService` handler: look up entry by port; set
     `authoritativeName` and `namingSource = Authoritative`; create
     `ServiceMonitor` only when `health_endpoint != ""`; fire CPU profiling
     callback only at `ServiceMonitor` creation (Tier 1).
   - Migrate all existing `monitors` lookups to the new map.

4. **`FunctionCache`** (`internal/agent/`):
   - Remove `DiscoverAndCache` call from `discoverProcessInfo`.
   - Add `EnsureIndexed(port int32) error` method called explicitly at debug
     session start and `ListFunctions` RPC invocation.
   - The binary-hash cache layer is unchanged; laziness only affects the
     trigger.

## Implementation Plan

### Phase 1: ServiceNameAdaptor and ServiceEntry

- [ ] Create `internal/agent/naming/` package
- [ ] Define `ServiceNameAdaptor` interface
- [ ] Implement `ProcessNameAdaptor`: unique binary name → name; conflict →
      `name-<port>`; no binary info → `port-<N>`
- [ ] Define `ServiceEntry` struct and `NamingSource` type
- [ ] Unit tests: unique process, conflicting names, unknown binary

### Phase 2: Unified service map

- [ ] Replace `agent.monitors` with `agent.services map[int32]*ServiceEntry`
- [ ] Implement `onBeylaServiceObserved`: create/update `ServiceEntry`, run
      adaptor chain on first observation
- [ ] Update `ConnectService` handler: look up by port, set authoritative name,
      create `ServiceMonitor` only when health endpoint provided
- [ ] Migrate all existing `monitors` references to the new map
- [ ] CPU profiling callback fires only at `ServiceMonitor` creation

### Phase 3: Lazy FunctionCache

- [ ] Remove `DiscoverAndCache` from `discoverProcessInfo`
- [ ] Add `EnsureIndexed(port int32) error` to `FunctionCache`
- [ ] Call `EnsureIndexed` at debug session start
- [ ] Call `EnsureIndexed` at `ListFunctions` RPC invocation

### Phase 4: Testing

- [ ] `TestProcessNameAdaptor_Unique` — single binary, returns binary name
- [ ] `TestProcessNameAdaptor_Conflict` — two `node` processes; both get
      `node-<port>` suffix
- [ ] `TestProcessNameAdaptor_Fallback` — no binary info; returns `port-<N>`
- [ ] `TestServiceMap_AutoFromOTLP` — OTLP callback fires; assert `ServiceEntry`
      created with `NamingSource=Auto`, `tier=0`
- [ ] `TestServiceMap_WatchWithHealthEnriches` — `ConnectService` with health
      endpoint on existing auto entry; assert authoritative name set,
      `has_monitor=true`, `tier=1`
- [ ] `TestServiceMap_WatchWithoutHealthNoMonitor` — `ConnectService` without
      health endpoint; assert `has_monitor=false`, `tier=0`
- [ ] `TestFunctionCache_LazyNotEager` — service observed; assert
      `DiscoverAndCache` NOT called; call `EnsureIndexed`; assert it IS called

## API Changes

No protobuf or RPC changes. Internal agent data model only. Proto exposure of
`ServiceEntry` fields is in RFD 106.

## Testing Strategy

### Unit Tests

See Phase 4 above.

### Integration Tests

- Start agent with default config; send synthetic OTLP spans for ports 3000
  and 6379; call internal `ListServices` equivalent; assert both entries
  present with auto-generated names and `tier=0`.
- Run `ConnectService` with health endpoint on port 3000; assert entry becomes
  `tier=1` with `ServiceMonitor` running.
- Run `ConnectService` without health endpoint on port 6379; assert no
  `ServiceMonitor` created.

## Implementation Status

**Core Capability:** ⏳ Not Started

## Future Work

**Kubernetes adaptor** (Future RFD)

`KubernetesAdaptor` queries the local kubelet or downward API to resolve pod
name, namespace, and service labels for a given PID. Registered first in the
chain; falls back to `ProcessNameAdaptor` when not in Kubernetes.

**Docker / Compose adaptor** (Future RFD)

`DockerAdaptor` reads container labels (e.g. `com.docker.compose.service`) to
derive service names in Compose environments.

**Re-resolution on adaptor change** (Future)

When a Kubernetes pod label changes, trigger re-resolution for affected
`ServiceEntry` records. Requires adaptors to support a notification interface.
