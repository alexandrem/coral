---
rfd: "102"
title: "Pluggable Service Discovery Providers"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: false
dependencies: [ "053", "036" ]
database_migrations: [ ]
areas: [ "observability", "ebpf", "beyla", "service-discovery" ]
---

# RFD 102 - Pluggable Service Discovery Providers

**Status:** 🚧 Draft

## Summary

Replace Beyla's `MonitorAll` catch-all port range (`open_ports: 1-65535`) with
a pluggable `ProcessDiscoveryProvider` interface. A `DiscoveryManager` runs
multiple providers concurrently, merges their results by priority, and feeds
`generateBeylaConfig` with complete process information — including client-only
processes that never bind a listening socket. The initial built-in providers
cover all Linux environments without external dependencies: `ProcFSProvider`
(socket table + full process scan) and `EnvVarProvider` (`OTEL_SERVICE_NAME`
from `/proc/<pid>/environ`).

## Problem

**Current behavior/limitations:**

When `MonitorAll` is enabled and no explicit service configuration is provided,
`generateBeylaConfig` emits a single Beyla discovery rule:

```yaml
discovery:
  services:
    - open_ports: "1-65535"
```

Beyla groups every process that matches the *same* rule into one logical service
instance, naming it after the first binary it encounters (typically
`coral-agent`). The result is that every call between `otel-app → cpu-app`
appears in the trace data as originating from `coral-agent`, producing a
spurious edge in the topology graph.

The topology materialization SQL JOIN — `child.service_name !=
parent.service_name` — always returns zero rows when all spans share one name,
so `coral query topology` reports no connections even when traffic is flowing.

Beyond the naming problem, the socket-table approach has three structural gaps:

1. **Client-only processes are invisible.** Workers, consumers, and batch jobs
   that make outbound calls but never bind a port do not appear in the socket
   table. They fall into the residual catch-all rule, get grouped under a
   single anonymous name, and produce no topology edges.
2. **The naming signal is weak.** `/proc/<pid>/comm` returns the binary name,
   which is fine for bare-metal but wrong for Docker Compose (where the
   container label is the canonical name) or Kubernetes (where the pod name is).
   There is no way to inject a richer name source without modifying the
   discovery core.
3. **Discovery is not extensible.** Adding Docker or Kubernetes awareness
   requires forking the discovery loop rather than plugging in a new provider.

**Why this matters:**

- `MonitorAll` is the zero-configuration experience for new users: no YAML
  required, plug and play. If it silently breaks topology, users have no way to
  diagnose missing edges without reading internals.
- Named service rules (the current workaround) require the user to know all
  ports in advance, which defeats the purpose of automatic discovery.
- eBPF probe conflicts arise when named rules and a broad catch-all match the
  same process simultaneously, causing Beyla to attach uprobes multiple times
  and drop all spans.
- Distributed systems routinely include pure-client processes (Kafka consumers,
  cron jobs, async workers). Missing them means missing edges in the topology
  graph and silent blind spots.

**Previously investigated alternatives:**

- `discovery.system_wide: true` — drops uprobe support entirely; Go binaries
  receive no traces.
- Catch-all via `exe_path: ".*"` — same single-rule grouping problem as
  the port range; Beyla still merges all matches under one name.
- Named rules in E2E fixture config — effective workaround for known
  environments, not a general solution.

**Use cases affected:**

- New users running `coral agent start` for the first time see no topology data.
- Any service that only makes outbound HTTP/gRPC calls (worker, consumer,
  proxy) is unobserved in MonitorAll mode.
- Users running Docker Compose see binary names instead of service labels.
- There is no supported path to add Kubernetes pod-name resolution without a
  core change.

## Solution

Define a `ProcessDiscoveryProvider` interface that abstracts process discovery.
A `DiscoveryManager` runs all active providers on a poll interval, merges
their `ProcessCandidate` results (first non-empty name wins, in priority
order), detects changes, and triggers the existing debounced Beyla restart
(RFD 053) on change. `generateBeylaConfig` maps candidates with ports to
`open_ports` rules and client-only candidates to `exe_path` rules.

**Key Design Decisions:**

- **Interface-first, not fork-first.** Adding a new environment (Docker,
  Kubernetes, bare-metal) means implementing one interface, not modifying
  discovery logic. The `Probe()` method allows providers to self-declare
  availability, enabling zero-config auto-detection.
- **ProcFSProvider covers both servers and clients.** The socket table scan
  (`/proc/net/tcp[6]`) discovers listening servers; a full `/proc/<pid>/comm`
  walk catches every other running process as client-only. Together they give
  complete host visibility with no external dependencies.
- **EnvVarProvider is the universal name hint.** Reading `OTEL_SERVICE_NAME`
  from `/proc/<pid>/environ` is lightweight, requires no sockets or external
  APIs, and respects operator-set names in any environment. It ranks above
  binary name but below environment-specific providers.
- **Priority chain, not last-write-wins.** Providers are ordered. The first
  provider that returns a non-empty name for a candidate wins. This allows
  future providers (Docker, Kubernetes) to slot in at higher priority without
  touching lower-priority implementations.
- **`exe_path` rules for client-only processes.** Beyla's discovery rules
  support `open_ports` and `exe_path` (a regex match against the process
  executable path); there is no `executable_name` field. Client-only
  processes have no port to match on, so `exe_path` is the correct rule type
  — the same field `generateBeylaConfig` already uses today for named
  service rules (`.*<name>.*`). This requires no Beyla changes.
- **Explicit `coral connect` registrations are a pinned candidate source, not
  a separate update path.** RFD 053 added `Manager.UpdateDiscovery(serviceMap
  map[int]string)`, called directly by the `ConnectService`/`DisconnectService`
  RPC handlers with a port→name map. That signature cannot represent
  client-only candidates because it is keyed by port. This RFD replaces it
  with `Manager.SetStaticCandidates(candidates []discovery.ProcessCandidate)`,
  so a `coral connect` registration becomes just another candidate — highest
  priority, always present regardless of poll timing — that `DiscoveryManager`
  merges alongside `EnvVarProvider` and `ProcFSProvider` on every cycle. This
  keeps a single merge point, a single change-detection pass, and a single
  debounced restart, instead of two independent code paths racing to
  reconfigure Beyla.

**Benefits:**

- Topology edges (`otel-app → cpu-app`) appear immediately in `coral query
  topology` without any user configuration.
- No eBPF probe conflicts: each process is matched by exactly one rule.
- Client-only processes (workers, consumers) appear in topology immediately.
- `OTEL_SERVICE_NAME` environment variables are respected automatically.
- Adding Docker Compose or Kubernetes support in a future RFD is a one-provider
  addition with no changes to discovery core logic.

**Architecture Overview:**

```
coral-agent (MonitorAll=true)
  │
  ├─ ConnectService/DisconnectService RPC
  │    └─ DiscoveryManager.SetStaticCandidates() → pinned, highest-priority candidates
  │
  ├─ DiscoveryManager (poll on sync_interval)
  │    ├─ EnvVarProvider.Probe() → true (always)
  │    │    └─ /proc/<pid>/environ → OTEL_SERVICE_NAME
  │    ├─ ProcFSProvider.Probe() → true (always)
  │    │    ├─ /proc/net/tcp[6] → listening ports → server candidates
  │    │    └─ /proc/<pid>/comm → all processes → client-only candidates
  │    │
  │    ├─ merge by PID (priority: static (coral connect) > EnvVar name >
  │    │    ProcFS binary name)
  │    ├─ detect added/removed candidates
  │    └─ Manager.applyDiscovery(candidates) → debounced Beyla restart (RFD 053)
  │
  └─ generateBeylaConfig([]ProcessCandidate)
       ├─ server candidates  → open_ports rules
       │    - name: otel-app
       │      open_ports: "8090"
       ├─ client-only candidates → exe_path rules
       │    - name: kafka-consumer
       │      exe_path: ".*kafka-consumer.*"
       └─ unresolved fallback → residual catch-all (if any)
```

### Component Changes

1. **`internal/agent/beyla/discovery/`** (new package):

    - Define `ProcessDiscoveryProvider` interface with `Probe`, `Discover`,
      and `Name` methods.
    - Define `ProcessCandidate` struct: PID, listening ports, name hint,
      source provider name, labels map, client-only flag.
    - Implement `DiscoveryManager`: provider registration, a pinned static
      candidate slot fed by `SetStaticCandidates` (always present, immune to
      poll-cycle aging, highest priority), priority-ordered merge, change
      detection, and an `applyDiscovery` callback on change.
    - Implement `ProcFSProvider`: socket table scan (`/proc/net/tcp[6]`) plus
      full `/proc/<pid>/comm` walk; marks candidates with no listening port as
      client-only.
    - Implement `EnvVarProvider`: reads `/proc/<pid>/environ`; extracts
      `OTEL_SERVICE_NAME` or `SERVICE_NAME`; returns name hints only (no
      port data).

2. **`internal/agent/beyla/Manager`** (config generation):

    - Update `generateBeylaConfig` to accept `[]ProcessCandidate`.
    - Emit `exe_path` service rules (regex-anchored to the process/service
      name, `.*<name>.*`, matching the pattern already used for named service
      rules today) for candidates with `IsClientOnly=true`.
    - Emit `open_ports` service rules for candidates with listening ports.
    - Residual catch-all scoped to ports not covered by any named rule; omitted
      entirely when all processes are resolved.
    - Static `ServiceMap` entries override auto-discovered names for the same
      port.

3. **`internal/agent/beyla/Manager`** (sync loop and dynamic connects):

    - Replace the existing poll goroutine with a call to
      `DiscoveryManager.Run()`.
    - Wire `DiscoverySyncInterval` and `DiscoveryProviders` config fields
      through to `DiscoveryManager`.
    - Replace `UpdateDiscovery(serviceMap map[int]string)` with
      `SetStaticCandidates(candidates []discovery.ProcessCandidate)`; update
      the `ConnectService`/`DisconnectService` handlers (`internal/agent/agent.go`)
      to build a `ProcessCandidate` per connected service (`IsClientOnly=false`,
      single-element `Ports`) instead of a raw port map.
    - `DiscoveryManager` merges static candidates and provider candidates into
      one table and drives the existing debounced-restart logic (RFD 053)
      from that single merge, so a poll tick and a `coral connect` call never
      trigger independent, racing restarts.

**Configuration:**

```yaml
beyla:
  monitor_all: true
  # How often DiscoveryManager polls all providers.
  discovery_sync_interval: 30s  # default

  # Per-provider enable/disable. "auto" means enabled if Probe() returns true.
  discovery_providers:
    procfs: enabled   # /proc/net/tcp + /proc/<pid>/comm  (default: enabled)
    envvar: enabled   # OTEL_SERVICE_NAME from /proc environ (default: enabled)
```

## Implementation Plan

### Phase 1: Provider interface and DiscoveryManager

- [ ] Create `internal/agent/beyla/discovery/` package
- [ ] Define `ProcessDiscoveryProvider` interface (`Probe`, `Discover`, `Name`)
- [ ] Define `ProcessCandidate` struct with all fields
- [ ] Implement `DiscoveryManager`: provider list, a pinned static-candidate
      slot (`SetStaticCandidates`, priority above all providers, not aged out
      by a poll cycle), priority merge by PID, change detection (added/removed
      PIDs), `applyDiscovery` callback on change
- [ ] Unit tests: merge with two providers (higher-priority name wins), static
      candidate always wins over provider candidates for the same PID, static
      candidate survives a poll cycle where no provider reports that PID,
      change detection fires callback on add/remove, no callback when map
      unchanged

### Phase 2: ProcFSProvider

- [ ] Implement `ProcFSProvider` in `internal/agent/beyla/discovery/procfs.go`
- [ ] Socket table path (`/proc/net/tcp[6]`): resolve inodes → PIDs → names
- [ ] Process walk path: scan `/proc/<pid>/comm` for all running PIDs; mark
      candidates not found in socket table as `IsClientOnly=true`
- [ ] `Probe()` always returns `true` (Linux-only, but agent is Linux-only)
- [ ] Unit tests: server process (appears in socket table), client-only process
      (comm scan only), process exits between scans (graceful skip), IPv6 hex
      parsing

### Phase 3: EnvVarProvider

- [ ] Implement `EnvVarProvider` in `internal/agent/beyla/discovery/envvar.go`
- [ ] Read `/proc/<pid>/environ` (null-delimited) for each known PID
- [ ] Extract `OTEL_SERVICE_NAME` first, fall back to `SERVICE_NAME`
- [ ] Return `ProcessCandidate` with name hint and PID only (no port data)
- [ ] `Probe()` always returns `true`
- [ ] Unit tests: env var present, env var absent, malformed environ file,
      process disappears between scan and read

### Phase 4: Integration, config, and documentation

- [ ] Wire `DiscoveryManager` into `Manager.Start()`
- [ ] Replace `Manager.UpdateDiscovery(map[int]string)` with
      `Manager.SetStaticCandidates([]discovery.ProcessCandidate)`; update the
      `ConnectService`/`DisconnectService` handlers in
      `internal/agent/agent.go` to build candidates instead of a port map
- [ ] Update `generateBeylaConfig` to accept `[]ProcessCandidate`; emit
      `exe_path` rules (`.*<name>.*`) for `IsClientOnly=true` candidates
- [ ] Add `DiscoverySyncInterval` and `DiscoveryProviders` to `Config` struct
- [ ] Unit tests for `generateBeylaConfig`: server rule, client-only rule,
      residual catch-all absent when all ports resolved, static `ServiceMap`
      override
- [ ] Unit tests for the static/provider merge: a `coral connect`-registered
      service and a provider-discovered service coexist without duplicate
      rules or restart races
- [ ] Update E2E topology test to use `MonitorAll` without a fixture config
      file; assert `coral query topology` returns the `otel-app → cpu-app`
      edge within the polling window
- [ ] E2E test: run a client-only process (no listening port) with
      `OTEL_SERVICE_NAME` set; assert Beyla config contains an `exe_path`
      rule for it
- [ ] Update `docs/AGENT.md`: document `MonitorAll` behaviour,
      `discovery_sync_interval`, and `discovery_providers` config keys
- [ ] Update `docs/SERVICE_DISCOVERY.md`: document provider priority chain
      and `OTEL_SERVICE_NAME` support

## API Changes

No protobuf or RPC changes. `Manager.UpdateDiscovery(map[int]string)` is
replaced by `Manager.SetStaticCandidates([]discovery.ProcessCandidate)` — an
internal Go API change only, not a wire-protocol change; `ConnectService` and
`DisconnectService` RPCs are unaffected.

### Configuration Changes

New fields in agent `beyla` config block:

```yaml
beyla:
  monitor_all: true
  discovery_sync_interval: 30s

  discovery_providers:
    procfs: enabled   # always-on fallback
    envvar: enabled   # reads OTEL_SERVICE_NAME from process environment
```

### Generated Beyla YAML (before → after)

**Before (MonitorAll, catch-all):**

```yaml
discovery:
  services:
    - open_ports: "1-65535"
```

**After (named rules per process, client-only via exe_path):**

```yaml
discovery:
  services:
    - name: otel-app
      open_ports: "8090"
    - name: cpu-app
      open_ports: "8080"
    - name: kafka-consumer         # OTEL_SERVICE_NAME or binary name
      exe_path: ".*kafka-consumer.*"
    # no residual catch-all when all processes are resolved
```

## Testing Strategy

### Unit Tests

- `DiscoveryManager`: merge priority (higher-priority provider name wins);
  static candidates (`SetStaticCandidates`) always outrank provider
  candidates and survive a poll cycle with no matching provider result;
  change detection fires callback on add, remove, name change; idempotent on
  unchanged map.
- `ProcFSProvider`: server candidate has correct port list; client-only
  candidate has `IsClientOnly=true`; disappeared PID handled gracefully;
  IPv6 hex address parsing.
- `EnvVarProvider`: `OTEL_SERVICE_NAME` extracted correctly; falls back to
  `SERVICE_NAME`; absent env var returns empty name (not an error).
- `generateBeylaConfig`: `open_ports` rule for server; `exe_path` rule for
  client-only; catch-all absent when all processes resolved; static
  `ServiceMap` overrides auto-discovered name.

### Integration Tests

- Start a TCP listener on a known port; run `ProcFSProvider.Discover()`; assert
  listener PID appears with correct port and `IsClientOnly=false`.
- Run a process with no listening socket and `OTEL_SERVICE_NAME=my-worker` in
  its environment; run full `DiscoveryManager` poll; assert candidate has
  `Name="my-worker"` and `IsClientOnly=true`.
- Call `SetStaticCandidates` with a `coral connect`-style entry, then run a
  `DiscoveryManager` poll that also discovers the same PID via `ProcFSProvider`
  with a different name; assert the static name wins and only one Beyla
  restart is triggered.

### E2E Tests

- Remove explicit `agent-0-config.yaml` fixture from the topology E2E scenario;
  run with `MonitorAll` only; assert `coral query topology` returns the
  `otel-app → cpu-app` edge within the polling window.
- Add a client-only worker container with `OTEL_SERVICE_NAME` set; assert
  worker appears as a node with correct name and outbound edges are present.

## Implementation Status

**Core Capability:** ⏳ Not Started

`DiscoveryManager` and two built-in providers (`ProcFSProvider`,
`EnvVarProvider`) will replace the `open_ports: 1-65535` catch-all rule in
`MonitorAll` mode. Every listening server gets a named `open_ports` rule;
every client-only process gets a named `exe_path` rule. `OTEL_SERVICE_NAME`
is respected automatically in all Linux environments.

## Future Work

**ContainerRuntimeProvider** (Future RFD)

- Read container name and `com.docker.compose.service` label from Docker or
  containerd socket.
- `Probe()` checks for socket existence (`/var/run/docker.sock`,
  `/run/containerd/containerd.sock`).
- Slots into the priority chain above `EnvVarProvider`.
- Handles containers that expose no ports (pure consumers in Compose stacks).

**KubernetesProvider** (Future — see RFD 012)

- Read pod name, namespace, and labels from the downward API
  (`/etc/podinfo/`) or node kubelet API.
- `Probe()` checks if the k8s API endpoint is reachable.
- Highest-priority built-in provider; slots above `ContainerRuntimeProvider`.
- Enables `coral query topology` to show pod names and namespace boundaries.

**Configurable Process Exclusions** (Future)

- Allow users to exclude specific process names from dynamic discovery (e.g.
  `exclude_processes: [sshd, systemd]`) to reduce noise when running on bare
  metal with many system daemons.

**Re-resolution on provider change** (Future)

- When a provider's name for a PID changes (e.g., container renamed), trigger
  re-resolution of the affected `ServiceEntry` in the naming chain (RFD 104).
- Requires providers to support a notification or diff interface.

**Windows / macOS Support** (Future)

- eBPF is Linux-only; `MonitorAll` dynamic discovery is therefore also
  Linux-only. No action needed — document the platform requirement.
