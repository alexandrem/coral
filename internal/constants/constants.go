package constants

var (
	ConfigFile = "config.yaml"

	DefaultBinaryPath = "/usr/local/bin/coral"

	DefaultDir = ".coral"

	DefaultColonyDatabasePath = DefaultDir + "/" + "colony.duckdb"

	DefaultDiscoveryEndpoint = "http://localhost:8080"

	// DefaultWireGuardPort is the default wireguard peering port.
	DefaultWireGuardPort = 41580

	DefaultDashboardPort = 3000
)
