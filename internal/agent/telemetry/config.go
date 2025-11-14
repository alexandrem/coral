package telemetry

// Config contains configuration for the OTLP receiver and telemetry processing.
type Config struct {
	// Enabled indicates if telemetry collection is enabled.
	Enabled bool

	// GRPCEndpoint is the address to bind the OTLP gRPC receiver (e.g., "0.0.0.0:4317").
	// Standard OTLP gRPC port is 4317.
	GRPCEndpoint string

	// HTTPEndpoint is the address to bind the OTLP HTTP receiver (e.g., "0.0.0.0:4318").
	// Standard OTLP HTTP port is 4318.
	HTTPEndpoint string

	// Filters define static filtering rules for spans.
	Filters FilterConfig

	// AgentID is the identifier of this agent.
	AgentID string

	// StorageRetentionHours defines how long to keep spans in local storage.
	// Default: 1 hour (pull-based architecture - colony queries recent data).
	StorageRetentionHours int
}

// FilterConfig defines static filtering rules (RFD 025).
type FilterConfig struct {
	// AlwaysCaptureErrors determines if error spans are always captured.
	AlwaysCaptureErrors bool

	// HighLatencyThresholdMs is the latency threshold in milliseconds.
	// Spans with latency > threshold are always captured.
	// Default: 500ms.
	HighLatencyThresholdMs float64

	// SampleRate is the sampling rate for normal spans (0.0 to 1.0).
	// Example: 0.10 means 10% of normal spans.
	// Default: 0.10 (10%).
	SampleRate float64
}

// DefaultConfig returns a default telemetry configuration.
func DefaultConfig() Config {
	return Config{
		Enabled:               false,
		GRPCEndpoint:          "0.0.0.0:4317",
		HTTPEndpoint:          "0.0.0.0:4318",
		StorageRetentionHours: 1,
		Filters: FilterConfig{
			AlwaysCaptureErrors:    true,
			HighLatencyThresholdMs: 500.0,
			SampleRate:             0.10,
		},
	}
}
