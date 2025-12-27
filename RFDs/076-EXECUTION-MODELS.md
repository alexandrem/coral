# RFD 076: Execution Models - Global vs Service-Scoped

**Status**: Implementation Ready
**Related**: [RFD 076 - Sandboxed TypeScript Execution](076-sandboxed-typescript-execution.md)

## Overview

Coral supports **two execution models** for TypeScript scripts, each optimized for different use cases:

1. **Global Scripts**: Full flexibility for cross-service correlation and system-wide monitoring
2. **Service-Scoped Scripts**: Portable, reusable scripts with dependency injection

## Execution Models Comparison

| Aspect | Global Scripts | Service-Scoped Scripts |
|--------|---------------|----------------------|
| **Scope** | All services | Single service |
| **Service Access** | Must specify service names | Service context auto-injected |
| **Portability** | Architecture-specific | Portable across services |
| **Community Sharing** | ❌ Not suitable | ✅ Highly reusable |
| **Cross-Service Logic** | ✅ Full support | ❌ Single service only |
| **Use Case** | Custom correlation, system-wide | Reusable monitoring patterns |

## Model 1: Global Scripts

### When to Use

- **Cross-service correlation**: Detect cascading failures across services
- **System-wide monitoring**: Monitor all services with custom logic
- **Architecture-specific logic**: Logic tied to your service topology
- **Complex queries**: Need to aggregate data across multiple services

### Characteristics

```protobuf
Script {
  scope: EXECUTION_SCOPE_GLOBAL
  service_name: ""  // Not applicable
}
```

**Access Pattern**: Must explicitly specify service names

```typescript
// Must specify service names
const paymentsErrors = await coral.metrics.getErrorRate("payments");
const ordersErrors = await coral.metrics.getErrorRate("orders");
const inventoryErrors = await coral.metrics.getErrorRate("inventory");

// Can correlate across services
if (paymentsErrors > 0.01 && ordersErrors > 0.01) {
  // Detect cascading failure
}
```

### Example: Cascading Failure Detection

```typescript
/**
 * Detect when errors in one service cause errors in dependent services.
 * This is specific to your architecture and service dependencies.
 */
import * as coral from "jsr:@coral/sdk";

const SERVICES = ["payments", "orders", "inventory", "notifications"];

while (true) {
  const healthChecks = await Promise.all(
    SERVICES.map(async (service) => ({
      service,
      errorRate: await coral.metrics.getErrorRate(service),
    }))
  );

  const unhealthy = healthChecks.filter(h => h.errorRate.rate > 0.01);

  if (unhealthy.length >= 2) {
    // Cascading failure detected!
    await coral.emit("cascading_failure", {
      affected: unhealthy.map(h => h.service),
    }, "critical");
  }

  await new Promise(r => setTimeout(r, 60_000));
}
```

### Deployment

```bash
# Deploy globally (no service attachment)
coral script deploy cross-service-correlation \
  --scope global \
  --type daemon
```

### Pros & Cons

**Pros:**
- ✅ Maximum flexibility
- ✅ Can implement any correlation logic
- ✅ Access to all services
- ✅ System-wide view

**Cons:**
- ❌ Not portable (hardcoded service names)
- ❌ Breaks if services are renamed
- ❌ Can't be shared in community marketplace
- ❌ Must be customized for each deployment

## Model 2: Service-Scoped Scripts

### When to Use

- **Reusable monitoring patterns**: Same logic works for any service
- **Community scripts**: Share scripts that work universally
- **Service-agnostic logic**: Monitoring patterns independent of service name
- **Standardized checks**: Apply same monitoring to all services

### Characteristics

```protobuf
Script {
  scope: EXECUTION_SCOPE_SERVICE
  service_name: "payments"  // Required
}
```

**Access Pattern**: Service context auto-injected

```typescript
// No need to specify service name - it's injected!
const p99 = await coral.context.service.getPercentile("http.server.duration", 0.99);
const errorRate = await coral.context.service.getErrorRate();

// Service metadata available
console.log(`Monitoring: ${coral.context.service.name}`);
console.log(`Region: ${coral.context.service.region}`);
console.log(`Version: ${coral.context.service.version}`);
```

### Example: Portable Latency Monitor

```typescript
/**
 * Portable high latency monitor.
 * Works for ANY service - just attach it!
 */
import * as coral from "jsr:@coral/sdk";

const threshold = parseInt(coral.context.params.get("threshold_ms") || "500");
const service = coral.context.service!;

while (true) {
  // Auto-scoped to attached service
  const p99 = await service.getPercentile("http.server.duration", 0.99);

  if (p99.value / 1_000_000 > threshold) {
    await coral.emit("high_latency", {
      service: service.name,
      p99_ms: p99.value / 1_000_000,
      threshold_ms: threshold,
    }, "warning");
  }

  await new Promise(r => setTimeout(r, 30_000));
}
```

### Deployment

```bash
# Deploy to multiple services with the SAME script!
coral script deploy high-latency-monitor --service payments --params threshold_ms=500
coral script deploy high-latency-monitor --service orders --params threshold_ms=300
coral script deploy high-latency-monitor --service users --params threshold_ms=1000
```

### Pros & Cons

**Pros:**
- ✅ Highly portable
- ✅ Service-agnostic
- ✅ Perfect for community sharing
- ✅ Parameterizable
- ✅ Reusable across deployments

**Cons:**
- ❌ Cannot access other services
- ❌ Limited to single service scope
- ❌ Can't implement cross-service logic

## Dependency Injection Details

### Service Context Interface

When `scope = EXECUTION_SCOPE_SERVICE`, the script receives:

```typescript
interface ServiceContext {
  // Service metadata
  name: string;           // "payments"
  namespace: string;      // "prod"
  region: string;         // "us-east-1"
  version: string;        // "v1.2.3"
  deployment: string;     // "payments-v1-2-3-abc123"

  // Auto-scoped methods (no service name needed)
  getPercentile(metric: string, percentile: number): Promise<MetricResult>;
  getErrorRate(timeWindowMs?: number): Promise<ErrorRateResult>;
  findSlowTraces(minDurationMs: number): Promise<TraceResult>;
  findErrors(timeRangeMs?: number): Promise<TraceResult>;
}
```

### Access via `coral.context.service`

```typescript
import * as coral from "jsr:@coral/sdk";

// Check scope
if (coral.context.scope !== "service") {
  throw new Error("This script requires service scope");
}

// Access injected service context
const service = coral.context.service!;

// Use auto-scoped methods
const p99 = await service.getPercentile("http.server.duration", 0.99);
console.log(`${service.name} P99: ${p99.value / 1_000_000}ms`);
```

### How Dependency Injection Works

1. **Script Deployment**:
   ```bash
   coral script deploy my-script --service payments
   ```

2. **Executor Sets Environment**:
   ```bash
   CORAL_SCOPE=service
   CORAL_SERVICE_NAME=payments
   CORAL_SERVICE_NAMESPACE=prod
   CORAL_SERVICE_REGION=us-east-1
   CORAL_SERVICE_VERSION=v1.2.3
   ```

3. **SDK Reads Environment**:
   ```typescript
   // Internal SDK implementation
   const context: ScriptContext = {
     scope: Deno.env.get("CORAL_SCOPE") as "global" | "service",
     service: Deno.env.get("CORAL_SCOPE") === "service" ? {
       name: Deno.env.get("CORAL_SERVICE_NAME")!,
       namespace: Deno.env.get("CORAL_SERVICE_NAMESPACE")!,
       // ...
       getPercentile: async (metric, p) => {
         // Auto-inject service name
         return await internalGetPercentile(Deno.env.get("CORAL_SERVICE_NAME")!, metric, p);
       }
     } : undefined
   };
   ```

## Parameterization

Both execution models support parameters:

```protobuf
Script {
  parameters: {
    "threshold_ms": "500",
    "check_interval_sec": "30",
    "alert_channel": "slack"
  }
}
```

**Access in TypeScript**:

```typescript
const threshold = parseInt(coral.context.params.get("threshold_ms") || "500");
const interval = parseInt(coral.context.params.get("check_interval_sec") || "30");
const channel = coral.context.params.get("alert_channel") || "email";
```

**Deployment**:

```bash
# Service-scoped with parameters
coral script deploy latency-monitor \
  --service payments \
  --params threshold_ms=500,check_interval_sec=30,alert_channel=slack

# Global with parameters
coral script deploy correlation \
  --scope global \
  --params error_threshold=0.01,services=payments,orders,inventory
```

## Community Script Marketplace

### Service-Scoped Scripts are Perfect for Sharing

**Example Community Script**: High Latency Monitor

```yaml
# metadata.yaml
name: high-latency-monitor
version: 1.0.0
author: jane@example.com
scope: service
type: daemon
description: Monitor service P99 latency and alert on degradation
tags:
  - latency
  - monitoring
  - performance
parameters:
  threshold_ms:
    type: integer
    default: 500
    description: Alert threshold in milliseconds
  check_interval_sec:
    type: integer
    default: 30
    description: How often to check (seconds)
downloads: 12453
rating: 4.8
```

**Installation**:

```bash
# Search community scripts
coral script search "latency monitoring"

# Install and deploy
coral script install high-latency-monitor
coral script deploy high-latency-monitor --service payments --params threshold_ms=300
```

### Global Scripts are Architecture-Specific

**Not suitable for community sharing** because:
- Hardcoded service names
- Specific to your service topology
- Custom correlation logic
- Organization-specific

**Should be kept private** or shared within your organization.

## Decision Matrix

### Choose Global Scripts When:

- ✅ You need cross-service correlation
- ✅ Logic is specific to your architecture
- ✅ You need access to multiple services
- ✅ Implementing custom system-wide monitoring
- ✅ One-off diagnostic queries

### Choose Service-Scoped Scripts When:

- ✅ Logic is service-agnostic
- ✅ You want to reuse the script across services
- ✅ You plan to share in community marketplace
- ✅ Monitoring pattern applies to any service
- ✅ Standardized checks across fleet

## Implementation Status

| Component | Status | Notes |
|-----------|--------|-------|
| Protobuf schema | ✅ | ExecutionScope and ScriptType enums added |
| TypeScript types | ✅ | ServiceContext interface defined |
| Service-scoped examples | ✅ | Example scripts created |
| Global examples | ✅ | Cross-service correlation example |
| Executor support | ⏳ | Environment injection pending |
| SDK implementation | ⏳ | Service context injection pending |
| CLI support | ⏳ | --scope and --service flags pending |

## Examples

### Example 1: Service-Scoped - Database Query Monitor

```typescript
/**
 * Monitor slow database queries for any service.
 * Portable - works for any service with database metrics.
 */
import * as coral from "jsr:@coral/sdk";

const threshold = parseInt(coral.context.params.get("db_query_threshold_ms") || "100");
const service = coral.context.service!;

while (true) {
  // Auto-scoped to attached service
  const slowQueries = await coral.db.query(`
    SELECT query, duration_ms
    FROM database_queries
    WHERE service_name = '${service.name}'
      AND duration_ms > ${threshold}
    ORDER BY duration_ms DESC
    LIMIT 10
  `);

  if (slowQueries.count > 0) {
    await coral.emit("slow_queries", {
      service: service.name,
      query_count: slowQueries.count,
      slowest_ms: slowQueries.rows[0].duration_ms,
    }, "warning");
  }

  await new Promise(r => setTimeout(r, 60_000));
}
```

### Example 2: Global - Payment-Order Correlation

```typescript
/**
 * Detect when payment failures cause order failures.
 * Architecture-specific - not portable.
 */
import * as coral from "jsr:@coral/sdk";

while (true) {
  const paymentsErrors = await coral.traces.findErrors("payments", 5 * 60 * 1000);
  const ordersErrors = await coral.traces.findErrors("orders", 5 * 60 * 1000);

  // Find correlated traces
  const correlated = [];
  for (const paymentError of paymentsErrors.traces) {
    const orderTrace = await coral.traces.correlate(paymentError.traceId, ["orders"]);
    if (orderTrace.relatedTraces.length > 0) {
      correlated.push(paymentError.traceId);
    }
  }

  if (correlated.length > 0) {
    await coral.emit("payment_order_correlation", {
      correlated_failures: correlated.length,
      payment_errors: paymentsErrors.totalCount,
      order_errors: ordersErrors.totalCount,
    }, "error");
  }

  await new Promise(r => setTimeout(r, 120_000));
}
```

## Future Enhancements

### Multi-Service Scoped Scripts

Allow scripts to be scoped to multiple specific services:

```protobuf
Script {
  scope: EXECUTION_SCOPE_MULTI_SERVICE
  service_names: ["payments", "orders"]  // Limited set
}
```

### Namespace/Region Scoping

Allow scripts to be scoped by namespace or region:

```protobuf
Script {
  scope: EXECUTION_SCOPE_NAMESPACE
  namespace: "prod"  // All services in prod
}
```

### Dynamic Service Discovery

Allow service-scoped scripts to discover related services:

```typescript
// Future capability
const relatedServices = await coral.context.service.discoverDependencies();
// Returns: ["database", "cache", "auth"]
```

## References

- [RFD 076: Sandboxed TypeScript Execution](076-sandboxed-typescript-execution.md)
- [Protobuf Schema](../proto/coral/agent/v1/script.proto)
- [TypeScript Type Definitions](../pkg/sdk/typescript/types.d.ts)
- [Service-Scoped Example](../examples/scripts/service-scoped-latency-monitor.ts)
- [Global Example](../examples/scripts/global-cross-service-correlation.ts)
