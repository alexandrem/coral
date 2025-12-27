/**
 * High Latency Alert Script
 *
 * Monitors a service for high latency and emits alerts when thresholds are exceeded.
 *
 * This script demonstrates:
 * - Querying metrics using the Coral SDK
 * - Combining multiple data sources for correlation
 * - Emitting custom alert events
 * - Continuous monitoring with periodic checks
 */

import * as coral from "jsr:@coral/sdk";

const SERVICE_NAME = "payments";
const LATENCY_THRESHOLD_MS = 500;
const ERROR_RATE_THRESHOLD = 0.01; // 1%
const CHECK_INTERVAL_MS = 30_000; // 30 seconds

/**
 * Check service health metrics and emit alerts if thresholds are exceeded.
 */
async function checkServiceHealth() {
  try {
    // Get P99 latency
    const p99Ns = await coral.metrics.getPercentile(
      SERVICE_NAME,
      "http.server.duration",
      0.99,
    );
    const p99Ms = p99Ns / 1_000_000;

    // Get error rate over last 5 minutes
    const errorRate = await coral.metrics.getErrorRate(SERVICE_NAME, "5m");

    // Get system metrics for correlation
    const cpu = await coral.system.getCPU();
    const memory = await coral.system.getMemory();
    const memoryUsagePct = (memory.used / memory.total) * 100;

    // Check if thresholds are exceeded
    const latencyExceeded = p99Ms > LATENCY_THRESHOLD_MS;
    const errorRateExceeded = errorRate > ERROR_RATE_THRESHOLD;

    if (latencyExceeded && errorRateExceeded) {
      // Critical: Both latency and error rate are high
      await coral.emit("alert", {
        message: `${SERVICE_NAME} service degraded: high latency AND high error rate`,
        service: SERVICE_NAME,
        severity: "critical",
        p99_latency_ms: p99Ms,
        latency_threshold_ms: LATENCY_THRESHOLD_MS,
        error_rate_pct: errorRate * 100,
        error_rate_threshold_pct: ERROR_RATE_THRESHOLD * 100,
        cpu_usage_pct: cpu.usage_percent,
        memory_usage_pct: memoryUsagePct,
        timestamp: new Date().toISOString(),
      }, "critical");

      console.log(
        `🚨 CRITICAL ALERT: ${SERVICE_NAME} degraded - P99=${p99Ms.toFixed(1)}ms, Errors=${(errorRate * 100).toFixed(2)}%`,
      );
    } else if (latencyExceeded) {
      // Warning: High latency only
      await coral.emit("alert", {
        message: `${SERVICE_NAME} service experiencing high latency`,
        service: SERVICE_NAME,
        severity: "warning",
        p99_latency_ms: p99Ms,
        latency_threshold_ms: LATENCY_THRESHOLD_MS,
        error_rate_pct: errorRate * 100,
        cpu_usage_pct: cpu.usage_percent,
        memory_usage_pct: memoryUsagePct,
        timestamp: new Date().toISOString(),
      }, "warning");

      console.log(
        `⚠️  WARNING: ${SERVICE_NAME} high latency - P99=${p99Ms.toFixed(1)}ms`,
      );
    } else if (errorRateExceeded) {
      // Warning: High error rate only
      await coral.emit("alert", {
        message: `${SERVICE_NAME} service experiencing high error rate`,
        service: SERVICE_NAME,
        severity: "warning",
        p99_latency_ms: p99Ms,
        error_rate_pct: errorRate * 100,
        error_rate_threshold_pct: ERROR_RATE_THRESHOLD * 100,
        cpu_usage_pct: cpu.usage_percent,
        memory_usage_pct: memoryUsagePct,
        timestamp: new Date().toISOString(),
      }, "warning");

      console.log(
        `⚠️  WARNING: ${SERVICE_NAME} high error rate - Errors=${(errorRate * 100).toFixed(2)}%`,
      );
    } else {
      // Everything is healthy
      console.log(
        `✓ OK: ${SERVICE_NAME} healthy - P99=${p99Ms.toFixed(1)}ms, Errors=${(errorRate * 100).toFixed(2)}%, CPU=${cpu.usage_percent.toFixed(1)}%, Memory=${memoryUsagePct.toFixed(1)}%`,
      );
    }
  } catch (error) {
    console.error(`Error checking service health: ${error}`);

    await coral.emit("alert", {
      message: `Failed to check ${SERVICE_NAME} service health`,
      service: SERVICE_NAME,
      severity: "error",
      error: String(error),
      timestamp: new Date().toISOString(),
    }, "error");
  }
}

/**
 * Main monitoring loop.
 */
async function main() {
  console.log(`Starting high latency monitoring for ${SERVICE_NAME}...`);
  console.log(`  Latency threshold: ${LATENCY_THRESHOLD_MS}ms`);
  console.log(`  Error rate threshold: ${ERROR_RATE_THRESHOLD * 100}%`);
  console.log(`  Check interval: ${CHECK_INTERVAL_MS / 1000}s`);

  // Run initial check
  await checkServiceHealth();

  // Schedule periodic checks
  while (true) {
    await new Promise((resolve) => setTimeout(resolve, CHECK_INTERVAL_MS));
    await checkServiceHealth();
  }
}

// Run the script
main().catch((error) => {
  console.error(`Fatal error: ${error}`);
  Deno.exit(1);
});
