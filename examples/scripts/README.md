# Coral Script Examples

Example TypeScript scripts demonstrating the Coral SDK for custom observability logic.

## Available Examples

### 1. High Latency Alert (`high-latency-alert.ts`)

Monitors a service for high P99 latency and error rates, emitting alerts when thresholds are exceeded.

**Features:**
- Multi-metric monitoring (latency + error rate)
- System resource correlation (CPU, memory)
- Severity-based alerting (warning vs critical)
- Continuous monitoring with periodic checks

**Usage:**
```bash
coral script deploy --name "high-latency-alert" --file examples/scripts/high-latency-alert.ts --targets "payments-service"
```

### 2. Correlation Analysis (`correlation-analysis.ts`)

Analyzes correlation between errors and system resource usage to identify resource-related issues.

**Features:**
- Error trace analysis
- Resource correlation detection
- Error pattern analysis by route
- Custom correlation events

**Usage:**
```bash
coral script deploy --name "correlation-analysis" --file examples/scripts/correlation-analysis.ts --targets "payments-service"
```

## Script Structure

All scripts follow a similar pattern:

```typescript
import * as coral from "jsr:@coral/sdk";

// Configuration
const SERVICE_NAME = "my-service";
const CHECK_INTERVAL_MS = 30_000;

// Main logic
async function checkHealth() {
  // Query metrics
  const p99 = await coral.metrics.getPercentile(SERVICE_NAME, "http.server.duration", 0.99);

  // Analyze and emit events
  if (p99 > threshold) {
    await coral.emit("alert", { ... }, "warning");
  }
}

// Monitoring loop
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
import * as coral from "jsr:@coral/sdk";
```

### 2. Query Data

Use the SDK to access Coral data:

```typescript
// Direct SQL queries
const spans = await coral.db.query("SELECT * FROM otel_spans_local LIMIT 10");

// High-level metric helpers
const p99 = await coral.metrics.getPercentile("service", "metric", 0.99);
const errorRate = await coral.metrics.getErrorRate("service", "5m");

// Trace queries
const traces = await coral.traces.query({
  service: "service",
  minDuration: "500ms",
  timeRange: "1h",
});

// System metrics
const cpu = await coral.system.getCPU();
const memory = await coral.system.getMemory();
```

### 3. Emit Events

Send custom events to colony:

```typescript
await coral.emit("alert", {
  message: "High latency detected",
  service: "payments",
  p99_ms: 650,
}, "warning");
```

### 4. Handle Errors

Always wrap logic in try-catch blocks:

```typescript
try {
  await checkHealth();
} catch (error) {
  console.error(`Error: ${error}`);
  await coral.emit("error", { error: String(error) }, "error");
}
```

## Deployment

### Deploy a Script

```bash
coral script deploy --name "my-script" --file script.ts --targets "service-name"
```

### List Deployed Scripts

```bash
coral script list
```

### View Script Status

```bash
coral script status <script-id>
```

### View Script Logs

```bash
coral script logs <script-id> --follow
```

### Stop a Script

```bash
coral script stop <script-id>
```

## Best Practices

1. **Always use try-catch** for error handling
2. **Set appropriate check intervals** to avoid overwhelming the system
3. **Use descriptive event names** for emitted events
4. **Include context** in alert payloads (service, metrics, timestamps)
5. **Test locally** before deploying to production
6. **Monitor script resource usage** via logs and system metrics

## Limitations

Scripts run in Deno's secure sandbox with restricted permissions:

- ✅ Read-only DuckDB access
- ✅ HTTP access to SDK server (localhost only)
- ❌ No filesystem write access
- ❌ No external network access
- ❌ No command execution

Memory limit: 512MB (configurable)
Timeout: 5 minutes (configurable)

## Troubleshooting

### Script fails with "Query failed"

Check that the SQL query is valid and the table exists:

```typescript
const result = await coral.db.query("SHOW TABLES");
console.log(result);
```

### Script fails with "Failed to emit event"

Ensure the SDK server is running and accessible:

```bash
curl http://localhost:9003/health
```

### Script times out

Reduce the check interval or optimize queries to run faster.

## Further Reading

- [RFD 076: Sandboxed TypeScript Execution](../../RFDs/076-sandboxed-typescript-execution.md)
- [Coral TypeScript SDK](../../pkg/sdk/typescript/README.md)
- [Deno Documentation](https://deno.land/manual)
