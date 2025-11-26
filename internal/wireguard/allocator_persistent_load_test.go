package wireguard

import (
	"fmt"
	"net"
	"testing"
)

// TestPersistentIPAllocator_LoadAndAllocate tests that existing allocations
// are loaded correctly and nextIP is incremented to prevent reuse.
func TestPersistentIPAllocator_LoadAndAllocate(t *testing.T) {
	_, subnet, err := net.ParseCIDR("10.42.0.0/16")
	if err != nil {
		t.Fatalf("Failed to parse subnet: %v", err)
	}

	// Create store with one existing allocation
	store := &mockIPAllocationStore{
		allocations: map[string]*StoredIPAllocation{
			"agent-1": {
				AgentID:   "agent-1",
				IPAddress: "10.42.0.2",
			},
		},
	}

	// Create persistent allocator - should load existing allocation
	allocator, err := NewPersistentIPAllocator(subnet, store)
	if err != nil {
		t.Fatalf("Failed to create allocator: %v", err)
	}

	// Verify the existing allocation was loaded
	ip1, err := allocator.GetAgentIP("agent-1")
	if err != nil {
		t.Errorf("Failed to get agent-1 IP: %v", err)
	}
	if ip1.String() != "10.42.0.2" {
		t.Errorf("Expected agent-1 to have IP 10.42.0.2, got %s", ip1.String())
	}

	// Allocate to a new agent - should get the NEXT IP (10.42.0.3)
	ip2, err := allocator.Allocate("agent-2")
	if err != nil {
		t.Fatalf("Failed to allocate IP for agent-2: %v", err)
	}

	if ip2.String() != "10.42.0.3" {
		t.Errorf("Expected agent-2 to get IP 10.42.0.3, got %s", ip2.String())
	}

	// Verify both allocations are in the store
	if len(store.allocations) != 2 {
		t.Errorf("Expected 2 allocations in store, got %d", len(store.allocations))
	}

	// Verify agent-2 allocation was persisted
	storedAlloc, exists := store.allocations["agent-2"]
	if !exists {
		t.Error("agent-2 allocation not found in store")
	} else if storedAlloc.IPAddress != "10.42.0.3" {
		t.Errorf("Expected agent-2 to have IP 10.42.0.3 in store, got %s", storedAlloc.IPAddress)
	}
}

// TestPersistentIPAllocator_LoadMultipleAllocations tests loading multiple
// existing allocations and ensures nextIP is set correctly.
func TestPersistentIPAllocator_LoadMultipleAllocations(t *testing.T) {
	_, subnet, err := net.ParseCIDR("10.42.0.0/16")
	if err != nil {
		t.Fatalf("Failed to parse subnet: %v", err)
	}

	// Create store with multiple existing allocations (not sequential)
	store := &mockIPAllocationStore{
		allocations: map[string]*StoredIPAllocation{
			"agent-1": {
				AgentID:   "agent-1",
				IPAddress: "10.42.0.2",
			},
			"agent-2": {
				AgentID:   "agent-2",
				IPAddress: "10.42.0.5",
			},
			"agent-3": {
				AgentID:   "agent-3",
				IPAddress: "10.42.0.3",
			},
		},
	}

	// Create persistent allocator
	allocator, err := NewPersistentIPAllocator(subnet, store)
	if err != nil {
		t.Fatalf("Failed to create allocator: %v", err)
	}

	// Verify count
	if allocator.AllocatedCount() != 3 {
		t.Errorf("Expected 3 allocations, got %d", allocator.AllocatedCount())
	}

	// Allocate to a new agent - should get IP AFTER the highest loaded IP (10.42.0.6)
	ip4, err := allocator.Allocate("agent-4")
	if err != nil {
		t.Fatalf("Failed to allocate IP for agent-4: %v", err)
	}

	// Should get 10.42.0.6 (one after the highest: 10.42.0.5)
	if ip4.String() != "10.42.0.6" {
		t.Errorf("Expected agent-4 to get IP 10.42.0.6, got %s", ip4.String())
	}
}

// TestPersistentIPAllocator_LoadWithFirstIP tests that when the first IP
// in the subnet is already allocated, it's properly loaded and nextIP is incremented.
func TestPersistentIPAllocator_LoadWithFirstIP(t *testing.T) {
	_, subnet, err := net.ParseCIDR("10.42.0.0/16")
	if err != nil {
		t.Fatalf("Failed to parse subnet: %v", err)
	}

	// Create store with the very first allocatable IP
	store := &mockIPAllocationStore{
		allocations: map[string]*StoredIPAllocation{
			"agent-first": {
				AgentID:   "agent-first",
				IPAddress: "10.42.0.2", // First allocatable IP in subnet
			},
		},
	}

	// Create persistent allocator
	allocator, err := NewPersistentIPAllocator(subnet, store)
	if err != nil {
		t.Fatalf("Failed to create allocator: %v", err)
	}

	// Allocate to a new agent - should NOT reuse 10.42.0.2
	ip, err := allocator.Allocate("agent-new")
	if err != nil {
		t.Fatalf("Failed to allocate IP: %v", err)
	}

	if ip.String() == "10.42.0.2" {
		t.Errorf("New allocation reused the loaded IP 10.42.0.2, should have gotten 10.42.0.3")
	}

	if ip.String() != "10.42.0.3" {
		t.Errorf("Expected new allocation to get 10.42.0.3, got %s", ip.String())
	}
}

// TestPersistentIPAllocator_ReconnectingAgentKeepsIP tests that when an agent
// that already has an allocation "re-registers", it keeps the same IP.
func TestPersistentIPAllocator_ReconnectingAgentKeepsIP(t *testing.T) {
	_, subnet, err := net.ParseCIDR("10.42.0.0/16")
	if err != nil {
		t.Fatalf("Failed to parse subnet: %v", err)
	}

	store := &mockIPAllocationStore{
		allocations: map[string]*StoredIPAllocation{},
	}

	allocator, err := NewPersistentIPAllocator(subnet, store)
	if err != nil {
		t.Fatalf("Failed to create allocator: %v", err)
	}

	// First allocation
	ip1, err := allocator.Allocate("agent-reconnect")
	if err != nil {
		t.Fatalf("Failed to allocate IP: %v", err)
	}

	// Same agent "re-registers" (allocates again)
	ip2, err := allocator.Allocate("agent-reconnect")
	if err != nil {
		t.Fatalf("Failed to allocate IP on reconnect: %v", err)
	}

	// Should get the same IP
	if ip1.String() != ip2.String() {
		t.Errorf("Reconnecting agent got different IP: first=%s, second=%s", ip1.String(), ip2.String())
	}

	// Should only have ONE allocation in the store
	if len(store.allocations) != 1 {
		t.Errorf("Expected 1 allocation in store, got %d", len(store.allocations))
	}
}

// TestPersistentIPAllocator_LoadEmptyStore tests that an empty store
// starts allocating from the first IP.
func TestPersistentIPAllocator_LoadEmptyStore(t *testing.T) {
	_, subnet, err := net.ParseCIDR("10.42.0.0/16")
	if err != nil {
		t.Fatalf("Failed to parse subnet: %v", err)
	}

	// Empty store
	store := &mockIPAllocationStore{
		allocations: map[string]*StoredIPAllocation{},
	}

	allocator, err := NewPersistentIPAllocator(subnet, store)
	if err != nil {
		t.Fatalf("Failed to create allocator: %v", err)
	}

	// First allocation should get the first allocatable IP
	ip, err := allocator.Allocate("agent-1")
	if err != nil {
		t.Fatalf("Failed to allocate IP: %v", err)
	}

	if ip.String() != "10.42.0.2" {
		t.Errorf("Expected first allocation to be 10.42.0.2, got %s", ip.String())
	}
}

// TestPersistentIPAllocator_ConcurrentLoadAndAllocate tests that loading
// existing allocations doesn't cause race conditions with new allocations.
func TestPersistentIPAllocator_ConcurrentLoadAndAllocate(t *testing.T) {
	_, subnet, err := net.ParseCIDR("10.42.0.0/16")
	if err != nil {
		t.Fatalf("Failed to parse subnet: %v", err)
	}

	// Create store with one existing allocation
	store := &mockIPAllocationStore{
		allocations: map[string]*StoredIPAllocation{
			"agent-existing": {
				AgentID:   "agent-existing",
				IPAddress: "10.42.0.2",
			},
		},
	}

	allocator, err := NewPersistentIPAllocator(subnet, store)
	if err != nil {
		t.Fatalf("Failed to create allocator: %v", err)
	}

	// Allocate 10 IPs concurrently
	const numAgents = 10
	results := make(chan string, numAgents)
	errors := make(chan error, numAgents)

	for i := 0; i < numAgents; i++ {
		go func(id int) {
			agentID := fmt.Sprintf("agent-%d", id)
			ip, err := allocator.Allocate(agentID)
			if err != nil {
				errors <- err
				return
			}
			results <- ip.String()
		}(i)
	}

	// Collect results
	allocatedIPs := make(map[string]bool)
	for i := 0; i < numAgents; i++ {
		select {
		case err := <-errors:
			t.Errorf("Allocation failed: %v", err)
		case ip := <-results:
			if allocatedIPs[ip] {
				t.Errorf("Duplicate IP allocation: %s", ip)
			}
			allocatedIPs[ip] = true
		}
	}

	// Verify none of the new allocations got the existing IP
	if allocatedIPs["10.42.0.2"] {
		t.Error("New allocation reused existing IP 10.42.0.2")
	}

	// Verify we got exactly 10 unique IPs
	if len(allocatedIPs) != numAgents {
		t.Errorf("Expected %d unique IPs, got %d", numAgents, len(allocatedIPs))
	}
}
