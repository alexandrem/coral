
## MCP Integration - Composable Intelligence

Coral embraces the **Model Context Protocol (MCP)** to be composable with other specialized tools, rather than trying to reinvent the wheel.

### Coral as an MCP Client

Coral calls specialized MCP servers to gather context from your existing tools:

```yaml
# ~/.coral/config.yaml
mcp:
  servers:
    # Metrics analysis via Grafana MCP
    grafana:
      command: "npx"
      args: ["-y", "@grafana/mcp-server"]
      env:
        GRAFANA_URL: "https://grafana.company.com"
        GRAFANA_TOKEN: "${GRAFANA_TOKEN}"
      capabilities:
        - query_metrics
        - list_dashboards
        - get_alerts

    # Error tracking via Sentry MCP
    sentry:
      command: "docker"
      args: ["run", "-i", "sentry/mcp-server"]
      env:
        SENTRY_DSN: "${SENTRY_DSN}"
        SENTRY_ORG: "my-org"
      capabilities:
        - query_errors
        - get_release_health
        - search_issues

    # Incident context via PagerDuty MCP
    pagerduty:
      command: "npx"
      args: ["-y", "@pagerduty/mcp-server"]
      env:
        PD_API_KEY: "${PD_API_KEY}"
      capabilities:
        - get_incidents
        - list_oncall
        - get_service_status

    # Custom internal tools
    internal-metrics:
      command: "/usr/local/bin/company-mcp-server"
      env:
        API_ENDPOINT: "https://internal-api.company.com"
      capabilities:
        - query_custom_metrics
        - get_deployment_info
```

### Coral as an MCP Server

Other AI assistants (Claude Desktop, custom tools) can query Coral for topology and correlation insights:

**Available Tools**:
```json
{
  "tools": [
    {
      "name": "coral_get_topology",
      "description": "Get current service topology and dependencies discovered by Coral agents",
      "inputSchema": {
        "type": "object",
        "properties": {
          "filter": {
            "type": "string",
            "description": "Filter by service name, tag, or region"
          }
        }
      }
    },
    {
      "name": "coral_query_events",
      "description": "Query deployment and operational events tracked by Coral",
      "inputSchema": {
        "type": "object",
        "properties": {
          "service": {"type": "string"},
          "event_type": {
            "type": "string",
            "enum": ["deploy", "restart", "crash", "connection", "alert"]
          },
          "time_range": {"type": "string", "description": "e.g., '1h', '24h', '7d'"}
        }
      }
    },
    {
      "name": "coral_analyze_correlation",
      "description": "Correlate events across services to identify root causes",
      "inputSchema": {
        "type": "object",
        "properties": {
          "incident_time": {"type": "string"},
          "affected_services": {"type": "array", "items": {"type": "string"}}
        }
      }
    },
    {
      "name": "coral_get_insights",
      "description": "Get AI-generated insights about system health and patterns",
      "inputSchema": {
        "type": "object",
        "properties": {
          "priority": {"type": "string", "enum": ["high", "medium", "low", "all"]}
        }
      }
    }
  ]
}
```

**Example Usage from Claude Desktop**:
```
User: "Why is the API slow right now?"

Claude Desktop:
  → Calls coral_query_events(service="api", time_range="2h")
  → Calls grafana MCP: query_metrics(service="api", metric="response_time")
  → Calls sentry MCP: query_errors(service="api", time_range="2h")
  → Synthesizes: "API deployed 1.5h ago, error rate up 3x, P95 latency increased 200ms"
```

### Architecture: Intelligence Orchestration

```
┌─────────────────────────────────────────────────────────────────┐
│                    CORAL COLONY                                  │
│              (Intelligence Orchestration Layer)                  │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │         AI Orchestrator (Claude/GPT)                      │  │
│  │                                                            │  │
│  │  Capabilities:                                            │  │
│  │  • Correlate events from Coral's agents                   │  │
│  │  • Query metrics via Grafana MCP                          │  │
│  │  • Fetch errors via Sentry MCP                            │  │
│  │  • Get incident context via PagerDuty MCP                 │  │
│  │  • Synthesize root cause across all sources               │  │
│  │  • Generate actionable recommendations                    │  │
│  └────┬──────────────────────────────────────┬───────────────┘  │
│       │                                      │                   │
│  ┌────▼──────────────┐           ┌──────────▼─────────────┐    │
│  │ Coral's Own Data  │           │  MCP Client Layer      │    │
│  │                   │           │                        │    │
│  │ • Service topology│           │  Connected to:         │    │
│  │ • Deploy events   │           │  • Grafana MCP         │    │
│  │ • Network graph   │           │  • Sentry MCP          │    │
│  │ • Agent health    │           │  • PagerDuty MCP       │    │
│  │ • Event history   │           │  • Custom MCPs         │    │
│  └───────────────────┘           └────────────────────────┘    │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │              MCP Server (Export Coral Data)              │  │
│  │  Exposes: topology, events, correlations, insights       │  │
│  └──────────────────────────────────────────────────────────┘  │
└──────────┬────────────────────────────────────────┬────────────┘
           │                                        │
      ┌────▼─────┐                           ┌─────▼──────┐
      │  Agents  │                           │  External  │
      │ (Observe)│                           │ AI Tools   │
      └──────────┘                           │ (Query)    │
                                             └────────────┘
```

### Example: Root Cause Analysis Workflow

When a user asks **"Why did the API crash?"**, Coral orchestrates multiple MCP servers:

```
1. Coral's Own Intelligence:
   ├─ Query local events: "api restarted 3x in last hour"
   ├─ Check topology: "api → database, api → cache, api → worker"
   └─ Find pattern: "All restarts happened 5-10 min after deploys"

2. Call Grafana MCP:
   ├─ Query: api_memory_usage (last 2h)
   ├─ Query: api_response_time_p95 (last 2h)
   └─ Result: "Memory spiked to 95% before each restart"

3. Call Sentry MCP:
   ├─ Query: errors in api (last 2h)
   └─ Result: "OutOfMemoryError, 47 occurrences"

4. Call PagerDuty MCP:
   ├─ Query: incidents for api (last 2h)
   └─ Result: "2 incidents auto-resolved after restart"

5. AI Synthesis:
   → Root Cause: "api v2.3.0 has memory leak"
   → Evidence: OOM errors + memory growth + restart pattern
   → Recommendation: "Rollback to v2.2.5" OR "Increase memory limit to 2GB"
```

### Why This Approach?

**Simpler**:
- Don't build Grafana integration - use their MCP server
- Don't build Sentry client - use their MCP server
- Focus on correlation and AI, not data plumbing

**Composable**:
- Users can add their own MCP servers
- Works with existing tools
- No lock-in to Coral's data model

**Interoperable**:
- Other AI assistants can query Coral
- Coral can query other systems
- Standard protocol (MCP) for everything

**Future-Proof**:
- New MCP servers added by community
- Coral focuses on core competency (intelligence)
- Ecosystem grows independently