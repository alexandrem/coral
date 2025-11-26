package wireguard

import (
	"net"
	"testing"
)

func TestNewIPAllocator(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("100.64.0.0/10")

	allocator, err := NewIPAllocator(subnet)
	if err != nil {
		t.Fatalf("NewIPAllocator failed: %v", err)
	}

	if allocator == nil {
		t.Fatal("allocator is nil")
	}

	// Verify next IP is .2 (skipping .0 and .1)
	expectedIP := net.ParseIP("100.64.0.2")
	if !allocator.nextIP.Equal(expectedIP) {
		t.Errorf("expected next IP %s, got %s", expectedIP, allocator.nextIP)
	}
}

func TestIPAllocator_Allocate(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("100.64.0.0/24")
	allocator, _ := NewIPAllocator(subnet)

	// Allocate first IP
	ip1, err := allocator.Allocate("agent1")
	if err != nil {
		t.Fatalf("Allocate failed: %v", err)
	}

	expectedIP1 := net.ParseIP("100.64.0.2")
	if !ip1.Equal(expectedIP1) {
		t.Errorf("expected IP %s, got %s", expectedIP1, ip1)
	}

	// Allocate second IP
	ip2, err := allocator.Allocate("agent2")
	if err != nil {
		t.Fatalf("Allocate failed: %v", err)
	}

	expectedIP2 := net.ParseIP("100.64.0.3")
	if !ip2.Equal(expectedIP2) {
		t.Errorf("expected IP %s, got %s", expectedIP2, ip2)
	}

	// Verify IPs are different
	if ip1.Equal(ip2) {
		t.Error("allocated IPs should be different")
	}

	// Allocate to same agent again should return same IP
	ip1Again, err := allocator.Allocate("agent1")
	if err != nil {
		t.Fatalf("Allocate failed: %v", err)
	}

	if !ip1Again.Equal(ip1) {
		t.Errorf("expected same IP %s, got %s", ip1, ip1Again)
	}
}

func TestIPAllocator_Release(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("100.64.0.0/24")
	allocator, _ := NewIPAllocator(subnet)

	// Allocate and release
	ip, _ := allocator.Allocate("agent1")

	if !allocator.IsAllocated(ip) {
		t.Error("IP should be allocated")
	}

	err := allocator.Release(ip)
	if err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	if allocator.IsAllocated(ip) {
		t.Error("IP should not be allocated after release")
	}

	// Verify IP is in released pool
	if len(allocator.released) != 1 {
		t.Errorf("expected 1 released IP, got %d", len(allocator.released))
	}
}

func TestIPAllocator_ReleaseByAgent(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("100.64.0.0/24")
	allocator, _ := NewIPAllocator(subnet)

	// Allocate
	ip, _ := allocator.Allocate("agent1")

	// Release by agent ID
	err := allocator.ReleaseByAgent("agent1")
	if err != nil {
		t.Fatalf("ReleaseByAgent failed: %v", err)
	}

	if allocator.IsAllocated(ip) {
		t.Error("IP should not be allocated after release")
	}

	// Try releasing non-existent agent
	err = allocator.ReleaseByAgent("agent999")
	if err == nil {
		t.Error("expected error when releasing non-existent agent")
	}
}

func TestIPAllocator_GetAgentIP(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("100.64.0.0/24")
	allocator, _ := NewIPAllocator(subnet)

	// Allocate
	expectedIP, _ := allocator.Allocate("agent1")

	// Get agent IP
	ip, err := allocator.GetAgentIP("agent1")
	if err != nil {
		t.Fatalf("GetAgentIP failed: %v", err)
	}

	if !ip.Equal(expectedIP) {
		t.Errorf("expected IP %s, got %s", expectedIP, ip)
	}

	// Try getting non-existent agent
	_, err = allocator.GetAgentIP("agent999")
	if err == nil {
		t.Error("expected error when getting non-existent agent")
	}
}

func TestIPAllocator_ReuseReleasedIP(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("100.64.0.0/24")
	allocator, _ := NewIPAllocator(subnet)

	// Allocate and release
	ip1, _ := allocator.Allocate("agent1")
	_ = allocator.Release(ip1)

	// Next allocation should reuse the released IP
	ip2, _ := allocator.Allocate("agent2")

	if !ip1.Equal(ip2) {
		t.Errorf("expected to reuse IP %s, got %s", ip1, ip2)
	}
}

func TestIPAllocator_AllocatedCount(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("100.64.0.0/24")
	allocator, _ := NewIPAllocator(subnet)

	if allocator.AllocatedCount() != 0 {
		t.Errorf("expected 0 allocated IPs, got %d", allocator.AllocatedCount())
	}

	_, _ = allocator.Allocate("agent1")
	if allocator.AllocatedCount() != 1 {
		t.Errorf("expected 1 allocated IP, got %d", allocator.AllocatedCount())
	}

	_, _ = allocator.Allocate("agent2")
	if allocator.AllocatedCount() != 2 {
		t.Errorf("expected 2 allocated IPs, got %d", allocator.AllocatedCount())
	}

	ip, _ := allocator.GetAgentIP("agent1")
	_ = allocator.Release(ip)
	if allocator.AllocatedCount() != 1 {
		t.Errorf("expected 1 allocated IP after release, got %d", allocator.AllocatedCount())
	}
}

func TestIncrementIP(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple increment", "100.64.0.1", "100.64.0.2"},
		{"rollover to next octet", "100.64.0.255", "100.64.1.0"},
		{"double rollover", "100.64.255.255", "100.65.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := net.ParseIP(tt.input)
			expected := net.ParseIP(tt.expected)

			result := incrementIP(input)

			if !result.Equal(expected) {
				t.Errorf("incrementIP(%s) = %s, want %s", tt.input, result, tt.expected)
			}
		})
	}
}
