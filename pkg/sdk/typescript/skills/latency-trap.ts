/**
 * Latency Trap skill — automatically capture CPU profiles when latency spikes.
 * 
 * This skill deploys a stateful correlation (RFD 091) to an agent. When the
 * specified function's latency exceeds a threshold, the agent will
 * automatically trigger a CPU profile.
 * 
 * @module
 */

import * as correlation from "../correlation.ts";
import { StrategyKind, ActionKind } from "../types.ts";
import type { SkillFn, SkillResult } from "../types.ts";

/**
 * Parameters for the latency trap skill.
 */
export interface LatencyTrapParams {
  /** Service name to monitor. */
  service: string;
  /** Function name to probe (e.g., 'main.ProcessPayment'). */
  function: string;
  /** Latency threshold in nanoseconds (e.g., 500000000 for 500ms). */
  threshold_ns: number;
  /** Capture duration for the CPU profile in milliseconds. Default: 10000 (10s). */
  profile_duration_ms?: number;
}

/**
 * Deploy a latency trap that triggers a CPU profile on slow function calls.
 */
export const latencyTrap: SkillFn<LatencyTrapParams> = async (
  params,
): Promise<SkillResult> => {
  console.error(`Deploying latency trap for ${params.service} on ${params.function}...`);
  console.error(`Threshold: ${params.threshold_ns / 1_000_000}ms`);

  const resp = await correlation.deploy(params.service, {
    strategy: StrategyKind.PERCENTILE_ALARM,
    source: {
      probe: params.function,
    },
    field: "duration_ns",
    threshold: params.threshold_ns,
    percentile: 0.99, // P99 alarm
    window: "30s",
    action: {
      kind: ActionKind.CPU_PROFILE,
      profileDurationMs: params.profile_duration_ms ?? 10_000,
    },
    cooldownMs: 60_000, // Wait 1 minute between profiles
  });

  if (!resp.success) {
    return {
      summary: `Failed to deploy latency trap: ${resp.error}`,
      status: "critical",
      data: { error: resp.error },
    };
  }

  return {
    summary: `Latency trap deployed successfully to agent ${resp.agentId}.`,
    status: "healthy",
    data: {
      correlation_id: resp.correlationId,
      agent_id: resp.agentId,
      service: params.service,
      function: params.function,
      threshold_ns: params.threshold_ns,
    },
    recommendations: [
      "Wait for the trap to fire. Check colony logs for 'Correlation action fired' events.",
      `Once fired, use coral_query_summary for ${params.service} to view the captured hotspots.`,
    ],
  };
};
