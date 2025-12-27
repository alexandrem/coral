/**
 * Service activity and health metrics.
 *
 * @module
 */

import { getClient } from "./client.ts";
import type { ClientConfig } from "./types.ts";

/**
 * Service activity statistics.
 */
export interface ServiceActivity {
  serviceName: string;
  requestCount: number;
  errorCount: number;
  errorRate: number;
}

/**
 * Service error statistics (alias for activity data).
 */
export interface ServiceErrors {
  errorCount: number;
  totalCount: number;
  errorRate: number;
}

/**
 * Get activity statistics for all services.
 *
 * @param timeRangeMs - Lookback window in milliseconds (default: 1 hour)
 * @param config - Optional client configuration
 * @returns Array of service activity statistics
 *
 * @example
 * ```typescript
 * import { activity } from "@coral/sdk";
 *
 * const stats = await activity.listServiceActivity();
 * for (const svc of stats) {
 *   console.log(`${svc.serviceName}: ${svc.requestCount} requests, ${svc.errorRate * 100}% errors`);
 * }
 * ```
 */
export async function listServiceActivity(
  timeRangeMs: number = 3600000, // 1 hour default
  config?: ClientConfig,
): Promise<ServiceActivity[]> {
  const client = getClient(config);

  interface ListServiceActivityRequest {
    timeRangeMs: number;
  }

  interface ListServiceActivityResponse {
    services: Array<{
      serviceName: string;
      requestCount: number;
      errorCount: number;
      errorRate: number;
    }>;
  }

  const request: ListServiceActivityRequest = {
    timeRangeMs,
  };

  const response = await client.call<
    ListServiceActivityRequest,
    ListServiceActivityResponse
  >(
    "coral.colony.v1.ColonyService",
    "ListServiceActivity",
    request,
  );

  return response.services.map((svc) => ({
    serviceName: svc.serviceName,
    requestCount: Number(svc.requestCount),
    errorCount: Number(svc.errorCount),
    errorRate: svc.errorRate,
  }));
}

/**
 * Get activity statistics for a specific service.
 *
 * @param serviceName - Service name
 * @param timeRangeMs - Lookback window in milliseconds (default: 1 hour)
 * @param config - Optional client configuration
 * @returns Service activity statistics or null if not found
 *
 * @example
 * ```typescript
 * import { activity } from "@coral/sdk";
 *
 * const stats = await activity.getServiceActivity("payments");
 * if (stats) {
 *   console.log(`Requests: ${stats.requestCount}`);
 *   console.log(`Errors: ${stats.errorCount} (${(stats.errorRate * 100).toFixed(2)}%)`);
 * }
 * ```
 */
export async function getServiceActivity(
  serviceName: string,
  timeRangeMs: number = 3600000,
  config?: ClientConfig,
): Promise<ServiceActivity | null> {
  const client = getClient(config);

  interface GetServiceActivityRequest {
    service: string;
    timeRangeMs: number;
  }

  interface GetServiceActivityResponse {
    serviceName: string;
    requestCount: number;
    errorCount: number;
    errorRate: number;
    timestamp?: string;
  }

  const request: GetServiceActivityRequest = {
    service: serviceName,
    timeRangeMs,
  };

  try {
    const response = await client.call<
      GetServiceActivityRequest,
      GetServiceActivityResponse
    >(
      "coral.colony.v1.ColonyService",
      "GetServiceActivity",
      request,
    );

    return {
      serviceName: response.serviceName,
      requestCount: Number(response.requestCount),
      errorCount: Number(response.errorCount),
      errorRate: response.errorRate,
    };
  } catch (error) {
    // Return null if service not found
    if (error instanceof Error && error.message.includes("not found")) {
      return null;
    }
    throw error;
  }
}

/**
 * Get error statistics for a specific service.
 *
 * @param serviceName - Service name
 * @param timeRangeMs - Lookback window in milliseconds (default: 1 hour)
 * @param config - Optional client configuration
 * @returns Error statistics
 *
 * @example
 * ```typescript
 * import { activity } from "@coral/sdk";
 *
 * const errors = await activity.getServiceErrors("payments", 5 * 60 * 1000);
 * console.log(`Error rate: ${(errors.errorRate * 100).toFixed(2)}%`);
 * ```
 */
export async function getServiceErrors(
  serviceName: string,
  timeRangeMs: number = 3600000,
  config?: ClientConfig,
): Promise<ServiceErrors> {
  const activity = await getServiceActivity(serviceName, timeRangeMs, config);

  if (!activity) {
    return {
      errorCount: 0,
      totalCount: 0,
      errorRate: 0,
    };
  }

  return {
    errorCount: activity.errorCount,
    totalCount: activity.requestCount,
    errorRate: activity.errorRate,
  };
}
