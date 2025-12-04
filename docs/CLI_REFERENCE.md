# Coral CLI Quick Reference

**See also:**
- [CLI.md](./CLI.md) - Detailed examples, concepts, and troubleshooting
- [CLI_MCP_MAPPING.md](./CLI_MCP_MAPPING.md) - CLI to MCP tool mapping for AI/LLM integration

---

## Setup & Configuration

```bash
# Initialize
coral init <colony-name>

# Configuration management
coral config get-contexts [--json]
coral config current-context [--verbose]
coral config use-context <colony-id>
coral config view [--colony <id>] [--raw]
coral config validate [--json]
coral config delete-context <colony-id>

# Version
coral version
```

---

## Colony & Agent Management

```bash
# Colony (central coordinator)
coral colony start [--daemon] [--port <port>] [--config <file>]
coral colony status [--json]
coral colony stop

# Agent (local observer)
coral agent start [--config <file>] [--colony-id <id>] [--connect <service>...] [--monitor-all]
coral agent status
coral agent stop

# Agent startup modes:
#   Passive:      coral agent start
#   With services: coral agent start --connect frontend:3000 --connect api:8080
#   Monitor all:  coral agent start --monitor-all
```

---

## Service Connections

```bash
# Connect agent to services (at startup or dynamically)
coral connect <service-spec>...

# At agent startup (automatic eBPF instrumentation)
coral agent start --connect frontend:3000 --connect api:8080:/health

# Dynamically after agent started (triggers eBPF restart)
coral connect frontend:3000
coral connect api:8080:/health:http
coral connect frontend:3000 api:8080:/health redis:6379

# Format: name:port[:health][:type]
# Examples:
coral connect frontend:3000                    # HTTP service on port 3000
coral connect api:8080:/health:http           # With health check endpoint
coral connect frontend:3000 api:8080:/health  # Multiple services

# Legacy syntax (single service)
coral connect <name> --port <port> [--health <path>]
```

---

## AI Queries

```bash
# Configuration (first time)
coral ask config

# Ask questions
coral ask "<question>" [--json] [--model <provider:model>] [--debug] [--dry-run]

# Flags:
#   --json             Output as JSON
#   --model <name>     Use specific model (e.g., anthropic:claude-3-5-sonnet-20241022)
#   --debug            Show debug information (prompts, tool calls, etc.)
#   --dry-run          Show what would be queried without executing

# Examples:
coral ask "Why is the API slow?"
coral ask "What changed in the last hour?"
coral ask "Show me error trends"
coral ask "System status?" --debug
coral ask "Check errors" --dry-run
```

---

## Unified Query Commands (RFD 067)

**Unified interface combining eBPF and OTLP data sources.**

```bash
# Service health summary
coral query summary [service] [--since <duration>]

# Distributed traces
coral query traces [service] [--since <duration>] [--trace-id <id>] [--source ebpf|telemetry|all] [--min-duration-ms <ms>] [--max-traces <n>]

# Service metrics (HTTP/gRPC/SQL)
coral query metrics [service] [--since <duration>] [--source ebpf|telemetry|all] [--protocol http|grpc|sql|auto] [--http-route <pattern>] [--http-method <method>] [--status-code-range <range>]

# Application logs
coral query logs [service] [--since <duration>] [--level debug|info|warn|error] [--search <text>] [--max-logs <n>]

# Time range options (all commands):
#   --since <duration>     # Relative (5m, 1h, 30m, 24h, 1d, 1w)

# Examples - Service health summary:
coral query summary                          # All services
coral query summary api                      # Specific service
coral query summary api --since 10m          # Custom time range

# Examples - Metrics:
coral query metrics api                              # All metrics for api service
coral query metrics api --protocol http              # Only HTTP metrics
coral query metrics api --source ebpf                # Only eBPF data
coral query metrics api --http-route /api/v1/*       # Filter by route
coral query metrics api --status-code-range 5xx      # Only 5xx errors
coral query metrics payments-api --since 1h          # Last hour

# Examples - Traces:
coral query traces api                               # All traces for api service
coral query traces --trace-id abc123def456789        # Specific trace by ID
coral query traces api --source ebpf                 # Only eBPF traces
coral query traces api --min-duration-ms 500         # Only slow traces (>500ms)
coral query traces payments-api --since 30m          # Last 30 minutes
coral query traces api --max-traces 5                # Limit results

# Examples - Logs:
coral query logs api                                 # All logs for api service
coral query logs api --level error                   # Only error logs
coral query logs --search "timeout"                  # Search for specific text
coral query logs api --since 30m --max-logs 50       # Last 30 minutes, limit 50
```

**What you get:**
- **Summary**: Health status (healthy/degraded/critical), error rates, latency, request counts
- **Metrics**: HTTP/gRPC/SQL RED metrics with P50/P95/P99 latency percentiles from eBPF + OTLP
- **Traces**: Distributed trace spans with parent-child relationships, source annotations (eBPF/OTLP)
- **Logs**: Application logs from OTLP with filtering and search
- **Automatic merging**: eBPF and OTLP data combined by default with source annotations
- **No SQL needed**: High-level commands for common observability patterns

> **Note**: Old `coral query ebpf` commands are deprecated. Use the unified `coral query` commands above.

---

## DuckDB Queries

```bash
# List agents and databases
coral duckdb list-agents
coral duckdb list  # alias

# One-shot queries
coral duckdb query <agent-id> "<sql>" [-d <database>] [-f table|csv|json]

# Interactive shell
coral duckdb shell <agent-id> [-d <database>]
coral duckdb shell --agents <agent-1>,<agent-2>,... [-d <database>]

# Shell meta-commands
.tables      # List all tables
.databases   # Show attached databases
.help        # Show help
.refresh     # Detach and re-attach databases to refresh data
.exit        # Exit shell
```

### Available Databases

**Agent:**

- `metrics.duckdb` - All agent metrics (spans, HTTP/gRPC/SQL metrics)

**Colony (future):**

- `metrics.duckdb` - Aggregated historical data

### Agent Key Tables

**Beyla (eBPF metrics):**

- `beyla_http_metrics_local` - HTTP RED metrics
- `beyla_grpc_metrics_local` - gRPC call metrics
- `beyla_sql_metrics_local` - Database query metrics

**Beyla (eBPF traces):**

- `beyla_traces_local` - OTLP distributed tracing spans

**Telemetry (OTel):**

- `otel_spans_local` - OTLP distributed tracing spans

---

## Live Debugging (SDK mode)

```bash
# Attach probes
coral debug attach <service> --function <name> [--duration <time>] [--capture-args] [--capture-return]
coral debug trace <service> --path <path> [--duration <time>]

# Manage probes
coral debug list [service]
coral debug detach <session-id>

# Query results
coral debug query <service> --function <name> [--since <duration>]
```

---

## Agent Shell Access

```bash
# Interactive shell
coral shell [--agent <agent-id>] [--agent-addr <address>] [--user-id <user>]

# One-off command execution (like kubectl exec)
coral shell [--agent <agent-id>] -- <command> [args...]

# Examples - Interactive mode:
coral shell                                   # Local agent
coral shell --agent hostname-api-1            # Specific agent by ID
coral shell --agent-addr 100.64.0.5:9001      # Specific agent by address

# Examples - Command execution mode:
coral shell -- ps aux                         # Execute command on local agent
coral shell --agent 6b86a4acc127 -- ps aux    # Execute on specific agent
coral shell -- sh -c "ps aux && netstat -tunlp"  # Complex command with shell
coral shell --user-id alice@company.com -- whoami  # With audit user ID

# Available tools in agent shell:
#   - Network: tcpdump, netcat, curl, dig
#   - Process: ps, top
#   - Database: duckdb (query agent's local database)
#   - Files: agent config, logs, data
```

---

## Container Execution

```bash
# Execute commands in service containers (nsenter mode)
coral exec <service> <command> [args...] [flags]

# Flags:
#   --agent <agent-id>              Target specific agent by ID
#   --agent-addr <address>          Target specific agent by address
#   --colony <colony-id>            Colony ID (default: auto-detect)
#   --user-id <user>                User ID for audit (default: $USER)
#   --container <name>              Container name (multi-container pods)
#   --timeout <seconds>             Timeout in seconds (max 300, default: 30)
#   --working-dir <path>            Working directory in container
#   --env <KEY=VALUE>               Environment variables (repeatable)
#   --namespaces <ns1,ns2,...>      Namespaces to enter (default: mnt)
#                                   Options: mnt,pid,net,ipc,uts,cgroup

# Examples - Basic usage:
coral exec nginx cat /etc/nginx/nginx.conf
coral exec api-server -- ls -la /data
coral exec web -- ps aux

# Examples - Advanced options:
coral exec nginx --agent hostname-api-1 cat /app/config.yaml
coral exec app --working-dir /app -- find . -name "*.log"
coral exec api --env DEBUG=true env
coral exec nginx --namespaces mnt,pid ps aux
coral exec logs-processor --timeout 60 -- find /data -name "*.log"
coral exec web --container nginx cat /etc/nginx/nginx.conf

# Key differences:
#   coral shell    → Runs on AGENT HOST (agent's environment)
#   coral exec     → Runs in SERVICE CONTAINER (via nsenter)
```

---

## Environment Variables

- `CORAL_CONFIG` - Override config directory (default: `~/.coral`)
- `CORAL_COLONY_ID` - Override active colony

---

**For detailed documentation, see [CLI.md](./CLI.md)**
