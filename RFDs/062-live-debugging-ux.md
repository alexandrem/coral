---
rfd: "062"
title: "Live Debugging UX & AI"
status: "Implemented"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "059", "060", "061", "063" ]
database_migrations: [ ]
areas: [ "cli", "ai", "ux" ]
---

# RFD 062 - Live Debugging UX & AI

**Status:** 🎉 Implemented

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

### Architecture Overview

The live debugging system integrates the CLI and MCP server with the Colony backend and Agent eBPF probes.

```
User (CLI) ──┐
             ▼
           Colony ───► Agent (eBPF Probes)
             ▲
AI (MCP) ────┘
```

### Component Changes

1. **CLI (`coral debug`)**:
    - New command group for managing debug sessions.
    - Supports attaching, detaching, listing, and querying sessions.

2. **Colony (MCP Server)**:
    - Exposes debugging tools to AI agents.
    - Translates high-level intent into RPC calls to Agents.

3. **Agent**:
    - Manages eBPF uprobes and traces.
    - Streams events back to Colony.

## Implementation Plan

### Phase 1: CLI Commands

- [x] Implement `coral debug attach` command
- [x] Implement `coral debug trace` command (request path tracing)
- [x] Implement `coral debug list` command
- [x] Implement `coral debug detach` command
- [x] Implement `coral debug query` command (historical data)

### Phase 2: CLI Output Formatting

- [x] Progress bars and status indicators
- [x] Table formatting for session lists
- [x] Call tree visualization (ASCII art)
- [x] Export to JSON/CSV formats

### Phase 3: MCP Tool Integration

- [x] Register MCP tools with Colony server
- [x] Implement `coral_search_functions` tool (semantic search)
- [x] Implement `coral_get_function_context` tool (call graph navigation)
- [x] Implement `coral_attach_uprobe` tool
- [x] Implement `coral_trace_request_path` tool
- [x] Implement `coral_list_debug_sessions` tool
- [x] Implement `coral_detach_uprobe` tool
- [x] Implement `coral_get_debug_results` tool
- [x] Implement `coral_list_probeable_functions` tool (regex fallback)

### Phase 6: Testing & Documentation

- [ ] E2E tests for all CLI commands
- [ ] MCP tool integration tests
- [x] User documentation

## API Changes

### CLI Commands

#### coral debug attach

Attach uprobe to specific function for live debugging.

```bash
$ coral debug attach api --function handleCheckout --duration 60s

🔍 Debug session started (id: dbg-01H...)
📊 Function: main.handleCheckout
⏱️  Duration: 60 seconds
🎯 Target: api-001, api-002 (2 agents)

Collecting events... (Ctrl+C to stop early)

[Live tail of events...]

Function: handleCheckout
  Calls:        342
  P50 duration: 12.4ms
  P95 duration: 45.2ms
  P99 duration: 89.1ms
  Max duration: 234.5ms

Top slow calls:
  1. 234.5ms - user_id=u_12345 (api-001)
  2. 198.3ms - user_id=u_67890 (api-002)
  3. 156.7ms - user_id=u_54321 (api-001)

✓ Session completed. Full data saved to: ./debug-sessions/dbg-01H.../
```

**Options:**

* `--duration <duration>`: Session duration (default: 60s, max: 600s)
* `--sample-rate <N>`: Sample every Nth call (default: 1 = all calls)
* `--format <format>`: Output format (text, json, csv)

#### coral debug trace

Trace entire request path (auto-discovery).

```bash
$ coral debug trace api --path "/api/checkout" --duration 5m

🔍 Tracing /api/checkout for 5 minutes...
📊 Auto-discovering functions in request path...

Discovered call chain:
  handleCheckout (entry)
    → validateCart (12.3ms)
      → checkInventory (8.1ms)
    → processPayment (142.5ms) ← SLOW
      → validateCard (135.2ms) ← SLOW
        → callExternalAPI (130.1ms) ← SLOW
      → recordTransaction (5.8ms)
    → sendConfirmation (23.4ms)

Analysis:
  Total: 178.2ms
  Slowest: callExternalAPI (130.1ms, 73% of total)
  Recommendation: External API is bottleneck

✓ Session completed. Call tree saved to: ./debug-traces/trace-01K.../
```

#### coral debug list

List active and recent debug sessions.

```bash
$ coral debug list

Active Debug Sessions:
┌───────────┬──────────┬─────────────────┬──────────┬───────────┬────────────┐
│ SESSION   │ SERVICE  │ FUNCTION        │ AGENT    │ STARTED   │ EXPIRES    │
├───────────┼──────────┼─────────────────┼──────────┼───────────┼────────────┤
│ dbg-01H.. │ api      │ handleCheckout  │ api-001  │ 2m ago    │ in 58m     │
│ dbg-02K.. │ worker   │ processJob      │ work-001 │ 15s ago   │ in 4m 45s  │
└───────────┴──────────┴─────────────────┴──────────┴───────────┴────────────┘

Recent Completed Sessions:
┌───────────┬──────────┬─────────────────┬───────────┬────────────┐
│ SESSION   │ SERVICE  │ FUNCTION        │ EVENTS    │ COMPLETED  │
├───────────┼──────────┼─────────────────┼───────────┼────────────┤
│ dbg-00A.. │ api      │ processPayment  │ 1,234     │ 10m ago    │
└───────────┴──────────┴─────────────────┴───────────┴────────────┘
```

#### coral debug detach

Stop debug session early.

```bash
$ coral debug detach api --session-id dbg-01H...

✓ Debug session dbg-01H... detached
  Total events collected: 342
  Data saved to: ./debug-sessions/dbg-01H.../

# Or detach all sessions for a service
$ coral debug detach api --all

✓ Detached 3 debug sessions for service: api
```

#### coral debug query

Query historical debug data.

```bash
$ coral debug query api --function handleCheckout --since 1h

Debug Sessions for handleCheckout (last 1 hour):

Session: dbg-01H... (60s, 10m ago)
  Calls:        342
  P50 duration: 12.4ms
  P95 duration: 45.2ms
  Max duration: 234.5ms

Session: dbg-00A... (120s, 45m ago)
  Calls:        687
  P50 duration: 11.8ms
  P95 duration: 43.1ms
  Max duration: 198.7ms

Trend: Performance stable ✓
```

### MCP Tools

We expose tools to the AI agent (Claude, etc.) via the Model Context Protocol (RFD 004).

#### Tool: coral_attach_uprobe

```json
{
    "name": "coral_attach_uprobe",
    "description": "Attach eBPF uprobe to application function for live debugging. Captures entry/exit events, measures duration. Time-limited and production-safe.",
    "inputSchema": {
        "type": "object",
        "properties": {
            "service": {
                "type": "string",
                "description": "Service name (e.g., 'api', 'payment-service')"
            },
            "function": {
                "type": "string",
                "description": "Function name to probe (e.g., 'handleCheckout', 'main.processPayment')"
            },
            "duration": {
                "type": "string",
                "description": "Collection duration (e.g., '30s', '5m'). Default: 60s, max: 600s",
                "default": "60s"
            },
            "sample_rate": {
                "type": "integer",
                "description": "Sample every Nth call (1 = all calls). Default: 1",
                "default": 1
            }
        },
        "required": ["service", "function"]
    }
}
```

#### Tool: coral_trace_request_path

```json
{
    "name": "coral_trace_request_path",
    "description": "Trace all functions called during HTTP request execution. Auto-discovers call chain and builds execution tree.",
    "inputSchema": {
        "type": "object",
        "properties": {
            "service": {
                "type": "string",
                "description": "Service name"
            },
            "path": {
                "type": "string",
                "description": "HTTP path to trace (e.g., '/api/checkout')"
            },
            "duration": {
                "type": "string",
                "description": "Trace duration. Default: 60s, max: 600s",
                "default": "60s"
            }
        },
        "required": ["service", "path"]
    }
}
```

#### Tool: coral_list_debug_sessions

```json
{
    "name": "coral_list_debug_sessions",
    "description": "List active and recent debug sessions across services.",
    "inputSchema": {
        "type": "object",
        "properties": {
            "service": {
                "type": "string",
                "description": "Filter by service name (optional)"
            },
            "status": {
                "type": "string",
                "enum": ["active", "expired", "all"],
                "description": "Filter by status. Default: active",
                "default": "active"
            }
        }
    }
}
```

#### Tool: coral_detach_uprobe

```json
{
    "name": "coral_detach_uprobe",
    "description": "Stop debug session early and detach eBPF probes. Returns collected data summary.",
    "inputSchema": {
        "type": "object",
        "properties": {
            "session_id": {
                "type": "string",
                "description": "Debug session ID to detach"
            }
        },
        "required": ["session_id"]
    }
}
```

#### Tool: coral_get_debug_results

```json
{
    "name": "coral_get_debug_results",
    "description": "Get aggregated results from debug session: call counts, duration percentiles, slow outliers.",
    "inputSchema": {
        "type": "object",
        "properties": {
            "session_id": {
                "type": "string",
                "description": "Debug session ID"
            },
            "format": {
                "type": "string",
                "enum": ["summary", "full", "histogram"],
                "description": "Result format. Default: summary",
                "default": "summary"
            }
        },
        "required": ["session_id"]
    }
}
```

#### Tool: coral_search_functions (New - For Discovery)

```json
{
    "name": "coral_search_functions",
    "description": "Semantic search for functions by keywords. Searches function names, file paths, and comments. Returns ranked results. Prefer this over list_probeable_functions for discovery.",
    "inputSchema": {
        "type": "object",
        "properties": {
            "service": {
                "type": "string",
                "description": "Service name"
            },
            "query": {
                "type": "string",
                "description": "Natural language query (e.g., 'checkout payment processing', 'database query', 'authentication')"
            },
            "limit": {
                "type": "integer",
                "description": "Max results to return (default: 20, max: 50)",
                "default": 20
            }
        },
        "required": ["service", "query"]
    }
}
```

#### Tool: coral_get_function_context (New - For Navigation)

```json
{
    "name": "coral_get_function_context",
    "description": "Get context about a function: what calls it, what it calls, recent performance metrics. Use this to navigate the call graph after discovering an entry point.",
    "inputSchema": {
        "type": "object",
        "properties": {
            "service": {
                "type": "string",
                "description": "Service name"
            },
            "function": {
                "type": "string",
                "description": "Function name (e.g., 'main.handleCheckout')"
            },
            "include_callers": {
                "type": "boolean",
                "description": "Include functions that call this one",
                "default": true
            },
            "include_callees": {
                "type": "boolean",
                "description": "Include functions this one calls",
                "default": true
            },
            "include_metrics": {
                "type": "boolean",
                "description": "Include performance metrics if available",
                "default": true
            }
        },
        "required": ["service", "function"]
    }
}
```

#### Tool: coral_list_probeable_functions (Fallback)

```json
{
    "name": "coral_list_probeable_functions",
    "description": "List functions available for uprobe attachment using regex pattern. Use coral_search_functions instead for semantic search. This is a fallback for regex-based filtering.",
    "inputSchema": {
        "type": "object",
        "properties": {
            "service": {
                "type": "string",
                "description": "Service name"
            },
            "pattern": {
                "type": "string",
                "description": "Regex filter for function names (e.g., 'handle.*', 'process.*')"
            }
        },
        "required": ["service"]
    }
}
```

### Configuration Changes

```yaml
# colony-config.yaml
colony:
    debug:
        enabled: true

        # Session management
        sessions:
            default_duration: 60s
            max_duration: 600s
            auto_cleanup_after: 24h  # Clean up expired sessions

        # Storage retention
        storage:
            events_retention: 24h
            sessions_retention: 7d

        # AI integration
        ai:
            auto_attach_probes: true  # Let AI attach probes automatically
            max_probes_per_query: 5   # Limit AI to 5 functions per query
```

## Testing Strategy

### CLI Tests

* **Command Parsing**: Verify all flags and options work correctly.
* **Output Formatting**: Validate table formatting, progress bars, live updates.
* **Error Handling**: Test invalid service names, missing sessions, network
  failures.
* **Export Formats**: Verify JSON, CSV, SVG output correctness.

### MCP Tool Tests

* **Tool Registration**: Verify all tools registered with Colony MCP server.
* **Input Validation**: Test with valid and invalid parameters.
* **Response Format**: Verify JSON responses match schema.
* **Error Handling**: Test with unreachable services, expired sessions.

### AI Workflow Tests

* **Pattern Matching**: Verify AI recognizes debug-related queries.
* **Auto-Probe Selection**: Test AI selects correct functions to probe.
* **Analysis Accuracy**: Verify bottleneck identification is correct.
* **Recommendation Quality**: Test generated recommendations are actionable.

### E2E Tests

* **Full User Workflow**: User → CLI → Colony → Agent → SDK → Application.
* **AI-Driven Workflow**: AI query → Auto-probe → Analysis → Recommendations.
* **Multi-Service Tracing**: Trace across multiple services.
* **Error Recovery**: Test graceful handling of failures at each layer.

## Implementation Status

**Core Capability:** ✅ Complete

The CLI commands (`attach`, `detach`, `list`, `query`, `trace`) are fully implemented and integrated with the Colony backend. The MCP tools are registered and available for AI agents.

**Operational Components:**
- ✅ `coral debug` CLI commands
- ✅ MCP tools for debugging and discovery
- ✅ Agent eBPF integration (uprobes)
- ✅ Documentation updated

**What Works Now:**
- Users can attach probes to functions and see live events.
- AI agents can discover functions and attach probes autonomously.
- Historical data can be queried via CLI.

## Deferred Features

**Advanced AI Workflows** (Future - RFD 063 & Follow-up)
- Auto-attach probes based on bottleneck identification
- Analysis of debug session results
- Recommendation generation engine
- Evidence packaging (traces, histograms, reports)

**Visualization & Export** (Future)
- Generate flamegraphs from trace data
- Generate duration histograms
- Export call trees as SVG
- Create Markdown reports
- Integration with Grafana

**Note:** Semantic search, call graph analysis, and auto-context injection are covered in [RFD 063](063-intelligent-function-discovery.md).

## Appendix

### Function Discovery Strategy for AI

**See [RFD 063: Intelligent Function Discovery](063-intelligent-function-discovery.md) for complete details.**

Applications have **10,000-50,000+ functions**. We use a **multi-tier discovery strategy** to narrow down from 50,000 functions to the relevant 5-10:

1. **Tier 1: Metrics-Driven Pre-Filtering** - Colony auto-injects performance anomalies
2. **Tier 2: Semantic Search** - `coral_search_functions` finds relevant functions by keywords
3. **Tier 3: Call Graph Navigation** - `coral_get_function_context` navigates from entry points to bottlenecks
4. **Tier 4: Pattern Fallback** - Regex matching when semantic search fails

### AI Workflow Examples

#### Example 1: Performance Analysis

**User**: "Why is checkout slow on api service?"

**AI**:

1. Calls `coral_list_probeable_functions(service="api", pattern="checkout")`.
2. Finds `handleCheckout`.
3. Calls `coral_attach_uprobe(service="api", function="handleCheckout", duration="30s")`.
4. Waits 30s for data collection.
5. Calls `coral_get_debug_results(session_id="dbg-01H...")`.
6. Analyzes: P95 is 245ms (baseline: 80ms).
7. Identifies bottleneck: `processPayment` (94% of time).
8. Attaches new probe to `processPayment`.
9. Finds root cause: `validateCard` calling slow external API.

#### Example 2: Database Query Debugging

**User**: "The payment API is slow"

**AI**:

1. Calls `coral_attach_uprobe(service="payment", function="ProcessPayment")`.
2. Detects slow database query function.
3. Calls `coral_attach_uprobe(service="payment", function="QueryTransactions")`.
4. Analyzes query execution time: 2.0s avg.
5. Detects sequential scan (missing index).
