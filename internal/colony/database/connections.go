package database

import (
	"context"
	"fmt"
	"time"
)

// ServiceConnection represents a discovered connection between services.
type ServiceConnection struct {
	FromService     string    `duckdb:"from_service,pk"`
	ToService       string    `duckdb:"to_service,pk"`
	Protocol        string    `duckdb:"protocol,pk"`
	FirstObserved   time.Time `duckdb:"first_observed"`
	LastObserved    time.Time `duckdb:"last_observed"`
	ConnectionCount int       `duckdb:"connection_count"`
}

// MaterializeConnections re-derives service connections from the beyla_traces table
// and upserts the results into service_connections (RFD 092).
func (d *Database) MaterializeConnections(ctx context.Context, since time.Time) error {
	d.connectionsMu.Lock()
	defer d.connectionsMu.Unlock()

	// default to last materialized time minus 1 minute for overlap if since is zero.
	if since.IsZero() {
		since = time.Now().Add(-1 * time.Hour)
	}

	d.logger.Info().
		Time("since", since).
		Msg("Materializing service connections from Beyla traces")

	// Verify we have spans to work with.
	var count int
	if err := d.db.QueryRowContext(ctx, "SELECT count(*) FROM beyla_traces WHERE start_time >= ?", since).Scan(&count); err != nil {
		return fmt.Errorf("failed to check trace count: %w", err)
	}
	if count == 0 {
		d.logger.Info().Msg("No recent traces found for materialization")
		return nil
	}

	d.logger.Info().Int("trace_count", count).Msg("Processing traces for materialization")

	// Robust join strategy:
	// 1. Primary: Use parent_span_id to find direct caller/callee relationships (precise).
	// 2. Fallback: Use trace_id to correlate different services within the same trace.
	//    This handles cases where eBPF context propagation works (shared trace_id)
	//    but the specific parent_span_id link was lost or a middle span (e.g. CLIENT)
	//    was not captured.
	query := `
		WITH child_spans AS (
			-- Potential destination spans (we filter by kind within specific strategies below)
			SELECT * FROM beyla_traces 
			WHERE start_time >= ?
		),
		matches AS (
			-- STRATEGY 1: Direct parent_span_id link (Precise)
			-- Both sides were captured and explicitly linked. This is kind-agnostic to 
			-- support various instrumentation styles and legacy unit tests.
			SELECT 
				c.span_id as child_id,
				LOWER(p.service_name) as from_service,
				LOWER(c.service_name) as to_service,
				c.start_time,
				p.start_time as parent_time,
				1 as priority
			FROM child_spans c
			JOIN beyla_traces p ON c.parent_span_id = p.span_id
			WHERE LOWER(c.service_name) != LOWER(p.service_name)

			UNION ALL

			-- STRATEGY 2: Trace ID match (Fallback when direct link missing but trace context present)
			-- Used when eBPF context propagation works but the parent span itself was not recorded.
			SELECT 
				c.span_id as child_id,
				LOWER(p.service_name) as from_service,
				LOWER(c.service_name) as to_service,
				c.start_time,
				p.start_time as parent_time,
				2 as priority
			FROM child_spans c
			JOIN beyla_traces p ON c.trace_id = p.trace_id
			WHERE c.trace_id != ''
			  AND UPPER(p.span_kind) = 'CLIENT' AND UPPER(c.span_kind) = 'SERVER'
			  AND LOWER(c.service_name) != LOWER(p.service_name)
			  -- Only fallback if the child actually expected a parent (prevents root spans from matching)
			  AND c.parent_span_id != ''

			UNION ALL

			-- STRATEGY 3: Time-based correlation (Last resort fallback)
			-- Handles traces with broken context propagation. Correlation window is 2 seconds.
			SELECT 
				c.span_id as child_id,
				LOWER(p.service_name) as from_service,
				LOWER(c.service_name) as to_service,
				c.start_time,
				p.start_time as parent_time,
				3 as priority
			FROM child_spans c
			JOIN beyla_traces p ON ABS(EXTRACT(EPOCH FROM c.start_time::TIMESTAMP) - EXTRACT(EPOCH FROM p.start_time::TIMESTAMP)) <= 2.0
			WHERE UPPER(p.span_kind) = 'CLIENT' AND UPPER(c.span_kind) = 'SERVER'
			  AND LOWER(c.service_name) != LOWER(p.service_name)
			  -- Only use this heuristic if trace-id based matching is not possible
			  AND (c.trace_id = '' OR p.trace_id = '')
		),
		best_matches AS (
			-- Pick only the best match for each child span to avoid multiplication across strategies.
			-- If multiple candidates exist within the same priority level, pick the one closest in time.
			SELECT from_service, to_service, start_time
			FROM matches
			QUALIFY row_number() OVER (
				PARTITION BY child_id 
				ORDER BY priority ASC, ABS(EXTRACT(EPOCH FROM start_time::TIMESTAMP) - EXTRACT(EPOCH FROM parent_time::TIMESTAMP)) ASC
			) = 1
		),
		aggregated AS (
			SELECT 
				from_service, 
				to_service, 
				'http' as protocol,
				COUNT(*) as connection_count,
				MIN(start_time) as first_observed,
				MAX(start_time) as last_observed
			FROM best_matches
			GROUP BY 1, 2, 3
		)
		INSERT INTO service_connections (from_service, to_service, protocol, connection_count, first_observed, last_observed)
		SELECT from_service, to_service, protocol, connection_count, first_observed, last_observed
		FROM aggregated
		ON CONFLICT (from_service, to_service, protocol) DO UPDATE SET
			connection_count = service_connections.connection_count + excluded.connection_count,
			last_observed    = CASE WHEN EXCLUDED.last_observed > service_connections.last_observed THEN EXCLUDED.last_observed ELSE service_connections.last_observed END
	`

	res, err := d.db.ExecContext(ctx, query, since)
	if err != nil {
		return fmt.Errorf("failed to materialize connections: %w", err)
	}

	rows, _ := res.RowsAffected()
	d.logger.Info().Int64("edges_materialized", rows).Msg("Materialization complete")

	return nil
}

// GetServiceConnections returns materialized service connections, re-deriving them
// from trace data when the cached data is stale (TTL 30 s) (RFD 092).
func (d *Database) GetServiceConnections(ctx context.Context, since time.Time) ([]*ServiceConnection, error) {
	d.connectionsMu.Lock()
	needsMaterialization := time.Since(d.connectionsLastMaterialized) >= d.connectionsCacheTTL
	d.connectionsMu.Unlock()

	if needsMaterialization {
		// MaterializeConnections will handle its own locking.
		if err := d.MaterializeConnections(ctx, since); err != nil {
			d.logger.Warn().Err(err).Msg("Failed to materialize service connections, serving stale data")
		} else {
			d.connectionsMu.Lock()
			d.connectionsLastMaterialized = time.Now()
			d.connectionsMu.Unlock()
		}
	}

	const query = `
		SELECT from_service, to_service, protocol, first_observed, last_observed, connection_count
		FROM service_connections
		ORDER BY connection_count DESC
	`
	rows, err := d.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query service connections: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []*ServiceConnection
	for rows.Next() {
		var c ServiceConnection
		if err := rows.Scan(
			&c.FromService,
			&c.ToService,
			&c.Protocol,
			&c.FirstObserved,
			&c.LastObserved,
			&c.ConnectionCount,
		); err != nil {
			return nil, fmt.Errorf("failed to scan service connection: %w", err)
		}
		results = append(results, &c)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating service connections: %w", err)
	}

	return results, nil
}
