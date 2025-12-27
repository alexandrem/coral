/**
 * Event emission helpers for sending custom events to colony.
 */

const SDK_URL = Deno.env.get("CORAL_SDK_URL") || "http://localhost:9003";

/**
 * Event severity levels.
 */
export type EventSeverity = "info" | "warning" | "error" | "critical";

/**
 * Event data payload.
 */
export type EventData = Record<string, unknown>;

/**
 * Emit a custom event to colony.
 *
 * Events are collected by the agent and forwarded to colony for aggregation.
 *
 * @param name Event name (e.g., "alert", "metric", "correlation")
 * @param data Event payload as key-value pairs
 * @param severity Event severity level
 *
 * @example
 * ```typescript
 * import { emit } from "@coral/sdk";
 *
 * await emit("alert", {
 *   message: "High latency detected",
 *   service: "payments",
 *   p99_ms: 650,
 *   threshold_ms: 500,
 * }, "warning");
 * ```
 */
export async function emit(
  name: string,
  data: EventData,
  severity: EventSeverity = "info",
): Promise<void> {
  const response = await fetch(`${SDK_URL}/emit`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      name,
      data,
      severity,
    }),
  });

  if (!response.ok) {
    const error = await response.text();
    throw new Error(`Failed to emit event: ${error}`);
  }
}
