package initcmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/coral-mesh/coral/internal/config"
)

func TestNewInitCmd(t *testing.T) {
	cmd := NewInitCmd()

	if cmd == nil {
		t.Fatal("NewInitCmd() returned nil")
	}

	if cmd.Use != "init <app-name>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "init <app-name>")
	}

	if cmd.Short == "" {
		t.Error("Short description is empty")
	}

	if cmd.Long == "" {
		t.Error("Long description is empty")
	}

	// Verify flags are defined
	envFlag := cmd.Flags().Lookup("env")
	if envFlag == nil {
		t.Error("--env flag not defined")
	} else if envFlag.DefValue != "dev" {
		t.Errorf("--env default = %q, want %q", envFlag.DefValue, "dev")
	}

	storageFlag := cmd.Flags().Lookup("storage")
	if storageFlag == nil {
		t.Error("--storage flag not defined")
	}

	discoveryFlag := cmd.Flags().Lookup("discovery")
	if discoveryFlag == nil {
		t.Error("--discovery flag not defined")
	}
}

func TestRunInit_BasicFlow(t *testing.T) {
	// Create temporary directory for config
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, ".coral")

	// Set HOME to tmpDir so config goes there
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", tmpDir)

	// Also need to clear XDG_CONFIG_HOME if set
	originalXDG := os.Getenv("XDG_CONFIG_HOME")
	defer func() {
		if originalXDG != "" {
			os.Setenv("XDG_CONFIG_HOME", originalXDG)
		} else {
			os.Unsetenv("XDG_CONFIG_HOME")
		}
	}()
	os.Unsetenv("XDG_CONFIG_HOME")

	// Run init
	err := runInit("test-app", "dev", configDir, "http://localhost:8080")

	if err != nil {
		t.Fatalf("runInit() error = %v", err)
	}

	// Verify config directory was created
	if _, err := os.Stat(configDir); os.IsNotExist(err) {
		t.Error("Config directory was not created")
	}

	// Verify colonies directory exists
	coloniesDir := filepath.Join(configDir, "colonies")
	if _, err := os.Stat(coloniesDir); os.IsNotExist(err) {
		t.Error("Colonies directory was not created")
	}

	// Project config is created in current working directory
	// In this test, that would be the repo root, not tmpDir
	// Just verify the runInit succeeded - cleanup if file was created
	projectConfigPath := filepath.Join(".", ".coral.yaml")
	defer os.Remove(projectConfigPath) // Clean up if created
}

func TestRunInit_CustomStorage(t *testing.T) {
	// Create temporary directory for custom storage
	tmpDir := t.TempDir()
	customStorage := filepath.Join(tmpDir, "custom-coral-storage")

	// Set HOME to tmpDir
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", tmpDir)

	originalXDG := os.Getenv("XDG_CONFIG_HOME")
	defer func() {
		if originalXDG != "" {
			os.Setenv("XDG_CONFIG_HOME", originalXDG)
		} else {
			os.Unsetenv("XDG_CONFIG_HOME")
		}
	}()
	os.Unsetenv("XDG_CONFIG_HOME")

	// Run init with custom storage path
	err := runInit("test-app", "production", customStorage, "")

	if err != nil {
		t.Fatalf("runInit() with custom storage error = %v", err)
	}

	// Note: The storage path parameter is used for colony config storagePath field,
	// but the actual config files are still created in HOME/.coral
	// This is expected behavior - the storagePath is for data, not config

	// Clean up project config if created
	defer os.Remove(filepath.Join(".", ".coral.yaml"))
}

func TestRunInit_DifferentEnvironments(t *testing.T) {
	tests := []struct {
		name        string
		appName     string
		environment string
	}{
		{
			name:        "dev environment",
			appName:     "my-app",
			environment: "dev",
		},
		{
			name:        "staging environment",
			appName:     "my-app",
			environment: "staging",
		},
		{
			name:        "production environment",
			appName:     "my-app",
			environment: "production",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory for this test
			tmpDir := t.TempDir()

			// Set HOME to tmpDir
			originalHome := os.Getenv("HOME")
			defer os.Setenv("HOME", originalHome)
			os.Setenv("HOME", tmpDir)

			originalXDG := os.Getenv("XDG_CONFIG_HOME")
			defer func() {
				if originalXDG != "" {
					os.Setenv("XDG_CONFIG_HOME", originalXDG)
				} else {
					os.Unsetenv("XDG_CONFIG_HOME")
				}
			}()
			os.Unsetenv("XDG_CONFIG_HOME")

			configDir := filepath.Join(tmpDir, ".coral")

			err := runInit(tt.appName, tt.environment, configDir, "")
			if err != nil {
				t.Errorf("runInit() error = %v", err)
			}

			// Verify global config was created
			globalConfigPath := filepath.Join(configDir, "config.yaml")
			if _, err := os.Stat(globalConfigPath); os.IsNotExist(err) {
				t.Error("Global config was not created")
			}

			// Load and verify the config
			loader, err := config.NewLoader()
			if err != nil {
				t.Fatalf("Failed to create loader: %v", err)
			}

			globalConfig, err := loader.LoadGlobalConfig()
			if err != nil {
				t.Fatalf("Failed to load global config: %v", err)
			}

			if globalConfig.DefaultColony == "" {
				t.Error("Default colony was not set")
			}

			// Clean up project config
			defer os.Remove(filepath.Join(".", ".coral.yaml"))
		})
	}
}

func TestNewInitCmd_ArgsValidation(t *testing.T) {
	cmd := NewInitCmd()

	// Test that the command requires exactly one arg
	// This is enforced by cobra.ExactArgs(1)
	err := cmd.Args(cmd, []string{})
	if err == nil {
		t.Error("Command should require exactly one argument")
	}

	err = cmd.Args(cmd, []string{"app1", "app2"})
	if err == nil {
		t.Error("Command should not accept more than one argument")
	}

	err = cmd.Args(cmd, []string{"valid-app"})
	if err != nil {
		t.Errorf("Command should accept one argument, got error: %v", err)
	}
}

func TestRunInit_VerifyCACreation(t *testing.T) {
	tmpDir := t.TempDir()

	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", tmpDir)

	originalXDG := os.Getenv("XDG_CONFIG_HOME")
	defer func() {
		if originalXDG != "" {
			os.Setenv("XDG_CONFIG_HOME", originalXDG)
		} else {
			os.Unsetenv("XDG_CONFIG_HOME")
		}
	}()
	os.Unsetenv("XDG_CONFIG_HOME")

	configDir := filepath.Join(tmpDir, ".coral")

	err := runInit("ca-test-app", "dev", configDir, "")
	if err != nil {
		t.Fatalf("runInit() error = %v", err)
	}

	// Verify CA directory was created
	// The colony ID format is <app-name>-<env>-<random>, so we need to find it
	coloniesDir := filepath.Join(configDir, "colonies")
	entries, err := os.ReadDir(coloniesDir)
	if err != nil {
		t.Fatalf("Failed to read colonies directory: %v", err)
	}

	if len(entries) == 0 {
		t.Fatal("No colony directory created")
	}

	// Check first colony (should be the only one)
	colonyDir := filepath.Join(coloniesDir, entries[0].Name())
	caDir := filepath.Join(colonyDir, "ca")

	if _, err := os.Stat(caDir); os.IsNotExist(err) {
		t.Error("CA directory was not created")
	}

	// Verify CA files exist
	expectedFiles := []string{
		"root-ca.crt",
		"root-ca.key",
		"server-intermediate.crt",
		"server-intermediate.key",
		"agent-intermediate.crt",
		"agent-intermediate.key",
		"policy-signing.crt",
		"policy-signing.key",
	}

	for _, file := range expectedFiles {
		filePath := filepath.Join(caDir, file)
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			t.Errorf("CA file %s was not created", file)
		}
	}

	// Clean up project config
	defer os.Remove(filepath.Join(".", ".coral.yaml"))
}
