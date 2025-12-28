#!/usr/bin/env -S coral run

/**
 * Example: Service Activity Report
 *
 * Uses the Coral SDK activity module to get service metrics.
 * Shows request counts and error rates for all services.
 */

import * as coral from "@coral/sdk";

console.log("Service Activity Report\n");
console.log("=".repeat(60));

// Get service activity using SDK abstraction
const services = await coral.activity.listServiceActivity();

if (services.length === 0) {
  console.log("No service activity in the last hour");
  Deno.exit(0);
}

console.log(`\nFound ${services.length} active service(s)\n`);

// Display results
for (const svc of services) {
  const errorRatePercent = svc.errorRate * 100;

  console.log(`${svc.serviceName}:`);
  console.log(`  Requests: ${svc.requestCount}`);
  console.log(`  Errors: ${svc.errorCount} (${errorRatePercent.toFixed(2)}%)`);

  if (errorRatePercent > 1) {
    console.log(`  ⚠️  WARNING: High error rate (>1%)`);
  }

  console.log();
}

console.log("=".repeat(60));
