package config

import (
	"fmt"
	"net"
	"os"
	"time"

	"github.com/coral-mesh/coral/internal/constants"
)

// SchemaVersion is the configuration schema version.
const SchemaVersion = "1"

// GlobalConfig represents ~/.coral/config.yaml config file.
// The config consists of user-level settings and preferences.
type GlobalConfig struct {
	Version       string          `yaml:"version"`
	DefaultColony string          `yaml:"default_colony,omitempty" env:"CORAL_DEFAULT_COLONY"`
	Discovery     DiscoveryGlobal `yaml:"discovery"`
	AI            AIConfig        `yaml:"ai"`
	Preferences   Preferences     `yaml:"preferences"`
}

// DiscoveryGlobal contains global discovery settings.
type DiscoveryGlobal struct {
	Endpoint    string        `yaml:"endpoint" env:"CORAL_DISCOVERY_ENDPOINT"`
	Timeout     time.Duration `yaml:"timeout" env:"CORAL_DISCOVERY_TIMEOUT"`
	STUNServers []string      `yaml:"stun_servers,omitempty" env:"CORAL_STUN_SERVERS"` // STUN servers for NAT traversal
}

// AIConfig contains AI provider configuration.
type AIConfig struct {
	Provider     string    `yaml:"provider"`       // "anthropic" or "openai"
	APIKeySource string    `yaml:"api_key_source"` // "env", "keychain", "file"
	Ask          AskConfig `yaml:"ask,omitempty"`  // coral ask LLM configuration (RFD 030)
}

// AskConfig contains configuration for the coral ask command (RFD 030).
type AskConfig struct {
	DefaultModel   string                `yaml:"default_model,omitempty"`   // Primary model (e.g., "openai:gpt-4o-mini")
	FallbackModels []string              `yaml:"fallback_models,omitempty"` // Fallback models in order
	APIKeys        map[string]string     `yaml:"api_keys,omitempty"`        // Provider API keys (env:// references)
	Conversation   AskConversationConfig `yaml:"conversation,omitempty"`    // Conversation settings
	Agent          AskAgentConfig        `yaml:"agent,omitempty"`           // Agent deployment settings
}

// AskConversationConfig contains conversation management settings.
type AskConversationConfig struct {
	MaxTurns      int  `yaml:"max_turns,omitempty"`      // Maximum conversation history turns
	ContextWindow int  `yaml:"context_window,omitempty"` // Max tokens for context
	AutoPrune     bool `yaml:"auto_prune,omitempty"`     // Auto-prune old messages
}

// AskAgentConfig contains agent deployment mode settings.
type AskAgentConfig struct {
	Mode         string        `yaml:"mode,omitempty"`          // "embedded", "daemon", "ephemeral"
	DaemonSocket string        `yaml:"daemon_socket,omitempty"` // Unix socket for daemon mode
	IdleTimeout  time.Duration `yaml:"idle_timeout,omitempty"`  // Daemon idle timeout
}

// Preferences contains user preferences.
type Preferences struct {
	AutoUpdateCheck  bool `yaml:"auto_update_check"`
	TelemetryEnabled bool `yaml:"telemetry_enabled"`
}

// ColonyConfig represents ~/.coral/colonies/<colony-id>.yaml config file.
// The config consists of per-colony identity and security credentials.
type ColonyConfig struct {
	Version             string                          `yaml:"version"`
	ColonyID            string                          `yaml:"colony_id"`
	ApplicationName     string                          `yaml:"application_name"`
	Environment         string                          `yaml:"environment"`
	ColonySecret        string                          `yaml:"colony_secret"`
	WireGuard           WireGuardConfig                 `yaml:"wireguard"`
	Services            ServicesConfig                  `yaml:"services"`
	StoragePath         string                          `yaml:"storage_path"`
	Discovery           DiscoveryColony                 `yaml:"discovery"`
	MCP                 MCPConfig                       `yaml:"mcp,omitempty"`
	PublicEndpoint      PublicEndpointConfig            `yaml:"public_endpoint,omitempty"` // RFD 031
	Remote              RemoteConfig                    `yaml:"remote,omitempty"`          // Client-side remote connection config
	Beyla               BeylaPollerConfig               `yaml:"beyla,omitempty"`
	SystemMetrics       SystemMetricsPollerConfig       `yaml:"system_metrics,omitempty"`       // RFD 071
	ContinuousProfiling ContinuousProfilingPollerConfig `yaml:"continuous_profiling,omitempty"` // RFD 072
	FunctionRegistry    FunctionRegistryConfig          `yaml:"function_registry,omitempty"`    // RFD 063
	Ask                 *AskConfig                      `yaml:"ask,omitempty"`                  // Per-colony ask overrides (RFD 030)
	CreatedAt           time.Time                       `yaml:"created_at"`
	CreatedBy           string                          `yaml:"created_by"`
	LastUsed            time.Time                       `yaml:"last_used,omitempty"`
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
// Multiple endpoints can be specified (comma-separated):
//
//	CORAL_PUBLIC_ENDPOINT=192.168.5.2:9000,10.0.0.5:9000,colony.example.com:9000
//
// The mesh IPs (mesh_ipv4, mesh_ipv6) are only used INSIDE the tunnel for
// service communication, not for establishing the initial connection.
type WireGuardConfig struct {
	PrivateKey          string   `yaml:"private_key"`
	PublicKey           string   `yaml:"public_key"`
	Port                int      `yaml:"port"`                           // WireGuard UDP listen port
	PublicEndpoints     []string `yaml:"public_endpoints,omitempty"`     // Public endpoints for agent connections
	InterfaceName       string   `yaml:"interface_name,omitempty"`       // Interface name (e.g., wg0)
	MeshIPv4            string   `yaml:"mesh_ipv4,omitempty"`            // IPv4 address inside tunnel
	MeshIPv6            string   `yaml:"mesh_ipv6,omitempty"`            // IPv6 address inside tunnel
	MeshNetworkIPv4     string   `yaml:"mesh_network_ipv4,omitempty"`    // IPv4 network CIDR
	MeshNetworkIPv6     string   `yaml:"mesh_network_ipv6,omitempty"`    // IPv6 network CIDR
	MTU                 int      `yaml:"mtu,omitempty"`                  // Interface MTU
	PersistentKeepalive int      `yaml:"persistent_keepalive,omitempty"` // Keepalive interval (seconds)
}

// DiscoveryColony contains colony-specific discovery settings.
type DiscoveryColony struct {
	Enabled          bool          `yaml:"enabled"`
	MeshID           string        `yaml:"mesh_id"` // Should match colony_id
	AutoRegister     bool          `yaml:"auto_register"`
	RegisterInterval time.Duration `yaml:"register_interval"`
	STUNServers      []string      `yaml:"stun_servers,omitempty"` // STUN servers for NAT traversal
}

// MCPConfig contains MCP server configuration (RFD 004).
type MCPConfig struct {
	// Disabled controls whether the MCP server is enabled.
	// Default: false (MCP server is enabled by default).
	Disabled bool `yaml:"disabled,omitempty"`

	// EnabledTools optionally restricts which tools are available.
	// If empty, all tools are enabled.
	EnabledTools []string `yaml:"enabled_tools,omitempty"`

	// Security settings.
	Security MCPSecurityConfig `yaml:"security,omitempty"`
}

// MCPSecurityConfig contains MCP security settings.
type MCPSecurityConfig struct {
	// RequireRBACForActions requires RBAC checks for action tools.
	// (exec, shell, start_ebpf).
	RequireRBACForActions bool `yaml:"require_rbac_for_actions,omitempty"`

	// AuditEnabled enables auditing of all MCP tool calls.
	AuditEnabled bool `yaml:"audit_enabled,omitempty"`
}

// RemoteConfig contains client-side connection settings for remote colonies.
// This enables CLI access to colonies running on remote hosts without WireGuard.
// Similar to kubectl's cluster configuration in kubeconfig.
type RemoteConfig struct {
	// Endpoint is the remote colony's public HTTPS endpoint URL.
	// Example: "https://colony.example.com:8443"
	Endpoint string `yaml:"endpoint,omitempty" env:"CORAL_COLONY_ENDPOINT"`

	// CertificateAuthority is the path to the CA certificate file for TLS verification.
	// Example: "~/.coral/ca/production-ca.crt"
	CertificateAuthority string `yaml:"certificate_authority,omitempty" env:"CORAL_CA_FILE"`

	// CertificateAuthorityData is the base64-encoded CA certificate for TLS verification.
	// Takes precedence over CertificateAuthority if both are set.
	CertificateAuthorityData string `yaml:"certificate_authority_data,omitempty"`

	// InsecureSkipTLSVerify disables TLS certificate verification.
	// WARNING: Only use for testing. Never use in production.
	InsecureSkipTLSVerify bool `yaml:"insecure_skip_tls_verify,omitempty" env:"CORAL_INSECURE"`
}

// PublicEndpointConfig contains optional public HTTPS endpoint configuration (RFD 031).
// When enabled, Colony exposes a public HTTPS endpoint in addition to the WireGuard mesh.
// This enables CLI access without coral proxy, external integrations, and MCP SSE transport.
type PublicEndpointConfig struct {
	// Enabled controls whether the public endpoint is active.
	// Default: false (opt-in for security).
	Enabled bool `yaml:"enabled"`

	// Host is the address to bind the public endpoint to.
	// Default: "127.0.0.1" (localhost-only for security).
	// Set to "0.0.0.0" to expose on all interfaces (requires TLS).
	Host string `yaml:"host,omitempty"`

	// Port is the port for the public HTTPS endpoint.
	// Default: 8443.
	Port int `yaml:"port,omitempty"`

	// TLS contains TLS certificate configuration.
	// Required when Host is not "127.0.0.1" or "localhost".
	TLS TLSConfig `yaml:"tls,omitempty"`

	// MCP contains MCP-over-SSE configuration for AI assistant integration.
	MCP PublicMCPConfig `yaml:"mcp,omitempty"`

	// Auth contains authentication configuration for the public endpoint.
	Auth PublicAuthConfig `yaml:"auth,omitempty"`
}

// TLSConfig contains TLS certificate configuration.
type TLSConfig struct {
	// CertFile is the path to the TLS certificate file.
	CertFile string `yaml:"cert,omitempty"`

	// KeyFile is the path to the TLS private key file.
	KeyFile string `yaml:"key,omitempty"`
}

// PublicMCPConfig contains MCP-over-SSE configuration for the public endpoint.
type PublicMCPConfig struct {
	// Enabled controls whether MCP SSE endpoint is active.
	Enabled bool `yaml:"enabled"`

	// Transport is the MCP transport type (currently only "sse" supported).
	Transport string `yaml:"transport,omitempty"`

	// Path is the URL path for the MCP SSE endpoint.
	// Default: "/mcp/sse".
	Path string `yaml:"path,omitempty"`
}

// PublicAuthConfig contains authentication configuration for the public endpoint.
type PublicAuthConfig struct {
	// Require controls whether authentication is required.
	// Default: true (anonymous access is not allowed).
	Require bool `yaml:"require"`

	// TokensFile is the path to the API tokens file.
	// Default: tokens.yaml in the colony config directory.
	TokensFile string `yaml:"tokens_file,omitempty"`
}

// BeylaPollerConfig contains Beyla metrics/traces collection configuration (RFD 032, RFD 036).
type BeylaPollerConfig struct {
	// PollInterval is how often to poll agents for Beyla data (seconds).
	PollInterval int `yaml:"poll_interval,omitempty"`

	// Retention settings for different data types.
	Retention BeylaRetentionConfig `yaml:"retention,omitempty"`
}

// FunctionRegistryConfig contains function discovery configuration (RFD 063).
type FunctionRegistryConfig struct {
	// PollInterval is how often to poll agents for function metadata (seconds).
	// Default: 300 (5 minutes).
	PollInterval int `yaml:"poll_interval,omitempty"`

	// Disabled controls whether function discovery is enabled.
	// Default: false (function discovery is enabled).
	Disabled bool `yaml:"disabled,omitempty"`
}

// BeylaRetentionConfig contains retention periods for Beyla data.
type BeylaRetentionConfig struct {
	// HTTPDays is retention period for HTTP metrics (days).
	HTTPDays int `yaml:"http_days,omitempty"`

	// GRPCDays is retention period for gRPC metrics (days).
	GRPCDays int `yaml:"grpc_days,omitempty"`

	// SQLDays is retention period for SQL metrics (days).
	SQLDays int `yaml:"sql_days,omitempty"`

	// TracesDays is retention period for distributed traces (days) (RFD 036).
	TracesDays int `yaml:"traces_days,omitempty"`
}

// SystemMetricsPollerConfig contains system metrics poller configuration (RFD 071).
// This is for colony-side aggregation, distinct from agent-side SystemMetricsConfig.
type SystemMetricsPollerConfig struct {
	// PollInterval is how often to poll agents for system metrics (seconds).
	// Default: 60 (1 minute).
	PollInterval int `yaml:"poll_interval,omitempty"`

	// RetentionDays is how long to keep aggregated system metrics summaries (days).
	// Default: 30 days.
	RetentionDays int `yaml:"retention_days,omitempty"`
}

// ContinuousProfilingPollerConfig contains continuous CPU profiling configuration (RFD 072).
type ContinuousProfilingPollerConfig struct {
	// PollInterval is how often to poll agents for CPU profile samples (seconds).
	// Default: 30 seconds.
	PollInterval int `yaml:"poll_interval,omitempty"`

	// RetentionDays is how long to keep aggregated CPU profile summaries (days).
	// Default: 30 days.
	RetentionDays int `yaml:"retention_days,omitempty"`
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

// BeylaConfig contains Beyla integration configuration (RFD 032).
type BeylaConfig struct {
	Disabled bool `yaml:"disabled"`

	// Discovery configuration.
	Discovery BeylaDiscoveryConfig `yaml:"discovery"`

	// Protocol-specific configuration.
	Protocols BeylaProtocolsConfig `yaml:"protocols"`

	// Attributes to add to all metrics/traces.
	Attributes map[string]string `yaml:"attributes,omitempty"`

	// Performance tuning.
	Sampling BeylaSamplingConfig `yaml:"sampling,omitempty"`

	// Resource limits.
	Limits BeylaLimitsConfig `yaml:"limits,omitempty"`

	// OTLP endpoint for Beyla output.
	OTLPEndpoint string `yaml:"otlp_endpoint,omitempty"`
}

// BeylaDiscoveryConfig specifies which processes to instrument.
type BeylaDiscoveryConfig struct {
	Services []BeylaServiceConfig `yaml:"services,omitempty"`
}

// BeylaServiceConfig defines a service to instrument.
type BeylaServiceConfig struct {
	Name         string            `yaml:"name"`
	OpenPort     int               `yaml:"open_port,omitempty"`
	K8sPodName   string            `yaml:"k8s_pod_name,omitempty"`
	K8sNamespace string            `yaml:"k8s_namespace,omitempty"`
	K8sPodLabel  map[string]string `yaml:"k8s_pod_label,omitempty"`
}

// BeylaProtocolsConfig enables/disables specific protocols.
type BeylaProtocolsConfig struct {
	HTTP  BeylaHTTPConfig  `yaml:"http,omitempty"`
	GRPC  BeylaGRPCConfig  `yaml:"grpc,omitempty"`
	SQL   BeylaSQLConfig   `yaml:"sql,omitempty"`
	Kafka BeylaKafkaConfig `yaml:"kafka,omitempty"`
	Redis BeylaRedisConfig `yaml:"redis,omitempty"`
}

// BeylaHTTPConfig contains HTTP-specific configuration.
type BeylaHTTPConfig struct {
	Enabled        bool     `yaml:"enabled"`
	CaptureHeaders bool     `yaml:"capture_headers,omitempty"`
	RoutePatterns  []string `yaml:"route_patterns,omitempty"`
}

// BeylaGRPCConfig contains gRPC-specific configuration.
type BeylaGRPCConfig struct {
	Enabled bool `yaml:"enabled"`
}

// BeylaSQLConfig contains SQL-specific configuration.
type BeylaSQLConfig struct {
	Enabled          bool `yaml:"enabled"`
	ObfuscateQueries bool `yaml:"obfuscate_queries,omitempty"`
}

// BeylaKafkaConfig contains Kafka-specific configuration.
type BeylaKafkaConfig struct {
	Enabled bool `yaml:"enabled"`
}

// BeylaRedisConfig contains Redis-specific configuration.
type BeylaRedisConfig struct {
	Enabled bool `yaml:"enabled"`
}

// BeylaSamplingConfig contains sampling configuration.
type BeylaSamplingConfig struct {
	Rate float64 `yaml:"rate,omitempty"` // 0.0-1.0, default 1.0
}

// BeylaLimitsConfig contains resource limits.
type BeylaLimitsConfig struct {
	MaxTracedConnections int `yaml:"max_traced_connections,omitempty"`
	RingBufferSize       int `yaml:"ring_buffer_size,omitempty"`
}

// SystemMetricsConfig configures host system metrics collection (RFD 071).
type SystemMetricsConfig struct {
	Disabled       bool          `yaml:"disabled" env:"CORAL_SYSTEM_METRICS_DISABLED"`
	Interval       time.Duration `yaml:"interval,omitempty" env:"CORAL_SYSTEM_METRICS_INTERVAL"`               // Collection interval (default: 15s)
	Retention      time.Duration `yaml:"retention,omitempty" env:"CORAL_SYSTEM_METRICS_RETENTION"`             // Local retention (default: 1h)
	CPUEnabled     bool          `yaml:"cpu_enabled,omitempty" env:"CORAL_SYSTEM_METRICS_CPU_ENABLED"`         // Collect CPU metrics
	MemoryEnabled  bool          `yaml:"memory_enabled,omitempty" env:"CORAL_SYSTEM_METRICS_MEMORY_ENABLED"`   // Collect memory metrics
	DiskEnabled    bool          `yaml:"disk_enabled,omitempty" env:"CORAL_SYSTEM_METRICS_DISK_ENABLED"`       // Collect disk metrics
	NetworkEnabled bool          `yaml:"network_enabled,omitempty" env:"CORAL_SYSTEM_METRICS_NETWORK_ENABLED"` // Collect network metrics
}

// ContinuousProfilingConfig configures continuous CPU profiling (RFD 072).
type ContinuousProfilingConfig struct {
	Disabled bool               `yaml:"disabled,omitempty" env:"CORAL_CPU_PROFILING_DISABLED"` // Master disable switch (default: false, meaning enabled)
	CPU      CPUProfilingConfig `yaml:"cpu,omitempty"`                                         // CPU profiling configuration
}

// CPUProfilingConfig contains CPU profiling specific settings.
type CPUProfilingConfig struct {
	Disabled          bool          `yaml:"disabled,omitempty" env:"CORAL_CPU_PROFILING_DISABLED"`                     // CPU profiling disabled (default: false, meaning enabled)
	FrequencyHz       int           `yaml:"frequency_hz,omitempty" env:"CORAL_CPU_PROFILING_FREQUENCY_HZ"`             // Sampling frequency (default: 19Hz)
	Interval          time.Duration `yaml:"interval,omitempty" env:"CORAL_CPU_PROFILING_INTERVAL"`                     // Collection interval (default: 15s)
	Retention         time.Duration `yaml:"retention,omitempty" env:"CORAL_CPU_PROFILING_RETENTION"`                   // Local sample retention (default: 1h)
	MetadataRetention time.Duration `yaml:"metadata_retention,omitempty" env:"CORAL_CPU_PROFILING_METADATA_RETENTION"` // Binary metadata retention (default: 7d)
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

// AgentConfig represents agent-specific configuration (RFD 025).
type AgentConfig struct {
	Agent struct {
		Runtime string `yaml:"runtime" env:"CORAL_AGENT_RUNTIME"` // auto, native, docker, kubernetes
		Colony  struct {
			ID           string `yaml:"id" env:"CORAL_COLONY_ID"`
			AutoDiscover bool   `yaml:"auto_discover" env:"CORAL_AUTO_DISCOVER"`
		} `yaml:"colony"`
		NAT struct {
			STUNServers []string `yaml:"stun_servers,omitempty" env:"CORAL_STUN_SERVERS"` // STUN servers for NAT traversal
			EnableRelay bool     `yaml:"enable_relay,omitempty" env:"CORAL_ENABLE_RELAY"` // Enable relay fallback
		} `yaml:"nat,omitempty"`
	} `yaml:"agent"`
	Telemetry struct {
		Disabled              bool   `yaml:"disabled" env:"CORAL_TELEMETRY_DISABLED"`
		GRPCEndpoint          string `yaml:"grpc_endpoint,omitempty" env:"CORAL_OTLP_GRPC_ENDPOINT"`
		HTTPEndpoint          string `yaml:"http_endpoint,omitempty" env:"CORAL_OTLP_HTTP_ENDPOINT"`
		DatabasePath          string `yaml:"database_path,omitempty" env:"CORAL_TELEMETRY_DATABASE_PATH"`
		StorageRetentionHours int    `yaml:"storage_retention_hours,omitempty" env:"CORAL_TELEMETRY_RETENTION_HOURS"`
		Filters               struct {
			AlwaysCaptureErrors    bool    `yaml:"always_capture_errors,omitempty" env:"CORAL_ALWAYS_CAPTURE_ERRORS"`
			HighLatencyThresholdMs float64 `yaml:"high_latency_threshold_ms,omitempty" env:"CORAL_HIGH_LATENCY_THRESHOLD_MS"`
			SampleRate             float64 `yaml:"sample_rate,omitempty" env:"CORAL_SAMPLE_RATE"`
		} `yaml:"filters,omitempty"`
	} `yaml:"telemetry,omitempty"`
	Services []struct {
		Name           string `yaml:"name"`
		Port           int    `yaml:"port"`
		HealthEndpoint string `yaml:"health_endpoint,omitempty"`
		Type           string `yaml:"type,omitempty"`
	} `yaml:"services"`
	Beyla               BeylaConfig               `yaml:"beyla,omitempty"`
	SystemMetrics       SystemMetricsConfig       `yaml:"system_metrics,omitempty"`
	Debug               DebugConfig               `yaml:"debug,omitempty"`
	ContinuousProfiling ContinuousProfilingConfig `yaml:"continuous_profiling,omitempty"` // RFD 072
}

// DebugConfig contains debug session configuration (RFD 061).
type DebugConfig struct {
	Enabled bool `yaml:"enabled"`

	// SDK communication
	SDKAPI struct {
		Timeout       time.Duration `yaml:"timeout"`
		RetryAttempts int           `yaml:"retry_attempts"`
	} `yaml:"sdk_api"`

	// Function discovery configuration (RFD 065)
	Discovery struct {
		// EnableSDK enables SDK-based discovery (Priority 1)
		EnableSDK bool `yaml:"enable_sdk"`

		// EnablePprof enables pprof-based discovery (Priority 2, not yet implemented)
		EnablePprof bool `yaml:"enable_pprof"`

		// EnableBinaryScanning enables binary DWARF scanning (Priority 3)
		EnableBinaryScanning bool `yaml:"enable_binary_scanning"`

		// BinaryScanning contains binary scanning configuration
		BinaryScanning struct {
			// AccessMethod specifies how to access container binaries
			// Options: "direct", "nsenter", "cri"
			AccessMethod string `yaml:"access_method"`

			// CacheEnabled enables caching of parsed function metadata
			CacheEnabled bool `yaml:"cache_enabled"`

			// CacheTTL is the time-to-live for cached entries
			CacheTTL time.Duration `yaml:"cache_ttl"`

			// MaxCachedBinaries limits the number of cached binaries
			MaxCachedBinaries int `yaml:"max_cached_binaries"`

			// TempDir is the directory for storing temporary binary copies
			TempDir string `yaml:"temp_dir"`
		} `yaml:"binary_scanning"`
	} `yaml:"discovery"`

	// Uprobe limits (safety)
	Limits struct {
		MaxConcurrentSessions int           `yaml:"max_concurrent_sessions"`
		MaxSessionDuration    time.Duration `yaml:"max_session_duration"`
		MaxEventsPerSecond    int           `yaml:"max_events_per_second"`
		MaxMemoryMB           int           `yaml:"max_memory_mb"`
	} `yaml:"limits"`

	// BPF program settings
	BPF struct {
		MapSize         int `yaml:"map_size"`
		PerfBufferPages int `yaml:"perf_buffer_pages"`
	} `yaml:"bpf"`
}

// ResolveMeshSubnet resolves the mesh subnet to use, with the following precedence:
// 1. Environment variable CORAL_MESH_SUBNET
// 2. Config file value (cfg.WireGuard.MeshNetworkIPv4)
// 3. Default from constants
//
// Returns the resolved subnet and the colony's IP address within that subnet.
func ResolveMeshSubnet(cfg *ColonyConfig) (subnet string, colonyIP string, err error) {
	// Check environment variable first
	if envSubnet := os.Getenv("CORAL_MESH_SUBNET"); envSubnet != "" {
		ipNet, err := ValidateMeshSubnet(envSubnet)
		if err != nil {
			return "", "", fmt.Errorf("invalid CORAL_MESH_SUBNET environment variable: %w", err)
		}

		// Calculate colony IP (.1 in the subnet)
		colonyIP := calculateColonyIP(ipNet)

		return envSubnet, colonyIP, nil
	}

	// Use config value if set
	if cfg.WireGuard.MeshNetworkIPv4 != "" {
		ipNet, err := ValidateMeshSubnet(cfg.WireGuard.MeshNetworkIPv4)
		if err != nil {
			return "", "", fmt.Errorf("invalid mesh_network_ipv4 in config: %w", err)
		}

		// Use configured colony IP if set, otherwise calculate it
		colonyIP := cfg.WireGuard.MeshIPv4
		if colonyIP == "" {
			colonyIP = calculateColonyIP(ipNet)
		}

		return cfg.WireGuard.MeshNetworkIPv4, colonyIP, nil
	}

	// Fall back to default
	return constants.DefaultColonyMeshIPv4Subnet, constants.DefaultColonyMeshIPv4, nil
}

// calculateColonyIP calculates the colony IP address for a given subnet.
// The colony always gets the .1 address (first usable IP after network address).
func calculateColonyIP(subnet *net.IPNet) string {
	// Make a copy of the network IP
	ip := make(net.IP, len(subnet.IP))
	copy(ip, subnet.IP)

	// Increment to .1 (colony address)
	ip[len(ip)-1]++

	return ip.String()
}
