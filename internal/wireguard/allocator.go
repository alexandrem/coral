// Package wireguard provides WireGuard mesh network management.
package wireguard

import (
	"fmt"
	"net"
	"sync"
)

// IPAllocator manages IP address allocation for a WireGuard mesh.
type IPAllocator struct {
	subnet    *net.IPNet
	allocated map[string]string // IP -> AgentID
	released  []net.IP          // Pool of released IPs
	nextIP    net.IP
	mu        sync.RWMutex
}

// NewIPAllocator creates a new IP allocator for the given subnet.
func NewIPAllocator(subnet *net.IPNet) (*IPAllocator, error) {
	if subnet == nil {
		return nil, fmt.Errorf("subnet is nil")
	}

	// Calculate the first usable IP (network address + 2, since +1 is the colony)
	nextIP := make(net.IP, len(subnet.IP))
	copy(nextIP, subnet.IP)

	// Increment by 2 to skip network address (x.x.x.0) and colony address (x.x.x.1)
	nextIP = incrementIP(nextIP)
	nextIP = incrementIP(nextIP)

	return &IPAllocator{
		subnet:    subnet,
		allocated: make(map[string]string),
		released:  make([]net.IP, 0),
		nextIP:    nextIP,
	}, nil
}

// Allocate assigns the next available IP address to the given agent.
func (a *IPAllocator) Allocate(agentID string) (net.IP, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if agentID == "" {
		return nil, fmt.Errorf("agent ID is required")
	}

	// Check if agent already has an IP
	for ip, id := range a.allocated {
		if id == agentID {
			return net.ParseIP(ip), nil
		}
	}

	// Try to reuse a released IP first
	if len(a.released) > 0 {
		ip := a.released[0]
		a.released = a.released[1:]
		a.allocated[ip.String()] = agentID
		return ip, nil
	}

	// Allocate next IP
	if !a.subnet.Contains(a.nextIP) {
		return nil, fmt.Errorf("IP address pool exhausted for subnet %s", a.subnet)
	}

	ip := make(net.IP, len(a.nextIP))
	copy(ip, a.nextIP)

	a.allocated[ip.String()] = agentID

	// Increment for next allocation
	a.nextIP = incrementIP(a.nextIP)

	return ip, nil
}

// Release marks an IP address as available for reuse.
func (a *IPAllocator) Release(ip net.IP) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if ip == nil {
		return fmt.Errorf("IP is nil")
	}

	ipStr := ip.String()
	if _, ok := a.allocated[ipStr]; !ok {
		return fmt.Errorf("IP %s is not allocated", ipStr)
	}

	delete(a.allocated, ipStr)
	a.released = append(a.released, ip)

	return nil
}

// ReleaseByAgent releases the IP address allocated to the given agent.
func (a *IPAllocator) ReleaseByAgent(agentID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for ipStr, id := range a.allocated {
		if id == agentID {
			ip := net.ParseIP(ipStr)
			delete(a.allocated, ipStr)
			a.released = append(a.released, ip)
			return nil
		}
	}

	return fmt.Errorf("no IP allocated to agent %s", agentID)
}

// IsAllocated checks if an IP address is currently allocated.
func (a *IPAllocator) IsAllocated(ip net.IP) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	_, ok := a.allocated[ip.String()]
	return ok
}

// GetAgentIP returns the IP address allocated to the given agent.
func (a *IPAllocator) GetAgentIP(agentID string) (net.IP, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	for ipStr, id := range a.allocated {
		if id == agentID {
			return net.ParseIP(ipStr), nil
		}
	}

	return nil, fmt.Errorf("no IP allocated to agent %s", agentID)
}

// AllocatedCount returns the number of currently allocated IPs.
func (a *IPAllocator) AllocatedCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return len(a.allocated)
}

// incrementIP increments an IP address by one.
func incrementIP(ip net.IP) net.IP {
	result := make(net.IP, len(ip))
	copy(result, ip)

	for i := len(result) - 1; i >= 0; i-- {
		result[i]++
		if result[i] != 0 {
			break
		}
	}

	return result
}
