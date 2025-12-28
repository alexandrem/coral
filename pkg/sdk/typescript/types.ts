/**
 * Common types for the Coral SDK.
 *
 * @module
 */

/**
 * Service represents a discovered service in the mesh.
 */
export interface Service {
  name: string;
  namespace: string;
  instanceCount: number;
  lastSeen?: Date;
}

/**
 * MetricValue represents a metric measurement.
 */
export interface MetricValue {
  value: number;
  unit: string;
  timestamp?: Date;
}

/**
 * Trace represents a distributed trace.
 */
export interface Trace {
  traceId: string;
  durationNs: number;
  timestamp: Date;
  service: string;
}

/**
 * SystemMetrics represents system-level metrics for a service.
 */
export interface SystemMetrics {
  cpuPercent: number;
  memoryPercent: number;
  memoryBytes: number;
  timestamp: Date;
}

/**
 * QueryResult represents the result of a raw SQL query.
 */
export interface QueryResult {
  columns: string[];
  rows: Record<string, unknown>[];
  rowCount: number;
}

/**
 * Client configuration options.
 */
export interface ClientConfig {
  /**
   * Colony gRPC address (e.g., "localhost:9090").
   * Defaults to CORAL_COLONY_ADDR environment variable or "localhost:9090".
   */
  colonyAddr?: string;

  /**
   * Request timeout in milliseconds.
   * Default: 30000 (30 seconds).
   */
  timeout?: number;
}
