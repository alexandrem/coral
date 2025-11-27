---
rfd: "061"
title: "Live Debugging UX & AI"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "058" ]
database_migrations: [ ]
areas: [ "cli", "ai", "ux" ]
---

# RFD 061 - Live Debugging UX & AI

**Status:** 🚧 Draft

## Summary

This RFD defines the **User Experience** for live debugging, including the CLI
commands and the AI-driven workflows powered by the Model Context Protocol (
MCP).

## Problem

Raw eBPF data is complex. Users need a simple, intuitive interface to "ask
questions" about their running code. Furthermore, AI agents need structured
tools to autonomously investigate performance issues.

## Solution

We provide two primary interfaces:

1. **CLI**: `coral debug` for manual, granular control.
2. **AI**: `coral ask` and MCP tools for high-level intent ("Why is checkout
   slow?").

### CLI Commands

The `coral debug` command group manages sessions.

```bash
# Attach to a function
coral debug attach <service> --function <name> --duration <duration>

# Trace a request path (auto-discovery)
coral debug trace <service> --path <http-path> --duration <duration>

# List active sessions
coral debug list [service]

# Detach
coral debug detach <service> [--session-id <id> | --all]

# Query historical data
coral debug query <service> --function <name> --since <duration>
```

#### Example Output

```text
$ coral debug attach api --function handleCheckout --duration 60s

🔍 Debug session started (id: dbg-01H...)
📊 Function: main.handleCheckout
⏱️  Duration: 60 seconds
🎯 Target: api-001, api-002 (2 agents)

Collecting events...

Function: handleCheckout
  Calls:        342
  P50 duration: 12.4ms
  P95 duration: 45.2ms
  Max duration: 234.5ms
```

### AI Integration (MCP)

We expose tools to the AI agent (Claude, etc.) via the Model Context Protocol (
RFD 004).

#### Tools

* `coral_attach_uprobe`: Start a session.
* `coral_trace_request_path`: Trace a call chain.
* `coral_list_debug_sessions`: Check status.
* `coral_get_debug_results`: Get analysis data.
* `coral_list_probeable_functions`: Discover available functions.

#### AI Workflow Example

**User**: "Why is checkout slow?"

**AI**:

1. Calls `coral_list_probeable_functions(service="api", pattern="checkout")`.
2. Finds `handleCheckout`.
3. Calls `coral_attach_uprobe(service="api", function="handleCheckout")`.
4. Analyzes results: "P95 is 200ms".
5. Iterates: "Let's check `processPayment` called by `handleCheckout`."
6. Attaches new probe.
7. Concludes: "The bottleneck is in `validateCard`."

### Visualization

The CLI should support outputting data in formats suitable for external tools:

* `--format=json`: For scripts.
* `--format=svg`: Generate a flamegraph or histogram.
