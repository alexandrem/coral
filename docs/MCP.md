# MCP Integration - AI-Powered Observability

Coral Colony exposes its observability data through the **Model Context
Protocol (MCP)**, enabling AI assistants like Claude Desktop to query
distributed system health, metrics, traces, and telemetry.

## Overview

**Colony acts as an MCP Server** - it exposes tools that AI assistants can call
to access:

- Service health and topology
- eBPF RED metrics (HTTP/gRPC/SQL)
- Distributed traces
- OTLP telemetry (spans, metrics, logs)
- Operational events

**No embedded LLM** - Colony is a pure data provider. External LLMs (Claude
Desktop, `coral ask`) query Colony via MCP and synthesize insights.

## Quick Start

### 1. Start a Colony

```bash
coral colony start
```

Colony's MCP server starts automatically (unless disabled in config).

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

Claude will automatically call the appropriate Coral MCP tools and synthesize
the results.

## Architecture

### Current Implementation

```
┌──────────────────────────────────────────────────────────┐
│  Claude Desktop / coral ask / Custom MCP Client          │
│  (External LLM - Anthropic Claude, OpenAI, Ollama)       │
└─────────────────────┬────────────────────────────────────┘
                      │ MCP Protocol (stdio)
                      ▼
         ┌────────────────────────────┐
         │   Proxy Command            │
         │   (MCP ↔ RPC translator)   │
         └────────────┬───────────────┘
                      │ Buf Connect gRPC
                      │ (CallTool, StreamTool, ListTools)
                      ▼
         ┌────────────────────────────┐
         │   Colony Server            │
         │   • MCP Server (Genkit)    │
         │   • Tool execution         │
         │   • Business logic         │
         └────────────┬───────────────┘
                      │ (real-time queries)
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
- Clean separation: proxy only translates protocols, no business logic
- No database access in proxy
- Scalable: multiple proxies can connect to same colony
- Type-safe with protocol buffers

**Key Point:** The LLM lives OUTSIDE the colony. Colony just provides data
access tools.

## Available MCP Tools

### Service Health & Topology

#### `coral_get_service_health`

Get current health status of all services.

```
Input:
  service_filter (optional): Filter by service name pattern

Returns: Health status, CPU/memory usage, uptime, issues
```

#### `coral_get_service_topology`

Get service dependency graph discovered from traces.

```
Input:
  filter (optional): Filter by service, tag, or region
  format: "graph" | "list" | "json"

Returns: Service relationships and call frequencies
```

### eBPF RED Metrics

#### `coral_query_ebpf_http_metrics`

Query HTTP request rate, error rate, and latency distributions.

```
Input:
  service (required): Service name
  time_range: "1h", "30m", "24h" (default: "1h")
  http_route (optional): Filter by route pattern
  http_method (optional): GET, POST, PUT, DELETE, PATCH
  status_code_range (optional): 2xx, 3xx, 4xx, 5xx

Returns: P50/P95/P99 latency, request rates, error counts by status code
```

#### `coral_query_ebpf_grpc_metrics`

Query gRPC method-level RED metrics.

```
Input:
  service (required): Service name
  time_range: "1h", "30m", "24h" (default: "1h")
  grpc_method (optional): Filter by gRPC method
  status_code (optional): gRPC status code (0=OK, 1=CANCELLED, etc.)

Returns: RPC rate, latency distributions, status breakdowns
```

#### `coral_query_ebpf_sql_metrics`

Query SQL operation metrics.

```
Input:
  service (required): Service name
  time_range: "1h", "30m", "24h" (default: "1h")
  sql_operation (optional): SELECT, INSERT, UPDATE, DELETE
  table_name (optional): Filter by table

Returns: Query latencies, operation types, table statistics
```

### Distributed Tracing

#### `coral_query_ebpf_traces`

Query distributed traces.

```
Input:
  trace_id (optional): Specific trace ID
  service (optional): Filter by service
  time_range: "1h", "30m", "24h" (default: "1h")
  min_duration_ms (optional): Only slow traces
  max_traces: Default 10

Returns: List of traces with spans and timing
```

#### `coral_get_trace_by_id`

Get a specific trace by ID with full span tree.

```
Input:
  trace_id (required): 32-char hex trace ID
  format: "tree" | "flat" | "json"

Returns: Complete trace with parent-child relationships
```

### OTLP Telemetry

#### `coral_query_telemetry_spans`

Query generic OTLP spans from instrumented applications.

```
Input:
  service (required): Service name
  time_range: "1h", "30m", "24h" (default: "1h")
  operation (optional): Filter by operation name

Returns: OTLP span summaries
```

#### `coral_query_telemetry_metrics`

Query generic OTLP metrics.

```
Input:
  metric_name: Metric name (e.g., "http.server.duration")
  service (optional): Filter by service
  time_range: Default "1h"

Returns: Time-series data for custom metrics
```

#### `coral_query_telemetry_logs`

Query generic OTLP logs.

```
Input:
  query: Search query (full-text)
  service (optional): Filter by service
  level (optional): DEBUG, INFO, WARN, ERROR, FATAL
  time_range: Default "1h"

Returns: Log entries with timestamps and attributes
```

### Events

#### `coral_query_events`

Query operational events tracked by Coral.

```
Input:
  event_type (optional): deploy, restart, crash, alert, config_change, connection, error_spike
  time_range: Default "24h"
  service (optional): Filter by service

Returns: List of events with timestamps and details
```

### Live Debugging

#### `coral_attach_uprobe`

Attach eBPF uprobe to application function for live debugging.

```
Input:
  agent_id (optional): Specific agent ID (auto-resolved from service_name if omitted)
  service_name (required): Service name to debug
  function_name (required): Function to attach uprobe to
  sdk_addr (optional): SDK address (auto-resolved from service labels if omitted)
  duration (optional): Collection duration (default: 60s, max: 10m)
  config (optional): Additional collector configuration

Returns: Session ID, expiration time, success status
```

**Note:** Uprobes are production-safe and time-limited. They capture function
entry/exit events and measure duration without modifying application behavior.

#### `coral_detach_uprobe`

Stop debug session early and detach eBPF probes.

```
Input:
  session_id (required): Debug session ID from coral_attach_uprobe

Returns: Success status, collected data summary
```

#### `coral_list_debug_sessions`

List active and recent debug sessions across services.

```
Input:
  service_name (optional): Filter by service
  status (optional): Filter by status (active, stopped)

Returns: List of debug sessions with metadata
```

#### `coral_get_debug_results`

Get aggregated results from debug session.

```
Input:
  session_id (required): Debug session ID

Returns: Call counts, duration percentiles, slow outliers
```

**Note:** This tool is not yet fully implemented. Use `coral_detach_uprobe` to
get basic session summary.

## CLI Commands

All MCP-related commands are under `coral colony mcp`:

```bash
# List available tools
coral colony mcp list-tools

# Test a tool locally (without MCP client)
coral colony mcp test-tool coral_get_service_health
coral colony mcp test-tool coral_query_ebpf_http_metrics \
  --args '{"service":"api","time_range":"1h"}'

# Generate Claude Desktop config
coral colony mcp generate-config

# Generate config for multiple colonies
coral colony mcp generate-config --all-colonies

# Start MCP proxy (used by Claude Desktop)
coral colony mcp proxy
coral colony mcp proxy --colony my-shop-production
```

## Configuration

### Colony Config (`colony.yaml`)

```yaml
# MCP server configuration (enabled by default)
mcp:
    # Set to true to disable MCP server
    disabled: false

    # Tool filtering (optional)
    enabled_tools:
    # By default, all tools are enabled
    # Uncomment to restrict to specific tools:
    # - coral_query_ebpf_http_metrics
    # - coral_get_service_health
    # - coral_query_events

    # Security settings
    security:
        # Require RBAC checks for action tools (future: exec, shell, ebpf)
        require_rbac_for_actions: true

        # Audit all MCP tool calls
        audit_enabled: true
```

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

Claude: [Automatically queries MCP tools]
  → coral_get_service_health()
  → coral_query_ebpf_http_metrics(service=api, time_range=1h)
  → coral_query_events(event_type=deploy, time_range=24h)

"Based on production state:
- All services healthy ✓
- API P95 latency: 145ms (normal range)
- Error rate: 0.2% (baseline)
- No deploys in last 2 hours ✓
- No active incidents ✓

Safe to deploy. Recommend deploying during low-traffic window
(current: 450 req/s, peak: 1200 req/s)."
```

### Incident Investigation

```
You: "Why is checkout slow?"

Claude: [Orchestrates multiple tool calls]
  → coral_query_ebpf_http_metrics(service=checkout, time_range=1h)
     Result: P95 latency 850ms (baseline: 200ms)

  → coral_query_ebpf_traces(service=checkout, min_duration_ms=500, max_traces=5)
     Result: 80% of slow traces wait for payment-api

  → coral_query_ebpf_http_metrics(service=payment-api, time_range=1h)
     Result: payment-api P95 is 700ms (baseline: 150ms)

  → coral_query_ebpf_sql_metrics(service=payment-api, time_range=1h)
     Result: SELECT on 'cards' table: P95 650ms (baseline: 50ms)

"Checkout is slow because payment-api is slow. Analysis shows:

1. Checkout P95: 850ms (↑325% from baseline)
2. Root cause: payment-api SQL queries are slow
3. Specific issue: 'cards' table SELECT queries taking 650ms

Recommendation: Check database indexes on the 'cards' table or
investigate if a recent data migration caused table bloat."
```

### Cross-Environment Comparison

```
You: "Why is prod slower than staging?"

Claude: [Queries both colonies]
  → coral-prod: coral_query_ebpf_http_metrics(service=api)
  → coral-staging: coral_query_ebpf_http_metrics(service=api)

"Production vs Staging Comparison:

Latency (P95):
- Prod: 245ms
- Staging: 180ms
- Difference: +36%

Contributing factors:
1. Traffic: Prod has 3.2x more traffic (1200 vs 375 req/s)
2. Database connections: Prod at 95% pool utilization, Staging at 60%
3. Cache hit rate: Prod 78%, Staging 92%

Recommendation: Increase prod database pool from 100 → 150 connections.
Expected improvement: 245ms → ~200ms based on staging correlation."
```

### Health Monitoring

```
You: "Is everything healthy?"

Claude: [Single tool call]
  → coral_get_service_health()

"System Health Report:

Overall Status: ✓ Healthy

Services (6 total):
✓ api: Healthy (CPU: 45%, Memory: 2.1GB, Uptime: 2d 3h)
✓ frontend: Healthy (CPU: 12%, Memory: 512MB, Uptime: 2d 3h)
✓ checkout: Healthy (CPU: 23%, Memory: 1.8GB, Uptime: 2d 3h)
✓ payment-api: Healthy (CPU: 18%, Memory: 980MB, Uptime: 2d 3h)
✓ worker: Healthy (CPU: 31%, Memory: 756MB, Uptime: 2d 3h)
✓ database: Healthy (CPU: 55%, Memory: 4.5GB, Uptime: 14d 2h)

No alerts or issues detected."
```

### Live Debugging Investigation

```
You: "Debug the ProcessPayment function in the payment service"

Claude: [Attaches uprobe and monitors]
  → coral_attach_uprobe(service_name=payment-service, function_name=ProcessPayment, duration=60s)
     Result: Session ID: abc-123, expires in 60s

  [Waits for events to collect...]

  → coral_detach_uprobe(session_id=abc-123)
     Result: Collected 47 events, avg duration: 125ms, P95: 380ms

"Debug Session Results:

Function: ProcessPayment
Duration: 60 seconds
Events Captured: 47 calls

Performance Analysis:
- Average duration: 125ms
- P95 latency: 380ms
- Slowest call: 890ms

The function is being called ~47 times per minute. Most calls complete
quickly (125ms avg), but 5% of calls take over 380ms. The slowest call
took 890ms, suggesting occasional performance spikes.

Recommendation: Investigate the slow outliers - they may indicate
database contention or external API timeouts."
```

## Implementation Status

**Currently Implemented:**

- ✅ MCP server with stdio transport
- ✅ Service health and topology tools
- ✅ eBPF HTTP/gRPC/SQL metrics
- ✅ Distributed tracing queries
- ✅ OTLP telemetry summaries
- ✅ Claude Desktop integration
- ✅ CLI commands for testing and config generation
- ✅ Live debugging tools (uprobe attach/detach, session management)

**Not Yet Implemented:**

- ⏳ `test-tool` command execution (structure exists, prints placeholder)
- ⏳ `coral_get_debug_results` aggregation (basic summary available via detach)
- ⏳ Analysis tools (event correlation, environment comparison) - requires event
  storage
- ⏳ Raw telemetry queries - see RFD 041 for agent direct queries

## Troubleshooting

### MCP server not showing in Claude Desktop

1. Check colony is running: `coral colony status`
2. Verify MCP is enabled in `colony.yaml`: `mcp.disabled: false`
3. Check Claude Desktop config path is correct
4. Restart Claude Desktop after config changes
5. Check Claude Desktop logs for errors

### Tools not working

1. Test tools locally first:
   ```bash
   coral colony mcp test-tool coral_get_service_health
   ```

2. Check if colony has data:
    - Are agents connected?
    - Is eBPF observability collecting metrics?
    - Are services instrumented with OTLP?

3. Verify time ranges:
    - Default is 1 hour
    - If services just started, try shorter ranges: "5m", "10m"

### Data seems stale or outdated

The proxy uses real-time RPCs to query the colony, so data should be up-to-date.
If you see stale data:

1. Check colony is actively receiving data from agents:
   ```bash
   coral colony status
   ```

2. Verify agents are connected and sending telemetry:
   ```bash
   coral colony mcp test-tool coral_get_service_health
   ```

3. Check Beyla is running and collecting metrics in agents

4. Adjust time ranges to match your data collection intervals:
    - If services just started, use shorter ranges: "5m", "10m"
    - For historical analysis, use longer ranges: "1h", "24h"

### Permission denied errors

Check that:

- Colony is running with proper permissions
- MCP security settings in `colony.yaml` are not too restrictive
- Audit logging is working (check colony logs)
