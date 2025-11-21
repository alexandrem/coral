# Coral CLI Reference

**Last Updated**: 2025-11-20

---

## Overview

The Coral CLI (`coral`) provides a unified interface for debugging distributed
applications, querying metrics, and managing the Coral mesh network. This
document covers all CLI commands with practical examples.

**Key Commands:**

- `coral init` - Initialize Coral configuration
- `coral agent` - Manage agents (start, status)
- `coral colony` - Manage colony server
- `coral connect` - Connect to services
- `coral ask` - Natural language debugging queries
- `coral duckdb` - Query agent and colony metrics databases
- `coral shell` - Interactive shell on agents

---

## Installation

```bash
# Install Coral CLI
# TODO - for now: make build-dev

# Verify installation
coral version
```

---

## Quick Start

```bash
# 1. Initialize Coral
coral init <colony name>

# 2. Start a colony (central coordinator)
coral colony start

# 3. Start an agent (on each machine you want to monitor)
coral agent start

# 4. Check status
coral status

# 5. Query your infrastructure
coral ask "what services are running?"
```

---

## Command Reference

### `coral init`

Initialize Coral colony configuration in `~/.coral/`.

```bash
coral init <colony name>

# Output:
✓ Created ~/.coral/config.yaml
✓ Generated WireGuard keypair
✓ Configuration complete

Next steps:
  1. Start colony: coral colony start
  2. Start agents: coral agent start
```

**Configuration File** (`~/.coral/config.yaml`):

```yaml
colony:
    url: http://localhost:9000

agent:
    id: agent-<hostname>
    colony_url: http://localhost:9000
```

---

### `coral agent`

Manage Coral agents that monitor services.

#### `coral agent start`

Start an agent daemon.

```bash
# Start agent with default config
coral agent start

# Start with custom config
coral agent start --config /etc/coral/agent.yaml

# Start with specific colony
coral agent start --colony-url http://colony.example.com:9000
```

#### `coral agent status`

Show agent status and connected services.

```bash
coral agent status

# Output:
Agent: agent-prod-1
Status: healthy
Mesh IP: 10.42.1.5
Colony: http://localhost:9000

Connected Services:
NAME           STATUS    PORT   LAST CHECK
api-server     healthy   8080   2s ago
auth-service   healthy   8081   1s ago
database       healthy   5432   3s ago
```

---

### `coral colony`

Manage the colony server (central coordinator).

#### `coral colony start`

Start the colony server.

```bash
# Start colony with default config
coral colony start

# Start with custom config
coral colony start --config /etc/coral/colony.yaml

# Start with specific port
coral colony start --port 9000
```

---

### `coral connect`

Connect to a service through an agent.

```bash
# Connect to a service by name and port
coral connect api-server:8080

# Connect to specific agent
coral connect api-server:8080 --agent agent-prod-1
```

---

### `coral ask`

Natural language queries about your infrastructure.

```bash
# General questions
coral ask "what services are running?"
coral ask "why is the API slow?"
coral ask "show me recent errors"

# Service-specific
coral ask "what's wrong with the auth service?"
coral ask "show me database query performance"

# Historical analysis
coral ask "what happened 2 hours ago?"
coral ask "show me error trends this week"
```

---

## `coral duckdb` - SQL Metrics Queries

The `coral duckdb` command provides direct SQL access to all agent databases using
DuckDB's remote attach feature. Query real-time agent metrics (~1 hour
retention) including telemetry spans, Beyla metrics, and any custom databases.

**Key Features:**

- **Zero serialization overhead** - Native DuckDB binary protocol
- **Full SQL power** - Use complete DuckDB SQL dialect
- **Multi-database support** - Access telemetry, Beyla, and custom databases
- **Interactive shell** - REPL with readline, history, multi-line queries
- **Multiple output formats** - Table, CSV, JSON
- **Multi-agent queries** - Join data from multiple agents

**Architecture:**

- Agents serve local DuckDB files at `http://agent:9001/duckdb/<database-name>`
  - `telemetry.duckdb` - OTLP telemetry spans (RFD 025)
  - `beyla.duckdb` - Beyla HTTP/gRPC/SQL metrics (RFD 032)
  - Custom databases registered by agents
- Colony serves aggregated database at `http://colony:9000/duckdb/metrics.duckdb` (RFD 046 - future)
- CLI attaches via HTTP using DuckDB's `httpfs` extension
- Database discovery via `/duckdb` endpoint (lists available databases)

---

### `coral duckdb list-agents` (alias: `list`)

List all agents with their available databases.

```bash
coral duckdb list-agents
# or
coral duckdb list
```

**Output:**

```
AGENT ID        STATUS    LAST SEEN           DATABASES
agent-prod-1    healthy   2025-11-20 10:30    telemetry.duckdb, beyla.duckdb
agent-prod-2    healthy   2025-11-20 10:29    telemetry.duckdb, beyla.duckdb
agent-dev-1     degraded  2025-11-20 09:15    telemetry.duckdb
agent-test-1    healthy   2025-11-20 10:28    -

Total: 4 agents (3 with databases, 5 total databases)
```

**What it shows:**

- **Agent ID** - Unique identifier for each agent
- **Status** - Health status (healthy/degraded/unhealthy)
- **Last Seen** - Last heartbeat timestamp
- **Databases** - Comma-separated list of available databases for querying

---

### `coral duckdb query` - One-Shot Queries

Execute a SQL query against an agent database and print results.

**Syntax:**

```bash
coral duckdb query <agent-id> "<sql>" [--database <db-name>] [--format table|csv|json]
```

**Flags:**

- `--database, -d` - Database name to query (e.g., `telemetry.duckdb`, `beyla.duckdb`)
  - If omitted, uses the first available database
- `--format, -f` - Output format: `table` (default), `csv`, or `json`

#### Basic Query Examples

**Query telemetry spans:**

```bash
# Explicitly specify database
coral duckdb query agent-prod-1 "SELECT * FROM spans LIMIT 10" --database telemetry.duckdb

# Auto-detect first available database
coral duckdb query agent-prod-1 "SELECT * FROM spans LIMIT 10"
# Using database: telemetry.duckdb
```

**Query recent HTTP requests (Beyla):**

```bash
coral duckdb query agent-prod-1 "SELECT * FROM beyla_http_metrics_local LIMIT 10" -d beyla.duckdb
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

#### Telemetry Span Queries

**Query error spans:**

```bash
coral duckdb query agent-prod-1 \
  "SELECT trace_id, span_id, name, status, duration_ms
   FROM spans
   WHERE status = 'error'
   ORDER BY timestamp DESC
   LIMIT 20" \
  -d telemetry.duckdb
```

**High-latency operations:**

```bash
coral duckdb query agent-prod-1 \
  "SELECT name, service_name, AVG(duration_ms) as avg_duration_ms, COUNT(*) as count
   FROM spans
   WHERE timestamp > now() - INTERVAL '10 minutes'
     AND duration_ms > 500
   GROUP BY name, service_name
   ORDER BY avg_duration_ms DESC" \
  -d telemetry.duckdb
```

#### Aggregation Queries (Beyla)

**Request count by service:**

```bash
coral duckdb query agent-prod-1 \
  "SELECT service_name, COUNT(*) as request_count
   FROM beyla_http_metrics_local
   WHERE timestamp > now() - INTERVAL '5 minutes'
   GROUP BY service_name
   ORDER BY request_count DESC" \
  -d beyla.duckdb
```

**Output:**

```
service_name     request_count
api-server       15478
auth-service     8234
payment-gateway  3421

(3 rows)
```

**P99 latency by endpoint:**

```bash
coral duckdb query agent-prod-1 \
  "SELECT
     http_route,
     COUNT(*) as count,
     AVG(latency_bucket_ms) as avg_latency_ms,
     PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY latency_bucket_ms) as p99_latency_ms
   FROM beyla_http_metrics_local
   WHERE timestamp > now() - INTERVAL '10 minutes'
   GROUP BY http_route
   ORDER BY p99_latency_ms DESC
   LIMIT 10"
```

#### Error Analysis

**Find 5xx errors:**

```bash
coral duckdb query agent-prod-1 \
  "SELECT
     timestamp,
     service_name,
     http_method,
     http_route,
     http_status_code,
     count
   FROM beyla_http_metrics_local
   WHERE http_status_code >= 500
   ORDER BY timestamp DESC
   LIMIT 20"
```

**Error rate by service:**

```bash
coral duckdb query agent-prod-1 \
  "SELECT
     service_name,
     COUNT(*) as total_requests,
     SUM(CASE WHEN http_status_code >= 500 THEN 1 ELSE 0 END) as errors,
     (SUM(CASE WHEN http_status_code >= 500 THEN 1 ELSE 0 END)::FLOAT / COUNT(*) * 100) as error_rate_pct
   FROM beyla_http_metrics_local
   WHERE timestamp > now() - INTERVAL '1 hour'
   GROUP BY service_name"
```

#### gRPC Metrics

**Query gRPC methods:**

```bash
coral duckdb query agent-prod-1 \
  "SELECT
     service_name,
     grpc_method,
     COUNT(*) as call_count,
     AVG(latency_bucket_ms) as avg_latency_ms,
     grpc_status_code
   FROM beyla_grpc_metrics_local
   WHERE timestamp > now() - INTERVAL '15 minutes'
   GROUP BY service_name, grpc_method, grpc_status_code
   ORDER BY call_count DESC"
```

#### SQL Query Metrics

**Slow database queries:**

```bash
coral duckdb query agent-prod-1 \
  "SELECT
     table_name,
     sql_operation,
     COUNT(*) as query_count,
     AVG(latency_bucket_ms) as avg_latency_ms,
     MAX(latency_bucket_ms) as max_latency_ms
   FROM beyla_sql_metrics_local
   WHERE timestamp > now() - INTERVAL '10 minutes'
     AND latency_bucket_ms > 100
   GROUP BY table_name, sql_operation
   ORDER BY avg_latency_ms DESC"
```

#### CSV Export

Export query results to CSV for analysis in spreadsheets or BI tools.

```bash
coral duckdb query agent-prod-1 \
  "SELECT
     service_name,
     http_route,
     COUNT(*) as count,
     AVG(latency_bucket_ms) as avg_latency
   FROM beyla_http_metrics_local
   WHERE timestamp > now() - INTERVAL '1 hour'
   GROUP BY service_name, http_route" \
  --format csv > metrics.csv
```

**Output (metrics.csv):**

```csv
service_name,http_route,count,avg_latency
api-server,/checkout,1547,45.2
api-server,/products,3421,12.5
auth-service,/login,892,23.1
```

#### JSON Export

Export as JSON for programmatic processing.

```bash
coral duckdb query agent-prod-1 \
  "SELECT * FROM beyla_http_metrics_local LIMIT 5" \
  --format json | jq '.'
```

**Output:**

```json
[
    {
        "timestamp": "2025-11-20 10:25:14",
        "service_name": "api-server",
        "http_method": "POST",
        "http_route": "/checkout",
        "http_status_code": 200,
        "latency_bucket_ms": 45.2,
        "count": 1547
    },
    ...
]
```

---

### `coral duckdb shell` - Interactive SQL Shell

Open an interactive DuckDB REPL for exploring agent databases.

**Syntax:**

```bash
coral duckdb shell <agent-id> [--database <db-name>]
coral duckdb shell --agents <agent-1>,<agent-2>,<agent-3> [--database <db-name>]
```

**Flags:**

- `--database, -d` - Database name to attach (e.g., `telemetry.duckdb`, `beyla.duckdb`)
  - If omitted, uses the first available database
- `--agents` - Comma-separated list of agent IDs for multi-agent queries

#### Basic Shell Usage

**Start interactive shell with auto-detected database:**

```bash
coral duckdb shell agent-prod-1
```

**Output:**

```
Using database: telemetry.duckdb (agent: agent-prod-1)
DuckDB interactive shell. Type '.exit' to quit, '.help' for help.

Attached agent database: agent_agent_prod_1

duckdb>
```

**Start shell with specific database:**

```bash
coral duckdb shell agent-prod-1 --database beyla.duckdb
# or
coral duckdb shell agent-prod-1 -d telemetry.duckdb
```

#### Meta-Commands

The shell supports special meta-commands (prefix with `.`):

```sql
-- List all tables
duckdb
> .tables
beyla_http_metrics_local
beyla_grpc_metrics_local
beyla_sql_metrics_local

-- Show attached databases
duckdb> .databases
agent_agent_prod_1

-- Show help
duckdb> .help
Meta-commands:
  .tables     - List all tables in attached databases
  .databases  - Show attached databases
  .help       - Show this help message
  .exit       - Exit shell
  .quit       - Exit shell

Query syntax:
  -
End queries with semicolon (;)
  - Use Ctrl+C to cancel current query
  - Use Ctrl+D or .exit to quit

-- Exit shell
duckdb> .exit
```

#### Multi-Line Queries

The shell supports multi-line SQL queries:

```sql
duckdb
>
SELECT
    ..> service_name, ..> COUNT (*) as count, ..> AVG (latency_bucket_ms) as avg_latency
    ..>
FROM beyla_http_metrics_local
    ..>
WHERE timestamp
    > now() - INTERVAL '5 minutes'
    ..
    >
GROUP BY service_name;

service_name
count  avg_latency
api-server       1547   45.2
auth-service     892    23.1
(2 rows in 45ms)

duckdb>
```

#### Interactive Exploration Example

**Full debugging session:**

```sql
duckdb
> -- Start by listing tables
duckdb> .tables
beyla_http_metrics_local
beyla_grpc_metrics_local
beyla_sql_metrics_local

duckdb> -- Check recent HTTP traffic
duckdb>
SELECT service_name,
       COUNT(*) as request_count
    ..>
FROM beyla_http_metrics_local
         ..>
WHERE timestamp
    > now() - INTERVAL '5 minutes'
    ..
    >
GROUP BY service_name;

service_name
request_count
api-server       1547
auth-service     892
(2 rows in 23ms)

duckdb> -- Investigate errors
duckdb>
SELECT
    ..> timestamp, ..> http_route, ..> http_status_code, ..> count
    ..>
FROM beyla_http_metrics_local
    ..>
WHERE http_status_code >= 500
    ..
    >
ORDER BY timestamp DESC
    ..> LIMIT 5;

timestamp            http_route  http_status_code  count
2025-11-20 10:25:14  /checkout   500               3
2025-11-20 10:24:58  /products   503               1
(2 rows in 12ms)

duckdb> -- Check latency for specific endpoint
duckdb>
SELECT
    ..> AVG (latency_bucket_ms) as avg_ms, ..> PERCENTILE_CONT(0.99) WITHIN
GROUP (ORDER BY latency_bucket_ms) as p99_ms
    ..>
FROM beyla_http_metrics_local
    ..>
WHERE http_route = '/checkout';

avg_ms
p99_ms
45.2    250.0
(1 row in 8ms)

duckdb> .exit
```

#### Multi-Agent Queries

Attach multiple agent databases and query across them. When using `--agents`, the same database is attached from each agent.

```bash
# Query same database across multiple agents
coral duckdb shell --agents agent-prod-1,agent-prod-2,agent-prod-3 --database beyla.duckdb
```

**Output:**

```
Using database: beyla.duckdb (agent: agent-prod-1)
Using database: beyla.duckdb (agent: agent-prod-2)
Using database: beyla.duckdb (agent: agent-prod-3)
DuckDB interactive shell. Type '.exit' to quit, '.help' for help.

Attached databases: agent_agent_prod_1, agent_agent_prod_2, agent_agent_prod_3

duckdb>
```

**Query across all agents:**

```sql
duckdb
> -- Aggregate requests from all agents
duckdb>
SELECT service_name,
       SUM(count) as total_requests
    ..>
FROM (
         ..>   SELECT * FROM agent_agent_prod_1.beyla_http_metrics_local
    ..>   UNION ALL
    ..>   SELECT * FROM agent_agent_prod_2.beyla_http_metrics_local
    ..>   UNION ALL
    ..>   SELECT * FROM agent_agent_prod_3.beyla_http_metrics_local
    ..> ) ..>
WHERE timestamp
    > now() - INTERVAL '10 minutes'
    ..
    >
GROUP BY service_name;

service_name
total_requests
api-server       45892
auth-service     23451
payment-gateway  12087
(3 rows in 125ms)
```

---

### Available Tables and Schema

#### Telemetry Database (`telemetry.duckdb`)

The telemetry database stores OTLP spans from instrumented applications.

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
  AND timestamp > now() - INTERVAL '1 hour';

-- Trace latency breakdown
SELECT trace_id, span_id, name, duration_ms
FROM spans
WHERE trace_id = 'abc123...'
ORDER BY timestamp;
```

---

#### Beyla Database (`beyla.duckdb`)

The Beyla database stores eBPF-collected HTTP, gRPC, and SQL metrics.

##### `beyla_http_metrics_local` (Agent)

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

#### `beyla_grpc_metrics_local` (Agent)

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

#### `beyla_sql_metrics_local` (Agent)

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

The CLI automatically discovers available databases from agents using the `/duckdb` HTTP endpoint.

**How it works:**

1. CLI queries agent at `http://<agent-mesh-ip>:9001/duckdb`
2. Agent returns JSON list: `{"databases": ["telemetry.duckdb", "beyla.duckdb"]}`
3. If `--database` not specified, CLI uses first available database
4. Database list shown in `coral duckdb list-agents` output

**Manual discovery:**

```bash
# List all agents and their databases
coral duckdb list-agents

# Query specific agent's databases via HTTP
curl http://10.42.1.5:9001/duckdb
# Returns: {"databases":["telemetry.duckdb","beyla.duckdb"]}
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
# Should return: {"databases":["telemetry.duckdb","beyla.duckdb"]}

# Check firewall rules
# Agent must allow port 9001 from WireGuard mesh (not public internet)

# Verify agent database path is configured
# Check agent.yaml for database_path settings:
#   telemetry.database_path: ~/.coral/agent/telemetry.duckdb
#   beyla.db_path: ~/.coral/agent/beyla.duckdb
```

#### "query timeout"

**Problem:** Large query takes too long.

**Solutions:**

- Add time filter: `WHERE timestamp > now() - INTERVAL '1 hour'`
- Limit results: `LIMIT 1000`
- Use aggregations instead of raw data
- Query colony for historical data (larger retention)

---

## Related Documentation

- **RFD 039** - DuckDB Remote Query CLI (detailed specification)
- **RFD 025** - OTLP Telemetry Receiver (spans in `telemetry.duckdb`)
- **RFD 032** - Beyla RED Metrics Integration (metrics in `beyla.duckdb`)
- **RFD 038** - CLI-to-Agent Direct Mesh Connectivity
- **RFD 046** - Colony DuckDB Remote Query (historical data - future)
- **DuckDB Documentation** - https://duckdb.org/docs/

---

## Examples Repository

See `examples/queries/` for more SQL query examples:

- `examples/queries/performance-analysis.sql`
- `examples/queries/error-detection.sql`
- `examples/queries/capacity-planning.sql`
