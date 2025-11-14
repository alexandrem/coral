package database

import (
	"fmt"
)

// initSchema creates all required tables and indexes for colony storage.
// Uses CREATE TABLE IF NOT EXISTS for idempotency across restarts.
func (d *Database) initSchema() error {
	// Wrap all DDL statements in a transaction for atomicity.
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Execute all DDL statements.
	for _, ddl := range schemaDDL {
		if _, err := tx.Exec(ddl); err != nil {
			return fmt.Errorf("failed to execute DDL: %w", err)
		}
	}

	// Commit transaction.
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit schema transaction: %w", err)
	}

	return nil
}

// schemaDDL contains all DDL statements for colony database schema.
var schemaDDL = []string{
	// Services table - registry of services in the mesh.
	`CREATE TABLE IF NOT EXISTS services (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		app_id TEXT NOT NULL,
		version TEXT,
		agent_id TEXT NOT NULL,
		labels TEXT,
		last_seen TIMESTAMP NOT NULL,
		status TEXT NOT NULL
	)`,

	`CREATE INDEX IF NOT EXISTS idx_services_agent_id ON services(agent_id)`,
	`CREATE INDEX IF NOT EXISTS idx_services_status ON services(status)`,
	`CREATE INDEX IF NOT EXISTS idx_services_last_seen ON services(last_seen)`,

	// Metric summaries - aggregated metrics (downsampled).
	`CREATE TABLE IF NOT EXISTS metric_summaries (
		timestamp TIMESTAMP NOT NULL,
		service_id TEXT NOT NULL,
		metric_name TEXT NOT NULL,
		interval TEXT NOT NULL,
		p50 DOUBLE,
		p95 DOUBLE,
		p99 DOUBLE,
		mean DOUBLE,
		max DOUBLE,
		count INTEGER,
		PRIMARY KEY (timestamp, service_id, metric_name, interval)
	)`,

	`CREATE INDEX IF NOT EXISTS idx_metric_summaries_service_id ON metric_summaries(service_id)`,
	`CREATE INDEX IF NOT EXISTS idx_metric_summaries_metric_name ON metric_summaries(metric_name)`,

	// Events - important events only (deploy, crash, restart, alert, connection).
	`CREATE TABLE IF NOT EXISTS events (
		id INTEGER PRIMARY KEY,
		timestamp TIMESTAMP NOT NULL,
		service_id TEXT NOT NULL,
		event_type TEXT NOT NULL,
		details TEXT,
		correlation_group TEXT
	)`,

	`CREATE INDEX IF NOT EXISTS idx_events_service_id ON events(service_id)`,
	`CREATE INDEX IF NOT EXISTS idx_events_type ON events(event_type)`,
	`CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp)`,
	`CREATE INDEX IF NOT EXISTS idx_events_correlation ON events(correlation_group)`,

	// Insights - AI-generated insights and recommendations.
	`CREATE TABLE IF NOT EXISTS insights (
		id INTEGER PRIMARY KEY,
		created_at TIMESTAMP NOT NULL,
		insight_type TEXT NOT NULL,
		priority TEXT NOT NULL,
		title TEXT NOT NULL,
		summary TEXT NOT NULL,
		details TEXT,
		affected_services TEXT,
		status TEXT NOT NULL,
		confidence DOUBLE,
		expires_at TIMESTAMP
	)`,

	`CREATE INDEX IF NOT EXISTS idx_insights_status ON insights(status)`,
	`CREATE INDEX IF NOT EXISTS idx_insights_priority ON insights(priority)`,
	`CREATE INDEX IF NOT EXISTS idx_insights_created_at ON insights(created_at)`,

	// Service connections - auto-discovered service topology.
	`CREATE TABLE IF NOT EXISTS service_connections (
		from_service TEXT NOT NULL,
		to_service TEXT NOT NULL,
		protocol TEXT NOT NULL,
		first_observed TIMESTAMP NOT NULL,
		last_observed TIMESTAMP NOT NULL,
		connection_count INTEGER NOT NULL,
		PRIMARY KEY (from_service, to_service, protocol)
	)`,

	`CREATE INDEX IF NOT EXISTS idx_service_connections_from ON service_connections(from_service)`,
	`CREATE INDEX IF NOT EXISTS idx_service_connections_to ON service_connections(to_service)`,

	// Baselines - learned baselines for anomaly detection.
	`CREATE TABLE IF NOT EXISTS baselines (
		service_id TEXT NOT NULL,
		metric_name TEXT NOT NULL,
		time_window TEXT NOT NULL,
		mean DOUBLE,
		stddev DOUBLE,
		p50 DOUBLE,
		p95 DOUBLE,
		p99 DOUBLE,
		sample_count INTEGER,
		last_updated TIMESTAMP NOT NULL,
		PRIMARY KEY (service_id, metric_name, time_window)
	)`,

	`CREATE INDEX IF NOT EXISTS idx_baselines_service_id ON baselines(service_id)`,
	`CREATE INDEX IF NOT EXISTS idx_baselines_metric_name ON baselines(metric_name)`,

	// OpenTelemetry summaries - aggregated telemetry data from queried agents (RFD 025 - pull-based).
	`CREATE TABLE IF NOT EXISTS otel_summaries (
		bucket_time TIMESTAMP NOT NULL,
		agent_id TEXT NOT NULL,
		service_name TEXT NOT NULL,
		span_kind TEXT,
		p50_ms DOUBLE,
		p95_ms DOUBLE,
		p99_ms DOUBLE,
		error_count INTEGER DEFAULT 0,
		total_spans INTEGER DEFAULT 0,
		sample_traces TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (bucket_time, agent_id, service_name, span_kind)
	)`,

	`CREATE INDEX IF NOT EXISTS idx_otel_summaries_lookup ON otel_summaries(agent_id, bucket_time, service_name)`,
	`CREATE INDEX IF NOT EXISTS idx_otel_summaries_service ON otel_summaries(service_name)`,
	`CREATE INDEX IF NOT EXISTS idx_otel_summaries_bucket_time ON otel_summaries(bucket_time)`,
}
