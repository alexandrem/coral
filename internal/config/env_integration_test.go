package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoader_EnvVarOverrides(t *testing.T) {
	// Create temporary home directory
	tmpHome := t.TempDir()
	loader := &Loader{homeDir: tmpHome}

	// Create a base colony config
	config := &ColonyConfig{
		ColonyID:        "test-colony",
		ApplicationName: "test-app",
		WireGuard: WireGuardConfig{
			Port: 51820,
		},
	}
	err := loader.SaveColonyConfig(config)
	require.NoError(t, err)

	// Set environment variables for overrides
	envVars := map[string]string{
		"CORAL_STORAGE_PATH":                   "/tmp/custom-storage",
		"CORAL_SERVICES_POLL_INTERVAL":         "120s",
		"CORAL_DISCOVERY_REGISTER_INTERVAL":    "5m",
		"CORAL_WG_KEEPALIVE":                   "45",
		"CORAL_ASK_MODEL":                      "anthropic:claude-3-opus",
		"CORAL_ASK_MAX_TURNS":                  "50",
		"CORAL_MESH_SUBNET":                    "10.99.0.0/24",
		"CORAL_BEYLA_POLL_INTERVAL":            "10s",
		"CORAL_FUNCTIONS_POLL_INTERVAL":        "10m",
		"CORAL_SYSTEM_METRICS_POLLER_INTERVAL": "30s",
		"CORAL_PROFILING_POLLER_INTERVAL":      "15s",
		"CORAL_TELEMETRY_POLL_INTERVAL":        "90s",
	}

	for k, v := range envVars {
		os.Setenv(k, v)
		defer os.Unsetenv(k)
	}

	// Load config
	loaded, err := loader.LoadColonyConfig("test-colony")
	require.NoError(t, err)

	// Verify overrides for non-pointer fields or fields present in struct
	assert.Equal(t, "/tmp/custom-storage", loaded.StoragePath)
	assert.Equal(t, 120*time.Second, loaded.Services.PollInterval)
	assert.Equal(t, 5*time.Minute, loaded.Discovery.RegisterInterval)
	assert.Equal(t, 45, loaded.WireGuard.PersistentKeepalive)
	assert.Equal(t, "10.99.0.0/24", loaded.WireGuard.MeshNetworkIPv4)
	assert.Equal(t, 10*time.Second, loaded.Beyla.PollInterval)
	assert.Equal(t, 10*time.Minute, loaded.FunctionRegistry.PollInterval)
	assert.Equal(t, 30*time.Second, loaded.SystemMetrics.PollInterval)
	assert.Equal(t, 15*time.Second, loaded.ContinuousProfiling.PollInterval)
	assert.Equal(t, 90*time.Second, loaded.Telemetry.PollInterval)

	// Verify pointer struct (Ask) logic
	// If Ask wasn't in YAML and is nil, it remains nil (envloader doesn't auto-create nil pointers)
	// This is expected behavior.
	if loaded.Ask == nil {
		// Verify expected behavior: Re-save with Ask initialized to test override
		config.Ask = &AskConfig{
			DefaultModel: "gpt-3.5-turbo", // overridden
		}
		err = loader.SaveColonyConfig(config)
		require.NoError(t, err)

		// Reload
		loaded, err = loader.LoadColonyConfig("test-colony")
		require.NoError(t, err)
	}

	require.NotNil(t, loaded.Ask)
	assert.Equal(t, "anthropic:claude-3-opus", loaded.Ask.DefaultModel)
	assert.Equal(t, 50, loaded.Ask.Conversation.MaxTurns)
}

func TestLoader_EnvVarOverrides_DurationStringsForIntFields(t *testing.T) {
	// Test that int interval fields accept duration strings (e.g., "2m", "10s")
	// in addition to plain integer seconds.
	tmpHome := t.TempDir()
	loader := &Loader{homeDir: tmpHome}

	config := &ColonyConfig{
		ColonyID:        "test-colony",
		ApplicationName: "test-app",
		WireGuard: WireGuardConfig{
			Port: 51820,
		},
	}
	err := loader.SaveColonyConfig(config)
	require.NoError(t, err)

	// Set interval env vars using duration strings instead of plain integers.
	envVars := map[string]string{
		"CORAL_SERVICES_POLL_INTERVAL":         "2m",
		"CORAL_WG_KEEPALIVE":                   "30",
		"CORAL_BEYLA_POLL_INTERVAL":            "10s",
		"CORAL_FUNCTIONS_POLL_INTERVAL":        "5m",
		"CORAL_SYSTEM_METRICS_POLLER_INTERVAL": "1m30s",
		"CORAL_PROFILING_POLLER_INTERVAL":      "45s",
		"CORAL_TELEMETRY_POLL_INTERVAL":        "1m",
	}

	for k, v := range envVars {
		os.Setenv(k, v)
		defer os.Unsetenv(k)
	}

	loaded, err := loader.LoadColonyConfig("test-colony")
	require.NoError(t, err)

	assert.Equal(t, 2*time.Minute, loaded.Services.PollInterval)
	assert.Equal(t, 30, loaded.WireGuard.PersistentKeepalive)
	assert.Equal(t, 10*time.Second, loaded.Beyla.PollInterval)
	assert.Equal(t, 5*time.Minute, loaded.FunctionRegistry.PollInterval)
	assert.Equal(t, 90*time.Second, loaded.SystemMetrics.PollInterval)
	assert.Equal(t, 45*time.Second, loaded.ContinuousProfiling.PollInterval)
	assert.Equal(t, time.Minute, loaded.Telemetry.PollInterval)
}

func TestLoader_GlobalConfig_EnvOverrides(t *testing.T) {
	tmpHome := t.TempDir()
	loader := &Loader{homeDir: tmpHome}

	envVars := map[string]string{
		"CORAL_DISCOVERY_ENDPOINT": "https://custom.discovery.com",
		"CORAL_DEFAULT_COLONY":     "env-colony-id",
		"CORAL_ASK_MODEL":          "openai:gpt-4o",
	}

	for k, v := range envVars {
		os.Setenv(k, v)
		defer os.Unsetenv(k)
	}

	// Load global config (should use defaults + env)
	config, err := loader.LoadGlobalConfig()
	require.NoError(t, err)

	assert.Equal(t, "https://custom.discovery.com", config.Discovery.Endpoint)
	assert.Equal(t, "env-colony-id", config.DefaultColony)
	// Verify Ask model in Global config (struct field, not pointer, so always works)
	assert.Equal(t, "openai:gpt-4o", config.AI.Ask.DefaultModel)
}
