package telemetry

// Config contains configuration for the OTLP receiver and telemetry processing.
type Config struct {
	// Enabled indicates if telemetry collection is enabled.
	Enabled bool

	// Endpoint is the address to bind the OTLP receiver (e.g., "127.0.0.1:4317").
	Endpoint string

	// Filters define static filtering rules for spans.
	Filters FilterConfig

	// AgentID is the identifier of this agent.
	AgentID string
}

// FilterConfig defines static filtering rules (RFD 025).
type FilterConfig struct {
	// AlwaysCaptureErrors determines if error spans are always captured.
	AlwaysCaptureErrors bool

	// LatencyThresholdMs is the latency threshold in milliseconds.
	// Spans with latency > threshold are always captured.
	LatencyThresholdMs float64

	// SampleRate is the sampling rate for normal spans (0.0 to 1.0).
	// Example: 0.10 means 10% of normal spans.
	SampleRate float64
}

// DefaultConfig returns a default telemetry configuration.
func DefaultConfig() Config {
	return Config{
		Enabled:  false,
		Endpoint: "127.0.0.1:4317",
		Filters: FilterConfig{
			AlwaysCaptureErrors:  true,
			LatencyThresholdMs:   500.0,
			SampleRate:           0.10,
		},
	}
}
