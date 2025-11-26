// Package constants defines shared configuration constants.
package constants

var (
	ConfigFile = "config.yaml"

	DefaultBinaryPath = "/usr/local/bin/coral"

	DefaultDir = ".coral"

	DefaultColonyDatabasePath = DefaultDir + "/" + "colony.duckdb"

	// DefaultColonyMeshIPv4 is default colony mesh IPv4 address.
	DefaultColonyMeshIPv4 = "100.64.0.1"

	// DefaultColonyMeshIPv4Subnet is the default IPv4 subnet for mesh network.
	// Uses CGNAT address space (RFC 6598) to avoid conflicts with RFC 1918 addresses.
	DefaultColonyMeshIPv4Subnet = "100.64.0.0/10"

	// DefaultColonyMeshIPv6 is default colony mesh IPv6 address.
	DefaultColonyMeshIPv6 = "fd42::1"

	// DefaultColonyMeshIPv6Subnet is the default IPv6 subnet for mesh network.
	DefaultColonyMeshIPv6Subnet = "fd42::/48"

	DefaultDiscoveryEndpoint = "http://localhost:8080"

	// DefaultSTUNServer is the default STUN server for NAT traversal.
	DefaultSTUNServer = "stun.cloudflare.com:3478"

	// DefaultWireGuardPort is the default WireGuard peering port for colonies.
	DefaultWireGuardPort = 41580

	DefaultWireGuardKeepaliveSeconds = 25

	// DefaultWireGuardMTU is default MTU for WireGuard (1500 - 80 overhead).
	DefaultWireGuardMTU = 1420

	DefaultDashboardPort = 3000
)
