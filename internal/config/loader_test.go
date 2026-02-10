package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coral-mesh/coral/internal/constants"
)

func TestLoader_SaveAndLoadGlobalConfig(t *testing.T) {
	// Create temporary home directory
	tmpHome := t.TempDir()
	loader := &Loader{homeDir: tmpHome}

	// Create test config
	config := &GlobalConfig{
		Version:       "1",
		DefaultColony: "test-colony-123",
		Discovery: DiscoveryGlobal{
			Endpoint: "https://discovery.coralmesh.dev",
			Timeout:  10 * time.Second,
		},
		AI: AIConfig{
			Provider:     "anthropic",
			APIKeySource: "env",
		},
		Preferences: Preferences{
			AutoUpdateCheck:  true,
			TelemetryEnabled: false,
		},
	}

	// Save config
	err := loader.SaveGlobalConfig(config)
	require.NoError(t, err)

	// Verify file exists
	configPath := loader.GlobalConfigPath()
	assert.FileExists(t, configPath)

	// Load config
	loaded, err := loader.LoadGlobalConfig()
	require.NoError(t, err)
	assert.Equal(t, config.Version, loaded.Version)
	assert.Equal(t, config.DefaultColony, loaded.DefaultColony)
	assert.Equal(t, config.Discovery.Endpoint, loaded.Discovery.Endpoint)
	assert.Equal(t, config.AI.Provider, loaded.AI.Provider)
}

func TestLoader_LoadGlobalConfig_NotExists(t *testing.T) {
	// Create temporary home directory
	tmpHome := t.TempDir()
	loader := &Loader{homeDir: tmpHome}

	// Load should return default config
	config, err := loader.LoadGlobalConfig()
	require.NoError(t, err)
	assert.NotNil(t, config)
	assert.Equal(t, SchemaVersion, config.Version)
	assert.Equal(t, "https://discovery.coralmesh.dev", config.Discovery.Endpoint)
}

func TestLoader_LoadGlobalConfig_DiscoveryEndpointEnvOverride(t *testing.T) {
	// Create temporary home directory
	tmpHome := t.TempDir()
	loader := &Loader{homeDir: tmpHome}

	// Save a config with a specific discovery endpoint
	config := &GlobalConfig{
		Version: "1",
		Discovery: DiscoveryGlobal{
			Endpoint: "https://discovery.coralmesh.dev",
			Timeout:  10 * time.Second,
		},
	}
	err := loader.SaveGlobalConfig(config)
	require.NoError(t, err)

	// Set CORAL_DISCOVERY_ENDPOINT environment variable
	originalValue := os.Getenv("CORAL_DISCOVERY_ENDPOINT")
	os.Setenv("CORAL_DISCOVERY_ENDPOINT", "http://discovery:9999")
	defer os.Setenv("CORAL_DISCOVERY_ENDPOINT", originalValue)

	// Load config - should use env var override
	loaded, err := loader.LoadGlobalConfig()
	require.NoError(t, err)
	assert.Equal(t, "http://discovery:9999", loaded.Discovery.Endpoint)
}

func TestLoader_LoadGlobalConfig_DiscoveryEndpointEnvOverride_NoFile(t *testing.T) {
	// Create temporary home directory (no config file exists)
	tmpHome := t.TempDir()
	loader := &Loader{homeDir: tmpHome}

	// Set CORAL_DISCOVERY_ENDPOINT environment variable
	originalValue := os.Getenv("CORAL_DISCOVERY_ENDPOINT")
	os.Setenv("CORAL_DISCOVERY_ENDPOINT", "http://custom-discovery:8080")
	defer os.Setenv("CORAL_DISCOVERY_ENDPOINT", originalValue)

	// Load config - should use default config with env var override
	loaded, err := loader.LoadGlobalConfig()
	require.NoError(t, err)
	assert.Equal(t, "http://custom-discovery:8080", loaded.Discovery.Endpoint)
}

func TestLoader_SaveAndLoadColonyConfig(t *testing.T) {
	// Create temporary home directory
	tmpHome := t.TempDir()
	loader := &Loader{homeDir: tmpHome}

	// Create test config
	config := &ColonyConfig{
		Version:         "1",
		ColonyID:        "test-app-dev-abc123",
		ApplicationName: "test-app",
		Environment:     "dev",
		ColonySecret:    "secret123",
		WireGuard: WireGuardConfig{
			PrivateKey: "private-key-base64",
			PublicKey:  "public-key-base64",
			Port:       constants.DefaultWireGuardPort,
		},
		StoragePath: "/path/to/storage",
		Discovery: DiscoveryColony{
			Disabled:         false,
			MeshID:           "test-app-dev-abc123",
			AutoRegister:     true,
			RegisterInterval: 60 * time.Second,
		},
		CreatedAt: time.Now(),
		CreatedBy: "user@host",
	}

	// Save config
	err := loader.SaveColonyConfig(config)
	require.NoError(t, err)

	// Verify file exists with correct permissions
	configPath := loader.ColonyConfigPath(config.ColonyID)
	assert.FileExists(t, configPath)

	info, err := os.Stat(configPath)
	require.NoError(t, err)
	// Check that file is only readable/writable by owner (0600)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())

	// Load config
	loaded, err := loader.LoadColonyConfig(config.ColonyID)
	require.NoError(t, err)
	assert.Equal(t, config.ColonyID, loaded.ColonyID)
	assert.Equal(t, config.ApplicationName, loaded.ApplicationName)
	assert.Equal(t, config.ColonySecret, loaded.ColonySecret)
	assert.Equal(t, config.WireGuard.PrivateKey, loaded.WireGuard.PrivateKey)
	assert.Equal(t, config.WireGuard.PublicKey, loaded.WireGuard.PublicKey)
}

func TestLoader_LoadColonyConfig_NotExists(t *testing.T) {
	tmpHome := t.TempDir()
	loader := &Loader{homeDir: tmpHome}

	// Ensure no env vars trigger config-less fallback.
	t.Setenv("CORAL_COLONY_ENDPOINT", "")

	// Load non-existent colony should fail.
	_, err := loader.LoadColonyConfig("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestLoader_LoadColonyConfig_ConfigLessMode(t *testing.T) {
	tmpHome := t.TempDir()
	loader := &Loader{homeDir: tmpHome}

	t.Run("returns synthetic config when endpoint env var is set", func(t *testing.T) {
		t.Setenv("CORAL_COLONY_ENDPOINT", "https://localhost:8443")
		t.Setenv("CORAL_INSECURE", "true")

		cfg, err := loader.LoadColonyConfig("my-colony-abc123")
		require.NoError(t, err)
		assert.Equal(t, "my-colony-abc123", cfg.ColonyID)
		assert.Equal(t, "https://localhost:8443", cfg.Remote.Endpoint)
		assert.True(t, cfg.Remote.InsecureSkipTLSVerify)
	})

	t.Run("returns not found when no endpoint env var is set", func(t *testing.T) {
		t.Setenv("CORAL_COLONY_ENDPOINT", "")

		_, err := loader.LoadColonyConfig("my-colony-abc123")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestLoader_ListColonies(t *testing.T) {
	tmpHome := t.TempDir()
	loader := &Loader{homeDir: tmpHome}

	// Empty directory should return empty list
	colonies, err := loader.ListColonies()
	require.NoError(t, err)
	assert.Empty(t, colonies)

	// Create some colony configs
	colony1 := &ColonyConfig{
		Version:         "1",
		ColonyID:        "app1-dev-abc",
		ApplicationName: "app1",
		Environment:     "dev",
		CreatedAt:       time.Now(),
	}
	colony2 := &ColonyConfig{
		Version:         "1",
		ColonyID:        "app2-prod-xyz",
		ApplicationName: "app2",
		Environment:     "prod",
		CreatedAt:       time.Now(),
	}

	err = loader.SaveColonyConfig(colony1)
	require.NoError(t, err)
	err = loader.SaveColonyConfig(colony2)
	require.NoError(t, err)

	// List should return both colonies
	colonies, err = loader.ListColonies()
	require.NoError(t, err)
	assert.Len(t, colonies, 2)
	assert.Contains(t, colonies, "app1-dev-abc")
	assert.Contains(t, colonies, "app2-prod-xyz")
}

func TestLoader_DeleteColonyConfig(t *testing.T) {
	tmpHome := t.TempDir()
	loader := &Loader{homeDir: tmpHome}

	// Create a colony config
	config := &ColonyConfig{
		Version:   "1",
		ColonyID:  "test-colony",
		CreatedAt: time.Now(),
	}
	err := loader.SaveColonyConfig(config)
	require.NoError(t, err)

	// Verify it exists
	configPath := loader.ColonyConfigPath(config.ColonyID)
	assert.FileExists(t, configPath)

	// Delete it
	err = loader.DeleteColonyConfig(config.ColonyID)
	require.NoError(t, err)

	// Verify it's gone
	assert.NoFileExists(t, configPath)

	// Deleting again should fail
	err = loader.DeleteColonyConfig(config.ColonyID)
	assert.Error(t, err)
}

func TestSaveAndLoadProjectConfig(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test config
	config := &ProjectConfig{
		Version:  "1",
		ColonyID: "test-colony-123",
		Dashboard: DashboardConfig{
			Port:    constants.DefaultDashboardPort,
			Enabled: true,
		},
		Storage: ProjectStorage{
			Path: constants.DefaultDir,
		},
	}

	// Save config
	err := SaveProjectConfig(tmpDir, config)
	require.NoError(t, err)

	// Verify file exists
	configPath := filepath.Join(tmpDir, constants.DefaultDir, constants.ConfigFile)
	assert.FileExists(t, configPath)

	// Load config
	loaded, err := LoadProjectConfig(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, config.ColonyID, loaded.ColonyID)
	assert.Equal(t, config.Dashboard.Port, loaded.Dashboard.Port)
	assert.Equal(t, config.Storage.Path, loaded.Storage.Path)
}

func TestLoadProjectConfig_NotExists(t *testing.T) {
	tmpDir := t.TempDir()

	// Loading non-existent project config should return nil, no error
	config, err := LoadProjectConfig(tmpDir)
	require.NoError(t, err)
	assert.Nil(t, config)
}

// RFD 050 - CORAL_CONFIG env var support tests.

func TestNewLoader_CoralConfigEnvVar(t *testing.T) {
	// Create a temporary directory for config.
	tmpDir := t.TempDir()

	// Set CORAL_CONFIG env var.
	originalValue := os.Getenv("CORAL_CONFIG")
	os.Setenv("CORAL_CONFIG", tmpDir)
	defer os.Setenv("CORAL_CONFIG", originalValue)

	// Create loader.
	loader, err := NewLoader()
	require.NoError(t, err)

	// Verify the loader uses the custom directory.
	expectedPath := filepath.Join(tmpDir, ".coral", "config.yaml")
	assert.Equal(t, expectedPath, loader.GlobalConfigPath())
}

func TestNewLoader_DefaultHomeDir(t *testing.T) {
	// Ensure CORAL_CONFIG is not set.
	originalValue := os.Getenv("CORAL_CONFIG")
	_ = os.Unsetenv("CORAL_CONFIG")
	defer func(key, value string) {
		_ = os.Setenv(key, value) // TODO: errcheck
	}("CORAL_CONFIG", originalValue)

	// Create loader.
	loader, err := NewLoader()
	require.NoError(t, err)

	// Verify it uses home directory.
	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)
	expectedPath := filepath.Join(homeDir, ".coral", "config.yaml")
	assert.Equal(t, expectedPath, loader.GlobalConfigPath())
}

// RFD 050 - DeleteColonyDir tests.

func TestLoader_DeleteColonyDir(t *testing.T) {
	tmpHome := t.TempDir()
	loader := &Loader{homeDir: tmpHome}

	// Create a colony config.
	config := &ColonyConfig{
		Version:         "1",
		ColonyID:        "test-colony-delete",
		ApplicationName: "test-app",
		CreatedAt:       time.Now(),
	}
	err := loader.SaveColonyConfig(config)
	require.NoError(t, err)

	// Create additional files in colony directory (simulating CA, data).
	colonyDir := loader.ColonyDir(config.ColonyID)
	caDir := filepath.Join(colonyDir, "ca")
	err = os.MkdirAll(caDir, 0700)
	require.NoError(t, err)

	caFile := filepath.Join(caDir, "root-ca.crt")
	err = os.WriteFile(caFile, []byte("cert data"), 0600)
	require.NoError(t, err)

	// Verify files exist.
	assert.FileExists(t, loader.ColonyConfigPath(config.ColonyID))
	assert.DirExists(t, caDir)
	assert.FileExists(t, caFile)

	// Delete entire colony directory.
	err = loader.DeleteColonyDir(config.ColonyID)
	require.NoError(t, err)

	// Verify everything is gone.
	assert.NoDirExists(t, colonyDir)
}

func TestLoader_DeleteColonyDir_NotExists(t *testing.T) {
	tmpHome := t.TempDir()
	loader := &Loader{homeDir: tmpHome}

	// Deleting non-existent colony should fail.
	err := loader.DeleteColonyDir("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// RFD 050 - ValidateAll tests.

func TestLoader_ValidateAll(t *testing.T) {
	tmpHome := t.TempDir()
	loader := &Loader{homeDir: tmpHome}

	// Create a valid colony config.
	validConfig := &ColonyConfig{
		Version:         "1",
		ColonyID:        "valid-colony",
		ApplicationName: "valid-app",
		Environment:     "dev",
		WireGuard: WireGuardConfig{
			Port:            41580,
			MTU:             1420,
			MeshNetworkIPv4: "100.64.0.0/10",
		},
		CreatedAt: time.Now(),
	}
	err := loader.SaveColonyConfig(validConfig)
	require.NoError(t, err)

	// Create an invalid colony config (missing application_name).
	invalidConfig := &ColonyConfig{
		Version:   "1",
		ColonyID:  "invalid-colony",
		CreatedAt: time.Now(),
	}
	err = loader.SaveColonyConfig(invalidConfig)
	require.NoError(t, err)

	// Validate all.
	results, err := loader.ValidateAll()
	require.NoError(t, err)
	assert.Len(t, results, 2)

	// Valid colony should have nil error.
	assert.Nil(t, results["valid-colony"])

	// Invalid colony should have error.
	assert.NotNil(t, results["invalid-colony"])
	assert.Contains(t, results["invalid-colony"].Error(), "application_name")
}

func TestLoader_ValidateAll_Empty(t *testing.T) {
	tmpHome := t.TempDir()
	loader := &Loader{homeDir: tmpHome}

	// No colonies should return empty map.
	results, err := loader.ValidateAll()
	require.NoError(t, err)
	assert.Empty(t, results)
}

// RFD 050 - ValidateColonyConfig tests.

func TestValidateColonyConfig_Valid(t *testing.T) {
	config := &ColonyConfig{
		ColonyID:        "my-colony",
		ApplicationName: "my-app",
		WireGuard: WireGuardConfig{
			Port:            41580,
			MTU:             1420,
			MeshNetworkIPv4: "100.64.0.0/10",
		},
	}

	err := ValidateColonyConfig(config)
	assert.NoError(t, err)
}

func TestValidateColonyConfig_MissingColonyID(t *testing.T) {
	config := &ColonyConfig{
		ApplicationName: "my-app",
	}

	err := ValidateColonyConfig(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "colony_id")
}

func TestValidateColonyConfig_MissingApplicationName(t *testing.T) {
	config := &ColonyConfig{
		ColonyID: "my-colony",
	}

	err := ValidateColonyConfig(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "application_name")
}

func TestValidateColonyConfig_InvalidMeshSubnet(t *testing.T) {
	config := &ColonyConfig{
		ColonyID:        "my-colony",
		ApplicationName: "my-app",
		WireGuard: WireGuardConfig{
			MeshNetworkIPv4: "10.100.0.0/30", // Too small (smaller than /24).
		},
	}

	err := ValidateColonyConfig(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mesh_network_ipv4")
}

func TestValidateColonyConfig_InvalidPort(t *testing.T) {
	config := &ColonyConfig{
		ColonyID:        "my-colony",
		ApplicationName: "my-app",
		WireGuard: WireGuardConfig{
			Port: 70000, // Invalid port.
		},
	}

	err := ValidateColonyConfig(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "port")
}

func TestValidateColonyConfig_InvalidMTU(t *testing.T) {
	config := &ColonyConfig{
		ColonyID:        "my-colony",
		ApplicationName: "my-app",
		WireGuard: WireGuardConfig{
			MTU: 10000, // Invalid MTU.
		},
	}

	err := ValidateColonyConfig(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "MTU")
}
