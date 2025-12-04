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

### Unified Query Interface (RFD 067)

The unified query tools combine data from eBPF and OTLP sources automatically, providing a complete observability picture.

#### `coral_query_summary`

Get high-level health summary for services with anomaly detection.

```
Input:
  service (optional): Service name (omit for all services)
  time_range: "5m", "1h", "24h" (default: "5m")

Returns:
  - Health status (✅ healthy, ⚠️ degraded, ❌ critical)
  - Request count, error rate, average latency
  - Data source annotation (eBPF, OTLP, or eBPF+OTLP)
  - Issues detected (error rate spikes, latency spikes)
```

**Example:**
```json
{
  "service": "payments-api",
  "time_range": "5m"
}
```

#### `coral_query_traces`

Query distributed traces from all sources (eBPF + OTLP).

```
Input:
  service (optional): Filter by service name
  time_range: "1h", "30m", "24h" (default: "1h")
  source (optional): "ebpf", "telemetry", "all" (default: "all")
  trace_id (optional): Specific trace ID
  min_duration_ms (optional): Filter slow traces
  max_traces: Maximum traces to return (default: 10)

Returns:
  - Trace ID, service name, span name, duration
  - Parent-child relationships
  - Source annotations (📍 eBPF, 📊 OTLP)
  - For OTLP: aggregated metrics (total spans, error count)
```

**Example:**
```json
{
  "service": "payments-api",
  "time_range": "1h",
  "source": "all",
  "min_duration_ms": 500
}
```

#### `coral_query_metrics`

Query HTTP/gRPC/SQL metrics from all sources (eBPF + OTLP).

```
Input:
  service (optional): Filter by service name
  time_range: "1h", "30m", "24h" (default: "1h")
  source (optional): "ebpf", "telemetry", "all" (default: "all")
  protocol (optional): "http", "grpc", "sql", "auto" (default: "auto")
  http_route (optional): Filter by HTTP route
  http_method (optional): Filter by HTTP method
  status_code_range (optional): Filter by status code range

Returns:
  - HTTP/gRPC/SQL metrics from eBPF and OTLP
  - Request counts, latency percentiles (P50/P95/P99)
  - Source annotations for each metric
  - Route/method/operation breakdown
```

**Example:**
```json
{
  "service": "payments-api",
  "time_range": "1h",
  "source": "all",
  "protocol": "http"
}
```

#### `coral_query_logs`

Query logs from OTLP sources.

```
Input:
  service (optional): Filter by service name
  time_range: "1h", "30m", "24h" (default: "1h")
  level (optional): "debug", "info", "warn", "error"
  search (optional): Full-text search query
  max_logs: Maximum logs to return (default: 100)

Returns:
  - Log entries from OTLP
  - Timestamp, level, message
  - Filtered by search terms and level
```

**Example:**
```json
{
  "service": "payments-api",
  "time_range": "1h",
  "level": "error",
  "search": "timeout"
}
```

### Service Discovery

#### `coral_list_services`

List all services known to the colony.

```
Input:
  agent_id (optional): Filter by agent ID
  service_name (optional): Filter by service name

Returns: Service names, ports, types, and health status
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
  → coral_query_summary()
  → coral_query_metrics(service=api, time_range=1h)

"Based on production state:
- All services healthy ✓
- API: 12.5k requests, 0.2% errors, 145ms avg latency (normal)
- No degraded services ✓
- No critical issues detected ✓

Safe to deploy. System is stable with normal traffic patterns."
```

### Incident Investigation

```
You: "Why is checkout slow?"

Claude: [Orchestrates multiple tool calls]
  → coral_query_summary(service=checkout, time_range=1h)
     Result: ⚠️ degraded - 850ms avg latency (baseline: 200ms)

  → coral_query_traces(service=checkout, min_duration_ms=500, max_traces=5)
     Result: 80% of slow traces wait for payment-api

  → coral_query_metrics(service=payment-api, time_range=1h, protocol=sql)
     Result: SQL queries: P95 650ms (baseline: 50ms)

"Checkout is slow because payment-api database queries are slow. Analysis:

1. Checkout: 850ms avg latency (↑325% from baseline)
2. Root cause: payment-api SQL queries
3. Specific issue: Database SELECT queries taking 650ms
4. Data sources: eBPF+OTLP (complete view)

Recommendation: Check database indexes or investigate recent
data migration that may have caused table bloat."
```

### Cross-Environment Comparison

```
You: "Why is prod slower than staging?"

Claude: [Queries both colonies]
  → coral-prod: coral_query_summary(service=api)
  → coral-staging: coral_query_summary(service=api)

"Production vs Staging Comparison:

Latency:
- Prod: 245ms avg (⚠️ degraded)
- Staging: 180ms avg (✅ healthy)
- Difference: +36%

Contributing factors:
1. Traffic: Prod has 3.2x more traffic (12.5k vs 3.8k req/min)
2. Error rate: Prod 0.8%, Staging 0.1%
3. Data sources: Both showing eBPF+OTLP data

Recommendation: Investigate prod error rate spike and traffic patterns.
Consider scaling or optimizing database connections."
```

### Health Monitoring

```
You: "Is everything healthy?"

Claude: [Single tool call]
  → coral_query_summary()

"System Health Report:

Overall Status: ✓ Healthy

Services:
✅ api (eBPF+OTLP): 12.5k requests, 0.2% errors, 145ms avg
✅ frontend (eBPF): 8.1k requests, 0.1% errors, 12ms avg
✅ checkout (eBPF+OTLP): 3.2k requests, 0.3% errors, 89ms avg
✅ payment-api (eBPF+OTLP): 2.8k requests, 0.5% errors, 156ms avg
✅ worker (OTLP): 1.2k tasks, 0.0% errors, 45ms avg

No critical issues detected. All services operating within normal parameters."
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
- ✅ Unified query interface (RFD 067)
  - ✅ `coral_query_summary` - Health overview with anomaly detection
  - ✅ `coral_query_traces` - Unified traces (eBPF + OTLP)
  - ✅ `coral_query_metrics` - Unified metrics (eBPF + OTLP)
  - ✅ `coral_query_logs` - Logs from OTLP
- ✅ Service discovery tools
- ✅ Claude Desktop integration
- ✅ CLI commands for testing and config generation
- ✅ Live debugging tools (uprobe attach/detach, session management)
- ✅ Shell and container execution tools

**Not Yet Implemented:**

- ⏳ Complete anomaly detection in `coral_query_summary`
- ⏳ `test-tool` command execution (structure exists, prints placeholder)
- ⏳ `coral_get_debug_results` aggregation (basic summary available via detach)
- ⏳ Analysis tools (event correlation, environment comparison)

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
