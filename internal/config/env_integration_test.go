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
		"CORAL_SERVICES_POLL_INTERVAL":         "120",
		"CORAL_DISCOVERY_REGISTER_INTERVAL":    "5m",
		"CORAL_WG_KEEPALIVE":                   "45",
		"CORAL_ASK_MODEL":                      "anthropic:claude-3-opus",
		"CORAL_ASK_MAX_TURNS":                  "50",
		"CORAL_MESH_SUBNET":                    "10.99.0.0/24",
		"CORAL_BEYLA_POLL_INTERVAL":            "10",
		"CORAL_FUNCTIONS_POLL_INTERVAL":        "600",
		"CORAL_SYSTEM_METRICS_POLLER_INTERVAL": "30",
		"CORAL_PROFILING_POLLER_INTERVAL":      "15",
		"CORAL_TELEMETRY_POLL_INTERVAL":        "90",
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
	assert.Equal(t, 120, loaded.Services.PollInterval)
	assert.Equal(t, 5*time.Minute, loaded.Discovery.RegisterInterval)
	assert.Equal(t, 45, loaded.WireGuard.PersistentKeepalive)
	assert.Equal(t, "10.99.0.0/24", loaded.WireGuard.MeshNetworkIPv4)
	assert.Equal(t, 10, loaded.Beyla.PollInterval)
	assert.Equal(t, 600, loaded.FunctionRegistry.PollInterval)
	assert.Equal(t, 30, loaded.SystemMetrics.PollInterval)
	assert.Equal(t, 15, loaded.ContinuousProfiling.PollInterval)
	assert.Equal(t, 90, loaded.Telemetry.PollInterval)

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
