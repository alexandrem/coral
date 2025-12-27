/**
 * Function metadata access (RFD 063).
 */

// This is a placeholder for function discovery integration.
// TODO: Implement function metadata access via SDK server.

/**
 * Function metadata.
 */
export interface FunctionInfo {
  name: string;
  package: string;
  file_path: string;
  line_number: number;
  offset: number;
  has_dwarf: boolean;
  service_name: string;
}

/**
 * List all functions for a service.
 *
 * @param service Service name
 * @returns List of functions
 *
 * @example
 * ```typescript
 * import { functions } from "@coral/sdk";
 *
 * const funcs = await functions.list("payments");
 * console.log(`Found ${funcs.length} functions`);
 * ```
 */
export async function list(service: string): Promise<FunctionInfo[]> {
  // TODO: Implement via SDK server endpoint.
  throw new Error("Not implemented yet");
}

/**
 * Get metadata for a specific function.
 *
 * @param service Service name
 * @param functionName Function name
 * @returns Function metadata
 *
 * @example
 * ```typescript
 * import { functions } from "@coral/sdk";
 *
 * const fn = await functions.get("payments", "ProcessPayment");
 * console.log(`Function offset: 0x${fn.offset.toString(16)}`);
 * ```
 */
export async function get(
  service: string,
  functionName: string,
): Promise<FunctionInfo | null> {
  // TODO: Implement via SDK server endpoint.
  throw new Error("Not implemented yet");
}

/**
 * Functions namespace.
 */
export const functions = {
  list,
  get,
};
