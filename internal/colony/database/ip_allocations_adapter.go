package database

import (
	"database/sql"
	"fmt"

	"github.com/coral-io/coral/internal/wireguard"
)

// GetAllIPAllocationsForWireguard retrieves all IP allocations in the format
// required by the wireguard.IPAllocationStore interface.
func (d *Database) GetAllIPAllocationsForWireguard() ([]*wireguard.StoredIPAllocation, error) {
	allocations, err := d.GetAllIPAllocations()
	if err != nil {
		return nil, err
	}

	result := make([]*wireguard.StoredIPAllocation, len(allocations))
	for i, alloc := range allocations {
		result[i] = &wireguard.StoredIPAllocation{
			AgentID:   alloc.AgentID,
			IPAddress: alloc.IPAddress,
		}
	}

	return result, nil
}

// GetIPAllocationForWireguard retrieves an IP allocation in the format
// required by the wireguard.IPAllocationStore interface.
func (d *Database) GetIPAllocationForWireguard(agentID string) (*wireguard.StoredIPAllocation, error) {
	allocation, err := d.GetIPAllocation(agentID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no IP allocation found for agent %s", agentID)
		}
		return nil, err
	}

	return &wireguard.StoredIPAllocation{
		AgentID:   allocation.AgentID,
		IPAddress: allocation.IPAddress,
	}, nil
}

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

func (s *DatabaseIPAllocationStore) GetIPAllocation(agentID string) (*wireguard.StoredIPAllocation, error) {
	return s.db.GetIPAllocationForWireguard(agentID)
}

func (s *DatabaseIPAllocationStore) GetAllIPAllocations() ([]*wireguard.StoredIPAllocation, error) {
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
