package wireguard

import (
	"fmt"
	"net"
	"sync"
	"testing"
)

// mockIPAllocationStore is a mock implementation of IPAllocationStore for testing.
type mockIPAllocationStore struct {
	mu          sync.RWMutex
	allocations map[string]*StoredIPAllocation // agent_id -> allocation
}

func newMockIPAllocationStore() *mockIPAllocationStore {
	return &mockIPAllocationStore{
		allocations: make(map[string]*StoredIPAllocation),
	}
}

func (m *mockIPAllocationStore) StoreIPAllocation(agentID, ipAddress string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.allocations[agentID] = &StoredIPAllocation{
		AgentID:   agentID,
		IPAddress: ipAddress,
	}
	return nil
}

func (m *mockIPAllocationStore) GetIPAllocation(agentID string) (*StoredIPAllocation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	alloc, ok := m.allocations[agentID]
	if !ok {
		return nil, fmt.Errorf("no allocation found for agent %s", agentID)
	}
	return alloc, nil
}

func (m *mockIPAllocationStore) GetAllIPAllocations() ([]*StoredIPAllocation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*StoredIPAllocation, 0, len(m.allocations))
	for _, alloc := range m.allocations {
		result = append(result, alloc)
	}
	return result, nil
}

func (m *mockIPAllocationStore) UpdateIPAllocationLastSeen(agentID string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.allocations[agentID]; !ok {
		return fmt.Errorf("no allocation found for agent %s", agentID)
	}
	// Mock doesn't track last_seen, just verify agent exists.
	return nil
}

func (m *mockIPAllocationStore) ReleaseIPAllocation(agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.allocations, agentID)
	return nil
}

func (m *mockIPAllocationStore) IsIPAllocated(ipAddress string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, alloc := range m.allocations {
		if alloc.IPAddress == ipAddress {
			return true, nil
		}
	}
	return false, nil
}

func TestPersistentIPAllocator_Allocate(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("10.42.0.0/16")
	store := newMockIPAllocationStore()

	allocator, err := NewPersistentIPAllocator(subnet, store)
	if err != nil {
		t.Fatalf("Failed to create allocator: %v", err)
	}

	// Allocate IP for agent1.
	ip1, err := allocator.Allocate("agent1")
	if err != nil {
		t.Fatalf("Failed to allocate IP: %v", err)
	}

	// Verify IP is in the correct range.
	if !subnet.Contains(ip1) {
		t.Errorf("Allocated IP %s is not in subnet %s", ip1, subnet)
	}

	// Verify IP is stored.
	stored, err := store.GetIPAllocation("agent1")
	if err != nil {
		t.Fatalf("Failed to get stored allocation: %v", err)
	}
	if stored.IPAddress != ip1.String() {
		t.Errorf("Stored IP %s doesn't match allocated IP %s", stored.IPAddress, ip1)
	}

	// Allocate IP for agent2.
	ip2, err := allocator.Allocate("agent2")
	if err != nil {
		t.Fatalf("Failed to allocate second IP: %v", err)
	}

	// Verify IPs are different.
	if ip1.String() == ip2.String() {
		t.Errorf("Allocated IPs are the same: %s", ip1)
	}
}

func TestPersistentIPAllocator_AllocateReconnection(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("10.42.0.0/16")
	store := newMockIPAllocationStore()

	allocator, err := NewPersistentIPAllocator(subnet, store)
	if err != nil {
		t.Fatalf("Failed to create allocator: %v", err)
	}

	// Allocate IP for agent1.
	ip1, err := allocator.Allocate("agent1")
	if err != nil {
		t.Fatalf("Failed to allocate IP: %v", err)
	}

	// Try to allocate again for same agent (reconnection).
	ip2, err := allocator.Allocate("agent1")
	if err != nil {
		t.Fatalf("Failed to allocate IP on reconnection: %v", err)
	}

	// Should get the same IP.
	if ip1.String() != ip2.String() {
		t.Errorf("Reconnection got different IP: %s vs %s", ip1, ip2)
	}
}

func TestPersistentIPAllocator_LoadFromStore(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("10.42.0.0/16")
	store := newMockIPAllocationStore()

	// Pre-populate store with allocations.
	store.StoreIPAllocation("agent1", "10.42.0.5")
	store.StoreIPAllocation("agent2", "10.42.0.10")

	// Create allocator - it should load existing allocations.
	allocator, err := NewPersistentIPAllocator(subnet, store)
	if err != nil {
		t.Fatalf("Failed to create allocator: %v", err)
	}

	// Verify agent1's IP is remembered.
	ip1, err := allocator.GetAgentIP("agent1")
	if err != nil {
		t.Fatalf("Failed to get agent1 IP: %v", err)
	}
	if ip1.String() != "10.42.0.5" {
		t.Errorf("Agent1 IP not loaded correctly: got %s, want 10.42.0.5", ip1)
	}

	// Verify agent2's IP is remembered.
	ip2, err := allocator.GetAgentIP("agent2")
	if err != nil {
		t.Fatalf("Failed to get agent2 IP: %v", err)
	}
	if ip2.String() != "10.42.0.10" {
		t.Errorf("Agent2 IP not loaded correctly: got %s, want 10.42.0.10", ip2)
	}

	// Allocate new IP - should not conflict with loaded IPs.
	ip3, err := allocator.Allocate("agent3")
	if err != nil {
		t.Fatalf("Failed to allocate IP for agent3: %v", err)
	}

	if ip3.String() == "10.42.0.5" || ip3.String() == "10.42.0.10" {
		t.Errorf("New allocation conflicts with loaded IPs: %s", ip3)
	}
}

func TestPersistentIPAllocator_Release(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("10.42.0.0/16")
	store := newMockIPAllocationStore()

	allocator, err := NewPersistentIPAllocator(subnet, store)
	if err != nil {
		t.Fatalf("Failed to create allocator: %v", err)
	}

	// Allocate IP.
	ip, err := allocator.Allocate("agent1")
	if err != nil {
		t.Fatalf("Failed to allocate IP: %v", err)
	}

	// Verify it's allocated.
	if !allocator.IsAllocated(ip) {
		t.Error("IP should be allocated")
	}

	// Release by agent.
	if err := allocator.ReleaseByAgent("agent1"); err != nil {
		t.Fatalf("Failed to release IP: %v", err)
	}

	// Verify it's no longer allocated.
	if allocator.IsAllocated(ip) {
		t.Error("IP should not be allocated after release")
	}

	// Verify it's removed from store.
	_, err = store.GetIPAllocation("agent1")
	if err == nil {
		t.Error("Expected error when getting released allocation from store")
	}
}

func TestPersistentIPAllocator_Concurrent(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("10.42.0.0/16")
	store := newMockIPAllocationStore()

	allocator, err := NewPersistentIPAllocator(subnet, store)
	if err != nil {
		t.Fatalf("Failed to create allocator: %v", err)
	}

	// Allocate IPs concurrently.
	const numAgents = 100
	var wg sync.WaitGroup
	errors := make(chan error, numAgents)
	ips := make(chan string, numAgents)

	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			agentID := fmt.Sprintf("agent%d", id)
			ip, err := allocator.Allocate(agentID)
			if err != nil {
				errors <- err
				return
			}
			ips <- ip.String()
		}(i)
	}

	wg.Wait()
	close(errors)
	close(ips)

	// Check for errors.
	for err := range errors {
		t.Errorf("Concurrent allocation error: %v", err)
	}

	// Verify all IPs are unique.
	ipSet := make(map[string]bool)
	for ip := range ips {
		if ipSet[ip] {
			t.Errorf("Duplicate IP allocated: %s", ip)
		}
		ipSet[ip] = true
	}

	if len(ipSet) != numAgents {
		t.Errorf("Expected %d unique IPs, got %d", numAgents, len(ipSet))
	}
}
