package config

import (
	"time"

	"github.com/coral-io/coral/internal/constants"
)

// SchemaVersion is the configuration schema version.
const SchemaVersion = "1"

// GlobalConfig represents ~/.coral/config.yaml config file.
// The config consists of user-level settings and preferences.
type GlobalConfig struct {
	Version       string          `yaml:"version"`
	DefaultColony string          `yaml:"default_colony,omitempty"`
	Discovery     DiscoveryGlobal `yaml:"discovery"`
	AI            AIConfig        `yaml:"ai"`
	Preferences   Preferences     `yaml:"preferences"`
}

// DiscoveryGlobal contains global discovery settings.
type DiscoveryGlobal struct {
	Endpoint string        `yaml:"endpoint"`
	Timeout  time.Duration `yaml:"timeout"`
}

// AIConfig contains AI provider configuration.
type AIConfig struct {
	Provider     string `yaml:"provider"`       // "anthropic" or "openai"
	APIKeySource string `yaml:"api_key_source"` // "env", "keychain", "file"
}

// Preferences contains user preferences.
type Preferences struct {
	AutoUpdateCheck  bool `yaml:"auto_update_check"`
	TelemetryEnabled bool `yaml:"telemetry_enabled"`
}

// ColonyConfig represents ~/.coral/colonies/<colony-id>.yaml config file.
// The config consists of per-colony identity and security credentials.
type ColonyConfig struct {
	Version         string          `yaml:"version"`
	ColonyID        string          `yaml:"colony_id"`
	ApplicationName string          `yaml:"application_name"`
	Environment     string          `yaml:"environment"`
	ColonySecret    string          `yaml:"colony_secret"`
	WireGuard       WireGuardConfig `yaml:"wireguard"`
	Services        ServicesConfig  `yaml:"services"`
	StoragePath     string          `yaml:"storage_path"`
	Discovery       DiscoveryColony `yaml:"discovery"`
	CreatedAt       time.Time       `yaml:"created_at"`
	CreatedBy       string          `yaml:"created_by"`
	LastUsed        time.Time       `yaml:"last_used,omitempty"`
}

// ServicesConfig contains service port configuration.
type ServicesConfig struct {
	ConnectPort   int `yaml:"connect_port"`
	DashboardPort int `yaml:"dashboard_port"`
}

// WireGuardConfig contains WireGuard mesh configuration.
//
// For production deployments, you must set the CORAL_PUBLIC_ENDPOINT environment
// variable to your colony's publicly reachable address. This tells agents where
// to establish the WireGuard tunnel.
//
// Example production setup:
//
//	CORAL_PUBLIC_ENDPOINT=colony.example.com:41580 coral colony start
//
// The mesh IPs (mesh_ipv4, mesh_ipv6) are only used INSIDE the tunnel for
// service communication, not for establishing the initial connection.
type WireGuardConfig struct {
	PrivateKey          string `yaml:"private_key"`
	PublicKey           string `yaml:"public_key"`
	Port                int    `yaml:"port"`                           // WireGuard UDP listen port
	InterfaceName       string `yaml:"interface_name,omitempty"`       // Interface name (e.g., wg0)
	MeshIPv4            string `yaml:"mesh_ipv4,omitempty"`            // IPv4 address inside tunnel
	MeshIPv6            string `yaml:"mesh_ipv6,omitempty"`            // IPv6 address inside tunnel
	MeshNetworkIPv4     string `yaml:"mesh_network_ipv4,omitempty"`    // IPv4 network CIDR
	MeshNetworkIPv6     string `yaml:"mesh_network_ipv6,omitempty"`    // IPv6 network CIDR
	MTU                 int    `yaml:"mtu,omitempty"`                  // Interface MTU
	PersistentKeepalive int    `yaml:"persistent_keepalive,omitempty"` // Keepalive interval (seconds)
}

// DiscoveryColony contains colony-specific discovery settings.
type DiscoveryColony struct {
	Enabled          bool          `yaml:"enabled"`
	MeshID           string        `yaml:"mesh_id"` // Should match colony_id
	AutoRegister     bool          `yaml:"auto_register"`
	RegisterInterval time.Duration `yaml:"register_interval"`
}

// ProjectConfig represents <project>/.coral/config.yaml config file.
// The config consists of project-local configuration that links to a colony.
type ProjectConfig struct {
	Version   string          `yaml:"version"`
	ColonyID  string          `yaml:"colony_id"`
	Dashboard DashboardConfig `yaml:"dashboard,omitempty"`
	Storage   ProjectStorage  `yaml:"storage,omitempty"`
}

// DashboardConfig contains dashboard settings.
type DashboardConfig struct {
	Port    int  `yaml:"port"`
	Enabled bool `yaml:"enabled"`
}

// ProjectStorage contains project-specific storage settings.
type ProjectStorage struct {
	Path string `yaml:"path"` // Relative to project root
}

// ResolvedConfig is the final merged configuration after resolution.
type ResolvedConfig struct {
	ColonyID        string
	ColonySecret    string
	ApplicationName string
	Environment     string
	WireGuard       WireGuardConfig
	StoragePath     string
	DiscoveryURL    string
	Dashboard       DashboardConfig
}

// DefaultGlobalConfig returns a global config with sensible defaults.
func DefaultGlobalConfig() *GlobalConfig {
	return &GlobalConfig{
		Version: SchemaVersion,
		Discovery: DiscoveryGlobal{
			Endpoint: constants.DefaultDiscoveryEndpoint,
			Timeout:  10 * time.Second,
		},
		AI: AIConfig{
			Provider:     "anthropic",
			APIKeySource: "env",
		},
		Preferences: Preferences{
			AutoUpdateCheck:  true,
			TelemetryEnabled: false,
		},
	}
}

// DefaultColonyConfig returns a colony config template.
func DefaultColonyConfig(colonyID, appName, env string) *ColonyConfig {
	return &ColonyConfig{
		Version:         SchemaVersion,
		ColonyID:        colonyID,
		ApplicationName: appName,
		Environment:     env,
		WireGuard: WireGuardConfig{
			Port:                constants.DefaultWireGuardPort,
			MeshIPv4:            constants.DefaultColonyMeshIPv4,
			MeshIPv6:            constants.DefaultColonyMeshIPv6,
			MeshNetworkIPv4:     constants.DefaultColonyMeshIPv4Subnet,
			MeshNetworkIPv6:     constants.DefaultColonyMeshIPv6Subnet,
			MTU:                 constants.DefaultWireGuardMTU,
			PersistentKeepalive: constants.DefaultWireGuardKeepaliveSeconds,
		},
		Discovery: DiscoveryColony{
			Enabled:          true,
			MeshID:           colonyID, // mesh_id = colony_id
			AutoRegister:     true,
			RegisterInterval: 60 * time.Second,
		},
		CreatedAt: time.Now(),
	}
}

// DefaultProjectConfig returns a project config template.
func DefaultProjectConfig(colonyID string) *ProjectConfig {
	return &ProjectConfig{
		Version:  SchemaVersion,
		ColonyID: colonyID,
		Dashboard: DashboardConfig{
			Port:    constants.DefaultDashboardPort,
			Enabled: true,
		},
		Storage: ProjectStorage{
			Path: constants.DefaultDir,
		},
	}
}
