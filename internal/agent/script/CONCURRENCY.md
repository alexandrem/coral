# DuckDB Concurrent Read Access for Scripts

## Overview

The SDK server provides concurrent read access to the agent's local DuckDB database for all running TypeScript scripts. This document explains how the concurrency model works and how it enables many scripts to access live data simultaneously.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│ Deno Script 1 (high-latency-alert.ts)                      │
│   await coral.metrics.getPercentile("payments", 0.99)       │
└───────────────┬─────────────────────────────────────────────┘
                │ HTTP GET localhost:9003/metrics/percentile
                ▼
┌─────────────────────────────────────────────────────────────┐
│ Deno Script 2 (correlation-analysis.ts)                    │
│   await coral.db.query("SELECT * FROM otel_spans_local")    │
└───────────────┬─────────────────────────────────────────────┘
                │ HTTP POST localhost:9003/db/query
                ▼
┌─────────────────────────────────────────────────────────────┐
│ SDK Server (HTTP Proxy)                                    │
│  - Listens on localhost:9003                                │
│  - Connection pool with 20 max connections                  │
│  - Request timeout: 30s                                     │
│  - Concurrency tracking & monitoring                        │
└───────────────┬─────────────────────────────────────────────┘
                │ SQL queries via sql.DB
                ▼
┌─────────────────────────────────────────────────────────────┐
│ DuckDB Connection Pool (Read-Only)                         │
│  - access_mode=read_only                                    │
│  - MaxOpenConns: 20                                         │
│  - MaxIdleConns: 5                                          │
│  - ConnMaxLifetime: 5 minutes                               │
└───────────────┬─────────────────────────────────────────────┘
                │ File I/O
                ▼
┌─────────────────────────────────────────────────────────────┐
│ DuckDB Database File                                        │
│  /var/lib/coral/duckdb/agent.db                             │
│  - Written by agent telemetry/metrics collectors            │
│  - Read by SDK server (read-only mode)                      │
│  - Live data: ~1hr retention                                │
└─────────────────────────────────────────────────────────────┘
```

## DuckDB Concurrency Model

### Read-Only Mode

The SDK server opens DuckDB with `access_mode=read_only`:

```go
connStr := fmt.Sprintf("%s?access_mode=read_only&threads=4", s.dbPath)
db, err := sql.Open("duckdb", connStr)
```

**Key Properties:**
- **Multiple concurrent readers**: DuckDB allows unlimited read-only connections
- **No write locks**: Read-only mode guarantees no interference with agent writers
- **Isolated transactions**: Each query runs in its own read snapshot
- **Consistent reads**: Readers see a consistent view of the database at query start time

### Writer Side (Agent)

The agent's main process writes to DuckDB via:
- **Telemetry receiver**: Inserts OTLP spans into `otel_spans_local`
- **Beyla collector**: Inserts HTTP/gRPC/SQL metrics into `beyla_*_metrics`
- **System metrics collector**: Inserts CPU/memory metrics into `system_metrics_local`

**Writer Configuration:**
- Single writer process (the agent)
- WAL mode enabled for concurrent write batching
- Periodic commits (e.g., every 5 seconds)
- TTL-based cleanup (deletes data older than 1 hour)

### Read-Write Coordination

DuckDB's concurrency model:
1. **Writers** acquire exclusive locks during commits (brief, milliseconds)
2. **Readers** never block writers (they read from snapshots)
3. **Writers** never block readers (reads use MVCC snapshots)

This means:
- Scripts can query while the agent is inserting data
- Scripts see data as of their query start time (snapshot isolation)
- No deadlocks or lock contention between readers and writers

## Connection Pooling

The SDK server maintains a connection pool to handle concurrent script requests:

```go
db.SetMaxOpenConns(20)   // Max concurrent queries
db.SetMaxIdleConns(5)    // Keep connections warm
db.SetConnMaxLifetime(5 * time.Minute)
db.SetConnMaxIdleTime(1 * time.Minute)
```

### Pool Sizing

**MaxOpenConns = 20** is chosen to:
- Support up to 20 concurrent query requests from all scripts
- Avoid overwhelming DuckDB with too many connections
- Leave headroom for agent's own queries

**Example Scenario:**
- 5 scripts running concurrently
- Each script queries every 30 seconds
- Average query time: 100ms
- Concurrent queries: ~5 queries at peak
- Pool utilization: 25% (5/20)

If more than 20 concurrent requests arrive, Go's `database/sql` package will:
1. Queue requests until a connection becomes available
2. Timeout after 30 seconds (per our `QueryContext` timeout)
3. Log warnings when queuing occurs

### Monitoring

The `/health` endpoint exposes connection pool stats:

```bash
curl http://localhost:9003/health
```

```json
{
  "status": "ok",
  "active_requests": 3,
  "db_stats": {
    "open_connections": 5,
    "in_use": 3,
    "idle": 2
  }
}
```

**Metrics:**
- `open_connections`: Total connections in the pool
- `in_use`: Connections currently executing queries
- `idle`: Connections waiting in the pool
- `active_requests`: HTTP requests being processed

## Query Timeouts

All queries have context-based timeouts to prevent resource exhaustion:

```go
ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
defer cancel()

rows, err := s.db.QueryContext(ctx, query)
```

**Timeout Values:**
- General queries: 30 seconds
- Metric queries: 10 seconds
- System metric queries: 5 seconds
- Health checks: 2 seconds

If a script issues a slow query (e.g., full table scan), it will timeout rather than blocking other scripts.

## Resource Limits

### Per-Request Limits

- **Max rows returned**: 10,000 rows per query
- **Query timeout**: 30 seconds max
- **Request body size**: Standard HTTP limits

```go
const maxRows = 10000
for rows.Next() && len(results) < maxRows {
    // Scan row...
}
```

### Per-Script Limits

Each Deno script has its own resource limits (enforced by Deno):
- **Memory**: 512MB max (via `--max-old-space-size`)
- **Execution time**: 5 minutes max (enforced by executor)
- **Network**: localhost:9003 only (no external access)

### Global Limits

- **Max concurrent scripts**: 5 (enforced by executor)
- **Max concurrent HTTP requests**: Unlimited (Go HTTP server)
- **Max concurrent DB queries**: 20 (enforced by connection pool)

## Live Data Access

Scripts access live data with ~1 hour retention:

### Data Flow

```
┌──────────────────────────────────────────────────────────┐
│ Service Process (e.g., payments API)                    │
└────────────┬─────────────────────────────────────────────┘
             │ OTLP spans, HTTP metrics
             ▼
┌──────────────────────────────────────────────────────────┐
│ Agent (Writer)                                           │
│  - Receives telemetry                                    │
│  - Batches inserts every 5s                              │
│  - Inserts into DuckDB tables                            │
│  - Deletes data older than 1hr (TTL cleanup)             │
└────────────┬─────────────────────────────────────────────┘
             │ Periodic INSERTs
             ▼
┌──────────────────────────────────────────────────────────┐
│ DuckDB (agent.db)                                        │
│  - otel_spans_local (traces)                             │
│  - beyla_http_metrics (RED metrics)                      │
│  - system_metrics_local (host metrics)                   │
└────────────┬─────────────────────────────────────────────┘
             │ Read-only queries
             ▼
┌──────────────────────────────────────────────────────────┐
│ SDK Server (Reader)                                      │
│  - Queries on behalf of scripts                          │
│  - Returns JSON results                                  │
└────────────┬─────────────────────────────────────────────┘
             │ HTTP responses
             ▼
┌──────────────────────────────────────────────────────────┐
│ Deno Scripts (Consumers)                                 │
│  - Process metrics, traces, system data                  │
│  - Emit alerts and events                                │
└──────────────────────────────────────────────────────────┘
```

### Data Freshness

- **Write latency**: Data appears in DuckDB within 5 seconds of generation
- **Query latency**: Scripts see data as of query start time
- **Snapshot isolation**: Scripts see consistent data even during writes

**Example:**
1. At `T=0s`: Service generates trace span
2. At `T=5s`: Agent commits span to DuckDB
3. At `T=6s`: Script queries for recent spans
4. Result: Script sees the span (6s > 5s write time)

## Concurrency Best Practices

### For Script Authors

1. **Use time-range filters** to limit data scanned:
   ```typescript
   await coral.db.query(`
     SELECT * FROM otel_spans_local
     WHERE start_time > now() - INTERVAL '5 minutes'
   `);
   ```

2. **Add LIMIT clauses** to prevent large result sets:
   ```typescript
   await coral.db.query(`
     SELECT * FROM otel_spans_local
     ORDER BY start_time DESC
     LIMIT 100
   `);
   ```

3. **Use indexes** when available (DuckDB auto-indexes timestamp columns)

4. **Avoid full table scans** on large tables

5. **Cache results** when appropriate instead of repeated queries:
   ```typescript
   const p99 = await coral.metrics.getPercentile("payments", 0.99);
   // Cache p99 for 30 seconds, don't re-query every second
   ```

### For SDK Server Operators

1. **Monitor connection pool utilization** via `/health` endpoint

2. **Adjust pool size** if seeing connection queuing:
   ```go
   db.SetMaxOpenConns(40)  // Increase if needed
   ```

3. **Watch for slow queries** in logs:
   ```
   {"level":"warn","duration":25000,"msg":"Slow query detected"}
   ```

4. **Set query timeouts** appropriately for workload

## Failure Modes & Recovery

### Database Lock Contention

**Symptom**: Queries timeout or slow down significantly

**Cause**: Writer holding exclusive lock for too long

**Mitigation**:
- Agent commits in small batches (5s intervals)
- Read-only mode prevents write locks from affecting readers
- Query timeouts prevent indefinite blocking

### Connection Pool Exhaustion

**Symptom**: HTTP 500 errors, "too many connections" in logs

**Cause**: More than 20 concurrent queries

**Mitigation**:
- Increase `MaxOpenConns` in SDK server
- Reduce script query frequency
- Optimize slow queries

### Out of Memory

**Symptom**: Script or SDK server crashes

**Cause**: Large query results (e.g., millions of rows)

**Mitigation**:
- 10,000 row limit enforced per query
- Scripts have 512MB memory limit (Deno)
- Use pagination for large datasets

## Performance Characteristics

### Query Latency

Typical query latencies (DuckDB read-only, 1M rows):

| Query Type | Latency | Notes |
|------------|---------|-------|
| Indexed lookup (timestamp range) | 5-10ms | Fast path |
| Aggregation (COUNT, AVG) | 50-100ms | Parallel scan |
| Full table scan | 500ms-2s | Avoid if possible |
| Complex JOIN | 100ms-1s | Depends on data size |

### Throughput

With 20 concurrent connections:
- **Point queries**: ~2000 qps (100 queries/sec per connection)
- **Aggregations**: ~200 qps (10 queries/sec per connection)
- **Full scans**: ~20 qps (1 query/sec per connection)

### Scalability

The system scales horizontally:
- Each agent has its own SDK server
- Each SDK server serves only local scripts
- No cross-agent queries (data is local)
- Colony aggregates results if needed

## Comparison to Alternatives

### Alternative 1: Direct DuckDB Access from Scripts

**Pros**:
- Lower latency (no HTTP overhead)
- Direct SQL access

**Cons**:
- Deno doesn't have DuckDB driver
- Security risk (scripts could open in read-write mode)
- Harder to enforce query limits and timeouts
- Harder to monitor and debug

### Alternative 2: Replicated Read-Only Database

**Pros**:
- Zero contention between readers and writers
- Can scale readers independently

**Cons**:
- Replication lag (data not live)
- 2x storage overhead
- Complexity of keeping replicas in sync

### Alternative 3: Query via gRPC to Agent

**Pros**:
- Consistent with other agent RPCs
- Strong typing with protobuf

**Cons**:
- Higher overhead (protobuf encoding)
- More complex for scripts (need gRPC client)
- Harder to expose arbitrary SQL queries

### Our Choice: HTTP Proxy with Read-Only Pool

**Why this approach wins**:
- ✅ Simple HTTP/JSON interface for scripts
- ✅ Read-only mode ensures safety
- ✅ Connection pooling handles concurrency
- ✅ Easy to monitor and debug
- ✅ Works with standard Deno `fetch()`
- ✅ Low latency (localhost HTTP)
- ✅ Live data (no replication lag)

## Future Optimizations

### 1. Query Result Caching

Cache frequently-queried metrics in memory:

```go
type Cache struct {
    mu    sync.RWMutex
    data  map[string]CacheEntry
}

type CacheEntry struct {
    value      interface{}
    expiresAt  time.Time
}
```

**Benefits**:
- Reduce DuckDB load for repeated queries
- Lower latency for hot paths (percentiles, error rates)

### 2. Prepared Statements

Reuse prepared statements for common queries:

```go
stmt, err := db.Prepare("SELECT * FROM otel_spans_local WHERE service_name = ? AND start_time > ?")
```

**Benefits**:
- Faster query planning
- Reduced memory allocations

### 3. Streaming Results

Stream large result sets instead of buffering:

```go
encoder := json.NewEncoder(w)
for rows.Next() {
    var row Row
    rows.Scan(&row)
    encoder.Encode(row)
}
```

**Benefits**:
- Lower memory usage
- Faster time-to-first-byte

### 4. Read Replicas

For very high query loads, create a read-only replica:

```bash
# Periodically copy database to read-only replica
cp /var/lib/coral/duckdb/agent.db /var/lib/coral/duckdb/agent-replica.db
```

**Benefits**:
- Zero impact on writer
- Unlimited read scalability

## Summary

The SDK server's DuckDB concurrency model provides:

- ✅ **Many concurrent readers**: 20+ simultaneous queries
- ✅ **Live data access**: See data within seconds of generation
- ✅ **Read-write safety**: Read-only mode prevents interference
- ✅ **Resource limits**: Timeouts, row limits, connection pooling
- ✅ **Simple interface**: HTTP/JSON, works with Deno fetch()
- ✅ **Low latency**: Localhost HTTP, sub-100ms queries
- ✅ **Monitoring**: Health endpoint with pool stats

This design enables natural language-driven debugging with AI-deployed scripts that safely and efficiently query live observability data.
