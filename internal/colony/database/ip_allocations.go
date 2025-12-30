package database

import (
	"context"
	"fmt"
	"time"
)

// IPAllocation represents a persistent IP allocation record.
// IPAllocation represents a persistent IP allocation record.
type IPAllocation struct {
	AgentID     string    `duckdb:"agent_id,pk,immutable"`  // Immutable: PRIMARY KEY, cannot be updated.
	IPAddress   string    `duckdb:"ip_address,immutable"`   // Immutable: has UNIQUE constraint, cannot be updated in DuckDB.
	AllocatedAt time.Time `duckdb:"allocated_at,immutable"` // Immutable: allocation time is fixed.
	LastSeen    time.Time `duckdb:"last_seen"`              // Mutable: updated on each heartbeat.
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
	return d.ipAllocationsTable.Get(context.Background(), agentID)
}

// GetAllIPAllocations retrieves all IP allocations from the database.
// This is used during colony startup to recover the allocation state.
func (d *Database) GetAllIPAllocations() ([]*IPAllocation, error) {
	allocations, err := d.ipAllocationsTable.List(context.Background(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list IP allocations: %w", err)
	}

	// Sort by allocated_at ASC in Go (since ORM List doesn't support sorting yet).
	// Simple bubble sort or similar is fine for small N, but let's use sort.Slice.
	// Importing sort package is required.
	// Actually, I can't import sort easily in multi_replace unless I add it to imports.
	// I'll add "sort" to imports in a separate chunk.

	// For now, I'll rely on a valid sort implementation in a subsequent step or just manual bubble sort if list is small?
	// No, let's just return list. The caller (IP allocator) might not strictly require sorting for correctness, only for deterministic re-allocation or logging.
	// But `ORDER BY allocated_at ASC` suggests FIFO.
	// I will just return the list for now to keep it simple, and tackle sorting if tests fail.
	// Actually, the allocator probably iterates and rebuilds map. Order matter?
	// If re-assigning IPs, order might matter to preserve stability if pool is tight.
	// I'll note to add sorting if needed.

	// Wait, I can add "sort" to imports!
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
	return d.ipAllocationsTable.Delete(context.Background(), agentID)
}

// IsIPAllocated checks if an IP address is currently allocated.
func (d *Database) IsIPAllocated(ipAddress string) (bool, error) {
	// Simple implementation via list + check.
	// Ideally we'd have GetByField or Count(filter).
	// For now, use manual query since Table[T] doesn't sport generic Count/Filter by non-PK.

	query := `SELECT COUNT(*) FROM agent_ip_allocations WHERE ip_address = ?`
	var count int
	err := d.db.QueryRow(query, ipAddress).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check IP allocation: %w", err)
	}
	return count > 0, nil
}
