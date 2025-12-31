package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/coral-mesh/coral/internal/constants"
)

func TestLayeredLoader_LoadAgentConfig_DefaultsOnly(t *testing.T) {
	loader := NewLayeredLoader()
	loader.DisableLayer(LayerFile)
	loader.DisableLayer(LayerEnv)

	cfg, err := loader.LoadAgentConfig("")
	if err != nil {
		t.Fatalf("LoadAgentConfig() failed: %v", err)
	}

	// Verify defaults
	if cfg.Agent.Runtime != "auto" {
		t.Errorf("Agent.Runtime = %q, want %q", cfg.Agent.Runtime, "auto")
	}

	if cfg.Telemetry.Filters.SampleRate != constants.DefaultSampleRate {
		t.Errorf("Telemetry.Filters.SampleRate = %f, want %f", cfg.Telemetry.Filters.SampleRate, constants.DefaultSampleRate)
	}
}

func TestLayeredLoader_LoadAgentConfig_FileOverridesDefaults(t *testing.T) {
	// Create temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "agent.yaml")

	configContent := `
agent:
  runtime: docker
  colony:
    id: test-colony-file
    auto_discover: false
telemetry:
  disabled: true
  filters:
    sample_rate: 0.50
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	loader := NewLayeredLoader()
	loader.DisableLayer(LayerEnv)

	cfg, err := loader.LoadAgentConfig(configPath)
	if err != nil {
		t.Fatalf("LoadAgentConfig() failed: %v", err)
	}

	// Verify file overrides defaults
	if cfg.Agent.Runtime != "docker" {
		t.Errorf("Agent.Runtime = %q, want %q", cfg.Agent.Runtime, "docker")
	}

	if cfg.Agent.Colony.ID != "test-colony-file" {
		t.Errorf("Agent.Colony.ID = %q, want %q", cfg.Agent.Colony.ID, "test-colony-file")
	}

	if cfg.Agent.Colony.AutoDiscover != false {
		t.Errorf("Agent.Colony.AutoDiscover = %v, want false", cfg.Agent.Colony.AutoDiscover)
	}

	if cfg.Telemetry.Disabled != true {
		t.Errorf("Telemetry.Disabled = %v, want true", cfg.Telemetry.Disabled)
	}

	if cfg.Telemetry.Filters.SampleRate != 0.50 {
		t.Errorf("Telemetry.Filters.SampleRate = %f, want 0.50", cfg.Telemetry.Filters.SampleRate)
	}

	// Verify other defaults are still present
	if cfg.Telemetry.Filters.HighLatencyThresholdMs != constants.DefaultHighLatencyThresholdMs {
		t.Errorf("Telemetry.Filters.HighLatencyThresholdMs = %f, want %f",
			cfg.Telemetry.Filters.HighLatencyThresholdMs, constants.DefaultHighLatencyThresholdMs)
	}
}

func TestLayeredLoader_LoadAgentConfig_EnvOverridesFileAndDefaults(t *testing.T) {
	// Create temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "agent.yaml")

	configContent := `
agent:
  runtime: docker
  colony:
    id: test-colony-file
telemetry:
  filters:
    sample_rate: 0.50
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	// Set environment variables
	os.Setenv("CORAL_AGENT_RUNTIME", "kubernetes")
	os.Setenv("CORAL_COLONY_ID", "test-colony-env")
	os.Setenv("CORAL_SAMPLE_RATE", "0.75")
	defer func() {
		os.Unsetenv("CORAL_AGENT_RUNTIME")
		os.Unsetenv("CORAL_COLONY_ID")
		os.Unsetenv("CORAL_SAMPLE_RATE")
	}()

	loader := NewLayeredLoader()

	cfg, err := loader.LoadAgentConfig(configPath)
	if err != nil {
		t.Fatalf("LoadAgentConfig() failed: %v", err)
	}

	// Verify env overrides file and defaults
	if cfg.Agent.Runtime != "kubernetes" {
		t.Errorf("Agent.Runtime = %q, want %q (from env)", cfg.Agent.Runtime, "kubernetes")
	}

	if cfg.Agent.Colony.ID != "test-colony-env" {
		t.Errorf("Agent.Colony.ID = %q, want %q (from env)", cfg.Agent.Colony.ID, "test-colony-env")
	}

	if cfg.Telemetry.Filters.SampleRate != 0.75 {
		t.Errorf("Telemetry.Filters.SampleRate = %f, want 0.75 (from env)", cfg.Telemetry.Filters.SampleRate)
	}
}

func TestLayeredLoader_LoadAgentConfig_NonExistentFile(t *testing.T) {
	loader := NewLayeredLoader()
	loader.DisableLayer(LayerEnv)

	// Non-existent file should not cause error
	cfg, err := loader.LoadAgentConfig("/nonexistent/path/agent.yaml")
	if err != nil {
		t.Fatalf("LoadAgentConfig() should not fail for non-existent file: %v", err)
	}

	// Should still have defaults
	if cfg.Agent.Runtime != "auto" {
		t.Errorf("Agent.Runtime = %q, want %q", cfg.Agent.Runtime, "auto")
	}
}

func TestLayeredLoader_LoadAgentConfig_InvalidYAML(t *testing.T) {
	// Create temporary config file with invalid YAML
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "agent.yaml")

	invalidYAML := `
agent:
  runtime: docker
  invalid yaml here: [
`

	if err := os.WriteFile(configPath, []byte(invalidYAML), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	loader := NewLayeredLoader()

	_, err := loader.LoadAgentConfig(configPath)
	if err == nil {
		t.Errorf("LoadAgentConfig() should fail for invalid YAML")
	}
}

func TestLayeredLoader_LoadGlobalConfig(t *testing.T) {
	// Create temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "global.yaml")

	configContent := `
version: "1"
default_colony: my-test-colony
discovery:
  endpoint: https://discovery.test.com
  timeout: 20s
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	// Set environment variable to override
	os.Setenv("CORAL_DEFAULT_COLONY", "env-colony")
	defer os.Unsetenv("CORAL_DEFAULT_COLONY")

	loader := NewLayeredLoader()

	cfg, err := loader.LoadGlobalConfig(configPath)
	if err != nil {
		t.Fatalf("LoadGlobalConfig() failed: %v", err)
	}

	// Env should override file
	if cfg.DefaultColony != "env-colony" {
		t.Errorf("DefaultColony = %q, want %q (from env)", cfg.DefaultColony, "env-colony")
	}

	// File should override defaults
	if cfg.Discovery.Endpoint != "https://discovery.test.com" {
		t.Errorf("Discovery.Endpoint = %q, want %q", cfg.Discovery.Endpoint, "https://discovery.test.com")
	}
}

func TestLayeredLoader_LoadColonyConfig(t *testing.T) {
	// Create temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "colony.yaml")

	configContent := `
version: "1"
colony_id: file-colony
application_name: test-app
environment: production
wireguard:
  port: 52000
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	loader := NewLayeredLoader()
	loader.DisableLayer(LayerEnv)

	cfg, err := loader.LoadColonyConfig(configPath, "default-colony", "default-app", "dev")
	if err != nil {
		t.Fatalf("LoadColonyConfig() failed: %v", err)
	}

	// File should override defaults passed to function
	if cfg.ColonyID != "file-colony" {
		t.Errorf("ColonyID = %q, want %q", cfg.ColonyID, "file-colony")
	}

	if cfg.ApplicationName != "test-app" {
		t.Errorf("ApplicationName = %q, want %q", cfg.ApplicationName, "test-app")
	}

	if cfg.Environment != "production" {
		t.Errorf("Environment = %q, want %q", cfg.Environment, "production")
	}

	if cfg.WireGuard.Port != 52000 {
		t.Errorf("WireGuard.Port = %d, want 52000", cfg.WireGuard.Port)
	}

	// Defaults should still be present for other fields
	if cfg.WireGuard.MTU != constants.DefaultWireGuardMTU {
		t.Errorf("WireGuard.MTU = %d, want %d", cfg.WireGuard.MTU, constants.DefaultWireGuardMTU)
	}
}

func TestLayeredLoader_LoadProjectConfig(t *testing.T) {
	// Create temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "project.yaml")

	configContent := `
version: "1"
colony_id: project-colony
dashboard:
  port: 4000
  enabled: false
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	loader := NewLayeredLoader()
	loader.DisableLayer(LayerEnv)

	cfg, err := loader.LoadProjectConfig(configPath, "default-colony")
	if err != nil {
		t.Fatalf("LoadProjectConfig() failed: %v", err)
	}

	// File should override defaults
	if cfg.ColonyID != "project-colony" {
		t.Errorf("ColonyID = %q, want %q", cfg.ColonyID, "project-colony")
	}

	if cfg.Dashboard.Port != 4000 {
		t.Errorf("Dashboard.Port = %d, want 4000", cfg.Dashboard.Port)
	}

	if cfg.Dashboard.Enabled != false {
		t.Errorf("Dashboard.Enabled = %v, want false", cfg.Dashboard.Enabled)
	}
}

func TestLayeredLoader_EnableDisableLayers(t *testing.T) {
	loader := NewLayeredLoader()

	// Test initial state
	if !loader.enabledLayers[LayerDefaults] {
		t.Errorf("LayerDefaults should be enabled by default")
	}
	if !loader.enabledLayers[LayerFile] {
		t.Errorf("LayerFile should be enabled by default")
	}
	if !loader.enabledLayers[LayerEnv] {
		t.Errorf("LayerEnv should be enabled by default")
	}
	if loader.enabledLayers[LayerFlags] {
		t.Errorf("LayerFlags should be disabled by default")
	}

	// Test disabling
	loader.DisableLayer(LayerDefaults)
	if loader.enabledLayers[LayerDefaults] {
		t.Errorf("LayerDefaults should be disabled after DisableLayer()")
	}

	// Test enabling
	loader.EnableLayer(LayerFlags)
	if !loader.enabledLayers[LayerFlags] {
		t.Errorf("LayerFlags should be enabled after EnableLayer()")
	}
}

func TestLayeredLoader_ValidateConfig(t *testing.T) {
	loader := NewLayeredLoader()

	// Valid config
	validCfg := DefaultGlobalConfig()
	if err := loader.ValidateConfig(validCfg); err != nil {
		t.Errorf("ValidateConfig() should not fail for valid config: %v", err)
	}

	// Invalid config
	invalidCfg := &GlobalConfig{
		Version: "", // Missing version
	}
	if err := loader.ValidateConfig(invalidCfg); err == nil {
		t.Errorf("ValidateConfig() should fail for invalid config")
	}
}
