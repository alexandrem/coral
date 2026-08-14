# Process & Service Discovery and Naming

This chapter covers *what* Coral observes and *what it calls it* —
distinct from chapter 03 (the eBPF/Beyla mechanics of *how* a process is
instrumented once found) and chapter 13 (the Colony/Agent/Discovery mesh
**enrollment** rendezvous, an unrelated use of the word "discovery"). The
pipeline here runs entirely within a single agent: find candidate
processes, merge/prioritize what multiple sources say about them, hand the
result to Beyla as instrumentation rules, and maintain one authoritative
name per port as traffic and explicit `coral connect` calls arrive.

## Pluggable Process Discovery (RFD 102)

### The Problem: One Name for Every Process

Before RFD 102, `--monitor-all` (zero-config mode) emitted a single Beyla
discovery rule: `open_ports: "1-65535"`. Beyla groups every process matching
the *same* rule into one logical service, named after the first binary it
encounters — typically `coral-agent`. Every call between two real services
therefore appeared in trace data as originating from the same name, and the
topology materialization join (`child.service_name != parent.service_name`,
see chapter 15) always evaluated false, so `coral query topology` reported no
edges at all despite traffic flowing. Client-only processes (workers,
consumers — anything that never binds a listening socket) were invisible
entirely, since the socket-table-based fallback had nothing to key on.

### Architecture (`internal/agent/beyla/discovery`)

A `ProcessDiscoveryProvider` interface abstracts "how do we find and name a
process":

```go
type ProcessDiscoveryProvider interface {
    Name() string
    Probe() bool                                          // is this provider usable here?
    Discover(ctx context.Context) ([]ProcessCandidate, error)
}
```

`ProcessCandidate` carries `PID`, `Ports`, `Name`, `Source`, `Labels`, and
`IsClientOnly`. A `DiscoveryManager` polls every registered provider on an
interval (`discovery_sync_interval`, default 30s), merges their candidates,
detects changes against the previous merge, and — on change — invokes an
`applyDiscovery` callback that feeds the Beyla `Manager`'s **Runtime Service
Tracking (RFD 053)** debounced-restart path (chapter 03).

**Merge priority** (highest wins per process):

1. **Static candidates** (`SetStaticCandidates`) — pinned entries from
   `coral connect`/`coral disconnect` and the agent's config-file
   `beyla.discovery.services` map. Re-applied on *every* poll, so they are
   never aged out by a cycle where no provider currently reports the same
   process.
2. **`EnvVarProvider`** — reads `OTEL_SERVICE_NAME` (falling back to
   `SERVICE_NAME`) from `/proc/<pid>/environ`. Respects an operator-set name
   in any environment (bare metal, Docker Compose, Kubernetes) without
   requiring `coral connect`.
3. **`ProcFSProvider`** — the universal fallback. A socket-table scan
   (`/proc/net/tcp[6]`, resolving listening-socket inodes to PIDs via each
   process's `fd` directory) finds every listening server; a full
   `/proc/<pid>/comm` walk finds every *other* running process and reports it
   with `IsClientOnly=true`. This is what makes client-only processes visible
   at all — they were structurally unreachable under the old catch-all.

**Merge mechanics** (`internal/agent/beyla/discovery/manager.go`): candidates
are joined primarily by PID, so an `EnvVarProvider` name hint and a
`ProcFSProvider` port list for the same running process collapse into one
entry. Static candidates registered before the real PID is known (a
`coral connect` call made ahead of the process starting) are additionally
joined by **port**, once a provider reports a real process there — this
prevents the static entry and the provider-discovered entry from producing
two separate, conflicting Beyla rules for the same port.

### From Candidates to Beyla Rules (`generateBeylaConfig`)

`beyla.Manager.generateBeylaConfig` (chapter 03) maps the merged candidate
list directly to Beyla `discovery.services` rules:

- A candidate with `Ports` becomes a named `open_ports` rule (plus an
  `exe_path: .*<name>.*` clause, so outbound calls on other ports are still
  attributed to the same service).
- A candidate with `IsClientOnly=true` becomes a named `exe_path`-only rule —
  there is no port to match on.
- The residual `open_ports: "1-65535"` catch-all is only emitted when
  `MonitorAll` is set **and** no candidate resolved to a named rule (e.g. the
  very first poll cycle, before `ProcFSProvider` has run). Mixing named rules
  with the catch-all causes Beyla to attach eBPF uprobes to the same process
  twice, which silently drops all spans for it — so the two are always
  mutually exclusive.

## Namespace-Aware Process Discovery (RFD 112)

### The Problem: One Socket Table, Many Network Namespaces

`ProcFSProvider`'s socket-table scan originally read `/proc/net/tcp[6]`
once — the *agent's own* network namespace. That is the host namespace for a
privileged host-installed agent, but Docker Compose (and Podman/containerd)
gives every application container its own network namespace. A container
process's listening socket simply never appears in the host's socket table,
so `ProcFSProvider` produced no `ProcessCandidate` for it at all — not a
misnamed candidate, an absent one. The symptom was a `port-8080` fallback
service with zero requests: Coral's generic "known port, no resolved binary"
label, not evidence Beyla ever attached.

### Solution: Scan Per-Namespace, Join by Composite Key

`ProcFSProvider.Discover` (`internal/agent/beyla/discovery/procfs.go`) now
scans every network namespace visible to the agent, not just its own:

1. `groupPIDsByNamespace` resolves each PID's `netNamespaceID` from the
   `/proc/<pid>/ns/net` symlink target (e.g. `net:[4026531993]`) and groups
   PIDs sharing one namespace together.
2. `scanNamespaces` reads exactly one socket table per namespace — the
   lowest-PID member acts as representative, so cost is proportional to
   namespace count, not process count. `scanNamespaceSocketTable` retries
   the next PID in ascending order if the representative has exited (its
   `/proc/<pid>` directory is gone) or its socket table is unreadable,
   before giving up on that namespace for the cycle; an unreadable namespace
   never fails discovery for the others.
3. Socket inodes are only unique *within* a namespace, so every lookup keys
   on the composite `socketKey{ns, inode}` rather than the inode alone —
   this is the core correctness fix. Two containers coincidentally reusing
   the same inode number in their own namespaces (a common, expected
   occurrence) must never cross-attribute a port from one PID to the other.
4. The existing per-PID `fd` walk (`socket:[inode]` symlinks) is unchanged;
   it now looks up `(that PID's namespace, inode)` in the merged table
   instead of a single global one.

Namespace IDs and PIDs are sorted before scanning and before candidates are
emitted, so output ordering is deterministic across polls — required by
`DiscoveryManager`'s change-detection diff (`equalCandidateSets`), which
would otherwise see a spurious change every cycle from map iteration order
alone and trigger unnecessary Beyla restarts.

Permission errors reading `/proc/<pid>/ns/net` are aggregated into one
`Warn`-level log per `Discover` cycle (a count, not one log line per PID) so
a host running many containers the agent cannot fully inspect gets a visible
capability warning instead of silently under-reporting container discovery.

### Why Extend `ProcFSProvider` Instead of Adding a Provider

The defect was a Linux network-namespace visibility gap, not something
Docker-specific — the fix benefits Podman, containerd, and manually-created
namespaces equally, and needs no Docker socket, no `network_mode: host`, and
no Beyla sidecar per container. Extending the existing universal-fallback
provider in place also means the RFD 102 merge/priority/naming pipeline
(`EnvVarProvider` outranking `ProcFSProvider`, static candidates outranking
both) is untouched: a container's `ProcessCandidate` flows through exactly
the same `DiscoveryManager` → `generateBeylaConfig` path as a host process.
Naming still falls back to `comm`/`OTEL_SERVICE_NAME` as before — RFD 112
makes containerized listeners *discoverable*, not *named* by container/pod
metadata; see "Container-Aware Discovery Providers" below for that
still-open, separate problem.

## Default-On Observation and OTLP Feedback (RFD 103)

### Zero-Flag Startup

Before RFD 103, `coral agent start` collected nothing unless the operator
passed `--monitor-all` — a flag most users only discovered after filing a
"why is nothing showing up?" issue. `--monitor-all` is now implicit: Beyla
starts unconditionally unless the operator opts out.

- **`internal/cli/agent/startup/storage.go`**: `StorageManager.Initialize`
  reads `agentCfg.Agent.MonitorAll` (config field `agent.monitor_all`, env
  `CORAL_MONITOR_ALL`, default `true`) instead of gating on a CLI-only flag.
- **`--no-monitor-all`**: the supported escape hatch for resource-constrained
  hosts, threaded through `AgentServerBuilder` → `ConfigValidator` →
  `agentCfg.Agent.MonitorAll` (`internal/cli/agent/startup/{start,builder,
  validator}.go`). It forces `MonitorAll` to `false` regardless of what the
  config file/env set — CLI opt-out always wins.
- **`--monitor-all` is now a no-op**: still accepted so existing scripts and
  container images don't break, but `start.go`'s `RunE` checks
  `cmd.Flags().Changed("monitor-all")` and emits a deprecation warning to
  stderr rather than gating anything on the flag's value.
- **`--connect` was removed** from `agent start` (it duplicated default
  observation's job). The standalone `coral connect` command
  (`internal/cli/agent/connect.go`) is unaffected — it remains the way to
  pin an explicit name/health-check for a process ahead of
  `ProcFSProvider`/`EnvVarProvider` naming it.

### OTLP Feedback Callback

Default-on observation means the agent no longer knows in advance which
ports it will see traffic on — RFD 102's `DiscoveryManager` poll loop still
runs on a timer (`discovery_sync_interval`, default 30s), which is too slow
to give upstream components (service map, RFD 105) *immediate* awareness of
a newly observed process. Rather than add a second poll loop, RFD 103 reuses
data already flowing through the system: every OTLP span Beyla emits.

- **`beyla.Manager.HandleSpan`** (the `SpanHandler` callback that routes
  every incoming span to `beyla_traces_local`) calls
  `notifyServiceObserved` before the DuckDB write. `extractSpanPort` reads
  the span's `server.port` attribute (falling back to the older
  `net.host.port` semconv key) — the same attribute a `server`-kind HTTP
  span from Beyla's OTel exporter always carries.
- **Dedup**: `Manager.observedPorts` (guarded by `Manager.mu`) tracks which
  ports have already fired this session, so a hot endpoint doesn't spam the
  callback on every request — only the *first* span for a given port
  triggers it.
- **Callback**: `Manager.SetServiceObservedHandler` lets `agent.New` wire
  `Agent.onBeylaServiceObserved(port, pid, serviceName)`
  (`internal/agent/agent.go`). This is the write-path into the unified
  service map — see **Unified Service Map and Naming (RFD 104)** below.
- **Why spans and not metrics**: `Transformer.TransformMetrics` only
  threads `service.name` through to the `ebpfpb.EbpfEvent` payloads it
  emits — RFD 103 explicitly avoided adding protobuf fields, so
  metric-only-sampled processes (traces sampled out but metrics still
  aggregated) won't trigger the callback. Not a coverage gap under Coral's
  default protocol/sampling config, since spans and metrics are emitted for
  the same requests.

## Unified Service Map and Naming (RFD 104)

### The Problem: Anonymous Auto-Discovered Processes

Default-on observation (RFD 103) means the agent sees traffic on ports it
was never told about. Before RFD 104, `Agent.monitors` was a
`map[string]*ServiceMonitor` keyed by name and populated only by explicit
`coral connect` calls — an auto-discovered port had nowhere to live. There
was no agent-level record of "what is listening on port 3000," which left
`onBeylaServiceObserved` with nothing to write into, and left `coral
services` (RFD 107) and topology joins (`service_name`, chapter 15) with no
name to attach to auto-discovered traffic.

### `ServiceEntry` and the Port-Keyed Map (`internal/agent/service_entry.go`)

`Agent.monitors` is replaced by `Agent.services map[int32]*ServiceEntry`,
keyed by **port** rather than name — the one identifier every observed
process has, whether or not it was ever named. A `ServiceEntry` is the
single record of truth for a port:

```go
type ServiceEntry struct {
    Port                        int32
    AutoName, AuthoritativeName string
    NamingSource                NamingSource // Auto | Authoritative
    Tier                        Tier         // TierObserved (0) | TierWatched (1)
    PID                         int32
    BinaryPath, BinaryHash      string
    Monitor                     *ServiceMonitor // nil until TierWatched
}
```

- **`NamingSource`**: `Auto` (set by the naming adaptor chain from process
  metadata) or `Authoritative` (set explicitly via `ConnectService` —
  `coral connect` / `coral services watch`). Authoritative always wins:
  `ServiceEntry.Name()` returns `AuthoritativeName` if set, else `AutoName`.
- **`Tier`**: `TierObserved` (seen via OTLP feedback, no health checking) or
  `TierWatched` (has a running `ServiceMonitor`). `ConnectService` only
  constructs and starts a `ServiceMonitor` — and only then fires the CPU
  (RFD 072) / memory (RFD 077) profiling callbacks — when the caller
  supplies a `health_endpoint`. A bare `coral connect` with no health check
  records an authoritative name but stays `TierObserved`; there is nothing
  meaningful to poll.
- **Promotion, not replacement**: `onBeylaServiceObserved` and
  `ConnectService` share one code path (`connectServiceLocked`) that either
  creates a fresh `ServiceEntry` or promotes an existing `TierObserved` entry
  in place — an auto-named `node` process becomes `frontend` the moment
  `coral services watch frontend:3000:/health` runs, without losing its PID
  or binary path.

### `ServiceNameAdaptor` Chain (`internal/agent/naming`)

A pluggable interface derives a name from process metadata:

```go
type ServiceNameAdaptor interface {
    Resolve(port int32, pid int32, binaryPath string) (name string, ok bool)
}
```

`Chain` runs a priority-ordered list of adaptors and returns the first
`ok=true` result — the same "first non-empty wins" pattern `DiscoveryManager`
(RFD 102) uses for provider merging, so a future `KubernetesAdaptor` or
`DockerAdaptor` can be prepended without touching agent code. The only
built-in today is `ProcessNameAdaptor`:

- **Unique binary** → the binary's base name (e.g. `node`).
- **Conflict** (a second port resolves to a binary name already claimed by
  another port) → `<name>-<port>` for the *new* port. The first port keeps
  its bare name — `ProcessNameAdaptor.Resolve` only returns a value for the
  port being resolved right now, with no channel back to rename an earlier
  caller's stored `AutoName`. Retroactive renaming of the first entry is
  deferred (see Future Engineering Note).
- **No binary info** (non-Linux, or `pid` didn't resolve via
  `proc.GetBinaryPath`) → `port-<N>`.

`onBeylaServiceObserved` resolves `binaryPath` via `proc.GetBinaryPath(pid)`
(the same `/proc/<pid>/exe` helper `ServiceMonitor.discoverProcessInfo`
uses) and only runs the chain when the entry isn't already
`NamingSource=Authoritative` — an explicitly connected service's name is
never overwritten by auto-discovery.

## Future Engineering Note

### Metric-Triggered Service Observation

`onBeylaServiceObserved` (RFD 103) currently fires only from the span path
(`Manager.HandleSpan`). A process whose spans are always sampled out but
whose metrics still aggregate normally would not be observed until
`DiscoveryManager`'s next poll. Extending `Manager.consumeMetrics` to
extract `server.port` from metric data-point attributes and fire the same
callback — without adding fields to the `ebpfpb.EbpfEvent` protobufs — would
close this gap; deferred because it's not a coverage gap under the default
protocol/sampling configuration.

### Retroactive Naming Conflict Resolution

`ProcessNameAdaptor` (RFD 104) only suffixes the port it is actively
resolving when a binary-name conflict appears; an earlier port that already
resolved to the bare name keeps it until something re-triggers resolution
for it. A `KubernetesAdaptor`'s pod labels can also change after the fact
(a rolling deploy relabels a pod). Both cases need the same missing
primitive: a way for an adaptor to signal "re-resolve these already-known
ports," and for `Agent` to apply the update to entries whose `NamingSource`
hasn't been pinned by `ConnectService` in the meantime.

### Container-Aware Discovery Providers

RFD 112 closed the *visibility* half of container discovery: `ProcFSProvider`
now finds a container's listening PID and port regardless of which network
namespace it lives in (see "Namespace-Aware Process Discovery" above). The
*naming* half remains open — today a containerized process is still named via
`comm` or `OTEL_SERVICE_NAME`/`SERVICE_NAME`, never via Compose service name,
container label, or Kubernetes pod/namespace. The `ProcessDiscoveryProvider`
chain (RFD 102) is designed so that naming-only sources slot in above
`EnvVarProvider` without touching existing code: a planned
`ContainerRuntimeProvider` (Docker/Compose service labels, via the Docker
socket — deliberately not required by RFD 112's core fix) and
`KubernetesProvider` (pod name/namespace via the downward API) are pure
additions — `RegisterProvider` order is the only integration point.
`KubernetesProvider` would also let `coral query topology` show pod/namespace
boundaries directly.

## Related Design Documents (RFDs)

- [**RFD 102**: Pluggable Service Discovery Providers](../../RFDs/102-pluggable-service-discovery-providers.md)
- [**RFD 103**: Default-On Observation](../../RFDs/103-default-on-observation.md)
- [**RFD 104**: ServiceNameAdaptor and Unified Service Map](../../RFDs/104-service-name-adaptor.md)
- [**RFD 112**: Container Network Namespace-Aware Process Discovery](../../RFDs/112-container-network-namespace-discovery.md)
