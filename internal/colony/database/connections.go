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
		WITH joined_traces AS (
			SELECT  LOWER(parent.service_name) AS from_service,
					LOWER(child.service_name)  AS to_service,
					'http'              AS protocol,
					COUNT(*)            AS connection_count,
					MIN(child.start_time) AS first_observed,
					MAX(child.start_time) AS last_observed
			FROM beyla_traces child
			JOIN beyla_traces parent
				ON (child.parent_span_id = parent.span_id OR
				     (child.trace_id = parent.trace_id AND child.trace_id != '' AND
				      UPPER(parent.span_kind) = 'CLIENT' AND UPPER(child.span_kind) = 'SERVER' AND
				      child.parent_span_id != '' AND
				      NOT EXISTS (SELECT 1 FROM beyla_traces p2 WHERE p2.span_id = child.parent_span_id)) OR
				     (UPPER(parent.span_kind) = 'CLIENT' AND UPPER(child.span_kind) = 'SERVER' AND
				      (parent.trace_id = '' OR child.trace_id = '' OR
				       NOT EXISTS (SELECT 1 FROM beyla_traces x
				                   WHERE x.trace_id = parent.trace_id
				                     AND UPPER(x.span_kind) = 'SERVER'
				                     AND LOWER(x.service_name) != LOWER(parent.service_name))) AND
				      ABS(EXTRACT(EPOCH FROM child.start_time::TIMESTAMP) - EXTRACT(EPOCH FROM parent.start_time::TIMESTAMP)) <= 2.0))

			WHERE   LOWER(child.service_name) != LOWER(parent.service_name)

			AND     child.start_time    >= ?
			GROUP BY 1, 2, 3
		)

		INSERT INTO service_connections (from_service, to_service, protocol, connection_count, first_observed, last_observed)
		SELECT from_service, to_service, protocol, connection_count, first_observed, last_observed
		FROM joined_traces
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
