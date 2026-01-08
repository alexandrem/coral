package helpers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// CLITestEnv holds environment configuration for CLI tests.
// It creates an isolated environment with temporary directories
// for config and data to prevent tests from interfering with each other.
type CLITestEnv struct {
	ColonyID       string
	ColonyEndpoint string
	ConfigDir      string // Path to .coral directory
	TempDir        string // Root temp directory
	HomeDir        string // Simulated HOME directory
}

// SetupCLIEnv prepares an isolated environment for CLI testing.
// Creates temporary directories and sets up required structure.
func SetupCLIEnv(ctx context.Context, colonyID, colonyEndpoint string) (*CLITestEnv, error) {
	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "coral-cli-test-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Create home directory structure
	homeDir := filepath.Join(tempDir, "home")
	configDir := filepath.Join(homeDir, ".coral")
	coloniesDir := filepath.Join(configDir, "colonies")

	// Create directory structure
	if err := os.MkdirAll(coloniesDir, 0755); err != nil {
		os.RemoveAll(tempDir)
		return nil, fmt.Errorf("failed to create config directories: %w", err)
	}

	env := &CLITestEnv{
		ColonyID:       colonyID,
		ColonyEndpoint: colonyEndpoint,
		ConfigDir:      configDir,
		TempDir:        tempDir,
		HomeDir:        homeDir,
	}

	return env, nil
}

// Cleanup removes all temporary directories created by this environment.
func (e *CLITestEnv) Cleanup() error {
	if e.TempDir != "" {
		return os.RemoveAll(e.TempDir)
	}
	return nil
}

// EnvVars returns environment variables map for CLI commands.
// These variables tell the CLI where to find config and which colony to use.
func (e *CLITestEnv) EnvVars() map[string]string {
	env := map[string]string{
		"HOME": e.HomeDir,
	}

	// Add colony-specific vars if set
	if e.ColonyID != "" {
		env["CORAL_COLONY_ID"] = e.ColonyID
	}

	if e.ColonyEndpoint != "" {
		env["CORAL_COLONY_ENDPOINT"] = e.ColonyEndpoint
	}

	return env
}

// WithColonyID returns a copy of environment variables with colony ID set.
func (e *CLITestEnv) WithColonyID(colonyID string) map[string]string {
	env := e.EnvVars()
	env["CORAL_COLONY_ID"] = colonyID
	return env
}

// WithEndpoint returns a copy of environment variables with custom endpoint.
func (e *CLITestEnv) WithEndpoint(endpoint string) map[string]string {
	env := e.EnvVars()
	env["CORAL_COLONY_ENDPOINT"] = endpoint
	return env
}

// WithEnv returns a copy of environment variables with additional custom vars.
func (e *CLITestEnv) WithEnv(customEnv map[string]string) map[string]string {
	env := e.EnvVars()
	for key, value := range customEnv {
		env[key] = value
	}
	return env
}

// ConfigPath returns the path to the .coral config directory.
func (e *CLITestEnv) ConfigPath() string {
	return e.ConfigDir
}

// ColoniesPath returns the path to the colonies directory.
func (e *CLITestEnv) ColoniesPath() string {
	return filepath.Join(e.ConfigDir, "colonies")
}

// ColonyPath returns the path to a specific colony's directory.
func (e *CLITestEnv) ColonyPath(colonyID string) string {
	return filepath.Join(e.ColoniesPath(), colonyID)
}

// CreateColonyDir creates a colony directory in the test environment.
// This is useful for testing config file operations.
func (e *CLITestEnv) CreateColonyDir(colonyID string) error {
	colonyPath := e.ColonyPath(colonyID)
	return os.MkdirAll(colonyPath, 0755)
}

// WriteConfigFile writes a config file to the specified path in the test environment.
func (e *CLITestEnv) WriteConfigFile(relativePath string, content []byte) error {
	filePath := filepath.Join(e.ConfigDir, relativePath)

	// Ensure parent directory exists
	parentDir := filepath.Dir(filePath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	return os.WriteFile(filePath, content, 0644)
}

// ReadConfigFile reads a config file from the test environment.
func (e *CLITestEnv) ReadConfigFile(relativePath string) ([]byte, error) {
	filePath := filepath.Join(e.ConfigDir, relativePath)
	return os.ReadFile(filePath)
}

// FileExists checks if a file exists in the test environment.
func (e *CLITestEnv) FileExists(relativePath string) bool {
	filePath := filepath.Join(e.ConfigDir, relativePath)
	_, err := os.Stat(filePath)
	return err == nil
}
