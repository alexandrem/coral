/**
 * Global Cross-Service Correlation Monitor
 *
 * Execution Scope: GLOBAL
 * Script Type: DAEMON
 *
 * This script monitors MULTIPLE services and detects cascading failures.
 * It requires explicit service names and is specific to your architecture.
 *
 * Use case: Detect when errors in "payments" cause errors in "orders"
 *
 * Deploy globally:
 *   coral script deploy cross-service-correlation --scope global
 *
 * This script is NOT portable - it's specific to your service architecture.
 */

import * as coral from "jsr:@coral/sdk";

// Validate scope
if (coral.context.scope !== "global") {
  throw new Error("This script requires ExecutionScope=GLOBAL");
}

// Configuration (specific to your architecture)
const MONITORED_SERVICES = ["payments", "orders", "inventory", "notifications"];
const ERROR_THRESHOLD = 0.01; // 1%
const CHECK_INTERVAL_SEC = 60;

console.log("Starting cross-service correlation monitor");
console.log(`  Monitoring services: ${MONITORED_SERVICES.join(", ")}`);
console.log(`  Error threshold: ${ERROR_THRESHOLD * 100}%`);
console.log(`  Check interval: ${CHECK_INTERVAL_SEC}s`);

interface ServiceHealth {
  service: string;
  errorRate: number;
  p99LatencyMs: number;
  totalRequests: number;
  errorRequests: number;
}

// Monitoring loop
while (true) {
  try {
    const timestamp = new Date();
    const healthChecks: ServiceHealth[] = [];

    // Query all services (Global scope - must specify service names)
    for (const serviceName of MONITORED_SERVICES) {
      try {
        const [errorRateResult, p99Result] = await Promise.all([
          coral.metrics.getErrorRate(serviceName, 5 * 60 * 1000), // Last 5 minutes
          coral.metrics.getPercentile(serviceName, "http.server.duration", 0.99),
        ]);

        const health: ServiceHealth = {
          service: serviceName,
          errorRate: errorRateResult.rate,
          p99LatencyMs: p99Result.value / 1_000_000,
          totalRequests: errorRateResult.totalRequests,
          errorRequests: errorRateResult.errorRequests,
        };

        healthChecks.push(health);

        console.log(
          `[${timestamp.toISOString()}] ${serviceName}: ` +
          `Errors=${(health.errorRate * 100).toFixed(2)}%, ` +
          `P99=${health.p99LatencyMs.toFixed(1)}ms, ` +
          `Requests=${health.totalRequests}`
        );
      } catch (error) {
        console.error(`Failed to check ${serviceName}:`, error);
      }
    }

    // Detect cascading failures
    const unhealthyServices = healthChecks.filter(h => h.errorRate > ERROR_THRESHOLD);

    if (unhealthyServices.length >= 2) {
      // Multiple services failing - likely cascading failure
      console.log(`🚨 CASCADING FAILURE DETECTED: ${unhealthyServices.length} services affected`);

      // Find correlations by checking trace propagation
      const primaryFailure = unhealthyServices.reduce((prev, current) =>
        prev.errorRate > current.errorRate ? prev : current
      );

      // Check if errors in primary service correlate with errors in dependent services
      const correlations: Array<{from: string; to: string; correlation: string}> = [];

      // Example: Check if payments errors cause orders errors
      if (primaryFailure.service === "payments") {
        const ordersHealth = healthChecks.find(h => h.service === "orders");
        if (ordersHealth && ordersHealth.errorRate > ERROR_THRESHOLD) {
          // Query traces to confirm correlation
          const paymentsErrors = await coral.traces.findErrors("payments", 5 * 60 * 1000, 100);

          for (const trace of paymentsErrors.traces) {
            // Check if this trace also has orders spans
            const correlated = await coral.traces.correlate(trace.traceId, ["payments", "orders"]);

            if (correlated.relatedTraces.length > 1) {
              correlations.push({
                from: "payments",
                to: "orders",
                correlation: `Trace ${trace.traceId} spans both services`,
              });
            }
          }
        }
      }

      // Emit critical alert
      await coral.emit(
        "cascading_failure",
        {
          affected_services: unhealthyServices.map(h => h.service),
          primary_failure: primaryFailure.service,
          service_health: Object.fromEntries(
            unhealthyServices.map(h => [
              h.service,
              {
                error_rate_pct: h.errorRate * 100,
                p99_latency_ms: h.p99LatencyMs,
                error_count: h.errorRequests,
              },
            ])
          ),
          correlations: correlations,
          timestamp: timestamp.toISOString(),
        },
        "critical"
      );
    } else if (unhealthyServices.length === 1) {
      // Single service failure
      const service = unhealthyServices[0];

      console.log(`⚠️  Service degradation: ${service.service}`);

      await coral.emit(
        "service_degradation",
        {
          service: service.service,
          error_rate_pct: service.errorRate * 100,
          p99_latency_ms: service.p99LatencyMs,
          error_count: service.errorRequests,
        },
        "warning"
      );
    } else {
      console.log("✅ All services healthy");
    }

    // Additional correlation: Check if system metrics correlate with errors
    const systemMetrics = await coral.system.getMetrics(["cpu", "memory"]);
    const cpuUsage = systemMetrics.metrics["cpu"]?.value || 0;
    const memoryUsage = systemMetrics.metrics["memory"]?.value || 0;

    if (unhealthyServices.length > 0 && (cpuUsage > 80 || memoryUsage > 80)) {
      console.log("🔍 CORRELATION: Service errors + High resource usage");

      await coral.emit(
        "resource_correlation",
        {
          affected_services: unhealthyServices.map(h => h.service),
          cpu_usage_pct: cpuUsage,
          memory_usage_pct: memoryUsage,
          message: "Service errors correlated with resource exhaustion",
        },
        "error"
      );
    }
  } catch (error) {
    console.error("Error in correlation monitor:", error);
  }

  // Wait before next check
  await new Promise((resolve) => setTimeout(resolve, CHECK_INTERVAL_SEC * 1000));
}
