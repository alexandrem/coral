#!/usr/bin/env -S coral run

/**
 * Example: Service Activity Report
 *
 * Uses raw SQL to query service metrics from Colony DuckDB.
 * Shows request counts and error rates for all services.
 */

import * as coral from "@coral/sdk";

console.log("Service Activity Report\n");
console.log("=".repeat(60));

// Query service activity using raw SQL
const result = await coral.db.query(`
  SELECT
    service_name,
    COUNT(*) as request_count,
    SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END) as error_count
  FROM ebpf_http_metrics
  WHERE timestamp > now() - INTERVAL '1 hour'
  GROUP BY service_name
  ORDER BY request_count DESC
`);

if (result.rowCount === 0) {
  console.log("No service activity in the last hour");
  Deno.exit(0);
}

console.log(`\nFound ${result.rowCount} active service(s)\n`);

// Display results
for (const row of result.rows) {
  const serviceName = row.service_name;
  const requestCount = Number(row.request_count);
  const errorCount = Number(row.error_count);
  const errorRate = (errorCount / requestCount) * 100;

  console.log(`${serviceName}:`);
  console.log(`  Requests: ${requestCount}`);
  console.log(`  Errors: ${errorCount} (${errorRate.toFixed(2)}%)`);

  if (errorRate > 1) {
    console.log(`  ⚠️  WARNING: High error rate (>${1}%)`);
  }

  console.log();
}

console.log("=".repeat(60));
