/**
 * Latency report skill — check P99 latency and error rates across services.
 *
 * Queries P99 latency and error rates for all services (or a single service)
 * and flags those above a configurable threshold.
 *
 * @module
 */

import { listServiceActivity } from "../activity.ts";
import { getP99 } from "../metrics.ts";
import type { SkillFn, SkillResult } from "../types.ts";

/**
 * Parameters for the latency report skill.
 */
export interface LatencyReportParams {
  /**
   * P99 latency threshold in milliseconds. Services above this are flagged.
   * Default: 500ms.
   */
  threshold_ms?: number;
  /**
   * Optional: limit the report to a single service name.
   * When omitted, all known services are checked.
   */
  service?: string;
}

interface ServiceLatency {
  service: string;
  p99Ms: number | null;
  errorRate: number | null;
  status: "healthy" | "warning" | "critical" | "unknown";
}

/**
 * Run a latency report across all services.
 *
 * Reports P99 HTTP latency and error rates, flagging services above
 * the configured threshold.
 *
 * @example
 * ```typescript
 * import { latencyReport } from "@coral/sdk/skills/latency-report";
 * const result = await latencyReport({ threshold_ms: 500 });
 * console.log(JSON.stringify(result));
 * ```
 */
export const latencyReport: SkillFn<LatencyReportParams> = async (
  params,
): Promise<SkillResult> => {
  const thresholdMs = params.threshold_ms ?? 500;

  // Gather activity data (includes error rates) for all services.
  const activityList = await listServiceActivity();
  const filtered = params.service
    ? activityList.filter((a) => a.serviceName === params.service)
    : activityList;

  if (filtered.length === 0) {
    return {
      summary: "No services found.",
      status: "unknown",
      data: { threshold_ms: thresholdMs, services: [] },
    };
  }

  console.error(`Checking ${filtered.length} service(s) (P99 threshold: ${thresholdMs}ms)...`);

  const results: ServiceLatency[] = [];

  for (const svc of filtered) {
    try {
      const p99 = await getP99(svc.serviceName, "http.server.duration");
      const p99Ms = p99.value / 1_000_000;
      const aboveLatency = p99Ms > thresholdMs;
      const aboveErrorRate = svc.errorRate > 0.05; // >5% error rate is warning.
      const status: ServiceLatency["status"] = aboveLatency
        ? "critical"
        : aboveErrorRate
        ? "warning"
        : "healthy";
      results.push({
        service: svc.serviceName,
        p99Ms,
        errorRate: svc.errorRate,
        status,
      });
      console.error(
        `  ${svc.serviceName}: p99=${p99Ms.toFixed(1)}ms err=${(svc.errorRate * 100).toFixed(2)}% → ${status}`,
      );
    } catch {
      results.push({
        service: svc.serviceName,
        p99Ms: null,
        errorRate: svc.errorRate,
        status: "unknown",
      });
      console.error(`  ${svc.serviceName}: latency unavailable`);
    }
  }

  const critical = results.filter((r) => r.status === "critical");
  const warning = results.filter((r) => r.status === "warning");
  const overallStatus = critical.length > 0
    ? "critical"
    : warning.length > 0
    ? "warning"
    : "healthy";

  const flagged = [...critical, ...warning];
  const summary = flagged.length > 0
    ? `${flagged.length} service(s) above threshold: ${flagged.map((s) => s.service).join(", ")}`
    : `All ${results.length} service(s) within P99 latency threshold (${thresholdMs}ms)`;

  const recommendations = flagged.length > 0
    ? flagged.map(
      (s) =>
        `Investigate ${s.service}: P99=${s.p99Ms?.toFixed(1) ?? "n/a"}ms, error_rate=${((s.errorRate ?? 0) * 100).toFixed(2)}%`,
    )
    : undefined;

  return {
    summary,
    status: overallStatus,
    data: {
      threshold_ms: thresholdMs,
      services: results,
    },
    recommendations,
  };
};
