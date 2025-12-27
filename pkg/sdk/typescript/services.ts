/**
 * Service discovery and management.
 *
 * @module
 */

import { getClient } from "./client.ts";
import type { ClientConfig, Service } from "./types.ts";

/**
 * List all discovered services.
 *
 * @param namespace - Optional namespace filter
 * @param config - Optional client configuration
 * @returns Array of discovered services
 *
 * @example
 * ```typescript
 * import { services } from "@coral/sdk";
 *
 * const allServices = await services.list();
 * console.log(`Found ${allServices.length} services`);
 *
 * const prodServices = await services.list("production");
 * ```
 */
export async function list(
  namespace?: string,
  config?: ClientConfig,
): Promise<Service[]> {
  const client = getClient(config);

  interface ListServicesRequest {
    namespace?: string;
  }

  interface ListServicesResponse {
    services: Array<{
      name: string;
      namespace: string;
      instanceCount: number;
      lastSeen?: string; // ISO timestamp
    }>;
  }

  const request: ListServicesRequest = {
    namespace,
  };

  const response = await client.call<
    ListServicesRequest,
    ListServicesResponse
  >(
    "coral.colony.v1.ColonyService",
    "ListServices",
    request,
  );

  // Convert response to Service objects
  return response.services.map((svc) => ({
    name: svc.name,
    namespace: svc.namespace,
    instanceCount: svc.instanceCount,
    lastSeen: svc.lastSeen ? new Date(svc.lastSeen) : undefined,
  }));
}

/**
 * Get details for a specific service.
 *
 * @param name - Service name
 * @param config - Optional client configuration
 * @returns Service details or null if not found
 *
 * @example
 * ```typescript
 * import { services } from "@coral/sdk";
 *
 * const svc = await services.get("payments");
 * if (svc) {
 *   console.log(`${svc.name} has ${svc.instanceCount} instances`);
 * }
 * ```
 */
export async function get(
  name: string,
  config?: ClientConfig,
): Promise<Service | null> {
  const allServices = await list(undefined, config);
  return allServices.find((svc) => svc.name === name) || null;
}
