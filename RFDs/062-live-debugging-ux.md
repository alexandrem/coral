---
rfd: "062"
title: "Live Debugging UX & AI"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "059", "060", "061", "063" ]
database_migrations: [ ]
areas: [ "cli", "ai", "ux" ]
---

# RFD 062 - Live Debugging UX & AI

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

## Detailed CLI Examples

### coral debug attach

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

### coral debug trace

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

### coral debug list

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

### coral debug detach

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

### coral debug query

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

## Function Discovery Strategy for AI

**See [RFD 063: Intelligent Function Discovery](063-intelligent-function-discovery.md) for complete details.**

### Overview

Applications have **10,000-50,000+ functions**. We use a **multi-tier discovery strategy** to narrow down from 50,000 functions to the relevant 5-10:

1. **Tier 1: Metrics-Driven Pre-Filtering** - Colony auto-injects performance anomalies
2. **Tier 2: Semantic Search** - `coral_search_functions` finds relevant functions by keywords
3. **Tier 3: Call Graph Navigation** - `coral_get_function_context` navigates from entry points to bottlenecks
4. **Tier 4: Pattern Fallback** - Regex matching when semantic search fails

**Example workflow:**

```
User: "Why is checkout slow?"

→ Step 1: Colony auto-injects: "POST /api/checkout: P95 245ms (+206%)"
→ Step 2: LLM calls coral_search_functions(query="checkout")
          Returns: handleCheckout, processCheckout, ...
→ Step 3: LLM calls coral_get_function_context(function="handleCheckout")
          Returns: Calls processPayment (94% of time) ← bottleneck
→ Step 4: LLM attaches uprobe to processPayment
```

**Result**: LLM discovers the bottleneck in 2-3 tool calls, using <5,000 tokens.

For full implementation details, see [RFD 063](063-intelligent-function-discovery.md).

## MCP Tools (AI Integration)

Complete MCP tool definitions for AI-driven debugging.

### Tool: coral_attach_uprobe

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

**Example response:**

```json
{
    "session_id": "dbg-01H...",
    "status": "active",
    "function": "main.handleCheckout",
    "offset": "0x4a20",
    "target_agents": ["api-001", "api-002"],
    "expires_at": "2025-11-17T15:32:00Z",
    "streaming": true
}
```

### Tool: coral_trace_request_path

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

**Example response:**

```json
{
    "session_id": "trace-02M...",
    "path": "/api/checkout",
    "call_tree": [
        {
            "function": "handleCheckout",
            "duration_p95": "245ms",
            "calls": 127,
            "children": [
                {
                    "function": "validateCart",
                    "duration_p95": "12ms",
                    "calls": 127
                },
                {
                    "function": "processPayment",
                    "duration_p95": "230ms",
                    "calls": 127,
                    "children": [
                        {
                            "function": "validateCard",
                            "duration_p95": "225ms",
                            "calls": 127
                        }
                    ]
                }
            ]
        }
    ],
    "bottleneck": "validateCard (98% of total time)"
}
```

### Tool: coral_list_debug_sessions

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

### Tool: coral_detach_uprobe

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

### Tool: coral_get_debug_results

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

**Example response:**

```json
{
    "session_id": "dbg-01H...",
    "function": "handleCheckout",
    "duration": "60s",
    "statistics": {
        "total_calls": 342,
        "duration_p50": "12.4ms",
        "duration_p95": "45.2ms",
        "duration_p99": "89.1ms",
        "duration_max": "234.5ms"
    },
    "slow_outliers": [
        {
            "duration": "234.5ms",
            "timestamp": "2025-11-17T15:30:42Z",
            "labels": {
                "user_id": "u_12345"
            }
        }
    ]
}
```

### Tool: coral_search_functions (New - For Discovery)

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

**Example response:**

```json
{
    "service": "api",
    "query": "checkout payment",
    "results": [
        {
            "name": "main.handleCheckout",
            "file": "handlers/checkout.go",
            "line": 42,
            "offset": "0x4a20",
            "score": 0.95,
            "reason": "Exact match in function name"
        },
        {
            "name": "main.processPayment",
            "file": "handlers/payment.go",
            "line": 78,
            "offset": "0x4c80",
            "score": 0.87,
            "reason": "Match: 'payment'"
        }
    ]
}
```

### Tool: coral_get_function_context (New - For Navigation)

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

**Example response:**

```json
{
    "function": "main.processPayment",
    "file": "handlers/payment.go:78",
    "offset": "0x4c80",
    "performance": {
        "p50_latency": "198ms",
        "p95_latency": "230ms",
        "p99_latency": "312ms",
        "calls_per_minute": 340,
        "error_rate": 0.002
    },
    "called_by": [
        {
            "function": "main.handleCheckout",
            "file": "handlers/checkout.go:42",
            "call_frequency": "always"
        }
    ],
    "calls": [
        {
            "function": "main.validateCard",
            "file": "payment/validator.go:156",
            "estimated_contribution": "98%",
            "avg_duration": "225ms"
        },
        {
            "function": "main.recordTransaction",
            "file": "payment/transaction.go:89",
            "estimated_contribution": "2%",
            "avg_duration": "5ms"
        }
    ],
    "recommendation": "Focus on main.validateCard (98% of time)"
}
```

### Tool: coral_list_probeable_functions (Fallback)

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

**Example response:**

```json
{
    "service": "api",
    "functions": [
        {
            "name": "main.handleCheckout",
            "file": "handlers/checkout.go",
            "line": 42,
            "offset": "0x4a20"
        },
        {
            "name": "main.processPayment",
            "file": "handlers/payment.go",
            "line": 78,
            "offset": "0x4c80"
        },
        {
            "name": "main.validateCard",
            "file": "payment/validator.go",
            "line": 156,
            "offset": "0x5100"
        }
    ],
    "sdk_version": "v1.0.0",
    "debug_symbols": true
}
```

## AI Workflow Examples

### Example 1: Performance Analysis

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

**AI Response:**

```
Analysis Complete:

Root Cause: External card validation API responding slowly (220ms vs 50ms baseline).

Evidence:
- handleCheckout P95: 245ms (baseline: 80ms) ⚠️ +206%
- processPayment P95: 230ms (94% of handleCheckout time)
- validateCard P95: 225ms (98% of processPayment time)
- External API call: 220ms (bottleneck)

Recommendations:
1. Check external API health (card validation service)
2. Add timeout (current: none detected)
3. Implement circuit breaker
4. Cache repeat validations (30% potential cache hit rate)

Debug sessions: dbg-01H..., dbg-02K...
```

### Example 2: Database Query Debugging

**User**: "The payment API is slow"

**AI**:

1. Calls `coral_attach_uprobe(service="payment", function="ProcessPayment")`.
2. Detects slow database query function.
3. Calls `coral_attach_uprobe(service="payment", function="QueryTransactions")`.
4. Analyzes query execution time: 2.0s avg.
5. Detects sequential scan (missing index).

**AI Response:**

```
Database Performance Issue Detected:

Function: db.QueryTransactions()
Average Duration: 2.0s (expected: ~5ms)
Root Cause: Missing database index

Query Pattern:
  SELECT * FROM transactions WHERE user_id = ?

Current: Sequential scan (234,891 rows)
Needed: Index on transactions.user_id

Immediate Fix:
  CREATE INDEX idx_transactions_user_id ON transactions(user_id);

Expected Improvement: 2.3s → 150ms (15x faster)

Evidence: debug-sessions/dbg-04M.../
```

## Implementation Plan

### Phase 1: CLI Commands

- [ ] Implement `coral debug attach` command
- [ ] Implement `coral debug trace` command (request path tracing)
- [ ] Implement `coral debug list` command
- [ ] Implement `coral debug detach` command
- [ ] Implement `coral debug query` command (historical data)

### Phase 2: CLI Output Formatting

- [ ] Live event streaming UI
- [ ] Progress bars and status indicators
- [ ] Table formatting for session lists
- [ ] Call tree visualization (ASCII art)
- [ ] Export to JSON/CSV formats

### Phase 3: MCP Tool Integration

- [ ] Register MCP tools with Colony server
- [ ] Implement `coral_search_functions` tool (semantic search)
- [ ] Implement `coral_get_function_context` tool (call graph navigation)
- [ ] Implement `coral_attach_uprobe` tool
- [ ] Implement `coral_trace_request_path` tool
- [ ] Implement `coral_list_debug_sessions` tool
- [ ] Implement `coral_detach_uprobe` tool
- [ ] Implement `coral_get_debug_results` tool
- [ ] Implement `coral_list_probeable_functions` tool (regex fallback)

### Phase 4: AI Workflow Patterns

- [ ] Auto-context injection (metrics anomalies, service metadata)
- [ ] Pattern matching for debug-related queries
- [ ] Semantic function search implementation (keyword-based V1)
- [ ] Call graph analysis and contribution estimation
- [ ] Auto-attach probes based on bottleneck identification
- [ ] Analysis of debug session results
- [ ] Recommendation generation engine
- [ ] Evidence packaging (traces, histograms, reports)

### Phase 5: Visualization & Export

- [ ] Generate flamegraphs from trace data
- [ ] Generate duration histograms
- [ ] Export call trees as SVG
- [ ] Create Markdown reports
- [ ] Integration with Grafana (optional)

### Phase 6: Testing & Documentation

- [ ] E2E tests for all CLI commands
- [ ] MCP tool integration tests
- [ ] AI workflow validation tests
- [ ] User documentation
- [ ] Video demos and tutorials

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

## Configuration Changes

### Colony Configuration

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
