/**
 * Cross-Service Correlation Analysis
 *
 * Analyzes latency correlation across multiple services to identify
 * cascading performance issues.
 *
 * This script demonstrates:
 * - Querying metrics for multiple services using SDK abstractions
 * - Correlating data across services
 * - Advanced data analysis in TypeScript
 * - Pattern detection
 */

import * as coral from "@coral/sdk";

const CHECK_INTERVAL_MS = 60_000; // 1 minute
const LATENCY_THRESHOLD_MULTIPLIER = 2; // Alert when service is 2x average

/**
 * Analyze cross-service latency correlation.
 */
async function analyzeCorrelation() {
  try {
    // Get all services
    const services = await coral.services.list();

    if (services.length === 0) {
      console.log("✓ No services found");
      return;
    }

    console.log(`Analyzing ${services.length} service(s)...\n`);

    // Collect latency data for all services
    const serviceLatencies: Array<{
      name: string;
      p99: number;
      p95: number;
      p50: number;
    }> = [];

    for (const svc of services) {
      try {
        const p99 = await coral.metrics.getP99(
          svc.name,
          "http.server.duration",
          5 * 60 * 1000, // Last 5 minutes
        );
        const p95 = await coral.metrics.getP95(
          svc.name,
          "http.server.duration",
          5 * 60 * 1000,
        );
        const p50 = await coral.metrics.getP50(
          svc.name,
          "http.server.duration",
          5 * 60 * 1000,
        );

        serviceLatencies.push({
          name: svc.name,
          p99: p99.value / 1_000_000, // Convert to ms
          p95: p95.value / 1_000_000,
          p50: p50.value / 1_000_000,
        });
      } catch (error) {
        console.log(`⚠️  Could not get metrics for ${svc.name}: ${error}`);
      }
    }

    if (serviceLatencies.length === 0) {
      console.log("No latency data available");
      return;
    }

    // Calculate average latencies
    const avgP99 =
      serviceLatencies.reduce((sum, s) => sum + s.p99, 0) /
      serviceLatencies.length;
    const avgP95 =
      serviceLatencies.reduce((sum, s) => sum + s.p95, 0) /
      serviceLatencies.length;

    // Find services with high latency (> threshold x average)
    const highLatencyServices = serviceLatencies.filter(
      (s) => s.p99 > avgP99 * LATENCY_THRESHOLD_MULTIPLIER,
    );

    if (highLatencyServices.length > 0) {
      console.log(
        `🔍 CORRELATION DETECTED: ${highLatencyServices.length} service(s) with abnormally high latency\n`,
      );
      console.log(`Average P99: ${avgP99.toFixed(1)}ms`);
      console.log(`Average P95: ${avgP95.toFixed(1)}ms\n`);

      console.log("High latency services:");
      for (const svc of highLatencyServices) {
        console.log(`  ${svc.name}:`);
        console.log(`    P99: ${svc.p99.toFixed(1)}ms (${(svc.p99 / avgP99).toFixed(1)}x average)`);
        console.log(`    P95: ${svc.p95.toFixed(1)}ms`);
        console.log(`    P50: ${svc.p50.toFixed(1)}ms`);
      }

      // Get error details for high-latency services
      console.log("\nError analysis:");
      for (const svc of highLatencyServices) {
        const errors = await coral.activity.getServiceErrors(
          svc.name,
          5 * 60 * 1000, // Last 5 minutes
        );

        console.log(
          `  ${svc.name}: ${errors.errorCount} errors (${(errors.errorRate * 100).toFixed(2)}%)`,
        );
      }
    } else {
      console.log("✓ All services within normal latency range\n");
      console.log(`Average P99: ${avgP99.toFixed(1)}ms`);
      console.log(`Average P95: ${avgP95.toFixed(1)}ms`);

      // Show top 3 slowest services
      const slowest = serviceLatencies
        .sort((a, b) => b.p99 - a.p99)
        .slice(0, 3);

      console.log("\nSlowest services:");
      for (const svc of slowest) {
        console.log(
          `  ${svc.name}: P99=${svc.p99.toFixed(1)}ms, P95=${svc.p95.toFixed(1)}ms, P50=${svc.p50.toFixed(1)}ms`,
        );
      }
    }
  } catch (error) {
    console.error(`Error analyzing correlation: ${error}`);
  }
}

/**
 * Main monitoring loop.
 */
async function main() {
  console.log("Starting cross-service correlation analysis...");
  console.log(`  Latency threshold: ${LATENCY_THRESHOLD_MULTIPLIER}x average`);
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
