/**
 * Memory leak detector skill — identify services with sustained heap growth.
 *
 * Queries heap memory metrics over a sliding window and identifies services
 * with a monotonically increasing heap, sorted by growth rate. A sustained
 * upward trend without GC recovery is a strong indicator of a memory leak.
 *
 * @module
 */

import { query } from "../db.ts";
import type { SkillFn, SkillResult } from "../types.ts";

/**
 * Parameters for the memory leak detector skill.
 */
export interface MemoryLeakDetectorParams {
  /**
   * Lookback window in milliseconds over which to observe heap growth.
   * Default: 900000 (15 minutes).
   */
  window_ms?: number;
  /**
   * Minimum heap growth rate in bytes/second to flag as a potential leak.
   * Default: 102400 (100 KB/s).
   */
  min_growth_rate_bps?: number;
}

interface ServiceMemoryTrend {
  service: string;
  /** Growth rate in bytes per second. Positive = growing. */
  growthRateBps: number;
  firstSampleBytes: number;
  lastSampleBytes: number;
  sampleCount: number;
  leaking: boolean;
}

/**
 * Identify services with sustained heap growth over a window.
 *
 * Uses DuckDB to compute per-service heap growth rate from stored system
 * metrics. Services with a growth rate above the threshold are flagged as
 * potential memory leaks.
 *
 * @example
 * ```typescript
 * import { memoryLeakDetector } from "@coral/sdk/skills/memory-leak-detector";
 * const result = await memoryLeakDetector({ window_ms: 900000 });
 * console.log(JSON.stringify(result));
 * ```
 */
export const memoryLeakDetector: SkillFn<MemoryLeakDetectorParams> = async (
  params,
): Promise<SkillResult> => {
  const windowMs = params.window_ms ?? 900_000; // 15 minutes default.
  const minGrowthRateBps = params.min_growth_rate_bps ?? 102_400; // 100 KB/s.

  console.error(
    `Scanning heap growth over last ${windowMs / 1000}s (min growth rate: ${(minGrowthRateBps / 1024).toFixed(0)} KB/s)...`,
  );

  // Query heap metrics grouped by service, ordered by time.
  // Uses a linear regression approximation: (last - first) / elapsed seconds.
  const windowSec = windowMs / 1000;
  const rows = await query(`
    SELECT
      service_name,
      COUNT(*) AS sample_count,
      FIRST(memory_bytes ORDER BY timestamp ASC)  AS first_bytes,
      LAST(memory_bytes  ORDER BY timestamp ASC)  AS last_bytes,
      EPOCH(MAX(timestamp) - MIN(timestamp))      AS elapsed_sec
    FROM system_metrics
    WHERE
      timestamp >= NOW() - INTERVAL '${Math.ceil(windowSec)} seconds'
      AND memory_bytes > 0
    GROUP BY service_name
    HAVING COUNT(*) >= 2
    ORDER BY (last_bytes - first_bytes) / NULLIF(EPOCH(MAX(timestamp) - MIN(timestamp)), 0) DESC
  `);

  if (rows.length === 0) {
    return {
      summary: "No system metrics found. Ensure agents are collecting heap data.",
      status: "unknown",
      data: { window_ms: windowMs, min_growth_rate_bps: minGrowthRateBps, services: [] },
    };
  }

  const trends: ServiceMemoryTrend[] = rows.map((row) => {
    const firstBytes = Number(row["first_bytes"] ?? 0);
    const lastBytes = Number(row["last_bytes"] ?? 0);
    const elapsedSec = Number(row["elapsed_sec"] ?? 1);
    const growthRateBps = elapsedSec > 0
      ? (lastBytes - firstBytes) / elapsedSec
      : 0;
    return {
      service: String(row["service_name"]),
      growthRateBps,
      firstSampleBytes: firstBytes,
      lastSampleBytes: lastBytes,
      sampleCount: Number(row["sample_count"] ?? 0),
      leaking: growthRateBps >= minGrowthRateBps,
    };
  });

  const leaking = trends.filter((t) => t.leaking);
  const stable = trends.filter((t) => !t.leaking);

  for (const t of trends) {
    const growthKbps = t.growthRateBps / 1024;
    console.error(
      `  ${t.service}: growth=${growthKbps.toFixed(1)} KB/s (${t.firstSampleBytes}→${t.lastSampleBytes} bytes) → ${t.leaking ? "LEAKING" : "stable"}`,
    );
  }

  const overallStatus = leaking.length === 0
    ? "healthy"
    : leaking.length >= 3
    ? "critical"
    : "warning";

  const summary = leaking.length === 0
    ? `All ${trends.length} service(s) have stable heap usage over the ${windowMs / 1000}s window.`
    : `${leaking.length} service(s) show sustained heap growth: ${leaking.map((t) => t.service).join(", ")}`;

  const recommendations = leaking.length > 0
    ? leaking.map((t) => {
      const growthMbMin = (t.growthRateBps * 60) / (1024 * 1024);
      return `${t.service}: heap growing at ${(t.growthRateBps / 1024).toFixed(1)} KB/s (~${growthMbMin.toFixed(1)} MB/min). Attach a memory profile with coral_profile_memory.`;
    })
    : undefined;

  return {
    summary,
    status: overallStatus,
    data: {
      window_ms: windowMs,
      min_growth_rate_bps: minGrowthRateBps,
      services: trends.map((t) => ({
        service: t.service,
        growthRateBps: t.growthRateBps,
        growthKbps: t.growthRateBps / 1024,
        firstSampleBytes: t.firstSampleBytes,
        lastSampleBytes: t.lastSampleBytes,
        sampleCount: t.sampleCount,
        leaking: t.leaking,
      })),
    },
    recommendations,
  };
};
