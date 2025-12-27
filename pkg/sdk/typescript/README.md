# Coral TypeScript SDK

TypeScript SDK for querying Coral observability data from CLI scripts.

## Overview

The Coral TypeScript SDK provides a simple API for querying metrics, traces, and services from Colony's aggregated DuckDB summaries. Scripts run locally via `coral run` with sandboxed Deno execution.

**Key Features:**
- Service discovery API
- Metric percentile queries (P50, P95, P99)
- Raw SQL access to Colony DuckDB
- Connect RPC over HTTP/JSON
- Full TypeScript type definitions

## Installation

The SDK is bundled with Coral CLI - no installation required. Import using relative paths:

```typescript
import * as coral from "../../pkg/sdk/typescript/mod.ts";
```

For published packages (future):
```typescript
import * as coral from "jsr:@coral/sdk";
```

## Quick Start

Create a script that lists service latencies:

```typescript
import * as coral from "../../pkg/sdk/typescript/mod.ts";

const services = await coral.services.list();

for (const svc of services) {
  const p99 = await coral.metrics.getP99(svc.name, "http.server.duration");
  console.log(`${svc.name}: P99 = ${(p99.value / 1_000_000).toFixed(1)}ms`);
}
```

Run it:
```bash
coral run latency-check.ts
```

## API Reference

### Services

Query discovered services and their metadata.

#### `services.list(namespace?: string)`

List all discovered services, optionally filtered by namespace.

**Parameters:**
- `namespace` (optional): Filter by namespace

**Returns:** `Promise<Service[]>`

**Example:**
```typescript
// List all services
const services = await coral.services.list();

// List services in production namespace
const prodServices = await coral.services.list("production");

// Iterate over services
for (const svc of services) {
  console.log(`${svc.name} (${svc.namespace})`);
  console.log(`  Instances: ${svc.instanceCount}`);
  console.log(`  Last seen: ${svc.lastSeen}`);
}
```

**Service Type:**
```typescript
interface Service {
  name: string;          // Service name
  namespace: string;     // Namespace
  instanceCount: number; // Number of instances
  lastSeen?: Date;       // Last activity timestamp
}
```

### Metrics

Query latency percentiles and metrics from Colony.

#### `metrics.getPercentile(service, metric, percentile, timeRangeMs?)`

Get arbitrary percentile for a metric.

**Parameters:**
- `service`: Service name
- `metric`: Metric name (e.g., "http.server.duration")
- `percentile`: Percentile value (0.0 to 1.0)
- `timeRangeMs` (optional): Lookback window in milliseconds (default: 3600000 = 1 hour)

**Returns:** `Promise<MetricValue>`

**Example:**
```typescript
const p90 = await coral.metrics.getPercentile(
  "payments",
  "http.server.duration",
  0.90,
  5 * 60 * 1000  // Last 5 minutes
);

console.log(`P90: ${p90.value / 1_000_000}ms`);
```

#### `metrics.getP99(service, metric, timeRangeMs?)`

Get 99th percentile (convenience wrapper).

**Parameters:**
- `service`: Service name
- `metric`: Metric name
- `timeRangeMs` (optional): Lookback window in milliseconds

**Returns:** `Promise<MetricValue>`

**Example:**
```typescript
const p99 = await coral.metrics.getP99("api", "http.server.duration");
console.log(`P99 latency: ${(p99.value / 1_000_000).toFixed(1)}ms`);
```

#### `metrics.getP95(service, metric, timeRangeMs?)`

Get 95th percentile (convenience wrapper).

#### `metrics.getP50(service, metric, timeRangeMs?)`

Get 50th percentile / median (convenience wrapper).

**MetricValue Type:**
```typescript
interface MetricValue {
  value: number;      // Metric value (nanoseconds for duration metrics)
  unit: string;       // Unit of measurement
  timestamp?: Date;   // Timestamp of measurement
}
```

**Unit Conversion:**
```typescript
// Duration metrics are in nanoseconds - convert to milliseconds
const p99 = await coral.metrics.getP99("api", "http.server.duration");
const p99Ms = p99.value / 1_000_000;

console.log(`P99: ${p99Ms.toFixed(2)}ms`);
```

### Database

Execute raw SQL queries against Colony DuckDB.

#### `db.query(sql, maxRows?)`

Execute arbitrary SQL query.

**Parameters:**
- `sql`: SQL query string
- `maxRows` (optional): Maximum rows to return (default: 1000)

**Returns:** `Promise<QueryResult>`

**Example:**
```typescript
const result = await coral.db.query(`
  SELECT
    service_name,
    COUNT(*) as request_count,
    SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END) as error_count
  FROM ebpf_http_metrics
  WHERE timestamp > now() - INTERVAL '1 hour'
  GROUP BY service_name
  ORDER BY request_count DESC
`);

console.log(`Columns: ${result.columns.join(", ")}`);
for (const row of result.rows) {
  console.log(`${row.service_name}: ${row.request_count} requests, ${row.error_count} errors`);
}
```

**QueryResult Type:**
```typescript
interface QueryResult {
  columns: string[];           // Column names
  rows: Record<string, any>[]; // Array of row objects
  rowCount: number;            // Number of rows returned
}
```

**Available Tables:**
- `ebpf_http_metrics` - HTTP request metrics
- `otel_spans_local` - OpenTelemetry spans
- `otel_metrics_local` - OpenTelemetry metrics

### Traces (Placeholder)

Trace query functions are placeholders for future implementation:

```typescript
// Will be implemented in future phases
await coral.traces.findSlow(service, minDurationNs, timeRangeMs);
await coral.traces.findErrors(service, timeRangeMs);
```

### System (Placeholder)

System metrics functions are placeholders for future implementation:

```typescript
// Will be implemented in future phases
await coral.system.getMetrics();
await coral.system.getCPU();
await coral.system.getMemory();
```

## Configuration

### Colony Address

Scripts connect to Colony via the `CORAL_COLONY_ADDR` environment variable:

```bash
# Default: localhost:9090
export CORAL_COLONY_ADDR=colony.example.com:9090
coral run script.ts
```

Or specify when running:
```bash
CORAL_COLONY_ADDR=colony.prod.internal:9090 coral run script.ts
```

### Custom Configuration

Pass custom configuration to SDK functions:

```typescript
import type { ClientConfig } from "../../pkg/sdk/typescript/client.ts";

const config: ClientConfig = {
  colonyAddr: "custom-colony:9090",
  timeout: 30000,
};

const services = await coral.services.list(undefined, config);
```

## Complete Examples

### Example 1: Service Health Dashboard

```typescript
import * as coral from "../../pkg/sdk/typescript/mod.ts";

console.log("Service Health Dashboard\n");
console.log("=".repeat(80));

const services = await coral.services.list();

for (const svc of services) {
  // Get latency percentiles
  const p50 = await coral.metrics.getP50(svc.name, "http.server.duration");
  const p95 = await coral.metrics.getP95(svc.name, "http.server.duration");
  const p99 = await coral.metrics.getP99(svc.name, "http.server.duration");

  // Get error rate via SQL
  const errorResult = await coral.db.query(`
    SELECT
      COUNT(*) FILTER (WHERE status_code >= 400) as error_count,
      COUNT(*) as total_count
    FROM ebpf_http_metrics
    WHERE service_name = '${svc.name}'
      AND timestamp > now() - INTERVAL '1 hour'
  `);

  const errorCount = Number(errorResult.rows[0]?.error_count || 0);
  const totalCount = Number(errorResult.rows[0]?.total_count || 0);
  const errorRate = totalCount > 0 ? (errorCount / totalCount) * 100 : 0;

  console.log(`\n${svc.name}:`);
  console.log(`  P50: ${(p50.value / 1_000_000).toFixed(1)}ms`);
  console.log(`  P95: ${(p95.value / 1_000_000).toFixed(1)}ms`);
  console.log(`  P99: ${(p99.value / 1_000_000).toFixed(1)}ms`);
  console.log(`  Error Rate: ${errorRate.toFixed(2)}%`);

  // Health status
  if (p99.value / 1_000_000 > 500) {
    console.log(`  ⚠️  WARNING: High latency`);
  }
  if (errorRate > 1) {
    console.log(`  🚨 CRITICAL: High error rate`);
  }
}
```

### Example 2: Cross-Service Correlation

```typescript
import * as coral from "../../pkg/sdk/typescript/mod.ts";

const services = await coral.services.list();
const results = [];

// Collect metrics for all services
for (const svc of services) {
  const p99 = await coral.metrics.getP99(svc.name, "http.server.duration");
  results.push({ service: svc.name, p99: p99.value / 1_000_000 });
}

// Calculate average
const avgP99 = results.reduce((sum, r) => sum + r.p99, 0) / results.length;

// Find outliers (> 2x average)
const outliers = results.filter(r => r.p99 > avgP99 * 2);

if (outliers.length > 0) {
  console.log(`🔍 CORRELATION DETECTED: ${outliers.length} service(s) with abnormal latency`);
  console.log(`Average P99: ${avgP99.toFixed(1)}ms\n`);

  for (const outlier of outliers) {
    console.log(`${outlier.service}:`);
    console.log(`  P99: ${outlier.p99.toFixed(1)}ms (${(outlier.p99 / avgP99).toFixed(1)}x average)`);
  }
}
```

### Example 3: Custom SQL Analytics

```typescript
import * as coral from "../../pkg/sdk/typescript/mod.ts";

// Find slowest endpoints across all services
const slowest = await coral.db.query(`
  SELECT
    service_name,
    http_route,
    PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY latency_bucket_ms) as p99_ms,
    COUNT(*) as request_count
  FROM ebpf_http_metrics
  WHERE timestamp > now() - INTERVAL '1 hour'
  GROUP BY service_name, http_route
  HAVING COUNT(*) > 100
  ORDER BY p99_ms DESC
  LIMIT 10
`);

console.log("Top 10 Slowest Endpoints (last hour):\n");
for (const row of slowest.rows) {
  console.log(`${row.service_name} ${row.http_route}:`);
  console.log(`  P99: ${Number(row.p99_ms).toFixed(1)}ms`);
  console.log(`  Requests: ${row.request_count}`);
}
```

## Security & Sandboxing

Scripts run in Deno's sandboxed environment with restricted permissions:

**Allowed:**
- Network access to Colony gRPC API (`--allow-net=<colony-addr>`)
- Read access to local files (`--allow-read=./`)
- Access to `CORAL_COLONY_ADDR` and `CORAL_MODE` environment variables

**Restricted:**
- No filesystem write access
- No external network access (only Colony)
- No command execution
- No access to other environment variables

## Error Handling

Always wrap SDK calls in try-catch blocks:

```typescript
try {
  const services = await coral.services.list();
  // Process results...
} catch (error) {
  console.error(`Error fetching services: ${error}`);
  Deno.exit(1);
}
```

## Best Practices

1. **Convert Units**: Duration metrics are in nanoseconds - convert to milliseconds
```typescript
const p99Ms = p99.value / 1_000_000;
```

2. **Specify Time Ranges**: Always specify time ranges for metric queries
```typescript
const p99 = await coral.metrics.getP99(service, metric, 5 * 60 * 1000); // 5 minutes
```

3. **Limit SQL Results**: Use LIMIT clauses in raw SQL queries
```typescript
await coral.db.query("SELECT * FROM spans LIMIT 100");
```

4. **Handle Errors**: Wrap all SDK calls in try-catch blocks

5. **Use High-Level APIs**: Prefer `metrics.getP99()` over raw SQL when possible

## Troubleshooting

### "Connection refused"

Ensure Colony is running and accessible:
```bash
# Check colony status
coral colony status

# Verify colony address
echo $CORAL_COLONY_ADDR
```

### "Query failed"

Check SQL syntax and table names:
```typescript
// List available tables
const tables = await coral.db.query("SHOW TABLES");
console.log(tables.rows);
```

### "Script timeout"

Increase timeout:
```bash
coral run script.ts --timeout 120  # 120 seconds
```

## Further Reading

- [Example Scripts](../../examples/scripts/) - Complete working examples
- [CLI Documentation](../../docs/CLI.md) - `coral run` command usage
- [RFD 076](../../RFDs/076-sandboxed-typescript-execution.md) - Technical design
- [Deno Documentation](https://deno.land/manual) - Deno runtime

## License

Apache 2.0
