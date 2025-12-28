/**
 * High Latency Alert Script
 *
 * Monitors a service for high latency and alerts when thresholds are exceeded.
 *
 * This script demonstrates:
 * - Querying metrics using the Coral SDK
 * - Using activity API for error rate calculation
 * - Continuous monitoring with periodic checks
 * - Console-based alerting
 */

import * as coral from "@coral/sdk";

const SERVICE_NAME = "payments";
const LATENCY_THRESHOLD_MS = 500;
const ERROR_RATE_THRESHOLD = 0.01; // 1%
const CHECK_INTERVAL_MS = 30_000; // 30 seconds

/**
 * Check service health metrics and alert if thresholds are exceeded.
 */
async function checkServiceHealth() {
  try {
    // Get P99 latency
    const p99 = await coral.metrics.getP99(
      SERVICE_NAME,
      "http.server.duration",
      5 * 60 * 1000, // Last 5 minutes
    );
    const p99Ms = p99.value / 1_000_000;

    // Get error rate using SDK abstraction
    const errors = await coral.activity.getServiceErrors(
      SERVICE_NAME,
      5 * 60 * 1000, // Last 5 minutes
    );

    const errorRate = errors.errorRate;
    const errorCount = errors.errorCount;

    // Check if thresholds are exceeded
    const latencyExceeded = p99Ms > LATENCY_THRESHOLD_MS;
    const errorRateExceeded = errorRate > ERROR_RATE_THRESHOLD;

    const timestamp = new Date().toISOString();

    if (latencyExceeded && errorRateExceeded) {
      // Critical: Both latency and error rate are high
      console.log(`🚨 CRITICAL ALERT [${timestamp}]`);
      console.log(`   Service: ${SERVICE_NAME}`);
      console.log(`   P99 Latency: ${p99Ms.toFixed(1)}ms (threshold: ${LATENCY_THRESHOLD_MS}ms)`);
      console.log(`   Error Rate: ${(errorRate * 100).toFixed(2)}% (threshold: ${ERROR_RATE_THRESHOLD * 100}%)`);
      console.log(`   Error Count: ${errorCount}`);
    } else if (latencyExceeded) {
      // Warning: High latency only
      console.log(`⚠️  WARNING [${timestamp}]: ${SERVICE_NAME} high latency`);
      console.log(`   P99: ${p99Ms.toFixed(1)}ms (threshold: ${LATENCY_THRESHOLD_MS}ms)`);
      console.log(`   Error Rate: ${(errorRate * 100).toFixed(2)}% (OK)`);
    } else if (errorRateExceeded) {
      // Warning: High error rate only
      console.log(`⚠️  WARNING [${timestamp}]: ${SERVICE_NAME} high error rate`);
      console.log(`   Error Rate: ${(errorRate * 100).toFixed(2)}% (threshold: ${ERROR_RATE_THRESHOLD * 100}%)`);
      console.log(`   Error Count: ${errorCount}`);
      console.log(`   P99 Latency: ${p99Ms.toFixed(1)}ms (OK)`);
    } else {
      // Everything is healthy
      console.log(`✓ OK [${timestamp}]: ${SERVICE_NAME} healthy`);
      console.log(`   P99: ${p99Ms.toFixed(1)}ms, Errors: ${(errorRate * 100).toFixed(2)}%`);
    }
  } catch (error) {
    console.error(`❌ Error checking service health: ${error}`);
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
