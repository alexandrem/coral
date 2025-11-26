package wireguard

import "net"

// Allocator defines the interface for IP address allocation.
// Both IPAllocator and PersistentIPAllocator implement this interface.
type Allocator interface {
	// Allocate assigns an IP address to the given agent.
	Allocate(agentID string) (net.IP, error)

	// Release marks an IP address as available for reuse.
	Release(ip net.IP) error

	// ReleaseByAgent releases the IP address allocated to the given agent.
	ReleaseByAgent(agentID string) error

	// IsAllocated checks if an IP address is currently allocated.
	IsAllocated(ip net.IP) bool

	// GetAgentIP returns the IP address allocated to the given agent.
	GetAgentIP(agentID string) (net.IP, error)

	// AllocatedCount returns the number of currently allocated IPs.
	AllocatedCount() int
}
