---
rfd: "106"
title: "coral services Root Command"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: false
dependencies: [ "044", "105" ]
database_migrations: [ ]
areas: [ "cli", "service-discovery" ]
---

# RFD 106 - coral services Root Command

**Status:** 🚧 Draft

## Summary

Introduce `coral services` as a root-level command that is the single place to
list and manage services at any scope (colony-wide, per-agent, or local).
Demote `coral connect` to a permanent backward-compatible alias for
`coral services watch`. Remove the `service` subcommand from `coral colony`.

## Problem

**Current behavior/limitations:**

- Colony-level service listing lives at `coral colony service list` — three
  words deep and hard to discover.
- Agent-level service detail has no CLI surface at all; the only way to see
  auto-observed processes is a raw DuckDB query.
- `coral connect` implies it establishes a network link, when it actually
  registers a process for enriched observation.
- `coral colony` mixes infrastructure commands (start/stop, agents, certs)
  with service management, making neither area easy to find.

**Why this matters:**

Services are the primary object users interact with. Burying them two levels
deep in `coral colony` produces a common support question: "how do I see what
coral is monitoring?"

## Solution

A new `internal/cli/services/` package implements `coral services` as a
first-class root command. It delegates to the agent `ListServices` RPC (RFD
105) for data and supports colony-wide, per-agent, and local scopes via flags.

**Key Design Decisions:**

- **`coral services` defaults to the list action.** Running it with no
  subcommand lists services, identical to `coral services list`. The explicit
  subcommand exists for scripting clarity but is not required.
- **`coral connect` is a permanent alias**, not deprecated. Existing scripts,
  CI configs, and doc links continue to work unchanged.
- **`coral colony service list` is a permanent hidden alias** to
  `coral services`. Same semantics; the old path just routes to the new
  implementation.
- **`coral colony` becomes infrastructure-only.** The `service` subcommand is
  removed. Colony retains: start/stop/status, agents, mcp, ca/psk/token,
  export/import, list/use/current/add-remote.
- **Output columns adapt by scope.** Colony scope shows cross-agent
  aggregation; agent scope adds PID, tier, and health detail.

**Architecture Overview:**

```
coral services                    → colony ListServices (aggregated)
coral services --agent <id>       → agent ListServices via mesh IP (RFD 044)
coral services --local            → localhost:9001 ListServices (no colony)
coral services watch <args>       → ConnectService RPC (enrichment)
coral connect <args>              → alias → coral services watch
coral colony service list         → hidden alias → coral services
```

### Component Changes

1. **New package** (`internal/cli/services/`):
   - `root.go`: registers `coral services` in the root command; default action
     calls `list.go`.
   - `list.go`: colony-wide list by default; `--agent <id>` routes to that
     agent's `ListServices` via mesh IP lookup (RFD 044); `--local` hits
     `localhost:9001`; `--source auto|watched` maps to `source_filter`.
   - `watch.go`: moves logic from `internal/cli/agent/connect.go`; updates
     help text to describe enrichment semantics.

2. **`internal/cli/root.go`**:
   - Register `coral services` command.
   - Remove `agent.NewConnectCmd()` registration; register `coral connect` as
     a hidden alias for `coral services watch`.

3. **`internal/cli/colony/commands.go`**:
   - Remove `newServiceCmd()`.
   - Register `coral colony service list` as a hidden alias for
     `coral services`.

## Implementation Plan

### Phase 1: Package scaffold and list command

- [ ] Create `internal/cli/services/` package with `root.go`, `list.go`,
      `watch.go`
- [ ] `list.go`: colony-wide default; `--agent <id>` routes via mesh IP;
      `--local` hits `localhost:9001`
- [ ] Colony-scope output columns: `SERVICE`, `TYPE`, `INSTANCES`, `SOURCE`,
      `AGENTS`
- [ ] Agent-scope output columns: `NAME`, `PORT`, `SOURCE`, `TIER`, `PID`,
      `HEALTH`
- [ ] `--source auto|watched` flag wired to `source_filter` in
      `ListServicesRequest`

### Phase 2: Watch subcommand and aliases

- [ ] `watch.go`: move logic from `internal/cli/agent/connect.go`; update help
      text
- [ ] Register `coral services` in `internal/cli/root.go`
- [ ] Remove `agent.NewConnectCmd()` from root; register `coral connect` as
      hidden alias for `coral services watch`
- [ ] Remove `newServiceCmd()` from `internal/cli/colony/commands.go`
- [ ] Register `coral colony service list` as hidden alias for `coral services`

### Phase 3: Agent status cleanup

- [ ] Remove service list section from `coral agent status` output; it should
      show agent health and component state only

### Phase 4: Testing and documentation

- [ ] E2E test: `coral colony service list` alias produces identical output to
      `coral services`
- [ ] E2E test: `coral connect frontend:3000` alias produces identical output
      to `coral services watch frontend:3000`
- [ ] E2E test: `coral services --local` lists auto-observed services after
      default-on agent start
- [ ] E2E test: `coral services --agent <id>` routes to correct agent and
      renders agent-scope columns
- [ ] Update `docs/CLI.md` and `docs/CLI_REFERENCE.md`: document
      `coral services`, `coral services watch`, note aliases
- [ ] Update `docs/SERVICE_DISCOVERY.md`: document discovery-to-service flow

## API Changes

No protobuf changes. CLI surface changes only.

### CLI Commands

**Colony-wide list (default):**

```bash
coral services

# Example output:
Services (4) at 2026-03-20 14:23:00 UTC:

SERVICE        TYPE   INSTANCES  SOURCE     AGENTS
api            http   2          watched    agent-eu-1 (✓), agent-us-1 (✓)
frontend       http   1          watched    agent-eu-1 (✓)
redis-server   -      1          auto       agent-eu-1 (?)
postgresql     -      1          auto       agent-us-1 (?)
```

**Agent-scoped view:**

```bash
coral services --agent agent-eu-1

# Example output:
Services on agent-eu-1:

NAME           PORT   SOURCE   TIER   PID     HEALTH
frontend       3000   watched  0+1    12345   healthy
api            8080   watched  0+1    12346   healthy
redis-server   6379   auto     0      12347   -
postgresql     5432   auto     0      12349   -
```

**Local (no colony required):**

```bash
coral services --local
```

**Filter by source:**

```bash
coral services --source auto
coral services --agent agent-eu-1 --source watched
```

**Explicit enrichment (watch subcommand):**

```bash
# Name-only (no ServiceMonitor)
coral services watch redis:6379

# Name + health endpoint (activates Tier 1)
coral services watch frontend:3000:/health

# Multiple services
coral services watch frontend:3000:/health api:8080:/healthz redis:6379

# Target a remote agent
coral services watch frontend:3000 --agent agent-eu-1

# Permanent backward-compatible aliases
coral connect frontend:3000:/health
```

**`coral colony` after cleanup:**

```bash
coral colony start / stop / status     # lifecycle
coral colony agents                    # agent registry
coral colony mcp                       # MCP server management
coral colony ca / psk / token          # security
coral colony list / use / current      # context switching
coral colony add-remote                # multi-colony (RFD 031)
# coral colony service  ← removed; use coral services
```

## Testing Strategy

### E2E Tests

- `coral services` (no flags) returns colony-wide list matching
  `coral colony service list`.
- `coral connect frontend:3000` produces identical result to
  `coral services watch frontend:3000`.
- `coral services --local` lists auto-observed services without colony
  connectivity.
- `coral services --agent <id>` renders agent-scope columns including `TIER`
  and `PID`.
- `coral services --source auto` omits watched services.
- `coral colony service list` hidden alias produces identical output to
  `coral services`.

## Implementation Status

**Core Capability:** ⏳ Not Started

## Future Work

**Declarative service config** (Future RFD)

`coral.yaml` port-to-name mappings as an alternative to running
`coral services watch` at runtime:

```yaml
agent:
  services:
    - port: 3000
      name: frontend
      health: /health
```

**`--no-monitor-all` with explicit port allowlist** (Future)

```bash
coral agent start --no-monitor-all --monitor-ports 3000,8080
```
