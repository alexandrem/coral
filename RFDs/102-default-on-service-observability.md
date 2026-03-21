---
rfd: "102"
title: "Default-On Service Observability"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "053", "064", "084" ]
database_migrations: [ ]
areas: [ "agent", "cli", "service-discovery", "observability" ]
---

# RFD 102 - Default-On Service Observability

**Status:** 🚧 Draft

## Summary

Make full process observation the default agent behaviour, introduce
auto-naming via a pluggable `ServiceNameAdaptor` chain, promote `coral
services` to a root-level command with both colony-wide and agent-local scopes,
and demote the explicit registration path (`coral services watch`, formerly
`coral connect`) to an enrichment operation rather than a prerequisite for
observability.

## Problem

### Observation is opt-in by default

Without `--monitor-all`, the agent collects nothing. Users must discover this
flag from the docs and pass it explicitly, which contradicts coral's
zero-configuration promise. The safe, ergonomic default should be to observe
everything; opting out for resource-constrained environments should be the
exception.

### Auto-discovered processes are anonymous and invisible

When `--monitor-all` is set, beyla captures RED metrics for every listening
process, but all entries are nameless — identifiable only by port. There is no
agent-level view of what has been discovered. Users cannot ask "what is coral
seeing right now?" without querying DuckDB directly.

### `coral connect` is both misnamed and misplaced

The command is named `connect`, implying it establishes a link, when it
actually registers an existing process for richer observation. It sits at the
root of the CLI while the related `coral colony service list` command is buried
two levels deep under `coral colony service list`. Services are a first-class
concept; their commands should be at the root.

### `coral colony service list` is hard to discover

Colony-level service listing lives at `coral colony service list` — three words
deep — while agent-level service detail has no CLI surface at all. There is no
single command that gives a unified picture of what coral is observing.

### Observation intensity is not tiered

All observability features activate at the same trigger point (service
connect), regardless of cost:

- Beyla RED metrics — single shared eBPF program, negligible per-service cost
- Continuous CPU profiling — 500 KB of BPF maps per process, ~1% CPU
- Function indexing — 100–500 ms of DWARF parsing per binary, async but eager

With monitor-all as the default, eager function indexing for every process on
the host is unacceptable. Observation intensity must be tiered by cost, with
heavier capabilities gated behind explicit need.

## Solution

Four coordinated changes:

**1. Default-on observation.** Remove the requirement to pass `--monitor-all`.
Beyla instruments all listening processes by default. A new `--no-monitor-all`
flag opts out for resource-constrained hosts. The old `--monitor-all` flag is
accepted as a no-op for backward compatibility.

**2. Auto-naming via `ServiceNameAdaptor`.** On first observation of a port,
the agent derives a stable human-readable name through a pluggable adaptor
chain. The built-in adaptor uses process metadata from RFD 064: binary name
when unique on the host, `<binary-name>-<port>` when two processes share a
binary name. Future adaptors (Kubernetes, Docker) slot in at the front of the
chain without modifying agent core.

**3. `coral services` at the root.** A new root-level command group that is the
single place to see and manage services at any scope:

- `coral services` — colony-wide list (replaces `coral colony service list`)
- `coral services --agent <id>` — agent-scoped list with local tier detail
- `coral services --local` — local agent without colony connectivity
- `coral services watch` — explicit enrichment (replaces `coral connect`)

`coral colony service list` and `coral connect` are kept as permanent
backward-compatible aliases.

**4. Observation tiering.** Three tiers with different activation thresholds:

| Tier | Capability | Default activation |
|---|---|---|
| 0 | Beyla RED metrics | All observed processes (default-on) |
| 1 | Continuous CPU profiling | Watched services only (`coral services watch`) |
| 2 | Function indexing | On first explicit need (debug session, AI query) |
| 3 | Memory profiling, uprobes | Explicit interactive |

Tiers 0 and 1 produce continuous data that enables the highest-value
correlation: slow beyla requests cross-referenced with CPU stack frames from
the same time window. Tier 2 (function indexing) is made truly lazy — it fires
on the first request that requires it, not at service discovery time.

**Key design decisions**

- **`coral connect` is a permanent alias**, not deprecated. Existing scripts,
  CI configs, and docs links continue to work unchanged.

- **`coral services` defaults to the list action.** Running `coral services`
  with no subcommand lists services, same as `coral services list`. The
  subcommand is available for explicitness but not required.

- **`coral services watch` without a health endpoint does not start a
  `ServiceMonitor`.** It attaches an authoritative name to an observed entry.
  A `ServiceMonitor` (health checks, SDK discovery) is only created when a
  health endpoint is provided.

- **`coral colony` becomes infrastructure-only.** The `service` subcommand is
  removed from `coral colony`. Colony retains: start/stop/status, agents, mcp,
  ca/psk/token, export/import, list/use/current/add-remote.

- **Colony-level and agent-level views are complementary, not competing.**
  Colony scope shows instances across agents and mesh IPs. Agent scope shows
  PIDs, binary names, auto-names vs authoritative names, and tier state.

**Architecture Overview:**

```
Agent start (default: --monitor-all implicit)
        │
        ▼
┌───────────────────────────────────────────────────────────────┐
│                       Beyla (Tier 0)                          │
│           Instruments all listening processes                  │
│   port 3000 → OTLP spans/metrics → agent OTLP receiver        │
└──────────────────────────┬────────────────────────────────────┘
                           │  onBeylaServiceObserved(port, pid, name)
                           ▼
┌───────────────────────────────────────────────────────────────┐
│                  ServiceNameAdaptor chain                      │
│   1. KubernetesAdaptor  (future: pod labels)                  │
│   2. DockerAdaptor      (future: compose service labels)      │
│   3. ProcessNameAdaptor (built-in: binary name + port)        │
└──────────────────────────┬────────────────────────────────────┘
                           │
                           ▼
┌───────────────────────────────────────────────────────────────┐
│                    Agent Service Map                           │
│                                                               │
│  node-3000       port:3000  auto    Tier 0   PID:12345        │
│  redis-server    port:6379  auto    Tier 0   PID:12347        │
│  prometheus      port:9090  auto    Tier 0   PID:12348        │
│  postgresql      port:5432  auto    Tier 0   PID:12349        │
└──────────────────────────┬────────────────────────────────────┘
                           │
            coral services watch frontend:3000:/health
                           │
                           ▼
┌───────────────────────────────────────────────────────────────┐
│                    Agent Service Map                           │
│                                                               │
│  frontend        port:3000  watched  Tier 0+1  PID:12345  ✓h │
│  redis-server    port:6379  auto     Tier 0    PID:12347      │
│  prometheus      port:9090  auto     Tier 0    PID:12348      │
│  postgresql      port:5432  auto     Tier 0    PID:12349      │
└───────────────────────────────────────────────────────────────┘
```

### Component Changes

1. **Agent startup** (`internal/cli/agent/startup/`):
   - Remove the `--monitor-all` gate; beyla is default-enabled with
     `open_ports: "1-65535"` unless `--no-monitor-all` is passed
   - Accept `--monitor-all` as a no-op with a deprecation warning
   - Remove `--connect` flag from startup (superseded by default observation)

2. **Beyla manager** (`internal/agent/beyla/`):
   - On each OTLP ingest, extract `(service.name, net.host.port, pid)` and
     fire `onBeylaServiceObserved` on the agent

3. **ServiceNameAdaptor** (`internal/agent/naming/`):
   - Define `ServiceNameAdaptor` interface
   - Implement `ProcessNameAdaptor`: binary name from RFD 064 process info;
     append `-<port>` suffix when two processes share the same binary name

4. **FunctionCache** (`internal/agent/`):
   - Change trigger from async-eager (on service connect) to lazy (on first
     explicit need: debug session start, `ListFunctions` RPC, AI query)
   - Keep the binary-hash cache layer unchanged; laziness only affects the
     trigger, not the caching strategy

5. **Agent service map** (`internal/agent/agent.go`):
   - Replace `monitors map[string]*ServiceMonitor` with unified
     `services map[int32]*ServiceEntry` keyed by port
   - `ServiceEntry` holds: auto-name, authoritative name, naming source, tier
     state, PID, binary info, optional `*ServiceMonitor`
   - `onBeylaServiceObserved`: create/update entry; run adaptor chain on first
     observation
   - `ConnectService` handler: update `authoritativeName`; create
     `ServiceMonitor` only when `health_endpoint != ""`
   - CPU profiling callback fires only when a `ServiceMonitor` is created (Tier
     1 is watched-only)

6. **Agent proto** (`proto/coral/agent/v1/agent.proto`):
   - Add `ServiceNamingSource` enum
   - Add `ObservationTier` field to `ServiceStatus`
   - Extend `ServiceStatus` with `auto_name`, `authoritative_name`,
     `naming_source`, `has_monitor`

7. **CLI: `coral services` root command** (`internal/cli/services/`):
   - New package `internal/cli/services/`
   - `coral services` (default: list action, colony-wide) replaces
     `coral colony service list`
   - `coral services --agent <id>`: route request to that agent's `ListServices`
     gRPC via mesh IP resolution (RFD 044); render agent-local columns
   - `coral services --local`: hit `localhost:9001` directly, no colony needed
   - `coral services watch`: move logic from `internal/cli/agent/connect.go`
   - Register `coral colony service list` as a hidden alias
   - Register `coral connect` as a hidden alias for `coral services watch`
   - Remove `newServiceCmd()` from `internal/cli/colony/commands.go`
   - Remove `agent.NewConnectCmd()` from `internal/cli/root.go`

## Implementation Plan

### Phase 1: Default-on observation and OTLP feedback loop

- [ ] Remove `--monitor-all` gate from agent startup; set beyla as
      default-enabled; accept `--monitor-all` as a no-op with warning
- [ ] Add `--no-monitor-all` flag to `coral agent start`
- [ ] Add `onBeylaServiceObserved(port int32, pid int32, observedName string)`
      callback to `Agent`
- [ ] Wire callback into the beyla manager OTLP ingest path: extract
      `net.host.port` (or `server.port`) and `service.name` from each incoming
      span/metric resource before DuckDB write

### Phase 2: ServiceNameAdaptor and unified service map

- [ ] Define `ServiceNameAdaptor` interface in `internal/agent/naming/`:
      `Resolve(port int32, pid int32, binaryPath string) (name string, ok bool)`
- [ ] Implement `ProcessNameAdaptor`: read binary name from `binaryPath`; if
      two entries share a binary name, append `-<port>` to disambiguate both
- [ ] Define `ServiceEntry` struct: port, autoName, authoritativeName,
      namingSource, tier, PID, binaryPath, binaryHash, monitor pointer
- [ ] Replace `agent.monitors` with `agent.services map[int32]*ServiceEntry`;
      migrate all existing lookups and callbacks
- [ ] Implement `onBeylaServiceObserved`: create/update `ServiceEntry`, run
      adaptor chain on first observation, cache result
- [ ] Update `ConnectService` handler: look up entry by port, set
      `authoritativeName`, create `ServiceMonitor` only when health endpoint
      provided; fire CPU profiling callback only at `ServiceMonitor` creation
- [ ] Make `FunctionCache` trigger lazy: remove the `DiscoverAndCache` call from
      `discoverProcessInfo`; add an explicit `EnsureIndexed` method called at
      debug session start and function query time

### Phase 3: Proto and agent `ListServices` update

- [ ] Add `ServiceNamingSource` enum and extend `ServiceStatus` with
      `auto_name`, `authoritative_name`, `naming_source`, `has_monitor`,
      `observation_tier` in `proto/coral/agent/v1/agent.proto`; regenerate
- [ ] Update `ListServices` RPC handler to merge all `ServiceEntry` records
      (auto and watched) into the response with correct field population

### Phase 4: `coral services` root command

- [ ] Create `internal/cli/services/` package with `root.go`, `list.go`,
      `watch.go`
- [ ] `list.go`: colony-wide list by default (calls colony `ListServices`);
      `--agent <id>` routes to agent `ListServices` via mesh IP lookup;
      `--local` hits `localhost:9001`; output columns adapt by scope:
      - Colony scope: SERVICE, TYPE, INSTANCES, SOURCE, AGENTS
      - Agent scope: NAME, PORT, SOURCE, TIER, PID, HEALTH
- [ ] `watch.go`: move logic from `internal/cli/agent/connect.go`; update
      help text to describe enrichment semantics
- [ ] Register `coral services` in `internal/cli/root.go`; remove
      `agent.NewConnectCmd()` registration from root
- [ ] Register hidden aliases: `coral connect` → `coral services watch`;
      `coral colony service list` → `coral services list`
- [ ] Remove `newServiceCmd()` from `internal/cli/colony/commands.go`
- [ ] Update `coral agent status` to remove the service list section; it
      shows agent health and component state only

### Phase 5: Testing and Documentation

- [ ] Unit tests: `TestProcessNameAdaptor_Unique`,
      `TestProcessNameAdaptor_Conflict`, `TestProcessNameAdaptor_Fallback`,
      `TestServiceMap_AutoFromOTLP`, `TestServiceMap_WatchEnriches`,
      `TestServiceMap_WatchNoHealthNoMonitor`,
      `TestFunctionCache_LazyNotEagerOnDiscovery`
- [ ] Integration test: start agent with default config; send synthetic OTLP
      spans for two ports; assert `coral services --local` lists both with
      auto-generated names and tier `0`
- [ ] Integration test: run `coral services watch frontend:3000:/health`; assert
      name becomes `frontend`, `ServiceMonitor` starts, tier becomes `0+1`
- [ ] Integration test: run `coral services watch redis:6379` (no health
      endpoint); assert no `ServiceMonitor` started
- [ ] E2E test: verify `coral colony service list` alias produces identical
      output to `coral services`
- [ ] E2E test: verify `coral connect frontend:3000` alias produces identical
      output to `coral services watch frontend:3000`
- [ ] Update `docs/CLI.md` and `docs/CLI_REFERENCE.md`: document `coral
      services`, `coral services watch`, `--no-monitor-all`; note aliases
- [ ] Update `docs/AGENT.md`: document default-on observation, tier model,
      auto-naming, lazy function indexing
- [ ] Update `docs/SERVICE_DISCOVERY.md`: document `ServiceNameAdaptor`
      interface and built-in process adaptor

## API Changes

### New Protobuf Definitions

```protobuf
// ServiceNamingSource indicates how the effective service name was determined.
enum ServiceNamingSource {
    SERVICE_NAMING_SOURCE_UNSPECIFIED = 0;

    // Name generated by the ServiceNameAdaptor chain (process binary name
    // or port-based fallback).
    SERVICE_NAMING_SOURCE_AUTO = 1;

    // Name explicitly set by the operator via `coral services watch`.
    SERVICE_NAMING_SOURCE_AUTHORITATIVE = 2;

    // Name resolved by an external adaptor (e.g., Kubernetes pod labels).
    // Reserved for future adaptor implementations.
    SERVICE_NAMING_SOURCE_ADAPTOR = 3;
}

// Extended ServiceStatus (agent.proto)
message ServiceStatus {
    string name = 1;              // Effective name: authoritative ?? auto
    int32  port = 2;
    string health_endpoint = 3;   // Only set for watched services
    string service_type = 4;
    map<string, string> labels = 5;
    string status = 6;            // healthy/unhealthy/unknown; empty for Tier 0 only
    google.protobuf.Timestamp last_check = 7;
    string error = 8;
    int32  process_id = 9;
    string binary_path = 10;
    string binary_hash = 11;

    // NEW
    string auto_name = 12;                     // Name from adaptor chain
    string authoritative_name = 13;            // Name from coral services watch (if set)
    ServiceNamingSource naming_source = 14;
    bool   has_monitor = 15;                   // True if ServiceMonitor is running (Tier 1)
    uint32 observation_tier = 16;              // Highest active tier (0, 1, 2, ...)
}

// Extended ListServicesRequest
message ListServicesRequest {
    // Filter by naming source. Unset returns all.
    optional ServiceNamingSource source_filter = 1;
}
```

### CLI Commands

**Agent startup:**

```bash
# Default: observe everything (no flag needed)
coral agent start

# Opt out on resource-constrained hosts
coral agent start --no-monitor-all

# Legacy flag accepted, now a no-op (emits deprecation warning)
coral agent start --monitor-all
```

**`coral services` — unified service view:**

```bash
# Colony-wide list (default action, replaces coral colony service list)
coral services

# Example colony-scope output:
Services (4) at 2026-03-20 14:23:00 UTC:

SERVICE        TYPE   INSTANCES  SOURCE     AGENTS
api            http   2          VERIFIED   agent-eu-1 (✓), agent-us-1 (✓)
frontend       http   1          VERIFIED   agent-eu-1 (✓)
redis-server   -      1          OBSERVED   agent-eu-1 (?)
postgresql     -      1          OBSERVED   agent-us-1 (?)

# Agent-scoped view (richer local detail)
coral services --agent agent-eu-1

# Example agent-scope output:
Services on agent-eu-1:

NAME           PORT   SOURCE   TIER   PID     HEALTH
frontend       3000   watched  0+1    12345   healthy
api            8080   watched  0+1    12346   healthy
redis-server   6379   auto     0      12347   -
postgresql     5432   auto     0      12349   -

# Local agent (no colony required)
coral services --local

# Filter flags (both scopes)
coral services --source auto
coral services --source watched
coral services --agent agent-eu-1 --source auto
```

**`coral services watch` — explicit enrichment:**

```bash
# Name an observed service (no ServiceMonitor created)
coral services watch redis:6379

# Name + health endpoint (creates ServiceMonitor, activates Tier 1)
coral services watch frontend:3000:/health

# Name + health + type
coral services watch api:8080:/healthz:http

# Multiple services
coral services watch frontend:3000:/health api:8080:/healthz redis:6379

# Target a remote agent
coral services watch frontend:3000 --agent agent-eu-1
coral services watch frontend:3000 --agent-url http://10.42.0.5:9001

# Permanent backward-compatible aliases (identical behaviour)
coral connect frontend:3000:/health
coral watch frontend:3000:/health       # if previously introduced
```

**`coral colony` after cleanup** (infrastructure only):

```bash
coral colony start / stop / status     # lifecycle
coral colony agents                    # agent registry
coral colony mcp                       # MCP server management
coral colony ca / psk / token          # security
coral colony list / use / current      # context switching
coral colony add-remote                # multi-colony (RFD 031)
# coral colony service  ← removed; use coral services
```

### Configuration

```yaml
# coral.yaml (agent config)
agent:
  # Default: true. Set false to disable beyla on resource-constrained hosts.
  monitor_all: true
```

## Testing Strategy

### Unit Tests

- `TestProcessNameAdaptor_UniqueProcess` — single binary, returns binary name
- `TestProcessNameAdaptor_ConflictingNames` — two `node` processes on different
  ports; both get `node-<port>` suffix
- `TestProcessNameAdaptor_UnknownBinary` — no binary info; falls back to
  `port-<N>`
- `TestServiceMap_AutoPopulatedFromOTLP` — OTLP callback fires; assert
  `ServiceEntry` created with `naming_source = AUTO`, `observation_tier = 0`
- `TestServiceMap_WatchWithHealthEnriches` — `ConnectService` with health
  endpoint on existing auto entry; assert `authoritativeName` set,
  `has_monitor = true`, `observation_tier = 1`
- `TestServiceMap_WatchWithoutHealthNoMonitor` — `ConnectService` without
  health endpoint; assert `has_monitor = false`, `observation_tier = 0`
- `TestFunctionCache_LazyNotEager` — simulate service discovery; assert
  `DiscoverAndCache` is NOT called; call `EnsureIndexed`; assert it IS called

### Integration Tests

- Start agent with default config; send synthetic OTLP spans for ports 3000
  and 6379; assert `coral services --local` lists both with auto-generated
  names and `TIER 0`
- Run `coral services watch frontend:3000:/health`; assert port 3000 entry now
  has `NAME=frontend`, `SOURCE=watched`, `TIER=0+1`, `HEALTH=healthy`
- Run `coral services watch redis:6379` (no health endpoint); assert no
  `ServiceMonitor` started for port 6379
- Run `coral colony service list`; assert output matches `coral services`
- Run `coral connect frontend:3000`; assert output matches
  `coral services watch frontend:3000`

## Implementation Status

**Core Capability:** ⏳ Not Started

Default-on observation, auto-naming via `ServiceNameAdaptor`, `coral services`
as the unified root-level service command with colony and agent scopes, and lazy
function indexing.

## Future Work

**Kubernetes adaptor** (Future - RFD TBD)

`KubernetesAdaptor` queries the local kubelet or downward API to resolve pod
name, namespace, and service labels for a given PID. Registered first in the
chain; falls back to `ProcessNameAdaptor` when not in Kubernetes.

**Docker / Compose adaptor** (Future - RFD TBD)

`DockerAdaptor` reads container labels (e.g., `com.docker.compose.service`) to
derive service names in Compose environments.

**Declarative service config** (Future - RFD TBD)

`coral.yaml` port-to-name mappings as an alternative to running `coral services
watch` at runtime. Useful in automated deployments:

```yaml
agent:
  services:
    - port: 3000
      name: frontend
      health: /health
```

**`--no-monitor-all` with explicit port allowlist** (Future)

When opting out of full observation, allow specifying ports to monitor instead
of all-or-nothing:

```bash
coral agent start --no-monitor-all --monitor-ports 3000,8080
```

**Re-resolution on adaptor change** (Future)

When a Kubernetes pod label changes (e.g., canary deploy), trigger
re-resolution for affected service map entries. Requires adaptor implementations
to support a notification interface.
