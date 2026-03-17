---
rfd: "100"
title: "CLI-Native Agent Tool Dispatch"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: []
database_migrations: []
areas: ["ask", "tui", "mcp", "cli"]
---

# RFD 100 - CLI-Native Agent Tool Dispatch

**Status:** 🚧 Draft

## Summary

The Coral TUI is a first-party client that routes agent tool calls through the
MCP protocol — a mechanism designed for third-party external integrations. This
RFD replaces MCP tool dispatch in the TUI with direct CLI subprocess invocations
using `--format json`, giving the agent the same vocabulary as a human operator,
producing reproducible and auditable session logs that SREs can understand and
replay.

## Problem

**Current behavior:**

The TUI agent connects to the colony via `coral colony mcp proxy`, which
translates JSON-RPC tool calls over stdio into gRPC `CallToolRequest` RPCs to
the colony MCP server. This means:

- The LLM context carries all 21 MCP tool schemas on every request, adding noise
  and consuming context budget.
- Agent actions are expressed as opaque tool calls (`coral_query_traces`,
  `coral_attach_uprobe`) that are invisible to operators reviewing a session.
- Every new colony capability requires a new MCP tool registration to be
  available in the TUI, creating maintenance overhead.
- The MCP proxy adds an extra network hop that is pure overhead for a first-party
  client that already has direct colony access.

**Why this matters:**

When an AI agent debugs a production incident, its actions should be legible to
the team — not just the outcome. A session log full of `coral_query_traces({...})`
tool calls is opaque. A session log showing `coral query traces --service api
--since 10m --format json` is a reproducible runbook. SREs reviewing the session
can re-run the exact commands, verify the agent's findings, and paste them
directly into postmortems or incident reports.

**Use cases affected:**

- Incident retrospectives: agent sessions cannot currently be used as runbook
  drafts.
- Team onboarding: watching the TUI agent work does not teach the CLI.
- Trust building: SREs cannot verify agent reasoning by re-running its steps.
- Tooling growth: every new observability capability requires a parallel MCP tool
  definition.

## Solution

**Key Design Decisions:**

- **TUI is first-party — bypass MCP.** The TUI runs inside the same binary and
  has direct CLI access. It does not need the MCP abstraction layer, which exists
  for third-party clients.
- **CLI is the contract.** The same commands a human operator runs are exactly
  what the agent runs. One vocabulary, two users.
- **`coral_cli` as a meta-tool.** A single tool replaces 21 MCP tools. The LLM
  receives a compact CLI reference resource and composes commands from it, the
  same way a human would read `coral --help`.
- **Two-track architecture.** External clients (Claude Desktop, Cursor) continue
  to use the MCP protocol unchanged. This RFD only changes the TUI's first-party
  path.
- **Latency is not a real concern.** Subprocess fork for a compiled Go binary
  adds ~10–50ms. LLM inference and colony network round-trips dominate agent loop
  latency. The round-trip count (number of LLM steps) matters far more than
  per-call overhead.

**Benefits:**

- Agent actions are auditable: every tool call becomes a reproducible CLI
  command in the session log.
- LLM context shrinks from 21 tool schemas to 3, improving reasoning quality and
  reducing token cost.
- New CLI commands are immediately available to the agent without any MCP
  registration.
- Debugging sessions become hand-offable runbooks — paste the command log into a
  postmortem and it works.
- Junior SREs watching the TUI learn the CLI as a side effect.

**Architecture Overview:**

```
Current (TUI via MCP):
  TUI → Agent → MCP Client (stdio) → coral colony mcp proxy → gRPC → Colony → MCP Server → tool handler

Proposed (TUI via CLI):
  TUI → Agent → coral_cli tool → subprocess: coral <cmd> --format json → gRPC → Colony

External clients (unchanged):
  Claude Desktop → MCP stdio → coral colony mcp proxy → gRPC → Colony → MCP Server → tool handler
```

### Component Changes

1. **Agent (`internal/cli/ask/agent.go`)**:

   - Add a `DispatchMode` to the agent configuration: `mcp` (default for
     external use) or `cli` (used by the TUI).
   - In `cli` mode, skip `connectToColonyMCP()` and register the local tool set
     (`coral_cli`, `coral_run`, `coral_exec`) instead.
   - Log each `coral_cli` invocation as a formatted command string in the
     session event stream so the TUI can surface it inline.

2. **`coral_cli` tool (new, `internal/cli/ask/`)**:

   - Accepts a `command` string array (e.g., `["query", "traces", "--service",
     "api", "--since", "10m"]`).
   - Automatically appends `--format json` unless already present.
   - Executes `coral <args>` as a subprocess, captures stdout as the tool
     result, relays stderr to the terminal for live progress.
   - Returns structured JSON to the LLM.

3. **CLI reference resource (`internal/cli/ask/`)**:

   - A compact plain-text index of coral CLI commands, their flags, and example
     outputs — analogous to `coral://sdk/reference` for the TypeScript SDK.
   - Served as a local resource (`coral://cli/reference`) that the agent reads
     on demand before composing commands.
   - Generated from the Cobra command tree so it stays in sync automatically.

4. **CLI JSON output completeness (`internal/cli/`)**:

   - Audit all commands the agent is likely to call and ensure they support
     `--format json` via `helpers.AddFormatFlag`.
   - Priority: `coral query *`, `coral debug *`, `coral list *`, `coral
     correlation *`.

5. **TUI session log (`internal/cli/terminal/`, `internal/cli/ask/`)**:

   - Emit `tool_start` events that include the full CLI command string, not just
     the tool name.
   - Display commands inline in the conversation (e.g., `$ coral query traces
     --service api --since 10m`) so users see exactly what the agent ran.

## Implementation Plan

### Phase 1: `coral_cli` tool and CLI dispatch mode

- [ ] Define `DispatchMode` enum (`mcp`, `cli`) in agent config.
- [ ] Implement `coral_cli` tool in `internal/cli/ask/tools_cli.go`: accept
      args, append `--format json`, exec subprocess, return stdout as JSON.
- [ ] Update `NewAgent()` in `ask/agent.go` to select tool set based on
      `DispatchMode`: CLI mode registers `coral_cli`, `coral_run`, `coral_exec`;
      MCP mode uses existing `connectToColonyMCP()` path.
- [ ] Wire TUI (`internal/cli/terminal/`) to use `DispatchMode: cli` by default.
- [ ] Unit tests for `coral_cli` tool: valid command, unknown command, non-zero
      exit, `--format json` already present.

### Phase 2: CLI reference resource

- [ ] Implement `coral://cli/reference` resource: walk the Cobra command tree and
      emit a compact plain-text reference (command, synopsis, key flags, example
      JSON output shape).
- [ ] Register the resource in the agent's local resource server (CLI mode only).
- [ ] Update agent system prompt to instruct the LLM to read
      `coral://cli/reference` before composing `coral_cli` calls, mirroring the
      `coral://sdk/reference` pattern.
- [ ] Unit test: resource lists all expected top-level command groups.

### Phase 3: JSON output completeness

- [ ] Audit `coral query summary/traces/metrics/logs` — confirm `--format json`
      output is machine-readable and stable.
- [ ] Audit `coral debug attach/detach/results` — add `--format json` where
      missing.
- [ ] Audit `coral list services`, `coral correlation list/deploy/remove` — add
      `--format json` where missing.
- [ ] Emit CLI command string in `tool_start` / `tool_complete` agent events.
- [ ] Display command string inline in TUI conversation view.

### Phase 4: Testing and documentation

- [ ] Integration test: TUI agent in CLI dispatch mode completes a multi-step
      query using only `coral_cli` calls.
- [ ] E2E test: verify session log contains parseable CLI commands that produce
      the same output when run manually.
- [ ] Update `docs/CLI.md` and `docs/CLI_REFERENCE.md` with `--format json` flag
      coverage.
- [ ] Update `docs/AGENT.md` with CLI dispatch mode, `coral_cli` tool contract,
      and `coral://cli/reference` resource.

## API Changes

### New tool: `coral_cli`

Registered in the agent's local tool set when operating in CLI dispatch mode.
Not exposed via MCP.

```
Tool name: coral_cli
Description: Run a coral CLI command and return its JSON output.

Input schema:
{
  "args": {
    "type": "array",
    "items": { "type": "string" },
    "description": "coral subcommand and flags, e.g. [\"query\", \"traces\", \"--service\", \"api\", \"--since\", \"10m\"]"
  }
}

Output: stdout of `coral <args> --format json`
```

### CLI commands (no new commands, extended coverage)

The following commands gain or confirm `--format json` support:

```bash
# Service observability
coral query summary --service <name> --format json
coral query traces  --service <name> --since <duration> --format json
coral query metrics --service <name> --since <duration> --format json
coral query logs    --service <name> --since <duration> --level <level> --format json

# Debug sessions
coral debug attach  --service <name> --function <sig> --duration <d> --format json
coral debug detach  --session <id> --format json
coral debug results --session <id> --format json

# Discovery & correlation
coral list services                          --format json
coral correlation list                       --format json
coral correlation deploy --rule <file>       --format json
coral correlation remove --id <id>           --format json
```

### New MCP resource: `coral://cli/reference`

Available in CLI dispatch mode only (not served over the colony MCP server).

```
URI:    coral://cli/reference
MIME:   text/plain
Format: compact plain-text command index, auto-generated from Cobra tree
```

Example content:

```
coral query summary  --service STR [--since DUR] [--format json|table]
  → {service, health, p99_ms, error_rate, request_rate}

coral query traces   --service STR [--since DUR] [--trace-id STR] [--format json|table]
  → {traces: [{trace_id, duration_ms, spans, status}]}

coral debug attach   --service STR --function STR [--duration DUR] [--format json|table]
  → {session_id, attached_at, function, service}
...
```

### Configuration changes

- New agent config field: `dispatch_mode` (`"mcp"` | `"cli"`, default `"cli"`
  when invoked from the TUI, `"mcp"` otherwise).

```yaml
ask:
  dispatch_mode: cli   # or "mcp" for Claude Desktop / external clients
```

## Security Considerations

- `coral_cli` only executes the `coral` binary with controlled arguments. It does
  not provide arbitrary shell execution (that remains `coral_exec`).
- `--format json` is appended programmatically; user-supplied args cannot inject
  shell metacharacters since execution uses `exec.Command` with a string array
  (no shell interpolation).
- The subprocess inherits the CLI user's colony credentials, which is the same
  trust boundary as running `coral query` manually.

## Implementation Status

**Core Capability:** ⏳ Not Started

The TUI agent will gain a CLI dispatch mode that replaces MCP tool calls with
direct `coral <cmd> --format json` subprocess invocations. A single `coral_cli`
meta-tool replaces 21 MCP tools in the LLM's context. Agent actions are surfaced
as CLI commands in the session log.

## Future Work

**Composite / higher-level commands** (Future — unassigned RFD)

Common multi-step patterns observed in real sessions should be promoted to
composite CLI commands that run their sub-commands in parallel and return a
unified JSON response. Examples:

- `coral diagnose --service api` → runs `query summary`, `query traces`, `query
  logs` concurrently and returns a single JSON object.
- `coral triage --service api` → runs diagnosis + attaches a probe to the top
  offending function.

These commands reduce agent round-trip count (fewer LLM inference steps) while
remaining auditable. They should be derived from usage data gathered after this
RFD is implemented, not designed speculatively.

**MCP tool surface reduction for external clients** (Future — unassigned RFD)

Once the CLI dispatch path is proven for the TUI, the same principle can be
applied to external MCP clients by exposing `coral_cli` as an MCP tool and
retiring the per-operation tools. This is a separate change with different
trade-offs (external clients may not have the `coral` binary available).
