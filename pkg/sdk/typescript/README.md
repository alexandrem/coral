# Coral TypeScript SDK

TypeScript SDK for accessing Coral observability data from sandboxed Deno scripts.

## Installation

Import the SDK in your TypeScript scripts:

```typescript
import * as coral from "jsr:@coral/sdk";
```

## Usage

### Database Queries

Execute SQL queries against the local agent DuckDB:

```typescript
const spans = await coral.db.query(`
  SELECT trace_id, span_id, duration_ns
  FROM otel_spans_local
  WHERE service_name = 'payments'
    AND duration_ns > 500000000
  ORDER BY start_time DESC
  LIMIT 100
`);

console.log(`Found ${spans.count} slow spans`);
```

### Metrics

Get metrics with high-level helpers:

```typescript
// Get P99 latency
const p99 = await coral.metrics.getPercentile("payments", "http.server.duration", 0.99);
console.log(`P99 latency: ${p99 / 1_000_000}ms`);

// Get error rate
const errorRate = await coral.metrics.getErrorRate("payments", "5m");
console.log(`Error rate: ${(errorRate * 100).toFixed(2)}%`);
```

### Traces

Query traces with filters:

```typescript
const slowTraces = await coral.traces.query({
  service: "payments",
  minDuration: "500ms",
  timeRange: "1h",
});

for (const trace of slowTraces) {
  console.log(`Slow trace: ${trace.trace_id} (${trace.duration_ns / 1_000_000}ms)`);
}
```

### System Metrics

Access system-level metrics:

```typescript
const cpu = await coral.system.getCPU();
console.log(`CPU usage: ${cpu.usage_percent.toFixed(1)}%`);

const memory = await coral.system.getMemory();
const usagePercent = (memory.used / memory.total) * 100;
console.log(`Memory usage: ${usagePercent.toFixed(1)}%`);
```

### Event Emission

Emit custom events to colony:

```typescript
await coral.emit("alert", {
  message: "High latency detected",
  service: "payments",
  p99_ms: 650,
  threshold_ms: 500,
}, "warning");
```

## Complete Example

High latency alert script:

```typescript
import * as coral from "jsr:@coral/sdk";

async function checkLatency() {
  const p99 = await coral.metrics.getPercentile("payments", "http.server.duration", 0.99);
  const errorRate = await coral.metrics.getErrorRate("payments", "5m");

  const thresholdNs = 500_000_000; // 500ms
  const errorThreshold = 0.01; // 1%

  if (p99 > thresholdNs && errorRate > errorThreshold) {
    await coral.emit("alert", {
      severity: "critical",
      message: "Payments service degraded: high latency AND high error rate",
      p99_ms: p99 / 1_000_000,
      error_rate_pct: errorRate * 100,
      timestamp: new Date().toISOString(),
    }, "critical");

    console.log(`🚨 ALERT: P99=${p99 / 1_000_000}ms, Errors=${(errorRate * 100).toFixed(2)}%`);
  } else {
    console.log(`✓ OK: P99=${p99 / 1_000_000}ms, Errors=${(errorRate * 100).toFixed(2)}%`);
  }
}

// Run every 30 seconds
while (true) {
  await checkLatency();
  await new Promise((resolve) => setTimeout(resolve, 30_000));
}
```

## Security

Scripts run in Deno's secure sandbox with restricted permissions:

- ✅ Read-only access to local DuckDB
- ✅ HTTP access to SDK server (localhost only)
- ❌ No filesystem write access
- ❌ No external network access
- ❌ No command execution

## API Reference

### `db.query(sql: string): Promise<QueryResult>`

Execute a read-only SQL query.

### `metrics.getPercentile(service: string, metric: string, p: number): Promise<number>`

Get percentile value for a metric.

### `metrics.getErrorRate(service: string, window?: string): Promise<number>`

Get error rate for a service.

### `traces.query(options: QueryOptions): Promise<Trace[]>`

Query traces matching filter criteria.

### `system.getCPU(): Promise<CPUUsage>`

Get current CPU usage.

### `system.getMemory(): Promise<MemoryUsage>`

Get current memory usage.

### `emit(name: string, data: EventData, severity?: EventSeverity): Promise<void>`

Emit a custom event to colony.

## License

Apache 2.0
