package database

import (
	"fmt"
	"time"
)

// IPAllocation represents a persistent IP allocation record.
type IPAllocation struct {
	AgentID     string
	IPAddress   string
	AllocatedAt time.Time
	LastSeen    time.Time
}

// StoreIPAllocation persists an IP allocation to the database.
// Uses ON CONFLICT to handle both new allocations and updates atomically.
func (d *Database) StoreIPAllocation(agentID, ipAddress string) error {
	now := time.Now()

	// First, check if this is a new allocation for logging purposes
	var exists bool
	checkQuery := `SELECT COUNT(*) > 0 FROM agent_ip_allocations WHERE agent_id = ?`
	err := d.db.QueryRow(checkQuery, agentID).Scan(&exists)

	if err == nil && !exists {
		d.logger.Info().
			Str("agent_id", agentID).
			Str("ip_address", ipAddress).
			Str("db_path", d.path).
			Msg("Storing new IP allocation to database")
	} else if err == nil {
		d.logger.Debug().
			Str("agent_id", agentID).
			Str("ip_address", ipAddress).
			Msg("Updating existing IP allocation")
	}

	// Use ON CONFLICT with explicit conflict target (agent_id).
	// On conflict, only update last_seen timestamp, preserving allocated_at.
	query := `
		INSERT INTO agent_ip_allocations (agent_id, ip_address, allocated_at, last_seen)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (agent_id) DO UPDATE SET
			last_seen = excluded.last_seen
	`
	_, err = d.db.Exec(query, agentID, ipAddress, now, now)
	if err != nil {
		d.logger.Error().
			Err(err).
			Str("agent_id", agentID).
			Str("ip_address", ipAddress).
			Msg("Failed to store IP allocation")
		return fmt.Errorf("failed to store IP allocation: %w", err)
	}

	if !exists {
		d.logger.Info().
			Str("agent_id", agentID).
			Str("ip_address", ipAddress).
			Msg("✅ IP allocation stored successfully")
	}

	return nil
}

// GetIPAllocation retrieves the IP allocation for a specific agent.
func (d *Database) GetIPAllocation(agentID string) (*IPAllocation, error) {
	query := `
		SELECT agent_id, ip_address, allocated_at, last_seen
		FROM agent_ip_allocations
		WHERE agent_id = ?
	`

	var allocation IPAllocation
	err := d.db.QueryRow(query, agentID).Scan(
		&allocation.AgentID,
		&allocation.IPAddress,
		&allocation.AllocatedAt,
		&allocation.LastSeen,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get IP allocation: %w", err)
	}

	return &allocation, nil
}

// GetAllIPAllocations retrieves all IP allocations from the database.
// This is used during colony startup to recover the allocation state.
func (d *Database) GetAllIPAllocations() ([]*IPAllocation, error) {
	query := `
		SELECT agent_id, ip_address, allocated_at, last_seen
		FROM agent_ip_allocations
		ORDER BY allocated_at ASC
	`

	rows, err := d.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query IP allocations: %w", err)
	}
	defer func() { _ = rows.Close() }() // TODO: errcheck

	var allocations []*IPAllocation
	for rows.Next() {
		var allocation IPAllocation
		err := rows.Scan(
			&allocation.AgentID,
			&allocation.IPAddress,
			&allocation.AllocatedAt,
			&allocation.LastSeen,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan IP allocation: %w", err)
		}
		allocations = append(allocations, &allocation)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating IP allocations: %w", err)
	}

	return allocations, nil
}

// UpdateIPAllocationLastSeen updates the last_seen timestamp for an IP allocation.
func (d *Database) UpdateIPAllocationLastSeen(agentID string) error {
	query := `
		UPDATE agent_ip_allocations
		SET last_seen = ?
		WHERE agent_id = ?
	`

	_, err := d.db.Exec(query, time.Now(), agentID)
	if err != nil {
		return fmt.Errorf("failed to update IP allocation last_seen: %w", err)
	}

	return nil
}

// ReleaseIPAllocation removes an IP allocation from the database.
func (d *Database) ReleaseIPAllocation(agentID string) error {
	query := `DELETE FROM agent_ip_allocations WHERE agent_id = ?`

	_, err := d.db.Exec(query, agentID)
	if err != nil {
		return fmt.Errorf("failed to release IP allocation: %w", err)
	}

	return nil
}

// IsIPAllocated checks if an IP address is currently allocated.
func (d *Database) IsIPAllocated(ipAddress string) (bool, error) {
	query := `SELECT COUNT(*) FROM agent_ip_allocations WHERE ip_address = ?`

	var count int
	err := d.db.QueryRow(query, ipAddress).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check IP allocation: %w", err)
	}

	return count > 0, nil
}
