package database

import (
	"fmt"
)

// initSchema creates all required tables and indexes for colony storage.
// Uses CREATE TABLE IF NOT EXISTS for idempotency across restarts.
// Assumes VSS extension is already loaded if vector search is needed.
func (d *Database) initSchema() error {
	// Wrap all DDL statements in a transaction for atomicity.
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // TODO: errcheck

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

	// Create HNSW index (requires VSS extension to be loaded first).
	// This is done outside the main transaction.
	// Wrap in defer/recover to catch any segfaults from VSS extension.
	func() {
		defer func() {
			if r := recover(); r != nil {
				d.logger.Warn().Interface("panic", r).Msg("HNSW index creation panicked, continuing without index")
			}
		}()
		if err := d.createHNSWIndex(); err != nil {
			d.logger.Warn().Err(err).Msg("Failed to create HNSW index, vector search may be slower")
		}
	}()

	return nil
}

// ensureVSSExtension attempts to install and load the VSS extension.
// Returns an error if installation fails, but this should not block database initialization.
func (d *Database) ensureVSSExtension() error {
	// Try to install the extension (will be skipped if already installed).
	if _, err := d.db.Exec("INSTALL vss FROM core"); err != nil {
		return fmt.Errorf("failed to install VSS extension: %w", err)
	}

	// Try to load the extension.
	if _, err := d.db.Exec("LOAD vss"); err != nil {
		return fmt.Errorf("failed to load VSS extension: %w", err)
	}

	return nil
}

// createHNSWIndex creates the HNSW index for vector similarity search.
// This requires the VSS extension to be loaded.
func (d *Database) createHNSWIndex() error {
	// Check if we already have the index to avoid recreating it.
	var indexExists bool
	err := d.db.QueryRow(`
		SELECT COUNT(*) > 0
		FROM duckdb_indexes()
		WHERE index_name = 'idx_functions_embedding'
	`).Scan(&indexExists)

	if err != nil {
		d.logger.Debug().Err(err).Msg("Could not check if index exists, will attempt creation")
	} else if indexExists {
		d.logger.Debug().Msg("HNSW index already exists, skipping creation")
		return nil
	}

	query := `CREATE INDEX IF NOT EXISTS idx_functions_embedding ON functions
		USING HNSW (embedding)
		WITH (metric = 'cosine')`

	if _, err := d.db.Exec(query); err != nil {
		return fmt.Errorf("failed to create HNSW index: %w", err)
	}

	d.logger.Info().Msg("Successfully created HNSW index for vector search")
	return nil
}

// schemaDDL contains all DDL statements for colony database schema.
// Note: VSS extension installation is handled separately in ensureVSSExtension().
var schemaDDL = []string{
	// Services table - registry of services in the mesh.
	// Note: last_seen moved to service_heartbeats table for better performance.
	// Note: Indexes removed due to DuckDB bug with UPDATE statements on indexed columns in transactions.
	`CREATE TABLE IF NOT EXISTS services (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		app_id TEXT NOT NULL,
		version TEXT,
		agent_id TEXT NOT NULL,
		labels TEXT,
		status TEXT NOT NULL,
		registered_at TIMESTAMP NOT NULL
	)`,

	// Service heartbeats - frequent updates separated for performance.
	// Note: No foreign key constraint or indexes to avoid DuckDB update conflicts.
	`CREATE TABLE IF NOT EXISTS service_heartbeats (
		service_id TEXT PRIMARY KEY,
		last_seen TIMESTAMP NOT NULL
	)`,

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

	// Beyla HTTP metrics - RED metrics from Beyla (RFD 032).
	`CREATE TABLE IF NOT EXISTS beyla_http_metrics (
		timestamp TIMESTAMPTZ NOT NULL,
		agent_id TEXT NOT NULL,
		service_name TEXT NOT NULL,
		http_method VARCHAR(10),
		http_route VARCHAR(255),
		http_status_code SMALLINT,
		latency_bucket_ms DOUBLE NOT NULL,
		count BIGINT NOT NULL,
		attributes TEXT,
		PRIMARY KEY (timestamp, agent_id, service_name, http_method, http_route, http_status_code, latency_bucket_ms)
	)`,

	`CREATE INDEX IF NOT EXISTS idx_beyla_http_service_time ON beyla_http_metrics(service_name, timestamp DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_beyla_http_route ON beyla_http_metrics(http_route, timestamp DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_beyla_http_agent ON beyla_http_metrics(agent_id, timestamp DESC)`,

	// Beyla gRPC metrics - gRPC method-level RED metrics (RFD 032).
	`CREATE TABLE IF NOT EXISTS beyla_grpc_metrics (
		timestamp TIMESTAMPTZ NOT NULL,
		agent_id TEXT NOT NULL,
		service_name TEXT NOT NULL,
		grpc_method VARCHAR(255),
		grpc_status_code SMALLINT,
		latency_bucket_ms DOUBLE NOT NULL,
		count BIGINT NOT NULL,
		attributes TEXT,
		PRIMARY KEY (timestamp, agent_id, service_name, grpc_method, grpc_status_code, latency_bucket_ms)
	)`,

	`CREATE INDEX IF NOT EXISTS idx_beyla_grpc_service_time ON beyla_grpc_metrics(service_name, timestamp DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_beyla_grpc_method ON beyla_grpc_metrics(grpc_method, timestamp DESC)`,

	// Beyla SQL metrics - database query performance (RFD 032).
	`CREATE TABLE IF NOT EXISTS beyla_sql_metrics (
		timestamp TIMESTAMPTZ NOT NULL,
		agent_id TEXT NOT NULL,
		service_name TEXT NOT NULL,
		sql_operation VARCHAR(50),
		table_name VARCHAR(255),
		latency_bucket_ms DOUBLE NOT NULL,
		count BIGINT NOT NULL,
		attributes TEXT,
		PRIMARY KEY (timestamp, agent_id, service_name, sql_operation, table_name, latency_bucket_ms)
	)`,

	`CREATE INDEX IF NOT EXISTS idx_beyla_sql_service_time ON beyla_sql_metrics(service_name, timestamp DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_beyla_sql_operation ON beyla_sql_metrics(sql_operation, timestamp DESC)`,

	// Beyla traces - distributed trace spans (RFD 036).
	`CREATE TABLE IF NOT EXISTS beyla_traces (
		trace_id VARCHAR(32) NOT NULL,
		span_id VARCHAR(16) NOT NULL,
		parent_span_id VARCHAR(16),
		agent_id VARCHAR NOT NULL,
		service_name TEXT NOT NULL,
		span_name TEXT NOT NULL,
		span_kind VARCHAR(10),
		start_time TIMESTAMPTZ NOT NULL,
		duration_us BIGINT NOT NULL,
		status_code SMALLINT,
		attributes TEXT,
		PRIMARY KEY (trace_id, span_id)
	)`,

	`CREATE INDEX IF NOT EXISTS idx_beyla_traces_service_time ON beyla_traces(service_name, start_time DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_beyla_traces_trace_id ON beyla_traces(trace_id, start_time DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_beyla_traces_duration ON beyla_traces(duration_us DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_beyla_traces_agent_id ON beyla_traces(agent_id, start_time DESC)`,

	// Agent IP allocations - persistent IP allocation for agents (RFD 019).
	`CREATE TABLE IF NOT EXISTS agent_ip_allocations (
		agent_id TEXT PRIMARY KEY,
		ip_address TEXT NOT NULL UNIQUE,
		allocated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_seen TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,

	`CREATE INDEX IF NOT EXISTS idx_agent_ip_allocations_ip ON agent_ip_allocations(ip_address)`,

	`CREATE TABLE IF NOT EXISTS issued_certificates (
		serial_number TEXT PRIMARY KEY,
		agent_id TEXT NOT NULL,
		colony_id TEXT NOT NULL,
		certificate_pem TEXT NOT NULL,
		issued_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		expires_at TIMESTAMP NOT NULL,
		revoked_at TIMESTAMP,
		revocation_reason TEXT,
		status TEXT NOT NULL DEFAULT 'active'
	)`,

	`CREATE INDEX IF NOT EXISTS idx_issued_certificates_agent ON issued_certificates(agent_id)`,
	`CREATE INDEX IF NOT EXISTS idx_issued_certificates_colony ON issued_certificates(colony_id)`,
	`CREATE INDEX IF NOT EXISTS idx_issued_certificates_status ON issued_certificates(status)`,
	`CREATE INDEX IF NOT EXISTS idx_issued_certificates_expires ON issued_certificates(expires_at)`,

	`CREATE TABLE IF NOT EXISTS certificate_revocations (
		id INTEGER PRIMARY KEY,
		serial_number TEXT NOT NULL,
		revoked_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		reason TEXT NOT NULL,
		revoked_by TEXT
	)`,

	`CREATE INDEX IF NOT EXISTS idx_certificate_revocations_serial ON certificate_revocations(serial_number)`,

	// Debug sessions - active and past debug sessions.
	`CREATE TABLE IF NOT EXISTS debug_sessions (
		session_id VARCHAR PRIMARY KEY,
		collector_id VARCHAR NOT NULL,
		service_name VARCHAR NOT NULL,
		function_name VARCHAR NOT NULL,
		agent_id VARCHAR NOT NULL,
		sdk_addr VARCHAR NOT NULL,
		started_at TIMESTAMP NOT NULL,
		expires_at TIMESTAMP NOT NULL,
		status VARCHAR NOT NULL,
		requested_by VARCHAR,
		event_count INTEGER DEFAULT 0
	)`,

	`CREATE INDEX IF NOT EXISTS idx_debug_sessions_service ON debug_sessions(service_name)`,
	// Note: Removed index on status due to DuckDB limitation with updating indexed columns.
	// `CREATE INDEX IF NOT EXISTS idx_debug_sessions_status ON debug_sessions(status)`,
	`CREATE INDEX IF NOT EXISTS idx_debug_sessions_agent ON debug_sessions(agent_id)`,

	// Debug events - stored uprobe events from debug sessions (RFD 062).
	`CREATE SEQUENCE IF NOT EXISTS seq_debug_events_id START 1`,
	`CREATE TABLE IF NOT EXISTS debug_events (
		id INTEGER PRIMARY KEY DEFAULT nextval('seq_debug_events_id'),
		session_id VARCHAR NOT NULL,
		timestamp TIMESTAMPTZ NOT NULL,
		collector_id VARCHAR NOT NULL,
		agent_id VARCHAR NOT NULL,
		service_name VARCHAR NOT NULL,
		function_name VARCHAR NOT NULL,
		event_type VARCHAR(10) NOT NULL,
		duration_ns BIGINT,
		pid INTEGER,
		tid INTEGER,
		args TEXT,
		return_value TEXT,
		labels TEXT
	)`,

	`CREATE INDEX IF NOT EXISTS idx_debug_events_session ON debug_events(session_id, timestamp)`,
	`CREATE INDEX IF NOT EXISTS idx_debug_events_timestamp ON debug_events(timestamp)`,
	`CREATE INDEX IF NOT EXISTS idx_debug_events_collector ON debug_events(collector_id)`,

	// Function registry - discovered functions from services (RFD 063).
	`CREATE TABLE IF NOT EXISTS functions (
		service_name VARCHAR NOT NULL,
		function_name VARCHAR NOT NULL,
		binary_hash VARCHAR(64) NOT NULL,
		agent_id VARCHAR NOT NULL,
		package_name VARCHAR,
		file_path VARCHAR,
		line_number INTEGER,
		func_offset BIGINT,
		has_dwarf BOOLEAN DEFAULT false,
		embedding FLOAT[384],
		is_exported BOOLEAN DEFAULT false,
		discovered_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_seen TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (service_name, function_name, binary_hash)
	)`,

	`CREATE INDEX IF NOT EXISTS idx_functions_service ON functions(service_name)`,
	`CREATE INDEX IF NOT EXISTS idx_functions_agent ON functions(agent_id)`,
	`CREATE INDEX IF NOT EXISTS idx_functions_name ON functions(function_name)`,
	`CREATE INDEX IF NOT EXISTS idx_functions_last_seen ON functions(last_seen)`,

	// Function metrics - time-series performance data from uprobe sessions (RFD 063).
	`CREATE TABLE IF NOT EXISTS function_metrics (
		function_id VARCHAR NOT NULL,
		timestamp TIMESTAMPTZ NOT NULL,
		p50_latency_ms DOUBLE,
		p95_latency_ms DOUBLE,
		p99_latency_ms DOUBLE,
		calls_per_minute INTEGER,
		error_rate DOUBLE,
		PRIMARY KEY (function_id, timestamp)
	)`,

	`CREATE INDEX IF NOT EXISTS idx_function_metrics_function ON function_metrics(function_id, timestamp DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_function_metrics_timestamp ON function_metrics(timestamp DESC)`,

	// System metrics summaries - aggregated host-level metrics (CPU, Memory, Disk, Network) from agents (RFD 071).
	`CREATE TABLE IF NOT EXISTS system_metrics_summaries (
		bucket_time TIMESTAMP NOT NULL,
		agent_id TEXT NOT NULL,
		metric_name VARCHAR(50) NOT NULL,
		min_value DOUBLE PRECISION,
		max_value DOUBLE PRECISION,
		avg_value DOUBLE PRECISION,
		p95_value DOUBLE PRECISION,
		delta_value DOUBLE PRECISION,
		sample_count BIGINT,
		unit VARCHAR(20),
		metric_type VARCHAR(10),
		attributes TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (bucket_time, agent_id, metric_name, attributes)
	)`,

	`CREATE INDEX IF NOT EXISTS idx_system_summaries_lookup ON system_metrics_summaries(agent_id, bucket_time, metric_name)`,
	`CREATE INDEX IF NOT EXISTS idx_system_summaries_agent_time ON system_metrics_summaries(agent_id, bucket_time DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_system_summaries_bucket_time ON system_metrics_summaries(bucket_time DESC)`,

	// CPU profile summaries - 1-minute aggregated CPU profiling samples (RFD 072).
	`CREATE TABLE IF NOT EXISTS cpu_profile_summaries (
		timestamp TIMESTAMPTZ NOT NULL,
		agent_id TEXT NOT NULL,
		service_name TEXT NOT NULL,
		build_id TEXT NOT NULL,
		stack_hash VARCHAR(64) NOT NULL,
		stack_frame_ids BIGINT[] NOT NULL,
		sample_count INTEGER NOT NULL,
		PRIMARY KEY (timestamp, agent_id, service_name, build_id, stack_hash)
	)`,

	`CREATE INDEX IF NOT EXISTS idx_cpu_profiles_service_time ON cpu_profile_summaries(service_name, timestamp DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_cpu_profiles_agent ON cpu_profile_summaries(agent_id, timestamp DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_cpu_profiles_build_id ON cpu_profile_summaries(build_id)`,

	// Binary metadata registry - build ID to service mapping (RFD 072).
	`CREATE TABLE IF NOT EXISTS binary_metadata_registry (
		build_id TEXT PRIMARY KEY,
		service_name TEXT NOT NULL,
		binary_path TEXT,
		first_seen TIMESTAMPTZ NOT NULL,
		last_seen TIMESTAMPTZ NOT NULL,
		has_debug_info BOOLEAN DEFAULT false
	)`,

	`CREATE INDEX IF NOT EXISTS idx_binary_metadata_service ON binary_metadata_registry(service_name)`,

	// Profile frame dictionary - global frame name mapping (RFD 072).
	`CREATE SEQUENCE IF NOT EXISTS seq_profile_frame_id START 1`,
	`CREATE TABLE IF NOT EXISTS profile_frame_dictionary (
		frame_id BIGINT PRIMARY KEY DEFAULT nextval('seq_profile_frame_id'),
		frame_name TEXT NOT NULL UNIQUE
	)`,

	`CREATE INDEX IF NOT EXISTS idx_profile_frame_name ON profile_frame_dictionary(frame_name)`,
}
