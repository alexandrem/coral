/**
 * Coral SDK Type Definitions
 *
 * Comprehensive type definitions for the Coral observability SDK.
 * These types enable IDE autocompletion and type checking for scripts.
 *
 * @module @coral/sdk/types
 */

//
// ═══════════════════════════════════════════════════════════════
// LEVEL 1: PASSIVE QUERIES (Read-Only)
// ═══════════════════════════════════════════════════════════════
//

/**
 * Database query interface for raw SQL access (fallback).
 */
export namespace DB {
  /**
   * Query options with automatic guardrails.
   */
  export interface QueryOptions {
    /** Disable automatic LIMIT injection (default: false) */
    disableLimitInjection?: boolean;
    /** Disable automatic time filter injection (default: false) */
    disableTimeFilterInjection?: boolean;
    /** Query type affects timeout (ad-hoc: 60s, daemon: 24h) */
    queryType?: "adhoc" | "daemon";
  }

  /**
   * Query result row (column name → value mapping).
   */
  export interface Row {
    [column: string]: string | number | boolean | Date | null;
  }

  /**
   * Query execution statistics.
   */
  export interface QueryStats {
    /** Execution time in milliseconds */
    executionTimeMs: number;
    /** Number of rows scanned */
    rowsScanned: number;
    /** Bytes scanned during query */
    bytesScanned: number;
    /** True if LIMIT or time filters were auto-injected */
    guardrailsApplied: boolean;
  }

  /**
   * Query result.
   */
  export interface QueryResult {
    /** Result rows */
    rows: Row[];
    /** Number of rows returned */
    count: number;
    /** True if result was truncated (hit row limit) */
    truncated: boolean;
    /** Query execution statistics */
    stats: QueryStats;
  }

  /**
   * Execute a raw SQL query against local DuckDB.
   *
   * **Automatic Guardrails Applied:**
   * - `LIMIT 10000` injected unless disabled
   * - `WHERE timestamp > now() - INTERVAL '1 hour'` injected unless disabled
   *
   * @param sql - SQL query to execute
   * @param options - Query options
   * @returns Query results with statistics
   *
   * @example
   * ```typescript
   * const result = await db.query(`
   *   SELECT * FROM otel_spans_local
   *   WHERE service_name = 'payments'
   *     AND is_error = true
   *   ORDER BY start_time DESC
   * `);
   * console.log(`Found ${result.count} errors`);
   * ```
   */
  export function query(sql: string, options?: QueryOptions): Promise<QueryResult>;
}

/**
 * High-level metrics helpers (preferred over raw SQL).
 */
export namespace Metrics {
  /**
   * Get percentile value for a metric.
   *
   * @param service - Service name
   * @param metric - Metric name (e.g., "http.server.duration")
   * @param percentile - Percentile value (0.0 - 1.0)
   * @param timeWindowMs - Time window in milliseconds (default: 5 minutes)
   * @returns Percentile value with unit and stats
   *
   * @example
   * ```typescript
   * const p99 = await metrics.getPercentile("payments", "http.server.duration", 0.99);
   * console.log(`P99 latency: ${p99.value / 1_000_000}ms`);
   * ```
   */
  export function getPercentile(
    service: string,
    metric: string,
    percentile: number,
    timeWindowMs?: number
  ): Promise<{
    value: number;
    unit: "nanoseconds" | "milliseconds" | "seconds";
    stats: DB.QueryStats;
  }>;

  /**
   * Get error rate for a service.
   *
   * @param service - Service name
   * @param timeWindowMs - Time window in milliseconds (default: 5 minutes)
   * @returns Error rate (0.0 - 1.0) with statistics
   *
   * @example
   * ```typescript
   * const errorRate = await metrics.getErrorRate("payments", 300_000); // 5 min
   * if (errorRate.rate > 0.01) {
   *   console.log(`High error rate: ${(errorRate.rate * 100).toFixed(2)}%`);
   * }
   * ```
   */
  export function getErrorRate(
    service: string,
    timeWindowMs?: number
  ): Promise<{
    rate: number;
    totalRequests: number;
    errorRequests: number;
    stats: DB.QueryStats;
  }>;

  /**
   * Get request rate for a service (requests per second).
   *
   * @param service - Service name
   * @param timeWindowMs - Time window in milliseconds (default: 5 minutes)
   * @returns Request rate in requests/second
   */
  export function getRequestRate(
    service: string,
    timeWindowMs?: number
  ): Promise<{
    rps: number;
    totalRequests: number;
    stats: DB.QueryStats;
  }>;
}

/**
 * Trace query helpers.
 */
export namespace Traces {
  /**
   * Trace span data.
   */
  export interface Trace {
    traceId: string;
    spanId: string;
    serviceName: string;
    durationNs: number;
    isError: boolean;
    httpStatus: number;
    httpMethod: string;
    httpRoute: string;
    startTime: Date;
    attributes: Record<string, string>;
  }

  /**
   * Trace correlation information.
   */
  export interface Correlation {
    fromService: string;
    toService: string;
    correlationType: "http" | "grpc" | "queue" | "database";
    latencyNs: number;
  }

  /**
   * Find slow traces for a service.
   *
   * @param service - Service name
   * @param minDurationMs - Minimum duration in milliseconds
   * @param timeRangeMs - Time range to search (default: 1 hour)
   * @param limit - Maximum traces to return (default: 100, max: 10000)
   * @returns Slow traces with statistics
   *
   * @example
   * ```typescript
   * const slowTraces = await traces.findSlow("payments", 500); // >500ms
   * for (const trace of slowTraces.traces) {
   *   console.log(`Slow trace: ${trace.traceId} (${trace.durationNs / 1_000_000}ms)`);
   * }
   * ```
   */
  export function findSlow(
    service: string,
    minDurationMs: number,
    timeRangeMs?: number,
    limit?: number
  ): Promise<{
    traces: Trace[];
    totalCount: number;
    stats: DB.QueryStats;
  }>;

  /**
   * Correlate traces across services.
   *
   * @param traceId - Root trace ID
   * @param services - Services to correlate (default: all)
   * @returns Related traces and correlation information
   *
   * @example
   * ```typescript
   * const correlated = await traces.correlate("trace-123", ["payments", "orders"]);
   * for (const correlation of correlated.correlations) {
   *   console.log(`${correlation.fromService} → ${correlation.toService}: ${correlation.latencyNs / 1_000_000}ms`);
   * }
   * ```
   */
  export function correlate(
    traceId: string,
    services?: string[]
  ): Promise<{
    relatedTraces: Trace[];
    correlations: Correlation[];
    stats: DB.QueryStats;
  }>;

  /**
   * Find error traces for a service.
   *
   * @param service - Service name
   * @param timeRangeMs - Time range to search (default: 1 hour)
   * @param limit - Maximum traces to return (default: 100)
   * @returns Error traces
   */
  export function findErrors(
    service: string,
    timeRangeMs?: number,
    limit?: number
  ): Promise<{
    traces: Trace[];
    totalCount: number;
    stats: DB.QueryStats;
  }>;
}

/**
 * System metrics helpers.
 */
export namespace System {
  /**
   * System metric value.
   */
  export interface Metric {
    value: number;
    unit: string;
    timestamp: Date;
  }

  /**
   * Get current CPU utilization.
   *
   * @returns CPU usage percentage (0-100)
   *
   * @example
   * ```typescript
   * const cpu = await system.getCPU();
   * console.log(`CPU: ${cpu.value.toFixed(1)}%`);
   * ```
   */
  export function getCPU(): Promise<Metric>;

  /**
   * Get current memory usage.
   *
   * @returns Memory usage in bytes
   *
   * @example
   * ```typescript
   * const memory = await system.getMemory();
   * const usedGB = memory.value / (1024 ** 3);
   * console.log(`Memory: ${usedGB.toFixed(1)}GB`);
   * ```
   */
  export function getMemory(): Promise<Metric>;

  /**
   * Get multiple system metrics at once.
   *
   * @param metricNames - Metrics to fetch ("cpu", "memory", "disk", "network")
   * @returns Map of metric name to value
   */
  export function getMetrics(
    metricNames: string[]
  ): Promise<{
    metrics: Record<string, Metric>;
    stats: DB.QueryStats;
  }>;
}

/**
 * Function metadata access (RFD 063 integration).
 */
export namespace Functions {
  /**
   * Function metadata.
   */
  export interface FunctionInfo {
    name: string;
    package: string;
    filePath: string;
    lineNumber: number;
    offset: number;
    hasDwarf: boolean;
    serviceName: string;
  }

  /**
   * List all functions for a service.
   *
   * @param service - Service name
   * @returns List of discovered functions
   */
  export function list(service: string): Promise<FunctionInfo[]>;

  /**
   * Get metadata for a specific function.
   *
   * @param service - Service name
   * @param functionName - Function name
   * @returns Function metadata or null if not found
   */
  export function get(service: string, functionName: string): Promise<FunctionInfo | null>;
}

//
// ═══════════════════════════════════════════════════════════════
// LEVEL 2: ACTIVE OPERATIONS (Event Emission)
// ═══════════════════════════════════════════════════════════════
//

/**
 * Event emission for alerts and custom metrics.
 */
export namespace Events {
  /**
   * Event severity levels.
   */
  export type Severity = "info" | "warning" | "error" | "critical";

  /**
   * Event data payload (arbitrary key-value pairs).
   */
  export type EventData = Record<string, string | number | boolean | Date>;

  /**
   * Emit a custom event to colony.
   *
   * Events are collected by the agent and forwarded to colony for aggregation.
   *
   * @param name - Event type (e.g., "alert", "metric", "correlation")
   * @param data - Event payload
   * @param severity - Event severity level
   * @returns Event ID for tracking
   *
   * @example
   * ```typescript
   * await emit("alert", {
   *   message: "High latency detected",
   *   service: "payments",
   *   p99_ms: 650,
   *   threshold_ms: 500,
   * }, "warning");
   * ```
   */
  export function emit(
    name: string,
    data: EventData,
    severity?: Severity
  ): Promise<{
    success: boolean;
    eventId: string;
  }>;
}

//
// ═══════════════════════════════════════════════════════════════
// LEVEL 3: DYNAMIC INSTRUMENTATION (Requires Elevated Permissions)
// ═══════════════════════════════════════════════════════════════
//

/**
 * Dynamic eBPF instrumentation (opt-in, requires authorization).
 */
export namespace Trace {
  /**
   * Probe configuration.
   */
  export interface ProbeConfig {
    /** Capture function arguments */
    captureArgs?: boolean;
    /** Capture return value */
    captureReturn?: boolean;
    /** Capture stack trace */
    captureStackTrace?: boolean;
    /** Sampling rate (1 = every event, 100 = 1 in 100) */
    sampleRate?: number;
    /** Filters to apply */
    filters?: ProbeFilter[];
  }

  /**
   * Probe filter.
   */
  export interface ProbeFilter {
    /** Field to filter on ("pid", "uid", "arg0", etc.) */
    field: string;
    /** Comparison operator */
    operator: "==" | "!=" | ">" | "<" | "contains";
    /** Value to compare */
    value: string | number;
  }

  /**
   * Uprobe event data.
   */
  export interface UprobeEvent {
    timestamp: Date;
    pid: number;
    tid: number;
    functionName: string;
    args: Argument[];
    returnValue?: Argument;
    stackTrace?: string[];
    durationNs?: number;
  }

  /**
   * Function argument.
   */
  export interface Argument {
    name: string;
    type: string;
    value: string;
  }

  /**
   * Attach a userspace probe to a function.
   *
   * **Requires elevated permissions** (Level 3).
   *
   * @param serviceName - Service name
   * @param functionName - Function to probe (e.g., "main.ProcessPayment")
   * @param config - Probe configuration
   * @param authToken - Authorization token
   * @returns Async iterator of probe events
   *
   * @example
   * ```typescript
   * // Attach probe to slow payment processing
   * const probe = trace.uprobe("payments", "main.ProcessPayment", {
   *   captureArgs: true,
   *   captureReturn: true,
   *   sampleRate: 10, // 1 in 10 calls
   * }, AUTH_TOKEN);
   *
   * for await (const event of probe) {
   *   console.log(`Called at ${event.timestamp}, took ${event.durationNs / 1_000_000}ms`);
   *   if (event.durationNs > 500_000_000) {
   *     await emit("slow_call", { event }, "warning");
   *   }
   * }
   * ```
   */
  export function uprobe(
    serviceName: string,
    functionName: string,
    config: ProbeConfig,
    authToken: string
  ): AsyncIterableIterator<UprobeEvent>;

  /**
   * Attach a kernel probe.
   *
   * **Requires elevated permissions** (Level 3).
   *
   * @param kernelFunction - Kernel function to probe (e.g., "sys_open")
   * @param config - Probe configuration
   * @param authToken - Authorization token
   * @returns Async iterator of probe events
   */
  export function kprobe(
    kernelFunction: string,
    config: ProbeConfig,
    authToken: string
  ): AsyncIterableIterator<UprobeEvent>;

  /**
   * Detach a probe.
   *
   * @param probeId - Probe ID returned from uprobe/kprobe
   */
  export function detach(probeId: string): Promise<{ success: boolean }>;
}

//
// ═══════════════════════════════════════════════════════════════
// UTILITY TYPES
// ═══════════════════════════════════════════════════════════════
//

/**
 * Service context (injected when ExecutionScope = SERVICE).
 * Provides dependency injection for service-scoped scripts.
 */
export interface ServiceContext {
  /** Service name (e.g., "payments") */
  name: string;
  /** Service namespace/environment (e.g., "prod", "staging") */
  namespace: string;
  /** Service deployment region (e.g., "us-east-1") */
  region: string;
  /** Service version (e.g., "v1.2.3") */
  version: string;
  /** Deployment identifier */
  deployment: string;

  /**
   * Get percentile metric for this service (auto-scoped).
   *
   * @param metric - Metric name
   * @param percentile - Percentile value (0.0 - 1.0)
   * @param timeWindowMs - Time window in milliseconds
   * @returns Percentile value with statistics
   *
   * @example
   * ```typescript
   * // Service-scoped: No need to specify service name
   * const p99 = await coral.context.service.getPercentile("http.server.duration", 0.99);
   * ```
   */
  getPercentile(
    metric: string,
    percentile: number,
    timeWindowMs?: number
  ): Promise<{
    value: number;
    unit: string;
    stats: DB.QueryStats;
  }>;

  /**
   * Get error rate for this service (auto-scoped).
   *
   * @param timeWindowMs - Time window in milliseconds
   * @returns Error rate with statistics
   */
  getErrorRate(timeWindowMs?: number): Promise<{
    rate: number;
    totalRequests: number;
    errorRequests: number;
    stats: DB.QueryStats;
  }>;

  /**
   * Find slow traces for this service (auto-scoped).
   *
   * @param minDurationMs - Minimum duration in milliseconds
   * @param timeRangeMs - Time range to search
   * @param limit - Maximum traces to return
   * @returns Slow traces with statistics
   */
  findSlowTraces(
    minDurationMs: number,
    timeRangeMs?: number,
    limit?: number
  ): Promise<{
    traces: Traces.Trace[];
    totalCount: number;
    stats: DB.QueryStats;
  }>;

  /**
   * Find error traces for this service (auto-scoped).
   *
   * @param timeRangeMs - Time range to search
   * @param limit - Maximum traces to return
   * @returns Error traces
   */
  findErrors(
    timeRangeMs?: number,
    limit?: number
  ): Promise<{
    traces: Traces.Trace[];
    totalCount: number;
    stats: DB.QueryStats;
  }>;
}

/**
 * Script execution context (injected by Deno runtime).
 */
export interface ScriptContext {
  /** Unique script ID */
  scriptId: string;
  /** Unique execution ID */
  executionId: string;
  /** Script type (adhoc or daemon) */
  scriptType: "adhoc" | "daemon";
  /** Execution scope (global or service) */
  scope: "global" | "service";
  /** Execution start time */
  startedAt: Date;

  /**
   * Service context (only available when scope = "service").
   * Provides auto-scoped methods for the attached service.
   */
  service?: ServiceContext;

  /**
   * Script parameters (key-value pairs from deployment).
   * Access via: coral.context.params.get("threshold_ms")
   */
  params: Map<string, string>;
}

/**
 * Global Coral SDK instance.
 */
export interface CoralSDK {
  db: typeof DB;
  metrics: typeof Metrics;
  traces: typeof Traces;
  system: typeof System;
  functions: typeof Functions;
  emit: typeof Events.emit;
  trace: typeof Trace;
  context: ScriptContext;
}

/**
 * Declare global Coral SDK instance.
 */
declare global {
  const coral: CoralSDK;
}

export {};
