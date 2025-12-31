// Package constants defines shared configuration constants and defaults.
package constants

import "time"

// Ports - Service port defaults.
const (
	// DefaultAgentPort is the default port for the agent's gRPC/Connect server.
	// Already defined in constants.go - kept here for documentation.
	// DefaultAgentPort = 9001

	// DefaultOTLPGRPCPort is the default port for OTLP gRPC endpoint.
	DefaultOTLPGRPCPort = 4317

	// DefaultOTLPHTTPPort is the default port for OTLP HTTP endpoint.
	DefaultOTLPHTTPPort = 4318

	// DefaultBeylaGRPCPort is the default port for Beyla gRPC endpoint.
	DefaultBeylaGRPCPort = 4319

	// DefaultBeylaHTTPPort is the default port for Beyla HTTP endpoint.
	DefaultBeylaHTTPPort = 4320
)

// Timeouts - Default timeout values.
const (
	// DefaultRPCTimeout is the default timeout for RPC calls.
	DefaultRPCTimeout = 10 * time.Second

	// DefaultQueryTimeout is the default timeout for database queries.
	DefaultQueryTimeout = 30 * time.Second

	// DefaultHealthTimeout is the default timeout for health checks.
	DefaultHealthTimeout = 500 * time.Millisecond

	// DefaultSDKAPITimeout is the default timeout for SDK API calls.
	DefaultSDKAPITimeout = 5 * time.Second

	// DefaultDiscoveryTimeout is the default timeout for discovery service calls.
	DefaultDiscoveryTimeout = 10 * time.Second

	// DefaultAskIdleTimeout is the default idle timeout for ask daemon.
	DefaultAskIdleTimeout = 10 * time.Minute
)

// Intervals - Default interval values.
const (
	// DefaultPollInterval is the default polling interval.
	DefaultPollInterval = 30 * time.Second

	// DefaultCleanupInterval is the default cleanup interval.
	DefaultCleanupInterval = 1 * time.Hour

	// DefaultRegisterInterval is the default registration interval for discovery.
	DefaultRegisterInterval = 60 * time.Second

	// DefaultSystemMetricsInterval is the default system metrics collection interval.
	DefaultSystemMetricsInterval = 15 * time.Second

	// DefaultCPUProfilingInterval is the default CPU profiling collection interval.
	DefaultCPUProfilingInterval = 15 * time.Second

	// DefaultBeylaPollerInterval is the default Beyla data polling interval.
	DefaultBeylaPollerInterval = 30 * time.Second

	// DefaultFunctionPollerInterval is the default function discovery polling interval.
	DefaultFunctionPollerInterval = 300 * time.Second // 5 minutes

	// DefaultSystemMetricsPollerInterval is the default system metrics aggregation polling interval.
	DefaultSystemMetricsPollerInterval = 60 * time.Second // 1 minute

	// DefaultCPUProfilingPollerInterval is the default CPU profiling aggregation polling interval.
	DefaultCPUProfilingPollerInterval = 30 * time.Second
)

// Retention - Default retention periods.
const (
	// DefaultTelemetryRetention is the default telemetry data retention period.
	DefaultTelemetryRetention = 1 * time.Hour

	// DefaultSystemMetricsRetention is the default system metrics local retention.
	DefaultSystemMetricsRetention = 1 * time.Hour

	// DefaultCPUProfilingRetention is the default CPU profile sample retention.
	DefaultCPUProfilingRetention = 1 * time.Hour

	// DefaultCPUProfilingMetadataRetention is the default binary metadata retention.
	DefaultCPUProfilingMetadataRetention = 7 * 24 * time.Hour // 7 days

	// DefaultBeylaRetentionDays is the default retention period for Beyla data (days).
	DefaultBeylaRetentionDays = 30

	// DefaultSystemMetricsRetentionDays is the default retention period for system metrics (days).
	DefaultSystemMetricsRetentionDays = 30

	// DefaultCPUProfilingRetentionDays is the default retention period for CPU profiles (days).
	DefaultCPUProfilingRetentionDays = 30

	// DefaultBinaryCacheTTL is the default TTL for cached binary metadata.
	DefaultBinaryCacheTTL = 1 * time.Hour
)

// Sampling and Filtering - Default sampling rates and thresholds.
const (
	// DefaultSampleRate is the default telemetry sample rate (0.0-1.0).
	DefaultSampleRate = 0.10 // 10%

	// DefaultBeylaSampleRate is the default Beyla sample rate (0.0-1.0).
	DefaultBeylaSampleRate = 1.0 // 100%

	// DefaultHighLatencyThresholdMs is the default high latency threshold in milliseconds.
	DefaultHighLatencyThresholdMs = 500.0 // 500ms
)

// CPU Profiling - Default CPU profiling settings.
const (
	// DefaultCPUProfilingFrequencyHz is the default CPU profiling sampling frequency.
	// Uses a prime number to avoid synchronization issues.
	DefaultCPUProfilingFrequencyHz = 19
)

// Debug Session - Default debug session limits.
const (
	// DefaultMaxConcurrentSessions is the default maximum concurrent debug sessions.
	DefaultMaxConcurrentSessions = 5

	// DefaultMaxSessionDuration is the default maximum debug session duration.
	DefaultMaxSessionDuration = 10 * time.Minute

	// DefaultMaxEventsPerSecond is the default maximum events per second for debug sessions.
	DefaultMaxEventsPerSecond = 10000

	// DefaultMaxMemoryMB is the default maximum memory for debug sessions.
	DefaultMaxMemoryMB = 256

	// DefaultSDKAPIRetryAttempts is the default number of retry attempts for SDK API calls.
	DefaultSDKAPIRetryAttempts = 3
)

// BPF - Default BPF settings.
const (
	// DefaultBPFMapSize is the default BPF map size.
	DefaultBPFMapSize = 10240

	// DefaultBPFPerfBufferPages is the default BPF perf buffer pages.
	DefaultBPFPerfBufferPages = 64
)

// Binary Scanning - Default binary scanning settings.
const (
	// DefaultBinaryAccessMethod is the default method for accessing container binaries.
	DefaultBinaryAccessMethod = "direct"

	// DefaultMaxCachedBinaries is the default maximum number of cached binaries.
	DefaultMaxCachedBinaries = 100

	// DefaultBinaryTempDir is the default directory for temporary binary copies.
	DefaultBinaryTempDir = "/tmp/coral-binaries"
)

// Beyla Limits - Default Beyla resource limits.
const (
	// DefaultBeylaMaxTracedConnections is the default maximum traced connections.
	DefaultBeylaMaxTracedConnections = 1000

	// DefaultBeylaRingBufferSize is the default ring buffer size.
	DefaultBeylaRingBufferSize = 16384
)

// Ask Configuration - Default Ask LLM settings.
const (
	// DefaultAskModel is the default LLM model for coral ask.
	DefaultAskModel = "openai:gpt-4o-mini"

	// DefaultAskMaxTurns is the default maximum conversation turns.
	DefaultAskMaxTurns = 10

	// DefaultAskContextWindow is the default context window size.
	DefaultAskContextWindow = 8192

	// DefaultAskAgentMode is the default agent deployment mode.
	DefaultAskAgentMode = "embedded"

	// DefaultAskDaemonSocket is the default Unix socket for ask daemon.
	DefaultAskDaemonSocket = "~/.coral/ask-agent.sock"
)
