# Coral CLI Guide

**For quick command syntax, see [CLI_REFERENCE.md](./CLI_REFERENCE.md)**

**Last Updated**: 2025-11-20

---

## Overview

The Coral CLI (`coral`) provides a unified interface for debugging distributed
applications, querying metrics, and managing the Coral mesh network. This guide
covers concepts, workflows, and detailed examples.

**Quick command reference:** See [CLI_REFERENCE.md](./CLI_REFERENCE.md)

**Key Capabilities:**

- **Mesh networking** - WireGuard-based secure connectivity
- **Config management** - kubectl-style context switching
- **Observability** - Real-time metrics and distributed tracing
- **AI-powered debugging** - Natural language queries with your own LLM
- **Direct SQL access** - Query agent databases with DuckDB
- **Container execution** - Execute commands in service containers via nsenter

---

## Installation

```bash
# Build from source (for now)
make build-dev

# Verify installation
coral version
```

---

## Quick Start Workflow

**Initial Setup:**

1. **Initialize** - `coral init <colony-name>` creates `~/.coral/config.yaml`
   and WireGuard keypair
2. **Start Colony** - `coral colony start` launches the central coordinator
3. **Start Agents** - `coral agent start` on each monitored machine
4. **Connect Services** - `coral connect frontend:3000 api:8080` or use
   `--connect` at startup
5. **Query** - `coral ask "what services are running?"`

**Agent Startup Modes:**

```bash
# Passive mode (no monitoring, use 'coral connect' later)
coral agent start

# Connect services at startup
coral agent start --connect frontend:3000 --connect api:8080:/health

# Monitor ALL processes (eBPF auto-discovery)
coral agent start --monitor-all
```

See [CLI_REFERENCE.md](./CLI_REFERENCE.md) for command syntax.

---

## Configuration Management

Coral uses a kubectl-inspired config system for managing multiple colonies (
environments).

**Configuration Priority:**

1. `CORAL_COLONY_ID` environment variable (highest)
2. Project config (`.coral/config.yaml` in current directory)
3. Global config (`~/.coral/config.yaml`)

**Workflow Example:**

```bash
# List available colonies
coral config get-contexts

# Switch to production colony
coral config use-context myapp-prod-xyz789

# Verify current context
coral config current-context

# View merged configuration with source annotations
coral config view

# Validate all colony configs
coral config validate
```

See [CLI_REFERENCE.md](./CLI_REFERENCE.md) for all `coral config` commands.

---

## AI-Powered Debugging

Coral integrates with your own LLM (OpenAI, Anthropic, or local Ollama) to
provide
natural language debugging queries.

**Setup:**

```bash
# First-time configuration
coral ask config
# Choose provider: OpenAI, Anthropic, or Ollama
# Provide API key (stored locally in ~/.coral/)
```

**Privacy & Cost:**

- Uses YOUR LLM API keys (never sent to Coral servers)
- Runs locally as a Genkit agent on your workstation
- Connects to Colony as MCP server for observability data
- You control model choice, costs, and data privacy

**Example Workflows:**

```bash
# Investigate performance issues
coral ask "Why is the API slow?"
# → Queries recent metrics, identifies bottlenecks

# Debug errors
coral ask "Show me errors in the last hour"
# → Retrieves error spans, correlates with metrics

# Understand system state
coral ask "What changed in the last hour?"
# → Compares current vs historical data

# Get service health overview
coral ask "Are there any unhealthy services?"
# → Checks agent status, service health
```

See [CLI_REFERENCE.md](./CLI_REFERENCE.md) for `coral ask` syntax.

---

## Live Debugging

Coral provides real-time debugging capabilities using eBPF uprobes, allowing you
to trace function calls and request paths without modifying your code.

**Key Features:**

- **Zero-code instrumentation** - Attach probes to running services
- **Low overhead** - eBPF-based collection with minimal impact
- **Production safe** - Time-limited sessions and safety checks
- **AI-integrated** - Analyze traces and results with `coral ask`

**Commands:**

```bash
# Attach a uprobe to a specific function
coral debug attach payment-service --function processPayment --duration 5m

# Trace an HTTP request path across services
coral debug trace api-gateway --path /checkout --duration 2m

# List active debug sessions
coral debug list

# Stop a debug session
coral debug detach <session-id>

# Query historical debug results
coral debug query payment-service --function processPayment --since 1h
```

**Workflow Example:**

1. **Identify a bottleneck:**
   `coral ask "Why is checkout slow?"` -> Suggests tracing `/checkout`.

2. **Start a trace:**
   `coral debug trace api-gateway --path /checkout`

3. **Analyze results:**
   `coral debug query api-gateway --function handleCheckout`

4. **Deep dive:**
   `coral debug attach payment-service --function processPayment --capture-args`

See [CLI_REFERENCE.md](./CLI_REFERENCE.md) for full command syntax.

---

## SQL Metrics Queries with DuckDB

Coral provides direct SQL access to agent databases using DuckDB, enabling
powerful
real-time analysis without serialization overhead.

**Why DuckDB?**

- **Zero overhead** - Native binary protocol over HTTP
- **Full SQL** - Complete DuckDB SQL dialect with analytics functions
- **Real-time** - Query live agent data (~1 hour retention)
- **Multi-source** - Join data across multiple agents
- **Flexible output** - Table, CSV, or JSON formats

**Available Databases:**

- `metrics.duckdb` - Agent database (OTLP spans + eBPF HTTP/gRPC/SQL metrics)
- Custom databases registered by agents

**Common Use Cases:**

```bash
# Discover what's available
coral duckdb list-agents

# One-shot queries
coral duckdb query agent-prod-1 "SELECT * FROM spans WHERE status='error' LIMIT 10"

# Interactive exploration
coral duckdb shell agent-prod-1

# Multi-agent analysis
coral duckdb shell --agents agent-1,agent-2,agent-3
```

See [CLI_REFERENCE.md](./CLI_REFERENCE.md) for command syntax.

---

### Architecture Overview

**How it works:**

1. Agents serve DuckDB files at `http://agent:9001/duckdb/<database-name>`
2. CLI discovers databases via `/duckdb` endpoint
3. CLI attaches databases using DuckDB's `httpfs` extension
4. Queries execute directly against agent storage

**Data Retention:**

- **Agents**: ~1 hour of metrics (real-time debugging)
- **Colony** (future): 30 days HTTP/gRPC, 14 days SQL (historical analysis)

---

### Discovering Databases

List all agents and their available databases:

```bash
coral duckdb list-agents

# Example output:
# AGENT ID        STATUS    LAST SEEN           DATABASES
# agent-prod-1    healthy   2025-11-20 10:30    metrics.duckdb
# agent-prod-2    healthy   2025-11-20 10:29    metrics.duckdb
```

---

### Query Examples

#### Basic Queries

**Query telemetry spans:**

```bash
# Auto-detect first available database
coral duckdb query agent-prod-1 "SELECT * FROM spans LIMIT 10"

# Explicitly specify database
coral duckdb query agent-prod-1 "SELECT * FROM spans LIMIT 10" -d metrics.duckdb
```

**Query recent HTTP requests (Beyla):**

```bash
coral duckdb query agent-prod-1 "SELECT * FROM beyla_http_metrics_local LIMIT 10" -d metrics.duckdb
```

**Output (table format):**

```
timestamp            service_name  http_method  http_route  http_status_code  latency_bucket_ms  count
2025-11-20 10:25:14  api-server    POST         /checkout   200               45.2               1547
2025-11-20 10:25:14  api-server    GET          /products   200               12.5               3421
2025-11-20 10:25:13  auth-service  POST         /login      200               23.1               892
2025-11-20 10:25:12  api-server    POST         /checkout   500               250.0              3
...

(10 rows)
```

#### Performance Analysis

**Find high-latency operations (telemetry):**

```bash
coral duckdb query agent-prod-1 \
  "SELECT name, service_name, AVG(duration_ms) as avg_ms, COUNT(*) as count
   FROM spans
   WHERE timestamp > now() - INTERVAL '10 minutes' AND duration_ms > 500
   GROUP BY name, service_name
   ORDER BY avg_ms DESC"
```

**P99 latency by endpoint (Beyla):**

```bash
coral duckdb query agent-prod-1 \
  "SELECT http_route,
          PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY latency_bucket_ms) as p99_ms
   FROM beyla_http_metrics_local
   WHERE timestamp > now() - INTERVAL '10 minutes'
   GROUP BY http_route
   ORDER BY p99_ms DESC LIMIT 10"
```

#### Error Detection

**Find error spans (telemetry):**

```bash
coral duckdb query agent-prod-1 \
  "SELECT trace_id, name, service_name, duration_ms
   FROM spans
   WHERE status = 'error' AND timestamp > now() - INTERVAL '1 hour'
   ORDER BY timestamp DESC LIMIT 20"
```

**5xx error rate (Beyla):**

```bash
coral duckdb query agent-prod-1 \
  "SELECT service_name,
          COUNT(*) as total,
          SUM(CASE WHEN http_status_code >= 500 THEN 1 ELSE 0 END) as errors
   FROM beyla_http_metrics_local
   WHERE timestamp > now() - INTERVAL '1 hour'
   GROUP BY service_name"
```

#### Data Export

**Export to CSV:**

```bash
coral duckdb query agent-prod-1 \
  "SELECT service_name, http_route, COUNT(*) as count
   FROM beyla_http_metrics_local
   GROUP BY service_name, http_route" \
  --format csv > metrics.csv
```

**Export to JSON:**

```bash
coral duckdb query agent-prod-1 \
  "SELECT * FROM beyla_http_metrics_local LIMIT 100" \
  --format json | jq '.'
```

---

### Interactive SQL Shell

For exploratory analysis, use the interactive shell with readline support,
command history,
and multi-line query editing.

**Start a shell:**

```bash
# Single agent
coral duckdb shell agent-prod-1

# Multiple agents (for cross-agent queries)
coral duckdb shell --agents agent-prod-1,agent-prod-2,agent-prod-3
```

**Shell meta-commands:**

- `.tables` - List all tables
- `.databases` - Show attached databases
- `.help` - Show help
- `.exit` - Exit shell

**Example debugging session:**

```sql
duckdb
> .tables
beyla_http_metrics_local
beyla_grpc_metrics_local
spans

duckdb> -- Check recent traffic
SELECT service_name, COUNT(*) as requests
FROM beyla_http_metrics_local
WHERE timestamp > now() - INTERVAL '5 minutes'
GROUP BY service_name;

service_name
requests
api-server      1547
auth-service    892
(2 rows in 23ms)

duckdb> -- Find errors
SELECT timestamp, http_route, http_status_code
FROM beyla_http_metrics_local
WHERE http_status_code >= 500
ORDER BY timestamp DESC LIMIT 5;

timestamp            http_route  http_status_code
2025-11-20 10:25:14  /checkout   500
2025-11-20 10:24:58  /products   503
(2 rows in 12ms)

duckdb> .exit
```

**Multi-agent queries:**

When querying multiple agents, databases are prefixed with agent IDs:

```sql
-- Aggregate across all agents
SELECT service_name, SUM(count) as total
FROM (SELECT *
      FROM agent_agent_prod_1.beyla_http_metrics_local
      UNION ALL
      SELECT *
      FROM agent_agent_prod_2.beyla_http_metrics_local
      UNION ALL
      SELECT *
      FROM agent_agent_prod_3.beyla_http_metrics_local)
WHERE timestamp > now() - INTERVAL '10 minutes'
GROUP BY service_name;
```

---

### Available Tables and Schema

#### Agent Database (`metrics.duckdb`)

The agent database stores both OTLP spans and eBPF metrics.

##### `spans` Table

Distributed tracing spans with full OpenTelemetry compatibility.

**Columns:**

- `trace_id` (VARCHAR) - Unique trace identifier
- `span_id` (VARCHAR) - Unique span identifier
- `parent_span_id` (VARCHAR) - Parent span ID (NULL for root spans)
- `name` (VARCHAR) - Span name/operation
- `kind` (VARCHAR) - Span kind (server, client, internal, producer, consumer)
- `status` (VARCHAR) - Span status (ok, error, unset)
- `service_name` (VARCHAR) - Service that generated the span
- `timestamp` (TIMESTAMP) - Span start time
- `duration_ms` (DOUBLE) - Span duration in milliseconds
- `attributes` (JSON) - Span attributes (tags)
- `resource_attributes` (JSON) - Resource attributes
- `scope_name` (VARCHAR) - Instrumentation scope name
- `scope_version` (VARCHAR) - Instrumentation scope version
- `created_at` (TIMESTAMP) - When span was stored

**Example queries:**

```sql
-- Find traces with errors
SELECT DISTINCT trace_id, name, service_name
FROM spans
WHERE status = 'error'
  AND timestamp
    > now() - INTERVAL '1 hour';

-- Trace latency breakdown
SELECT trace_id, span_id, name, duration_ms
FROM spans
WHERE trace_id = 'abc123...'
ORDER BY timestamp;
```

---

##### Beyla Metrics Tables

eBPF-collected HTTP, gRPC, and SQL metrics (stored in `metrics.duckdb`).

**`beyla_http_metrics_local` Table**

HTTP request metrics with RED (Rate, Errors, Duration) data.

**Columns:**

- `timestamp` (TIMESTAMP) - Request timestamp
- `service_name` (VARCHAR) - Service name
- `http_method` (VARCHAR) - HTTP method (GET, POST, etc.)
- `http_route` (VARCHAR) - HTTP route/endpoint
- `http_status_code` (SMALLINT) - Status code (200, 404, 500, etc.)
- `latency_bucket_ms` (DOUBLE) - Latency in milliseconds
- `count` (BIGINT) - Number of requests in this bucket
- `attributes` (JSON) - Additional metadata
- `created_at` (TIMESTAMP) - When metric was stored

**`beyla_grpc_metrics_local` Table**

gRPC method call metrics.

**Columns:**

- `timestamp` (TIMESTAMP) - Call timestamp
- `service_name` (VARCHAR) - Service name
- `grpc_method` (VARCHAR) - gRPC method name
- `grpc_status_code` (SMALLINT) - gRPC status code
- `latency_bucket_ms` (DOUBLE) - Latency in milliseconds
- `count` (BIGINT) - Number of calls
- `attributes` (JSON) - Additional metadata
- `created_at` (TIMESTAMP) - When metric was stored

**`beyla_sql_metrics_local` Table**

Database query metrics.

**Columns:**

- `timestamp` (TIMESTAMP) - Query timestamp
- `service_name` (VARCHAR) - Service name
- `sql_operation` (VARCHAR) - Operation type (SELECT, INSERT, UPDATE, DELETE)
- `table_name` (VARCHAR) - Table name
- `latency_bucket_ms` (DOUBLE) - Latency in milliseconds
- `count` (BIGINT) - Number of queries
- `attributes` (JSON) - Additional metadata
- `created_at` (TIMESTAMP) - When metric was stored

---

### Common Query Patterns

#### Performance Analysis

**Top 10 slowest endpoints:**

```sql
SELECT http_route,
       COUNT(*) as count,
  AVG(latency_bucket_ms) as avg_ms,
  MAX(latency_bucket_ms) as max_ms
FROM beyla_http_metrics_local
WHERE timestamp > now() - INTERVAL '1 hour'
GROUP BY http_route
ORDER BY avg_ms DESC
    LIMIT 10;
```

**Latency percentiles by service:**

```sql
SELECT service_name,
       PERCENTILE_CONT(0.50) WITHIN GROUP (ORDER BY latency_bucket_ms) as p50_ms,
  PERCENTILE_CONT(0.95) WITHIN
GROUP (ORDER BY latency_bucket_ms) as p95_ms,
    PERCENTILE_CONT(0.99) WITHIN
GROUP (ORDER BY latency_bucket_ms) as p99_ms
FROM beyla_http_metrics_local
WHERE timestamp > now() - INTERVAL '30 minutes'
GROUP BY service_name;
```

#### Traffic Analysis

**Request volume over time (5-minute buckets):**

```sql
SELECT DATE_TRUNC('minute', timestamp) as time_bucket,
       service_name,
       SUM(count)                      as total_requests
FROM beyla_http_metrics_local
WHERE timestamp > now() - INTERVAL '1 hour'
GROUP BY DATE_TRUNC('minute', timestamp), service_name
ORDER BY time_bucket DESC;
```

**HTTP status code distribution:**

```sql
SELECT http_status_code,
       COUNT(*) as count,
  (COUNT(*)::FLOAT / SUM(COUNT(*)) OVER () * 100) as percentage
FROM beyla_http_metrics_local
WHERE timestamp > now() - INTERVAL '30 minutes'
GROUP BY http_status_code
ORDER BY count DESC;
```

#### Database Performance

**Top database tables by query count:**

```sql
SELECT table_name,
       sql_operation,
       COUNT(*)               as query_count,
       AVG(latency_bucket_ms) as avg_latency_ms
FROM beyla_sql_metrics_local
WHERE timestamp > now() - INTERVAL '1 hour'
GROUP BY table_name, sql_operation
ORDER BY query_count DESC
    LIMIT 10;
```

**Slow database queries:**

```sql
SELECT
    timestamp, service_name, table_name, sql_operation, latency_bucket_ms
FROM beyla_sql_metrics_local
WHERE timestamp
    > now() - INTERVAL '30 minutes'
  AND latency_bucket_ms
    > 1000
ORDER BY latency_bucket_ms DESC;
```

---

### Database Discovery

The CLI automatically discovers available databases from agents using the
`/duckdb` HTTP endpoint.

**How it works:**

1. CLI queries agent at `http://<agent-mesh-ip>:9001/duckdb`
2. Agent returns JSON list: `{"databases": ["metrics.duckdb"]}`
3. If `--database` not specified, CLI uses first available database
4. Database list shown in `coral duckdb list-agents` output

**Manual discovery:**

```bash
# List all agents and their databases
coral duckdb list-agents

# Query specific agent's databases via HTTP
curl http://100.64.0.5:9001/duckdb
# Returns: {"databases":["metrics.duckdb"]}
```

**Registering custom databases:**

Agents can register custom DuckDB databases by modifying the agent startup code:

```go
// In agent initialization
duckdbHandler.RegisterDatabase("custom.duckdb", "/path/to/custom.duckdb")
```

Any registered database becomes queryable via the CLI.

---

### Tips and Best Practices

#### Query Performance

**Use time filters:**

```sql
-- Good: Limits data scanned
WHERE timestamp > now() - INTERVAL '1 hour'

-- Bad: Scans entire table
WHERE true
```

**Use indexes:**

```sql
-- Indexes on timestamp and service_name columns make these fast:
WHERE timestamp > now() - INTERVAL '5 minutes'
  AND service_name = 'api-server'
```

#### Shell Productivity

**Command history:**

- Press `↑` / `↓` to navigate command history
- History saved to `~/.coral/duckdb_history`

**Cancel queries:**

- Press `Ctrl+C` to cancel a running query
- Query buffer is preserved

**Multi-line editing:**

- Shell auto-continues lines until semicolon
- Use `Ctrl+C` to clear multi-line buffer

#### Data Retention

**Agent retention:**

- Agents keep ~1 hour of metrics
- Use agents for real-time debugging
- Data automatically cleaned up

**Colony retention (RFD 046 - future):**

- Colony stores 30 days of HTTP/gRPC metrics
- Colony stores 14 days of SQL metrics
- Use colony for historical analysis

---

### Troubleshooting

#### "database not found"

**Problem:** Specified database not available on agent.

**Solutions:**

```bash
# List available databases for all agents
coral duckdb list-agents

# Check specific agent's databases via HTTP
curl http://<agent-mesh-ip>:9001/duckdb

# Verify agent is healthy
coral agent status

# Check WireGuard mesh connectivity
ping <agent-mesh-ip>
```

**Common causes:**

- Database not configured in agent (check `agent.yaml`)
- Agent using in-memory database (`:memory:`) - must use file path
- Database file deleted or moved

#### "failed to attach database"

**Problem:** Cannot connect to agent HTTP endpoint.

**Solutions:**

```bash
# Verify agent HTTP server is running and databases are registered
curl http://<agent-mesh-ip>:9001/duckdb
# Should return: {"databases":["metrics.duckdb"]}

# Check firewall rules
# Agent must allow port 9001 from WireGuard mesh (not public internet)

# Verify agent database path is configured
# Check agent.yaml for database_path setting:
#   database_path: ~/.coral/agent/metrics.duckdb
```

#### "query timeout"

**Problem:** Large query takes too long.

**Solutions:**

- Add time filter: `WHERE timestamp > now() - INTERVAL '1 hour'`
- Limit results: `LIMIT 1000`
- Use aggregations instead of raw data
- Query colony for historical data (larger retention)

---

## Agent Shell Access

Coral provides interactive shell access to agent environments for debugging and
diagnostics. This enables direct access to the agent's container/process with
full terminal capabilities.

**Key Features:**

- **Interactive terminal** - Full PTY support with readline, signals, and
  terminal resize
- **Debugging utilities** - Network tools (tcpdump, netcat, curl), process
  inspection (ps, top)
- **Direct database access** - Query agent's local DuckDB database
- **Agent resolution** - Connect by agent ID or explicit address
- **Audit logging** - All sessions are recorded with session IDs

**Security Considerations:**

⚠️ **WARNING**: Agent shells run with elevated privileges:

- Access to CRI socket (can exec into containers)
- eBPF monitoring capabilities
- WireGuard mesh network access
- Agent configuration and storage access

All sessions are fully audited and recorded.

### Basic Usage

**Connect to local agent:**

```bash
coral shell
```

**Connect to specific agent by ID:**

```bash
coral shell --agent hostname-api-1
```

**Connect to agent by explicit address:**

```bash
coral shell --agent-addr 100.64.0.5:9001
```

**Specify user ID for audit:**

```bash
coral shell --user-id alice@company.com
```

See [CLI_REFERENCE.md](./CLI_REFERENCE.md) for command syntax.

---

### Agent Resolution

The `coral shell` command supports multiple ways to specify the target agent:

**1. Auto-discovery (local agent):**

```bash
coral shell
# Connects to localhost:9001 (default agent port)
```

**2. Agent ID (via colony registry):**

```bash
coral shell --agent hostname-api-1
# Colony resolves agent ID → mesh IP (e.g., 100.64.0.5)
# Requires colony to be running
```

**3. Explicit address:**

```bash
coral shell --agent-addr 100.64.0.5:9001
# Direct connection to mesh IP
# No colony lookup required
```

**Agent ID disambiguation:**

When multiple agents serve the same service, use agent ID for unambiguous
targeting:

```bash
# List agents to find IDs
coral colony agents

# Connect to specific agent
coral shell --agent hostname-api-2
```

---

### Available Tools

Agent shells provide access to debugging utilities:

**Network diagnostics:**

- `tcpdump` - Packet capture and analysis
- `netcat` (nc) - TCP/UDP connections
- `curl` - HTTP requests
- `dig` / `nslookup` - DNS queries
- `ss` / `netstat` - Socket statistics
- `ip` - Network interface configuration

**Process inspection:**

- `ps` - Process listing
- `top` - Real-time process monitoring
- `lsof` - Open files and sockets

**Database access:**

- `duckdb` - Query agent's local database directly

**File access:**

- Agent configuration files
- Agent logs
- Agent data storage

---

### Example Workflows

#### Network Debugging

**Check listening ports:**

```bash
coral shell --agent hostname-api-1

# In shell:
ss -tlnp
# Shows all listening TCP ports with process names
```

**Capture HTTP traffic:**

```bash
coral shell --agent hostname-api-1

# In shell:
tcpdump -i any -A 'tcp port 8080' -c 20
# Captures 20 HTTP packets on port 8080
```

**Test connectivity:**

```bash
coral shell --agent hostname-api-1

# In shell:
curl -v http://localhost:8080/health
# Tests local service health endpoint
```

#### Process Debugging

**Find resource-intensive processes:**

```bash
coral shell --agent hostname-api-1

# In shell:
top -bn1 | head -20
# Shows top processes by CPU/memory
```

**Check if service is running:**

```bash
coral shell --agent hostname-api-1

# In shell:
ps auxwwf | grep nginx
# Shows nginx processes with full command lines
```

#### Database Queries

**Query agent's local database:**

```bash
coral shell --agent hostname-api-1

# In shell:
duckdb ~/.coral/agent/metrics.duckdb

# In DuckDB:
SELECT * FROM beyla_http_metrics_local
WHERE timestamp > now() - INTERVAL '5 minutes'
LIMIT 10;
```

---

### Session Management

**Terminal features:**

- **Readline support** - Command history, line editing (Ctrl+A, Ctrl+E, etc.)
- **Signal handling** - Ctrl+C, Ctrl+Z work as expected
- **Terminal resize** - Window resize events are forwarded
- **Exit codes** - Shell exit code is preserved

**Exiting the shell:**

```bash
# Type exit or press Ctrl+D
exit

# Or use Ctrl+D (EOF)
^D
```

**Session audit:**

All shell sessions are logged with:

- Session ID (UUID)
- User ID (from `--user-id` or `$USER`)
- Agent ID
- Start/end timestamps
- Commands executed (future: RFD 042)

---

### Security and RBAC

**Current security model:**

- Shell access requires WireGuard mesh connectivity
- Agent validates source IP (must be from colony or authorized peer)
- All sessions are audited with session IDs
- User ID tracking for accountability

**Future enhancements (RFD 043):**

- RBAC policies for shell access
- Approval workflows for production access
- Command whitelisting/blacklisting
- Session recording and playback

---

### Troubleshooting

#### "failed to connect to agent"

**Problem:** Cannot establish connection to agent.

**Solutions:**

```bash
# Verify agent is running
coral agent status

# Check WireGuard mesh connectivity
ping 100.64.0.5

# Verify agent HTTP server is listening
curl http://100.64.0.5:9001/health

# Check colony is running (for agent ID resolution)
coral colony status
```

#### "agent not found"

**Problem:** Agent ID not found in colony registry.

**Solutions:**

```bash
# List all connected agents
coral colony agents

# Verify agent ID is correct
coral colony agents | grep hostname-api

# Use explicit address instead
coral shell --agent-addr 100.64.0.5:9001
```

#### "permission denied"

**Problem:** Agent rejects connection.

**Solutions:**

- Verify source IP is in agent's AllowedIPs (WireGuard config)
- Check agent logs for rejection reason
- Ensure colony is running (for colony-mediated routing)

---

## Container Execution

Coral provides the ability to execute commands within service container
namespaces using `nsenter`. This enables access to container-mounted files,
configs, and volumes that are not visible from the agent's host filesystem.

**Key Features:**

- **Container filesystem access** - Read configs, logs, and volumes as mounted
  in the container
- **Namespace isolation** - Enter mount, PID, network, and other Linux
  namespaces
- **Service-based targeting** - Execute by service name, not container ID
- **Multi-deployment support** - Works with docker-compose sidecars, Kubernetes
  sidecars, and DaemonSets
- **Audit logging** - All executions are recorded with session IDs

**Security Considerations:**

⚠️ **WARNING**: Container exec requires elevated privileges:

- CAP_SYS_ADMIN capability (for nsenter)
- CAP_SYS_PTRACE capability (for /proc inspection)
- Access to container PIDs via shared PID namespace or hostPID

All executions are fully audited and recorded.

### Basic Usage

**Execute in service container:**

```bash
coral exec <service> <command> [args...]
```

**Target specific agent:**

```bash
coral exec <service> --agent <agent-id> <command> [args...]
```

**Execute with timeout:**

```bash
coral exec <service> --timeout 60 <command> [args...]
```

See docs/CLI_REFERENCE.md:180 for command syntax.

---

### Key Differences: coral shell vs coral exec

Understanding when to use each command is critical:

| Command       | Target                      | Filesystem View                     | Use Case                                              |
|---------------|-----------------------------|-------------------------------------|-------------------------------------------------------|
| `coral shell` | Agent host environment      | Agent's filesystem                  | Host diagnostics, network debugging, agent management |
| `coral exec`  | Service container (nsenter) | Container's mounted volumes/configs | Container configs, app files, mounted volumes         |

**Examples:**

```bash
# coral shell - Agent host environment
coral shell --agent api-1
# In shell: ps aux, tcpdump -i any, ss -tulpn
# Sees: agent's processes, host network, host filesystem

# coral exec - Service container namespace
coral exec api cat /app/config.yaml
# Executes: nsenter into container's mount namespace
# Sees: container's filesystem, mounted configs, volumes
```

**When to use coral shell:**

- Network diagnostics: `tcpdump`, `netstat`, `ss -tulpn`
- Process inspection: `ps aux`, `top`, `pgrep`
- Host filesystem: agent logs, system files
- System commands: `uptime`, `free -h`, `df -h`

**When to use coral exec:**

- App configs: `/app/config.yaml`, `/etc/nginx/nginx.conf`
- Mounted volumes: `/data`, `/logs`, `/var/lib`
- Container environment: `env`, `pwd`, `id`
- App-specific files: `/usr/share/nginx/html`

---

### Service Resolution

The `coral exec` command supports multiple ways to specify the target:

**1. By service name (automatic agent resolution):**

```bash
coral exec nginx cat /etc/nginx/nginx.conf
# Colony resolves "nginx" service → agent mesh IP
```

**2. By service name + specific agent:**

```bash
coral exec nginx --agent hostname-api-1 cat /app/config.yaml
# Targets specific agent running the nginx service
```

**3. By service name + explicit address:**

```bash
coral exec nginx --agent-addr 100.64.0.5:9001 cat /app/config.yaml
# Direct connection to agent mesh IP
# No colony lookup required
```

**Service disambiguation:**

When multiple agents serve the same service, specify the agent ID:

```bash
# List agents to find IDs
coral colony agents

# Target specific agent
coral exec nginx --agent hostname-api-2 cat /app/config.yaml
```

---

### Common Use Cases

#### Read Application Configs

**Read nginx config from container:**

```bash
coral exec nginx cat /etc/nginx/nginx.conf
```

**Read application config:**

```bash
coral exec api-server cat /app/config.yaml
```

**Verify environment variables:**

```bash
coral exec api-server env
```

#### Inspect Mounted Volumes

**List files in data volume:**

```bash
# Be careful to use -- notation when command has hyphens
coral exec api-server -- ls -la /data
```

**Check volume permissions:**

```bash
coral exec api-server -- ls -ld /data /logs /uploads
```

**Find large files in volumes:**

```bash
coral exec api-server -- du -sh /data/*
```

#### Debug Container State

**Check running processes (with pid namespace):**

```bash
coral exec nginx --namespaces mnt,pid ps aux
```

**Verify working directory:**

```bash
coral exec app --working-dir /app pwd
```

**Test file accessibility:**

```bash
coral exec api-server test -r /app/config.yaml && echo "readable"
```

#### Multi-Container Pods

**Execute in specific container:**

```bash
coral exec web --container nginx cat /etc/nginx/nginx.conf
coral exec web --container app cat /app/config.yaml
```

---

### Advanced Options

#### Namespace Selection

By default, `coral exec` enters only the mount namespace (`mnt`). You can
specify additional namespaces:

```bash
# Mount namespace only (default)
coral exec nginx cat /etc/nginx/nginx.conf

# Mount + PID namespaces
coral exec nginx --namespaces mnt,pid ps aux

# All namespaces (full isolation)
coral exec nginx --namespaces mnt,pid,net,ipc,uts ps aux
```

**Available namespaces:**

- `mnt` - Mount namespace (filesystem)
- `pid` - PID namespace (processes)
- `net` - Network namespace
- `ipc` - IPC namespace
- `uts` - UTS namespace (hostname)
- `cgroup` - Cgroup namespace

#### Working Directory

```bash
# Execute in specific directory
coral exec app --working-dir /app ls -la

# Verify current directory
coral exec app --working-dir /data pwd
# Output: /data
```

#### Environment Variables

```bash
# Pass environment variables
coral exec api --env DEBUG=true --env LOG_LEVEL=debug env

# Use for debugging
coral exec api --env VERBOSE=1 /app/healthcheck.sh
```

#### Timeout Control

```bash
# Default timeout: 30 seconds
coral exec api cat /app/config.yaml

# Longer timeout for slow commands
coral exec logs-processor --timeout 120 -- find /data -name "*.log"

# Maximum timeout: 300 seconds (5 minutes)
coral exec backup --timeout 300 tar czf /tmp/backup.tar.gz /data
```

---

### Troubleshooting

#### "service not found"

**Problem:** Cannot resolve service name to agent.

**Solutions:**

```bash
# List available services
coral colony agents

# Verify colony is running
coral colony status

# Use explicit agent address
coral exec nginx --agent-addr 100.64.0.5:9001 cat /etc/nginx/nginx.conf
```

#### "failed to execute command in container"

**Problem:** nsenter failed to enter container namespace.

**Common causes:**

- **Missing capabilities**: Agent lacks CAP_SYS_ADMIN or CAP_SYS_PTRACE
- **PID namespace not shared**: Agent cannot see container PIDs
- **nsenter not available**: Binary not in agent container

**Solutions:**

```bash
# Verify agent has required capabilities
# Check docker-compose.yml or K8s manifest for:
#   cap_add: [SYS_ADMIN, SYS_PTRACE]

# Verify PID namespace sharing
# Docker-compose: pid: "service:app"
# Kubernetes: shareProcessNamespace: true OR hostPID: true

# Verify nsenter is available in agent container
coral shell --agent api-1
# In shell: which nsenter
```

#### "no container PID found"

**Problem:** Agent cannot detect container process.

**Solutions:**

- Verify shared PID namespace configuration
- Check that application container is running
- For DaemonSet mode, ensure `hostPID: true` is set
- Use verbose mode to debug: `CORAL_VERBOSE=1 coral exec ...`

#### "timeout exceeded"

**Problem:** Command took longer than timeout.

**Solutions:**

```bash
# Increase timeout
coral exec logs-processor --timeout 120 find /data -name "*.log"

# For very long operations, use coral shell instead
coral shell --agent api-1
# In shell: find /data -name "*.log"
```

---

## Related Documentation

- **RFD 026** - Shell Command Implementation (agent shell access)
- **RFD 056** - Container Exec via nsenter (container namespace execution)
- **RFD 039** - DuckDB Remote Query CLI (detailed specification)
- **RFD 025** - OTLP Telemetry Receiver (spans in `metrics.duckdb`)
- **RFD 032** - Beyla RED Metrics Integration (HTTP/gRPC/SQL metrics in
  `metrics.duckdb`)
- **RFD 038** - CLI-to-Agent Direct Mesh Connectivity
- **RFD 045** - MCP Shell Exec Tool (one-off command execution via MCP)
- **RFD 046** - Colony DuckDB Remote Query (historical data - future)
- **DuckDB Documentation** - https://duckdb.org/docs/

---

## Examples Repository

See `examples/queries/` for more SQL query examples:

- `examples/queries/performance-analysis.sql`
- `examples/queries/error-detection.sql`
- `examples/queries/capacity-planning.sql`
