/**
 * Service-Scoped High Latency Monitor
 *
 * Execution Scope: SERVICE
 * Script Type: DAEMON
 *
 * This script is PORTABLE and can be attached to ANY service.
 * The service context is dependency-injected, making it service-agnostic.
 *
 * Deploy to multiple services:
 *   coral script deploy service-scoped-latency --service payments --params threshold_ms=500
 *   coral script deploy service-scoped-latency --service orders --params threshold_ms=300
 *   coral script deploy service-scoped-latency --service users --params threshold_ms=1000
 *
 * The SAME script works for all services!
 */

import * as coral from "jsr:@coral/sdk";

// Get parameters (injected by executor)
const thresholdMs = parseInt(coral.context.params.get("threshold_ms") || "500");
const checkIntervalSec = parseInt(coral.context.params.get("check_interval_sec") || "30");

// Validate scope
if (coral.context.scope !== "service") {
  throw new Error("This script requires ExecutionScope=SERVICE");
}

// Service context is auto-injected!
const service = coral.context.service!;

console.log(`Starting latency monitor for service: ${service.name}`);
console.log(`  Threshold: ${thresholdMs}ms`);
console.log(`  Check interval: ${checkIntervalSec}s`);
console.log(`  Namespace: ${service.namespace}`);
console.log(`  Region: ${service.region}`);
console.log(`  Version: ${service.version}`);

// Monitoring loop
while (true) {
  try {
    // Auto-scoped to attached service - no need to specify service name!
    const p99Result = await service.getPercentile("http.server.duration", 0.99);
    const errorRateResult = await service.getErrorRate();

    const p99Ms = p99Result.value / 1_000_000;
    const errorRatePct = errorRateResult.rate * 100;

    console.log(`[${new Date().toISOString()}] ${service.name}: P99=${p99Ms.toFixed(1)}ms, Errors=${errorRatePct.toFixed(2)}%`);

    // Check threshold
    if (p99Ms > thresholdMs) {
      await coral.emit(
        "high_latency",
        {
          service: service.name, // Injected context
          namespace: service.namespace,
          region: service.region,
          p99_ms: p99Ms,
          threshold_ms: thresholdMs,
          error_rate_pct: errorRatePct,
        },
        "warning"
      );

      console.log(`⚠️  WARNING: ${service.name} P99 latency (${p99Ms.toFixed(1)}ms) exceeds threshold (${thresholdMs}ms)`);
    }

    // Also check for errors
    if (errorRateResult.rate > 0.01) {
      // Find recent error traces
      const errors = await service.findErrors(5 * 60 * 1000, 10); // Last 5 minutes, max 10 traces

      await coral.emit(
        "high_error_rate",
        {
          service: service.name,
          error_rate_pct: errorRatePct,
          error_count: errorRateResult.errorRequests,
          total_requests: errorRateResult.totalRequests,
          sample_trace_ids: errors.traces.slice(0, 3).map(t => t.traceId),
        },
        "error"
      );

      console.log(`🚨 ERROR: ${service.name} error rate (${errorRatePct.toFixed(2)}%) above threshold (1%)`);
    }
  } catch (error) {
    console.error(`Error monitoring ${service.name}:`, error);
  }

  // Wait before next check
  await new Promise((resolve) => setTimeout(resolve, checkIntervalSec * 1000));
}
