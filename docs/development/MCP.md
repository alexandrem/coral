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
                "description": "Get current health status of services. Returns health state, resource usage (CPU, memory), uptime, and recent issues.",
                "inputSchema": {
                    "$defs": {
                        "ServiceHealthInput": {
                            "additionalProperties": false,
                            "properties": {
                                "service_filter": {
                                    "description": "Optional: Filter by service name pattern (e.g. 'api*' 'payment*')",
                                    "type": "string"
                                }
                            },
                            "type": "object"
                        }
                    },
                    "$id": "https://github.com/coral-io/coral/internal/colony/mcp/service-health-input",
                    "$ref": "#/$defs/ServiceHealthInput",
                    "$schema": "https://json-schema.org/draft/2020-12/schema"
                },
                "name": "coral_get_service_health"
            },
            {
                "description": "Get service dependency graph discovered from distributed traces. Shows which services communicate and call frequency.",
                "inputSchema": {
                    "$defs": {
                        "ServiceTopologyInput": {
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
                        }
                    },
                    "$id": "https://github.com/coral-io/coral/internal/colony/mcp/service-topology-input",
                    "$ref": "#/$defs/ServiceTopologyInput",
                    "$schema": "https://json-schema.org/draft/2020-12/schema"
                },
                "name": "coral_get_service_topology"
            },
            {
                "description": "Query operational events tracked by Coral (deployments, restarts, crashes, alerts, configuration changes).",
                "inputSchema": {
                    "$defs": {
                        "QueryEventsInput": {
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
                        }
                    },
                    "$id": "https://github.com/coral-io/coral/internal/colony/mcp/query-events-input",
                    "$ref": "#/$defs/QueryEventsInput",
                    "$schema": "https://json-schema.org/draft/2020-12/schema"
                },
                "name": "coral_query_events"
            },
            {
                "description": "Query SQL operation metrics collected by Beyla. Returns query latencies, operation types, and table-level statistics.",
                "inputSchema": {
                    "$defs": {
                        "BeylaSQLMetricsInput": {
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
                        }
                    },
                    "$id": "https://github.com/coral-io/coral/internal/colony/mcp/beyla-sql-metrics-input",
                    "$ref": "#/$defs/BeylaSQLMetricsInput",
                    "$schema": "https://json-schema.org/draft/2020-12/schema"
                },
                "name": "coral_query_beyla_sql_metrics"
            },
            {
                "description": "Query distributed traces collected by Beyla. Can search by trace ID, service, time range, or duration threshold.",
                "inputSchema": {
                    "$defs": {
                        "BeylaTracesInput": {
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
                        }
                    },
                    "$id": "https://github.com/coral-io/coral/internal/colony/mcp/beyla-traces-input",
                    "$ref": "#/$defs/BeylaTracesInput",
                    "$schema": "https://json-schema.org/draft/2020-12/schema"
                },
                "name": "coral_query_beyla_traces"
            },
            {
                "description": "Get a specific distributed trace by ID with full span tree showing parent-child relationships and timing.",
                "inputSchema": {
                    "$defs": {
                        "TraceByIDInput": {
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
                        }
                    },
                    "$id": "https://github.com/coral-io/coral/internal/colony/mcp/trace-by-id-input",
                    "$ref": "#/$defs/TraceByIDInput",
                    "$schema": "https://json-schema.org/draft/2020-12/schema"
                },
                "name": "coral_get_trace_by_id"
            },
            {
                "description": "Query generic OTLP spans (from instrumented applications using OpenTelemetry SDKs). Returns aggregated telemetry summaries. For detailed raw spans, see RFD 041.",
                "inputSchema": {
                    "$defs": {
                        "TelemetrySpansInput": {
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
                        }
                    },
                    "$id": "https://github.com/coral-io/coral/internal/colony/mcp/telemetry-spans-input",
                    "$ref": "#/$defs/TelemetrySpansInput",
                    "$schema": "https://json-schema.org/draft/2020-12/schema"
                },
                "name": "coral_query_telemetry_spans"
            },
            {
                "description": "Query generic OTLP metrics (from instrumented applications). Returns time-series data for custom application metrics.",
                "inputSchema": {
                    "$defs": {
                        "TelemetryMetricsInput": {
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
                        }
                    },
                    "$id": "https://github.com/coral-io/coral/internal/colony/mcp/telemetry-metrics-input",
                    "$ref": "#/$defs/TelemetryMetricsInput",
                    "$schema": "https://json-schema.org/draft/2020-12/schema"
                },
                "name": "coral_query_telemetry_metrics"
            },
            {
                "description": "Query HTTP RED metrics collected by Beyla (request rate, error rate, latency distributions). Returns percentiles, status code breakdown, and route-level metrics.",
                "inputSchema": {
                    "$defs": {
                        "BeylaHTTPMetricsInput": {
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
                        }
                    },
                    "$id": "https://github.com/coral-io/coral/internal/colony/mcp/beyla-http-metrics-input",
                    "$ref": "#/$defs/BeylaHTTPMetricsInput",
                    "$schema": "https://json-schema.org/draft/2020-12/schema"
                },
                "name": "coral_query_beyla_http_metrics"
            },
            {
                "description": "Query gRPC method-level RED metrics collected by Beyla. Returns RPC rate, latency distributions, and status code breakdown.",
                "inputSchema": {
                    "$defs": {
                        "BeylaGRPCMetricsInput": {
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
                        }
                    },
                    "$id": "https://github.com/coral-io/coral/internal/colony/mcp/beyla-grpc-metrics-input",
                    "$ref": "#/$defs/BeylaGRPCMetricsInput",
                    "$schema": "https://json-schema.org/draft/2020-12/schema"
                },
                "name": "coral_query_beyla_grpc_metrics"
            },
            {
                "description": "Query generic OTLP logs (from instrumented applications). Search application logs with full-text search and filters.",
                "inputSchema": {
                    "$defs": {
                        "TelemetryLogsInput": {
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
                        }
                    },
                    "$id": "https://github.com/coral-io/coral/internal/colony/mcp/telemetry-logs-input",
                    "$ref": "#/$defs/TelemetryLogsInput",
                    "$schema": "https://json-schema.org/draft/2020-12/schema"
                },
                "name": "coral_query_telemetry_logs"
            }
        ]
    }
}
```
