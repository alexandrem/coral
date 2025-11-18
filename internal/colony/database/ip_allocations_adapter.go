package database

import (
	"database/sql"
	"fmt"
)

// StoredIPAllocation represents a stored IP allocation for the allocator interface.
// This matches the wireguard.StoredIPAllocation type.
type StoredIPAllocation struct {
	AgentID   string
	IPAddress string
}

// GetAllIPAllocations retrieves all IP allocations in the format
// required by the wireguard.IPAllocationStore interface.
// This overrides the original GetAllIPAllocations to return the correct type.
func (d *Database) GetAllIPAllocationsForWireguard() ([]*StoredIPAllocation, error) {
	allocations, err := d.GetAllIPAllocations()
	if err != nil {
		return nil, err
	}

	result := make([]*StoredIPAllocation, len(allocations))
	for i, alloc := range allocations {
		result[i] = &StoredIPAllocation{
			AgentID:   alloc.AgentID,
			IPAddress: alloc.IPAddress,
		}
	}

	return result, nil
}

// GetIPAllocationForWireguard retrieves an IP allocation in the format
// required by the wireguard.IPAllocationStore interface.
func (d *Database) GetIPAllocationForWireguard(agentID string) (*StoredIPAllocation, error) {
	allocation, err := d.GetIPAllocation(agentID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no IP allocation found for agent %s", agentID)
		}
		return nil, err
	}

	return &StoredIPAllocation{
		AgentID:   allocation.AgentID,
		IPAddress: allocation.IPAddress,
	}, nil
}

// Ensure Database implements the wireguard.IPAllocationStore interface.
// This is done by creating wrapper methods that match the interface.

// DatabaseIPAllocationStore wraps Database to implement wireguard.IPAllocationStore.
type DatabaseIPAllocationStore struct {
	db *Database
}

// NewIPAllocationStore creates a wrapper that implements wireguard.IPAllocationStore.
func NewIPAllocationStore(db *Database) *DatabaseIPAllocationStore {
	return &DatabaseIPAllocationStore{db: db}
}

func (s *DatabaseIPAllocationStore) StoreIPAllocation(agentID, ipAddress string) error {
	return s.db.StoreIPAllocation(agentID, ipAddress)
}

func (s *DatabaseIPAllocationStore) GetIPAllocation(agentID string) (*StoredIPAllocation, error) {
	return s.db.GetIPAllocationForWireguard(agentID)
}

func (s *DatabaseIPAllocationStore) GetAllIPAllocations() ([]*StoredIPAllocation, error) {
	return s.db.GetAllIPAllocationsForWireguard()
}

func (s *DatabaseIPAllocationStore) UpdateIPAllocationLastSeen(agentID string) error {
	return s.db.UpdateIPAllocationLastSeen(agentID)
}

func (s *DatabaseIPAllocationStore) ReleaseIPAllocation(agentID string) error {
	return s.db.ReleaseIPAllocation(agentID)
}

func (s *DatabaseIPAllocationStore) IsIPAllocated(ipAddress string) (bool, error) {
	return s.db.IsIPAllocated(ipAddress)
}
