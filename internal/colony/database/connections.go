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
// The derivation uses a parent-span self-join to identify calls that cross service
// boundaries within the given time window.
func (d *Database) MaterializeConnections(ctx context.Context, since time.Time) error {
	d.logger.Info().
		Time("since", since).
		Msg("Materializing service connections")

	// Query trace pairs across service boundaries to discover dependency edges.
	// We join spans sharing the same Trace ID where one's Span ID is another's Parent ID.
	res, err := d.db.ExecContext(ctx, `
		INSERT INTO service_connections (from_service, to_service, protocol, first_observed, last_observed, connection_count)
		SELECT
			parent.service_name   AS from_service,
			child.service_name    AS to_service,
			'http'                AS protocol,
			MIN(child.start_time) AS first_observed,
			MAX(child.start_time) AS last_observed,
			COUNT(*)              AS connection_count
		FROM beyla_traces child
		JOIN beyla_traces parent
			ON  child.trace_id       = parent.trace_id
			AND child.parent_span_id = parent.span_id
			AND child.service_name  != parent.service_name
		WHERE child.start_time >= ?
		GROUP BY parent.service_name, child.service_name
		ON CONFLICT (from_service, to_service, protocol) DO UPDATE SET
			last_observed    = EXCLUDED.last_observed,
			connection_count = EXCLUDED.connection_count
	`, since)
	if err != nil {
		return fmt.Errorf("failed to materialize connections: %w", err)
	}

	rowsAffected, _ := res.RowsAffected()
	d.logger.Info().
		Int64("rows_affected", rowsAffected).
		Msg("Materialized service connections")

	return nil
}

// GetServiceConnections returns materialized service connections, re-deriving them
// from trace data when the cached data is stale (TTL 30 s) (RFD 092).
func (d *Database) GetServiceConnections(ctx context.Context, since time.Time) ([]*ServiceConnection, error) {
	d.connectionsMu.Lock()
	needsMaterialization := time.Since(d.connectionsLastMaterialized) >= d.connectionsCacheTTL
	d.connectionsMu.Unlock()

	if needsMaterialization {
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
