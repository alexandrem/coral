package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coral-io/coral/internal/constants"
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
			Endpoint: "http://localhost:8080",
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
	assert.Equal(t, "http://localhost:8080", config.Discovery.Endpoint)
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
			Enabled:          true,
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

	// Load non-existent colony should fail
	_, err := loader.LoadColonyConfig("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
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
