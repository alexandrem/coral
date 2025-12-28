/**
 * System metrics queries.
 *
 * Note: System metrics use the unified query API (QueryUnifiedSummary).
 * For now, this module provides placeholder functions. Full implementation
 * will be added when Colony system metrics storage is ready.
 *
 * @module
 */

import type { ClientConfig, SystemMetrics } from "./types.ts";

/**
 * Get system metrics for a service.
 *
 * @param service - Service name
 * @param config - Optional client configuration
 * @returns System metrics
 *
 * @example
 * ```typescript
 * import * as coral from "@coral/sdk";
 *
 * const metrics = await coral.system.getMetrics("payments");
 * console.log(`CPU: ${metrics.cpuPercent}%`);
 * console.log(`Memory: ${metrics.memoryPercent}%`);
 * ```
 */
export async function getMetrics(
  service: string,
  config?: ClientConfig,
): Promise<SystemMetrics> {
  // TODO: Implement using QueryUnifiedSummary or a dedicated GetSystemMetrics RPC
  console.warn(
    "system.getMetrics() not yet implemented - Colony system metrics storage pending",
  );

  // Return placeholder data
  return {
    cpuPercent: 0,
    memoryPercent: 0,
    memoryBytes: 0,
    timestamp: new Date(),
  };
}
