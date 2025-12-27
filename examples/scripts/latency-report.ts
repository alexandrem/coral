#!/usr/bin/env -S coral run

/**
 * Example: Service Latency Report
 *
 * Lists all services and their P99, P95, P50 latencies.
 * Highlights services with latency > 500ms.
 */

import * as coral from "../../pkg/sdk/typescript/mod.ts";

console.log("Service Latency Report\n");
console.log("=".repeat(60));

// Get all services
const services = await coral.services.list();

if (services.length === 0) {
  console.log("No services found");
  Deno.exit(0);
}

console.log(`Found ${services.length} service(s)\n`);

// Query latency percentiles for each service
for (const svc of services) {
  console.log(`\n${svc.name} (${svc.namespace || "default"}):`);
  console.log("-".repeat(40));

  try {
    // Get P50, P95, P99 latencies
    const p50 = await coral.metrics.getP50(
      svc.name,
      "http.server.duration",
    );
    const p95 = await coral.metrics.getP95(
      svc.name,
      "http.server.duration",
    );
    const p99 = await coral.metrics.getP99(
      svc.name,
      "http.server.duration",
    );

    // Convert from nanoseconds to milliseconds
    const p50Ms = p50.value / 1_000_000;
    const p95Ms = p95.value / 1_000_000;
    const p99Ms = p99.value / 1_000_000;

    console.log(`  P50: ${p50Ms.toFixed(2)}ms`);
    console.log(`  P95: ${p95Ms.toFixed(2)}ms`);
    console.log(`  P99: ${p99Ms.toFixed(2)}ms`);

    // Highlight high latency
    if (p99Ms > 500) {
      console.log(`  ⚠️  WARNING: High P99 latency (>${500}ms)`);
    }
  } catch (error) {
    console.log(`  ❌ Error: ${error.message}`);
  }
}

console.log("\n" + "=".repeat(60));
