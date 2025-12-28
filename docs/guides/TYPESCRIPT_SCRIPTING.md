# TypeScript Scripting Guide

**Execute custom observability analysis with sandboxed TypeScript scripts.**

Coral's TypeScript scripting runtime enables custom analysis, dashboards,
alerts, and integrations using familiar TypeScript and a curated SDK. Scripts
run locally via embedded Deno, querying colony-aggregated data through gRPC.

---

## Table of Contents

- [Quick Start](#quick-start)
- [Why TypeScript Scripts?](#why-typescript-scripts)
- [Script Anatomy](#script-anatomy)
- [Common Patterns](#common-patterns)
- [Security Model](#security-model)
- [Development Workflow](#development-workflow)
- [Deployment & Sharing](#deployment--sharing)
- [Troubleshooting](#troubleshooting)

---

## Quick Start

### 1. Create a Script

```typescript
#!/usr/bin/env -S coral run

import * as coral from "@coral/sdk";

// List all services
const services = await coral.services.list();
console.log(`Found ${services.length} services`);

for (const svc of services) {
    console.log(`- ${svc.name} (${svc.instanceCount} instances)`);
}
```

### 2. Make Executable (Optional)

```bash
chmod +x script.ts
```

### 3. Run

```bash
coral run script.ts
# or if executable:
./script.ts
```

### 4. Watch Mode for Development

```bash
coral run script.ts --watch
```

---

## Why TypeScript Scripts?

### Problems Solved

**Before (Manual Queries):**

```bash
# Complex shell scripting
coral query metrics api --percentile 99
coral query metrics payments --percentile 99
coral query metrics orders --percentile 99
# ... repeat for each service, manually compare
```

**After (TypeScript Script):**

```typescript
const services = await coral.services.list();
for (const svc of services) {
    const p99 = await coral.metrics.getP99(svc.name, "http.server.duration");
    if (p99.value > 500_000_000) {  // > 500ms
        console.log(`⚠️  ${svc.name}: ${p99.value / 1_000_000}ms`);
    }
}
```

### Key Benefits

1. **AI-Generated**: Claude writes scripts from natural language ("alert me when
   latency is high")
2. **Composable**: Combine metrics, traces, system data for correlation analysis
3. **Reusable**: Version control and share scripts across teams
4. **Familiar**: TypeScript syntax with full IDE support
5. **Safe**: Sandboxed execution prevents destructive actions

---

## Script Anatomy

### Basic Structure

```typescript
#!/usr/bin/env -S coral run  // Optional shebang for direct execution

import * as coral from "@coral/sdk";  // Import SDK

// Your analysis logic
const services = await coral.services.list();

// Process data
for (const svc of services) {
    // Query metrics, logs, traces
    const p99 = await coral.metrics.getP99(svc.name, "http.server.duration");

    // Analyze and alert
    if (p99.value > threshold) {
        console.log(`Alert: ${svc.name}`);
    }
}

// Exit codes
Deno.exit(0);  // Optional: explicit exit
```

### SDK Imports

```typescript
// Full SDK
import * as coral from "@coral/sdk";

coral.services.list();
coral.metrics.getP99(...);

// Specific modules
import {services} from "@coral/sdk";

const svcs = await services.list();

// Types
import type {ServiceInfo, MetricResult} from "@coral/sdk";
```

### Output

```typescript
// Standard output
console.log("Info message");
console.error("Error message");
console.warn("Warning message");

// Formatted output
const p99Ms = (p99.value / 1_000_000).toFixed(2);
console.log(`P99: ${p99Ms}ms`);

// Tables (using console.table)
console.table(services.map(s => ({
    Name: s.name,
    Instances: s.instanceCount
})));
```

---

## Common Patterns

### 1. Service Health Monitoring

**Use case:** Check all services for high latency or error rates.

```typescript
#!/usr/bin/env -S coral run

import * as coral from "@coral/sdk";

const LATENCY_THRESHOLD_MS = 500;
const ERROR_RATE_THRESHOLD = 0.05;  // 5%

const services = await coral.services.list();

console.log("Service Health Report\n");

for (const svc of services) {
    const activity = await coral.activity.getServiceActivity(svc.name);
    const p99 = await coral.metrics.getP99(svc.name, "http.server.duration");

    const p99Ms = p99.value / 1_000_000;
    const errorPct = activity.errorRate * 100;

    console.log(`${svc.name}:`);
    console.log(`  P99 Latency: ${p99Ms.toFixed(2)}ms`);
    console.log(`  Error Rate: ${errorPct.toFixed(2)}%`);

    // Alert logic
    if (p99Ms > LATENCY_THRESHOLD_MS) {
        console.log(`  ⚠️  HIGH LATENCY`);
    }
    if (activity.errorRate > ERROR_RATE_THRESHOLD) {
        console.log(`  ⚠️  HIGH ERROR RATE`);
    }
    console.log();
}
```

### 2. Percentile Comparison

**Use case:** Compare P50, P95, P99 latencies across services.

```typescript
#!/usr/bin/env -S coral run

import * as coral from "@coral/sdk";

const services = await coral.services.list();

console.log("Latency Percentile Report\n");
console.log("Service".padEnd(20) + "P50".padEnd(10) + "P95".padEnd(10) + "P99");
console.log("-".repeat(50));

for (const svc of services) {
    try {
        const [p50, p95, p99] = await Promise.all([
            coral.metrics.getP50(svc.name, "http.server.duration"),
            coral.metrics.getP95(svc.name, "http.server.duration"),
            coral.metrics.getP99(svc.name, "http.server.duration"),
        ]);

        const p50Ms = (p50.value / 1_000_000).toFixed(1);
        const p95Ms = (p95.value / 1_000_000).toFixed(1);
        const p99Ms = (p99.value / 1_000_000).toFixed(1);

        console.log(
            svc.name.padEnd(20) +
            `${p50Ms}ms`.padEnd(10) +
            `${p95Ms}ms`.padEnd(10) +
            `${p99Ms}ms`
        );
    } catch (error) {
        console.log(svc.name.padEnd(20) + "No data");
    }
}
```

### 3. Cross-Service Correlation

**Use case:** Detect cascading failures or correlated issues.

```typescript
#!/usr/bin/env -S coral run

import * as coral from "@coral/sdk";

const timeRange = 300000;  // 5 minutes
const services = await coral.services.list();

// Gather metrics for all services
const serviceHealth = await Promise.all(
    services.map(async (svc) => {
        const activity = await coral.activity.getServiceActivity(svc.name, timeRange);
        const system = await coral.system.getMetrics(svc.name);

        return {
            name: svc.name,
            errorRate: activity.errorRate,
            requestCount: activity.requestCount,
            cpu: system.cpuPercent,
            memory: system.memoryPercent,
        };
    })
);

// Detect correlated issues
const unhealthy = serviceHealth.filter(s => s.errorRate > 0.01);

if (unhealthy.length >= 2) {
    console.log("🚨 CASCADING FAILURE DETECTED\n");
    console.log("Multiple services experiencing errors:\n");

    for (const svc of unhealthy) {
        console.log(`${svc.name}:`);
        console.log(`  Error Rate: ${(svc.errorRate * 100).toFixed(2)}%`);
        console.log(`  Requests: ${svc.requestCount}`);
        console.log(`  CPU: ${svc.cpu.toFixed(1)}%`);
        console.log(`  Memory: ${svc.memory.toFixed(1)}%`);
        console.log();
    }
} else {
    console.log("✅ All services healthy");
}
```

### 4. Custom SQL Queries

**Use case:** Advanced analysis requiring complex SQL.

```typescript
#!/usr/bin/env -S coral run

import * as coral from "@coral/sdk";

const result = await coral.db.query(`
  SELECT
    service_name,
    http_route,
    COUNT(*) as request_count,
    AVG(duration_ns) / 1000000 as avg_latency_ms,
    quantile_cont(duration_ns, 0.99) / 1000000 as p99_latency_ms
  FROM beyla_http_metrics
  WHERE timestamp > now() - INTERVAL '1 hour'
    AND http_status_code < 400
  GROUP BY service_name, http_route
  HAVING COUNT(*) > 100
  ORDER BY p99_latency_ms DESC
  LIMIT 20
`);

console.log("Slowest Routes (Last Hour)\n");
console.log(result.columns.join(" | "));
console.log("-".repeat(80));

for (const row of result.rows) {
    console.log(row.values.join(" | "));
}
```

### 5. Slow Trace Analysis

**Use case:** Find and analyze slow traces.

```typescript
#!/usr/bin/env -S coral run

import * as coral from "@coral/sdk";

const service = "api";
const minDurationMs = 500;
const minDurationNs = minDurationMs * 1_000_000;

const slowTraces = await coral.traces.findSlow(service, minDurationNs, 3600000, 10);

console.log(`Slowest Traces for ${service} (last hour, >${minDurationMs}ms)\n`);

if (slowTraces.length === 0) {
    console.log("No slow traces found");
    Deno.exit(0);
}

for (const trace of slowTraces) {
    const durationMs = (trace.durationNs / 1_000_000).toFixed(2);
    const timestamp = trace.timestamp.toISOString();

    console.log(`Trace: ${trace.traceId}`);
    console.log(`  Duration: ${durationMs}ms`);
    console.log(`  Time: ${timestamp}`);
    console.log();
}
```

### 6. Daily Report Generation

**Use case:** Generate scheduled reports for email or Slack.

```typescript
#!/usr/bin/env -S coral run

import * as coral from "@coral/sdk";

const timeRange = 86400000;  // 24 hours
const services = await coral.services.list();

console.log("Daily Service Report");
console.log(`Date: ${new Date().toISOString()}\n`);

for (const svc of services) {
    const activity = await coral.activity.getServiceActivity(svc.name, timeRange);
    const p99 = await coral.metrics.getP99(svc.name, "http.server.duration");

    console.log(`## ${svc.name}`);
    console.log(`- Total Requests: ${activity.requestCount.toLocaleString()}`);
    console.log(`- Error Rate: ${(activity.errorRate * 100).toFixed(2)}%`);
    console.log(`- P99 Latency: ${(p99.value / 1_000_000).toFixed(2)}ms`);
    console.log();
}

// Could pipe to file or send via HTTP
// coral run daily-report.ts > report.txt
// cat report.txt | mail -s "Daily Report" team@example.com
```

---

## Security Model

### Sandboxing

Scripts run with **restricted Deno permissions**:

```bash
# Actual Deno invocation
deno run \
  --allow-net=<colony-address>  \  # Network: colony gRPC only
  --allow-read=./               \  # Read: current directory only
  --allow-env=CORAL_*           \  # Env: CORAL_* variables only
  script.ts
```

### What's Allowed ✅

- Query colony via gRPC (`@coral/sdk` APIs)
- Read local files for imports
- Console output (stdout/stderr)
- Access `CORAL_MODE` and `CORAL_COLONY_ADDR` env vars

### What's Blocked ❌

- Write to filesystem
- Execute shell commands
- Access arbitrary environment variables
- Network access beyond colony
- Native code execution

### Safety Guarantees

1. **Read-Only Data**: Scripts query colony summaries, cannot modify data
2. **Resource Limits**: Timeout enforced (default: 60s, configurable)
3. **No Privilege Escalation**: Runs in user context, no sudo
4. **Colony-Only Network**: Cannot exfiltrate data to external systems

---

## Development Workflow

### 1. Interactive Development

Use watch mode for rapid iteration:

```bash
coral run monitor.ts --watch
```

Changes to `monitor.ts` trigger automatic re-execution.

### 2. Debugging

**Console Logging:**

```typescript
console.log("Debug: services =", services);
console.error("Error occurred:", error);
```

**Type Checking:**

```bash
# Deno has built-in TypeScript support
# Type errors appear immediately
```

**Common Issues:**

```typescript
// ❌ Wrong: percentile as integer
const p99 = await coral.metrics.getPercentile(svc, metric, 99);

// ✅ Correct: percentile as decimal
const p99 = await coral.metrics.getPercentile(svc, metric, 0.99);
```

### 3. Error Handling

```typescript
try {
    const services = await coral.services.list();
    // ...
} catch (error) {
    console.error(`Failed to query services: ${error.message}`);
    Deno.exit(1);
}
```

### 4. Testing

```typescript
// Simple assertions
const services = await coral.services.list();
if (services.length === 0) {
    console.error("Expected services, got none");
    Deno.exit(1);
}

// Validate thresholds
const p99 = await coral.metrics.getP99("api", "http.server.duration");
if (p99.value > 1_000_000_000) {  // > 1s
    console.error("P99 latency too high!");
    Deno.exit(1);
}
```

---

## Deployment & Sharing

### Version Control

**Recommended structure:**

```
your-repo/
├── coral-scripts/
│   ├── health-check.ts
│   ├── daily-report.ts
│   └── alert-on-slow-traces.ts
└── README.md
```

**Git workflow:**

```bash
git add coral-scripts/
git commit -m "Add latency monitoring script"
git push
```

### Sharing Scripts

**Within team:**

```bash
# Share file
scp health-check.ts teammate@host:/scripts/

# Share via git
git clone <repo>
coral run coral-scripts/health-check.ts
```

**Community sharing:**

- GitHub repositories (e.g., `awesome-coral-scripts`)
- Coral script marketplace (future)

### Scheduled Execution

**Cron:**

```cron
# Run every 5 minutes
*/5 * * * * /usr/local/bin/coral run /path/to/monitor.ts >> /var/log/coral-monitor.log 2>&1

# Daily report at 9 AM
0 9 * * * /usr/local/bin/coral run /path/to/daily-report.ts | mail -s "Daily Report" team@example.com
```

**Systemd Timer:**

```ini
# /etc/systemd/system/coral-monitor.timer
[Unit]
Description=Coral Health Monitor

[Timer]
OnCalendar=*:0/5
Persistent=true

[Install]
WantedBy=timers.target
```

```ini
# /etc/systemd/system/coral-monitor.service
[Unit]
Description=Coral Health Monitor Script

[Service]
Type=oneshot
ExecStart=/usr/local/bin/coral run /opt/coral/scripts/health-check.ts
```

### CI/CD Integration

**GitHub Actions:**

```yaml
name: Coral Health Check

on:
    schedule:
        -   cron: '*/15 * * * *'  # Every 15 minutes

jobs:
    health-check:
        runs-on: ubuntu-latest
        steps:
            -   uses: actions/checkout@v4
            -   name: Install Coral
                run: curl -sSL https://install.coral.sh | sh
            -   name: Run health check
                run: coral run scripts/health-check.ts
```

---

## Troubleshooting

### Common Errors

**1. "Failed to connect to colony"**

```typescript
// Check colony address
coral
colony
status

// Verify network connectivity
ping < colony - address >
```

**2. "Service not found"**

```typescript
// List available services first
const services = await coral.services.list();
console.log(services.map(s => s.name));
```

**3. "Permission denied"**

```typescript
// Scripts cannot write files
// ❌ Deno.writeTextFile("output.txt", data);

// ✅ Use stdout instead
console.log(data);
```

**4. "Script timeout"**

```bash
# Increase timeout
coral run slow-script.ts --timeout 120
```

### Performance Tips

**1. Parallel queries:**

```typescript
// ✅ Fast: parallel
const [p50, p95, p99] = await Promise.all([
    coral.metrics.getP50(svc, metric),
    coral.metrics.getP95(svc, metric),
    coral.metrics.getP99(svc, metric),
]);

// ❌ Slow: sequential
const p50 = await coral.metrics.getP50(svc, metric);
const p95 = await coral.metrics.getP95(svc, metric);
const p99 = await coral.metrics.getP99(svc, metric);
```

**2. Batch operations:**

```typescript
// ✅ Batch service queries
const activities = await Promise.all(
    services.map(s => coral.activity.getServiceActivity(s.name))
);
```

**3. Limit time ranges:**

```typescript
// ✅ Shorter time ranges = faster queries
const recent = await coral.metrics.getP99(svc, metric, 300000);  // 5 min

// ❌ Avoid unnecessarily long ranges
const slow = await coral.metrics.getP99(svc, metric, 604800000);  // 7 days
```

### Best Practices

1. **Always handle errors** - Scripts may fail, handle gracefully
2. **Use appropriate time ranges** - Balance freshness vs. query cost
3. **Validate input** - Check for empty results before processing
4. **Exit with codes** - `Deno.exit(0)` for success, `Deno.exit(1)` for errors
5. **Document thresholds** - Explain alert criteria in comments
6. **Version control** - Track script changes in git
7. **Test thoroughly** - Validate against actual data before scheduling

---

## Next Steps

- **Explore examples:** See [examples/scripts/](../examples/scripts/)
- **SDK reference:** See [SDK_REFERENCE.md](./SDK_REFERENCE.md)
- **CLI commands:** See [CLI_REFERENCE.md](./CLI_REFERENCE.md)
- **Share scripts:** Contribute to community script repository

---

**Questions or feedback?** Open an issue on GitHub or join the Coral community.
