/**
 * Database query interface for accessing local DuckDB.
 */

const SDK_URL = Deno.env.get("CORAL_SDK_URL") || "http://localhost:9003";

/**
 * Query result row.
 */
export interface QueryRow {
  [key: string]: unknown;
}

/**
 * Query result.
 */
export interface QueryResult {
  rows: QueryRow[];
  count: number;
}

/**
 * Execute a SQL query against the local agent DuckDB.
 *
 * @param sql SQL query to execute (read-only)
 * @returns Query results
 *
 * @example
 * ```typescript
 * import { db } from "@coral/sdk";
 *
 * const spans = await db.query(`
 *   SELECT trace_id, span_id, duration_ns
 *   FROM otel_spans_local
 *   WHERE service_name = 'payments'
 *     AND duration_ns > 500000000
 *   ORDER BY start_time DESC
 *   LIMIT 100
 * `);
 *
 * console.log(`Found ${spans.count} slow spans`);
 * ```
 */
export async function query(sql: string): Promise<QueryResult> {
  const response = await fetch(`${SDK_URL}/db/query`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ sql }),
  });

  if (!response.ok) {
    const error = await response.text();
    throw new Error(`Query failed: ${error}`);
  }

  return await response.json();
}

/**
 * Database namespace.
 */
export const db = {
  query,
};
