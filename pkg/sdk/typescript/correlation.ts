/**
 * Correlation module for the Coral SDK.
 *
 * Provides functions for deploying, removing, and listing stateful correlation
 * descriptors on agents (RFD 091).
 *
 * @module
 */

import { getClient } from "./client.ts";
import type {
  CorrelationDescriptor,
  DeployCorrelationResponse,
} from "./types.ts";

/**
 * Deploy a correlation descriptor to the mesh.
 *
 * The orchestrator will find the agent for the specified service and forward
 * the descriptor.
 *
 * @param serviceName - Target service name.
 * @param descriptor - Correlation configuration.
 * @returns Deployment result with correlation ID and agent ID.
 */
export async function deploy(
  serviceName: string,
  descriptor: CorrelationDescriptor,
): Promise<DeployCorrelationResponse> {
  const client = getClient();
  return await client.call<
    { service_name: string; descriptor: CorrelationDescriptor },
    DeployCorrelationResponse
  >(
    "coral.colony.v1.ColonyDebugService",
    "DeployCorrelation",
    {
      service_name: serviceName,
      descriptor: descriptor,
    },
  );
}

/**
 * Remove an active correlation descriptor.
 *
 * @param correlationId - ID of the correlation to remove.
 * @param serviceName - Optional service name to speed up lookup.
 */
export async function remove(
  correlationId: string,
  serviceName?: string,
): Promise<void> {
  const client = getClient();
  await client.call(
    "coral.colony.v1.ColonyDebugService",
    "RemoveCorrelation",
    {
      correlation_id: correlationId,
      service_name: serviceName,
    },
  );
}

/**
 * List all active correlation descriptors across the mesh.
 *
 * @param serviceName - Optional service name to filter results.
 * @returns List of correlation descriptors.
 */
export async function list(
  serviceName?: string,
): Promise<CorrelationDescriptor[]> {
  const client = getClient();
  const resp = await client.call<{ service_name?: string }, { descriptors: CorrelationDescriptor[] }>(
    "coral.colony.v1.ColonyDebugService",
    "ListCorrelations",
    {
      service_name: serviceName,
    },
  );
  return resp.descriptors || [];
}
