/**
 * Common types for the Coral SDK.
 *
 * @module
 */

/**
 * Service represents a discovered service in the mesh.
 */
export interface Service {
  name: string;
  namespace: string;
  instanceCount: number;
  lastSeen?: Date;
}

/**
 * MetricValue represents a metric measurement.
 */
export interface MetricValue {
  value: number;
  unit: string;
  timestamp?: Date;
}

/**
 * Trace represents a distributed trace.
 */
export interface Trace {
  traceId: string;
  durationNs: number;
  timestamp: Date;
  service: string;
}

/**
 * SystemMetrics represents system-level metrics for a service.
 */
export interface SystemMetrics {
  cpuPercent: number;
  memoryPercent: number;
  memoryBytes: number;
  timestamp: Date;
}

/**
 * QueryResult represents the result of a raw SQL query.
 */
export interface QueryResult {
  columns: string[];
  rows: Record<string, unknown>[];
  rowCount: number;
}

/**
 * RenderType selects the built-in browser renderer for a SkillResult panel.
 * Unknown strings are rendered as a formatted JSON block.
 */
export type RenderType = "table" | "bar" | "timeseries";

/**
 * RenderSpec describes how a skill result should be visualised in the browser
 * dashboard served by coral terminal (RFD 094).
 */
export interface RenderSpec {
  /**
   * Renderer type. Determines how payload is visualised in the browser
   * dashboard. Unknown types are rendered as a formatted JSON block.
   */
  type: RenderType | string;
  /** Title shown in the dashboard panel header. */
  title?: string;
  /** Renderer-specific payload. Shape is documented per RenderType. */
  payload: unknown;
}

/**
 * SkillResult is the structured output returned by a Coral skill function
 * (RFD 093). Skills write JSON.stringify(result) to stdout; coral run parses
 * it. The render field is optional and activates the browser dashboard when
 * coral terminal is running (RFD 094).
 */
export interface SkillResult {
  /** One-line summary shown to the LLM and in the conversation pane. */
  summary: string;
  /** Aggregate health status of the result. */
  status: "healthy" | "warning" | "critical" | "unknown";
  /** Structured data returned to the LLM for further analysis. */
  data: Record<string, unknown>;
  /** Optional actionable recommendations. */
  recommendations?: string[];
  /**
   * Optional browser visualisation spec. When present and coral terminal is
   * running, the executor pushes a RenderEvent to the dashboard WebSocket.
   * Omitting this field leaves behaviour identical to RFD 093.
   */
  render?: RenderSpec;
}

/**
 * SkillFn is the type signature for a Coral skill function (RFD 093).
 * Skills are exported as named SkillFn constants from their module.
 */
export type SkillFn<P = Record<string, unknown>> = (
  params: P
) => Promise<SkillResult>;

/**
 * Client configuration options.
 */
export interface ClientConfig {
  /**
   * Colony gRPC address (e.g., "localhost:9090").
   * Defaults to CORAL_COLONY_ADDR environment variable or "localhost:9090".
   */
  colonyAddr?: string;

  /**
   * Request timeout in milliseconds.
   * Default: 30000 (30 seconds).
   */
  timeout?: number;
}
