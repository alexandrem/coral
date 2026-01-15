# Coral CLI Quick Reference

**See also:**

- [CLI.md](./CLI.md) - Detailed examples, concepts, and troubleshooting
- [CLI_MCP_MAPPING.md](./CLI_MCP_MAPPING.md) - CLI to MCP tool mapping for
  AI/LLM integration

---

## Global Flags & Options

### Output Format

All commands that produce output support the `--format` / `-o` flag:

```bash
--format <format>    # Output format: table (default), json, csv, yaml
-o <format>          # Short form
```

**Supported formats:**
- `table` - Human-readable tabular output (default)
- `json` - JSON format for programmatic consumption
- `csv` - CSV format for spreadsheet import (where applicable)
- `yaml` - YAML format (where applicable)

**Examples:**
```bash
coral colony list --format json
coral colony status -o yaml
coral config get-contexts --format table
```

### Verbose Output

Global verbose flag available to all commands:

```bash
--verbose    # Show additional details
-v           # Short form
```

### Colony Selection

Standard colony ID parameter across all commands:

```bash
--colony <id>    # Specify colony ID (overrides auto-detection)
-c <id>          # Short form
```

---

## Setup & Configuration

```bash
# Initialize
coral init <colony-name>

# Configuration management
coral config get-contexts [--format <format>]
coral config current-context [--verbose]
coral config use-context <colony-id>
coral config view [--colony <id>] [--raw]
coral config validate [--format <format>]
coral config delete-context <colony-id>

# Version
coral version
```

---

## Colony & Agent Management

```bash
# Colony (central coordinator)
coral colony start [--daemon] [--port <port>] [--config <file>]
coral colony status [--format <format>]
coral colony stop

# Agent (local observer)
coral agent start [--config <file>] [--colony <id>] [--connect <service>...] [--monitor-all]
coral agent status [--format <format>]
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
coral ask "<question>" [--format <format>] [--model <provider:model>] [--debug] [--dry-run]

# Flags:
#   --format <format>  Output format (table, json)
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

## Unified Query Commands

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

# Historical CPU profiles (RFD 072)
coral query cpu-profile --service <name> [--since <duration>] [--until <duration>] [--build-id <id>] [--format folded|json]

# Historical memory profiles (RFD 077 - coming soon)
coral query memory-profile --service <name> [--since <duration>] [--until <duration>] [--build-id <id>] [--show-growth] [--show-types]

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

# Examples - CPU Profiles:
coral query cpu-profile --service api --since 1h                    # Last hour of CPU profiles
coral query cpu-profile --service api --since 2h --until 1h         # Specific time range
coral query cpu-profile --service api --build-id abc123 --since 24h # Filter by build ID
coral query cpu-profile --service api --since 1h | flamegraph.pl > cpu.svg  # Generate flame graph

# Examples - Memory Profiles (RFD 077 - coming soon):
coral query memory-profile --service api --since 1h --show-growth --show-types
```

**What you get:**

- **Summary**: Health status (healthy/degraded/critical), error rates, latency,
  request counts
- **Metrics**: HTTP/gRPC/SQL RED metrics with P50/P95/P99 latency percentiles
  from eBPF + OTLP
- **Traces**: Distributed trace spans with parent-child relationships, source
  annotations (eBPF/OTLP)
- **Logs**: Application logs from OTLP with filtering and search
- **CPU Profiles**: Historical CPU profile data from continuous profiling (RFD 072)
- **Memory Profiles**: Historical memory allocation data (RFD 077 - coming soon)
- **Automatic merging**: eBPF and OTLP data combined by default with source
  annotations
- **No SQL needed**: High-level commands for common observability patterns

> **Note**: Old `coral query ebpf` commands are deprecated. Use the unified
`coral query` commands above.

---

## Focused Query Commands (RFD 076)

**Scriptable queries optimized for CLI and TypeScript SDK use.**

These commands provide focused, specific queries compared to the unified query
interface. They're designed for:

- TypeScript SDK usage (`@coral/sdk`)
- CLI scripting and automation
- Quick percentile calculations
- Service discovery

```bash
# Service discovery
coral query services [--namespace <name>] [--since <duration>] [--source <type>]

# Percentile queries (precise DuckDB quantile calculations)
coral query metrics <service> --metric <name> --percentile <0-100>

# Raw SQL queries with safety guardrails
coral query sql "<sql-query>" [--max-rows <n>]

# Examples - Service discovery:
coral query services                           # List all services (registry + telemetry)
coral query services --namespace production    # Filter by namespace
coral query services --since 24h               # Extend telemetry lookback
coral query services --source registered       # Only explicitly connected services
coral query services --source observed         # Only auto-observed from telemetry
coral query services --shadow                  # Alias for --source observed

# See: docs/SERVICE_DISCOVERY.md for architecture details

# Examples - Percentile queries:
coral query metrics payments --metric http.server.duration --percentile 99
coral query metrics api --metric http.server.duration --percentile 50
coral query metrics orders --metric http.server.duration --percentile 95

# Examples - Raw SQL:
coral query sql "SELECT service_name, COUNT(*) FROM beyla_http_metrics GROUP BY service_name"
coral query sql "SELECT * FROM beyla_http_metrics WHERE http_status_code >= 500 LIMIT 10"
coral query sql "SELECT service_name, AVG(duration_ns) FROM beyla_http_metrics GROUP BY service_name" --max-rows 100
```

**What you get:**

- **services**: Service names, instance counts, last seen timestamps
- **metrics --percentile**: Precise percentile values (P50, P95, P99, etc.)
  using DuckDB's `quantile_cont` function
- **sql**: Direct DuckDB queries with automatic row limits and safety validation

**Use cases:**

- Quick percentile checks without full metric analysis
- Service discovery for scripting
- Custom SQL queries for advanced analysis
- TypeScript SDK backend (same API used by `@coral/sdk`)

---

## TypeScript Scripting

**Execute custom analysis scripts locally with sandboxed Deno runtime.**

Write TypeScript scripts that query colony data for custom dashboards, alerts,
and correlation analysis.

```bash
# Execute TypeScript script
coral run <script.ts> [flags]

# Flags:
#   --timeout <seconds>    Script timeout (default: 60)
#   --watch                Re-run on file changes

# Examples:
coral run latency-report.ts                    # Run once
coral run dashboard.ts --timeout 120           # Custom timeout
coral run monitor.ts --watch                   # Watch mode for development
```

### TypeScript SDK (@coral/sdk)

Scripts have access to the Coral SDK for querying observability data:

```typescript
#!/usr/bin/env -S coral run

import * as coral from "@coral/sdk";

// Service discovery
const services = await coral.services.list();

// Metrics queries
const p99 = await coral.metrics.getP99("payments", "http.server.duration");
const p95 = await coral.metrics.getP95("payments", "http.server.duration");
const p50 = await coral.metrics.getP50("payments", "http.server.duration");

// Service activity
const activity = await coral.activity.getServiceActivity("payments");
console.log(`Requests: ${activity.requestCount}, Error rate: ${activity.errorRate}`);

// Raw SQL queries
const result = await coral.db.query(`
  SELECT service_name, AVG(duration_ns) as avg_latency
  FROM beyla_http_metrics
  WHERE timestamp > now() - INTERVAL '1 hour'
  GROUP BY service_name
`);
```

### Example Scripts

See `examples/scripts/` directory:

- `latency-report.ts` - Service latency monitoring with P50/P95/P99
- `correlation-analysis.ts` - Cross-service error correlation
- `high-latency-alert.ts` - Anomaly detection and alerts
- `service-activity.ts` - Request counts and error rates
- `sdk-demo-monitor.ts` - Full SDK feature demonstration

### Security & Sandboxing

Scripts run with restricted permissions:

- ✅ **Allowed**: Network access to colony gRPC API only
- ✅ **Allowed**: Read local files (for imports)
- ✅ **Allowed**: Console output (stdout/stderr)
- ❌ **Blocked**: Write to filesystem
- ❌ **Blocked**: Execute shell commands
- ❌ **Blocked**: Access environment variables (except `CORAL_*`)

Deno permissions:
`--allow-net=<colony-addr> --allow-read=./ --allow-env=CORAL_*`

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

## On-Demand Profiling

**Collect performance profiles on-demand from running services.**

```bash
# CPU profiling - Statistical sampling
coral profile cpu --service <name> [--duration <seconds>] [--frequency <hz>] [--format folded|json] [--pod <name>] [--agent-id <id>]

# Memory profiling - Heap allocation tracking (RFD 077 - coming soon)
coral profile memory --service <name> [--duration <seconds>] [--sample-rate <kb>] [--format folded|json]

# Examples - CPU profiling:
coral profile cpu --service api                               # Basic 30s CPU profile
coral profile cpu --service api --duration 60                 # 60 second profile
coral profile cpu --service api --frequency 99                # Custom sampling frequency
coral profile cpu --service api --format folded | flamegraph.pl > cpu.svg  # Generate flame graph
coral profile cpu --service api --pod api-7d8f9c              # Target specific pod

# Examples - Memory profiling (RFD 077 - coming soon):
coral profile memory --service api                            # Basic 30s memory profile
coral profile memory --service api --sample-rate 4096         # Custom sampling rate (4MB)

# Flags:
#   --service <name>       Service name (required)
#   --duration <seconds>   Profiling duration in seconds (default: 30, max: 300)
#   --frequency <hz>       CPU sampling frequency in Hz (default: 99, max: 1000)
#   --sample-rate <kb>     Memory sampling rate in KB (default: 512)
#   --format <type>        Output format: folded (default), json
#   --pod <name>           Specific pod/instance name (optional)
#   --agent-id <id>        Target specific agent (optional, auto-discovered if not provided)
```

**What you get:**

- **CPU Profiles**: Stack traces showing where CPU time is spent (on-demand, high-frequency sampling)
- **Memory Profiles**: Allocation flame graphs showing memory usage patterns (RFD 077)
- **Flame graph compatible**: Outputs folded stack format for flamegraph.pl visualization
- **Low overhead**: ~2-5% CPU overhead during profiling window

**Use cases:**

- Identify CPU hotspots in production
- Track memory allocation patterns
- Generate flame graphs for performance analysis
- Compare before/after optimization changes

**See also:** Use `coral query cpu-profile` and `coral query memory-profile` for historical profiling data.

---

## Live Debugging (SDK mode)

```bash
# Attach probes
coral debug attach <service> --function <name> [--duration <time>] [--capture-args] [--capture-return]
coral debug trace <service> --path <path> [--duration <time>]

# Manage debug sessions
coral debug session list [--service <name>] [--status <status>] [--format text|json|csv]
coral debug session get <session-id> [--format text|json|csv]
coral debug session query <service> --function <name> [--since <duration>] [--format text|json|csv]
coral debug session query <service> --session-id <id> [--format text|json|csv]
coral debug session events <session-id> [--max <n>] [--follow] [--since <duration>]
coral debug session stop <session-id>

# Examples - Session management:
coral debug session list                                    # List all active sessions
coral debug session list --service api                      # List sessions for specific service
coral debug session get abc123                              # Get session metadata
coral debug session query api --function processOrder       # Query results for function
coral debug session query api --session-id abc123           # Query specific session results
coral debug session events abc123 --follow                  # Stream events from session
coral debug session stop abc123                             # Stop a debug session
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
