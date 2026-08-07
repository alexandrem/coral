# CLI to MCP Integration

This document describes how AI clients (Claude Desktop, Cursor, `coral terminal`)
interact with Coral via the `coral_cli` tool.

**See also:**

- [CLI_REFERENCE.md](./CLI_REFERENCE.md) - Full CLI command reference
- [MCP.md](./MCP.md) - MCP architecture and proxy setup

---

## How It Works

All AI clients use a single `coral_cli` tool. The agent or proxy executes
`coral <args> --format json` as a subprocess and returns the JSON output to
the LLM.

```
LLM → coral_cli(["query", "traces", "--service", "api", "--since", "10m"])
    → subprocess: coral query traces --service api --since 10m --format json
    → JSON output returned to LLM
```

`--format json` is appended automatically. Do not include it in `args`.

---

## Command Examples for AI

### Service discovery

```json
["service", "list"]
["query", "summary"]
["query", "summary", "--service", "api"]
```

### Observability queries

```json
["query", "summary",  "--service", "api", "--since", "5m"]
["query", "traces",   "--service", "api", "--since", "1h"]
["query", "metrics",  "--service", "api", "--protocol", "http"]
["query", "logs",     "--service", "api", "--level", "error", "--since", "30m"]
["query", "topology"]
["query", "topology", "--include-l4=false"]
```

The topology response includes a `layer` field per connection (`L7`, `L4`, or
`BOTH`) — use `--include-l4=false` to suppress raw TCP edges and show only
trace-derived dependencies.

### Live debugging

```json
["debug", "attach",  "api", "--function", "ProcessOrder", "--duration", "60s"]
["debug", "session", "list"]
["debug", "session", "list", "--service", "api"]
["debug", "results", "--session", "abc123"]
["debug", "session", "stop", "abc123"]
["debug", "filter",  "abc123", "--min-duration", "50ms"]
```

### Function discovery

```json
["debug", "search", "--service", "api", "checkout"]
["debug", "info",   "--service", "api", "--function", "ProcessPayment"]
["debug", "profile", "--service", "api", "--query", "checkout"]
```

---

## Two-Track Architecture

| Client          | Path                                      |
|-----------------|-------------------------------------------|
| `coral terminal`| CLI dispatch — direct subprocess fork     |
| `coral ask`     | CLI dispatch — direct subprocess fork     |
| Claude Desktop  | MCP proxy → subprocess fork               |
| Cursor          | MCP proxy → subprocess fork               |

Both paths produce identical `coral <cmd> --format json` subprocesses.
Session logs are reproducible and auditable regardless of client.

---

**For detailed documentation:**

- CLI: [CLI_REFERENCE.md](./CLI_REFERENCE.md), [CLI.md](./CLI.md)
- MCP proxy: [MCP.md](./MCP.md)
