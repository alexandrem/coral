/**
 * Metrics queries and analysis.
 *
 * @module
 */

import { getClient } from "./client.ts";
import type { ClientConfig, MetricValue } from "./types.ts";

/**
 * Get a specific percentile value for a metric.
 *
 * @param service - Service name
 * @param metric - Metric name (e.g., "http.server.duration")
 * @param percentile - Percentile value (0.0-1.0, e.g., 0.99 for P99)
 * @param timeRangeMs - Lookback window in milliseconds (default: 1 hour)
 * @param config - Optional client configuration
 * @returns Metric value with unit
 *
 * @example
 * ```typescript
 * import { metrics } from "@coral/sdk";
 *
 * // Get P99 latency for last hour
 * const p99 = await metrics.getPercentile(
 *   "payments",
 *   "http.server.duration",
 *   0.99,
 * );
 * console.log(`P99 latency: ${p99.value / 1_000_000} ms`);
 *
 * // Get P50 latency for last 5 minutes
 * const p50 = await metrics.getPercentile(
 *   "payments",
 *   "http.server.duration",
 *   0.50,
 *   5 * 60 * 1000,
 * );
 * ```
 */
export async function getPercentile(
  service: string,
  metric: string,
  percentile: number,
  timeRangeMs: number = 3600000, // 1 hour default
  config?: ClientConfig,
): Promise<MetricValue> {
  const client = getClient(config);

  interface GetMetricPercentileRequest {
    service: string;
    metric: string;
    percentile: number;
    timeRangeMs: number;
  }

  interface GetMetricPercentileResponse {
    value: number;
    unit: string;
    timestamp?: string;
  }

  const request: GetMetricPercentileRequest = {
    service,
    metric,
    percentile,
    timeRangeMs,
  };

  const response = await client.call<
    GetMetricPercentileRequest,
    GetMetricPercentileResponse
  >(
    "coral.colony.v1.ColonyService",
    "GetMetricPercentile",
    request,
  );

  return {
    value: response.value,
    unit: response.unit,
    timestamp: response.timestamp ? new Date(response.timestamp) : undefined,
  };
}

/**
 * Get P99 latency for a service.
 *
 * Convenience function for getPercentile with P99.
 *
 * @param service - Service name
 * @param metric - Metric name
 * @param timeRangeMs - Lookback window in milliseconds
 * @param config - Optional client configuration
 * @returns P99 metric value
 *
 * @example
 * ```typescript
 * import { metrics } from "@coral/sdk";
 *
 * const p99 = await metrics.getP99("payments", "http.server.duration");
 * console.log(`P99: ${p99.value / 1_000_000} ms`);
 * ```
 */
export async function getP99(
  service: string,
  metric: string,
  timeRangeMs?: number,
  config?: ClientConfig,
): Promise<MetricValue> {
  return getPercentile(service, metric, 0.99, timeRangeMs, config);
}

/**
 * Get P95 latency for a service.
 *
 * Convenience function for getPercentile with P95.
 *
 * @param service - Service name
 * @param metric - Metric name
 * @param timeRangeMs - Lookback window in milliseconds
 * @param config - Optional client configuration
 * @returns P95 metric value
 */
export async function getP95(
  service: string,
  metric: string,
  timeRangeMs?: number,
  config?: ClientConfig,
): Promise<MetricValue> {
  return getPercentile(service, metric, 0.95, timeRangeMs, config);
}

/**
 * Get P50 (median) latency for a service.
 *
 * Convenience function for getPercentile with P50.
 *
 * @param service - Service name
 * @param metric - Metric name
 * @param timeRangeMs - Lookback window in milliseconds
 * @param config - Optional client configuration
 * @returns P50 metric value
 */
export async function getP50(
  service: string,
  metric: string,
  timeRangeMs?: number,
  config?: ClientConfig,
): Promise<MetricValue> {
  return getPercentile(service, metric, 0.50, timeRangeMs, config);
}
