/**
 * Raw SQL queries against Colony DuckDB.
 *
 * @module
 */

import { getClient } from "./client.ts";
import type { ClientConfig, QueryResult } from "./types.ts";

/**
 * Execute a raw SQL query against Colony DuckDB.
 *
 * Queries are executed in read-only mode with automatic guardrails.
 *
 * @param sql - SQL query to execute
 * @param maxRows - Maximum rows to return (default: 1000)
 * @param config - Optional client configuration
 * @returns Query results
 *
 * @example
 * ```typescript
 * import * as coral from "@coral/sdk";
 *
 * const result = await coral.db.query(`
 *   SELECT service_name, AVG(p99_duration_ns) as avg_p99
 *   FROM service_summary
 *   WHERE timestamp > now() - INTERVAL '1 hour'
 *   GROUP BY service_name
 *   ORDER BY avg_p99 DESC
 * `);
 *
 * console.log(`Found ${result.rowCount} services`);
 * for (const row of result.rows) {
 *   console.log(`${row.service_name}: ${row.avg_p99} ns`);
 * }
 * ```
 */
export async function query(
  sql: string,
  maxRows: number = 1000,
  config?: ClientConfig,
): Promise<QueryResult> {
  const client = getClient(config);

  interface ExecuteQueryRequest {
    sql: string;
    maxRows: number;
  }

  interface ExecuteQueryResponse {
    columns: string[];
    rows: Array<{ values: string[] }>;
    rowCount: number;
  }

  const request: ExecuteQueryRequest = {
    sql,
    maxRows,
  };

  const response = await client.call<
    ExecuteQueryRequest,
    ExecuteQueryResponse
  >(
    "coral.colony.v1.ColonyService",
    "ExecuteQuery",
    request,
  );

  // Convert rows from array of values to objects
  const rows: Record<string, unknown>[] = response.rows.map((row) => {
    const rowObj: Record<string, unknown> = {};
    response.columns.forEach((col, idx) => {
      rowObj[col] = row.values[idx];
    });
    return rowObj;
  });

  return {
    columns: response.columns,
    rows,
    rowCount: response.rowCount,
  };
}
