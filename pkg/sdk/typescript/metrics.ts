/**
 * High-level metrics query helpers.
 */

const SDK_URL = Deno.env.get("CORAL_SDK_URL") || "http://localhost:9003";

/**
 * Get percentile value for a metric.
 *
 * @param service Service name
 * @param metric Metric name
 * @param p Percentile (0-1)
 * @returns Percentile value in nanoseconds
 *
 * @example
 * ```typescript
 * import { metrics } from "@coral/sdk";
 *
 * const p99 = await metrics.getPercentile("payments", "http.server.duration", 0.99);
 * console.log(`P99 latency: ${p99 / 1_000_000}ms`);
 * ```
 */
export async function getPercentile(
  service: string,
  metric: string,
  p: number,
): Promise<number> {
  const response = await fetch(
    `${SDK_URL}/metrics/percentile?service=${encodeURIComponent(service)}&metric=${encodeURIComponent(metric)}&p=${p}`,
  );

  if (!response.ok) {
    const error = await response.text();
    throw new Error(`Failed to get percentile: ${error}`);
  }

  const result = await response.json();
  return result.value;
}

/**
 * Get error rate for a service.
 *
 * @param service Service name
 * @param window Time window (e.g., "5m", "1h")
 * @returns Error rate (0-1)
 *
 * @example
 * ```typescript
 * import { metrics } from "@coral/sdk";
 *
 * const errorRate = await metrics.getErrorRate("payments", "5m");
 * if (errorRate > 0.01) {
 *   console.log(`Error rate: ${(errorRate * 100).toFixed(2)}%`);
 * }
 * ```
 */
export async function getErrorRate(
  service: string,
  window = "5m",
): Promise<number> {
  const response = await fetch(
    `${SDK_URL}/metrics/error-rate?service=${encodeURIComponent(service)}&window=${encodeURIComponent(window)}`,
  );

  if (!response.ok) {
    const error = await response.text();
    throw new Error(`Failed to get error rate: ${error}`);
  }

  const result = await response.json();
  return result.value;
}

/**
 * Metrics namespace.
 */
export const metrics = {
  getPercentile,
  getErrorRate,
};
