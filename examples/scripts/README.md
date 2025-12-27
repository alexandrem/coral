# Coral Script Examples

Example TypeScript scripts demonstrating the Coral SDK for querying observability data from the CLI.

## Running Scripts

Execute scripts locally using the `coral run` command:

```bash
coral run examples/scripts/latency-report.ts
coral run examples/scripts/service-activity.ts --timeout 120
coral run examples/scripts/high-latency-alert.ts --watch
```

Scripts connect to your Colony instance and query observability data via gRPC.

## Available Examples

### 1. Service Latency Report (`latency-report.ts`)

Lists all services with their P99, P95, and P50 latencies. Highlights services with high latency.

**Features:**
- Service discovery
- Latency percentile queries
- Simple console output

**Usage:**
```bash
coral run examples/scripts/latency-report.ts
```

**Sample Output:**
```
Service Latency Report
============================================================
Found 3 service(s)

payments (default):
----------------------------------------
  P50: 45.23ms
  P95: 234.56ms
  P99: 512.34ms
  ⚠️  WARNING: High P99 latency (>500ms)
```

### 2. Service Activity Report (`service-activity.ts`)

Shows request counts and error rates for all services using raw SQL queries.

**Features:**
- Raw SQL queries to DuckDB
- Request and error rate analysis
- Console-based reporting

**Usage:**
```bash
coral run examples/scripts/service-activity.ts
```

**Sample Output:**
```
Service Activity Report
============================================================

Found 3 active service(s)

payments:
  Requests: 15234
  Errors: 23 (0.15%)

orders:
  Requests: 8932
  Errors: 145 (1.62%)
  ⚠️  WARNING: High error rate (>1%)
```

### 3. High Latency Alert (`high-latency-alert.ts`)

Continuously monitors a service for high latency and error rates with periodic checks.

**Features:**
- Multi-metric monitoring (P99 latency + error rate)
- Threshold-based alerting
- Continuous monitoring loop
- SQL-based error rate calculation

**Usage:**
```bash
coral run examples/scripts/high-latency-alert.ts
```

**Configuration:**
Edit the script to adjust thresholds and monitoring settings:
```typescript
const SERVICE_NAME = "payments";
const LATENCY_THRESHOLD_MS = 500;
const ERROR_RATE_THRESHOLD = 0.01; // 1%
const CHECK_INTERVAL_MS = 30_000; // 30 seconds
```

### 4. Cross-Service Correlation Analysis (`correlation-analysis.ts`)

Analyzes latency across multiple services to detect cascading performance issues.

**Features:**
- Cross-service latency comparison
- Anomaly detection (services >2x average latency)
- Error correlation analysis
- Continuous monitoring

**Usage:**
```bash
coral run examples/scripts/correlation-analysis.ts
```

**Sample Output:**
```
Analyzing 4 service(s)...

🔍 CORRELATION DETECTED: 2 service(s) with abnormally high latency

Average P99: 180.5ms
Average P95: 95.2ms

High latency services:
  payments:
    P99: 512.3ms (2.8x average)
    P95: 289.1ms
    P50: 156.7ms
  orders:
    P99: 445.9ms (2.5x average)
    P95: 267.3ms
    P50: 134.2ms

Error analysis:
  payments: 23 errors (0.15%)
  orders: 145 errors (1.62%)
```

## Script Structure

All scripts follow a similar pattern:

```typescript
import * as coral from "../../pkg/sdk/typescript/mod.ts";

// Configuration
const SERVICE_NAME = "my-service";
const CHECK_INTERVAL_MS = 30_000;

// Main logic
async function checkHealth() {
  // Query metrics
  const p99 = await coral.metrics.getP99(
    SERVICE_NAME,
    "http.server.duration",
    5 * 60 * 1000, // Last 5 minutes
  );
  const p99Ms = p99.value / 1_000_000;

  // Analyze and alert
  if (p99Ms > threshold) {
    console.log(`⚠️  WARNING: High latency detected: ${p99Ms.toFixed(1)}ms`);
  }
}

// Monitoring loop (optional)
async function main() {
  while (true) {
    await checkHealth();
    await new Promise(resolve => setTimeout(resolve, CHECK_INTERVAL_MS));
  }
}

main().catch(error => {
  console.error(`Fatal error: ${error}`);
  Deno.exit(1);
});
```

## Creating Your Own Scripts

### 1. Import the SDK

```typescript
import * as coral from "../../pkg/sdk/typescript/mod.ts";
```

For published packages (future), use:
```typescript
import * as coral from "jsr:@coral/sdk";
```

### 2. Query Data

Use the SDK to access Colony data:

**Service Discovery:**
```typescript
// List all services
const services = await coral.services.list();

// List services in a namespace
const services = await coral.services.list("production");
```

**Metric Queries:**
```typescript
// Get latency percentiles
const p99 = await coral.metrics.getP99(
  "payments",
  "http.server.duration",
  5 * 60 * 1000, // Last 5 minutes
);
const p95 = await coral.metrics.getP95("payments", "http.server.duration");
const p50 = await coral.metrics.getP50("payments", "http.server.duration");

// Convert from nanoseconds to milliseconds
const p99Ms = p99.value / 1_000_000;
```

**Raw SQL Queries:**
```typescript
// Direct DuckDB queries
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

// Access results
console.log(`Columns: ${result.columns.join(", ")}`);
for (const row of result.rows) {
  console.log(`${row.service_name}: ${row.request_count} requests`);
}
```

### 3. Handle Errors

Always wrap logic in try-catch blocks:

```typescript
try {
  await checkHealth();
} catch (error) {
  console.error(`Error: ${error}`);
}
```

## Command-Line Options

The `coral run` command supports several options:

**Timeout:**
```bash
coral run script.ts --timeout 120  # 120 seconds
```

**Watch Mode:**
```bash
coral run script.ts --watch  # Re-run on file changes
```

## Available SDK APIs

### Services

```typescript
// List all services
const services = await coral.services.list();

// List services in namespace
const services = await coral.services.list("production");

// Service properties
for (const svc of services) {
  console.log(svc.name);          // Service name
  console.log(svc.namespace);     // Namespace
  console.log(svc.instanceCount); // Number of instances
  console.log(svc.lastSeen);      // Last activity timestamp
}
```

### Metrics

```typescript
// Latency percentiles (returns value in nanoseconds)
const p99 = await coral.metrics.getP99(service, metric, timeRangeMs?);
const p95 = await coral.metrics.getP95(service, metric, timeRangeMs?);
const p50 = await coral.metrics.getP50(service, metric, timeRangeMs?);

// Common metrics
const p99 = await coral.metrics.getP99("payments", "http.server.duration");
const p99Ms = p99.value / 1_000_000; // Convert ns to ms
```

### Database

```typescript
// Execute raw SQL queries
const result = await coral.db.query(sql, maxRows?);

// Result structure
interface QueryResult {
  columns: string[];           // Column names
  rows: Record<string, any>[]; // Array of row objects
  rowCount: number;            // Number of rows returned
}

// Available tables
// - ebpf_http_metrics: HTTP request metrics
// - otel_spans_local: OpenTelemetry spans
// - otel_metrics_local: OpenTelemetry metrics
```

## Security and Sandboxing

Scripts run in Deno's secure sandbox with restricted permissions:

**Allowed:**
- Network access to Colony gRPC API
- Read access to local files (for imports)
- Access to `CORAL_COLONY_ADDR` and `CORAL_MODE` environment variables
- Console output (stdout/stderr)

**Restricted:**
- No filesystem write access
- No external network access (only Colony)
- No command execution
- No access to other environment variables

## Best Practices

1. **Always use try-catch** for error handling
2. **Set appropriate check intervals** to avoid overwhelming Colony
3. **Use specific time ranges** for metric queries (default: 1 hour)
4. **Limit SQL query results** using `LIMIT` clause or `maxRows` parameter
5. **Test locally** before using in production environments
6. **Convert units** appropriately (nanoseconds to milliseconds)

## Troubleshooting

### Script fails with "failed to find Deno binary"

Ensure Deno is installed or run `go generate ./internal/cli/run` to download embedded binaries.

### Script fails with "connection refused"

Ensure Colony is running and accessible. Check `$CORAL_COLONY_ADDR` or use `--colony` flag:

```bash
coral run script.ts --colony localhost:9090
```

### Script fails with "Query failed"

Check that the SQL query is valid and the table exists:

```typescript
const tables = await coral.db.query("SHOW TABLES");
console.log(tables.rows);
```

### Script times out

Increase timeout or optimize queries:

```bash
coral run script.ts --timeout 300  # 5 minutes
```

## Further Reading

- [RFD 076: CLI-Side TypeScript Execution](../../RFDs/076-IMPLEMENTATION-STATUS.md)
- [Coral TypeScript SDK](../../pkg/sdk/typescript/)
- [Deno Documentation](https://deno.land/manual)
