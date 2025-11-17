# MCP Integration - AI-Powered Observability

Coral Colony exposes its observability data through the **Model Context Protocol (MCP)**, enabling AI assistants like Claude Desktop to query distributed system health, metrics, traces, and telemetry.

## Overview

**Colony acts as an MCP Server** - it exposes tools that AI assistants can call to access:
- Service health and topology
- Beyla RED metrics (HTTP/gRPC/SQL)
- Distributed traces
- OTLP telemetry (spans, metrics, logs)
- Operational events

**No embedded LLM** - Colony is a pure data provider. External LLMs (Claude Desktop, `coral ask`) query Colony via MCP and synthesize insights.

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
      "args": ["colony", "mcp", "proxy"]
    }
  }
}
```

Copy this to `~/.config/claude/claude_desktop_config.json` (macOS) or `%APPDATA%/Claude/claude_desktop_config.json` (Windows).

### 3. Restart Claude Desktop

After restarting, Claude can now query your Coral colony.

### 4. Ask Questions

Try asking Claude:
- "Is production healthy?"
- "Show me HTTP error rates for the API service"
- "What's the P95 latency for the checkout service?"
- "Find slow database queries"

Claude will automatically call the appropriate Coral MCP tools and synthesize the results.

## Architecture

### Current Implementation (Temporary)

```
┌──────────────────────────────────────────────────────────┐
│  Claude Desktop / coral ask / Custom MCP Client          │
│  (External LLM - Anthropic Claude, OpenAI, Ollama)       │
└─────────────────────┬────────────────────────────────────┘
                      │ MCP Protocol (stdio)
                      ▼
         ┌────────────────────────────┐
         │   Proxy Command            │
         │   (Read-Only DB Access)    │  ← Temporary workaround
         └────────────┬───────────────┘
                      │ (reads database snapshots)
                      ▼
         ┌────────────────────────────┐
         │   Colony DuckDB            │
         │   • Metrics summaries      │
         │   • Trace data             │
         │   • Agent registry         │
         │   • Events                 │
         └────────────────────────────┘
```

**Current Limitation:** The proxy opens the database in read-only mode to query
data directly. This works but may show slightly stale data and isn't
architecturally correct.

### Planned Architecture (Future)

```
┌──────────────────────────────────────────────────────────┐
│  Claude Desktop / coral ask / Custom MCP Client          │
└─────────────────────┬────────────────────────────────────┘
                      │ MCP Protocol (stdio)
                      ▼
         ┌────────────────────────────┐
         │   Proxy Command            │
         │   (HTTP ↔ stdio bridge)    │
         └────────────┬───────────────┘
                      │ MCP over HTTP Streamable
                      ▼
         ┌────────────────────────────┐
         │   Colony MCP Server        │
         │   (HTTP endpoint)          │
         └────────────┬───────────────┘
                      │ (real-time queries)
                      ▼
         ┌────────────────────────────┐
         │   Colony DuckDB            │
         └────────────────────────────┘
```

**Future Improvement:** Colony will expose MCP over HTTP Streamable transport,
and the proxy will become a simple bridge between stdio and HTTP. See RFD 004
"Deferred Features" for details.

**Key Point:** The LLM lives OUTSIDE the colony. Colony just provides data access tools.

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

### Beyla RED Metrics

#### `coral_query_beyla_http_metrics`

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

#### `coral_query_beyla_grpc_metrics`

Query gRPC method-level RED metrics.

```
Input:
  service (required): Service name
  time_range: "1h", "30m", "24h" (default: "1h")
  grpc_method (optional): Filter by gRPC method
  status_code (optional): gRPC status code (0=OK, 1=CANCELLED, etc.)

Returns: RPC rate, latency distributions, status breakdowns
```

#### `coral_query_beyla_sql_metrics`

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

#### `coral_query_beyla_traces`

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

## CLI Commands

All MCP-related commands are under `coral colony mcp`:

```bash
# List available tools
coral colony mcp list-tools

# Test a tool locally (without MCP client)
coral colony mcp test-tool coral_get_service_health
coral colony mcp test-tool coral_query_beyla_http_metrics \
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
    # - coral_query_beyla_http_metrics
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
      "args": ["colony", "mcp", "proxy", "--colony", "my-app-production"]
    },
    "coral-staging": {
      "command": "coral",
      "args": ["colony", "mcp", "proxy", "--colony", "my-app-staging"]
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
  → coral_query_beyla_http_metrics(service=api, time_range=1h)
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
  → coral_query_beyla_http_metrics(service=checkout, time_range=1h)
     Result: P95 latency 850ms (baseline: 200ms)

  → coral_query_beyla_traces(service=checkout, min_duration_ms=500, max_traces=5)
     Result: 80% of slow traces wait for payment-api

  → coral_query_beyla_http_metrics(service=payment-api, time_range=1h)
     Result: payment-api P95 is 700ms (baseline: 150ms)

  → coral_query_beyla_sql_metrics(service=payment-api, time_range=1h)
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
  → coral-prod: coral_query_beyla_http_metrics(service=api)
  → coral-staging: coral_query_beyla_http_metrics(service=api)

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

## Implementation Status

**Currently Implemented (RFD 004):**
- ✅ MCP server with stdio transport
- ✅ Service health and topology tools
- ✅ Beyla HTTP/gRPC/SQL metrics
- ✅ Distributed tracing queries
- ✅ OTLP telemetry summaries
- ✅ Claude Desktop integration
- ✅ CLI commands for testing and config generation

**Not Yet Implemented:**
- ⏳ `test-tool` command execution (structure exists, prints placeholder)
- ⏳ Live debugging tools (eBPF, exec, shell) - requires RFD 013, 017, 026
- ⏳ Analysis tools (event correlation, environment comparison) - requires event storage
- ⏳ Raw telemetry queries - see RFD 041 for agent direct queries

## Advanced: Custom MCP Clients

You can build custom automation using Coral's MCP server:

```go
package main

import (
    "github.com/coral-io/coral/pkg/mcp/client"
)

func main() {
    // Connect to Coral MCP server
    c := client.New("coral", []string{"colony", "mcp", "proxy", "--colony", "production"})

    // Query health
    health, err := c.CallTool("coral_get_service_health", nil)
    if err != nil {
        log.Fatal(err)
    }

    // Parse and act on results
    if health.Status != "Healthy" {
        slackAlert("Production unhealthy: " + health.Details)
    }

    // Check for high error rates
    metrics, err := c.CallTool("coral_query_beyla_http_metrics", map[string]any{
        "service": "api",
        "time_range": "5m",
        "status_code_range": "5xx",
    })

    if metrics.ErrorRate > 1.0 {
        // Trigger auto-remediation
        scaleUp("api", currentInstances+2)
    }
}
```

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
   - Is Beyla collecting metrics?
   - Are services instrumented with OTLP?

3. Verify time ranges:
   - Default is 1 hour
   - If services just started, try shorter ranges: "5m", "10m"

### Data seems stale or outdated

**Known limitation:** The proxy currently reads the database in read-only mode,
which may show data that's a few seconds old. This is a temporary workaround.

**Workaround:** Data is typically refreshed within seconds. For real-time queries,
wait for the HTTP Streamable transport implementation (see RFD 004 Deferred
Features).

**Impact:** Usually negligible for typical observability queries (1h, 5m windows)

### Permission denied errors

Check that:
- Colony is running with proper permissions
- MCP security settings in `colony.yaml` are not too restrictive
- Audit logging is working (check colony logs)

## What's Next?

See [RFD 004](../RFDs/004-mcp-server-integration.md) for full implementation details and [RFD 041](../RFDs/041-mcp-agent-direct-queries.md) for upcoming features like direct agent queries for detailed telemetry.

For Coral as an MCP client (querying other MCP servers like Grafana, Sentry), see the roadmap in RFD 004's "Future Enhancements" section.
