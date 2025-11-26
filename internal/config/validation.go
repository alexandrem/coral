package config

import (
	"fmt"
	"net"
)

// ValidateMeshSubnet validates a mesh subnet CIDR and returns the parsed network.
// It ensures the subnet is valid and has sufficient address space for a mesh network.
func ValidateMeshSubnet(subnet string) (*net.IPNet, error) {
	if subnet == "" {
		return nil, fmt.Errorf("mesh subnet cannot be empty")
	}

	// Parse CIDR
	ip, ipNet, err := net.ParseCIDR(subnet)
	if err != nil {
		return nil, fmt.Errorf("invalid mesh subnet CIDR %q: %w", subnet, err)
	}

	// Ensure it's IPv4 (IPv6 support is separate)
	if ip.To4() == nil {
		return nil, fmt.Errorf("mesh subnet %q must be IPv4", subnet)
	}

	// Check subnet size - we need at least a /24 for meaningful mesh networks
	// (colony at .1, plus at least a few agents)
	ones, bits := ipNet.Mask.Size()
	if ones > 24 {
		return nil, fmt.Errorf("mesh subnet %q is too small (/%d), minimum is /24", subnet, ones)
	}

	// Warn about common conflicts (this is informational, not blocking)
	// We don't block these because users might intentionally want to use them
	// in isolated environments.
	if bits != 32 {
		return nil, fmt.Errorf("mesh subnet %q must be 32-bit IPv4", subnet)
	}

	return ipNet, nil
}

// RecommendedMeshSubnets returns a list of recommended mesh subnet CIDRs.
// These are subnets that are unlikely to conflict with existing networks.
func RecommendedMeshSubnets() []string {
	return []string{
		"100.64.0.0/10",  // CGNAT (RFC 6598) - Recommended, least likely to conflict
		"10.42.0.0/16",   // RFC 1918 - Legacy, may conflict with corporate networks
		"172.16.0.0/12",  // RFC 1918 - May conflict with Docker and cloud networks
		"192.168.0.0/16", // RFC 1918 - May conflict with home routers
	}
}

// IsCGNATSubnet checks if the given subnet is within the CGNAT address space (100.64.0.0/10).
func IsCGNATSubnet(subnet string) bool {
	_, ipNet, err := net.ParseCIDR(subnet)
	if err != nil {
		return false
	}

	// CGNAT range: 100.64.0.0/10
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")

	// Check if the subnet is within CGNAT range
	return cgnat.Contains(ipNet.IP)
}

// SubnetCapacity returns the number of usable IP addresses in a subnet.
// The first address (.0) is the network address and .1 is reserved for the colony.
func SubnetCapacity(subnet string) (int, error) {
	_, ipNet, err := net.ParseCIDR(subnet)
	if err != nil {
		return 0, err
	}

	ones, bits := ipNet.Mask.Size()
	if bits != 32 {
		return 0, fmt.Errorf("only IPv4 subnets are supported")
	}

	// Total addresses in subnet
	total := 1 << (bits - ones)

	// Subtract network address (.0) and colony address (.1)
	usable := total - 2

	if usable < 0 {
		return 0, nil
	}

	return usable, nil
}
