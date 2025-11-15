---
rfd: "036"
title: "MCP Server for AI-Driven Diagnostics"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: ["030", "032", "035"]
database_migrations: []
areas: ["ai", "mcp", "observability"]
---

# RFD 036 - MCP Server for AI-Driven Diagnostics

**Status:** 🚧 Draft

## Summary

Implement a Model Context Protocol (MCP) server that exposes Coral's observability
data (Beyla RED metrics, OTLP traces, custom eBPF events) as tools for AI assistants.
This enables `coral ask` (RFD 030) and external AI systems (Claude Desktop, VSCode
Copilot) to query distributed system behavior and diagnose issues using natural
language.

## Problem

**Current behavior/limitations:**

- RFD 030 implements `coral ask` but lacks structured data access for AI agents
- Beyla metrics (RFD 032) are stored but not exposed to AI systems
- No standardized way for AI to query "Why is X slow?" using Coral's data
- External AI tools (Claude Desktop, VSCode) cannot leverage Coral's observability

**Why this matters:**

- AI-driven diagnostics is Coral's core value proposition
- `coral ask` needs structured access to metrics, traces, and events
- Developers using Claude Desktop should query production issues without switching
  tools
- MCP provides vendor-neutral protocol for AI-data integration

**Use cases affected:**

- "Why is payments-api slow?" - AI needs HTTP P95 latency from Beyla
- "Show me the trace for request abc123" - AI needs trace lookup capability
- "Which service is causing errors?" - AI needs error rate analysis
- "Compare performance before/after deployment" - AI needs historical queries

## Solution

Implement MCP server that exposes Coral observability data as MCP tools, following
the Model Context Protocol specification (https://modelcontextprotocol.io).

**Key Design Decisions:**

- **MCP protocol compliance**: Implement official MCP server spec for
  interoperability
- **Tool-based interface**: Each query type is an MCP tool (
  `coral_get_red_metrics`, `coral_query_trace`, etc.)
- **Standalone server mode**: Run as separate process or embedded in Colony
- **Auto-discovery**: MCP clients (Claude Desktop) discover server via config file
- **Security**: API keys or mTLS for authentication, rate limiting
- **Evidence export**: Return structured data that AI can reason about and show to
  users

**Benefits:**

- Enable `coral ask` to leverage full observability dataset
- External AI tools (Claude Desktop, VSCode) can query Coral
- Standardized protocol enables future AI integrations
- Structured tool interface guides AI to ask right questions

**Architecture Overview:**

```
Claude Desktop / VSCode Copilot
          ↓
    MCP Protocol (JSON-RPC over stdio/HTTP)
          ↓
    Coral MCP Server
          ↓
    ┌──────────────┬──────────────┬──────────────┐
    │ Tool:        │ Tool:        │ Tool:        │
    │ get_red_     │ query_trace  │ list_        │
    │ metrics      │              │ services     │
    └──────┬───────┴──────┬───────┴──────┬───────┘
           │              │              │
    Colony gRPC API (QueryBeylaMetrics, QueryTelemetry, etc.)
           ↓
    Colony DuckDB Storage
```

### Component Changes

1. **MCP Server Core** (`internal/mcp/server/`):
    - Implement MCP protocol handler (JSON-RPC 2.0)
    - Tool registration and discovery
    - Request routing to tool handlers
    - Error handling and validation
    - Authentication and rate limiting

2. **MCP Tools** (`internal/mcp/tools/`):
    - `coral_get_red_metrics` - Query HTTP/gRPC RED metrics from Beyla
    - `coral_query_trace` - Lookup distributed trace by ID
    - `coral_list_services` - List all monitored services
    - `coral_get_service_health` - Get error rates and latency for service
    - `coral_compare_metrics` - Compare metrics across time ranges
    - `coral_get_topology` - Retrieve service dependency graph

3. **Colony Integration** (`internal/colony/mcp/`):
    - Embed MCP server in Colony process
    - Expose Colony's gRPC APIs to MCP tools
    - Query result formatting for AI consumption
    - Evidence export (JSON data with context)

4. **Configuration** (`.coral/mcp-config.json`):
    - MCP server endpoint configuration
    - Authentication settings
    - Tool permissions and rate limits
    - Claude Desktop / VSCode discovery

**Configuration Example:**

```json
// ~/.config/claude-desktop/mcp-servers.json (Claude Desktop)
{
  "coral": {
    "command": "coral",
    "args": ["mcp", "serve"],
    "env": {
      "CORAL_COLONY": "localhost:9000",
      "CORAL_API_KEY": "${CORAL_API_KEY}"
    }
  }
}
```

```yaml
# colony-config.yaml (Coral Colony)
mcp:
  enabled: true
  bind: "localhost:3000"
  auth:
    type: api_key
    keys_file: /etc/coral/mcp-keys.json

  tools:
    coral_get_red_metrics:
      enabled: true
      rate_limit: 100/minute
    coral_query_trace:
      enabled: true
      rate_limit: 50/minute

  # Integration with coral ask (RFD 030)
  ask_integration:
    enabled: true
    auto_query: true
    triggers:
      - pattern: "slow|latency|performance"
        tools: ["coral_get_red_metrics"]
      - pattern: "error|failing|5xx"
        tools: ["coral_get_red_metrics", "coral_query_trace"]
```

## Implementation Plan

### Phase 1: MCP Protocol Foundation

- [ ] Implement MCP JSON-RPC 2.0 server
- [ ] Handle tool discovery and registration
- [ ] Implement request validation and routing
- [ ] Add authentication (API keys)
- [ ] Create test client for validation

### Phase 2: Core MCP Tools

- [ ] Implement `coral_get_red_metrics` tool
  - Query Beyla HTTP metrics via QueryBeylaMetrics RPC
  - Format results as structured JSON for AI
  - Include percentiles, error rates, request counts
- [ ] Implement `coral_list_services` tool
  - Query all registered services from Colony
  - Return service names, health status, last seen
- [ ] Implement `coral_query_trace` tool
  - Lookup trace by ID from QueryTelemetry RPC
  - Build span tree and return structured trace data

### Phase 3: Integration with coral ask

- [ ] Integrate MCP tools with `coral ask` (RFD 030)
- [ ] Auto-query MCP tools based on user question patterns
- [ ] Format MCP tool responses for natural language presentation
- [ ] Add evidence links to original data

### Phase 4: External AI Integration

- [ ] Create `coral mcp serve` CLI command
- [ ] Support stdio transport for Claude Desktop
- [ ] Support HTTP transport for web-based AI
- [ ] Documentation for Claude Desktop setup
- [ ] Documentation for VSCode Copilot integration

### Phase 5: Advanced Tools

- [ ] Implement `coral_compare_metrics` tool (before/after comparison)
- [ ] Implement `coral_get_topology` tool (service dependency graph)
- [ ] Implement `coral_analyze_anomalies` tool (ML-based)
- [ ] Add query result caching for performance

## Tool Specifications

### coral_get_red_metrics

**Description:** Retrieve RED (Rate, Errors, Duration) metrics for a service.

**Input Schema:**
```json
{
  "service_name": "payments-api",
  "metric_type": "http|grpc|sql",
  "time_range": {
    "start": "2025-11-15T10:00:00Z",
    "end": "2025-11-15T11:00:00Z"
  },
  "filters": {
    "route": "/api/v1/payments",
    "status_code": "5xx"
  }
}
```

**Output Schema:**
```json
{
  "service_name": "payments-api",
  "metric_type": "http",
  "time_range": { ... },
  "summary": {
    "total_requests": 45200,
    "error_rate": 0.023,
    "latency_p50_ms": 45,
    "latency_p95_ms": 180,
    "latency_p99_ms": 420
  },
  "by_route": [
    {
      "route": "/api/v1/payments",
      "requests": 30000,
      "error_rate": 0.025,
      "latency_p95_ms": 200
    }
  ],
  "evidence_url": "http://colony:9000/evidence/beyla-http-payments-api-2025-11-15.json"
}
```

### coral_query_trace

**Description:** Lookup a distributed trace by trace ID.

**Input Schema:**
```json
{
  "trace_id": "abc123def456789",
  "include_spans": true
}
```

**Output Schema:**
```json
{
  "trace_id": "abc123def456789",
  "duration_ms": 1200,
  "span_count": 8,
  "services": ["frontend", "payments-api", "card-validator"],
  "root_span": {
    "span_id": "span-1",
    "service": "frontend",
    "operation": "POST /checkout",
    "duration_ms": 1200,
    "children": [ ... ]
  },
  "evidence_url": "http://colony:9000/evidence/trace-abc123def456789.json"
}
```

## Testing Strategy

**Unit Tests:**

- MCP JSON-RPC protocol compliance
- Tool registration and discovery
- Request validation and error handling
- Authentication and rate limiting

**Integration Tests:**

- End-to-end MCP tool invocation
- Tool output schema validation
- Integration with `coral ask`
- Multi-tool query orchestration

**Manual Testing:**

- Claude Desktop integration (install MCP server, test queries)
- VSCode Copilot integration
- Test AI-driven diagnostics: "Why is X slow?"
- Verify evidence export and formatting

## Security Considerations

- API key authentication for MCP clients
- Rate limiting per tool and per client
- Input validation and sanitization
- Audit logging of MCP tool invocations
- Scope-based permissions (read-only vs read-write tools)

## Future Work

- MCP server clustering for high availability
- Tool result streaming for large datasets
- Custom tool plugins via extension API
- Integration with Prometheus, Grafana as MCP tools
- Multi-colony tool federation

## Dependencies

- **RFD 030**: `coral ask` implementation
- **RFD 032**: Beyla metrics and QueryBeylaMetrics RPC
- **RFD 035**: CLI query framework (shared query logic)
- Colony storage and query APIs

## References

- Model Context Protocol Specification: https://modelcontextprotocol.io
- Claude Desktop MCP Integration: https://docs.anthropic.com/claude/docs/mcp
- MCP Server SDK (Go): https://github.com/modelcontextprotocol/sdk-go
