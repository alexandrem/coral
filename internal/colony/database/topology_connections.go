package database

import (
	"context"
	"fmt"
	"time"
)

// TopologyConnection represents a directed L4 network edge observed by an agent (RFD 033).
// One row per unique (source_agent_id, dest_ip, dest_port, protocol) tuple; upserted on each batch.
type TopologyConnection struct {
	SourceAgentID string // Reporting agent.
	DestAgentID   string // Empty string if the destination is external (not a registered agent).
	DestIP        string // Destination IP address.
	DestPort      int    // Destination TCP port.
	Protocol      string // Transport protocol ("tcp", "udp").
	BytesSent     uint64 // Accumulated bytes sent since first observation.
	BytesReceived uint64 // Accumulated bytes received since first observation.
	Retransmits   int    // Accumulated TCP retransmit count; 0 on netstat fallback path.
	RTTUS         int    // Smoothed RTT in microseconds; 0 on netstat fallback path.
	FirstObserved time.Time
	LastObserved  time.Time
}

// upsertQuery is the INSERT … ON CONFLICT statement for topology_connections.
// Parameters: source_agent_id, dest_agent_id (NULL when empty), dest_ip,
// dest_port, protocol, bytes_sent, bytes_received, retransmits, rtt_us
// (NULL when zero), first_observed, last_observed.
const upsertTopologyQuery = `
	INSERT INTO topology_connections
		(source_agent_id, dest_agent_id, dest_ip, dest_port, protocol,
		 bytes_sent, bytes_received, retransmits, rtt_us,
		 first_observed, last_observed)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT (source_agent_id, dest_ip, dest_port, protocol) DO UPDATE SET
		bytes_sent     = topology_connections.bytes_sent     + EXCLUDED.bytes_sent,
		bytes_received = topology_connections.bytes_received + EXCLUDED.bytes_received,
		retransmits    = topology_connections.retransmits    + EXCLUDED.retransmits,
		rtt_us         = COALESCE(EXCLUDED.rtt_us, topology_connections.rtt_us),
		last_observed  = CASE
			WHEN EXCLUDED.last_observed > topology_connections.last_observed
			THEN EXCLUDED.last_observed
			ELSE topology_connections.last_observed
		END
`

// UpsertTopologyConnections inserts or updates a batch of L4 connection edges.
// For existing rows the metrics are accumulated and last_observed is refreshed.
func (d *Database) UpsertTopologyConnections(ctx context.Context, entries []TopologyConnection) error {
	if len(entries) == 0 {
		return nil
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin topology upsert transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, e := range entries {
		// Convert empty DestAgentID and zero RTTUS to NULL in the DB.
		var destAgentID interface{}
		if e.DestAgentID != "" {
			destAgentID = e.DestAgentID
		}

		var rttUS interface{}
		if e.RTTUS != 0 {
			rttUS = e.RTTUS
		}

		_, err := tx.ExecContext(ctx, upsertTopologyQuery,
			e.SourceAgentID,
			destAgentID,
			e.DestIP,
			e.DestPort,
			e.Protocol,
			e.BytesSent,
			e.BytesReceived,
			e.Retransmits,
			rttUS,
			e.FirstObserved,
			e.LastObserved,
		)
		if err != nil {
			return fmt.Errorf("failed to upsert topology connection %s→%s:%d: %w",
				e.SourceAgentID, e.DestIP, e.DestPort, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit topology upsert transaction: %w", err)
	}

	d.logger.Debug().
		Int("count", len(entries)).
		Msg("Upserted topology connections")

	return nil
}

// GetL4Connections returns all L4 topology connections observed since the given time.
func (d *Database) GetL4Connections(ctx context.Context, since time.Time) ([]*TopologyConnection, error) {
	const query = `
		SELECT source_agent_id, COALESCE(dest_agent_id, ''), dest_ip, dest_port, protocol,
		       bytes_sent, bytes_received, retransmits, COALESCE(rtt_us, 0),
		       first_observed, last_observed
		FROM topology_connections
		WHERE last_observed >= ?
		ORDER BY last_observed DESC
	`

	rows, err := d.db.QueryContext(ctx, query, since)
	if err != nil {
		return nil, fmt.Errorf("failed to query topology connections: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []*TopologyConnection
	for rows.Next() {
		var c TopologyConnection
		if err := rows.Scan(
			&c.SourceAgentID,
			&c.DestAgentID,
			&c.DestIP,
			&c.DestPort,
			&c.Protocol,
			&c.BytesSent,
			&c.BytesReceived,
			&c.Retransmits,
			&c.RTTUS,
			&c.FirstObserved,
			&c.LastObserved,
		); err != nil {
			return nil, fmt.Errorf("failed to scan topology connection: %w", err)
		}
		results = append(results, &c)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating topology connections: %w", err)
	}

	return results, nil
}
