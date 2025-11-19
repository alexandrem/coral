package wireguard

import (
	"fmt"
	"net"
	"sync"
)

// IPAllocationStore defines the interface for persisting IP allocations.
// This allows the allocator to work with different storage backends (database, file, etc.).
type IPAllocationStore interface {
	// StoreIPAllocation persists an IP allocation.
	StoreIPAllocation(agentID, ipAddress string) error

	// GetIPAllocation retrieves an IP allocation for a specific agent.
	GetIPAllocation(agentID string) (*StoredIPAllocation, error)

	// GetAllIPAllocations retrieves all IP allocations (for recovery).
	GetAllIPAllocations() ([]*StoredIPAllocation, error)

	// UpdateIPAllocationLastSeen updates the last_seen timestamp.
	UpdateIPAllocationLastSeen(agentID string) error

	// ReleaseIPAllocation removes an IP allocation.
	ReleaseIPAllocation(agentID string) error

	// IsIPAllocated checks if an IP is currently allocated.
	IsIPAllocated(ipAddress string) (bool, error)
}

// StoredIPAllocation represents a stored IP allocation record.
type StoredIPAllocation struct {
	AgentID   string
	IPAddress string
}

// PersistentIPAllocator manages IP address allocation with database persistence.
// It combines the in-memory IPAllocator with a persistent store for recovery.
type PersistentIPAllocator struct {
	allocator *IPAllocator
	store     IPAllocationStore
	mu        sync.RWMutex
}

// NewPersistentIPAllocator creates a new persistent IP allocator.
// It loads existing allocations from the store during initialization.
func NewPersistentIPAllocator(subnet *net.IPNet, store IPAllocationStore) (*PersistentIPAllocator, error) {
	if subnet == nil {
		return nil, fmt.Errorf("subnet is nil")
	}
	if store == nil {
		return nil, fmt.Errorf("store is nil")
	}

	// Create the in-memory allocator.
	allocator, err := NewIPAllocator(subnet)
	if err != nil {
		return nil, fmt.Errorf("failed to create IP allocator: %w", err)
	}

	pa := &PersistentIPAllocator{
		allocator: allocator,
		store:     store,
	}

	// Load existing allocations from store.
	if err := pa.loadAllocationsFromStore(); err != nil {
		return nil, fmt.Errorf("failed to load allocations from store: %w", err)
	}

	return pa, nil
}

// loadAllocationsFromStore loads all existing allocations from the persistent store
// into the in-memory allocator. This is called during initialization to recover state.
func (pa *PersistentIPAllocator) loadAllocationsFromStore() error {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	allocations, err := pa.store.GetAllIPAllocations()
	if err != nil {
		return fmt.Errorf("failed to get allocations from store: %w", err)
	}

	// Restore each allocation into the in-memory allocator.
	for _, allocation := range allocations {
		ip := net.ParseIP(allocation.IPAddress)
		if ip == nil {
			// Skip invalid IP addresses from database.
			continue
		}

		// Directly update the in-memory allocator's state.
		// We bypass the normal Allocate() method to avoid generating new IPs.
		pa.allocator.mu.Lock()
		pa.allocator.allocated[ip.String()] = allocation.AgentID

		// Update nextIP if necessary to avoid reusing this IP.
		// If the loaded IP is >= nextIP, we need to move nextIP past it.
		if pa.isIPAfterOrEqualNext(ip) {
			pa.allocator.nextIP = incrementIP(ip)
		}
		pa.allocator.mu.Unlock()
	}

	return nil
}

// isIPAfterOrEqualNext checks if the given IP comes after or is equal to the current nextIP.
// We need to include equality because if the loaded IP equals nextIP, we still need to increment.
func (pa *PersistentIPAllocator) isIPAfterOrEqualNext(ip net.IP) bool {
	// Normalize both IPs to IPv4 for comparison.
	// This is necessary because net.IP can be stored in different formats (IPv4 or IPv6-mapped).
	ip4 := ip.To4()
	nextIP4 := pa.allocator.nextIP.To4()

	if ip4 == nil || nextIP4 == nil {
		return false
	}

	// Compare byte-by-byte.
	for i := 0; i < len(ip4); i++ {
		if ip4[i] > nextIP4[i] {
			return true
		} else if ip4[i] < nextIP4[i] {
			return false
		}
	}
	// IPs are equal - return true so we increment past it.
	return true
}

// Allocate assigns the next available IP address to the given agent.
// It first checks if the agent already has an allocation, then allocates from
// the in-memory pool and persists to the store.
func (pa *PersistentIPAllocator) Allocate(agentID string) (net.IP, error) {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	// Try to allocate from the in-memory allocator.
	ip, err := pa.allocator.Allocate(agentID)
	if err != nil {
		return nil, err
	}

	// Persist the allocation to the store.
	// If this fails, we should release the in-memory allocation.
	if err := pa.store.StoreIPAllocation(agentID, ip.String()); err != nil {
		// Rollback the in-memory allocation.
		pa.allocator.Release(ip)
		return nil, fmt.Errorf("failed to persist IP allocation: %w", err)
	}

	return ip, nil
}

// Release marks an IP address as available for reuse.
// It releases from both the in-memory allocator and the persistent store.
func (pa *PersistentIPAllocator) Release(ip net.IP) error {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	// Release from in-memory allocator.
	if err := pa.allocator.Release(ip); err != nil {
		return err
	}

	// Find the agent ID for this IP to delete from store.
	// We need to do this before we release from memory.
	ipStr := ip.String()
	pa.allocator.mu.RLock()
	agentID := ""
	for ipKey, id := range pa.allocator.allocated {
		if ipKey == ipStr {
			agentID = id
			break
		}
	}
	pa.allocator.mu.RUnlock()

	if agentID != "" {
		// Delete from persistent store.
		if err := pa.store.ReleaseIPAllocation(agentID); err != nil {
			return fmt.Errorf("failed to release IP from store: %w", err)
		}
	}

	return nil
}

// ReleaseByAgent releases the IP address allocated to the given agent.
func (pa *PersistentIPAllocator) ReleaseByAgent(agentID string) error {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	// Release from in-memory allocator.
	if err := pa.allocator.ReleaseByAgent(agentID); err != nil {
		return err
	}

	// Delete from persistent store.
	if err := pa.store.ReleaseIPAllocation(agentID); err != nil {
		return fmt.Errorf("failed to release IP from store: %w", err)
	}

	return nil
}

// IsAllocated checks if an IP address is currently allocated.
func (pa *PersistentIPAllocator) IsAllocated(ip net.IP) bool {
	pa.mu.RLock()
	defer pa.mu.RUnlock()

	return pa.allocator.IsAllocated(ip)
}

// GetAgentIP returns the IP address allocated to the given agent.
func (pa *PersistentIPAllocator) GetAgentIP(agentID string) (net.IP, error) {
	pa.mu.RLock()
	defer pa.mu.RUnlock()

	return pa.allocator.GetAgentIP(agentID)
}

// AllocatedCount returns the number of currently allocated IPs.
func (pa *PersistentIPAllocator) AllocatedCount() int {
	pa.mu.RLock()
	defer pa.mu.RUnlock()

	return pa.allocator.AllocatedCount()
}

// UpdateLastSeen updates the last_seen timestamp for an agent's allocation.
func (pa *PersistentIPAllocator) UpdateLastSeen(agentID string) error {
	pa.mu.RLock()
	defer pa.mu.RUnlock()

	return pa.store.UpdateIPAllocationLastSeen(agentID)
}
