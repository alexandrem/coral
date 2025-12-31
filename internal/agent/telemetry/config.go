package telemetry

import (
	"context"
	"fmt"

	"github.com/coral-mesh/coral/internal/constants"
)

// SpanHandler is a callback function for custom span processing.
// When set, the OTLP receiver calls this instead of storing to default storage.
// This allows Beyla to intercept spans and store them in beyla_traces_local.
type SpanHandler func(ctx context.Context, span Span) error

// Config contains configuration for the OTLP receiver and telemetry processing.
type Config struct {
	// Disabled indicates if telemetry collection is disabled.
	// Default: false (telemetry is enabled by default).
	Disabled bool

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

	// DatabasePath is the file path for DuckDB storage (RFD 039).
	// If empty, uses in-memory storage (:memory:).
	// Example: ~/.coral/agent/telemetry.duckdb
	DatabasePath string

	// StorageRetentionHours defines how long to keep spans in local storage.
	// Default: 1 hour (pull-based architecture - colony queries recent data).
	StorageRetentionHours int

	// SpanHandler is an optional callback for custom span processing.
	// When set, spans are passed to this handler instead of default storage.
	// Used by Beyla to route traces to beyla_traces_local table.
	SpanHandler SpanHandler
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
		Disabled:              false,
		GRPCEndpoint:          fmt.Sprintf("0.0.0.0:%d", constants.DefaultOTLPGRPCPort),
		HTTPEndpoint:          fmt.Sprintf("0.0.0.0:%d", constants.DefaultOTLPHTTPPort),
		StorageRetentionHours: int(constants.DefaultTelemetryRetention.Hours()),
		Filters: FilterConfig{
			AlwaysCaptureErrors:    true,
			HighLatencyThresholdMs: constants.DefaultHighLatencyThresholdMs,
			SampleRate:             constants.DefaultSampleRate,
		},
	}
}
