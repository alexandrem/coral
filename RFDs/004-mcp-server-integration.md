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

Enable Coral colonies to expose their comprehensive observability and debugging
capabilities as Model Context Protocol (MCP) servers, allowing any
MCP-compatible client (Claude Desktop, `coral ask`, custom agents) to query
distributed system state and execute live debugging actions. Colony MCP server
exposes data access tools (Beyla RED metrics, distributed traces, OTLP
telemetry, service topology, events) and action tools (eBPF profiling,
container exec, agent debug shells). This transforms Coral into a universal
context provider for AI-powered operations where external LLMs orchestrate
observability queries and live debugging workflows.

> **Architectural Note**: Colony acts as a secure MCP gateway with NO embedded
> LLM—it exposes tool-calling interfaces for data access and live debugging
> actions. External LLMs consume these tools:
> - **Claude Desktop**: User's AI assistant via Anthropic's hosted LLM
> - **`coral ask` (RFD 030)**: Local Genkit agent running on developer's machine
> - **Reef (future)**: Will add server-side LLM for cross-colony analysis
>
> This design offloads LLM compute from Colony, enables flexible model choice
> (local Ollama, cloud OpenAI/Anthropic), and maintains cost control at the
> developer level. The MCP tool interface defined here will serve as the
> foundation for future Reef integration (RFD 003).

## Problem

**Current behavior/limitations:**

- Colony infrastructure data (OTEL metrics/traces, Beyla RED metrics, eBPF
  profiling, service topology, deployment events) is only accessible via
  direct Colony RPC calls
- External LLM tools (Claude Desktop, custom agents) cannot access Coral's
  operational intelligence without custom integration
- No standardized protocol for AI assistants to query distributed system state
- `coral ask` command (RFD 030) will need to implement custom Colony RPC client
  instead of using standard MCP protocol
- Each new capability requires custom integration work for every LLM client
- Coral's rich observability data is isolated, not composable with other tools

**Why this matters:**

- **LLM integration is outside Colony**: Colony acts as a secure MCP gateway - it
  does NOT host embedded LLMs. External LLMs (Claude Desktop, `coral ask` with
  local Genkit) consume Colony data via MCP tools.
- **Main interface is MCP server**: The primary integration point is Colony's
  MCP server exposing tool-calling interfaces. `coral ask` CLI (RFD 030) and
  Claude Desktop are both MCP clients consuming these tools.
- **Developer workflow**: Developers already use Claude Desktop or other AI
  assistants for coding - they should be able to query production health
  without context-switching to terminal
- **Composability**: Want to combine Coral observability data with other MCP
  servers (GitHub, Sentry, Grafana) in a single LLM conversation
- **Universal access**: MCP is a standard protocol - any MCP-compatible client
  should be able to access Coral data
- **Custom automation**: Teams want to build custom agents that query Coral
  programmatically using standard tool-calling interfaces

**Use cases affected:**

- Developer in Claude Desktop wants to check production health before deploying
- SRE using Claude Desktop for incident investigation needs RED metrics,
  traces, and live eBPF profiling
- `coral ask` CLI (RFD 030) needs to query Colony data via MCP instead of
  custom RPC
- Custom automation scripts querying Coral data via standard MCP client
  libraries
- AI-powered runbooks that execute live debugging commands (shell, exec, eBPF
  probes)
- Multi-tool workflows combining Coral + Grafana + Sentry via MCP

**Example friction today:**

```
Developer in Claude Desktop:
"Should I deploy PR #123 to production?"

Claude: "I don't have access to your production metrics. You'll need to
check your monitoring system separately."

Developer switches to terminal:
$ coral ask "is production healthy?"
> [Hypothetically works, but requires custom RPC implementation in RFD 030]

Developer switches back to Claude Desktop:
"Production is healthy according to Coral"

Claude: "Okay, based on what you said, it should be safe to deploy."
```

**With MCP Server (this RFD):**

```
Developer in Claude Desktop (with Coral MCP server configured):
"Should I deploy PR #123 to production?"

Claude: [Queries coral-prod MCP server automatically]
  → Calls coral_get_service_health()
  → Calls coral_query_beyla_http_metrics(service=api, since=1h)
  → Calls coral_query_events(event_type=deploy, since=24h)

"Based on your production metrics:
- All services healthy ✓
- API P95 latency: 145ms (normal range)
- Error rate: 0.2% (baseline)
- No errors in last hour ✓
- Last deploy was 2 days ago
- No active incidents ✓

You should be safe to deploy. Would you like me to check the deployment
pipeline status via GitHub MCP?"
```

**Additional benefit - `coral ask` also uses MCP:**

```
$ coral ask "is production healthy?"

[coral ask spawns local Genkit LLM that connects to Colony MCP server]
→ Same MCP tools as Claude Desktop
→ Consistent behavior across all LLM clients
→ No duplicate RPC implementation needed
```

## Solution

Implement MCP server interface in Coral colonies, exposing operational data
through standardized MCP tools. This design will serve as the foundation for
future Reef MCP server implementation (RFD 003).

**Key Design Decisions:**

- **Colony MCP server**: Single-colony observability and debugging
    - Provides complete access to one colony's data and actions
    - Example: my-shop-prod colony exposes all production observability

- **Standard MCP protocol**: Use official MCP specification (JSON-RPC 2.0 over
  stdio)
    - Enables any MCP client to connect (Claude Desktop, custom tools)
    - Follows same patterns as existing MCP servers (GitHub, Sentry, Grafana)
    - No custom protocol needed
    - stdio transport is sufficient for all current use cases

- **Tool-based interface**: Expose Coral capabilities as MCP tools
    - **Observability Query Tools** (read-only data access):
        - `coral_query_beyla_http_metrics`: Query HTTP RED metrics (latency,
          error rate, request rate)
        - `coral_query_beyla_grpc_metrics`: Query gRPC method-level metrics
        - `coral_query_beyla_sql_metrics`: Query SQL operation metrics
        - `coral_query_beyla_traces`: Query distributed traces by ID, service, or
          time range
        - `coral_get_trace_by_id`: Get specific trace with full span tree
        - `coral_query_telemetry_spans`: Query generic OTLP spans
        - `coral_query_telemetry_metrics`: Query generic OTLP metrics
        - `coral_query_telemetry_logs`: Query generic OTLP logs
        - `coral_query_ebpf_data`: Query custom eBPF collector data (CPU
          profiles, syscall stats)
        - `coral_get_service_health`: Get current health status of services
        - `coral_get_service_topology`: Get service dependency graph
        - `coral_query_events`: Query deployment events, restarts, crashes,
          alerts
    - **Live Debugging Tools** (action-oriented, can modify state):
        - `coral_start_ebpf_collector`: Start on-demand eBPF collector (CPU
          profiling, HTTP latency, TCP metrics)
        - `coral_stop_ebpf_collector`: Stop running eBPF collector
        - `coral_list_ebpf_collectors`: List active eBPF collectors
        - `coral_exec_command`: Execute command in application container (via
          CRI)
        - `coral_shell_start`: Start interactive debug shell in agent environment
    - **Correlation & Analysis Tools**:
        - `coral_correlate_events`: Correlate events across services to identify
          patterns
        - `coral_compare_environments`: Compare metrics/traces across
          environments (prod vs staging)
        - `coral_get_deployment_timeline`: Get deployment timeline across
          environments
    - **Future: Reef Tools** (deferred to RFD 003):
        - Cross-colony analysis and correlation
        - Server-side LLM integration for global insights

- **Local-first**: MCP server runs locally, queries local colony
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
              │ MCP Protocol (stdio)
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

1. **Colony** (MCP server integrated):
    - MCP server starts automatically with colony (enabled by default)
    - Implements MCP protocol (JSON-RPC 2.0 over stdio)
    - Exposes data access and action tools via MCP interface
    - Queries local DuckDB to fulfill tool requests
    - Configuration in `colony.yaml` to control MCP server behavior

2. **CLI** (MCP helpers):
    - `coral colony mcp list-tools` - Show available MCP tools for running colony
    - `coral colony mcp test-tool <tool-name>` - Test MCP tool locally
    - `coral colony mcp generate-config` - Generate Claude Desktop config snippet

3. **MCP Client Library** (optional):
    - Go library for building custom MCP clients
    - Query Coral programmatically from Go applications
    - Used by custom automation scripts

**Configuration Example:**

**Colony config** (`colony.yaml`):

```yaml
# Colony configuration
id: my-shop-production
name: "My Shop Production"

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
    # Require RBAC checks for action tools (exec, shell, start_ebpf)
    require_rbac_for_actions: true

    # Audit all MCP tool calls
    audit_enabled: true
```

**Claude Desktop config** (`~/.config/claude/claude_desktop_config.json`):

**Single colony (uses default from coral config):**

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

**Multiple colonies (specify each explicitly):**

```json
{
    "mcpServers": {
        "coral-prod": {
            "command": "coral",
            "args": [
                "colony",
                "proxy",
                "mcp",
                "--colony",
                "my-shop-production"
            ]
        },
        "coral-staging": {
            "command": "coral",
            "args": [
                "colony",
                "proxy",
                "mcp",
                "--colony",
                "my-shop-staging"
            ]
        }
    }
}
```

> **Note**: `coral colony proxy mcp` connects to a running colony's MCP server via its
> stdio interface. The colony must be running with MCP enabled. If `--colony` is
> omitted, it uses the default colony from coral's configuration.

## API Changes

### MCP Tools Definition

MCP tools are defined using JSON Schema. Coral exposes these tools:

**Colony MCP Tools:**

```json
{
    "tools": [
        {
            "name": "coral_query_beyla_http_metrics",
            "description": "Query HTTP RED metrics collected by Beyla (request rate, error rate, latency distributions). Returns percentiles, status code breakdown, and route-level metrics.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "service": {
                        "type": "string",
                        "description": "Service name (required)"
                    },
                    "time_range": {
                        "type": "string",
                        "description": "Time range (e.g., '1h', '30m', '24h')",
                        "default": "1h"
                    },
                    "http_route": {
                        "type": "string",
                        "description": "Optional: Filter by HTTP route pattern (e.g., '/api/v1/users/:id')"
                    },
                    "http_method": {
                        "type": "string",
                        "description": "Optional: Filter by HTTP method",
                        "enum": ["GET", "POST", "PUT", "DELETE", "PATCH"]
                    },
                    "status_code_range": {
                        "type": "string",
                        "description": "Optional: Filter by status code range",
                        "enum": ["2xx", "3xx", "4xx", "5xx"]
                    }
                },
                "required": ["service"]
            }
        },
        {
            "name": "coral_query_beyla_grpc_metrics",
            "description": "Query gRPC method-level RED metrics collected by Beyla. Returns RPC rate, latency distributions, and status code breakdown.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "service": {
                        "type": "string",
                        "description": "Service name (required)"
                    },
                    "time_range": {
                        "type": "string",
                        "description": "Time range (e.g., '1h', '30m', '24h')",
                        "default": "1h"
                    },
                    "grpc_method": {
                        "type": "string",
                        "description": "Optional: Filter by gRPC method (e.g., '/payments.PaymentService/Charge')"
                    },
                    "status_code": {
                        "type": "integer",
                        "description": "Optional: Filter by gRPC status code (0=OK, 1=CANCELLED, etc.)"
                    }
                },
                "required": ["service"]
            }
        },
        {
            "name": "coral_query_beyla_sql_metrics",
            "description": "Query SQL operation metrics collected by Beyla. Returns query latencies, operation types, and table-level statistics.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "service": {
                        "type": "string",
                        "description": "Service name (required)"
                    },
                    "time_range": {
                        "type": "string",
                        "description": "Time range (e.g., '1h', '30m', '24h')",
                        "default": "1h"
                    },
                    "sql_operation": {
                        "type": "string",
                        "description": "Optional: Filter by SQL operation",
                        "enum": ["SELECT", "INSERT", "UPDATE", "DELETE"]
                    },
                    "table_name": {
                        "type": "string",
                        "description": "Optional: Filter by table name"
                    }
                },
                "required": ["service"]
            }
        },
        {
            "name": "coral_query_beyla_traces",
            "description": "Query distributed traces collected by Beyla. Can search by trace ID, service, time range, or duration threshold.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "trace_id": {
                        "type": "string",
                        "description": "Specific trace ID (32-char hex string)"
                    },
                    "service": {
                        "type": "string",
                        "description": "Filter traces involving this service"
                    },
                    "time_range": {
                        "type": "string",
                        "description": "Time range (e.g., '1h', '30m', '24h')",
                        "default": "1h"
                    },
                    "min_duration_ms": {
                        "type": "integer",
                        "description": "Optional: Only return traces longer than this duration (milliseconds)"
                    },
                    "max_traces": {
                        "type": "integer",
                        "description": "Maximum number of traces to return",
                        "default": 10
                    }
                }
            }
        },
        {
            "name": "coral_get_trace_by_id",
            "description": "Get a specific distributed trace by ID with full span tree showing parent-child relationships and timing.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "trace_id": {
                        "type": "string",
                        "description": "Trace ID (32-char hex string)"
                    },
                    "format": {
                        "type": "string",
                        "description": "Output format",
                        "enum": ["tree", "flat", "json"],
                        "default": "tree"
                    }
                },
                "required": ["trace_id"]
            }
        },
        {
            "name": "coral_query_telemetry_spans",
            "description": "Query generic OTLP spans (from instrumented applications using OpenTelemetry SDKs). Complementary to Beyla traces.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "service": {
                        "type": "string",
                        "description": "Service name"
                    },
                    "time_range": {
                        "type": "string",
                        "description": "Time range (e.g., '1h', '30m', '24h')",
                        "default": "1h"
                    },
                    "operation": {
                        "type": "string",
                        "description": "Optional: Filter by operation name"
                    }
                },
                "required": ["service"]
            }
        },
        {
            "name": "coral_query_telemetry_metrics",
            "description": "Query generic OTLP metrics (from instrumented applications). Returns time-series data for custom application metrics.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "metric_name": {
                        "type": "string",
                        "description": "Metric name (e.g., 'http.server.duration', 'custom.orders.count')"
                    },
                    "service": {
                        "type": "string",
                        "description": "Optional: Filter by service"
                    },
                    "time_range": {
                        "type": "string",
                        "description": "Time range",
                        "default": "1h"
                    }
                }
            }
        },
        {
            "name": "coral_query_telemetry_logs",
            "description": "Query generic OTLP logs (from instrumented applications). Search application logs with full-text search and filters.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "query": {
                        "type": "string",
                        "description": "Search query (full-text search)"
                    },
                    "service": {
                        "type": "string",
                        "description": "Optional: Filter by service"
                    },
                    "level": {
                        "type": "string",
                        "description": "Optional: Filter by log level",
                        "enum": ["DEBUG", "INFO", "WARN", "ERROR", "FATAL"]
                    },
                    "time_range": {
                        "type": "string",
                        "description": "Time range",
                        "default": "1h"
                    }
                }
            }
        },
        {
            "name": "coral_query_ebpf_data",
            "description": "Query data from custom eBPF collectors (CPU profiles, syscall stats, network flows). Requires collector to be running.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "collector_type": {
                        "type": "string",
                        "description": "Type of eBPF collector",
                        "enum": ["cpu_profile", "syscall_stats", "http_latency", "tcp_metrics"]
                    },
                    "service": {
                        "type": "string",
                        "description": "Service name"
                    },
                    "time_range": {
                        "type": "string",
                        "description": "Time range",
                        "default": "5m"
                    }
                },
                "required": ["collector_type", "service"]
            }
        },
        {
            "name": "coral_get_service_health",
            "description": "Get current health status of services. Returns health state, resource usage (CPU, memory), uptime, and recent issues.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "service_filter": {
                        "type": "string",
                        "description": "Optional: Filter by service name pattern (e.g., 'api*', 'payment*')"
                    }
                }
            }
        },
        {
            "name": "coral_get_service_topology",
            "description": "Get service dependency graph discovered from distributed traces. Shows which services communicate and call frequency.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "filter": {
                        "type": "string",
                        "description": "Optional: Filter by service name, tag, or region"
                    },
                    "format": {
                        "type": "string",
                        "description": "Output format",
                        "enum": ["graph", "list", "json"],
                        "default": "graph"
                    }
                }
            }
        },
        {
            "name": "coral_query_events",
            "description": "Query operational events tracked by Coral (deployments, restarts, crashes, alerts, configuration changes).",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "event_type": {
                        "type": "string",
                        "description": "Event type filter",
                        "enum": ["deploy", "restart", "crash", "alert", "config_change", "connection", "error_spike"]
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
            "name": "coral_start_ebpf_collector",
            "description": "Start an on-demand eBPF collector for live debugging (CPU profiling, syscall tracing, network analysis). Collector runs for specified duration.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "collector_type": {
                        "type": "string",
                        "description": "Type of eBPF collector to start",
                        "enum": ["cpu_profile", "syscall_stats", "http_latency", "tcp_metrics"]
                    },
                    "service": {
                        "type": "string",
                        "description": "Target service name"
                    },
                    "duration_seconds": {
                        "type": "integer",
                        "description": "How long to run collector (max 300s)",
                        "default": 30
                    },
                    "config": {
                        "type": "object",
                        "description": "Optional collector-specific configuration (sample rate, filters, etc.)"
                    }
                },
                "required": ["collector_type", "service"]
            }
        },
        {
            "name": "coral_stop_ebpf_collector",
            "description": "Stop a running eBPF collector before its duration expires.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "collector_id": {
                        "type": "string",
                        "description": "Collector ID returned from start_ebpf_collector"
                    }
                },
                "required": ["collector_id"]
            }
        },
        {
            "name": "coral_list_ebpf_collectors",
            "description": "List currently active eBPF collectors with their status and remaining duration.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "service": {
                        "type": "string",
                        "description": "Optional: Filter by service"
                    }
                }
            }
        },
        {
            "name": "coral_exec_command",
            "description": "Execute a command in an application container (kubectl/docker exec semantics). Useful for checking configuration, running diagnostic commands, or inspecting container state.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "service": {
                        "type": "string",
                        "description": "Target service name"
                    },
                    "command": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "Command and arguments to execute (e.g., ['ls', '-la', '/app'])"
                    },
                    "timeout_seconds": {
                        "type": "integer",
                        "description": "Command timeout",
                        "default": 30
                    },
                    "working_dir": {
                        "type": "string",
                        "description": "Optional: Working directory"
                    }
                },
                "required": ["service", "command"]
            }
        },
        {
            "name": "coral_shell_start",
            "description": "Start an interactive debug shell in the agent's environment (not the application container). Provides access to debugging tools (tcpdump, netcat, curl) and agent's data. Returns session ID for audit.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "service": {
                        "type": "string",
                        "description": "Service whose agent to connect to"
                    },
                    "shell": {
                        "type": "string",
                        "description": "Shell to use",
                        "enum": ["/bin/bash", "/bin/sh"],
                        "default": "/bin/bash"
                    }
                },
                "required": ["service"]
            }
        },
        {
            "name": "coral_correlate_events",
            "description": "Correlate events across services to identify causal patterns (e.g., 'deploy → error spike', 'restart → latency increase').",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "incident_time": {
                        "type": "string",
                        "description": "Timestamp of incident to investigate (ISO 8601 or relative like '1h ago')"
                    },
                    "affected_services": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "Optional: List of affected services to focus correlation"
                    },
                    "correlation_window": {
                        "type": "string",
                        "description": "Time window to search for correlated events",
                        "default": "15m"
                    }
                },
                "required": ["incident_time"]
            }
        },
        {
            "name": "coral_compare_environments",
            "description": "Compare metrics or traces across environments (production vs staging). Useful for identifying configuration drift or deployment issues.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "metric_type": {
                        "type": "string",
                        "description": "Type of data to compare",
                        "enum": ["http_latency", "error_rate", "throughput", "resource_usage"]
                    },
                    "service": {
                        "type": "string",
                        "description": "Service name (must exist in both environments)"
                    },
                    "environments": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "Environments to compare (defaults to ['production', 'staging'])",
                        "default": ["production", "staging"]
                    },
                    "time_range": {
                        "type": "string",
                        "description": "Time range for comparison",
                        "default": "1h"
                    }
                },
                "required": ["metric_type", "service"]
            }
        },
        {
            "name": "coral_get_deployment_timeline",
            "description": "Get deployment timeline across all environments. Shows deployment sequence, version changes, and rollback events.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "application": {
                        "type": "string",
                        "description": "Optional: Filter by application name"
                    },
                    "time_range": {
                        "type": "string",
                        "description": "Time range",
                        "default": "7d"
                    }
                }
            }
        }
    ]
}
```

**Future: Reef MCP Tools (Out of Scope)**

> **Note**: Reef implementation (RFD 003) is out of scope for this RFD. The MCP
> tool interface design established here will serve as the foundation for future
> Reef MCP server, which will add:
> - Cross-colony analysis and correlation tools
> - Server-side LLM integration for AI-powered insights
> - Unified view across multiple environments
>
> Reef tool definitions will be specified in RFD 003.

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
# List available MCP tools from running colony
coral colony mcp list-tools [flags]
  --colony <colony-id>    # Which colony to query (optional, uses default if omitted)

# Example output (using default colony):
$ coral colony mcp list-tools

Available MCP Tools for colony my-shop-production:
...

# Example output (specific colony):
$ coral colony mcp list-tools --colony my-shop-staging

Available MCP Tools for colony my-shop-production:

coral_query_beyla_http_metrics
  Query HTTP RED metrics collected by Beyla
  Required: service

coral_get_service_health
  Get current health status of services

coral_query_events
  Query operational events (deployments, restarts, crashes, alerts)

coral_start_ebpf_collector
  Start on-demand eBPF collector for live debugging
  Required: collector_type, service

... (24 total tools)

---

# Test MCP tool locally (without MCP client)
coral colony mcp test-tool <tool-name> [flags]
  --colony <colony-id>    # Optional, uses default if omitted
  --args <json>           # Tool arguments as JSON

# Example (using default colony):
$ coral colony mcp test-tool coral_get_service_health --args '{}'

# Example (specific colony):
$ coral colony mcp test-tool coral_get_service_health \
  --colony my-shop-production \
  --args '{}'

Calling tool: coral_get_service_health
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
coral colony mcp generate-config [flags]
  --colony <colony-id>    # Include this colony
  --all-colonies          # Include all configured colonies

# Example 1: Default colony only
$ coral colony mcp generate-config

Copy this to ~/.config/claude/claude_desktop_config.json:

{
  "mcpServers": {
    "coral": {
      "command": "coral",
      "args": ["colony", "proxy", "mcp"]
    }
  }
}

# Example 2: All colonies (explicit)
$ coral colony mcp generate-config --all-colonies

Copy this to ~/.config/claude/claude_desktop_config.json:

{
  "mcpServers": {
    "coral-my-shop-production": {
      "command": "coral",
      "args": ["colony", "proxy", "mcp", "--colony", "my-shop-production"]
    },
    "coral-my-shop-staging": {
      "command": "coral",
      "args": ["colony", "proxy", "mcp", "--colony", "my-shop-staging"]
    }
  }
}

After adding this config, restart Claude Desktop to enable Coral MCP servers.

---

# Connect to colony MCP server (used by Claude Desktop)
coral colony proxy mcp [--colony <colony-id>]

# This command:
# 1. Connects to running colony's MCP server
# 2. Proxies stdio to/from the colony's MCP interface
# 3. Used by Claude Desktop as MCP server command
# 4. If --colony is omitted, uses default colony from coral config

# Examples:
# Use default colony
coral colony proxy mcp

# Use specific colony
coral colony proxy mcp --colony my-shop-production
```

### Environment Variable Configuration

For colony MCP server configuration:

```bash
# Use custom config location
export CORAL_CONFIG_HOME=~/custom/.coral

# Disable MCP server (overrides config file)
export CORAL_MCP_DISABLED=true
```

## Implementation Plan

### Phase 1: Core MCP Protocol

- [ ] Implement MCP protocol handler (JSON-RPC 2.0)
- [ ] Implement stdio transport (for Claude Desktop via `coral colony proxy mcp`)
- [ ] Handle tool discovery (list_tools method)
- [ ] Handle tool execution (tools/call method)
- [ ] Integrate MCP server into colony startup (enabled by default)

### Phase 2: Colony MCP Tools - Observability

- [ ] Implement Beyla metrics tools: `coral_query_beyla_{http,grpc,sql}_metrics`
- [ ] Implement trace tools: `coral_query_beyla_traces`, `coral_get_trace_by_id`
- [ ] Implement OTLP tools: `coral_query_telemetry_{spans,metrics,logs}`
- [ ] Implement `coral_get_service_health` tool
- [ ] Implement `coral_get_service_topology` tool
- [ ] Implement `coral_query_events` tool

### Phase 3: Colony MCP Tools - Live Debugging

- [ ] Implement eBPF tools: `coral_{start,stop,list}_ebpf_collector`, `coral_query_ebpf_data`
- [ ] Implement `coral_exec_command` tool (requires RFD 017)
- [ ] Implement `coral_shell_start` tool (requires RFD 026)

### Phase 4: Colony MCP Tools - Analysis

- [ ] Implement `coral_correlate_events` tool
- [ ] Implement `coral_compare_environments` tool
- [ ] Implement `coral_get_deployment_timeline` tool

### Phase 5: CLI & Configuration

- [ ] Implement colony configuration (`mcp` section in `colony.yaml`)
- [ ] Implement `coral colony proxy mcp` command (connects to colony MCP)
- [ ] Implement `coral colony mcp list-tools` command
- [ ] Implement `coral colony mcp test-tool` command
- [ ] Implement `coral colony mcp generate-config` command

### Phase 6: Testing & Documentation

- [ ] Unit tests for MCP protocol handling
- [ ] Integration tests with MCP client library
- [ ] E2E test with Claude Desktop via `coral colony proxy mcp`
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
- stdio transport layer

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

**Approach:**

- Stdio transport inherits user's OS permissions
- Only user who can run `coral` command can access MCP server
- No network exposure (stdio is local-only)
- Transport is secure by design (requires local shell access)

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

- MCP tools have read-only access (observability tools)
- Action tools (exec, shell, eBPF) require RBAC checks
- User controls which colonies are exposed via config

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

### SSE Transport (Out of Scope)

> **Note**: Server-Sent Events (SSE) transport over HTTP is out of scope for this
> RFD. The stdio transport is sufficient for all current use cases (Claude
> Desktop, `coral ask`, custom local clients).
>
> If HTTP-based MCP access is needed in the future (e.g., web dashboard, remote
> clients), a separate RFD will detail SSE transport implementation including:
> - HTTP server with SSE endpoint
> - Authentication and authorization
> - Rate limiting and security controls
> - CORS and network access policies

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

- **RFD 001**: Discovery service (MCP server uses discovery for service
  resolution)
- **RFD 002**: Application identity (MCP server uses colony config for service
  targeting)
- **RFD 003**: Reef federation (Reef exposes MCP server with AI-powered
  analysis tools via server-side LLM)
- **RFD 013**: eBPF introspection (MCP exposes `coral_start_ebpf_collector`,
  `coral_query_ebpf_data` tools)
- **RFD 014**: Abandoned (Colony-embedded LLM approach replaced; Colony is now
  MCP gateway only)
- **RFD 017**: Exec command (MCP exposes `coral_exec_command` tool for
  container access)
- **RFD 025**: OTLP ingestion (MCP exposes `coral_query_telemetry_*` tools for
  OTLP data)
- **RFD 026**: Shell command (MCP exposes `coral_shell_start` tool for agent
  debug access)
- **RFD 030**: Coral ask CLI (local Genkit agent is primary consumer of
  Colony/Reef MCP tools)
- **RFD 032**: Beyla RED metrics (MCP exposes
  `coral_query_beyla_{http,grpc,sql}_metrics` tools)
- **RFD 035**: CLI query framework (CLI commands can also be MCP tool wrappers)
- **RFD 036**: Beyla distributed tracing (MCP exposes `coral_query_beyla_traces`,
  `coral_get_trace_by_id` tools)

**LLM Architecture Integration:**

- **Colony MCP Server** (this RFD): Exposes data access and action tools (query
  metrics, start probes, exec commands) - NO embedded LLM
    - Colony acts as secure MCP gateway with RBAC/audit enforcement
    - Issues delegate JWTs for direct agent connections when needed (live
      probes, shell sessions)
- **External LLM Clients** (MCP consumers):
    - **Claude Desktop**: User's AI assistant queries Coral MCP for production
      insights
    - **`coral ask` (RFD 030)**: Local Genkit LLM running on developer's
      machine
    - **Custom agents**: Teams build automation using MCP client libraries
- **Future: Reef MCP Server** (RFD 003, out of scope for this RFD):
    - Will extend Colony tools with AI-powered cross-colony analysis
    - Server-side LLM for global correlation and RCA
    - This RFD's tool interface design will serve as foundation

**Key Capabilities Exposed via MCP:**

1. **Observability Queries**:
    - Beyla RED metrics (HTTP/gRPC/SQL) from RFD 032
    - Distributed traces from RFD 036
    - Generic OTLP data (spans/metrics/logs) from RFD 025
    - Custom eBPF data (CPU profiles, syscall stats) from RFD 013

2. **Live Debugging Actions**:
    - Start/stop eBPF collectors (on-demand profiling) from RFD 013
    - Execute commands in containers (`exec`) from RFD 017
    - Open debug shells in agents (`shell`) from RFD 026

3. **Topology & Events**:
    - Service dependency graphs from distributed traces
    - Deployment events, crashes, restarts
    - Health status and resource usage

4. **Correlation & Analysis**:
    - Event correlation across services
    - Environment comparisons (prod vs staging)
    - Deployment timelines

**Why this is powerful:**

Coral becomes a **universal context provider** for AI-powered operations:

1. Developer uses Claude Desktop for coding
2. Claude: "Let me check production health before you deploy"
   → Calls `coral_get_service_health()`
   → Calls `coral_query_beyla_http_metrics(service=api, since=1h)`
   → Calls `coral_query_events(event_type=deploy, since=24h)`
3. Claude: "API P95 latency is 145ms (normal), no errors in last hour, safe to
   deploy"
4. Developer deploys with confidence, guided by real production state

**Example AI-orchestrated debugging workflow:**

```
User in Claude Desktop: "Why is checkout slow?"

Claude: [Orchestrates multiple MCP tools automatically]
→ coral_query_beyla_http_metrics(service=checkout, since=1h)
   Result: P95 latency 850ms (baseline: 200ms)

→ coral_query_beyla_traces(service=checkout, min_duration_ms=500, max_traces=5)
   Result: 80% of slow traces wait for payment-api

→ coral_query_beyla_http_metrics(service=payment-api, since=1h)
   Result: payment-api P95 is 700ms (baseline: 150ms)

→ coral_start_ebpf_collector(collector_type=cpu_profile, service=payment-api, duration_seconds=30)
   [Waits 30 seconds]

→ coral_query_ebpf_data(collector_type=cpu_profile, service=payment-api)
   Result: 65% CPU time in validateCard() function

Claude responds: "Checkout is slow because payment-api is slow. CPU profiling
shows 65% of time spent in validateCard(). Recommend investigating the card
validation logic or external payment gateway latency."
```

**Comparison with `coral ask` vs Claude Desktop:**

Both use the same MCP tools, but different LLM hosting:

- **`coral ask`**: Local LLM (Ollama, OpenAI API with your key), your compute,
  your cost
- **Claude Desktop**: Anthropic's LLM, their compute, your Anthropic
  subscription
