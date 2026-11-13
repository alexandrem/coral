# Coral TypeScript SDK Reference

`@coral/sdk` exports query and investigation modules for local Deno scripts.
For import maps, runtime setup, connection limitations, and script management,
use [TypeScript investigations](scripting.md). Command syntax lives in
the single [CLI reference](../CLI_REFERENCE.md).

The maintained interfaces are in [mod.ts](../../pkg/sdk/typescript/mod.ts) and
[types.ts](../../pkg/sdk/typescript/types.ts). The signatures below describe this
checkout. A declared interface or exported placeholder is not evidence that
an RPC-backed feature is implemented.

## Client Configuration

Query helpers generally accept an optional final `ClientConfig`:

```typescript
interface ClientConfig {
  colonyAddr?: string;
  timeout?: number;
}
```

`colonyAddr` is `host:port`, not a full URL. The current client constructs
`http://<host:port>` and sends JSON Connect requests. It defaults to
`CORAL_COLONY_ADDR`, then `localhost:9090`; `coral run` supplies the resolved
Colony host/port. The client does not forward `CORAL_API_TOKEN` or configure
CLI-style HTTPS CA trust. `timeout` is milliseconds, default 30,000.

A module-global client is created lazily on first use. A later helper call's
configuration does not recreate it, so choose the endpoint before the first
query rather than trying to switch endpoints between calls.

## Services API

```typescript
services.list(namespace?: string, config?: ClientConfig): Promise<Service[]>

interface Service {
  name: string;
  namespace: string;
  instanceCount: number;
  lastSeen?: Date;
}
```

This aggregates services from registered agents returned by `ListAgents`.
The optional namespace is a string. It does not accept `ListServicesOptions`,
`timeRange`, or `sourceFilter` objects, and does not expose the CLI's complete
merged registry/telemetry inventory. See [services and health](services.md)
when comparing CLI and SDK results.

Implementation: [services.ts](../../pkg/sdk/typescript/services.ts).

## Metrics API

```typescript
metrics.getPercentile(
  service: string, metric: string, percentile: number,
  timeRangeMs?: number, config?: ClientConfig,
): Promise<MetricValue>

metrics.getP50(service: string, metric: string, timeRangeMs?: number,
  config?: ClientConfig): Promise<MetricValue>
metrics.getP95(service: string, metric: string, timeRangeMs?: number,
  config?: ClientConfig): Promise<MetricValue>
metrics.getP99(service: string, metric: string, timeRangeMs?: number,
  config?: ClientConfig): Promise<MetricValue>

interface MetricValue {
  value: number;
  unit: string;
  timestamp?: Date;
}
```

`percentile` is a fraction between 0 and 1: use `0.99` for P99.
`timeRangeMs` defaults to 3,600,000 (one hour). Query windows are milliseconds,
while HTTP duration values such as `http.server.duration` use nanoseconds;
divide by 1,000,000 for milliseconds. Inspect the returned `unit` before
converting other metrics.

```typescript
const p99 = await coral.metrics.getP99("api", "http.server.duration", 600_000);
console.log({ value: p99.value, unit: p99.unit });
```

The example assumes `import * as coral from "@coral/sdk"` and the
[configured runtime](scripting.md#prepare-the-runtime-and-connection).
Implementation: [metrics.ts](../../pkg/sdk/typescript/metrics.ts).

## Activity API

```typescript
activity.listServiceActivity(timeRangeMs?: number,
  config?: ClientConfig): Promise<ServiceActivity[]>
activity.getServiceActivity(serviceName: string, timeRangeMs?: number,
  config?: ClientConfig): Promise<ServiceActivity | null>
activity.getServiceErrors(serviceName: string, timeRangeMs?: number,
  config?: ClientConfig): Promise<ServiceErrors>
```

The default lookback is one hour. Activity contains `serviceName`,
`requestCount`, `errorCount`, and an `errorRate` fraction. Error summaries
contain `errorCount`, `totalCount`, and `errorRate`. Multiply the fraction by
100 to display percent.

`getServiceActivity` returns null for a not-found response; other errors
propagate. `getServiceErrors` maps missing activity to zero counts, so zero
errors with zero total requests does not prove service health.

Implementation and local type definitions:
[activity.ts](../../pkg/sdk/typescript/activity.ts).

## Traces API

```typescript
traces.findSlow(service: string, minDurationNs: number,
  timeRangeMs?: number, limit?: number,
  config?: ClientConfig): Promise<Trace[]>
traces.findErrors(service: string, timeRangeMs?: number,
  limit?: number, config?: ClientConfig): Promise<Trace[]>
```

Both are currently placeholders: they warn and return an empty array without
querying the Colony. Their empty results do not establish that no slow or
error spans exist. Use `coral query traces` for implemented trace queries.
Defaults in the interface are one hour and 100 results; `minDurationNs` is
nanoseconds.

Implementation: [traces.ts](../../pkg/sdk/typescript/traces.ts).

## System API

```typescript
system.getMetrics(service: string, config?: ClientConfig): Promise<SystemMetrics>

interface SystemMetrics {
  cpuPercent: number;
  memoryPercent: number;
  memoryBytes: number;
  timestamp: Date;
}
```

This helper is currently a placeholder: it warns and returns zero values
with a current timestamp. Use `coral query summary` for collected host context.
The signature has no `timeRangeMs` argument.

Implementation: [system.ts](../../pkg/sdk/typescript/system.ts).

## Database API

```typescript
db.query(sql: string, maxRows?: number,
  config?: ClientConfig): Promise<QueryResult>

interface QueryResult {
  columns: string[];
  rows: Record<string, unknown>[];
  rowCount: number;
}
```

The default maximum is 1,000 rows. The client calls `ExecuteQuery` and maps
the response's string-valued rows to objects keyed by column name. Consume
`result.rows`, not `result` as if it were an array; convert numeric cell values
explicitly where needed.

```typescript
const result = await coral.db.query(
  "SELECT table_name FROM information_schema.tables ORDER BY table_name", 100,
);
for (const row of result.rows) console.log(row.table_name);
```

Inspect the current schema rather than assuming an example's table exists
on every software version. Query guardrails live on the server; the SDK
sandbox itself does not make all available RPC operations read-only.
Implementation: [db.ts](../../pkg/sdk/typescript/db.ts).

## Stateful Correlation API

```typescript
correlation.deploy(serviceName: string, descriptor: CorrelationDescriptor)
  : Promise<DeployCorrelationResponse>
correlation.list(serviceName?: string): Promise<CorrelationDescriptor[]>
correlation.remove(correlationId: string, serviceName?: string): Promise<void>
```

Deployment changes runtime state at the selected agent. Check `success` and
`error` in the response; keep the returned `correlationId` for inspection and
cleanup. Descriptors support strategies `rate_gate`, `edge_trigger`,
`causal_pair`, `absence`, `percentile_alarm`, and `sequence`. Actions include
`emit_event`, `goroutine_snapshot`, and `cpu_profile`; their execution requires
matching probe and runtime capabilities.

A descriptor provides `source` or `sourceA`/`sourceB` probe specifications,
`window`, `threshold`, and an `action`. Strategy-specific fields include
`field`, `percentile`, and `joinOn`. Probe filters use `filterExpr`.
`cooldownMs` and `action.profileDurationMs` are milliseconds. A percentile
fraction such as 0.99 and a `duration_ns` threshold use different units.

See the [deployment and cleanup example](scripting.md#stateful-correlations).
Full types: [types.ts](../../pkg/sdk/typescript/types.ts).
RPC wrappers: [correlation.ts](../../pkg/sdk/typescript/correlation.ts).

## Investigation Recipes

`coral.skills` exports `latencyReport`, `errorCorrelation`,
`memoryLeakDetector`, and `latencyTrap`. Import through the mapped root:

```typescript
import * as coral from "@coral/sdk";
const result = await coral.skills.latencyReport({ threshold_ms: 500 });
console.log(JSON.stringify(result));
```

These are experimental analysis recipes, not separate CLI commands or
documentation-authoring skills. Check the recipe source against its underlying
query results and schema before depending on its diagnosis. In particular,
a recipe that consumes `db.query` must account for the `QueryResult` wrapper;
its presence in the SDK does not guarantee compatibility with every current
storage schema. `latencyTrap` deploys an active correlation and needs explicit
cleanup when the investigation ends.

| Recipe | Parameters and defaults |
| --- | --- |
| `latencyReport` | `threshold_ms` 500; optional `service` |
| `errorCorrelation` | `threshold` 0.05; `window_ms` 300,000 |
| `memoryLeakDetector` | `window_ms` 900,000; `min_growth_rate_bps` 102,400 |
| `latencyTrap` | Required `service`, `function`, `threshold_ns`; `profile_duration_ms` 10,000 |

Recipe implementations: [skills/](../../pkg/sdk/typescript/skills).

### SkillResult Contract

```typescript
interface SkillResult {
  summary: string;
  status: "healthy" | "warning" | "critical" | "unknown";
  data: Record<string, unknown>;
  recommendations?: string[];
  render?: RenderSpec;
}

type SkillFn<P = Record<string, unknown>> = (params: P) => Promise<SkillResult>;

interface RenderSpec {
  type: "table" | "bar" | "timeseries" | string;
  title?: string;
  payload: unknown;
}
```

Write a final JSON result to stdout and progress to stderr when another tool
will parse the output. `render` can supply a dashboard panel when
`coral terminal` is running; unsupported renderer names fall back to formatted
JSON. Recipe statuses are interpretations, not independent health checks.

## Errors and Availability

The HTTP client throws on non-success responses and aborts requests exceeding
its timeout. Except for helper-specific handling described above, query errors
propagate. Reports covering several services should distinguish failed queries,
empty evidence, and observed health rather than replacing errors with zeros.

The local MCP proxy exposes `coral_cli` and does not currently implement the
resource-reading methods needed to assume `coral://sdk/reference` is available
through that proxy. Use this document and the maintained module sources when
writing scripts. See [AI assistants](ai-assistants.md) for client setup.
