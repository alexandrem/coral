# MCP Server Implementation (RFD 004)

This package implements the Model Context Protocol (MCP) server for Coral colonies, enabling AI assistants to query colony observability data and execute debugging actions.

## Overview

The MCP server exposes colony capabilities as standardized tools that can be consumed by:
- **Claude Desktop** - Anthropic's AI assistant
- **coral ask** - Local AI CLI (RFD 030)
- **Custom agents** - Using MCP client libraries

## Architecture

```
┌──────────────┐         ┌──────────────────┐
│ coral ask    │         │ Claude Desktop   │
│ (MCP client) │         │ (MCP client)     │
└──────┬───────┘         └────────┬─────────┘
       │                          │
       │  MCP Protocol (stdio)    │
       │                          │
       └──────────┬───────────────┘
                  │
                  ▼
         ┌────────────────┐
         │ Colony         │
         │ MCP Server     │
         │ (this package) │
         └────────┬───────┘
                  │
                  ▼
         ┌────────────────┐
         │ Colony DuckDB  │
         │ Agent Registry │
         └────────────────┘
```

## Features Implemented

### Phase 1: Core MCP Server Setup ✅
- [x] Genkit MCP plugin integration
- [x] Server initialization with stdio transport
- [x] Tool registration infrastructure
- [x] Configuration via colony.yaml

### Phase 2: Observability Tools ✅
- [x] `coral_get_service_health` - Service health and status
- [x] `coral_get_service_topology` - Service dependency graph
- [x] `coral_query_events` - Operational events (placeholder)
- [x] `coral_query_beyla_http_metrics` - HTTP RED metrics (placeholder)
- [x] `coral_query_beyla_grpc_metrics` - gRPC metrics (placeholder)
- [x] `coral_query_beyla_sql_metrics` - SQL metrics (placeholder)
- [x] `coral_query_beyla_traces` - Distributed traces (placeholder)
- [x] `coral_get_trace_by_id` - Specific trace retrieval (placeholder)
- [x] `coral_query_telemetry_spans` - OTLP spans (placeholder)
- [x] `coral_query_telemetry_metrics` - OTLP metrics (placeholder)
- [x] `coral_query_telemetry_logs` - OTLP logs (placeholder)

### Phase 3: Live Debugging Tools ⏳ (Planned)
- [ ] `coral_start_ebpf_collector` - Start eBPF profiling
- [ ] `coral_stop_ebpf_collector` - Stop eBPF profiling
- [ ] `coral_list_ebpf_collectors` - List active collectors
- [ ] `coral_query_ebpf_data` - Query eBPF data
- [ ] `coral_exec_command` - Execute commands in containers
- [ ] `coral_shell_start` - Start debug shell

### Phase 4: Analysis Tools ⏳ (Planned)
- [ ] `coral_correlate_events` - Event correlation
- [ ] `coral_compare_environments` - Cross-environment comparison
- [ ] `coral_get_deployment_timeline` - Deployment history

## CLI Commands

### List Tools
```bash
coral colony mcp list-tools
coral colony mcp list-tools --colony my-shop-production
```

### Test Tool
```bash
coral colony mcp test-tool coral_get_service_health
coral colony mcp test-tool coral_query_beyla_http_metrics \
  --args '{"service":"api","time_range":"1h"}'
```

### Generate Claude Desktop Config
```bash
coral colony mcp generate-config
coral colony mcp generate-config --all-colonies
```

### Proxy MCP Server (used by Claude Desktop)
```bash
coral colony proxy mcp
coral colony proxy mcp --colony my-shop-production
```

## Configuration

Add MCP configuration to `~/.coral/colonies/<colony-id>.yaml`:

```yaml
mcp:
  # Disable MCP server (default: false)
  disabled: false

  # Restrict to specific tools (default: all tools enabled)
  enabled_tools:
    - coral_get_service_health
    - coral_query_beyla_http_metrics

  # Security settings
  security:
    # Require RBAC for action tools (exec, shell, ebpf)
    require_rbac_for_actions: true

    # Audit all tool calls
    audit_enabled: true
```

## Claude Desktop Integration

1. Generate config:
```bash
coral colony mcp generate-config > /tmp/mcp-config.json
```

2. Add to `~/.config/claude/claude_desktop_config.json`:
```json
{
  "mcpServers": {
    "coral": {
      "command": "coral",
      "args": ["colony", "proxy", "mcp"]
    }
  }
}
```

3. Restart Claude Desktop

4. Ask Claude: "Is production healthy?"

Claude will automatically call `coral_get_service_health` and respond with real-time colony data.

## Implementation Notes

### Genkit MCP Integration
The server uses Firebase Genkit's MCP plugin (`github.com/firebase/genkit/go/plugins/mcp`) which provides:
- JSON-RPC 2.0 protocol handling
- Stdio transport
- Tool registration and discovery
- Automatic schema validation

### Tool Response Format
All tools return MCP-compliant responses:
```go
return map[string]interface{}{
    "content": []map[string]interface{}{
        {
            "type": "text",
            "text": "Human-readable response for LLM...",
        },
    },
}, nil
```

### Audit Logging
When `audit_enabled: true`, all tool invocations are logged:
```
{"level":"info","tool":"coral_get_service_health","args":{},"msg":"MCP tool called"}
```

## Testing

Run tests:
```bash
make test
```

## Future Work

See RFD 004 for complete implementation plan:
- Phase 3: Live debugging tools (eBPF, exec, shell)
- Phase 4: Analysis tools (correlation, comparison, timeline)
- Integration with RFD 025 (OTLP telemetry)
- Integration with RFD 032 (Beyla metrics)
- Integration with RFD 036 (distributed tracing)

## References

- RFD 004: MCP Server Integration
- RFD 030: Coral ask CLI (MCP client)
- [Genkit MCP Plugin](https://pkg.go.dev/github.com/firebase/genkit/go/plugins/mcp)
- [Model Context Protocol Spec](https://modelcontextprotocol.io/)
