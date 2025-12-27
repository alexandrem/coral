/**
 * Trace and span query helpers.
 */

const SDK_URL = Deno.env.get("CORAL_SDK_URL") || "http://localhost:9003";

/**
 * Trace span data.
 */
export interface Trace {
  trace_id: string;
  span_id: string;
  duration_ns: number;
  is_error: boolean;
  http_status: number;
  http_method: string;
  http_route: string;
}

/**
 * Query filter options.
 */
export interface QueryOptions {
  /** Service name */
  service: string;
  /** Minimum duration (e.g., "500ms", "1s") */
  minDuration?: string;
  /** Time range (e.g., "1h", "5m") */
  timeRange?: string;
}

/**
 * Query traces matching the filter.
 *
 * @param options Query filter options
 * @returns Matching traces
 *
 * @example
 * ```typescript
 * import { traces } from "@coral/sdk";
 *
 * const slowTraces = await traces.query({
 *   service: "payments",
 *   minDuration: "500ms",
 *   timeRange: "1h",
 * });
 *
 * for (const trace of slowTraces) {
 *   console.log(`Slow trace: ${trace.trace_id} (${trace.duration_ns / 1_000_000}ms)`);
 * }
 * ```
 */
export async function query(options: QueryOptions): Promise<Trace[]> {
  const params = new URLSearchParams({
    service: options.service,
  });

  if (options.minDuration) {
    params.set("minDuration", options.minDuration);
  }

  if (options.timeRange) {
    params.set("timeRange", options.timeRange);
  }

  const response = await fetch(`${SDK_URL}/traces/query?${params}`);

  if (!response.ok) {
    const error = await response.text();
    throw new Error(`Failed to query traces: ${error}`);
  }

  const result = await response.json();
  return result.traces;
}

/**
 * Traces namespace.
 */
export const traces = {
  query,
};
