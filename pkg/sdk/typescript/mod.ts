/**
 * Coral SDK for TypeScript/Deno Scripts
 *
 * Provides access to Coral observability data from sandboxed TypeScript scripts.
 *
 * @example
 * ```typescript
 * import * as coral from "@coral/sdk";
 *
 * // List services
 * const services = await coral.services.list();
 * console.log(`Found ${services.length} services`);
 *
 * // Get P99 latency
 * const p99 = await coral.metrics.getP99("payments", "http.server.duration");
 * console.log(`P99: ${p99.value / 1_000_000}ms`);
 *
 * // Raw SQL query
 * const result = await coral.db.query(`
 *   SELECT service_name, COUNT(*) as count
 *   FROM ebpf_http_metrics
 *   GROUP BY service_name
 * `);
 * ```
 *
 * @module @coral/sdk
 */

export * as services from "./services.ts";
export * as metrics from "./metrics.ts";
export * as traces from "./traces.ts";
export * as system from "./system.ts";
export * as db from "./db.ts";

export type * from "./types.ts";
