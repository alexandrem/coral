package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadFromEnv_AgentConfig(t *testing.T) {
	// Set up environment variables
	envVars := map[string]string{
		"CORAL_COLONY_ID":                 "test-colony-123",
		"CORAL_AUTO_DISCOVER":             "false",
		"CORAL_AGENT_RUNTIME":             "docker",
		"CORAL_TELEMETRY_DISABLED":        "true",
		"CORAL_OTLP_GRPC_ENDPOINT":        "localhost:9999",
		"CORAL_OTLP_HTTP_ENDPOINT":        "localhost:8888",
		"CORAL_SAMPLE_RATE":               "0.25",
		"CORAL_HIGH_LATENCY_THRESHOLD_MS": "1000.0",
		"CORAL_ALWAYS_CAPTURE_ERRORS":     "false",
		"CORAL_TELEMETRY_RETENTION_HOURS": "2",
		"CORAL_STUN_SERVERS":              "stun1.example.com:3478,stun2.example.com:3478",
		"CORAL_ENABLE_RELAY":              "true",
	}

	// Set environment variables
	for key, value := range envVars {
		os.Setenv(key, value)
		defer os.Unsetenv(key)
	}

	// Create default config
	cfg := DefaultAgentConfig()

	// Load from environment
	err := LoadFromEnv(cfg)
	if err != nil {
		t.Fatalf("LoadFromEnv() failed: %v", err)
	}

	// Verify values were loaded
	if cfg.Agent.Colony.ID != "test-colony-123" {
		t.Errorf("Agent.Colony.ID = %q, want %q", cfg.Agent.Colony.ID, "test-colony-123")
	}

	if cfg.Agent.Colony.AutoDiscover != false {
		t.Errorf("Agent.Colony.AutoDiscover = %v, want false", cfg.Agent.Colony.AutoDiscover)
	}

	if cfg.Agent.Runtime != "docker" {
		t.Errorf("Agent.Runtime = %q, want %q", cfg.Agent.Runtime, "docker")
	}

	if cfg.Telemetry.Disabled != true {
		t.Errorf("Telemetry.Disabled = %v, want true", cfg.Telemetry.Disabled)
	}

	if cfg.Telemetry.GRPCEndpoint != "localhost:9999" {
		t.Errorf("Telemetry.GRPCEndpoint = %q, want %q", cfg.Telemetry.GRPCEndpoint, "localhost:9999")
	}

	if cfg.Telemetry.HTTPEndpoint != "localhost:8888" {
		t.Errorf("Telemetry.HTTPEndpoint = %q, want %q", cfg.Telemetry.HTTPEndpoint, "localhost:8888")
	}

	if cfg.Telemetry.Filters.SampleRate != 0.25 {
		t.Errorf("Telemetry.Filters.SampleRate = %f, want 0.25", cfg.Telemetry.Filters.SampleRate)
	}

	if cfg.Telemetry.Filters.HighLatencyThresholdMs != 1000.0 {
		t.Errorf("Telemetry.Filters.HighLatencyThresholdMs = %f, want 1000.0", cfg.Telemetry.Filters.HighLatencyThresholdMs)
	}

	if cfg.Telemetry.Filters.AlwaysCaptureErrors != false {
		t.Errorf("Telemetry.Filters.AlwaysCaptureErrors = %v, want false", cfg.Telemetry.Filters.AlwaysCaptureErrors)
	}

	if cfg.Telemetry.StorageRetentionHours != 2 {
		t.Errorf("Telemetry.StorageRetentionHours = %d, want 2", cfg.Telemetry.StorageRetentionHours)
	}

	if len(cfg.Agent.NAT.STUNServers) != 2 {
		t.Errorf("Agent.NAT.STUNServers length = %d, want 2", len(cfg.Agent.NAT.STUNServers))
	} else {
		if cfg.Agent.NAT.STUNServers[0] != "stun1.example.com:3478" {
			t.Errorf("Agent.NAT.STUNServers[0] = %q, want %q", cfg.Agent.NAT.STUNServers[0], "stun1.example.com:3478")
		}
		if cfg.Agent.NAT.STUNServers[1] != "stun2.example.com:3478" {
			t.Errorf("Agent.NAT.STUNServers[1] = %q, want %q", cfg.Agent.NAT.STUNServers[1], "stun2.example.com:3478")
		}
	}

	if cfg.Agent.NAT.EnableRelay != true {
		t.Errorf("Agent.NAT.EnableRelay = %v, want true", cfg.Agent.NAT.EnableRelay)
	}
}

func TestLoadFromEnv_GlobalConfig(t *testing.T) {
	// Set up environment variables
	envVars := map[string]string{
		"CORAL_DEFAULT_COLONY":     "my-default-colony",
		"CORAL_DISCOVERY_ENDPOINT": "https://discovery.example.com",
		"CORAL_DISCOVERY_TIMEOUT":  "30s",
		"CORAL_STUN_SERVERS":       "stun.cloudflare.com:3478",
	}

	// Set environment variables
	for key, value := range envVars {
		os.Setenv(key, value)
		defer os.Unsetenv(key)
	}

	// Create default config
	cfg := DefaultGlobalConfig()

	// Load from environment
	err := LoadFromEnv(cfg)
	if err != nil {
		t.Fatalf("LoadFromEnv() failed: %v", err)
	}

	// Verify values were loaded
	if cfg.DefaultColony != "my-default-colony" {
		t.Errorf("DefaultColony = %q, want %q", cfg.DefaultColony, "my-default-colony")
	}

	if cfg.Discovery.Endpoint != "https://discovery.example.com" {
		t.Errorf("Discovery.Endpoint = %q, want %q", cfg.Discovery.Endpoint, "https://discovery.example.com")
	}

	if cfg.Discovery.Timeout != 30*time.Second {
		t.Errorf("Discovery.Timeout = %v, want 30s", cfg.Discovery.Timeout)
	}

	if len(cfg.Discovery.STUNServers) != 1 {
		t.Errorf("Discovery.STUNServers length = %d, want 1", len(cfg.Discovery.STUNServers))
	} else if cfg.Discovery.STUNServers[0] != "stun.cloudflare.com:3478" {
		t.Errorf("Discovery.STUNServers[0] = %q, want %q", cfg.Discovery.STUNServers[0], "stun.cloudflare.com:3478")
	}
}

func TestLoadFromEnv_SystemMetricsConfig(t *testing.T) {
	// Set up environment variables
	envVars := map[string]string{
		"CORAL_SYSTEM_METRICS_DISABLED":        "true",
		"CORAL_SYSTEM_METRICS_INTERVAL":        "30s",
		"CORAL_SYSTEM_METRICS_RETENTION":       "2h",
		"CORAL_SYSTEM_METRICS_CPU_ENABLED":     "false",
		"CORAL_SYSTEM_METRICS_MEMORY_ENABLED":  "true",
		"CORAL_SYSTEM_METRICS_DISK_ENABLED":    "false",
		"CORAL_SYSTEM_METRICS_NETWORK_ENABLED": "true",
	}

	// Set environment variables
	for key, value := range envVars {
		os.Setenv(key, value)
		defer os.Unsetenv(key)
	}

	// Create a test config
	cfg := &SystemMetricsConfig{
		Disabled:       false,
		Interval:       15 * time.Second,
		Retention:      1 * time.Hour,
		CPUEnabled:     true,
		MemoryEnabled:  false,
		DiskEnabled:    true,
		NetworkEnabled: false,
	}

	// Load from environment
	err := LoadFromEnv(cfg)
	if err != nil {
		t.Fatalf("LoadFromEnv() failed: %v", err)
	}

	// Verify values were loaded
	if cfg.Disabled != true {
		t.Errorf("Disabled = %v, want true", cfg.Disabled)
	}

	if cfg.Interval != 30*time.Second {
		t.Errorf("Interval = %v, want 30s", cfg.Interval)
	}

	if cfg.Retention != 2*time.Hour {
		t.Errorf("Retention = %v, want 2h", cfg.Retention)
	}

	if cfg.CPUEnabled != false {
		t.Errorf("CPUEnabled = %v, want false", cfg.CPUEnabled)
	}

	if cfg.MemoryEnabled != true {
		t.Errorf("MemoryEnabled = %v, want true", cfg.MemoryEnabled)
	}

	if cfg.DiskEnabled != false {
		t.Errorf("DiskEnabled = %v, want false", cfg.DiskEnabled)
	}

	if cfg.NetworkEnabled != true {
		t.Errorf("NetworkEnabled = %v, want true", cfg.NetworkEnabled)
	}
}

func TestLoadFromEnv_CPUProfilingConfig(t *testing.T) {
	// Set up environment variables
	envVars := map[string]string{
		"CORAL_CPU_PROFILING_DISABLED":           "true",
		"CORAL_CPU_PROFILING_FREQUENCY_HZ":       "50",
		"CORAL_CPU_PROFILING_INTERVAL":           "10s",
		"CORAL_CPU_PROFILING_RETENTION":          "30m",
		"CORAL_CPU_PROFILING_METADATA_RETENTION": "336h", // 14 days
	}

	// Set environment variables
	for key, value := range envVars {
		os.Setenv(key, value)
		defer os.Unsetenv(key)
	}

	// Create a test config
	cfg := &CPUProfilingConfig{
		Disabled:          false,
		FrequencyHz:       19,
		Interval:          15 * time.Second,
		Retention:         1 * time.Hour,
		MetadataRetention: 7 * 24 * time.Hour,
	}

	// Load from environment
	err := LoadFromEnv(cfg)
	if err != nil {
		t.Fatalf("LoadFromEnv() failed: %v", err)
	}

	// Verify values were loaded
	if cfg.Disabled != true {
		t.Errorf("Disabled = %v, want true", cfg.Disabled)
	}

	if cfg.FrequencyHz != 50 {
		t.Errorf("FrequencyHz = %d, want 50", cfg.FrequencyHz)
	}

	if cfg.Interval != 10*time.Second {
		t.Errorf("Interval = %v, want 10s", cfg.Interval)
	}

	if cfg.Retention != 30*time.Minute {
		t.Errorf("Retention = %v, want 30m", cfg.Retention)
	}

	if cfg.MetadataRetention != 336*time.Hour { // 14 days = 336 hours
		t.Errorf("MetadataRetention = %v, want 336h", cfg.MetadataRetention)
	}
}

func TestLoadFromEnv_InvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		envVar string
		value  string
	}{
		{"invalid boolean", "CORAL_TELEMETRY_DISABLED", "not-a-bool"},
		{"invalid integer", "CORAL_TELEMETRY_RETENTION_HOURS", "not-an-int"},
		{"invalid float", "CORAL_SAMPLE_RATE", "not-a-float"},
		{"invalid duration", "CORAL_SYSTEM_METRICS_INTERVAL", "not-a-duration"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv(tt.envVar, tt.value)
			defer os.Unsetenv(tt.envVar)

			var cfg interface{}
			switch tt.envVar {
			case "CORAL_TELEMETRY_DISABLED", "CORAL_TELEMETRY_RETENTION_HOURS", "CORAL_SAMPLE_RATE":
				cfg = DefaultAgentConfig()
			case "CORAL_SYSTEM_METRICS_INTERVAL":
				cfg = &SystemMetricsConfig{}
			}

			err := LoadFromEnv(cfg)
			if err == nil {
				t.Errorf("LoadFromEnv() should have failed with invalid %s", tt.name)
			}
		})
	}
}

func TestLoadFromEnv_EmptyEnvVars(t *testing.T) {
	// Create default config
	cfg := DefaultAgentConfig()

	// Store original values
	originalRuntime := cfg.Agent.Runtime
	originalSampleRate := cfg.Telemetry.Filters.SampleRate

	// Load from environment (no env vars set)
	err := LoadFromEnv(cfg)
	if err != nil {
		t.Fatalf("LoadFromEnv() failed: %v", err)
	}

	// Verify values were not changed (no env vars set)
	if cfg.Agent.Runtime != originalRuntime {
		t.Errorf("Agent.Runtime changed when no env var set")
	}

	if cfg.Telemetry.Filters.SampleRate != originalSampleRate {
		t.Errorf("Telemetry.Filters.SampleRate changed when no env var set")
	}
}
