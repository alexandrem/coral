/**
 * Error correlation skill — detect cascading failures via cross-service error
 * spike correlation.
 *
 * Queries error rates for all services over a short window and identifies
 * clusters of services whose error rates spiked simultaneously, which is a
 * pattern consistent with cascading failures.
 *
 * @module
 */

import { listServiceActivity } from "../activity.ts";
import type { SkillFn, SkillResult } from "../types.ts";

/**
 * Parameters for the error correlation skill.
 */
export interface ErrorCorrelationParams {
  /**
   * Error rate threshold (0–1) above which a service is considered spiking.
   * Default: 0.05 (5%).
   */
  threshold?: number;
  /**
   * Lookback window in milliseconds.
   * Default: 300000 (5 minutes).
   */
  window_ms?: number;
}

interface ServiceErrorState {
  service: string;
  errorRate: number;
  errorCount: number;
  requestCount: number;
  spiking: boolean;
}

/**
 * Detect cascading failures by finding services with simultaneous error spikes.
 *
 * @example
 * ```typescript
 * import { errorCorrelation } from "@coral/sdk/skills/error-correlation";
 * const result = await errorCorrelation({ threshold: 0.05 });
 * console.log(JSON.stringify(result));
 * ```
 */
export const errorCorrelation: SkillFn<ErrorCorrelationParams> = async (
  params,
): Promise<SkillResult> => {
  const threshold = params.threshold ?? 0.05;
  const windowMs = params.window_ms ?? 300_000; // 5 minutes default.

  console.error(
    `Checking error rates across all services (threshold: ${(threshold * 100).toFixed(0)}%, window: ${windowMs / 1000}s)...`,
  );

  const activityList = await listServiceActivity(windowMs);

  if (activityList.length === 0) {
    return {
      summary: "No services found.",
      status: "unknown",
      data: { threshold, window_ms: windowMs, spiking: [], healthy: [] },
    };
  }

  const states: ServiceErrorState[] = activityList.map((svc) => ({
    service: svc.serviceName,
    errorRate: svc.errorRate,
    errorCount: svc.errorCount,
    requestCount: svc.requestCount,
    spiking: svc.errorRate >= threshold,
  }));

  const spiking = states.filter((s) => s.spiking);
  const healthy = states.filter((s) => !s.spiking);

  for (const s of states) {
    console.error(
      `  ${s.service}: error_rate=${(s.errorRate * 100).toFixed(2)}% (${s.errorCount}/${s.requestCount}) → ${s.spiking ? "SPIKING" : "ok"}`,
    );
  }

  // Determine if this looks like a cascade: 3+ services spiking simultaneously.
  const isCascade = spiking.length >= 3;
  const overallStatus = spiking.length === 0
    ? "healthy"
    : isCascade
    ? "critical"
    : "warning";

  let summary: string;
  if (spiking.length === 0) {
    summary = `All ${states.length} service(s) have error rates below ${(threshold * 100).toFixed(0)}% threshold.`;
  } else if (isCascade) {
    summary =
      `Cascading failure detected: ${spiking.length} services spiking simultaneously — ${spiking.map((s) => s.service).join(", ")}`;
  } else {
    summary =
      `${spiking.length} service(s) above error threshold: ${spiking.map((s) => s.service).join(", ")}`;
  }

  const recommendations = spiking.length > 0
    ? [
      ...spiking.map(
        (s) =>
          `Investigate ${s.service}: ${(s.errorRate * 100).toFixed(2)}% error rate (${s.errorCount} errors in window)`,
      ),
      isCascade
        ? "Review shared dependencies (database, message queue, auth service) as a likely root cause."
        : "Check service logs and recent deployments for the affected service(s).",
    ]
    : undefined;

  return {
    summary,
    status: overallStatus,
    data: {
      threshold,
      window_ms: windowMs,
      spiking: spiking.map((s) => ({
        service: s.service,
        errorRate: s.errorRate,
        errorCount: s.errorCount,
        requestCount: s.requestCount,
      })),
      healthy: healthy.map((s) => ({
        service: s.service,
        errorRate: s.errorRate,
      })),
    },
    recommendations,
  };
};
