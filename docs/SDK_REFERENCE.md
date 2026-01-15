# Coral TypeScript SDK Reference

**Package:** `@coral/sdk`
**Runtime:** Deno (embedded in Coral CLI)
**Mode:** CLI-side execution (queries colony summaries)

The Coral TypeScript SDK provides programmatic access to observability data from
sandboxed TypeScript scripts executed via `coral run`.

---

## Table of Contents

- [Getting Started](#getting-started)
- [Services API](#services-api)
- [Metrics API](#metrics-api)
- [Activity API](#activity-api)
- [Traces API](#traces-api)
- [System API](#system-api)
- [Database API](#database-api)
- [Types Reference](#types-reference)
- [Error Handling](#error-handling)

---

## Getting Started

### Basic Script Structure

```typescript
#!/usr/bin/env -S coral run

import * as coral from "@coral/sdk";

// Your analysis logic here
const services = await coral.services.list();
console.log(`Found ${services.length} services`);
```

### Execution

```bash
coral run script.ts                # Run once
coral run script.ts --watch        # Watch mode
coral run script.ts --timeout 120  # Custom timeout
```

### Environment

Scripts automatically connect to the active colony via environment variables:

- `CORAL_MODE=cli` - Indicates CLI execution mode
- `CORAL_COLONY_ADDR` - Colony gRPC endpoint (auto-configured)

---

## Services API

**Module:** `coral.services`

Service discovery and enumeration with dual-source discovery (RFD 084).

**See:** [SERVICE_DISCOVERY.md](./SERVICE_DISCOVERY.md) for architecture details.

### `list(options?: ListServicesOptions): Promise<ServiceInfo[]>`

List all services from both registry and telemetry sources (dual-source discovery).

**Parameters:**

- `options` (optional): Query options
  - `namespace?: string` - Filter by namespace
  - `timeRange?: string` - Time range for telemetry discovery (e.g., "1h", "24h", default: "1h")
  - `sourceFilter?: ServiceSource` - Filter by source type

**Returns:** Array of `ServiceInfo` objects

**Example:**

```typescript
import * as coral from "@coral/sdk";

// List all services (registry + telemetry from last 1h)
const services = await coral.services.list();

for (const svc of services) {
    console.log(`${svc.name} - ${svc.source} [${svc.status || 'N/A'}]`);
    console.log(`  Instances: ${svc.instanceCount}`);
    if (svc.agentId) {
        console.log(`  Agent: ${svc.agentId}`);
    }
}

// Filter by namespace
const prodServices = await coral.services.list({ namespace: "production" });

// Extend telemetry lookback
const recentServices = await coral.services.list({ timeRange: "24h" });

// Filter by source
const registeredOnly = await coral.services.list({
    sourceFilter: ServiceSource.REGISTERED
});

const observedOnly = await coral.services.list({
    sourceFilter: ServiceSource.OBSERVED
});
```

**ServiceInfo Type:**

```typescript
interface ServiceInfo {
    name: string;                    // Service name
    namespace: string;               // Namespace (empty if none)
    instanceCount: number;           // Number of instances
    lastSeen: Date;                  // Last seen timestamp

    // RFD 084: Dual-source discovery fields
    source: ServiceSource;           // Where this service info came from
    status?: ServiceStatus;          // Current status (if registered)
    agentId?: string;                // Agent ID (if registered)
}

enum ServiceSource {
    UNSPECIFIED = 0,
    REGISTERED = 1,    // Explicitly connected via ConnectService API
    OBSERVED = 2,      // Auto-observed from telemetry data
    VERIFIED = 3       // Verified (registered AND has telemetry)
}

enum ServiceStatus {
    UNSPECIFIED = 0,
    ACTIVE = 1,           // Registered and passing health checks
    UNHEALTHY = 2,        // Registered but health checks failing
    DISCONNECTED = 3,     // No longer registered but has recent telemetry
    OBSERVED_ONLY = 4     // Only observed from telemetry
}
```

**Service Source Interpretation:**

```typescript
// Check service source
if (svc.source === ServiceSource.VERIFIED && svc.status === ServiceStatus.ACTIVE) {
    console.log(`✅ ${svc.name} is fully verified and healthy`);
} else if (svc.source === ServiceSource.OBSERVED) {
    console.log(`◐ ${svc.name} is auto-observed (consider explicitly connecting)`);
} else if (svc.status === ServiceStatus.UNHEALTHY) {
    console.log(`⚠️ ${svc.name} is unhealthy (health checks failing)`);
}
```

---

## Metrics API

**Module:** `coral.metrics`

Query service metrics with precise percentile calculations.

###
`getPercentile(service: string, metric: string, percentile: number, timeRangeMs?: number): Promise<MetricResult>`

Get specific percentile value for a metric.

**Parameters:**

- `service`: Service name
- `metric`: Metric name (e.g., `"http.server.duration"`)
- `percentile`: Percentile as decimal (0.0-1.0, e.g., `0.99` for P99)
- `timeRangeMs` (optional): Time range in milliseconds (default: 3600000 = 1
  hour)

**Returns:** `MetricResult` with value, unit, and timestamp

**Example:**

```typescript
const p99 = await coral.metrics.getPercentile(
    "payments",
    "http.server.duration",
    0.99,
    3600000  // Last hour
);

console.log(`P99: ${p99.value / 1_000_000}ms`);
```

###
`getP50(service: string, metric: string, timeRangeMs?: number): Promise<MetricResult>`

Convenience method for P50 (median).

**Example:**

```typescript
const p50 = await coral.metrics.getP50("payments", "http.server.duration");
console.log(`Median latency: ${(p50.value / 1_000_000).toFixed(2)}ms`);
```

###
`getP95(service: string, metric: string, timeRangeMs?: number): Promise<MetricResult>`

Convenience method for P95.

###
`getP99(service: string, metric: string, timeRangeMs?: number): Promise<MetricResult>`

Convenience method for P99.

**MetricResult Type:**

```typescript
interface MetricResult {
    value: number;        // Metric value in native units
    unit: string;         // Unit name (e.g., "nanoseconds")
    timestamp: Date;      // Query timestamp
}
```

**Common Metrics:**

- `http.server.duration` - HTTP request duration (nanoseconds)
- `http.client.duration` - HTTP client request duration (nanoseconds)

**Time Conversions:**

```typescript
// Nanoseconds to milliseconds
const ms = value / 1_000_000;

// Nanoseconds to seconds
const sec = value / 1_000_000_000;
```

---

## Activity API

**Module:** `coral.activity`

Service request counts and error rates.

###
`getServiceActivity(service: string, timeRangeMs?: number): Promise<ServiceActivity>`

Get activity metrics for a specific service.

**Parameters:**

- `service`: Service name
- `timeRangeMs` (optional): Time range in milliseconds (default: 3600000)

**Returns:** `ServiceActivity` object

**Example:**

```typescript
const activity = await coral.activity.getServiceActivity("payments", 300000); // 5 min

console.log(`Service: ${activity.serviceName}`);
console.log(`Requests: ${activity.requestCount}`);
console.log(`Errors: ${activity.errorCount}`);
console.log(`Error Rate: ${(activity.errorRate * 100).toFixed(2)}%`);
```

### `listServiceActivity(timeRangeMs?: number): Promise<ServiceActivity[]>`

Get activity metrics for all services.

**Example:**

```typescript
const activities = await coral.activity.listServiceActivity(3600000); // 1 hour

for (const activity of activities) {
    if (activity.errorRate > 0.01) {  // > 1% error rate
        console.log(`⚠️  ${activity.serviceName}: ${(activity.errorRate * 100).toFixed(2)}% errors`);
    }
}
```

**ServiceActivity Type:**

```typescript
interface ServiceActivity {
    serviceName: string;
    requestCount: number;   // Total requests in time range
    errorCount: number;     // Requests with status >= 400
    errorRate: number;      // Error rate (0.0-1.0)
}
```

---

## Traces API

**Module:** `coral.traces`

Query distributed traces and spans.

###
`findSlow(service: string, minDurationNs: number, timeRangeMs?: number, limit?: number): Promise<Trace[]>`

Find slow traces above a duration threshold.

**Parameters:**

- `service`: Service name
- `minDurationNs`: Minimum duration in nanoseconds
- `timeRangeMs` (optional): Time range in milliseconds (default: 3600000)
- `limit` (optional): Maximum traces to return (default: 100)

**Returns:** Array of `Trace` objects

**Example:**

```typescript
// Find traces slower than 500ms
const slowTraces = await coral.traces.findSlow(
    "payments",
    500_000_000,  // 500ms in nanoseconds
    3600000,      // Last hour
    10            // Top 10
);

for (const trace of slowTraces) {
    console.log(`Trace ${trace.traceId}: ${trace.durationNs / 1_000_000}ms`);
}
```

**Trace Type:**

```typescript
interface Trace {
    traceId: string;
    durationNs: number;
    timestamp: Date;
    service: string;
}
```

---

## System API

**Module:** `coral.system`

System and host metrics.

### `getMetrics(service: string, timeRangeMs?: number): Promise<SystemMetrics>`

Get system-level metrics for a service.

**Parameters:**

- `service`: Service name
- `timeRangeMs` (optional): Time range in milliseconds

**Returns:** `SystemMetrics` object

**Example:**

```typescript
const sysMetrics = await coral.system.getMetrics("payments");

console.log(`CPU: ${sysMetrics.cpuPercent.toFixed(1)}%`);
console.log(`Memory: ${sysMetrics.memoryPercent.toFixed(1)}%`);
```

**SystemMetrics Type:**

```typescript
interface SystemMetrics {
    cpuPercent: number;       // CPU utilization (0-100)
    memoryPercent: number;    // Memory utilization (0-100)
    memoryUsageBytes: number; // Memory usage in bytes
}
```

---

## Database API

**Module:** `coral.db`

Raw SQL queries for advanced use cases.

### `query(sql: string, maxRows?: number): Promise<QueryResult>`

Execute a raw SQL query against colony DuckDB.

**Parameters:**

- `sql`: SQL query string
- `maxRows` (optional): Maximum rows to return (default: 1000)

**Returns:** `QueryResult` with rows and columns

**Example:**

```typescript
const result = await coral.db.query(`
  SELECT
    service_name,
    COUNT(*) as request_count,
    AVG(duration_ns) / 1000000 as avg_latency_ms
  FROM beyla_http_metrics
  WHERE timestamp > now() - INTERVAL '1 hour'
    AND http_status_code < 400
  GROUP BY service_name
  ORDER BY avg_latency_ms DESC
  LIMIT 10
`);

console.log(`Columns: ${result.columns.join(', ')}`);
for (const row of result.rows) {
    console.log(row.values.join(' | '));
}
```

**QueryResult Type:**

```typescript
interface QueryResult {
    rows: QueryRow[];
    rowCount: number;
    columns: string[];
}

interface QueryRow {
    values: string[];  // Column values as strings
}
```

**Available Tables:**

- `beyla_http_metrics` - HTTP request metrics (eBPF)
- `beyla_grpc_metrics` - gRPC call metrics (eBPF)
- `beyla_sql_metrics` - SQL query metrics (eBPF)
- `beyla_traces` - Distributed trace spans (eBPF)
- `otel_spans` - OTLP trace spans (telemetry)

**Safety Guardrails:**

- Queries are read-only (no INSERT/UPDATE/DELETE)
- Automatic row limits enforced
- Timeouts applied (60 seconds default)

---

## Types Reference

### Complete Type Definitions

```typescript
// Service discovery
interface ServiceInfo {
    name: string;
    namespace: string;
    instanceCount: number;
    lastSeen: Date;
}

// Metrics
interface MetricResult {
    value: number;
    unit: string;
    timestamp: Date;
}

// Activity
interface ServiceActivity {
    serviceName: string;
    requestCount: number;
    errorCount: number;
    errorRate: number;
}

// Traces
interface Trace {
    traceId: string;
    durationNs: number;
    timestamp: Date;
    service: string;
}

// System
interface SystemMetrics {
    cpuPercent: number;
    memoryPercent: number;
    memoryUsageBytes: number;
}

// Database
interface QueryResult {
    rows: QueryRow[];
    rowCount: number;
    columns: string[];
}

interface QueryRow {
    values: string[];
}
```

---

## Error Handling

All SDK functions return Promises that may reject with errors.

### Error Types

**Connection Errors:**

```typescript
try {
    const services = await coral.services.list();
} catch (error) {
    if (error.message.includes("connect")) {
        console.error("Failed to connect to colony");
    }
}
```

**Not Found Errors:**

```typescript
try {
    const p99 = await coral.metrics.getP99("unknown-service", "http.server.duration");
} catch (error) {
    if (error.message.includes("not found")) {
        console.error("Service or metric not found");
    }
}
```

**Timeout Errors:**

```typescript
// Use script timeout flag
// coral run script.ts --timeout 120
```

### Best Practices

1. **Always handle errors:**

```typescript
try {
    const services = await coral.services.list();
    // Process services
} catch (error) {
    console.error(`Error: ${error.message}`);
    Deno.exit(1);
}
```

2. **Validate data before processing:**

```typescript
const services = await coral.services.list();
if (services.length === 0) {
    console.log("No services found");
    Deno.exit(0);
}
```

3. **Use appropriate time ranges:**

```typescript
// Short time range for real-time monitoring
const recent = await coral.metrics.getP99("api", "http.server.duration", 300000); // 5 min

// Longer time range for trend analysis
const historical = await coral.metrics.getP99("api", "http.server.duration", 86400000); // 24 hours
```

4. **Convert units appropriately:**

```typescript
const p99 = await coral.metrics.getP99("api", "http.server.duration");

// Convert nanoseconds to milliseconds for display
const p99Ms = p99.value / 1_000_000;
console.log(`P99 latency: ${p99Ms.toFixed(2)}ms`);
```

---

## Complete Examples

### Health Monitoring

```typescript
#!/usr/bin/env -S coral run

import * as coral from "@coral/sdk";

const services = await coral.services.list();

for (const svc of services) {
    const activity = await coral.activity.getServiceActivity(svc.name);
    const p99 = await coral.metrics.getP99(svc.name, "http.server.duration");

    const errorPct = (activity.errorRate * 100).toFixed(2);
    const latencyMs = (p99.value / 1_000_000).toFixed(2);

    console.log(`${svc.name}:`);
    console.log(`  Requests: ${activity.requestCount}`);
    console.log(`  Errors: ${errorPct}%`);
    console.log(`  P99 Latency: ${latencyMs}ms`);

    if (activity.errorRate > 0.05 || p99.value > 1_000_000_000) {
        console.log(`  ⚠️  ALERT: High errors or latency!`);
    }
}
```

### Correlation Analysis

```typescript
#!/usr/bin/env -S coral run

import * as coral from "@coral/sdk";

const services = ["frontend", "api", "database"];
const results = [];

for (const svc of services) {
    const activity = await coral.activity.getServiceActivity(svc, 300000); // 5 min
    const system = await coral.system.getMetrics(svc);

    results.push({
        service: svc,
        errorRate: activity.errorRate,
        cpu: system.cpuPercent,
        memory: system.memoryPercent
    });
}

// Find correlated issues
const highErrors = results.filter(r => r.errorRate > 0.01);
if (highErrors.length >= 2) {
    console.log("⚠️  Cascading failure detected!");
    for (const r of highErrors) {
        console.log(`  ${r.service}: ${(r.errorRate * 100).toFixed(2)}% errors, CPU: ${r.cpu}%, Memory: ${r.memory}%`);
    }
}
```

---

**See also:**

- [TYPESCRIPT_SCRIPTING.md](./guides/TYPESCRIPT_SCRIPTING.md) - Complete scripting
  guide
- [CLI_REFERENCE.md](./CLI_REFERENCE.md) - CLI command reference
- [examples/scripts/](../examples/scripts/) - Example scripts
