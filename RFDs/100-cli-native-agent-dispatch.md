---
rfd: "100"
title: "CLI-Native Agent Tool Dispatch"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: []
database_migrations: []
areas: ["ask", "tui", "mcp", "cli", "proxy"]
---

# RFD 100 - CLI-Native Agent Tool Dispatch

**Status:** 🎉 Implemented

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
- **Two-track architecture, unified tool vocabulary.** External clients (Claude
  Desktop, Cursor) continue to use the MCP protocol. Phase 5 extends the same
  `coral_cli` meta-tool to the proxy layer, so all clients — TUI and external —
  converge on a single tool and the 21 per-operation tools are removed.
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

Phase 1-3 (TUI via CLI):
  TUI → Agent → coral_cli tool → subprocess: coral <cmd> --format json → gRPC → Colony

Phase 5 (external clients via CLI, proxy intercepts):
  Claude Desktop → MCP stdio → coral colony mcp proxy → [intercept coral_cli] → subprocess: coral <cmd> --format json → gRPC → Colony

Final state (both paths, one tool):
  TUI          → Agent       → coral_cli tool    → subprocess: coral <cmd> --format json → gRPC → Colony
  Claude Desktop → MCP stdio → proxy coral_cli   → subprocess: coral <cmd> --format json → gRPC → Colony
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

6. **MCP proxy (`internal/cli/colony/mcp.go`) — Phase 5**:

   - In `mcpProxy.handleListTools`, return the `coral_cli` tool schema instead
     of delegating to the colony `ListTools` RPC.
   - In `mcpProxy.handleCallTool`, intercept `coral_cli` calls: parse the `args`
     array, append `--format json`, exec `os.Executable()` as a subprocess,
     return stdout as the MCP tool result. All other tool names are rejected
     (the colony no longer exposes per-operation tools).
   - Colony MCP server (`internal/colony/mcp/`) and its `CallTool` / `ListTools`
     gRPC handlers are removed once the proxy no longer calls them.

## Implementation Plan

### Phase 1: `coral_cli` tool and CLI dispatch mode

- [x] Define `DispatchMode` enum (`mcp`, `cli`) in agent config.
- [x] Implement `coral_cli` tool in `internal/cli/ask/tools_cli.go`: accept
      args, append `--format json`, exec subprocess, return stdout as JSON.
- [x] Update `NewAgent()` in `ask/agent.go` to select tool set based on
      `DispatchMode`: CLI mode registers `coral_cli`; MCP mode uses existing
      `connectToColonyMCP()` path.
- [x] Wire TUI (`internal/cli/terminal/`) to use `DispatchMode: cli` by default.
- [x] Unit tests for `coral_cli` tool: valid command, unknown command, non-zero
      exit, `--format json` already present.

### Phase 2: CLI reference resource

- [x] Implement `coral://cli/reference` resource: walk the Cobra command tree and
      emit a compact plain-text reference (command, synopsis, key flags).
- [x] Register the resource in the agent's local resource server (CLI mode only):
      included directly in the CLI system prompt via `buildCLISystemPrompt`.
- [x] Update agent system prompt to instruct the LLM to read
      `coral://cli/reference` before composing `coral_cli` calls.
- [x] Unit test: resource lists all expected top-level command groups.

### Phase 3: JSON output completeness

- [x] Audit `coral query summary/traces/metrics/logs` — `summary` already
      supported; added `--format json` to `traces`, `metrics`, `logs`.
- [x] Audit `coral debug attach/detach/results` — already supports `--format json`.
- [x] Audit `coral service list`, `coral debug correlations list/remove` — already
      supports `--format json`.
- [x] Emit CLI command string in `tool_start` agent events (`Command` field in
      `AgentEvent` and `ui.AgentEvent`).
- [x] Display command string inline in TUI conversation view (`$ coral <cmd>`).

### Phase 4: `coral_cli` as MCP tool in the proxy — retire the 21 per-operation tools

The proxy (`internal/cli/colony/mcp.go`) runs the `coral` binary locally. It
can intercept `coral_cli` tool calls and handle them as subprocesses, exactly
as the TUI does, without adding any dependency on the colony MCP server.

- [x] **`--format json` parity audit** — added `--format json` to
      `coral debug filter`, `coral debug session events`, and
      `coral debug session stop`. Commands `session list/get/query`,
      `debug search/info/profile` already had format support.
- [x] **Implement `coral_cli` in the proxy** — `mcpProxy.handleCallTool`
      intercepts `coral_cli` and handles it locally: parses the `args` array,
      appends `--format json`, execs `os.Executable()` as the subprocess,
      returns stdout as the MCP tool result. All other tool names return an error.
- [x] **Implement `coral_cli` in `mcpProxy.handleListTools`** — returns the
      single `coral_cli` tool schema directly without delegating to the colony RPC.
- [x] **Remove the 21 per-operation MCP tool handlers** — deleted
      `internal/colony/mcp/` package entirely and `internal/colony/server/mcp_tools.go`.
      Colony `CallTool`/`ListTools` gRPC handlers now return "not supported".
- [x] **Update `docs/MCP.md`** — replaced architecture diagram and added
      `coral_cli` tool contract section; legacy per-operation tools noted as
      reference-only.

### Phase 5: Testing and documentation

- [x] Unit tests for `coral_cli` tool helpers (`appendFormatJSON`,
      `cliCommandString`, `buildCLITools`).
- [x] Unit tests for `GenerateCLIReference`: validates query/debug/service
      command groups are included and unrelated groups excluded.
- [x] Integration test: `TestCLIDispatchMode` and `TestCLIDispatchToolsContract`
      in `internal/cli/ask/cli_dispatch_integration_test.go` — verify agent
      creates in CLI mode without MCP connection and uses `coral_cli` tool.
- [ ] E2E test in `tests/e2e/distributed`: verify TUI agent session log contains
      parseable CLI commands that produce the same output when run manually.
- [x] Update `docs/CLI.md` with CLI dispatch mode explanation and configuration.
- [x] Update `docs/CLI_REFERENCE.md` with `--format json` flag coverage for
      debug commands and CLI dispatch mode note.
- [x] Update `docs/MCP.md` architecture section to reflect the two-track model
      (TUI via CLI dispatch, external clients via MCP proxy).

## API Changes

### New tool: `coral_cli`

Registered in the agent's local tool set when operating in CLI dispatch mode
(TUI). Also exposed via the MCP proxy (Phase 5), replacing the 21
per-operation MCP tools for external clients.

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

🎉 **Implemented**

**What was built:**

- ✅ `DispatchMode` constant (`"mcp"` / `"cli"`) in `config.AskAgentConfig`
- ✅ `coral_cli` meta-tool in `internal/cli/ask/tools_cli.go`: accepts args array,
  appends `--format json` automatically, executes `coral <args>` as a subprocess,
  returns stdout as JSON to the LLM.
- ✅ `NewAgentWithCLIReference` in `ask/agent.go`: skips MCP connection in CLI
  mode and registers only `coral_cli`; system prompt includes `coral://cli/reference`.
- ✅ `GenerateCLIReference(*cobra.Command)` in `internal/cli/ask/cli_reference.go`:
  walks the Cobra tree and emits compact per-command flag reference.
- ✅ `coral terminal` wired to CLI dispatch mode: generates CLI reference from
  `cmd.Root()` and creates agent via `NewAgentWithCLIReference`.
- ✅ `AgentEvent.Command` field: carries the full `coral <args>` string for
  `tool_start` events; TUI displays it as `$ coral <cmd>`.
- ✅ `--format json` added to `coral query traces`, `coral query metrics`,
  `coral query logs`, `coral debug filter`, `coral debug session events`,
  `coral debug session stop` (already present on `summary`, `attach`, `detach`,
  `results`, `session list/get/query`, `search`, `info`, `profile`,
  `service list`, `correlations`).
- ✅ `coral_cli` in MCP proxy (`internal/cli/colony/mcp.go`): proxy intercepts
  `coral_cli` tool calls and handles them locally as subprocesses.
- ✅ Colony MCP server per-operation tools removed: `internal/colony/mcp/` and
  `internal/colony/server/mcp_tools.go` deleted; `CallTool`/`ListTools` handlers
  return "not supported".
- ✅ Integration tests: `TestCLIDispatchMode` and `TestCLIDispatchToolsContract`
  verify CLI dispatch creates correctly without MCP and registers `coral_cli`.
- ✅ `docs/MCP.md`, `docs/CLI.md`, `docs/CLI_REFERENCE.md` updated.

## Future Work

**E2E test for CLI dispatch session log** (Future — requires running colony)

Verify end-to-end that a TUI agent session log contains parseable CLI commands
that produce the same output when run manually. Deferred because this requires
a live `tests/e2e/distributed` colony environment and should be added alongside
the broader E2E suite expansion.

**Composite / higher-level commands** (Future — unassigned RFD)

If usage data shows that agents repeatedly follow the same multi-step pattern
across sessions, that pattern is a candidate for a composite CLI command —
reducing round-trip count while keeping the action auditable. The right
candidates should be derived from real session logs, not designed speculatively.

One genuine gap: `coral query summary` tells the agent *what* is wrong but not
*where in the code*. A `coral triage` command that crosses the
observability-to-debugging boundary (identify degraded service → find slowest
function → attach probe) could reduce a 3-step loop to 1, with no duplication
of existing commands.
