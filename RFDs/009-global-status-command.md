---
rfd: "009"
title: "Global Status Command for Multi-Colony Overview"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: false
dependencies: [ "006" ]
database_migrations: [ ]
areas: [ "cli", "observability", "ux" ]
---

# RFD 009 - Global Status Command for Multi-Colony Overview

**Status:** 🎉 Implemented

## Summary

Add a top-level `coral status` command that provides a unified dashboard view of
all configured colonies and the overall Coral environment. This complements the
existing `coral colony status` (single colony details) and `coral colony list` (
basic colony enumeration) by giving operators a comprehensive at-a-glance view
of their entire distributed application landscape.

## Problem

**Current behavior/limitations:**

- No single command to view the health of all colonies at once.
- `coral colony list` shows configured colonies but not runtime status (
  running/stopped).
- `coral colony status` requires knowing which colony to inspect and only shows
  one at a time.
- Operators managing multiple environments (prod, staging, dev) must run
  multiple commands to get overall system health.
- No visibility into discovery service health from the CLI.
- Network endpoint information (added in RFD 006) is only visible per-colony,
  not in aggregate view.

**Why this matters:**

- **Multi-environment operations**: Teams run multiple colonies (production,
  staging, development) and need to see status across all of them quickly.
- **Troubleshooting efficiency**: When investigating issues, operators need to
  quickly identify which colonies are running vs stopped vs degraded.
- **Discovery service visibility**: The discovery service is a critical shared
  component, but its health isn't visible in CLI.
- **Onboarding**: New team members need to understand what colonies exist and
  their current state without deep CLI knowledge.

**Use cases affected:**

- SRE checking overall system health: "Are all my environments up?"
- Developer starting work: "Which colonies are running right now?"
- Incident response: "Quick status of all components before diving into
  specifics"
- Troubleshooting connectivity: "Can I reach the discovery service? Which
  colonies are reachable?"

## Solution

Create a top-level `coral status` command that provides an executive dashboard
of the entire Coral environment, distinct from existing commands.

**Key Design Decisions:**

- **Top-level command**: `coral status` to emphasize system-wide view.
- **Quick health checks**: Query all colonies with short timeouts (500ms) to
  avoid blocking on unhealthy colonies.
- **Tabular output**: Condensed table format showing key metrics at a glance.
- **Discovery service ping**: Include discovery service health check in overview
  section.
- **Network endpoint summary**: Show both local and mesh endpoints for running
  colonies.
- **Consistent with existing patterns**: Reuse colony API client and config
  loading logic from `colony list` and `colony status`.

**Benefits:**

- Single command for complete system overview.
- Fast troubleshooting: identify unhealthy colonies immediately.
- Reduced cognitive load: no need to remember multiple colony IDs.
- Better operational visibility for multi-environment setups.
- Foundation for future monitoring integrations (Prometheus, dashboards).

**Information Hierarchy:**

```
┌─────────────────────────────────────────────────────────────┐
│ Coral Environment Status                                    │
│                                                              │
│ Global Overview:                                            │
│   - Discovery service health                                │
│   - Total colonies (running/stopped count)                  │
│   - Coral version                                           │
│                                                              │
│ Per-Colony Summary Table:                                   │
│   - Colony ID & environment                                 │
│   - Runtime status (running/stopped/degraded/unhealthy)    │
│   - Uptime (if running)                                     │
│   - Agent count (if running)                                │
│   - Network ports (WireGuard/Connect)                       │
│   - Endpoint URLs (local/mesh)                              │
│   - Default marker                                          │
│                                                              │
│ Action Hints:                                               │
│   - "Use 'coral colony status <id>' for details"           │
└─────────────────────────────────────────────────────────────┘
```

### Component Changes

1. **New CLI Command** (new file: `internal/cli/status.go`):
    - Implements `newStatusCmd()` returning cobra.Command.
    - Queries global config for discovery endpoint.
    - Loads all configured colonies via config loader.
    - Performs health check against discovery service.
    - Attempts to connect to each colony's RPC endpoint (quick timeout).
    - Formats output as table (default) or JSON (`--json` flag).
    - Supports verbose mode (`--verbose` or `-v`) for additional details (mesh
      IPs, public keys).

2. **Root CLI** (modified: `internal/cli/root.go`):
    - Register `newStatusCmd()` as direct child of root command.
    - Position in help text between `init` and `colony` commands.

3. **Reused Components** (no changes):
    - `config.Loader` for loading colonies.
    - `colonyv1connect.ColonyServiceClient` for querying colony status.
    - Discovery client for health check (from `internal/discovery/client`).

**Configuration Example:**

No configuration changes required. Command reads existing configuration:

```yaml
# ~/.coral/config.yaml (global config)
default_colony: my-app-prod
discovery:
    endpoint: https://discovery.coral.dev
    timeout: 10s

# ~/.coral/colonies/*.yaml (per-colony configs)
# Existing colony configurations used as-is
```

## Implementation Plan

### Phase 1: Core Command Structure

- [ ] Create `internal/cli/status.go` with basic cobra command skeleton.
- [ ] Implement colony enumeration using config loader.
- [ ] Add discovery service health check.
- [ ] Register command in root CLI.
- [ ] Verify command appears in `coral --help`.

### Phase 2: Colony Status Collection

- [ ] Implement parallel colony status queries with timeout.
- [ ] Extract status, uptime, agent count from RPC responses.
- [ ] Handle unreachable colonies gracefully (mark as "stopped").
- [ ] Collect network endpoint information (ports, URLs).
- [ ] Calculate aggregate statistics (running/stopped counts).

### Phase 3: Output Formatting

- [ ] Implement tabular output with aligned columns.
- [ ] Add color coding for status indicators (optional, check terminal support).
- [ ] Implement JSON output mode for machine readability.
- [ ] Add verbose mode with mesh IPs and public keys.
- [ ] Include action hints footer.

### Phase 4: Testing & Documentation

- [ ] Add unit tests for output formatting logic.
- [ ] Test with zero colonies configured.
- [ ] Test with mix of running/stopped colonies.
- [ ] Test with unreachable discovery service.
- [ ] Update `USAGE.md` with command examples.
- [ ] Add code comments following Go Doc Comments style.

## CLI Commands

### Primary Command

```bash
coral status

# Example output:
Coral Environment Status
========================

Discovery: https://discovery.coral.dev (healthy)
Colonies:  3 total (2 running, 1 stopped)
Version:   coral v0.1.0

COLONY ID        ENV      STATUS      UPTIME    AGENTS  NETWORK        ENDPOINTS
---------------- -------- ----------- --------- ------- -------------- ---------------------------
my-app-prod*     prod     running     2d 3h     5       51820/9000     localhost:9000, 10.42.0.1:9000
my-app-staging   staging  running     1h 15m    2       51821/9001     localhost:9001, 10.42.0.2:9001
my-app-dev       dev      stopped     -         -       51822/9002     -

* default colony

Use 'coral colony status <id>' for detailed information
```

### JSON Output

```bash
coral status --json

# Example output:
{
  "discovery": {
    "endpoint": "https://discovery.coral.dev",
    "healthy": true
  },
  "colonies": [
    {
      "colony_id": "my-app-prod",
      "environment": "prod",
      "is_default": true,
      "running": true,
      "status": "running",
      "uptime_seconds": 184920,
      "agent_count": 5,
      "wireguard_port": 51820,
      "connect_port": 9000,
      "local_endpoint": "http://localhost:9000",
      "mesh_endpoint": "http://10.42.0.1:9000",
      "mesh_ipv4": "10.42.0.1"
    },
    ...
  ],
  "summary": {
    "total": 3,
    "running": 2,
    "stopped": 1
  },
  "version": "v0.1.0"
}
```

### Verbose Output

```bash
coral status --verbose

# Example output (additional fields):
Coral Environment Status
========================

Discovery: https://discovery.coral.dev (healthy)
  Endpoint: https://discovery.coral.dev
  Timeout:  10s
Colonies:  3 total (2 running, 1 stopped)
Version:   coral v0.1.0 (commit: abc1234, built: 2025-10-30)

COLONY ID        ENV      STATUS      UPTIME    AGENTS  NETWORK        MESH IP      PUBLIC KEY
---------------- -------- ----------- --------- ------- -------------- ------------ ----------------
my-app-prod*     prod     running     2d 3h     5       51820/9000     10.42.0.1    abc123...xyz4
my-app-staging   staging  running     1h 15m    2       51821/9001     10.42.0.2    def456...uvw8
my-app-dev       dev      stopped     -         -       51822/9002     10.42.0.3    -

* default colony

Endpoints:
  my-app-prod:     http://localhost:9000 (local), http://10.42.0.1:9000 (mesh)
  my-app-staging:  http://localhost:9001 (local), http://10.42.0.2:9001 (mesh)

Use 'coral colony status <id>' for detailed information
```

## Testing Strategy

### Unit Tests

**Status Command Tests (`internal/cli/status_test.go`):**

- Output formatting with zero colonies.
- Output formatting with all running colonies.
- Output formatting with mix of running/stopped colonies.
- JSON output structure validation.
- Verbose mode field additions.
- Discovery service healthy vs unhealthy states.

### Integration Tests

**End-to-End Scenarios:**

- Start multiple colonies, verify all appear as running.
- Stop one colony, verify status changes to stopped.
- Disconnect discovery service, verify health indicator changes.
- Query with default colony vs non-default colonies.
- Verify timeout behavior with unresponsive colonies.

### Manual Testing

**Cross-platform validation:**

- macOS terminal output rendering.
- Linux terminal output rendering.
- Windows terminal output rendering (if supported).
- JSON output piping to `jq`.

## Security Considerations

**Local-only Command:**

- Command queries localhost endpoints, no remote connections except discovery
  service.
- Discovery service uses HTTPS with certificate validation.
- No authentication required for local colony RPC endpoints:
    - Local endpoint (`localhost:<connect_port>`) accessible to any process on
      the same host.
    - Mesh endpoint (`<mesh_ip>:<connect_port>`) protected by WireGuard tunnel
      authentication.
    - This command uses the local endpoint for querying colonies on the same
      host.

**Information Disclosure:**

- Status output may reveal colony IDs and network topology.
- Acceptable risk: CLI runs locally, output goes to user's terminal.
- Future: Consider `--redact` flag for shared screen scenarios.

## Future Enhancements

**Real-time Monitoring:**

- Add `coral status --watch` mode for continuous updates.
- Terminal UI (TUI) with refresh interval.
- Live updating of status, uptime, agent counts.

**Health Scores:**

- Calculate aggregate health score based on degraded/unhealthy agents.
- Color-coded indicators (green/yellow/red).
- Historical health trends (requires DuckDB integration).

**Prometheus Integration:**

- Export status as Prometheus metrics.
- `coral status --prometheus` outputs metrics format.
- Enables alerting on colony health.

**MCP Integration:**

- Expose status via MCP server for Claude Desktop.
- AI can query system health and suggest actions.
- Natural language queries: "which colonies are unhealthy?"

**Filtering and Sorting:**

- `coral status --env prod` to show only production colonies.
- `coral status --running` to show only active colonies.
- Sort by uptime, agent count, or colony ID.

## Appendix

### Command Comparison Matrix

| Command               | Scope         | Detail Level  | Use Case                       |
|-----------------------|---------------|---------------|--------------------------------|
| `coral status`        | All colonies  | Summary       | Quick system overview          |
| `coral colony list`   | All colonies  | Configuration | See what's configured          |
| `coral colony status` | Single colony | Full details  | Deep dive into one colony      |
| `coral colony agents` | Single colony | Agent list    | Troubleshoot agent connections |

### Discovery Health Check Logic

**Health determination:**

```
healthy:   Discovery service responds to /health within timeout (< 2s)
unhealthy: Discovery service unreachable or timeout exceeded
```

**Implementation:**

- Use existing `internal/discovery/client` package.
- Call `Health()` method with context timeout.
- Display endpoint URL and health status in overview.

### Colony Status Query Logic

**Parallel queries with timeout:**

- Create goroutines for each colony status check.
- Use 500ms timeout per colony (fast fail for unreachable).
- Collect results via channels.
- Mark unreachable colonies as "stopped" (assume not running).

**Why parallel:**

- Sequential queries would take too long with multiple colonies.
- Timeout on one colony shouldn't block others.
- Better user experience: fast response even with degraded colonies.

### Output Formatting Guidelines

**Table columns:**

- Fixed-width columns for alignment.
- Use padding for readability.
- Truncate long colony IDs if necessary (show full in verbose).
- Right-align numeric values (uptime, agent count).
- Left-align text values (colony ID, status).

**Status indicators:**

- `running`: Colony responds with status="running".
- `degraded`: Colony responds with status="degraded".
- `unhealthy`: Colony responds with status="unhealthy".
- `stopped`: Colony unreachable (timeout or connection refused).

**Uptime formatting:**

- `< 1h`: Show minutes and seconds (e.g., "15m 30s").
- `1h - 24h`: Show hours and minutes (e.g., "5h 20m").
- `> 24h`: Show days and hours (e.g., "2d 3h").

### Reference Implementation

**Similar patterns in codebase:**

- `coral colony list` - Iterating over colonies, formatting table output.
- `coral colony status` - Querying single colony RPC endpoint.
- Discovery client health check - `internal/discovery/client/client.go`.

---

## Notes

**Design Philosophy:**

- User-centric: Optimize for quick answers to "what's the status?"
- Fail fast: Don't block on unhealthy colonies.
- Actionable: Guide users to next steps (use colony status for details).
- Progressive disclosure: Default view is concise, verbose mode adds depth.

**Why Not Extend `colony list`:**

- `colony list` focuses on configuration (what exists).
- `coral status` focuses on operations (what's running).
- Different mental models: configuration vs runtime.
- Top-level placement emphasizes system-wide importance.

**Why Short Timeouts:**

- 500ms per colony is enough for healthy systems.
- Prevents blocking on network issues.
- User expectation: status command should be fast.
- Trade-off: May miss slow-responding but healthy colonies (rare).
