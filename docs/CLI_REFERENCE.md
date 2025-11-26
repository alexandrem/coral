# Coral CLI Quick Reference

**See [CLI.md](./CLI.md) for detailed examples, concepts, and troubleshooting.**

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
coral agent start [--config <file>] [--colony-id <id>] [--colony-url <url>]
coral agent status
coral agent stop
```

---

## Service Connections

```bash
# Connect agent to services
coral connect <service-spec>...

# Format: name:port[:health][:type]
# Examples:
coral connect frontend:3000
coral connect api:8080:/health:http
coral connect frontend:3000 api:8080:/health redis:6379

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

## Live Debugging (SDK mode) - Coming Soon

```bash
# Attach probes
coral debug attach <service> --function <name> --duration <time>
coral debug trace <service> --path <path> --duration <time>

# Manage probes
coral debug list <service>
coral debug detach <service> [--all]
coral debug logs <service>
```

---

## Diagnostic Commands

```bash
# Execute commands on agent hosts
coral exec <service> <command>

# Examples:
coral exec api "netstat -an | grep ESTABLISHED"
coral exec api "ps aux | grep node"
coral exec api "lsof -i :8080"
```

---

## Common Query Patterns

```sql
-- Recent errors (telemetry)
SELECT trace_id, name, service_name, duration_ms
FROM spans
WHERE status = 'error' AND timestamp > now() - INTERVAL '1 hour'
ORDER BY timestamp DESC LIMIT 20;

-- HTTP error rate (Beyla)
SELECT service_name, http_status_code, COUNT(*) as count
FROM beyla_http_metrics_local
WHERE http_status_code >= 500 AND timestamp > now() - INTERVAL '10 minutes'
GROUP BY service_name, http_status_code;

-- P99 latency by endpoint (Beyla)
SELECT http_route,
       PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY latency_bucket_ms) as p99_ms
FROM beyla_http_metrics_local
WHERE timestamp > now() - INTERVAL '10 minutes'
GROUP BY http_route
ORDER BY p99_ms DESC LIMIT 10;

-- Slow database queries (Beyla)
SELECT table_name, sql_operation, AVG(latency_bucket_ms) as avg_ms
FROM beyla_sql_metrics_local
WHERE timestamp > now() - INTERVAL '10 minutes' AND latency_bucket_ms > 100
GROUP BY table_name, sql_operation
ORDER BY avg_ms DESC;
```

---

## Environment Variables

- `CORAL_CONFIG` - Override config directory (default: `~/.coral`)
- `CORAL_COLONY_ID` - Override active colony

---

**For detailed documentation, see [CLI.md](./CLI.md)**
