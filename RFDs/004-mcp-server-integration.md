---
rfd: "004"
title: "MCP Server Integration"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "001", "002", "003", "030" ]
database_migrations: [ ]
areas: [ "mcp", "ai", "integration" ]
---

# RFD 004 - MCP Server Integration

**Status:** 🚧 Draft

## Summary

Enable Coral colonies and reefs to expose their data as Model Context Protocol
(MCP) servers, allowing any MCP-compatible client (Claude Desktop, custom tools,
other LLMs) to query distributed system state, metrics, and insights directly.
This transforms Coral from a standalone tool into a universal context provider
for AI-powered operations.

> **Architectural Note**: Colony acts as a lightweight MCP gateway (no embedded
> LLM), exposing data access tools. Reef hosts a server-side LLM service (RFD 003)
> and can provide AI-powered analysis tools. External LLMs (via `coral ask` with
> local Genkit per RFD 030, or Claude Desktop) consume these MCP tools for
> reasoning.

## Problem

**Current behavior/limitations:**

- Coral insights only accessible via `coral ask` CLI
- Users must context-switch between their LLM tool (Claude Desktop) and terminal
- No way for external AI assistants to access Coral's operational intelligence
- Each tool (Claude Desktop, Cursor, custom agents) would need custom
  integration
- Coral's rich infrastructure knowledge is isolated, not composable with other
  tools

**Why this matters:**

- **Developer workflow**: Developers already use Claude Desktop or other AI
  assistants for coding
- **Context switching**: "Let me check Coral... switch to terminal... copy/paste
  results back to Claude"
- **Composability**: Want to combine Coral data with other MCP servers (GitHub,
  Sentry, Grafana)
- **Universal access**: MCP is becoming a standard - expose Coral data to any
  MCP client
- **Custom automation**: Teams want to build custom agents that query Coral
  programmatically

**Use cases affected:**

- Developer coding in IDE with AI assistant wants to check production health
- SRE using Claude Desktop for incident investigation needs infra metrics
- Custom automation scripts querying Coral data via MCP
- Teams building composite dashboards pulling from multiple MCP sources
- AI-powered runbooks that query Coral for current system state

**Example friction today:**

```
Developer in Claude Desktop:
"Should I deploy this change to production?"

Claude: "I don't have access to your production metrics. You'll need to
check your monitoring system."

Developer switches to terminal:
$ coral ask "is production healthy?"
> Yes, all services healthy, CPU at 45%, no recent errors

Developer switches back to Claude Desktop:
"Production is healthy, CPU at 45%"

Claude: "Good, you should be safe to deploy."
```

**With MCP Server (this RFD):**

```
Developer in Claude Desktop (with Coral MCP server configured):
"Should I deploy this change to production?"

Claude: [Queries coral-prod MCP server automatically]
"Based on your production metrics:
- All services healthy
- CPU at 45% (normal range)
- No errors in last hour
- Last deploy was 2 days ago

You should be safe to deploy. Would you like me to trigger the deployment?"
```

## Solution

Implement MCP server interface in both Coral colonies and reefs, exposing
operational data through standardized MCP tools:

**Key Design Decisions:**

- **Both colony and reef expose MCP**: Developer chooses granularity
    - Colony MCP: Single environment/app (my-shop-prod)
    - Reef MCP: Unified view across environments (all of my-shop)

- **Standard MCP protocol**: Use official MCP specification (JSON-RPC 2.0 over
  stdio/SSE)
    - Enables any MCP client to connect (Claude Desktop, custom tools)
    - Follows same patterns as existing MCP servers (GitHub, Sentry, Grafana)
    - No custom protocol needed

- **Tool-based interface**: Expose Coral queries as MCP tools
    - Colony tools (data access):
        - `coral_get_health`: Get current system health
        - `coral_get_metrics`: Query specific metrics
        - `coral_query_events`: Search events/incidents
        - `coral_get_topology`: Get service dependency graph
        - `coral_query_traces`: Query distributed traces
    - Reef tools (data + AI analysis):
        - `coral_reef_analyze`: AI-powered cross-colony analysis (uses Reef LLM)
        - `coral_compare_environments`: Compare prod vs staging
        - `coral_get_correlations`: Cross-colony correlation patterns

- **Local-first**: MCP server runs locally, queries local colony/reef
    - No external service needed
    - Works air-gapped
    - Low latency (<100ms)

**Benefits:**

- **Seamless workflow**: Query infrastructure from wherever you're already
  working
- **Universal access**: Any MCP client can access Coral data
- **Composability**: Combine Coral with other MCP servers (Grafana + Coral +
  Sentry)
- **Automation-friendly**: Custom agents can use MCP to query Coral
  programmatically
- **No lock-in**: Standard protocol, works with future MCP clients

**Architecture Overview:**

```
┌─────────────────────────────────────────────────────────────┐
│  Claude Desktop (or any MCP client)                         │
│                                                             │
│  User: "Is production healthy? Should I deploy?"            │
│                                                             │
│  Claude: [Automatically queries MCP servers]                │
│    - coral-prod: coral_get_health()                         │
│    - coral-prod: coral_get_metrics(service=api)             │
│    - grafana: query_dashboard(name=production)              │
│    - sentry: get_recent_errors()                            │
│                                                             │
│  Claude: "Production looks good. CPU at 45%, no errors,     │
│           last deploy 2 days ago. Safe to deploy."          │
└─────────────┬───────────────────────────────────────────────┘
              │
              │ MCP Protocol (stdio or SSE)
              │
    ┌─────────┼──────────┬──────────────┬──────────────┐
    │         │          │              │              │
    ▼         ▼          ▼              ▼              ▼
┌─────────┐ ┌─────────┐ ┌──────────┐ ┌─────────┐ ┌─────────┐
│ Coral   │ │ Coral   │ │ Coral    │ │ Grafana │ │ Sentry  │
│ Prod    │ │ Staging │ │ Reef     │ │ MCP     │ │ MCP     │
│ MCP     │ │ MCP     │ │ MCP      │ │ Server  │ │ Server  │
└────┬────┘ └────┬────┘ └────┬─────┘ └─────────┘ └─────────┘
     │           │           │
     ▼           ▼           ▼
┌─────────┐ ┌─────────┐ ┌──────────┐
│ Colony  │ │ Colony  │ │   Reef   │
│ Prod    │ │ Staging │ │ (RFD 003)│
│ DuckDB/ │ │ DuckDB/ │ │ ClickHouse│
│ ClickH  │ │ ClickH  │ │ +LLM Svc │
└─────────┘ └─────────┘ └──────────┘
```

**How MCP Server works:**

```
1. User configures Claude Desktop with Coral MCP servers
   (one per colony or reef they want to query)

2. User asks question in Claude Desktop:
   "Is production healthy?"

3. Claude Desktop sees available MCP tools:
   - coral_get_health (from coral-prod MCP server)
   - coral_get_metrics (from coral-prod MCP server)
   - ...

4. Claude decides to call coral_get_health()

5. Claude Desktop → MCP request → Coral MCP server

6. Coral MCP server queries local colony DuckDB

7. Coral MCP server returns health data via MCP

8. Claude Desktop receives response, synthesizes answer

9. User sees: "Production is healthy, CPU 45%, no errors"
```

### Component Changes

1. **Colony** (MCP server mode):
    - New command: `coral colony mcp-server` - Run colony as MCP server
    - Implements MCP protocol (JSON-RPC 2.0 over stdio)
    - Exposes tools: get_health, get_metrics, query_events, get_topology, ask
    - Queries local DuckDB to fulfill tool requests

2. **Reef** (MCP server mode):
    - New command: `coral reef mcp-server` - Run reef as MCP server
    - Implements MCP protocol (JSON-RPC 2.0 over stdio)
    - Exposes additional tools: compare_environments, get_correlations
    - Queries federated data across colonies

3. **CLI** (MCP helpers):
    - `coral mcp list-tools` - Show available MCP tools for colony/reef
    - `coral mcp test-tool <tool-name>` - Test MCP tool locally
    - `coral mcp generate-config` - Generate Claude Desktop config snippet

4. **MCP Client Library** (optional):
    - Go library for building custom MCP clients
    - Query Coral programmatically from Go applications
    - Used by custom automation scripts

**Configuration Example:**

**Claude Desktop config** (`~/.config/claude/claude_desktop_config.json`):

```json
{
    "mcpServers": {
        "coral-prod": {
            "command": "coral",
            "args": [
                "colony",
                "mcp-server",
                "--colony",
                "my-shop-production"
            ]
        },
        "coral-staging": {
            "command": "coral",
            "args": [
                "colony",
                "mcp-server",
                "--colony",
                "my-shop-staging"
            ]
        },
        "coral-reef": {
            "command": "coral",
            "args": [
                "reef",
                "mcp-server",
                "--reef",
                "my-infrastructure"
            ]
        }
    }
}
```

**Coral MCP server can also run standalone** (SSE transport):

```bash
# Start MCP server with SSE transport (HTTP)
coral colony mcp-server --colony my-shop-production \
  --transport sse \
  --port 3001

# Custom MCP client connects via HTTP
curl http://localhost:3001/sse
```

## API Changes

### MCP Tools Definition

MCP tools are defined using JSON Schema. Coral exposes these tools:

**Colony MCP Tools:**

```json
{
    "tools": [
        {
            "name": "coral_get_health",
            "description": "Get current health status of all services in this colony",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "service_filter": {
                        "type": "string",
                        "description": "Optional: Filter by service name pattern"
                    }
                }
            }
        },
        {
            "name": "coral_get_metrics",
            "description": "Query metrics for a specific service",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "service": {
                        "type": "string",
                        "description": "Service name (e.g., 'api', 'frontend')"
                    },
                    "metric": {
                        "type": "string",
                        "description": "Metric name (e.g., 'cpu_percent', 'response_time_p95')"
                    },
                    "time_range": {
                        "type": "string",
                        "description": "Time range (e.g., '1h', '24h', '7d')",
                        "default": "1h"
                    }
                },
                "required": [
                    "service",
                    "metric"
                ]
            }
        },
        {
            "name": "coral_query_events",
            "description": "Search for events (deploys, restarts, crashes, errors)",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "event_type": {
                        "type": "string",
                        "description": "Event type filter (deploy, restart, crash, error_spike)",
                        "enum": [
                            "deploy",
                            "restart",
                            "crash",
                            "error_spike",
                            "alert"
                        ]
                    },
                    "time_range": {
                        "type": "string",
                        "description": "Time range to search",
                        "default": "24h"
                    },
                    "service": {
                        "type": "string",
                        "description": "Optional: Filter by service"
                    }
                }
            }
        },
        {
            "name": "coral_get_topology",
            "description": "Get service dependency graph and topology",
            "inputSchema": {
                "type": "object",
                "properties": {}
            }
        },
        {
            "name": "coral_query_traces",
            "description": "Query distributed traces for debugging",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "service": {
                        "type": "string",
                        "description": "Service to query traces for"
                    },
                    "time_range": {
                        "type": "string",
                        "description": "Time range (e.g., '1h', '24h')",
                        "default": "1h"
                    },
                    "filter": {
                        "type": "string",
                        "description": "Optional filter expression"
                    }
                },
                "required": [
                    "service"
                ]
            }
        }
    ]
}
```

**Reef MCP Tools (additional):**

> **Note**: Reef hosts a server-side LLM service (RFD 003), enabling AI-powered
> analysis tools. Colony MCP tools are data-only (no embedded LLM), while Reef MCP
> tools can include AI-generated insights.

```json
{
    "tools": [
        {
            "name": "coral_reef_analyze",
            "description": "AI-powered analysis across all colonies (uses Reef's server-side LLM)",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "question": {
                        "type": "string",
                        "description": "Natural language question about cross-colony state"
                    },
                    "time_window": {
                        "type": "string",
                        "description": "Analysis time window (e.g., '1h', '24h', '7d')",
                        "default": "24h"
                    }
                },
                "required": [
                    "question"
                ]
            }
        },
        {
            "name": "coral_compare_environments",
            "description": "Compare metrics across environments (prod vs staging)",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "metric": {
                        "type": "string",
                        "description": "Metric to compare (e.g., 'cpu_percent', 'latency')"
                    },
                    "environments": {
                        "type": "array",
                        "items": {
                            "type": "string"
                        },
                        "description": "Environments to compare (defaults to all)",
                        "default": [
                            "production",
                            "staging"
                        ]
                    },
                    "time_range": {
                        "type": "string",
                        "default": "1h"
                    }
                },
                "required": [
                    "metric"
                ]
            }
        },
        {
            "name": "coral_get_correlations",
            "description": "Get detected correlations across environments (e.g., staging deploy → prod issue)",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "min_confidence": {
                        "type": "number",
                        "description": "Minimum correlation confidence (0.0-1.0)",
                        "default": 0.7
                    },
                    "time_range": {
                        "type": "string",
                        "default": "7d"
                    }
                }
            }
        },
        {
            "name": "coral_get_deployment_timeline",
            "description": "Get deployment timeline across all environments",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "application": {
                        "type": "string",
                        "description": "Filter by application name"
                    },
                    "time_range": {
                        "type": "string",
                        "default": "7d"
                    }
                }
            }
        }
    ]
}
```

### MCP Protocol Implementation

Coral implements MCP using JSON-RPC 2.0:

**Example request (from Claude Desktop):**

```json
{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "tools/call",
    "params": {
        "name": "coral_get_health",
        "arguments": {}
    }
}
```

**Example response (from Coral MCP server):**

```json
{
    "jsonrpc": "2.0",
    "id": 1,
    "result": {
        "content": [
            {
                "type": "text",
                "text": "System Health Report:\n\nOverall Status: Healthy\n\nServices:\n- api: Healthy (CPU: 45%, Memory: 2.1GB, Uptime: 2d 3h)\n- frontend: Healthy (CPU: 12%, Memory: 512MB, Uptime: 2d 3h)\n- database: Healthy (CPU: 23%, Memory: 4.5GB, Uptime: 14d 2h)\n\nNo alerts or issues detected."
            }
        ]
    }
}
```

**Example Reef AI-powered query:**

```json
{
    "jsonrpc": "2.0",
    "id": 2,
    "method": "tools/call",
    "params": {
        "name": "coral_reef_analyze",
        "arguments": {
            "question": "Are there any memory leaks across environments in the last 24 hours?",
            "time_window": "24h"
        }
    }
}
```

**Response (from Reef's server-side LLM):**

```json
{
    "jsonrpc": "2.0",
    "id": 2,
    "result": {
        "content": [
            {
                "type": "text",
                "text": "Memory Analysis Across Environments (Last 24h):\n\nNo memory leaks detected in any environment.\n\nProduction:\n- api: Stable at ~2.1GB (±50MB variance)\n- frontend: Stable at ~512MB (normal for React app)\n- database: Gradual increase from 4.2GB → 4.5GB (normal cache growth)\n\nStaging:\n- api: Similar pattern, ~1.8GB (lower traffic)\n- frontend: Stable at ~480MB\n- database: Stable at ~3.2GB\n\nRecommendation: No action needed. All services within normal parameters across all environments.\n\nModel: anthropic:claude-3-5-sonnet-20241022\nConfidence: 0.94"
            }
        ]
    }
}
```

### CLI Commands

```bash
# Run colony as MCP server (stdio mode for Claude Desktop)
coral colony mcp-server [flags]
  --colony <colony-id>    # Which colony to expose
  --transport stdio       # Transport: stdio (default) or sse

# Example (used by Claude Desktop):
$ coral colony mcp-server --colony my-shop-production

# Output: (starts MCP server on stdio, waits for requests)

---

# Run reef as MCP server
coral reef mcp-server [flags]
  --reef <reef-id>        # Which reef to expose
  --transport stdio

# Example:
$ coral reef mcp-server --reef my-infrastructure

---

# List available MCP tools
coral mcp list-tools [flags]
  --colony <colony-id>    # List tools for colony
  --reef <reef-id>        # List tools for reef

# Example output:
$ coral mcp list-tools --colony my-shop-production

Available MCP Tools for colony my-shop-production:

coral_get_health
  Get current health status of all services in this colony

coral_get_metrics
  Query metrics for a specific service
  Required: service, metric

coral_query_events
  Search for events (deploys, restarts, crashes, errors)

coral_get_topology
  Get service dependency graph and topology

coral_query_traces
  Query distributed traces for debugging
  Required: service

---

# Test MCP tool locally (without MCP client)
coral mcp test-tool <tool-name> [flags]
  --colony <colony-id>
  --args <json>           # Tool arguments as JSON

# Example:
$ coral mcp test-tool coral_get_health --colony my-shop-production

Calling tool: coral_get_health
Arguments: {}

Response:
System Health Report:

Overall Status: Healthy

Services:
- api: Healthy (CPU: 45%, Memory: 2.1GB)
- frontend: Healthy (CPU: 12%, Memory: 512MB)
- database: Healthy (CPU: 23%, Memory: 4.5GB)

---

# Generate Claude Desktop config
coral mcp generate-config [flags]
  --colony <colony-id>    # Include this colony
  --reef <reef-id>        # Include this reef
  --all-colonies          # Include all configured colonies

# Example output:
$ coral mcp generate-config --all-colonies

Copy this to ~/.config/claude/claude_desktop_config.json:

{
  "mcpServers": {
    "coral-my-shop-production": {
      "command": "coral",
      "args": ["colony", "mcp-server", "--colony", "my-shop-production"]
    },
    "coral-my-shop-staging": {
      "command": "coral",
      "args": ["colony", "mcp-server", "--colony", "my-shop-staging"]
    }
  }
}

After adding this config, restart Claude Desktop to enable Coral MCP servers.
```

### Environment Variable Configuration

For Claude Desktop, Coral respects standard config:

```bash
# Use custom config location
export CORAL_CONFIG_HOME=~/custom/.coral

# Specify default colony for MCP server
export CORAL_DEFAULT_COLONY=my-shop-production
```

## Implementation Plan

### Phase 1: Core MCP Protocol

- [ ] Implement MCP protocol handler (JSON-RPC 2.0)
- [ ] Implement stdio transport (for Claude Desktop)
- [ ] Implement SSE transport (for custom clients)
- [ ] Handle tool discovery (list_tools method)
- [ ] Handle tool execution (tools/call method)

### Phase 2: Colony MCP Tools

- [ ] Implement `coral_get_health` tool
- [ ] Implement `coral_get_metrics` tool
- [ ] Implement `coral_query_events` tool
- [ ] Implement `coral_get_topology` tool
- [ ] Implement `coral_query_traces` tool

### Phase 3: Reef MCP Tools

- [ ] Implement `coral_reef_analyze` tool (uses Reef's server-side LLM from RFD
  003)
- [ ] Implement `coral_compare_environments` tool
- [ ] Implement `coral_get_correlations` tool
- [ ] Implement `coral_get_deployment_timeline` tool
- [ ] Handle cross-colony queries in reef MCP server

### Phase 4: CLI Integration

- [ ] Implement `coral colony mcp-server` command
- [ ] Implement `coral reef mcp-server` command
- [ ] Implement `coral mcp list-tools` command
- [ ] Implement `coral mcp test-tool` command
- [ ] Implement `coral mcp generate-config` command

### Phase 5: Testing & Documentation

- [ ] Unit tests for MCP protocol handling
- [ ] Integration tests with MCP client library
- [ ] E2E test with Claude Desktop
- [ ] Documentation: Setting up Coral in Claude Desktop
- [ ] Documentation: Building custom MCP clients
- [ ] Example: Custom automation script using Coral MCP

## Testing Strategy

### Unit Tests

- MCP protocol serialization/deserialization
- Tool schema validation
- Tool execution (mock DuckDB queries)
- Error handling (invalid tool names, missing args)

### Integration Tests

- Full MCP request/response cycle
- Tool execution with real DuckDB
- Multiple concurrent tool calls
- Transport layer (stdio, SSE)

### E2E Tests

**Scenario 1: Claude Desktop Integration**

1. Configure Claude Desktop with Coral MCP server
2. Ask Claude: "Is production healthy?"
3. Verify: Claude calls coral_get_health and returns results
4. Verify: Response includes actual health data from colony

**Scenario 2: Multi-Environment Comparison**

1. Configure Claude Desktop with prod and staging MCP servers
2. Ask Claude: "Compare prod vs staging latency"
3. Verify: Claude calls both MCP servers and compares results

**Scenario 3: Custom MCP Client**

1. Build simple MCP client in Go
2. Connect to Coral MCP server via stdio
3. Call coral_get_metrics tool
4. Verify: Receive metric data in MCP response format

## Security Considerations

### Authentication

**Problem**: MCP server runs locally but exposes system data

**Approach for stdio transport:**

- Stdio transport inherits user's OS permissions
- Only user who can run `coral` command can access MCP server
- No network exposure (stdio is local-only)

**Approach for SSE transport:**

- HTTP server requires authentication token
- Token generated per-session: `coral colony mcp-server --generate-token`
- Client must include token in SSE connection

```bash
# Start MCP server with auth
coral colony mcp-server --transport sse --port 3001 --require-auth

# Output:
MCP Server started on http://localhost:3001
Auth token: mcp-token-abc123xyz789

# Client connects with token:
curl -H "Authorization: Bearer mcp-token-abc123xyz789" \
  http://localhost:3001/sse
```

### Data Exposure

**What MCP exposes:**

- Health status (service names, CPU/memory usage)
- Metrics (numeric values, timestamps)
- Events (deploy times, crash logs)
- Topology (service dependencies)

**What MCP does NOT expose:**

- Raw application data
- Database credentials
- API keys or secrets
- User data or PII

**Controls:**

- MCP tools have read-only access
- No tool can modify colony state
- User controls which colonies are exposed via config

### Rate Limiting

For SSE transport (HTTP), implement rate limiting:

```yaml
# Colony config
mcp_server:
    sse:
        rate_limit: 100  # requests per minute
        burst: 20        # burst allowance
```

## Migration Strategy

**MCP server is optional:**

1. Existing `coral ask` CLI continues working
2. Users opt-in to MCP by configuring Claude Desktop
3. MCP server runs on-demand (not always running)

**Rollout:**

1. Add MCP server support to colonies
2. Document Claude Desktop setup
3. Users add MCP config when ready
4. No breaking changes to existing workflows

## Future Enhancements

### MCP Resources

In addition to tools, expose MCP resources (read-only data):

```json
{
    "resources": [
        {
            "uri": "coral://my-shop-production/health",
            "name": "Current Health Status",
            "mimeType": "application/json"
        },
        {
            "uri": "coral://my-shop-production/topology",
            "name": "Service Topology Graph",
            "mimeType": "application/json"
        }
    ]
}
```

Claude can proactively load these resources as context.

### MCP Prompts

Provide pre-built prompts for common operations:

```json
{
    "prompts": [
        {
            "name": "health_check",
            "description": "Check overall system health",
            "arguments": []
        },
        {
            "name": "deployment_readiness",
            "description": "Assess if system is ready for deployment",
            "arguments": []
        }
    ]
}
```

### Streaming Responses

For long-running queries (coral_reef_analyze), use MCP streaming:

```json
{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "tools/call",
    "params": {
        "name": "coral_reef_analyze",
        "arguments": {
            "question": "Analyze last week's incidents across all environments"
        }
    }
}

Response (streaming):
{
"jsonrpc": "2.0",
"id": 1,
"result": {
"content": [
{"type": "text", "text": "Analyzing incidents..."},
{"type": "text", "text": "Found 3 incidents..."},
{"type": "text", "text": "Incident 1: Database timeout on 2025-10-20..."}
],
"isPartial": true
}
}
```

### Custom Tool Plugins

Allow users to define custom MCP tools:

```yaml
# Colony config
mcp_server:
    custom_tools:
        -   name: my_custom_check
            description: Custom health check for my app
            script: ./scripts/custom_health.sh
```

## Appendix

### Example Use Cases

**Use Case 1: Pre-Deployment Check**

```
Developer in Claude Desktop:
"Should I deploy PR #123 to production?"

Claude:
[Calls coral_get_health]
[Calls coral_query_events(event_type=deploy)]
[Calls coral_get_metrics(service=all)]

"Based on production state:
- All services healthy ✓
- No deploys in last 2 hours ✓
- No active incidents ✓
- CPU/Memory within normal range ✓

Safe to deploy. Recommend deploying during low-traffic window (current traffic: 450 req/s, peak: 1200 req/s)."
```

**Use Case 2: Incident Investigation**

```
SRE in Claude Desktop:
"Why did the API response time spike at 3pm?"

Claude:
[Calls coral_query_events(time_range=2pm-4pm)]
[Calls coral_get_metrics(service=api, metric=response_time_p95)]
[Calls coral_get_topology]

"Timeline of events at 3pm:

14:55 - Database connection pool at 90% (warning threshold)
15:00 - Deploy: api v2.1.3
15:02 - API response time spike: 150ms → 450ms (p95)
15:03 - Database pool maxed out at 100%
15:05 - API error rate increased to 2.3%

Root cause: API v2.1.3 introduced connection leak. Each request opened new DB connection without closing.

Correlation: Same pattern observed in staging 2 hours earlier.

Recommendation: Rollback to v2.1.2 and fix connection handling."
```

**Use Case 3: Cross-Environment Comparison**

```
Developer in Claude Desktop (with reef MCP):
"Why is prod slower than staging?"

Claude:
[Calls coral_compare_environments(metric=latency)]
[Calls coral_get_correlations]

"Production vs Staging Comparison:

Latency (p95):
- Prod: 245ms
- Staging: 180ms
- Difference: +36%

Contributing factors:
1. Traffic: Prod has 3.2x more traffic (1200 vs 375 req/s)
2. Database pool: Prod at 95% utilization, Staging at 60%
3. Cache hit rate: Prod 78%, Staging 92% (higher in staging due to less traffic variability)

Recommendation:
- Increase prod database pool from 100 → 150 connections
- Expected latency reduction: 245ms → ~200ms (based on staging correlation)"
```

**Use Case 4: Custom Automation**

```go
// Custom automation script using Coral MCP client
package main

import (
    "github.com/coral-io/coral/pkg/mcp/client"
)

func main() {
    // Connect to Coral MCP server
    c := client.New("coral", []string{"colony", "mcp-server", "--colony", "my-shop-production"})

    // Call health check
    health, err := c.CallTool("coral_get_health", nil)
    if err != nil {
        log.Fatal(err)
    }

    // Parse response
    if health.Status != "Healthy" {
        // Send alert to Slack
        slackAlert("Production unhealthy: " + health.Details)
    }

    // Check for high CPU
    metrics, err := c.CallTool("coral_get_metrics", map[string]any{
        "service": "api",
        "metric":  "cpu_percent",
    })

    if metrics.Value > 80 {
        // Trigger auto-scaling
        scaleUp("api", currentInstances+2)
    }
}
```

### Comparison with Alternative Approaches

**Alternative 1: Custom REST API**

❌ Every client needs custom integration
❌ No standard protocol
❌ Less composable with other tools

**Alternative 2: GraphQL**

✅ Flexible querying
❌ Not AI-native (LLMs work better with tools/functions)
❌ Requires client code generation

**Alternative 3: MCP (Chosen)**

✅ Standard protocol (works with any MCP client)
✅ AI-native (designed for LLM integration)
✅ Composable (combine with other MCP servers)
✅ Tool-based interface matches LLM capabilities
✅ Growing ecosystem (Claude Desktop, custom clients)

### MCP vs Coral CLI

**When to use `coral ask` CLI:**

- ✅ Quick terminal-based queries
- ✅ Scripting/automation (shell scripts)
- ✅ CI/CD pipelines
- ✅ No AI assistant needed

**When to use MCP server:**

- ✅ Querying from Claude Desktop or other LLM tools
- ✅ Building custom AI agents
- ✅ Composing with other MCP servers (Grafana + Coral + Sentry)
- ✅ Rich context for AI decision-making

**Both coexist:** MCP server is additional interface, doesn't replace CLI.

---

## Notes

**Design Philosophy:**

- **Standard protocol**: Use MCP spec, don't invent custom protocol
- **Local-first**: MCP server queries local data, no cloud dependencies
- **Optional layer**: Colonies work without MCP, users opt-in
- **AI-native**: Tools designed for LLM consumption

**Relationship to other RFDs:**

- RFD 001: Discovery service (unchanged)
- RFD 002: Application identity (MCP server uses colony config)
- RFD 003: Reef federation (Reef exposes MCP server with AI-powered tools via
  server-side LLM)
- RFD 014: Abandoned (Colony-embedded LLM approach replaced; Colony is now MCP
  gateway only)
- RFD 030: Coral ask CLI (local Genkit agent can consume Colony/Reef MCP tools)

**LLM Architecture Integration:**

- **Colony MCP**: Data-only tools (metrics, events, topology, traces) - no
  embedded LLM
- **Reef MCP**: Data tools + AI-powered analysis (via server-side Genkit LLM
  service)
- **External LLM clients**: Claude Desktop, custom tools, or local Genkit
  agents (RFD 030) consume MCP tools
- **Three-tier model**: Colony (control plane) → Reef (enterprise
  intelligence) → Developer agents (exploration)

**Why this is powerful:**

Coral becomes a **universal context provider** for AI-powered operations:

1. Developer writes code in Cursor (with Claude)
2. Claude: "Let me check production health" → queries Coral MCP
3. Claude: "Database pool at 95%, recommend increasing" → from Coral data
4. Developer deploys with confidence, guided by real production state
