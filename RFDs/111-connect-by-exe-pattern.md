---
rfd: "111"
title: "Connect by Executable Pattern"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "053", "102" ]
database_migrations: [ ]
areas: [ "agent", "cli", "service-discovery", "beyla" ]
---

# RFD 111 - Connect by Executable Pattern

**Status:** 🎉 Implemented

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

RFD 102 closes part of this gap, but only for _automatic_ discovery:
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

**Readiness gaps found at implementation time** (the code moved since this
RFD was drafted; RFD 104's unified service map landed after RFD 102):

- `Agent.services` is `map[int32]*ServiceEntry`, and `ServiceEntry` is
  documented as "the agent's single record of truth for a port" (RFD 104).
  Every `exe_pattern` connect has `Port == 0`, so two portless connects would
  collide on the same map key. Fix: give `ServiceEntry` its own map-identity
  key, independent of `Port` — `Port` continues to mean "the real TCP port,
  0 if none" everywhere it's surfaced (API, `Resolve`), while the map is
  keyed by a `serviceKey` computed as `port:<n>` for port-based entries or
  `exe:<name>` for portless ones.
- `connectServiceLocked` returns early (no `ServiceMonitor` is created) when
  `HealthEndpoint == ""`. An `exe_pattern` connect never has a health
  endpoint, so under the current gate it would never get a monitor —
  liveness checking would never run. Fix: also start a monitor when
  `Port == 0 && ExePattern != ""`.
- `ProcessCandidate` (RFD 102, `internal/agent/beyla/discovery/discovery.go`)
  has no field for a literal exe-path regex; `generateBeylaConfig`'s
  client-only branch derives the Beyla `exe_path` rule from
  `".*" + candidate.Name + ".*"`, not from any pattern the caller supplied.
  So today an explicit `--exe-pattern "python.*consumer.py"` would never
  reach Beyla — only the service name would. Fix: add
  `ExePathPattern string` to `ProcessCandidate`; when set, `generateBeylaConfig`
  emits it verbatim as the rule's `ExePath` instead of the name-derived
  pattern.

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

# At agent startup, alongside port-based services (agent.yaml — `coral
# agent start` has no --connect flag; startup services come from the config
# file's `services:` list or the CORAL_SERVICES env var):
#   services:
#     - name: api
#       port: 8080
#       health_endpoint: /health
#     - name: worker
#       exe_pattern: "python.*consumer.py"

# Rejected: neither port nor pattern given.
$ coral connect worker
Error: service "worker" needs either a port or --exe-pattern

# Rejected: both given.
$ coral connect worker:8080 --exe-pattern "python.*consumer.py"
Error: service "worker" cannot specify both a port and --exe-pattern
```

## Implementation Plan

### Phase 1: Proto and process-resolution primitives

- [x] Add `exe_pattern` field to `ServiceInfo` (`proto/coral/mesh/v1/auth.proto`)
      and `ConnectServiceRequest` (`proto/coral/agent/v1/agent.proto`);
      regenerate Go bindings (also added to `ServiceStatus` for API visibility)
- [x] Implement `proc.FindPidByExePattern` in `internal/sys/proc/proc.go`
      (comm match, cmdline fallback)
- [x] Unit tests: pattern matches (via cmdline fallback against the running
      test binary), no match, invalid regex rejected

### Phase 2: CLI and RPC validation

- [x] Add `--exe-pattern` flag to `coral connect` (legacy single-service
      form, same restriction as `--port`/`--health`). Note: `coral agent
      start` has no `--connect` flag today — startup services come from
      `agent.yaml`'s `services:` list or the `CORAL_SERVICES` env var; added
      `exe_pattern` to the YAML schema (`internal/config/schema.go`) instead
- [x] Extend spec parsing to allow an omitted port when `--exe-pattern` is
      set; reject neither-nor and both-set cases with a clear error
- [x] Server-side validation in the `ConnectService` handler: exactly one of
      `port`/`exe_pattern` set
- [x] Unit tests: spec parsing (pattern-only, port-only, both, neither)

### Phase 3: Agent-side monitoring and discovery integration

- [x] `ServiceMonitor.discoverProcessInfo`: branch to
      `FindPidByExePattern` when `Port == 0`
- [x] `ServiceMonitor.performHealthCheck`: add process-liveness branch with
      `MissedLivenessThreshold` debounce (`constants.DefaultMissedLivenessThreshold`, default 2)
- [x] `Agent.services` re-keyed from `map[int32]*ServiceEntry` (port-only) to
      `map[string]*ServiceEntry` via a new `serviceKey(port, name)` helper —
      port-based entries key by port, portless entries key by name, so
      multiple `exe_pattern` connects don't collide (readiness gap #1)
- [x] `connectServiceLocked`: also start a monitor when `Port == 0 &&
      ExePattern != ""` (previously gated on `HealthEndpoint != ""` only,
      which would have silently skipped monitoring for every portless
      connect — readiness gap #2)
- [x] `collectDiscoveryCandidatesLocked` / `ConnectService` / `DisconnectService`:
      build a portless `ProcessCandidate` (`IsClientOnly: true`, no ports,
      `ExePathPattern` set) and call `Manager.SetStaticCandidates` (RFD 102)
- [x] `ProcessCandidate.ExePathPattern` (new field) and
      `generateBeylaConfig`: emit the literal pattern as the Beyla rule's
      `ExePath` when set, instead of always deriving it from the service
      name (readiness gap #3)
- [x] Unit tests: liveness check transitions (unknown before first check,
      held below miss threshold, unhealthy at threshold, recovers on
      match); multiple exe_pattern connects don't collide in the service
      map; discovery candidate carries `ExePathPattern` with
      `IsClientOnly=true` and no ports; RPC validation (neither/both
      port+exe_pattern rejected)

### Phase 4: E2E and documentation

- [x] Update `docs/CLI.md` and `docs/CLI_REFERENCE.md`: document
      `--exe-pattern`, the mutual-exclusion rule with `--port`, and example
      usage
- [x] Update `docs/SERVICE_DISCOVERY.md`: document process-liveness health
      checking and how explicit exe-pattern connects interact with RFD 102's
      automatic discovery
- [x] Update `docs/AGENT.md`: document `coral connect --exe-pattern` and the
      `services:` config-file equivalent (not a `--connect` startup flag,
      which `coral agent start` doesn't have — see readiness gaps)
- [x] E2E test: `coral connect worker --exe-pattern <pattern>` against a
      portless test binary; assert the agent reports the worker healthy and
      Beyla config contains the matching `exe_path` rule
      (`tests/e2e/distributed/service_exe_pattern_test.go`,
      `TestExePatternConnection`). Reuses `worker-app`, the portless fixture
      already added for RFD 102's client-only auto-discovery test — no new
      fixture or Docker Compose service needed
- [x] E2E test: kill and restart the matched process; assert the monitor
      re-discovers the new PID and status returns to healthy
      (`TestExePatternRecoversAfterRestart`, via `RestartService("worker-app")`)
- [x] E2E test: mutual-exclusion validation over the wire
      (`TestExePatternRejectsPortAndPattern`)

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

  // Executable path pattern for portless processes (RFD 111).
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

  // Executable path pattern for portless processes (RFD 111).
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

# Example output:
$ coral connect worker --exe-pattern "python.*consumer.py"
Connected service worker (process match: python.*consumer.py)
Health monitoring: process-liveness
eBPF metrics: enabled
```

### Configuration Changes

`coral agent start` has no `--connect` flag; startup services are declared
in `agent.yaml`'s `services:` list (or `CORAL_SERVICES`). Added `exe_pattern`
as an optional field there, mutually exclusive with `port`:

```yaml
services:
  - name: worker
    exe_pattern: "python.*consumer.py"
```

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

`tests/e2e/distributed/service_exe_pattern_test.go` (`ExePatternSuite`,
run as part of `Test2_ServiceManagement`):

- `TestExePatternConnection`: full `coral connect ... --exe-pattern` flow —
  connect, resolve PID, healthy status, and a Beyla `exe_path` rule carrying
  the literal pattern.
- `TestExePatternRecoversAfterRestart`: restarts the matched container and
  asserts the monitor lands on the new PID and recovers to healthy.
- `TestExePatternRejectsPortAndPattern`: mutual-exclusion validation over
  the wire.

Reuses `worker-app` (`tests/e2e/distributed/fixtures/apps/worker-app`), the
portless fixture already added for RFD 102's client-only auto-discovery
test, under a distinct service name so the explicit and automatic paths
don't collide in the agent's service map.

## Security Considerations

- `exe_pattern` is a regex evaluated against local `/proc` data only — no
  new network exposure. Same trust boundary as existing `--exe-pattern`-free
  connects (an authenticated agent client can already name arbitrary ports).
- A pattern careless enough to match multiple unrelated processes (e.g.
  `".*"`) will bind to whichever PID `FindPidByExePattern` matches first;
  this is a usability footgun, not a security boundary — document the
  first-match behavior clearly rather than adding new validation.

## Implementation Status

**Core Capability:** 🎉 Implemented

`coral connect --exe-pattern` lets users explicitly register a portless
process (a Kafka consumer, cron job, or batch worker that never binds a
listening port) by executable pattern instead of `name:port`. The agent
resolves the pattern to a PID via `/proc` scanning, monitors it by
process-liveness instead of a network health check, and feeds it into RFD
102's discovery merge as a pinned client-only candidate with the literal
pattern as its Beyla `exe_path` rule.

Operational:

- ✅ `exe_pattern` field on `ServiceInfo`/`ConnectServiceRequest`/`ServiceStatus`
  protos (`proto/coral/mesh/v1/auth.proto`, `proto/coral/agent/v1/agent.proto`).
- ✅ `proc.FindPidByExePattern` (`internal/sys/proc/proc.go`): regex against
  `/proc/<pid>/comm`, falling back to `/proc/<pid>/cmdline`.
- ✅ `coral connect worker --exe-pattern <regex>` (legacy single-service
  form, mutually exclusive with `--port`); `agent.yaml`'s `services:` list
  gained the same `exe_pattern` field for startup-time equivalents (`coral
  agent start` has no `--connect` flag today).
- ✅ Server-side and CLI-side validation: exactly one of port/exe_pattern.
- ✅ `ServiceMonitor` process-liveness health check with
  `constants.DefaultMissedLivenessThreshold` (2) miss debounce.
- ✅ `Agent.services` re-keyed from port-only to `serviceKey(port, name)` so
  multiple portless connects coexist without colliding.
- ✅ `connectServiceLocked` now also starts a monitor for portless connects
  (previously gated on `HealthEndpoint != ""` only).
- ✅ `ProcessCandidate.ExePathPattern` carries the literal regex through to
  `generateBeylaConfig`, which emits it verbatim instead of a name-derived
  `.*name.*` rule.
- ✅ Docs: `docs/CLI.md`, `docs/CLI_REFERENCE.md`, `docs/SERVICE_DISCOVERY.md`,
  `docs/AGENT.md`.
- ✅ E2E: `tests/e2e/distributed/service_exe_pattern_test.go` (`ExePatternSuite`),
  reusing RFD 102's `worker-app` fixture — connect/resolve/Beyla-rule flow,
  kill/restart PID recovery, and wire-level mutual-exclusion validation.

Three readiness gaps were found against the current codebase at
implementation time (the code had moved since this RFD was drafted — see
Key Design Decisions) and fixed inline rather than deferred, since the
feature would have been broken without them (colliding portless entries,
monitors never starting, patterns never reaching Beyla).

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
