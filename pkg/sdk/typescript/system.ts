/**
 * System metrics helpers.
 */

const SDK_URL = Deno.env.get("CORAL_SDK_URL") || "http://localhost:9003";

/**
 * CPU usage data.
 */
export interface CPUUsage {
  usage_percent: number;
}

/**
 * Memory usage data.
 */
export interface MemoryUsage {
  used: number;
  total: number;
}

/**
 * Get current CPU usage.
 *
 * @returns CPU usage data
 *
 * @example
 * ```typescript
 * import { system } from "@coral/sdk";
 *
 * const cpu = await system.getCPU();
 * console.log(`CPU usage: ${cpu.usage_percent.toFixed(1)}%`);
 * ```
 */
export async function getCPU(): Promise<CPUUsage> {
  const response = await fetch(`${SDK_URL}/system/cpu`);

  if (!response.ok) {
    const error = await response.text();
    throw new Error(`Failed to get CPU usage: ${error}`);
  }

  return await response.json();
}

/**
 * Get current memory usage.
 *
 * @returns Memory usage data
 *
 * @example
 * ```typescript
 * import { system } from "@coral/sdk";
 *
 * const memory = await system.getMemory();
 * const usagePercent = (memory.used / memory.total) * 100;
 * console.log(`Memory usage: ${usagePercent.toFixed(1)}%`);
 * ```
 */
export async function getMemory(): Promise<MemoryUsage> {
  const response = await fetch(`${SDK_URL}/system/memory`);

  if (!response.ok) {
    const error = await response.text();
    throw new Error(`Failed to get memory usage: ${error}`);
  }

  return await response.json();
}

/**
 * System namespace.
 */
export const system = {
  getCPU,
  getMemory,
};
