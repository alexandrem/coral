#!/usr/bin/env -S coral run

/**
 * SDK Demo Service Monitor
 *
 * Monitors the "demo" service from examples/sdk-go docker-compose setup.
 * This script demonstrates monitoring a Go application that uses the Coral SDK.
 *
 * Prerequisites:
 * 1. Start the sdk-go example: cd examples/sdk-go && docker-compose up
 * 2. Ensure you're connected to the colony
 * 3. Run this script: coral run examples/scripts/sdk-demo-monitor.ts
 *
 * The demo service simulates a payment processing application with:
 * - ProcessPayment() - called every 2 seconds
 * - ValidateCard() - called every 2 seconds
 * - CalculateTotal() - called every 2 seconds
 */

import * as coral from "@coral/sdk";

const SERVICE_NAME = "demo";
const REFRESH_INTERVAL_MS = 5_000; // 5 seconds

/**
 * Monitor the demo service metrics.
 */
async function monitorDemoService() {
  console.clear();
  console.log("Coral SDK Demo Service Monitor");
  console.log("=".repeat(60));
  console.log(`Service: ${SERVICE_NAME}`);
  console.log(`Refresh: ${REFRESH_INTERVAL_MS / 1000}s\n`);

  try {
    // Check if service exists
    const services = await coral.services.list();
    const demoService = services.find((s) => s.name === SERVICE_NAME);

    if (!demoService) {
      console.log(`⚠️  Service "${SERVICE_NAME}" not found`);
      console.log("\nAvailable services:");
      if (services.length === 0) {
        console.log("  (none)");
      } else {
        services.forEach((s) => console.log(`  - ${s.name}`));
      }
      console.log("\nMake sure the sdk-go example is running:");
      console.log("  cd examples/sdk-go && docker-compose up");
      return;
    }

    console.log("✓ Service found\n");

    // Get activity metrics
    console.log("Activity (last 5 minutes):");
    console.log("-".repeat(40));

    const activity = await coral.activity.getServiceActivity(
      SERVICE_NAME,
      5 * 60 * 1000,
    );

    console.log(`  Total Requests: ${activity.requestCount}`);
    console.log(`  Errors: ${activity.errorCount} (${(activity.errorRate * 100).toFixed(2)}%)`);

    // Get latency percentiles
    console.log("\nLatency Metrics:");
    console.log("-".repeat(40));

    try {
      const p50 = await coral.metrics.getP50(
        SERVICE_NAME,
        "http.server.duration",
        5 * 60 * 1000,
      );
      const p95 = await coral.metrics.getP95(
        SERVICE_NAME,
        "http.server.duration",
        5 * 60 * 1000,
      );
      const p99 = await coral.metrics.getP99(
        SERVICE_NAME,
        "http.server.duration",
        5 * 60 * 1000,
      );

      const p50Ms = p50.value / 1_000_000;
      const p95Ms = p95.value / 1_000_000;
      const p99Ms = p99.value / 1_000_000;

      console.log(`  P50: ${p50Ms.toFixed(2)}ms`);
      console.log(`  P95: ${p95Ms.toFixed(2)}ms`);
      console.log(`  P99: ${p99Ms.toFixed(2)}ms`);

      if (p99Ms > 100) {
        console.log(`\n  ⚠️  High latency detected (P99 > 100ms)`);
      }
    } catch (error) {
      console.log(`  No latency data available yet`);
      console.log(`  (metrics may take a minute to appear after startup)`);
    }

    // Show service info
    console.log("\nService Info:");
    console.log("-".repeat(40));
    console.log(`  Name: ${demoService.name}`);
    if (demoService.namespace) {
      console.log(`  Namespace: ${demoService.namespace}`);
    }

    console.log("\n" + "=".repeat(60));
    console.log(`Last updated: ${new Date().toLocaleTimeString()}`);
  } catch (error) {
    console.error(`\n❌ Error: ${error.message}`);
    console.log("\nTroubleshooting:");
    console.log("  1. Ensure sdk-go example is running");
    console.log("  2. Check that coral agent is connected");
    console.log("  3. Wait ~1 minute for metrics to populate");
  }
}

/**
 * Main monitoring loop.
 */
async function main() {
  // Run initial check
  await monitorDemoService();

  // Schedule periodic updates
  setInterval(monitorDemoService, REFRESH_INTERVAL_MS);
}

// Run the monitor
main().catch((error) => {
  console.error(`Fatal error: ${error}`);
  Deno.exit(1);
});
