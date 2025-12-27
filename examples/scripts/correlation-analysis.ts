/**
 * Correlation Analysis Script
 *
 * Analyzes correlation between errors and system resource usage to identify
 * resource-related issues.
 *
 * This script demonstrates:
 * - Querying traces with filters
 * - Correlating multiple data sources (traces + system metrics)
 * - Advanced data analysis in TypeScript
 * - Emitting correlation events for AI analysis
 */

import * as coral from "jsr:@coral/sdk";

const SERVICE_NAME = "payments";
const HIGH_MEMORY_THRESHOLD_PCT = 80;
const HIGH_CPU_THRESHOLD_PCT = 80;
const CHECK_INTERVAL_MS = 60_000; // 1 minute

/**
 * Analyze correlation between errors and system resources.
 */
async function analyzeCorrelation() {
  try {
    // Get recent error traces
    const errorTraces = await coral.db.query(`
      SELECT
        trace_id,
        span_id,
        service_name,
        duration_ns,
        http_status,
        http_route,
        start_time
      FROM otel_spans_local
      WHERE service_name = '${SERVICE_NAME}'
        AND is_error = true
        AND start_time > now() - INTERVAL '5 minutes'
      ORDER BY start_time DESC
      LIMIT 100
    `);

    if (errorTraces.count === 0) {
      console.log(`✓ No errors detected in ${SERVICE_NAME}`);
      return;
    }

    console.log(`Found ${errorTraces.count} error traces in the last 5 minutes`);

    // Get current system metrics
    const cpu = await coral.system.getCPU();
    const memory = await coral.system.getMemory();
    const memoryUsagePct = (memory.used / memory.total) * 100;

    // Analyze correlation
    const highMemory = memoryUsagePct > HIGH_MEMORY_THRESHOLD_PCT;
    const highCPU = cpu.usage_percent > HIGH_CPU_THRESHOLD_PCT;

    if (highMemory || highCPU) {
      // Correlation detected
      const correlationType = highMemory && highCPU
        ? "error_high_cpu_memory"
        : highMemory
        ? "error_high_memory"
        : "error_high_cpu";

      await coral.emit("correlation", {
        type: correlationType,
        service: SERVICE_NAME,
        error_count: errorTraces.count,
        cpu_usage_pct: cpu.usage_percent,
        memory_usage_pct: memoryUsagePct,
        sample_traces: errorTraces.rows.slice(0, 5).map((row: any) => ({
          trace_id: row.trace_id,
          duration_ms: row.duration_ns / 1_000_000,
          http_status: row.http_status,
          http_route: row.http_route,
        })),
        timestamp: new Date().toISOString(),
      }, "warning");

      console.log(
        `🔍 CORRELATION DETECTED: ${errorTraces.count} errors + High ${highCPU ? "CPU" : ""}${highCPU && highMemory ? " & " : ""}${highMemory ? "Memory" : ""}`,
      );
      console.log(
        `   CPU: ${cpu.usage_percent.toFixed(1)}%, Memory: ${memoryUsagePct.toFixed(1)}%`,
      );
    } else {
      console.log(
        `⚠️  Errors detected but no resource correlation: ${errorTraces.count} errors, CPU=${cpu.usage_percent.toFixed(1)}%, Memory=${memoryUsagePct.toFixed(1)}%`,
      );

      // Analyze error patterns
      const errorsByRoute = new Map<string, number>();
      for (const row of errorTraces.rows) {
        const route = (row as any).http_route || "unknown";
        errorsByRoute.set(route, (errorsByRoute.get(route) || 0) + 1);
      }

      const topErrorRoutes = Array.from(errorsByRoute.entries())
        .sort((a, b) => b[1] - a[1])
        .slice(0, 5);

      console.log("   Top error routes:");
      for (const [route, count] of topErrorRoutes) {
        console.log(`     - ${route}: ${count} errors`);
      }

      await coral.emit("error_pattern", {
        service: SERVICE_NAME,
        error_count: errorTraces.count,
        top_error_routes: Object.fromEntries(topErrorRoutes),
        timestamp: new Date().toISOString(),
      }, "info");
    }
  } catch (error) {
    console.error(`Error analyzing correlation: ${error}`);
  }
}

/**
 * Main monitoring loop.
 */
async function main() {
  console.log(`Starting correlation analysis for ${SERVICE_NAME}...`);
  console.log(`  High memory threshold: ${HIGH_MEMORY_THRESHOLD_PCT}%`);
  console.log(`  High CPU threshold: ${HIGH_CPU_THRESHOLD_PCT}%`);
  console.log(`  Check interval: ${CHECK_INTERVAL_MS / 1000}s`);

  // Run initial analysis
  await analyzeCorrelation();

  // Schedule periodic analysis
  while (true) {
    await new Promise((resolve) => setTimeout(resolve, CHECK_INTERVAL_MS));
    await analyzeCorrelation();
  }
}

// Run the script
main().catch((error) => {
  console.error(`Fatal error: ${error}`);
  Deno.exit(1);
});
