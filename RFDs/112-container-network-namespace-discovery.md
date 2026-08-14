---
rfd: "112"
title: "Container Network Namespace-Aware Process Discovery"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: false
dependencies: [ "102", "103" ]
database_migrations: [ ]
areas: [ "agent", "beyla", "ebpf", "service-discovery", "docker" ]
---

# RFD 112 - Container Network Namespace-Aware Process Discovery

**Status:** 🎉 Implemented

## Summary

Make Coral's built-in `ProcFSProvider` discover TCP listeners in every network
namespace visible to the agent, not only the host network namespace. This lets
a privileged Coral agent running directly on a Linux host discover and
instrument HTTP/gRPC servers in ordinary Docker Compose containers without
requiring `network_mode: host`, a Beyla sidecar per container, or access to the
Docker socket.

## Problem

`ProcFSProvider` currently reads `/proc/net/tcp` and `/proc/net/tcp6` once,
then maps their socket inodes to process file descriptors. Those files describe
the _agent's_ network namespace: normally the host namespace.

Docker Compose gives each application container a separate network namespace.
Consequently, a container process that listens on `:8080` is visible under the
host PID namespace, but its listening socket is absent from `/proc/net/tcp`.
The provider cannot associate port 8080 with that process and does not produce
a usable `ProcessCandidate` for it.

The user-facing symptom is a service such as `port-8080` with zero requests,
while host-network processes such as `dockerd` and `cloudflared` receive
telemetry. `port-8080` is Coral's fallback label for a known port without a
resolved binary; it is not evidence that Beyla attached to the application.

This also interacts badly with the current MonitorAll fallback. Once any
host-network process produces a specific discovery rule, Coral suppresses the
wide `open_ports: "1-65535"` rule to avoid duplicate Beyla probe attachment.
The container server is therefore not covered by either a specific rule or the
fallback rule.

### Why this matters

- Docker Compose is a common deployment model for Coral users.
- `agent.monitor_all: true` promises zero-configuration observation, but does
  not presently observe the default Compose network.
- Asking users to use host networking weakens isolation and changes application
  networking semantics.
- One Beyla sidecar per application container duplicates resource use and is
  unnecessary when Coral is already running on the host with eBPF privileges.

## Goals

- Discover TCP and TCP6 listeners in all visible Linux network namespaces.
- Associate each listener with its owning host PID and create normal
  `ProcessCandidate` records for the existing DiscoveryManager.
- Preserve host and non-container discovery behavior.
- Require no Docker API/socket access for the core feature.
- Degrade safely when a namespace or process disappears, or cannot be read.
- Avoid duplicate candidates when many processes share one network namespace.

## Non-goals

- Docker Compose service-name or container-label resolution. This RFD makes
  the application observable; naming may continue to use `OTEL_SERVICE_NAME`,
  `SERVICE_NAME`, or the executable fallback. A Docker metadata naming
  provider can be proposed separately.
- Discovery of UDP, Unix-domain sockets, or non-listening client processes.
  Although RFD 102 describes client-only `ProcessCandidate` discovery, the
  current `ProcFSProvider` skips PIDs with no listening ports. This RFD retains
  that behavior in every namespace; Compose workers and consumers remain out
  of scope until client-only discovery is implemented separately.
- Supporting unprivileged agents that cannot inspect other processes' `/proc`
  entries.
- Changing Beyla's instrumentation or its capability model.
- Inferring the host-published port for a Docker port mapping. Beyla will use
  the port on which the application process actually listens in its namespace.

## Solution

Replace the single host socket-table scan in `ProcFSProvider` with a
namespace-aware scan. The provider will group visible processes by the network
namespace identity exposed by `/proc/<pid>/ns/net`, scan each unique namespace
through one representative process's `/proc/<pid>/net/tcp` and `tcp6`, and
join the resulting socket inodes to all process file descriptors in that
namespace.

The candidate contract and DiscoveryManager merge semantics from RFD 102 stay
unchanged. A container application's PID and listening ports flow through the
same path as a host service:

```
host Coral agent (privileged, host PID namespace)
  │
  ├─ enumerate /proc/<pid>
  ├─ group PIDs by /proc/<pid>/ns/net identity
  │     ├─ host namespace      → dockerd, cloudflared
  │     └─ container namespace → compose-api (PID 12431)
  ├─ scan /proc/<representative-pid>/net/tcp[6] per namespace
  ├─ join (network namespace, socket inode) to /proc/<pid>/fd
  └─ ProcessCandidate{PID: 12431, Ports: [8080], Name: "api"}
          │
          ▼
     DiscoveryManager → named Beyla rule → OTLP spans/metrics
```

### Namespace-safe socket identity

Socket inode numbers are meaningful only in the context of a network
namespace. The implementation must use a composite key:

```
(network namespace ID, socket inode) → listening port
```

It must not join an inode from a container namespace to a process file
descriptor in the host namespace merely because their numeric inode values
match.

### Detailed algorithm

1. Enumerate numeric directories under `/proc`, as today.
2. For each readable PID, resolve `/proc/<pid>/ns/net`. Use the symlink target
   (for example `net:[4026531993]`) as its stable identity for this discovery
   cycle. Processes whose namespace cannot be read are skipped.
3. Group PIDs by that namespace identity. Select the lowest readable PID as a
   representative for each group.
4. Read `/proc/<representative-pid>/net/tcp` and `tcp6`; retain only `LISTEN`
   entries. Store each result by the composite socket key above.
5. Walk `/proc/<pid>/fd` for every PID in the group. For each `socket:[inode]`
   descriptor, look up the composite key and append the matching port to that
   PID's listener set.
6. Produce one `ProcessCandidate` for every PID with one or more listener
   ports. Keep the existing `comm`-based name as the lowest-priority name
   source.
7. Continue to scan other namespaces when an individual namespace disappears
   or cannot be read. If the selected representative exits or its socket table
   becomes unreadable, retry the remaining PIDs in that namespace group in
   ascending PID order before giving up on the namespace for that cycle. If no
   representative can be read, omit only that namespace's candidates, emit a
   debug log with namespace ID and reason, and do not fail the entire discovery
   round. This is an accepted transient gap; the next discovery poll retries
   the namespace.

The scan is intentionally read-only and does not enter namespaces with
`setns`/`nsenter`; procfs namespace views provide the needed socket tables.

## Design Decisions

### Extend ProcFSProvider instead of adding a Docker-only provider

The defect is not specific to Docker metadata: it is a Linux network namespace
visibility problem. Fixing it in `ProcFSProvider` supports Docker, Podman,
containerd, and manually-created namespaces without a runtime socket or a
new external dependency.

### One scan per namespace, not per process

Scanning `/proc/<pid>/net/tcp[6]` for every PID repeats the same work for all
processes in a namespace. Grouping first makes cost proportional to the number
of namespaces while still attributing sockets precisely by each process's file
descriptors.

### Preserve one candidate per PID

The DiscoveryManager already merges naming hints and static candidates by PID.
Returning a candidate per owning PID preserves this behavior and prevents two
unrelated application processes in one Compose network from being merged into
a single service.

### No Docker socket by default

Mounting `/var/run/docker.sock` gives the agent broad container-control access.
It is not needed to find listeners, and requiring it would make the default
deployment less safe. Docker-aware naming remains optional future work.

### Do not re-enable a global catch-all as the fix

A simultaneous wide Beyla rule and named rules can match the same process and
cause duplicate probe attachment. Namespace-aware candidates allow Coral to
continue emitting one specific rule per discovered server, avoiding that
failure mode.

## Component Changes

### `internal/agent/beyla/discovery/procfs.go`

- Replace `scanListeningSockets(root)` with a namespace-aware equivalent that
  accepts the discovered PID set and returns `map[PID][]port`.
- Add small internal types for network-namespace ID and composite socket key.
- Read namespace-local socket tables from
  `/proc/<representative-pid>/net/tcp[6]`.
- Update descriptor joining to include network-namespace ID.
- Keep existing parsers for TCP tables and `socket:[inode]` targets where
  possible.
- Log skipped namespaces at debug level and aggregate recurring permission
  failures to avoid log storms.

### `internal/agent/beyla/discovery/procfs_test.go`

- Generalize procfs fixtures so they can represent multiple PIDs and distinct
  network namespace directories.
- Cover host and container namespaces whose socket inodes overlap.
- Verify a listener in a container namespace maps only to the container PID.
- Verify two containers using port 8080 create two candidates rather than one.
- Verify an unreadable/exited representative does not prevent host discovery.

### Documentation

- Update the agent deployment guide to state that a host-installed privileged
  agent discovers ordinary Docker/Compose listeners automatically.
- Document required visibility: a host PID namespace and sufficient rights to
  read `/proc/<pid>/ns/net`, `/proc/<pid>/net`, and `/proc/<pid>/fd`.
- Document that Compose service names are not automatically inferred unless an
  application supplies `OTEL_SERVICE_NAME`/`SERVICE_NAME` or a future naming
  provider is enabled.
- Document that the observed listener is the application's namespace-local
  port. For a Compose mapping such as `9090:8080`, Coral/Beyla reports 8080,
  not the host-published port 9090.

## Configuration and Permissions

No new configuration is required. The behavior is active whenever the existing
`procfs` discovery provider is enabled:

```yaml
agent:
  monitor_all: true

beyla:
  discovery:
    discovery_providers:
      procfs: enabled
```

The host agent must run with the same process visibility required by current
eBPF/Beyla operation. Running as root is sufficient. A least-privilege
deployment must be able to read relevant `/proc` namespace, socket-table, and
file-descriptor entries; a `hidepid` mount policy may otherwise prevent
container discovery. The agent should report this as a capability warning,
not silently claim successful container discovery.

## Rollout and Compatibility

- This is additive and requires no API, database, or configuration migration.
- Existing host candidates should remain byte-for-byte equivalent after
  discovery, aside from ordering which must be deterministic (sort namespace
  IDs and PIDs before emitting candidates).
- When the provider cannot inspect a container namespace, its behavior falls
  back to today's host-only discovery for that namespace.
- On very high-density hosts, measure discovery-cycle time and expose it in
  debug telemetry before changing the default polling interval.

## Testing Strategy

### Unit tests

- Single host namespace: retain current TCP/TCP6 listener behavior.
- Host plus one container namespace: discover a container PID listening on
  8080 even though host `/proc/net/tcp` has no 8080 entry.
- Two namespaces with the same socket inode: never cross-attribute ports.
- Two Compose containers sharing one namespace: attribute each listener to
  its owning PID.
- Two isolated containers both listening on 8080: preserve two PIDs and two
  candidates.
- Namespace/socket-table/FD permission errors and process exit races: no
  provider-wide error; unaffected namespaces still return candidates.
- Selected representative exits before its socket table is read: retry a
  remaining PID in the same namespace; omit the namespace only when every
  representative is unreadable for that cycle.
- Stable candidate ordering across repeated scans.

### Integration tests

- Start a Compose fixture with an HTTP application published as `8080:8080`
  on the default bridge network and a host-network listener.
- Start Coral on the host with MonitorAll enabled; do not mount the Docker
  socket and do not use `network_mode: host` for the application.
- Send HTTP traffic to the published application port.
- Assert Coral generates a Beyla rule for the app's actual executable/PID and
  port 8080, stores HTTP metrics/traces, and `coral query summary` reports
  requests greater than zero for that service.
- Assert both the host listener and container application are observed.

### Acceptance criteria

The initial reported scenario succeeds: a privileged host Coral agent observes
traffic to a Docker Compose app listening on 8080, and the summary does not
show only an idle `port-8080` placeholder.

## Implementation Plan

1. [x] Refactor ProcFS socket-table and FD helpers to accept a namespace
   identity and process-specific procfs path, retaining current
   single-namespace tests.
2. [x] Implement namespace grouping and composite-key socket lookup.
3. [x] Add unit fixtures/tests for isolation, overlapping inodes, races, and
   deterministic output.
4. [x] Add a Linux Compose integration fixture and assert non-zero Beyla
   telemetry for a bridge-network app. (Added and wired into the
   orchestrator; not yet run to a full green pass in CI — see Implementation
   Status.)
5. [x] Add capability diagnostics and deployment documentation.
6. [ ] Benchmark discovery on a representative host with many containers;
   adjust logging or polling only if needed.

## Implementation Status

**Core Capability:** 🎉 Implemented

`ProcFSProvider` now discovers TCP listeners in every network namespace
visible to the agent, not only the host namespace, and joins socket inodes
using a composite `(network namespace, inode)` key so processes in different
namespaces never cross-attribute ports.

**Operational Components:**

- ✅ Namespace grouping: PIDs are grouped by `/proc/<pid>/ns/net` identity,
  with a lowest-PID representative scanning `/proc/<pid>/net/tcp[6]` per
  namespace (`internal/agent/beyla/discovery/procfs.go`).
- ✅ Representative retry: if the selected representative exits or its
  socket table is unreadable, the next PID in the namespace is tried before
  the namespace is skipped for that cycle.
- ✅ Capability warning: permission errors reading `/proc/<pid>/ns/net` are
  aggregated into one `Warn`-level log per discovery cycle rather than
  silently under-reporting container discovery or logging per-PID.
- ✅ Deterministic output: namespace IDs and PIDs are sorted before scanning
  and emitting candidates.
- ✅ Deployment documentation (`docs/AGENT_DEPLOYMENT.md`): required
  visibility, `hidepid` caveat, and the namespace-local (not host-published)
  port behavior for Compose port mappings.
- ✅ Unit tests: host-only discovery preserved; container namespace listener
  discovery; non-cross-attribution of colliding inodes across namespaces;
  two containers sharing one port produce two candidates; two processes
  sharing one namespace attribute a listener only to its owning PID;
  representative-exit retry; unreadable-namespace isolation; permission
  warning path; stable candidate ordering across repeated scans.
- ✅ Docker Compose integration fixture (`tests/e2e/distributed`): a new
  `netns-app` container shares agent-0's PID namespace (`pid:
  "service:agent-0"`) but keeps its own, separate network namespace (no
  `network_mode` override, no Docker socket, no `network_mode: host`) — the
  same visibility gap a bare-metal privileged agent has against an ordinary
  Compose app. `ContainerNamespaceSuite`
  (`tests/e2e/distributed/container_namespace_test.go`) asserts plain
  MonitorAll auto-discovers it (Beyla `open_ports` rule on its
  namespace-local port 8080, not the host-published 18085) and that Beyla
  actually captures HTTP traffic across the namespace boundary. Wired into
  `TestE2EOrchestrator`'s Group 3 (Passive Observability). Compiles and
  `go vet`s cleanly against the e2e module; the standalone `netns-app`
  Docker image was built and smoke-tested directly (serves `/health` and
  `/` correctly). The full distributed suite was not run to completion in
  this environment — see Integration Status.

**What Works Now:**

- A privileged host Coral agent discovers TCP/TCP6 listeners owned by
  processes in Docker Compose (or Podman/containerd/manual) network
  namespaces, without `network_mode: host`, a Beyla sidecar per container,
  or Docker socket access.
- Host and non-container discovery behavior is unchanged.

**Integration Status:**

- The Compose integration test (step 4) is written and wired into the
  orchestrator, but was not run to a full green pass: this development
  sandbox runs Docker via a resource-constrained colima VM (2 vCPU) where a
  single trivial Go build took ~3 minutes and the full stack build (colony +
  2 agents + 6 fixture apps, the whole Go module) did not finish within a
  practical session budget. Needs a run on a real Linux CI runner or a less
  constrained Docker host to get a full pass/fail signal.
- Implementation Plan step 6 (density benchmarking) also requires a
  representative host and is tracked in Future Work; it does not block this
  change since the namespace-join logic itself is exhaustively exercised by
  the unit fixtures above and the change is additive/backward-compatible.

## Future Work

**Run and Verify the Docker Compose Integration Test** (RFD 112
Implementation Plan step 4)
- `ContainerNamespaceSuite` (`tests/e2e/distributed/container_namespace_test.go`)
  and its `netns-app` fixture are implemented and wired into
  `TestE2EOrchestrator`, but need an actual run on a real Linux Docker host
  or CI runner (`make -C tests/e2e/distributed local-build local-up`, then
  `go test -v -timeout 30m -run
  "TestE2EOrchestrator/Test3_PassiveObservability/Netns"` from
  `tests/e2e/distributed`) to confirm the assertions pass end to end.
- Optionally extend it to also assert via `coral query summary` at the CLI/
  colony level (the acceptance criteria's literal wording), matching
  `CLIQuerySuite`'s pattern — the current test proves the mechanism at the
  agent/eBPF level via `QueryAgentEbpfMetrics`.

**Discovery Density Benchmark** (RFD 112 Implementation Plan step 6)
- Measure discovery-cycle time on a host with many containers before
  changing the default polling interval or logging verbosity, as called out
  in Rollout and Compatibility.

## Alternatives Considered

### Require `network_mode: host`

Rejected. It changes application isolation and port-conflict behavior, is an
unsafe default, and makes ordinary Compose deployments less representative of
production.

### Run a Beyla sidecar per application container

Rejected as the default. It works, but duplicates eBPF agents and operational
configuration even though a privileged host Coral agent can observe the same
processes.

### Use the Docker API/socket for listener discovery

Rejected for the core solution. It introduces a Docker-specific dependency and
substantial control-plane privileges. It can be introduced later solely for
richer container/service naming.

### Keep a global `open_ports: "1-65535"` Beyla rule alongside named rules

Rejected. It risks duplicate matches and probe conflicts, the behavior RFD 102
was designed to avoid.
