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
 * Uses the same registry-based approach as the CLI to list services
 * from agent registrations, not from metrics data.
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

  interface ListAgentsRequest {}

  interface Agent {
    agentId: string;
    componentName: string;
    services?: Array<{
      name: string;
      namespace?: string;
      port?: number;
    }>;
  }

  interface ListAgentsResponse {
    agents: Agent[];
  }

  // Call ListAgents to get registry data (same as CLI approach).
  const response = await client.call<
    ListAgentsRequest,
    ListAgentsResponse
  >(
    "coral.colony.v1.ColonyService",
    "ListAgents",
    {},
  );

  // Handle missing or empty agents array
  if (!response || !response.agents) {
    return [];
  }

  // Aggregate services from agents (same logic as CLI).
  const serviceMap = new Map<string, Service>();

  for (const agent of response.agents) {
    // Handle legacy single-service agents (componentName).
    if (agent.componentName && !agent.services) {
      const key = agent.componentName.toLowerCase();
      if (!serviceMap.has(key)) {
        serviceMap.set(key, {
          name: agent.componentName,
          namespace: namespace || "",
          instanceCount: 0,
        });
      }
      const svc = serviceMap.get(key)!;
      svc.instanceCount++;
    }

    // Handle multi-service agents.
    if (agent.services) {
      for (const svcInfo of agent.services) {
        const key = svcInfo.name.toLowerCase();
        if (!serviceMap.has(key)) {
          serviceMap.set(key, {
            name: svcInfo.name,
            namespace: svcInfo.namespace || namespace || "",
            instanceCount: 0,
          });
        }
        const svc = serviceMap.get(key)!;
        svc.instanceCount++;
      }
    }
  }

  // Convert map to array and filter by namespace if specified.
  let serviceList = Array.from(serviceMap.values());

  if (namespace) {
    serviceList = serviceList.filter((svc) =>
      svc.namespace?.toLowerCase() === namespace.toLowerCase()
    );
  }

  // Sort by name for consistent ordering.
  serviceList.sort((a, b) => a.name.localeCompare(b.name));

  return serviceList;
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
