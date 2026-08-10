---
rfd: "112"
title: "Connect by Executable Pattern"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "053", "102" ]
database_migrations: [ ]
areas: [ "agent", "cli", "service-discovery", "beyla" ]
---

# RFD 112 - Connect by Executable Pattern

**Status:** 🚧 Draft

## Summary

Add a `--exe-pattern` flag to `coral connect` so users can explicitly register
a process that never binds a listening port (a Kafka consumer, cron job, or
batch worker) by matching its executable path instead of a `name:port` spec.
The agent resolves the pattern to a PID via `/proc` scanning, feeds it into
the discovery merge introduced by RFD 102 as a pinned, portless
`ProcessCandidate`, and falls back to process-liveness checking in place of
TCP/HTTP health checks.

## Problem

**Current behavior/limitations:**

`coral connect` requires a `name:port[:health][:type]` spec —
`parseServiceSpecsWithLegacySupport` in `internal/cli/agent/connect.go` treats
port as mandatory, and the underlying `ConnectServiceRequest` proto message
has no way to represent a service without one. There is currently no explicit,
user-driven way to tell Coral "watch this process" when that process never
listens on a socket.

RFD 102 closes part of this gap, but only for *automatic* discovery:
`ProcFSProvider` walks `/proc/<pid>/comm` under `MonitorAll` and picks up
client-only processes on its own. It does not help a user who wants to
**explicitly and deterministically** connect a specific portless process —
for example, in a startup script or init container where the user knows
exactly which worker they want observed and does not want to depend on
`MonitorAll`'s broader, best-effort sweep.

**Why this matters:**

- `coral agent start --connect <spec>` is the recommended way to wire up
  known services at startup (RFD 053); it currently cannot express a
  portless worker at all, forcing users either to enable `MonitorAll` (which
  instruments everything on the host) or to leave the worker unobserved.
- Users who already know their service names (from a Kubernetes manifest, a
  Docker Compose file, or a supervisor config) want to declare them the same
  way they declare port-based services, not rely on a background scan.
- RFD 053's own Future Enhancements section named this exact capability
  (`coral connect --exe-pattern "python.*gunicorn" api:8080`) as deferred
  work; it has never been picked up.

**Use cases affected:**

- `coral agent start --connect worker --exe-pattern "python.*consumer.py"`
  at container startup, so the worker is instrumented from the first request
  without waiting for a `MonitorAll` poll cycle.
- Init-container / sidecar patterns where the operator lists every process
  they care about explicitly and does not want other host processes
  instrumented.
- Environments where `MonitorAll` is disabled (RFD 103's default-on mode
  still allows `--no-monitor-all` for resource-constrained hosts) but a
  specific portless worker still needs coverage.

**Previously investigated alternatives:**

- Rely solely on RFD 102's `ProcFSProvider` auto-discovery — works, but is
  opt-out only via `MonitorAll`/`discovery_providers`, not opt-in per
  process, and gives the user no explicit confirmation that a specific
  worker is being watched (`coral connect` returns success/failure inline;
  auto-discovery does not).
- Require the user to pass a fake/reserved port — rejected, misleading in
  logs and topology, and Beyla would try to bind discovery to a port that
  doesn't exist.

## Solution

Extend `coral connect`'s spec parsing and the `ConnectServiceRequest`/
`ServiceInfo` proto messages with an optional executable-pattern field.
When a spec has no port, the agent resolves the pattern to a PID via the
same `/proc/<pid>/comm` + `/proc/<pid>/cmdline` matching RFD 102's
`ProcFSProvider` uses, registers a `ProcessCandidate` with `IsClientOnly=true`
and no ports through `Manager.SetStaticCandidates` (RFD 102), and switches
`ServiceMonitor` to a process-liveness health check instead of TCP/HTTP.

**Key Design Decisions:**

- **Exactly one of `port` or `exe_pattern`, never both.** A service spec
  identifies a process either by the socket it listens on or by its
  executable path — mixing both is ambiguous about which one is
  authoritative for discovery and rejected at parse time.
- **Reuse RFD 102's process-walk code, don't fork it.** PID resolution for
  `exe_pattern` (regex against `/proc/<pid>/comm` and, if no match, the full
  `/proc/<pid>/cmdline`) is implemented as a shared helper in
  `internal/sys/proc/`, used by both `ProcFSProvider` (RFD 102) and
  `ServiceMonitor.discoverProcessInfo`. No duplicated matching logic.
- **Explicit connects are pinned candidates (RFD 102).** An `exe_pattern`
  registration becomes a `ProcessCandidate` fed through
  `Manager.SetStaticCandidates`, at the same highest-priority slot RFD 102
  defines for `coral connect`. It is merged with, not layered on top of,
  provider-discovered candidates — so an explicit connect and an
  auto-discovered match for the same process never produce duplicate Beyla
  rules.
- **Process-liveness health checking, not a fake network check.** A
  portless service cannot be TCP- or HTTP-checked. `ServiceMonitor` gains a
  third check mode: re-resolve the exe pattern to a PID on each check
  interval; `ServiceStatusHealthy` if a matching PID exists,
  `ServiceStatusUnhealthy` if none is found for `MissedLivenessThreshold`
  (default: 2) consecutive checks (avoids flapping on a brief PID-table
  race), `ServiceStatusUnknown` before the first check completes. This
  reuses the existing `ServiceStatus` enum and `GetStatus()`/`ListServices`
  surface unchanged — callers already handle `Unknown`.
- **No Beyla changes required.** `generateBeylaConfig` (RFD 102) already
  emits `exe_path` rules for `IsClientOnly=true` candidates; an explicit
  `exe_pattern` connect and an auto-discovered client-only candidate produce
  the identical rule shape.

**Benefits:**

- Users can declare portless services the same way they declare port-based
  ones, with the same inline success/failure feedback from `coral connect`.
- No dependency on `MonitorAll` to observe a specific known worker.
- Zero new Beyla-side mechanism — reuses the `exe_path` rule type RFD 102
  introduces for auto-discovered client-only processes.

**Architecture Overview:**

```
coral connect worker --exe-pattern "python.*consumer.py"
  │
  ▼
CLI: parseServiceSpecsWithLegacySupport
  │  (port unset, exe_pattern set → client-only spec)
  ▼
ConnectService RPC (ServiceInfo{name, exe_pattern, port:0})
  │
  ▼
Agent Server: ConnectService handler
  ├─ ServiceMonitor.Start()
  │    ├─ discoverProcessInfo(): proc.FindPidByExePattern(pattern)
  │    └─ performHealthCheck(): process-liveness mode (no port/health_endpoint)
  │
  └─ DiscoveryManager.SetStaticCandidates() (RFD 102)
       └─ ProcessCandidate{Name: worker, IsClientOnly: true, ExePathPattern: pattern}
            │
            ▼
       generateBeylaConfig → exe_path: "python.*consumer.py" rule
```

### Component Changes

1. **`internal/cli/agent/connect.go`** (CLI):

    - Add `--exe-pattern` flag (repeatable-safe: applies to the single
      spec it's paired with, consistent with existing `--port`/`--health`
      legacy flags).
    - Extend spec grammar: `name[:port][:health][:type]` where `port` may be
      omitted if `--exe-pattern` (or a new `:exe:<pattern>` inline token,
      following the project's preference for explicit flags over more inline
      tokens) is supplied. Reject specs with neither a port nor a pattern,
      and specs with both.

2. **`proto/coral/mesh/v1/auth.proto`** and **`proto/coral/agent/v1/agent.proto`**:

    - Add `string exe_pattern = 9;` to `ServiceInfo` and
      `string exe_pattern = 7;` to `ConnectServiceRequest`.
    - `port` becomes conditionally required: exactly one of `port` (non-zero)
      or `exe_pattern` (non-empty) must be set; validated server-side.

3. **`internal/sys/proc/proc.go`**:

    - Add `FindPidByExePattern(pattern string) (int32, error)`: compiles the
      pattern as a regex, scans `ListPids()`, matches against
      `/proc/<pid>/comm` then `/proc/<pid>/cmdline` (first match wins).
      Shared by `ServiceMonitor` (this RFD) and `ProcFSProvider` (RFD 102)
      so pattern-matching semantics never diverge between the explicit and
      automatic paths.

4. **`internal/agent/monitor.go`** (`ServiceMonitor`):

    - `discoverProcessInfo`: when `service.Port == 0` and
      `service.ExePattern != ""`, resolve via `FindPidByExePattern` instead
      of `FindPidByPort`.
    - `performHealthCheck`: add a third branch — process-liveness check —
      selected when `service.Port == 0`. Tracks consecutive misses against
      `MissedLivenessThreshold` before flipping to `ServiceStatusUnhealthy`.

5. **`internal/agent/agent.go`** (`ConnectService`/`DisconnectService`
   handlers, RFD 102 integration point):

    - Build the `ProcessCandidate` passed to
      `Manager.SetStaticCandidates` from `service.ExePattern` when
      `service.Port == 0` (`IsClientOnly: true`, no ports), instead of the
      single-port candidate used for port-based connects.

**Configuration Example:**

```bash
# Connect a portless worker by executable pattern.
$ coral connect worker --exe-pattern "python.*consumer.py"
Connected service worker (process match: python.*consumer.py)
Health monitoring: process-liveness
eBPF metrics: enabled

# At agent startup, alongside port-based services.
$ coral agent start \
    --connect api:8080:/health \
    --connect worker --exe-pattern "python.*consumer.py"

# Rejected: neither port nor pattern given.
$ coral connect worker
Error: service "worker" needs either a port or --exe-pattern

# Rejected: both given.
$ coral connect worker:8080 --exe-pattern "python.*consumer.py"
Error: service "worker" cannot specify both a port and --exe-pattern
```

## Implementation Plan

### Phase 1: Proto and process-resolution primitives

- [ ] Add `exe_pattern` field to `ServiceInfo` (`proto/coral/mesh/v1/auth.proto`)
      and `ConnectServiceRequest` (`proto/coral/agent/v1/agent.proto`);
      regenerate Go bindings
- [ ] Implement `proc.FindPidByExePattern` in `internal/sys/proc/proc.go`
      (comm match, cmdline fallback)
- [ ] Unit tests: pattern matches comm, pattern matches only cmdline (e.g.
      `python` comm with args-based pattern), no match, invalid regex
      rejected at parse time

### Phase 2: CLI and RPC validation

- [ ] Add `--exe-pattern` flag to `coral connect` / `coral agent start
      --connect`
- [ ] Extend spec parsing to allow an omitted port when `--exe-pattern` is
      set; reject neither-nor and both-set cases with a clear error
- [ ] Server-side validation in the `ConnectService` handler: exactly one of
      `port`/`exe_pattern` set
- [ ] Unit tests: spec parsing (pattern-only, port-only, both, neither),
      RPC validation error messages

### Phase 3: Agent-side monitoring and discovery integration

- [ ] `ServiceMonitor.discoverProcessInfo`: branch to
      `FindPidByExePattern` when `Port == 0`
- [ ] `ServiceMonitor.performHealthCheck`: add process-liveness branch with
      `MissedLivenessThreshold` debounce
- [ ] `ConnectService`/`DisconnectService` handlers: build a portless
      `ProcessCandidate` and call `Manager.SetStaticCandidates` (RFD 102)
- [ ] Unit tests: liveness check transitions (found → healthy, N misses →
      unhealthy, process restarts with new PID → healthy again); candidate
      correctly marked `IsClientOnly=true` with no ports

### Phase 4: E2E and documentation

- [ ] E2E test: `coral agent start --connect worker --exe-pattern <pattern>`
      against a portless test binary; assert `coral agent status` reports
      the worker healthy and Beyla config contains the matching `exe_path`
      rule
- [ ] E2E test: kill and restart the matched process; assert the monitor
      re-discovers the new PID and status returns to healthy
- [ ] Update `docs/CLI.md` and `docs/CLI_REFERENCE.md`: document
      `--exe-pattern`, the mutual-exclusion rule with `--port`, and example
      usage
- [ ] Update `docs/SERVICE_DISCOVERY.md`: document process-liveness health
      checking and how explicit exe-pattern connects interact with RFD 102's
      automatic discovery
- [ ] Update `docs/AGENT.md`: document the `--connect ... --exe-pattern`
      startup flag combination

## API Changes

### Modified Protobuf Messages

```protobuf
// proto/coral/mesh/v1/auth.proto
message ServiceInfo {
  string name = 1;
  int32 port = 2;                 // 0 if service is identified by exe_pattern instead.
  string health_endpoint = 3;
  string service_type = 4;
  map<string, string> labels = 5;

  int32 process_id = 6;
  string binary_path = 7;
  string binary_hash = 8;

  // Executable path pattern for portless processes (RFD 112).
  // Regex matched against /proc/<pid>/comm, falling back to /proc/<pid>/cmdline.
  // Mutually exclusive with port: exactly one of the two must be set.
  string exe_pattern = 9;
}
```

```protobuf
// proto/coral/agent/v1/agent.proto
message ConnectServiceRequest {
  string name = 1;
  int32 port = 2;                 // 0 if exe_pattern is set instead.
  string health_endpoint = 3;
  string service_type = 4;
  map<string, string> labels = 5;
  ServiceSdkCapabilities sdk_capabilities = 6;

  // Executable path pattern for portless processes (RFD 112).
  // Mutually exclusive with port.
  string exe_pattern = 7;
}
```

`ConnectServiceResponse` and `DisconnectServiceRequest`/`Response` are
unchanged — a portless service is disconnected by name, same as any other.

### CLI Commands

```bash
# New flag on the existing connect command.
coral connect <name> --exe-pattern <regex> [--agent <id>] [--agent-url <url>]

# Combined with existing --connect at startup.
coral agent start --connect <name> --exe-pattern <regex>

# Example output:
$ coral connect worker --exe-pattern "python.*consumer.py"
Connected service worker (process match: python.*consumer.py)
Health monitoring: process-liveness
eBPF metrics: enabled
```

### Configuration Changes

No new static YAML config fields — `exe_pattern` is a per-connect argument,
not agent-level configuration.

## Testing Strategy

### Unit Tests

- `proc.FindPidByExePattern`: comm match, cmdline-only match, no match,
  multiple matching PIDs (first-match semantics documented and tested).
- Spec parsing: pattern-only, port-only, both given (error), neither given
  (error).
- `ServiceMonitor` liveness branch: healthy → unhealthy after threshold
  misses, recovery on new PID, `ServiceStatusUnknown` before first check.
- `ConnectService` handler: portless candidate built correctly for
  `Manager.SetStaticCandidates`.

### Integration Tests

- Start a portless test process; `coral connect` with `--exe-pattern`
  matching it; assert `ListServices` reports it with `port: 0` and a
  resolved `process_id`.
- Kill the matched process without disconnecting; assert status transitions
  to unhealthy after `MissedLivenessThreshold` checks, then back to healthy
  once a replacement process matching the pattern starts.

### E2E Tests

- See Phase 4 tasks: full `coral agent start --connect ... --exe-pattern`
  flow through to a Beyla `exe_path` rule and a healthy status report.

## Security Considerations

- `exe_pattern` is a regex evaluated against local `/proc` data only — no
  new network exposure. Same trust boundary as existing `--exe-pattern`-free
  connects (an authenticated agent client can already name arbitrary ports).
- A pattern careless enough to match multiple unrelated processes (e.g.
  `".*"`) will bind to whichever PID `FindPidByExePattern` matches first;
  this is a usability footgun, not a security boundary — document the
  first-match behavior clearly rather than adding new validation.

## Implementation Status

**Core Capability:** ⏳ Not Started

`coral connect --exe-pattern` will let users explicitly register portless
processes by executable pattern, resolved to a PID via `/proc` scanning,
monitored via process-liveness instead of network health checks, and fed
into RFD 102's discovery merge as a pinned client-only candidate.

## Future Work

**Multiple exe patterns per connect** (Future)

- Allow a single `coral connect` call to match a set of patterns (e.g. a
  pool of worker processes sharing a binary), reporting them as one logical
  service with multiple PIDs. Deferred because the current one-spec-one-PID
  model matches how `ListServices`/`GetStatus` are shaped today; broadening
  it is a larger change to the service model, not a small extension of this
  RFD.

**Re-matching on restart with a different binary** (Future)

- If the matched process is replaced by a differently-named binary (e.g. a
  deploy that renames the executable), the pattern silently stops matching
  and the service goes unhealthy with no indication why. A future
  enhancement could surface "pattern last matched N minutes ago" in status
  output.

**Kubernetes/container-native equivalents** (Future — see RFD 102's
`ContainerRuntimeProvider`/`KubernetesProvider`)

- Connecting by container name or pod label instead of an exe-path regex is
  a distinct discovery mechanism, tracked under RFD 102's Future Work, not
  duplicated here.
