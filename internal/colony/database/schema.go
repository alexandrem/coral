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

	// Drop HNSW index if it exists in a legacy database, to prevent
	// DuckDB WAL replay failures on subsequent restarts. The vector search
	// fallback uses native array_cosine_similarity which is fast enough.
	func() {
		// Try to drop it safely, ignoring errors (e.g., if VSS is not loaded)
		_, _ = d.db.Exec(`DROP INDEX IF EXISTS idx_functions_embedding`)
	}()

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
		span_kind VARCHAR,
		start_time TIMESTAMPTZ NOT NULL,
		duration_us BIGINT NOT NULL,
		status_code SMALLINT,
		attributes TEXT,
		PRIMARY KEY (agent_id, service_name, trace_id, span_id)
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
	// `CREATE INDEX IF NOT EXISTS idx_issued_certificates_status ON issued_certificates(status)`,
	`CREATE INDEX IF NOT EXISTS idx_issued_certificates_expires ON issued_certificates(expires_at)`,

	`CREATE TABLE IF NOT EXISTS certificate_revocations (
		id INTEGER PRIMARY KEY,
		serial_number TEXT NOT NULL,
		revoked_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		reason TEXT NOT NULL,
		revoked_by TEXT
	)`,

	`CREATE INDEX IF NOT EXISTS idx_certificate_revocations_serial ON certificate_revocations(serial_number)`,

	// Bootstrap PSKs - pre-shared keys for agent certificate issuance (RFD 088).
	`CREATE TABLE IF NOT EXISTS bootstrap_psks (
		id TEXT PRIMARY KEY,
		encrypted_psk BLOB NOT NULL,
		encryption_nonce BLOB NOT NULL,
		status TEXT NOT NULL DEFAULT 'active',
		created_at TIMESTAMP NOT NULL,
		grace_expires_at TIMESTAMP,
		revoked_at TIMESTAMP
	)`,

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

	// Memory profile summaries - 1-minute aggregated memory profiling samples (RFD 077).
	`CREATE TABLE IF NOT EXISTS memory_profile_summaries (
		timestamp TIMESTAMPTZ NOT NULL,
		agent_id TEXT NOT NULL,
		service_name TEXT NOT NULL,
		build_id TEXT NOT NULL,
		stack_hash VARCHAR(64) NOT NULL,
		stack_frame_ids BIGINT[] NOT NULL,
		alloc_bytes BIGINT NOT NULL,
		alloc_objects BIGINT NOT NULL,
		PRIMARY KEY (timestamp, agent_id, service_name, build_id, stack_hash)
	)`,

	`CREATE INDEX IF NOT EXISTS idx_memory_profiles_service_time ON memory_profile_summaries(service_name, timestamp DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_memory_profiles_agent ON memory_profile_summaries(agent_id, timestamp DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_memory_profiles_build_id ON memory_profile_summaries(build_id)`,

	// Polling checkpoints - sequence-based checkpoint tracking per agent per data type (RFD 089).
	`CREATE TABLE IF NOT EXISTS polling_checkpoints (
		agent_id TEXT NOT NULL,
		data_type TEXT NOT NULL,
		session_id TEXT NOT NULL,
		last_seq_id UBIGINT NOT NULL,
		last_poll_time TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (agent_id, data_type)
	)`,

	`CREATE INDEX IF NOT EXISTS idx_polling_checkpoints_agent ON polling_checkpoints(agent_id)`,

	// Sequence gaps - tracks detected gaps in polling sequences for recovery (RFD 089).
	`CREATE SEQUENCE IF NOT EXISTS seq_sequence_gaps_id START 1`,
	`CREATE TABLE IF NOT EXISTS sequence_gaps (
		id INTEGER DEFAULT nextval('seq_sequence_gaps_id'),
		agent_id TEXT NOT NULL,
		data_type TEXT NOT NULL,
		start_seq_id UBIGINT NOT NULL,
		end_seq_id UBIGINT NOT NULL,
		detected_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		recovered_at TIMESTAMP,
		status TEXT NOT NULL DEFAULT 'detected',
		recovery_attempts INTEGER DEFAULT 0,
		last_recovery_attempt TIMESTAMP
	)`,

	`CREATE INDEX IF NOT EXISTS idx_sequence_gaps_agent ON sequence_gaps(agent_id, data_type)`,
	`CREATE INDEX IF NOT EXISTS idx_sequence_gaps_status ON sequence_gaps(status)`,

	// Correlation triggers - events fired by agent-side correlation strategies (RFD 091).
	`CREATE TABLE IF NOT EXISTS correlation_triggers (
		id TEXT PRIMARY KEY,
		correlation_id TEXT NOT NULL,
		strategy TEXT NOT NULL,
		fired_at TIMESTAMP NOT NULL,
		agent_id TEXT NOT NULL,
		service_name TEXT NOT NULL,
		context TEXT,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,

	`CREATE INDEX IF NOT EXISTS idx_correlation_triggers_correlation ON correlation_triggers(correlation_id, fired_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_correlation_triggers_service ON correlation_triggers(service_name, fired_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_correlation_triggers_agent ON correlation_triggers(agent_id, fired_at DESC)`,

	// L4 topology connections - outbound TCP edges observed by agents via eBPF or netstat (RFD 033).
	// One row per directed edge (source_agent_id, dest_ip, dest_port, protocol); upserted on each batch.
	`CREATE TABLE IF NOT EXISTS topology_connections (
		source_agent_id VARCHAR     NOT NULL,
		dest_agent_id   VARCHAR,
		dest_ip         VARCHAR     NOT NULL,
		dest_port       INTEGER     NOT NULL,
		protocol        VARCHAR     NOT NULL,
		bytes_sent      BIGINT      NOT NULL DEFAULT 0,
		bytes_received  BIGINT      NOT NULL DEFAULT 0,
		retransmits     INTEGER     NOT NULL DEFAULT 0,
		rtt_us          INTEGER,
		first_observed  TIMESTAMPTZ NOT NULL,
		last_observed   TIMESTAMPTZ NOT NULL,
		PRIMARY KEY (source_agent_id, dest_ip, dest_port, protocol)
	)`,

	`CREATE INDEX IF NOT EXISTS idx_topology_connections_source ON topology_connections(source_agent_id)`,
	// Note: No index on dest_agent_id due to DuckDB limitation with updating indexed columns in ON CONFLICT.
	`CREATE INDEX IF NOT EXISTS idx_topology_connections_dest_ip ON topology_connections(dest_ip)`,
}
