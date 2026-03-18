# MCP Integration - AI-Powered Observability

Coral exposes observability data to AI assistants through the **Model Context
Protocol (MCP)**, enabling tools like Claude Desktop and Cursor to query
distributed system health, metrics, traces, and telemetry.

## Overview

**The proxy handles everything locally** — it runs as a subprocess on the
operator's machine, executes `coral` CLI commands, and returns JSON to the LLM.
No colony MCP server is involved.

**No embedded LLM** — the proxy is a pure protocol translator. External LLMs
(Claude Desktop, `coral ask`, `coral terminal`) query the colony via `coral_cli`
and synthesize insights.

## Quick Start

### 1. Start a Colony

```bash
coral colony start
```

### 2. Configure Claude Desktop

Generate the configuration:

```bash
coral colony mcp generate-config
```

This outputs:

```json
{
    "mcpServers": {
        "coral": {
            "command": "coral",
            "args": [
                "colony",
                "mcp",
                "proxy"
            ]
        }
    }
}
```

Copy this to `~/.config/claude/claude_desktop_config.json` (macOS) or
`%APPDATA%/Claude/claude_desktop_config.json` (Windows).

### 3. Restart Claude Desktop

After restarting, Claude can now query your Coral colony.

### 4. Ask Questions

Try asking Claude:

- "Is production healthy?"
- "Show me HTTP error rates for the API service"
- "What's the P95 latency for the checkout service?"
- "Find slow database queries"

Claude will call `coral_cli` with the appropriate commands and synthesize the
results.

## Architecture

### Two-Track Design (RFD 100)

Coral supports two client paths that share a single tool vocabulary:

```
TUI (coral terminal)
  └─ Agent → coral_cli tool → subprocess: coral <cmd> --format json → gRPC → Colony

External clients (Claude Desktop, Cursor, custom)
  └─ MCP stdio → coral colony mcp proxy → coral_cli tool → subprocess: coral <cmd> --format json → gRPC → Colony
```

Both paths use the same `coral_cli` meta-tool. Every agent action is a
human-readable coral CLI command, making session logs reproducible and auditable.

### External Client Flow

```
┌──────────────────────────────────────────────────────────┐
│  Claude Desktop / Custom MCP Client                      │
│  (External LLM - Anthropic Claude, OpenAI, Ollama)       │
└─────────────────────┬────────────────────────────────────┘
                      │ MCP Protocol (stdio)
                      ▼
         ┌────────────────────────────┐
         │   Proxy Command            │
         │   coral colony mcp proxy   │
         │   • Exposes coral_cli tool │
         │   • Handles locally via    │
         │     subprocess             │
         └────────────┬───────────────┘
                      │ coral <cmd> --format json
                      ▼
         ┌────────────────────────────┐
         │   Colony gRPC              │
         │   • Real-time queries      │
         │   • eBPF debug sessions    │
         └────────────┬───────────────┘
                      │
                      ▼
         ┌────────────────────────────┐
         │   Colony DuckDB            │
         │   • Metrics summaries      │
         │   • Trace data             │
         │   • Agent registry         │
         │   • Events                 │
         └────────────────────────────┘
```

**Architecture Benefits:**

- Real-time data (no stale snapshots)
- One tool vocabulary for both TUI and external clients
- Proxy handles `coral_cli` locally — no colony MCP server required
- Every agent action is a reproducible CLI command in the session log
- New CLI commands are immediately available to the agent

**Key Point:** The LLM lives OUTSIDE the colony. Colony just provides data
access tools.

## Available MCP Tools

### `coral_cli`

The proxy exposes a single `coral_cli` tool. The LLM composes standard coral
CLI commands and the proxy executes them as subprocesses, returning JSON output.

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

`--format json` is appended automatically by the proxy. Do not include it
in `args`.

**Example args arrays:**

```json
["query", "summary", "--service", "api"]
["query", "traces", "--service", "api", "--since", "30m"]
["query", "metrics", "--service", "api", "--protocol", "http"]
["query", "logs", "--service", "api", "--level", "error"]
["debug", "attach", "api", "--function", "processPayment"]
["debug", "session", "list"]
["service", "list"]
```

See [CLI_REFERENCE.md](./CLI_REFERENCE.md) for the full command reference.

## CLI Commands

All MCP-related proxy commands are under `coral colony mcp`:

```bash
# Generate Claude Desktop config
coral colony mcp generate-config

# Generate config for multiple colonies
coral colony mcp generate-config --all-colonies

# Start MCP proxy (used by Claude Desktop)
coral colony mcp proxy
coral colony mcp proxy --colony my-shop-production
```

## Configuration

### Multiple Colonies

To expose multiple colonies to Claude Desktop:

```bash
coral colony mcp generate-config --all-colonies
```

Output:

```json
{
    "mcpServers": {
        "coral-production": {
            "command": "coral",
            "args": [
                "colony",
                "mcp",
                "proxy",
                "--colony",
                "my-app-production"
            ]
        },
        "coral-staging": {
            "command": "coral",
            "args": [
                "colony",
                "mcp",
                "proxy",
                "--colony",
                "my-app-staging"
            ]
        }
    }
}
```

Now Claude can query both environments and compare them.

## Example Use Cases

### Pre-Deployment Check

```
You: "Should I deploy PR #123 to production?"

Claude: [Calls coral_cli]
  → coral_cli(["query", "summary"])
  → coral_cli(["query", "metrics", "--service", "api", "--since", "1h"])

"Based on production state:
- All services healthy ✓
- API: 12.5k requests, 0.2% errors, 145ms avg latency (normal)
- No degraded services ✓

Safe to deploy. System is stable with normal traffic patterns."
```

### Incident Investigation

```
You: "Why is checkout slow?"

Claude: [Orchestrates coral_cli calls]
  → coral_cli(["query", "summary", "--service", "checkout", "--since", "1h"])
     Result: ⚠️ degraded - 850ms avg latency (baseline: 200ms)

  → coral_cli(["query", "traces", "--service", "checkout", "--min-duration-ms", "500"])
     Result: 80% of slow traces wait for payment-api

  → coral_cli(["query", "metrics", "--service", "payment-api", "--protocol", "sql"])
     Result: SQL queries: P95 650ms (baseline: 50ms)

"Checkout is slow because payment-api database queries are slow."
```

### Cross-Environment Comparison

```
You: "Why is prod slower than staging?"

Claude: [Queries both colonies]
  → coral-prod:    coral_cli(["query", "summary", "--service", "api"])
  → coral-staging: coral_cli(["query", "summary", "--service", "api"])

"Production vs Staging Comparison:
- Prod: 245ms avg (⚠️ degraded)
- Staging: 180ms avg (✅ healthy)"
```

### Live Debugging

```
You: "Debug the ProcessPayment function in the payment service"

Claude:
  → coral_cli(["debug", "attach", "payment-service", "--function", "ProcessPayment", "--duration", "60s"])
     Result: {"session_id": "abc-123", "expires_at": "..."}

  [Waits for events to collect...]

  → coral_cli(["debug", "results", "--session", "abc-123"])
     Result: 47 events, avg 125ms, P95 380ms
```

## Troubleshooting

### MCP server not showing in Claude Desktop

1. Check colony is running: `coral colony status`
2. Check Claude Desktop config path is correct
3. Restart Claude Desktop after config changes
4. Check Claude Desktop logs for errors

### Tools not working

1. Check if colony has data:
    - Are agents connected?
    - Is eBPF observability collecting metrics?
    - Are services instrumented with OTLP?

2. Verify time ranges:
    - If services just started, use shorter ranges: `--since 5m`

### Data seems stale or outdated

The proxy executes real-time CLI commands against the colony, so data should be
up-to-date. If you see stale data:

1. Check colony is actively receiving data:
   ```bash
   coral colony status
   ```

2. Verify agents are connected and sending telemetry.

3. Check Beyla is running and collecting eBPF metrics.

### Permission denied errors

Check that the colony is running with proper permissions.
