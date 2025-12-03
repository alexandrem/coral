# MCP Server Validation

## Proxy jsonRPC

Make sure colony is running before.

Then launch the proxy:

```console
coral colony mcp proxy start | jq
```

Then copy/paste the following snippets in the terminal.

### Initialize

**Request**:

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}
```

**Expected response**:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "capabilities": {
      "tools": {}
    },
    "protocolVersion": "2024-11-05",
    "serverInfo": {
      "name": "coral-my-shop-dev-af9c49",
      "version": "1.0.0"
    }
  }
}
```

### List tools

**Request**:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/list",
  "params": {}
}
```

**Expected response**:

```json
{
    "jsonrpc": "2.0",
    "id": 1,
    "result": {
        "tools": [
            {
                "description": "Query gRPC method-level RED metrics collected via eBPF. Returns RPC rate, latency distributions, and status code breakdown.",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {
                        "grpc_method": {
                            "description": "Optional: Filter by gRPC method (e.g. '/payments.PaymentService/Charge')",
                            "type": "string"
                        },
                        "service": {
                            "description": "Service name (required)",
                            "type": "string"
                        },
                        "status_code": {
                            "description": "Optional: Filter by gRPC status code (0=OK 1=CANCELLED etc.)",
                            "type": "integer"
                        },
                        "time_range": {
                            "default": "1h",
                            "description": "Time range (e.g. '1h' '30m' '24h')",
                            "type": "string"
                        }
                    },
                    "required": [
                        "service"
                    ],
                    "type": "object"
                },
                "name": "coral_query_ebpf_grpc_metrics"
            },
            {
                "description": "List currently active eBPF collectors with their status and remaining duration.",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {
                        "agent_id": {
                            "description": "Optional: Filter by agent ID",
                            "type": "string"
                        },
                        "service": {
                            "description": "Optional: Filter by service (use agent_id for disambiguation)",
                            "type": "string"
                        }
                    },
                    "type": "object"
                },
                "name": "coral_list_ebpf_collectors"
            },
            {
                "description": "Execute a one-off command in the agent's host environment. Returns stdout, stderr, and exit code. Command runs with 30s timeout (max 300s). Use for diagnostic commands like 'ps aux', 'ss -tlnp', 'tcpdump -c 10'.",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {
                        "agent_id": {
                            "description": "Target agent ID (overrides service lookup)",
                            "type": "string"
                        },
                        "command": {
                            "description": "Command as array (e.g. [\"ps\" \"aux\"])",
                            "items": {
                                "type": "string"
                            },
                            "minItems": 1,
                            "type": "array"
                        },
                        "env": {
                            "additionalProperties": {
                                "type": "string"
                            },
                            "description": "Additional environment variables",
                            "type": "object"
                        },
                        "service": {
                            "description": "Service whose agent to execute command on (use agent_id for disambiguation)",
                            "type": "string"
                        },
                        "timeout_seconds": {
                            "default": 30,
                            "description": "Timeout in seconds",
                            "maximum": 300,
                            "type": "integer"
                        },
                        "working_dir": {
                            "description": "Working directory for command execution",
                            "type": "string"
                        }
                    },
                    "required": [
                        "service",
                        "command"
                    ],
                    "type": "object"
                },
                "name": "coral_shell_exec"
            },
            {
                "description": "Attach eBPF uprobe to application function for live debugging. Captures entry/exit events, measures duration. Time-limited and production-safe.",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {
                        "agent_id": {
                            "description": "Target agent ID (optional",
                            "type": "string"
                        },
                        "duration": {
                            "description": "Collection duration (e.g.",
                            "type": "string"
                        },
                        "function": {
                            "description": "Function name to probe (e.g.",
                            "type": "string"
                        },
                        "sample_rate": {
                            "description": "Sample every Nth call (1 = all calls). Default: 1",
                            "type": "integer"
                        },
                        "sdk_addr": {
                            "description": "SDK address (optional",
                            "type": "string"
                        },
                        "service": {
                            "description": "Service name (required)",
                            "type": "string"
                        }
                    },
                    "required": [
                        "service",
                        "function"
                    ],
                    "type": "object"
                },
                "name": "coral_attach_uprobe"
            },
            {
                "description": "Stop debug session early and detach eBPF probes. Returns collected data summary.",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {
                        "session_id": {
                            "description": "Debug session ID to detach",
                            "type": "string"
                        }
                    },
                    "required": [
                        "session_id"
                    ],
                    "type": "object"
                },
                "name": "coral_detach_uprobe"
            },
            {
                "description": "Get context about a function: what calls it, what it calls, recent performance metrics. Use this to navigate the call graph after discovering an entry point.",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {
                        "function": {
                            "description": "Function name (e.g.",
                            "type": "string"
                        },
                        "include_callees": {
                            "description": "Include functions this one calls. Default: true",
                            "type": "boolean"
                        },
                        "include_callers": {
                            "description": "Include functions that call this one. Default: true",
                            "type": "boolean"
                        },
                        "include_metrics": {
                            "description": "Include performance metrics if available. Default: true",
                            "type": "boolean"
                        },
                        "service": {
                            "description": "Service name",
                            "type": "string"
                        }
                    },
                    "required": [
                        "service",
                        "function"
                    ],
                    "type": "object"
                },
                "name": "coral_get_function_context"
            },
            {
                "description": "Execute a command in a container's namespace using nsenter. Access container-mounted configs, logs, and volumes that are not visible from the agent's host filesystem. Works in sidecar and node agent deployments. Returns stdout, stderr, exit code, and container PID. Use for commands like 'cat /app/config.yaml', 'ls /data'.",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {
                        "agent_id": {
                            "description": "Target agent ID (overrides service lookup)",
                            "type": "string"
                        },
                        "command": {
                            "description": "Command as array (e.g. [\"cat\" \"/app/config.yaml\"])",
                            "items": {
                                "type": "string"
                            },
                            "minItems": 1,
                            "type": "array"
                        },
                        "container_name": {
                            "description": "Container name (optional in sidecar mode)",
                            "type": "string"
                        },
                        "env": {
                            "additionalProperties": {
                                "type": "string"
                            },
                            "description": "Additional environment variables",
                            "type": "object"
                        },
                        "namespaces": {
                            "description": "Namespaces to enter (default: [\"mnt\"])",
                            "items": {
                                "type": "string"
                            },
                            "type": "array"
                        },
                        "service": {
                            "description": "Service whose container to execute command in (use agent_id for disambiguation)",
                            "type": "string"
                        },
                        "timeout_seconds": {
                            "default": 30,
                            "description": "Timeout in seconds",
                            "maximum": 300,
                            "type": "integer"
                        },
                        "working_dir": {
                            "description": "Working directory in container namespace",
                            "type": "string"
                        }
                    },
                    "required": [
                        "service",
                        "command"
                    ],
                    "type": "object"
                },
                "name": "coral_container_exec"
            },
            {
                "description": "List active and recent debug sessions across services.",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {
                        "service": {
                            "description": "Filter by service name (optional)",
                            "type": "string"
                        },
                        "status": {
                            "description": "Filter by status (active",
                            "type": "string"
                        }
                    },
                    "type": "object"
                },
                "name": "coral_list_debug_sessions"
            },
            {
                "description": "List functions available for uprobe attachment using regex pattern. Use coral_search_functions instead for semantic search. This is a fallback for regex-based filtering.",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {
                        "pattern": {
                            "description": "Regex filter for function names (e.g.",
                            "type": "string"
                        },
                        "service": {
                            "description": "Service name",
                            "type": "string"
                        }
                    },
                    "required": [
                        "service"
                    ],
                    "type": "object"
                },
                "name": "coral_list_probeable_functions"
            },
            {
                "description": "Get current health status of services. Returns health state, resource usage (CPU, memory), uptime, and recent issues.",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {
                        "service_filter": {
                            "description": "Optional: Filter by service name pattern (e.g. 'api*' 'payment*')",
                            "type": "string"
                        }
                    },
                    "type": "object"
                },
                "name": "coral_get_service_health"
            },
            {
                "description": "Query operational events tracked by Coral (deployments, restarts, crashes, alerts, configuration changes).",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {
                        "event_type": {
                            "description": "Event type filter",
                            "enum": [
                                "deploy",
                                "restart",
                                "crash",
                                "alert",
                                "config_change",
                                "connection",
                                "error_spike"
                            ],
                            "type": "string"
                        },
                        "service": {
                            "description": "Optional: Filter by service",
                            "type": "string"
                        },
                        "time_range": {
                            "default": "24h",
                            "description": "Time range to search",
                            "type": "string"
                        }
                    },
                    "type": "object"
                },
                "name": "coral_query_events"
            },
            {
                "description": "Query HTTP RED metrics collected via eBPF (request rate, error rate, latency distributions). Returns percentiles, status code breakdown, and route-level metrics.",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {
                        "http_method": {
                            "description": "Optional: Filter by HTTP method",
                            "enum": [
                                "GET",
                                "POST",
                                "PUT",
                                "DELETE",
                                "PATCH"
                            ],
                            "type": "string"
                        },
                        "http_route": {
                            "description": "Optional: Filter by HTTP route pattern (e.g. '/api/v1/users/:id')",
                            "type": "string"
                        },
                        "service": {
                            "description": "Service name (required)",
                            "type": "string"
                        },
                        "status_code_range": {
                            "description": "Optional: Filter by status code range",
                            "enum": [
                                "2xx",
                                "3xx",
                                "4xx",
                                "5xx"
                            ],
                            "type": "string"
                        },
                        "time_range": {
                            "default": "1h",
                            "description": "Time range (e.g. '1h' '30m' '24h')",
                            "type": "string"
                        }
                    },
                    "required": [
                        "service"
                    ],
                    "type": "object"
                },
                "name": "coral_query_ebpf_http_metrics"
            },
            {
                "description": "Query SQL operation metrics collected via eBPF. Returns query latencies, operation types, and table-level statistics.",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {
                        "service": {
                            "description": "Service name (required)",
                            "type": "string"
                        },
                        "sql_operation": {
                            "description": "Optional: Filter by SQL operation",
                            "enum": [
                                "SELECT",
                                "INSERT",
                                "UPDATE",
                                "DELETE"
                            ],
                            "type": "string"
                        },
                        "table_name": {
                            "description": "Optional: Filter by table name",
                            "type": "string"
                        },
                        "time_range": {
                            "default": "1h",
                            "description": "Time range (e.g. '1h' '30m' '24h')",
                            "type": "string"
                        }
                    },
                    "required": [
                        "service"
                    ],
                    "type": "object"
                },
                "name": "coral_query_ebpf_sql_metrics"
            },
            {
                "description": "Start an on-demand eBPF collector for live debugging (CPU profiling, syscall tracing, network analysis). Collector runs for specified duration.",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {
                        "agent_id": {
                            "description": "Target agent ID (overrides service lookup",
                            "type": "string"
                        },
                        "collector_type": {
                            "description": "Type of eBPF collector to start",
                            "enum": [
                                "cpu_profile",
                                "syscall_stats",
                                "http_latency",
                                "tcp_metrics"
                            ],
                            "type": "string"
                        },
                        "config_json": {
                            "description": "Optional collector-specific configuration as JSON string",
                            "type": "string"
                        },
                        "duration_seconds": {
                            "default": 30,
                            "description": "How long to run collector (max 300s)",
                            "type": "integer"
                        },
                        "service": {
                            "description": "Target service name (use agent_id for disambiguation)",
                            "type": "string"
                        }
                    },
                    "required": [
                        "collector_type",
                        "service"
                    ],
                    "type": "object"
                },
                "name": "coral_start_ebpf_collector"
            },
            {
                "description": "Stop a running eBPF collector before its duration expires.",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {
                        "collector_id": {
                            "description": "Collector ID returned from start_ebpf_collector",
                            "type": "string"
                        }
                    },
                    "required": [
                        "collector_id"
                    ],
                    "type": "object"
                },
                "name": "coral_stop_ebpf_collector"
            },
            {
                "description": "Get aggregated results from debug session: call counts, duration percentiles, slow outliers.",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {
                        "format": {
                            "description": "Result format (summary",
                            "type": "string"
                        },
                        "session_id": {
                            "description": "Debug session ID",
                            "type": "string"
                        }
                    },
                    "required": [
                        "session_id"
                    ],
                    "type": "object"
                },
                "name": "coral_get_debug_results"
            },
            {
                "description": "Semantic search for functions by keywords. Searches function names, file paths, and comments. Returns ranked results. Prefer this over list_probeable_functions for discovery.",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {
                        "limit": {
                            "description": "Max results to return (default: 20",
                            "type": "integer"
                        },
                        "query": {
                            "description": "Natural language query (e.g.",
                            "type": "string"
                        },
                        "service": {
                            "description": "Service name",
                            "type": "string"
                        }
                    },
                    "required": [
                        "service",
                        "query"
                    ],
                    "type": "object"
                },
                "name": "coral_search_functions"
            },
            {
                "description": "Get service dependency graph discovered from distributed traces. Shows which services communicate and call frequency.",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {
                        "filter": {
                            "description": "Optional: Filter by service name tag or region",
                            "type": "string"
                        },
                        "format": {
                            "default": "graph",
                            "description": "Output format",
                            "enum": [
                                "graph",
                                "list",
                                "json"
                            ],
                            "type": "string"
                        }
                    },
                    "type": "object"
                },
                "name": "coral_get_service_topology"
            },
            {
                "description": "Query distributed traces collected via eBPF. Can search by trace ID, service, time range, or duration threshold.",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {
                        "max_traces": {
                            "default": 10,
                            "description": "Maximum number of traces to return",
                            "type": "integer"
                        },
                        "min_duration_ms": {
                            "description": "Optional: Only return traces longer than this duration (milliseconds)",
                            "type": "integer"
                        },
                        "service": {
                            "description": "Filter traces involving this service",
                            "type": "string"
                        },
                        "time_range": {
                            "default": "1h",
                            "description": "Time range (e.g. '1h' '30m' '24h')",
                            "type": "string"
                        },
                        "trace_id": {
                            "description": "Specific trace ID (32-char hex string)",
                            "type": "string"
                        }
                    },
                    "type": "object"
                },
                "name": "coral_query_ebpf_traces"
            },
            {
                "description": "Get a specific distributed trace by ID with full span tree showing parent-child relationships and timing.",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {
                        "format": {
                            "default": "tree",
                            "description": "Output format",
                            "enum": [
                                "tree",
                                "flat",
                                "json"
                            ],
                            "type": "string"
                        },
                        "trace_id": {
                            "description": "Trace ID (32-char hex string)",
                            "type": "string"
                        }
                    },
                    "required": [
                        "trace_id"
                    ],
                    "type": "object"
                },
                "name": "coral_get_trace_by_id"
            },
            {
                "description": "Query generic OTLP spans (from instrumented applications using OpenTelemetry SDKs). Returns aggregated telemetry summaries. For detailed raw spans, see RFD 041.",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {
                        "operation": {
                            "description": "Optional: Filter by operation name",
                            "type": "string"
                        },
                        "service": {
                            "description": "Service name",
                            "type": "string"
                        },
                        "time_range": {
                            "default": "1h",
                            "description": "Time range (e.g. '1h' '30m' '24h')",
                            "type": "string"
                        }
                    },
                    "required": [
                        "service"
                    ],
                    "type": "object"
                },
                "name": "coral_query_telemetry_spans"
            },
            {
                "description": "Query generic OTLP metrics (from instrumented applications). Returns time-series data for custom application metrics.",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {
                        "metric_name": {
                            "description": "Metric name (e.g. 'http.server.duration' 'custom.orders.count')",
                            "type": "string"
                        },
                        "service": {
                            "description": "Optional: Filter by service",
                            "type": "string"
                        },
                        "time_range": {
                            "default": "1h",
                            "description": "Time range",
                            "type": "string"
                        }
                    },
                    "type": "object"
                },
                "name": "coral_query_telemetry_metrics"
            },
            {
                "description": "Query generic OTLP logs (from instrumented applications). Search application logs with full-text search and filters.",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {
                        "level": {
                            "description": "Optional: Filter by log level",
                            "enum": [
                                "DEBUG",
                                "INFO",
                                "WARN",
                                "ERROR",
                                "FATAL"
                            ],
                            "type": "string"
                        },
                        "query": {
                            "description": "Search query (full-text search)",
                            "type": "string"
                        },
                        "service": {
                            "description": "Optional: Filter by service",
                            "type": "string"
                        },
                        "time_range": {
                            "default": "1h",
                            "description": "Time range",
                            "type": "string"
                        }
                    },
                    "type": "object"
                },
                "name": "coral_query_telemetry_logs"
            },
            {
                "description": "List all services known to the colony - includes both currently connected services and historical services from observability data. Returns service names, ports, and types. Useful for discovering available services before querying metrics or traces.",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {},
                    "type": "object"
                },
                "name": "coral_list_services"
            },
            {
                "description": "Trace all functions called during HTTP request execution. Auto-discovers call chain and builds execution tree.",
                "inputSchema": {
                    "additionalProperties": false,
                    "properties": {
                        "duration": {
                            "description": "Trace duration. Default: 60s",
                            "type": "string"
                        },
                        "path": {
                            "description": "HTTP path to trace (e.g.",
                            "type": "string"
                        },
                        "service": {
                            "description": "Service name",
                            "type": "string"
                        }
                    },
                    "required": [
                        "service",
                        "path"
                    ],
                    "type": "object"
                },
                "name": "coral_trace_request_path"
            }
        ]
    }
}
```
