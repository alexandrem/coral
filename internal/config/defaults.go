package config

import (
	"fmt"
	"time"

	"github.com/coral-mesh/coral/internal/constants"
)

// DefaultGlobalConfig returns a global config with sensible defaults.
func DefaultGlobalConfig() *GlobalConfig {
	return &GlobalConfig{
		Version: SchemaVersion,
		Discovery: DiscoveryGlobal{
			Endpoint:    constants.DefaultDiscoveryEndpoint,
			Timeout:     constants.DefaultDiscoveryTimeout,
			STUNServers: []string{constants.DefaultSTUNServer},
		},
		AI: AIConfig{
			Provider:     "anthropic",
			APIKeySource: "env",
			Ask: AskConfig{
				DefaultModel:   constants.DefaultAskModel,
				FallbackModels: []string{"anthropic:claude-3-5-sonnet-20241022"},
				APIKeys:        make(map[string]string),
				Conversation: AskConversationConfig{
					MaxTurns:      constants.DefaultAskMaxTurns,
					ContextWindow: constants.DefaultAskContextWindow,
					AutoPrune:     true,
				},
				Agent: AskAgentConfig{
					Mode:         constants.DefaultAskAgentMode,
					DaemonSocket: constants.DefaultAskDaemonSocket,
					IdleTimeout:  constants.DefaultAskIdleTimeout,
				},
			},
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
			MeshID:           colonyID, // mesh_id = colony_id
			AutoRegister:     true,
			RegisterInterval: constants.DefaultRegisterInterval,
			STUNServers:      []string{constants.DefaultSTUNServer},
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

// DefaultAgentConfig returns an agent config with sensible defaults.
func DefaultAgentConfig() *AgentConfig {
	cfg := &AgentConfig{}

	// Agent defaults
	cfg.Agent.Runtime = "auto"
	cfg.Agent.Colony.AutoDiscover = true
	cfg.Agent.NAT.STUNServers = []string{constants.DefaultSTUNServer}
	cfg.Agent.HeartbeatInterval = constants.DefaultHeartbeatInterval

	// Telemetry defaults
	cfg.Telemetry.Disabled = false
	cfg.Telemetry.GRPCEndpoint = formatEndpoint("0.0.0.0", constants.DefaultOTLPGRPCPort)
	cfg.Telemetry.HTTPEndpoint = formatEndpoint("0.0.0.0", constants.DefaultOTLPHTTPPort)
	cfg.Telemetry.StorageRetentionHours = int(constants.DefaultTelemetryRetention.Hours())
	cfg.Telemetry.Filters.AlwaysCaptureErrors = true
	cfg.Telemetry.Filters.HighLatencyThresholdMs = constants.DefaultHighLatencyThresholdMs
	cfg.Telemetry.Filters.SampleRate = constants.DefaultSampleRate

	// Beyla defaults
	cfg.Beyla.Disabled = false
	cfg.Beyla.OTLPEndpoint = formatEndpoint("127.0.0.1", constants.DefaultOTLPGRPCPort)
	cfg.Beyla.Protocols.HTTP.Enabled = true
	cfg.Beyla.Protocols.GRPC.Enabled = true
	cfg.Beyla.Protocols.SQL.Enabled = true
	cfg.Beyla.Sampling.Rate = constants.DefaultBeylaSampleRate

	// Debug defaults
	cfg.Debug.Enabled = true
	cfg.Debug.SDKAPI.Timeout = constants.DefaultSDKAPITimeout
	cfg.Debug.SDKAPI.RetryAttempts = constants.DefaultSDKAPIRetryAttempts
	cfg.Debug.Discovery.EnableSDK = true
	cfg.Debug.Discovery.EnablePprof = false // Not yet implemented
	cfg.Debug.Discovery.EnableBinaryScanning = true
	cfg.Debug.Discovery.BinaryScanning.AccessMethod = constants.DefaultBinaryAccessMethod
	cfg.Debug.Discovery.BinaryScanning.CacheEnabled = true
	cfg.Debug.Discovery.BinaryScanning.CacheTTL = constants.DefaultBinaryCacheTTL
	cfg.Debug.Discovery.BinaryScanning.MaxCachedBinaries = constants.DefaultMaxCachedBinaries
	cfg.Debug.Discovery.BinaryScanning.TempDir = constants.DefaultBinaryTempDir
	cfg.Debug.Limits.MaxConcurrentSessions = constants.DefaultMaxConcurrentSessions
	cfg.Debug.Limits.MaxSessionDuration = constants.DefaultMaxSessionDuration
	cfg.Debug.Limits.MaxEventsPerSecond = constants.DefaultMaxEventsPerSecond
	cfg.Debug.Limits.MaxMemoryMB = constants.DefaultMaxMemoryMB
	cfg.Debug.BPF.MapSize = constants.DefaultBPFMapSize
	cfg.Debug.BPF.PerfBufferPages = constants.DefaultBPFPerfBufferPages

	// SystemMetrics defaults (RFD 071)
	cfg.SystemMetrics.Disabled = false
	cfg.SystemMetrics.Interval = constants.DefaultSystemMetricsInterval
	cfg.SystemMetrics.Retention = constants.DefaultSystemMetricsRetention
	cfg.SystemMetrics.CPUEnabled = true
	cfg.SystemMetrics.MemoryEnabled = true
	cfg.SystemMetrics.DiskEnabled = true
	cfg.SystemMetrics.NetworkEnabled = true

	// ContinuousProfiling defaults (RFD 072)
	// Note: Disabled defaults to false (meaning enabled by default)
	cfg.ContinuousProfiling.CPU.FrequencyHz = constants.DefaultCPUProfilingFrequencyHz
	cfg.ContinuousProfiling.CPU.Interval = constants.DefaultCPUProfilingInterval
	cfg.ContinuousProfiling.CPU.Retention = constants.DefaultCPUProfilingRetention
	cfg.ContinuousProfiling.CPU.MetadataRetention = constants.DefaultCPUProfilingMetadataRetention

	return cfg
}

// formatEndpoint formats a host and port into an endpoint string.
func formatEndpoint(host string, port int) string {
	return fmt.Sprintf("%s:%d", host, port)
}
