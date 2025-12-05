---
rfd: "069"
title: "Function Discovery and Profiling Tools"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "063", "072", "067" ]
database_migrations: [ ]
areas: [ "mcp", "cli", "ai", "debugging", "tools" ]
---

# RFD 069 - Function Discovery and Profiling Tools

**Status:** 🚧 Draft

## Summary

Define user-facing tools for function discovery and profiling, building on the
function registry infrastructure (RFD 063) and debug CLI structure (RFD 072). For
LLMs, provide **two meta-tools** that replace 5+ low-level operations:
`coral_discover_functions` (unified search with embedded context) and
`coral_profile_functions` (batch instrumentation with automatic analysis). For
CLI, implement `coral debug search`, `coral debug info`, and `coral debug profile`
commands that integrate with the reorganized session management (RFD 072). This
reduces typical LLM debugging workflows from 7+ tool calls to 2-3.

## Problem

**Current limitations:**

- **Too many MCP tools**: Having separate tools for search, context lookup, and
  individual probe attachment wastes LLM context tokens and requires complex
  orchestration
- **Manual session management**: LLMs must track multiple uprobe session IDs,
  coordinate waits, and aggregate results across sessions
- **Cold start problem**: Discovery returns incomplete data (no metrics), forcing
  iterative manual instrumentation before bottlenecks can be identified
- **7+ tool calls per debugging session**: Search → get context → attach probe 1
  → attach probe 2 → wait → get results 1 → get results 2
- **Poor LLM efficiency**: Each tool call consumes API quota and introduces
  latency; reducing calls dramatically improves developer experience

**Why this matters:**

- **LLM context is precious**: System prompts grow with each tool definition;
  fewer tools = more context for actual debugging
- **Faster time to insight**: Reducing 7+ calls to 2-3 means faster bug
  identification
- **Better UX**: Automatic instrumentation and analysis reduces cognitive load
- **Follows RFD 067 pattern**: Unified, high-level interfaces for common
  workflows

## Solution

Implement **two meta-tools for MCP** and **dual-level CLI commands**:

### 1. MCP Tools (LLM Interface)

#### `coral_discover_functions` - Unified Discovery

**Purpose:** One-stop function discovery with embedded context.

**Features:**
- Semantic keyword search across function names and file paths
- Returns results with **embedded metrics** (single query to RFD
  063 registry)
- Data availability transparency: clearly indicates static vs dynamic data
- Suggests next actions when metrics are missing
- Supports regex patterns for advanced filtering

**Replaces:** `coral_search_functions`, `coral_list_probeable_functions`,
`coral_get_function_context`

#### `coral_profile_functions` - Meta-Instrumentation

**Purpose:** Intelligent batch profiling that handles everything automatically.

**Features:**
- Discovers relevant functions via semantic search
- Applies selection strategy (critical_path, all, entry_points, leaf_functions)
- Attaches probes to **multiple functions simultaneously** (max 50)
- Waits and collects data (synchronous by default, async optional)
- Analyzes bottlenecks automatically (contribution %, severity ranking)
- Persists results to function registry (RFD 063) for future queries
- Returns actionable recommendations

**Solves:** Cold start problem, manual session management, multi-step
orchestration

### 2. CLI Commands (Human Interface)

#### High-Level Commands (Simple Workflow)

```bash
# Search for functions
coral debug search --service api checkout

# Auto-profile multiple functions
coral debug profile --service api --query checkout \
  --strategy critical-path --duration 60s
```

#### Detailed Commands (Power User Control)

```bash
# Get function details with metrics
coral debug info --service api --function handleCheckout

# Attach single probe manually
coral debug attach --service api --function processPayment \
  --duration 60s --sample-rate 0.1

# Session management (grouped under "session" subcommand)
coral debug session list [--service api]
coral debug session get <session-id>         # Get session metadata (status, duration, etc.)
coral debug session events <session-id>      # Get captured events/data from session
coral debug session stop <session-id>
```

### Key Design Decisions

- **Meta-tools for LLMs only**: Reduces 5+ MCP tools to 2, saving ~2000 tokens in
  system prompt
- **Granular + meta for CLI**: Power users retain full control, casual users get
  simple commands
- **Synchronous by default**: `profile_functions` blocks until data collected (
  async mode available)
- **Automatic bottleneck analysis**: Don't just return raw data, identify
  critical path automatically
- **Strategy-based selection**: LLM specifies intent (critical_path vs all),
  tool figures out details
- **Follows RFD 067 pattern**: High-level unified tools over low-level plumbing

### Benefits

- **Dramatic workflow reduction**: 2-3 tool calls instead of 7+ for typical
  debugging
- **No session management**: LLM doesn't track IDs or coordinate multiple waits
- **Fewer MCP tools = more context**: ~2000 tokens saved in system prompt
- **Better results per call**: Each call returns comprehensive, actionable data
- **Maintains power user flexibility**: CLI retains granular operations

## Workflow Examples

### LLM Workflow (Meta-Tools)

```
User: "Why is checkout slow?"

Step 1: Discover
→ coral_discover_functions(service="api", query="checkout")
  Returns:
  {
    "results": [
      {
        "function": "main.handleCheckout",
        "file": "handlers/checkout.go:45",
        "metrics": null,  // Not instrumented yet
        "data_coverage": "0%",
        "suggestion": "No metrics available. Run coral_profile_functions."
      }
    ]
  }

Step 2: Profile (automatic multi-probe instrumentation)
→ coral_profile_functions(
    service="api",
    query="checkout",
    strategy="critical_path",
    duration=60)

  Internally:
  - Searches for "checkout" functions
  - Finds handleCheckout + its callees (8 functions)
  - Attaches probes to all 8 simultaneously
  - Waits 60s, collecting data
  - Analyzes bottlenecks
  - Persists to registry

  Returns:
  {
    "functions_probed": 8,
    "bottlenecks": [
      {
        "function": "processPayment",
        "p95_ms": 850,
        "contribution_pct": 94,
        "severity": "critical",
        "recommendation": "Primary bottleneck - 94% of handleCheckout's time"
      },
      {
        "function": "validateCard",
        "p95_ms": 510,
        "contribution_pct": 60,
        "severity": "major",
        "recommendation": "Bottleneck within processPayment"
      }
    ],
    "next_steps": [
      "coral_query_logs(service='api', search='payment timeout')",
      "coral_discover_functions(query='validateCard') for deeper analysis"
    ]
  }

Total: 2 tool calls
Time: ~60 seconds (1 wait cycle)
Result: Bottleneck identified with recommendations
```

### CLI Workflow (Power User)

```bash
# High-level workflow (quick and simple)
coral debug search -s api checkout
# → Shows 8 functions, most without metrics

coral debug profile -s api -q checkout --strategy critical-path -d 60s
# → Profiles all 8, shows bottlenecks automatically

# Detailed workflow (precise control)
coral debug search -s api '.*checkout.*'
# → Returns: handleCheckout, processCheckout

coral debug info -s api -f handleCheckout
# → Shows metrics (if available)

coral debug attach -s api -f processPayment -d 60s --sample-rate 0.1
# → Returns session_id: abc123

# [wait 60 seconds]

# Check session status
coral debug session get abc123
# → Session abc123: COMPLETED, duration: 60s, events: 245, started by: alice@example.com

# Get captured events/data
coral debug session events abc123
# → Shows detailed timing data: 245 events, P50: 234ms, P95: 850ms, P99: 1200ms

# List all active sessions
coral debug session list -s api
# → Shows all debug sessions for api service

# Stop session early (if still running)
coral debug session stop abc123
# → Stops profiling, session marked as stopped
```

## API Specification

### MCP Tool 1: `coral_discover_functions`

**Input:**
```json
{
  "service": "api",
  "query": "checkout",           // Semantic keywords or regex
  "max_results": 20,             // Default: 20, max: 50
  "include_metrics": true,       // Include perf data if available
  "prioritize_slow": false       // Rank by P95 latency
}
```

**Output:**
```json
{
  "service": "api",
  "query": "checkout",
  "data_coverage": "20%",        // % of results with metrics
  "results": [
    {
      "function": {
        "id": "fn_abc123",
        "name": "main.handleCheckout",
        "package": "handlers",
        "file": "handlers/checkout.go",
        "line": 45,
        "offset": "0x4f8a20"
      },
      "search": {
        "score": 1.0,
        "reason": "'checkout' in function name"
      },
      "metrics": {
        "source": "probe_history",  // "probe_history" | "estimated" | null
        "last_measured": "2025-12-05T10:30:00Z",
        "sample_size": 1245,
        "p50_ms": 234,
        "p95_ms": 900,
        "p99_ms": 1500,
        "calls_per_min": 120,
        "error_rate": 0.028
      },
      "instrumentation": {
        "is_probeable": true,
        "has_dwarf": true,
        "currently_probed": false,
        "last_probed": null
      },
      "suggestion": null  // Present when action recommended
    }
  ],
  "suggestion": "Low data coverage (20%). Consider: coral_profile_functions(query='checkout')"
}
```

### MCP Tool 2: `coral_profile_functions`

**Input:**
```json
{
  "service": "api",
  "query": "checkout",           // Semantic search query
  "strategy": "critical_path",   // "critical_path" | "all" | "entry_points" | "leaf_functions"
  "max_functions": 20,           // Default: 20, max: 50
  "duration_seconds": 60,        // Default: 60, max: 300
  "async": false,                // Return immediately vs wait
  "sample_rate": 1.0             // Event sampling: 0.1-1.0
}
```

**Selection Strategies:**
- `critical_path`: Entry points + their immediate callees (recommended for
  initial investigation)
- `all`: All matching functions up to max_functions
- `entry_points`: Only top-level handlers (HTTP handlers, RPC methods)
- `leaf_functions`: Only functions with no callees (often where work happens: DB
  queries, API calls)

**Output:**
```json
{
  "session_id": "profile_xyz789",
  "status": "completed",         // "completed" | "in_progress" | "partial_success" | "failed"
  "service": "api",
  "query": "checkout",
  "strategy": "critical_path",

  "summary": {
    "functions_selected": 8,
    "functions_probed": 8,
    "probes_failed": 0,
    "total_events_captured": 1960,
    "duration_seconds": 60
  },

  "results": [
    {
      "function": "main.handleCheckout",
      "probe_successful": true,
      "metrics": {
        "p50_ms": 234,
        "p95_ms": 900,
        "p99_ms": 1500,
        "calls": 245,
        "errors": 7,
        "error_rate": 0.029
      },
      "calls": [
        {
          "callee": "processPayment",
          "contribution_pct": 94,
          "p95_ms": 850,
          "is_bottleneck": true
        }
      ]
    }
  ],

  "bottlenecks": [
    {
      "function": "processPayment",
      "p95_ms": 850,
      "contribution_pct": 94,
      "severity": "critical",      // "critical" | "major" | "minor"
      "impact": "94% of handleCheckout's execution time",
      "recommendation": "Primary bottleneck. Investigate validateCard and chargeAPI within processPayment."
    },
    {
      "function": "validateCard",
      "p95_ms": 510,
      "contribution_pct": 60,
      "severity": "major",
      "impact": "60% of processPayment's execution time",
      "recommendation": "Second-level bottleneck. Check external validation service latency."
    }
  ],

  "recommendation": "processPayment is the critical bottleneck (850ms, 94% contribution). Focus optimization efforts here.",

  "next_steps": [
    "coral_query_logs(service='api', search='payment', level='error')",
    "coral_discover_functions(query='validateCard') to explore bottleneck further",
    "Check external payment gateway latency metrics"
  ]
}
```

## CLI Command Specification

All commands are under the `coral debug` namespace:

### Discovery Commands

```bash
# Search for functions (lightweight, returns list)
coral debug search [OPTIONS] <query>
  -s, --service <name>       Service name (required)
  -n, --max-results <num>    Max results (default: 20)
  --prioritize-slow          Rank by P95 latency
  --format <table|json|csv>  Output format

# Get detailed info about specific function (metrics, source)
coral debug info [OPTIONS]
  -s, --service <name>       Service name (required)
  -f, --function <name>      Function name (required)
  --no-metrics               Skip performance data
  --format <table|json>      Output format
```

### Instrumentation Commands

```bash
# Attach uprobe to single function (manual profiling)
coral debug attach [OPTIONS]
  -s, --service <name>       Service name (required)
  -f, --function <name>      Function name (required)
  -d, --duration <seconds>   Duration (default: 60, max: 300)
  --sample-rate <float>      Event sampling rate (default: 1.0)
  --async                    Return immediately, don't wait

# Auto-profile multiple functions (meta-instrumentation)
coral debug profile [OPTIONS]
  -s, --service <name>       Service name (required)
  -q, --query <text>         Semantic search query (required)
  --strategy <type>          Selection strategy (default: critical-path)
                             Options: critical-path, all, entry-points, leaf-functions
  -n, --max-functions <num>  Max functions to probe (default: 20, max: 50)
  -d, --duration <seconds>   Duration (default: 60, max: 300)
  --async                    Return immediately, don't wait
  --sample-rate <float>      Event sampling rate (default: 1.0)
  --format <table|json>      Output format
```

### Session Management Commands

All session operations are scoped under `coral debug session`:

```bash
# List all debug sessions
coral debug session list [OPTIONS]
  -s, --service <name>       Filter by service (optional)
  --active-only              Show only active sessions
  --format <table|json>      Output format

# Get session metadata (status, start time, duration, who started it)
coral debug session get <session-id> [OPTIONS]
  --format <table|json>      Output format

# Get captured events/data from session
coral debug session events <session-id> [OPTIONS]
  --format <table|json>      Output format
  --limit <num>              Max events to return (default: 100)
  --follow                   Stream events in real-time (for active sessions)

# Stop session early
coral debug session stop <session-id>
```

## Implementation Plan

### Phase 1: Foundation (RFD 063 dependency)
- ✅ Function registry with search capability
- ✅ Function metrics storage
- ✅ Query API

### Phase 2: Discovery Tool
- [ ] Implement `QueryFunctions` RPC (uses RFD 063 registry)
- [ ] Add MCP tool: `coral_discover_functions`
- [ ] Add CLI command: `coral discover functions`
- [ ] Add granular CLI: `coral search functions`, `coral get function-context`
- [ ] Data availability transparency (static vs dynamic labeling)

### Phase 3: Profiling Tool
- [ ] Implement batch probe orchestration logic
- [ ] Implement selection strategies (critical_path, all, entry_points, leaves)
- [ ] Implement bottleneck analysis algorithm
- [ ] Add MCP tool: `coral_profile_functions`
- [ ] Add CLI command: `coral profile functions`
- [ ] Safety limits (max 50 probes, rate limiting)

### Phase 4: Integration & Polish
- [ ] Integrate with existing `coral attach uprobe` (preserve for granular use)
- [ ] Add async mode for long-running profiling
- [ ] Add progress indicators for CLI
- [ ] Recommendation engine (suggest next steps)
- [ ] Update docs and examples

### Phase 5: Testing
- [ ] Unit tests: selection strategies, bottleneck analysis
- [ ] Integration tests: end-to-end discovery → profile → results
- [ ] Load tests: 50 concurrent probes, 10k function registry
- [ ] LLM workflow tests: verify 2-3 call pattern works

## Testing Strategy

### Unit Tests

**Discovery tool:**
- Search ranking accuracy (precision/recall)
- Metrics availability flags
- Data coverage calculation

**Profiling tool:**
- Selection strategy correctness (critical_path vs all vs entry_points)
- Bottleneck identification algorithm
- Contribution percentage calculation
- Safety limits enforcement (max probes, duration)

### Performance Tests

**Discovery latency:**
- Target: <100ms for 10,000 function registry
- Test with various query complexities

**Profiling orchestration:**
- 50 concurrent probe attachments
- Verify all sessions complete successfully
- Check aggregation performance

## Security & Safety

### Probe Limits

```yaml
# Colony configuration
profiling:
  max_concurrent_probes_per_service: 50
  max_probe_duration: 300  # 5 minutes
  max_events_per_probe: 10000
  rate_limit_per_user: 10  # profile calls per minute
```

### Graceful Degradation

When limits exceeded:
```json
{
  "status": "partial_success",
  "functions_selected": 100,
  "functions_probed": 50,
  "probes_failed": 50,
  "failure_reason": "Exceeded max 50 concurrent probes per service",
  "suggestion": "Use more specific query or reduce --max-functions"
}
```

### Authorization

- Inherit service RBAC from RFD 063
- Users can only profile services they have access to
- Audit logging for all profiling operations

## Future Enhancements

### V2: Advanced Selection Strategies

```
--strategy hot-path         // Functions with highest recent activity
--strategy error-prone       // Functions with elevated error rates
--strategy slow-percentile   // Functions with P99 > threshold
```

### V2: Continuous Profiling Mode

```bash
coral profile functions -s api -q checkout --continuous --interval 5m
# Profiles every 5 minutes, updates registry continuously
```

### V2: Distributed Profiling

Profile functions across multiple service instances:
```bash
coral profile functions -s api -q checkout --instances 5
# Profiles across 5 replicas, aggregates results
```

### V3: AI-Driven Profiling

```bash
coral profile functions -s api --auto-detect
# Uses endpoint metrics to automatically identify slow functions
# Profiles them without requiring explicit query
```

## Dependencies

- **RFD 063**: Function registry and indexing (provides query infrastructure)
- **RFD 059**: Live debugging architecture (uprobe attachment mechanism)
- **RFD 067**: Unified query interface pattern (design consistency)

## References

- RFD 063: Function Registry and Indexing Infrastructure
- RFD 067: Unified Query Interface (design pattern inspiration)
- RFD 059: Live Debugging Architecture (uprobe sessions)
