package telemetry

import (
	"testing"

	"github.com/coral-mesh/coral/internal/constants"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	// Test default values.
	if config.Disabled {
		t.Error("DefaultConfig() Disabled = true, want false")
	}

	expectedGRPC := "0.0.0.0:4317"
	if config.GRPCEndpoint != expectedGRPC {
		t.Errorf("DefaultConfig() GRPCEndpoint = %q, want %q", config.GRPCEndpoint, expectedGRPC)
	}

	expectedHTTP := "0.0.0.0:4318"
	if config.HTTPEndpoint != expectedHTTP {
		t.Errorf("DefaultConfig() HTTPEndpoint = %q, want %q", config.HTTPEndpoint, expectedHTTP)
	}

	expectedRetention := int(constants.DefaultTelemetryRetention.Hours())
	if config.StorageRetentionHours != expectedRetention {
		t.Errorf("DefaultConfig() StorageRetentionHours = %d, want %d", config.StorageRetentionHours, expectedRetention)
	}

	// Test filter defaults.
	if !config.Filters.AlwaysCaptureErrors {
		t.Error("DefaultConfig() Filters.AlwaysCaptureErrors = false, want true")
	}

	if config.Filters.HighLatencyThresholdMs != constants.DefaultHighLatencyThresholdMs {
		t.Errorf("DefaultConfig() Filters.HighLatencyThresholdMs = %f, want %f",
			config.Filters.HighLatencyThresholdMs, constants.DefaultHighLatencyThresholdMs)
	}

	if config.Filters.SampleRate != constants.DefaultSampleRate {
		t.Errorf("DefaultConfig() Filters.SampleRate = %f, want %f",
			config.Filters.SampleRate, constants.DefaultSampleRate)
	}
}

func TestFilterConfig(t *testing.T) {
	tests := []struct {
		name   string
		config FilterConfig
	}{
		{
			name: "all errors captured",
			config: FilterConfig{
				AlwaysCaptureErrors:    true,
				HighLatencyThresholdMs: 500,
				SampleRate:             0.1,
			},
		},
		{
			name: "no error capture",
			config: FilterConfig{
				AlwaysCaptureErrors:    false,
				HighLatencyThresholdMs: 1000,
				SampleRate:             0.5,
			},
		},
		{
			name: "zero sample rate",
			config: FilterConfig{
				AlwaysCaptureErrors:    true,
				HighLatencyThresholdMs: 100,
				SampleRate:             0.0,
			},
		},
		{
			name: "100% sample rate",
			config: FilterConfig{
				AlwaysCaptureErrors:    false,
				HighLatencyThresholdMs: 1000,
				SampleRate:             1.0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Just verify the config can be created and fields are set.
			if tt.config.SampleRate < 0 || tt.config.SampleRate > 1 {
				t.Errorf("Invalid sample rate: %f (should be 0.0-1.0)", tt.config.SampleRate)
			}

			if tt.config.HighLatencyThresholdMs < 0 {
				t.Errorf("Invalid latency threshold: %f (should be >= 0)", tt.config.HighLatencyThresholdMs)
			}
		})
	}
}

func TestConfig_CustomValues(t *testing.T) {
	config := Config{
		Disabled:              true,
		GRPCEndpoint:          "localhost:9999",
		HTTPEndpoint:          "localhost:8888",
		StorageRetentionHours: 24,
		AgentID:               "test-agent-123",
		DatabasePath:          "/tmp/test.db",
		Filters: FilterConfig{
			AlwaysCaptureErrors:    false,
			HighLatencyThresholdMs: 2000,
			SampleRate:             0.05,
		},
	}

	// Verify all fields are set correctly.
	if !config.Disabled {
		t.Error("Config Disabled should be true")
	}

	if config.GRPCEndpoint != "localhost:9999" {
		t.Errorf("Config GRPCEndpoint = %q, want %q", config.GRPCEndpoint, "localhost:9999")
	}

	if config.HTTPEndpoint != "localhost:8888" {
		t.Errorf("Config HTTPEndpoint = %q, want %q", config.HTTPEndpoint, "localhost:8888")
	}

	if config.StorageRetentionHours != 24 {
		t.Errorf("Config StorageRetentionHours = %d, want %d", config.StorageRetentionHours, 24)
	}

	if config.AgentID != "test-agent-123" {
		t.Errorf("Config AgentID = %q, want %q", config.AgentID, "test-agent-123")
	}

	if config.DatabasePath != "/tmp/test.db" {
		t.Errorf("Config DatabasePath = %q, want %q", config.DatabasePath, "/tmp/test.db")
	}

	if config.Filters.AlwaysCaptureErrors {
		t.Error("Config Filters.AlwaysCaptureErrors should be false")
	}

	if config.Filters.HighLatencyThresholdMs != 2000 {
		t.Errorf("Config Filters.HighLatencyThresholdMs = %f, want %f",
			config.Filters.HighLatencyThresholdMs, 2000.0)
	}

	if config.Filters.SampleRate != 0.05 {
		t.Errorf("Config Filters.SampleRate = %f, want %f", config.Filters.SampleRate, 0.05)
	}
}
