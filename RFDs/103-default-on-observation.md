---
rfd: "103"
title: "Default-On Observation"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: false
dependencies: [ "053", "102" ]
database_migrations: [ ]
areas: [ "agent", "beyla", "observability" ]
---

# RFD 103 - Default-On Observation

**Status:** 🚧 Draft

## Summary

Remove the requirement to pass `--monitor-all` to collect eBPF RED metrics.
Beyla is enabled by default when the agent starts; an explicit `--no-monitor-all`
flag opts out for resource-constrained hosts. An OTLP feedback callback fires
whenever Beyla reports a new service, giving the agent awareness of observed
processes without any user action.

## Problem

**Current behavior/limitations:**

Without `--monitor-all`, the agent collects nothing. Users must discover this
flag from the docs and pass it explicitly on every agent invocation.

**Why this matters:**

Coral's value proposition is zero-configuration observability. Requiring a flag
to turn on the core capability contradicts that promise and creates a common
"why is nothing showing up?" onboarding failure.

**Use cases affected:**

- New users running `coral agent start` for the first time see no data.
- CI and container deployments that copy example configs without `--monitor-all`
  silently collect nothing.

## Solution

Make `--monitor-all` implicit at startup. The agent always starts Beyla unless
the operator explicitly opts out. A new OTLP ingest callback feeds each newly
observed `(port, pid, service.name)` tuple back to the agent so that upstream
components (service map, naming chain) can react without polling.

**Key Design Decisions:**

- **`--monitor-all` becomes a no-op** accepted for backward compatibility and
  emits a deprecation warning, so existing scripts and docs continue to work.
- **`--no-monitor-all` is the opt-out**, not a flag removal. Resource-constrained
  hosts need a supported escape hatch.
- **OTLP feedback is the trigger for service awareness**, not a proactive poll.
  The agent learns about a process the first time Beyla emits a span or metric
  for it, keeping startup overhead minimal.
- **RFD 102 is a prerequisite.** Without per-process named Beyla rules, the
  OTLP feedback callback would receive `coral-agent` as the service name for
  every observed process, making the data useless. RFD 102 must ship first.

**Benefits:**

- `coral agent start` just works — no flags required.
- Every new process that starts listening is automatically observed within one
  Beyla poll cycle.
- Downstream RFDs (105 auto-naming, 107 `coral services`) receive correct input
  from day one.

**Architecture Overview:**

```
coral agent start  (no flags required)
        │
        ▼
┌───────────────────────────────────────────────────┐
│              Beyla (always started)               │
│  named rules per process (RFD 102)                │
│  port 3000 → spans/metrics → OTLP receiver        │
└──────────────────────┬────────────────────────────┘
                       │ resource attrs:
                       │   service.name = "node"
                       │   net.host.port = "3000"
                       ▼
          onBeylaServiceObserved(port, pid, name)
                       │
                       ▼
            Agent (service map, RFD 105)
```

### Component Changes

1. **Agent startup** (`internal/cli/agent/startup/`):
   - Remove the `--monitor-all` gate; Beyla starts unconditionally unless
     `--no-monitor-all` is set.
   - Accept `--monitor-all` as a no-op; log a deprecation warning.
   - Remove `--connect` flag from startup (superseded by default observation).

2. **Beyla manager** (`internal/agent/beyla/`):
   - On each OTLP ingest, extract `service.name`, `net.host.port` (or
     `server.port`), and process ID from span/metric resource attributes.
   - Call `onBeylaServiceObserved(port int32, pid int32, observedName string)`
     on the agent on first observation of each port.

3. **Agent** (`internal/agent/agent.go`):
   - Add `onBeylaServiceObserved` method stub; initial implementation logs the
     observation. Full service map integration is in RFD 105.

**Configuration:**

```yaml
# coral.yaml (agent config)
agent:
  # Default: true. Set false to disable Beyla on resource-constrained hosts.
  monitor_all: true
```

```bash
# Default: observe everything (no flag needed)
coral agent start

# Opt out on resource-constrained hosts
coral agent start --no-monitor-all

# Legacy flag accepted, now a no-op (emits deprecation warning)
coral agent start --monitor-all
```

## Implementation Plan

### Phase 1: Startup flag changes

- [ ] Remove `--monitor-all` gate from agent startup; set Beyla as
      default-enabled
- [ ] Add `--no-monitor-all` flag to `coral agent start`
- [ ] Accept `--monitor-all` as a no-op; emit deprecation warning to stderr
- [ ] Remove `--connect` flag from agent startup
- [ ] Update `coral.yaml` schema: `agent.monitor_all` defaults to `true`

### Phase 2: OTLP feedback callback

- [ ] Add `onBeylaServiceObserved(port int32, pid int32, observedName string)`
      to `Agent`
- [ ] Wire callback into Beyla manager OTLP ingest path: extract
      `net.host.port` (or `server.port`) and `service.name` from each incoming
      span/metric resource before DuckDB write
- [ ] Deduplicate: only fire callback on first observation of each port within
      a session
- [ ] Log observed `(port, name)` pairs at DEBUG level

### Phase 3: Testing and documentation

- [ ] Unit test: agent starts without `--monitor-all`; assert Beyla manager
      started
- [ ] Unit test: agent starts with `--no-monitor-all`; assert Beyla manager
      not started
- [ ] Unit test: `--monitor-all` flag accepted without error; deprecation
      warning emitted
- [ ] Integration test: send synthetic OTLP span for port 3000; assert
      `onBeylaServiceObserved` fires with correct port and name
- [ ] Update `docs/AGENT.md`: document default-on behaviour and
      `--no-monitor-all`
- [ ] Update `docs/CLI_REFERENCE.md`: document flag changes

## API Changes

No protobuf changes. Flag and configuration changes only.

### CLI Changes

```bash
# Before (required flag)
coral agent start --monitor-all

# After (implicit — no flag required)
coral agent start

# Opt-out
coral agent start --no-monitor-all
```

### Configuration Changes

```yaml
# coral.yaml — new default
agent:
  monitor_all: true  # default, can be omitted
```

## Testing Strategy

### Unit Tests

- `TestAgentStart_DefaultMonitorAll` — starts without flag; Beyla manager
  launched.
- `TestAgentStart_NoMonitorAll` — `--no-monitor-all`; Beyla manager not
  launched.
- `TestAgentStart_MonitorAllNoOp` — `--monitor-all` accepted; deprecation
  warning in stderr.
- `TestOTLPCallback_Fires` — synthetic OTLP ingest triggers
  `onBeylaServiceObserved`.
- `TestOTLPCallback_Deduplicates` — second OTLP span for same port does not
  re-fire callback.

### E2E Tests

- Start agent with no flags; assert Beyla is running and collecting spans from
  otel-app within the poll window.

## Implementation Status

**Core Capability:** ⏳ Not Started

## Future Work

**Auto-naming and service map** (RFD 105)

The `onBeylaServiceObserved` callback introduced here is consumed by the
`ServiceNameAdaptor` chain and unified service map in RFD 105.

**`coral services` CLI** (RFD 107)

The default-on observation enables `coral services` to list processes without
any prior `coral connect` step.
