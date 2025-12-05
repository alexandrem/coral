# CLI to MCP Tool Mapping

This document maps Coral CLI commands to their equivalent MCP tools for AI/LLM
integration.

**See also:**

- [CLI_REFERENCE.md](./CLI_REFERENCE.md) - CLI command reference
- [CLI.md](./CLI.md) - Detailed CLI documentation

---

## Quick Reference

| CLI Category              | MCP Tool(s)                                                                             | Status                            |
|---------------------------|-----------------------------------------------------------------------------------------|-----------------------------------|
| eBPF Metrics & Traces     | `coral_query_summary`, `coral_query_traces`, `coral_query_metrics`                      | ✅ Available                       |
| Live Debugging            | `coral_attach_uprobe`, `coral_detach_uprobe`, `coral_list_debug_sessions`, etc.         | ✅ Available                       |
| Container Execution       | `coral_container_exec`                                                                  | ✅ Available                       |
| Agent Shell Access        | `coral_shell_exec`                                                                      | ✅ Available                       |
| Service Discovery         | `coral_list_services`                                                                   | ✅ Available                       |
| DuckDB Queries            | ❌ No MCP equivalent                                                                     | Use `coral_query_*` tools instead |
| Setup & Configuration     | ❌ No MCP equivalent                                                                     | Local operations only             |

---

## Service Connections

```bash
# Connect agent to services
coral connect <service-spec>...
coral connect frontend:3000 api:8080:/health

# List connected services
# (no direct CLI command, but can check agent status)
```

**MCP Equivalents:**

| Operation                | MCP Tool                     | Parameters                                                              |
|--------------------------|------------------------------|-------------------------------------------------------------------------|
| List services            | `coral_list_services`        | `agent_id` (optional), `service_name` (optional)                        |

**Example:**

```json
{
    "name": "coral_list_services",
    "arguments": {
        "agent_id": "hostname-api-1"
    }
}
```

---

## AI Queries

```bash
coral ask "<question>" [--json] [--model <provider:model>] [--debug] [--dry-run]
```

**MCP Equivalent:** ⚡ **This IS the MCP interface**

The `coral ask` command is a CLI wrapper that:

1. Calls your configured AI provider (Anthropic Claude, etc.)
2. The AI uses MCP tools to query the colony
3. Returns formatted results

**Behind the scenes:** When you run `coral ask "Why is the API slow?"`, the AI
may call:

- `coral_query_summary` - Get service health
- `coral_query_metrics` - Get HTTP/gRPC/SQL metrics
- `coral_query_traces` - Examine slow traces
- `coral_get_debug_results` - Check uprobe data if available

---

## Unified Query Commands (RFD 067)

The unified query interface combines data from eBPF and OTLP sources by default, providing a complete observability picture.

### Service Health Summary

```bash
coral query summary [service] [--since <duration>]
```

**MCP Equivalent:** `coral_query_summary`

| CLI Parameter        | MCP Parameter | Example          |
|----------------------|---------------|------------------|
| `[service]`          | `service`     | `"payments-api"` |
| `--since <duration>` | `time_range`  | `"5m"`, `"1h"`   |

**Example:**

```json
{
    "name": "coral_query_summary",
    "arguments": {
        "service": "payments-api",
        "time_range": "5m"
    }
}
```

**Response includes:**

- Health status (✅ healthy, ⚠️ degraded, ❌ critical)
- Request count, error rate, average latency
- Data source annotation (eBPF, OTLP, or eBPF+OTLP)
- Issues detected (if any)

---

### Distributed Traces

```bash
coral query traces [service] [--since <duration>] [--trace-id <id>] [--source ebpf|telemetry|all] [--min-duration <ms>] [--max-traces <n>]
```

**MCP Equivalent:** `coral_query_traces`

| CLI Parameter         | MCP Parameter     | Example                          |
|-----------------------|-------------------|----------------------------------|
| `[service]`           | `service`         | `"payments-api"`                 |
| `--since <duration>`  | `time_range`      | `"1h"`, `"30m"`                  |
| `--trace-id <id>`     | `trace_id`        | `"abc123def456789"`              |
| `--source <type>`     | `source`          | `"ebpf"`, `"telemetry"`, `"all"` |
| `--min-duration <ms>` | `min_duration_ms` | `500`                            |
| `--max-traces <n>`    | `max_traces`      | `10`                             |

**Example - Query by service:**

```json
{
    "name": "coral_query_traces",
    "arguments": {
        "service": "payments-api",
        "time_range": "1h",
        "source": "all"
    }
}
```

**Example - Query by trace ID:**

```json
{
    "name": "coral_query_traces",
    "arguments": {
        "trace_id": "abc123def456789"
    }
}
```

**Response includes:**

- Trace ID, service name, span name, duration
- Parent-child relationships
- Source annotations (📍 eBPF, 📊 OTLP)
- For OTLP spans: aggregated metrics (total spans, error count)

---

### Metrics (HTTP/gRPC/SQL)

```bash
coral query metrics [service] [--since <duration>] [--source ebpf|telemetry|all] [--protocol http|grpc|sql|auto]
```

**MCP Equivalent:** `coral_query_metrics`

| CLI Parameter        | MCP Parameter | Example                          |
|----------------------|---------------|----------------------------------|
| `[service]`          | `service`     | `"payments-api"`                 |
| `--since <duration>` | `time_range`  | `"1h"`, `"30m"`, `"24h"`         |
| `--source <type>`    | `source`      | `"ebpf"`, `"telemetry"`, `"all"` |
| `--protocol <type>`  | `protocol`    | `"http"`, `"grpc"`, `"sql"`      |

**Example:**

```json
{
    "name": "coral_query_metrics",
    "arguments": {
        "service": "payments-api",
        "time_range": "1h",
        "source": "all",
        "protocol": "http"
    }
}
```

**Response includes:**

- HTTP/gRPC/SQL metrics from eBPF and OTLP
- Request counts, latency percentiles (P50/P95/P99)
- Source annotations for each metric
- Route/method/operation breakdown

---

### Logs

```bash
coral query logs [service] [--since <duration>] [--level debug|info|warn|error] [--search <text>] [--max-logs <n>]
```

**MCP Equivalent:** `coral_query_logs`

| CLI Parameter        | MCP Parameter | Example                        |
|----------------------|---------------|--------------------------------|
| `[service]`          | `service`     | `"payments-api"`               |
| `--since <duration>` | `time_range`  | `"1h"`, `"30m"`                |
| `--level <level>`    | `level`       | `"error"`, `"warn"`, `"info"`  |
| `--search <text>`    | `search`      | `"timeout"`, `"database"`      |
| `--max-logs <n>`     | `max_logs`    | `100`                          |

**Example:**

```json
{
    "name": "coral_query_logs",
    "arguments": {
        "service": "payments-api",
        "time_range": "1h",
        "level": "error",
        "search": "timeout"
    }
}
```

**Response includes:**

- Log entries from OTLP
- Timestamp, level, message
- Filtered by search terms and level

---

## DuckDB Queries

```bash
# List agents and databases
coral duckdb list-agents
coral duckdb list

# One-shot queries
coral duckdb query <agent-id> "<sql>" [-d <database>] [-f table|csv|json]

# Interactive shell
coral duckdb shell <agent-id> [-d <database>]
```

**MCP Equivalent:** ❌ None - Use unified query tools instead

**Why:** Direct SQL access is useful for ad-hoc exploration, but MCP provides
higher-level abstractions:

| DuckDB Query Pattern    | MCP Alternative                              |
|-------------------------|----------------------------------------------|
| Recent HTTP errors      | `coral_query_summary` + filter by error_rate |
| P99 latency by endpoint | `coral_query_metrics`                        |
| Slow SQL queries        | `coral_query_metrics`                        |
| Trace analysis          | `coral_query_traces`                         |

**Recommendation for AI:** Use `coral_query_summary`, `coral_query_metrics`, and
`coral_query_traces` instead of raw SQL. These tools:

- Merge eBPF + OTLP data automatically
- Provide health status and anomaly detection
- Return pre-aggregated, AI-friendly results

---

## Live Debugging (SDK Mode)

### Attach Probes

```bash
coral debug attach <service> --function <name> [--duration <time>] [--capture-args] [--capture-return]
```

**MCP Equivalent:** `coral_attach_uprobe`

| CLI Parameter       | MCP Parameter      | Example            |
|---------------------|--------------------|--------------------|
| `<service>`         | `service_name`     | `"payments-api"`   |
| `--function <name>` | `function_name`    | `"ProcessPayment"` |
| `--duration <time>` | `duration_seconds` | `300` (5 minutes)  |
| `--capture-args`    | `capture_args`     | `true`             |
| `--capture-return`  | `capture_return`   | `true`             |

**Example:**

```json
{
    "name": "coral_attach_uprobe",
    "arguments": {
        "service_name": "payments-api",
        "function_name": "ProcessPayment",
        "duration_seconds": 300,
        "capture_args": true,
        "capture_return": true
    }
}
```

**Response:** Session ID for later querying

---

### Trace Request Path

```bash
coral debug trace <service> --path <path> [--duration <time>]
```

**MCP Equivalent:** `coral_trace_request_path`

| CLI Parameter       | MCP Parameter      | Example              |
|---------------------|--------------------|----------------------|
| `<service>`         | `service_name`     | `"payments-api"`     |
| `--path <path>`     | `request_path`     | `"/api/v1/checkout"` |
| `--duration <time>` | `duration_seconds` | `300`                |

**Example:**

```json
{
    "name": "coral_trace_request_path",
    "arguments": {
        "service_name": "payments-api",
        "request_path": "/api/v1/checkout",
        "duration_seconds": 300
    }
}
```

---

### Manage Probes

```bash
coral debug session list [service]
coral debug session stop <session-id>
coral debug session get <session-id>
```

**MCP Equivalents:**

| CLI Command                         | MCP Tool                    | Parameters                |
|-------------------------------------|-----------------------------|---------------------------|
| `coral debug session list`          | `coral_list_debug_sessions` | `service_name` (optional) |
| `coral debug session stop <id>`     | `coral_detach_uprobe`       | `session_id`              |
| `coral debug session get <id>`      | `coral_get_debug_results`   | `session_id`              |

**Example - List sessions:**

```json
{
    "name": "coral_list_debug_sessions",
    "arguments": {
        "service_name": "payments-api"
    }
}
```

**Example - Stop session:**

```json
{
    "name": "coral_detach_uprobe",
    "arguments": {
        "session_id": "abc123"
    }
}
```

---

### Query Results

```bash
coral debug query <service> --function <name> [--since <duration>]
```

**MCP Equivalent:** `coral_get_debug_results`

| CLI Parameter        | MCP Parameter   | Example            |
|----------------------|-----------------|--------------------|
| `<service>`          | `service_name`  | `"payments-api"`   |
| `--function <name>`  | `function_name` | `"ProcessPayment"` |
| `--since <duration>` | `time_range`    | `"5m"`             |

**Example:**

```json
{
    "name": "coral_get_debug_results",
    "arguments": {
        "service_name": "payments-api",
        "function_name": "ProcessPayment",
        "time_range": "5m"
    }
}
```

---

### Function Discovery

**No direct CLI commands**, but MCP provides rich function search capabilities:

| MCP Tool                         | Purpose                                          | Parameters                      |
|----------------------------------|--------------------------------------------------|---------------------------------|
| `coral_search_functions`         | Search for functions by name/pattern             | `service_name`, `search_query`  |
| `coral_list_probeable_functions` | List all functions that can be probed            | `service_name`, `agent_id`      |
| `coral_get_function_context`     | Get detailed function info (signature, location) | `service_name`, `function_name` |

**Example - Search for payment-related functions:**

```json
{
    "name": "coral_search_functions",
    "arguments": {
        "service_name": "payments-api",
        "search_query": "payment"
    }
}
```

**Example - Get function signature:**

```json
{
    "name": "coral_get_function_context",
    "arguments": {
        "service_name": "payments-api",
        "function_name": "ProcessPayment"
    }
}
```

---

## Agent Shell Access

```bash
# Interactive shell
coral shell [--agent <agent-id>] [--agent-addr <address>] [--user-id <user>]

# One-off command execution
coral shell [--agent <agent-id>] -- <command> [args...]
```

**MCP Equivalent:** `coral_shell_exec`

| CLI Parameter            | MCP Parameter   | Example               |
|--------------------------|-----------------|-----------------------|
| `--agent <agent-id>`     | `agent_id`      | `"hostname-api-1"`    |
| `--agent-addr <address>` | `agent_address` | `"100.64.0.5:9001"`   |
| `-- <command>`           | `command`       | `"ps aux"`            |
| `--user-id <user>`       | `user_id`       | `"alice@company.com"` |

**Example:**

```json
{
    "name": "coral_shell_exec",
    "arguments": {
        "agent_id": "hostname-api-1",
        "command": "ps aux | grep nginx"
    }
}
```

**Note:** For interactive shell sessions, use the CLI. MCP is designed for
one-off command execution.

---

## Container Execution

```bash
coral exec <service> <command> [args...] [flags]
```

**MCP Equivalent:** `coral_container_exec`

| CLI Parameter                | MCP Parameter     | Example                       |
|------------------------------|-------------------|-------------------------------|
| `<service>`                  | `service_name`    | `"nginx"`                     |
| `<command> [args...]`        | `command`         | `"cat /etc/nginx/nginx.conf"` |
| `--agent <agent-id>`         | `agent_id`        | `"hostname-api-1"`            |
| `--agent-addr <address>`     | `agent_address`   | `"100.64.0.5:9001"`           |
| `--container <name>`         | `container_name`  | `"nginx"`                     |
| `--timeout <seconds>`        | `timeout_seconds` | `60`                          |
| `--working-dir <path>`       | `working_dir`     | `"/app"`                      |
| `--env <KEY=VALUE>`          | `env_vars`        | `{"DEBUG": "true"}`           |
| `--namespaces <ns1,ns2,...>` | `namespaces`      | `["mnt", "pid"]`              |

**Example - Basic:**

```json
{
    "name": "coral_container_exec",
    "arguments": {
        "service_name": "nginx",
        "command": "cat /etc/nginx/nginx.conf"
    }
}
```

**Example - Advanced:**

```json
{
    "name": "coral_container_exec",
    "arguments": {
        "service_name": "api-server",
        "command": "find /data -name '*.log'",
        "agent_id": "hostname-api-1",
        "working_dir": "/app",
        "env_vars": {
            "DEBUG": "true"
        },
        "timeout_seconds": 60
    }
}
```

---

## Summary: Complete MCP Tool Reference

### Unified Query Tools (RFD 067)

| Tool Name             | Description                          | Key Parameters                      |
|-----------------------|--------------------------------------|-------------------------------------|
| `coral_query_summary` | Service health summary (eBPF + OTLP) | `service`, `time_range`             |
| `coral_query_traces`  | Distributed traces (eBPF + OTLP)     | `trace_id`, `service`, `time_range` |
| `coral_query_metrics` | HTTP/gRPC/SQL metrics (eBPF + OTLP)  | `service`, `time_range`             |
| `coral_query_logs`    | Logs (OTLP)                          | `service`, `time_range`, `level`    |

### Service Discovery

| Tool Name             | Description                 | Key Parameters                 |
|-----------------------|-----------------------------|--------------------------------|
| `coral_list_services` | List all monitored services | `agent_id`, `service_name`     |

### Live Debugging (RFD 062)

| Tool Name                        | Description                | Key Parameters                                                                        |
|----------------------------------|----------------------------|---------------------------------------------------------------------------------------|
| `coral_attach_uprobe`            | Attach uprobe to function  | `service_name`, `function_name`, `duration_seconds`, `capture_args`, `capture_return` |
| `coral_trace_request_path`       | Trace HTTP request path    | `service_name`, `request_path`, `duration_seconds`                                    |
| `coral_list_debug_sessions`      | List active debug sessions | `service_name`                                                                        |
| `coral_detach_uprobe`            | Detach uprobe              | `session_id`                                                                          |
| `coral_get_debug_results`        | Get uprobe results         | `service_name`, `function_name`, `time_range`                                         |
| `coral_search_functions`         | Search for functions       | `service_name`, `search_query`                                                        |
| `coral_get_function_context`     | Get function details       | `service_name`, `function_name`                                                       |
| `coral_list_probeable_functions` | List probeable functions   | `service_name`, `agent_id`                                                            |

### Shell & Execution

| Tool Name              | Description                   | Key Parameters                                                                                                      |
|------------------------|-------------------------------|---------------------------------------------------------------------------------------------------------------------|
| `coral_shell_exec`     | Execute command on agent host | `agent_id`, `command`, `user_id`                                                                                    |
| `coral_container_exec` | Execute command in container  | `service_name`, `command`, `agent_id`, `container_name`, `timeout_seconds`, `working_dir`, `env_vars`, `namespaces` |

---

## Key Differences: CLI vs MCP

### Time Ranges

**CLI:** Supports both relative (`--since 1h`) and absolute (`--from`, `--to`)
timestamps

**MCP:** Only supports relative time ranges via `time_range` parameter:

- Examples: `"5m"`, `"1h"`, `"24h"`, `"1d"`, `"1w"`
- Default varies by tool (summary: `5m`, traces/metrics: `1h`)

### Output Formats

**CLI:** Supports `--output table|json|csv` and `--format tree` (traces)

**MCP:** Always returns structured text or JSON. AI can parse and reformat as
needed.

### Filtering

**CLI:** Many commands support filters like `--route`, `--method`,
`--operation`, `--table`

**MCP:** Unified tools return broader datasets. AI performs filtering in
post-processing.

**Example:** To find slow `/checkout` requests:

1. Call `coral_query_traces` with `service` and `time_range`
2. AI filters response for spans matching `/checkout` pattern
3. AI analyzes latency distribution

### Interactive vs Programmatic

**CLI:** Supports interactive modes (`coral shell`, `coral duckdb shell`)

**MCP:** Designed for programmatic access. Use one-shot command execution tools.

---

## Best Practices for AI/LLM Usage

### 1. Use Unified Query Tools First

Instead of multiple specific queries, start with:

- `coral_query_summary` - High-level health overview
- Then drill down with `coral_query_metrics` or `coral_query_traces`

### 2. Combine Tools for Root Cause Analysis

**Example workflow for "API is slow":**

1. `coral_query_summary` → Identify degraded services
2. `coral_query_metrics` → Get latency breakdown
3. `coral_query_traces` → Find slowest traces
4. `coral_search_functions` → Find relevant functions
5. `coral_attach_uprobe` → Attach probes to suspect functions
6. `coral_get_debug_results` → Analyze function-level data

### 3. Leverage Source Annotations

MCP tools annotate data with source (eBPF, OTLP, or eBPF+OTLP):

- **eBPF** = Kernel-level auto-instrumentation (no code changes)
- **OTLP** = Application-level instrumentation (OpenTelemetry)
- **eBPF+OTLP** = Merged data (most complete view)

Use this to understand data completeness and quality.

### 4. Time Range Selection

**Summary queries:** Use short time ranges (`5m`, `15m`) for current health
**Trend analysis:** Use longer ranges (`1h`, `24h`) for patterns
**Incident investigation:** Use specific time windows around the incident

### 5. Iterative Narrowing

Start broad, then narrow:

1. `coral_query_summary` (all services) → Identify problem service
2. `coral_query_summary` (specific service) → Confirm issue
3. `coral_query_traces` (specific service) → Find slow traces
4. `coral_attach_uprobe` (specific function) → Debug root cause

---

**For detailed documentation:**

- CLI: [CLI_REFERENCE.md](./CLI_REFERENCE.md), [CLI.md](./CLI.md)
- MCP Protocol: See MCP server implementation in `internal/colony/mcp/`
- RFD
  067: [RFDs/067-unified-query-interface.md](../RFDs/067-unified-query-interface.md)
