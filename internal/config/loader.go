package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/coral-io/coral/internal/constants"
	"github.com/coral-io/coral/internal/privilege"
)

// Loader handles loading and saving configuration files.
type Loader struct {
	homeDir string
}

// NewLoader creates a new config loader.
// The base directory can be overridden via CORAL_CONFIG environment variable.
func NewLoader() (*Loader, error) {
	// Check for CORAL_CONFIG env var override (RFD 050).
	if baseDir := os.Getenv("CORAL_CONFIG"); baseDir != "" {
		return &Loader{
			homeDir: baseDir,
		}, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user home directory: %w", err)
	}

	return &Loader{
		homeDir: homeDir,
	}, nil
}

// GlobalConfigPath returns the path to the global config file.
func (l *Loader) GlobalConfigPath() string {
	return filepath.Join(l.homeDir, constants.DefaultDir, constants.ConfigFile)
}

// ColonyConfigPath returns the path to a colony's config file.
// Config is stored at ~/.coral/colonies/<colony-id>/config.yaml
func (l *Loader) ColonyConfigPath(colonyID string) string {
	return filepath.Join(l.homeDir, constants.DefaultDir, "colonies", colonyID, "config.yaml")
}

// ColoniesDir returns the path to the colonies directory.
func (l *Loader) ColoniesDir() string {
	return filepath.Join(l.homeDir, constants.DefaultDir, "colonies")
}

// ColonyDir returns the path to a specific colony's directory.
// This is where the colony's CA and other data are stored.
func (l *Loader) ColonyDir(colonyID string) string {
	return filepath.Join(l.homeDir, constants.DefaultDir, "colonies", colonyID)
}

// LoadGlobalConfig loads the global configuration.
// Returns default config if file doesn't exist.
func (l *Loader) LoadGlobalConfig() (*GlobalConfig, error) {
	path := l.GlobalConfigPath()

	// Return default if file doesn't exist
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return DefaultGlobalConfig(), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read global config: %w", err)
	}

	var config GlobalConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse global config: %w", err)
	}

	return &config, nil
}

// SaveGlobalConfig saves the global configuration.
func (l *Loader) SaveGlobalConfig(config *GlobalConfig) error {
	path := l.GlobalConfigPath()

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Fix directory ownership if running as root via sudo.
	if privilege.IsRoot() {
		if err := privilege.FixFileOwnership(dir); err != nil {
			log.Printf("warning: failed to fix directory ownership: %v", err)
		}
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal global config: %w", err)
	}

	// Global config is not as sensitive, use 0644
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write global config: %w", err)
	}

	// Fix file ownership if running as root via sudo.
	if privilege.IsRoot() {
		if err := privilege.FixFileOwnership(path); err != nil {
			log.Printf("warning: failed to fix file ownership: %v", err)
		}
	}

	return nil
}

// LoadColonyConfig loads a colony configuration by ID.
func (l *Loader) LoadColonyConfig(colonyID string) (*ColonyConfig, error) {
	path := l.ColonyConfigPath(colonyID)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("colony %q not found", colonyID)
		}
		return nil, fmt.Errorf("failed to read colony config: %w", err)
	}

	var config ColonyConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse colony config: %w", err)
	}

	return &config, nil
}

// SaveColonyConfig saves a colony configuration.
// Uses 0600 permissions to protect secrets.
func (l *Loader) SaveColonyConfig(config *ColonyConfig) error {
	path := l.ColonyConfigPath(config.ColonyID)

	// Ensure colonies directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create colonies directory: %w", err)
	}

	// Fix directory ownership if running as root via sudo.
	if privilege.IsRoot() {
		if err := privilege.FixFileOwnership(dir); err != nil {
			log.Printf("warning: failed to fix directory ownership: %v", err)
		}
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal colony config: %w", err)
	}

	// Use 0600 for colony configs (contain secrets)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write colony config: %w", err)
	}

	// Fix file ownership if running as root via sudo.
	if privilege.IsRoot() {
		if err := privilege.FixFileOwnership(path); err != nil {
			log.Printf("warning: failed to fix file ownership: %v", err)
		}
	}

	return nil
}

// ListColonies returns all configured colony IDs.
// Looks for directories containing config.yaml files.
func (l *Loader) ListColonies() ([]string, error) {
	coloniesDir := l.ColoniesDir()

	// Return empty list if directory doesn't exist.
	if _, err := os.Stat(coloniesDir); os.IsNotExist(err) {
		return []string{}, nil
	}

	entries, err := os.ReadDir(coloniesDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read colonies directory: %w", err)
	}

	var colonyIDs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Check if this directory has a config.yaml file.
		configPath := filepath.Join(coloniesDir, entry.Name(), "config.yaml")
		if _, err := os.Stat(configPath); err == nil {
			colonyIDs = append(colonyIDs, entry.Name())
		}
	}

	return colonyIDs, nil
}

// DeleteColonyConfig removes a colony configuration file.
func (l *Loader) DeleteColonyConfig(colonyID string) error {
	path := l.ColonyConfigPath(colonyID)

	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("colony %q not found", colonyID)
		}
		return fmt.Errorf("failed to delete colony config: %w", err)
	}

	return nil
}

// DeleteColonyDir removes an entire colony directory including config, CA, and data (RFD 050).
func (l *Loader) DeleteColonyDir(colonyID string) error {
	colonyDir := l.ColonyDir(colonyID)

	// Verify colony exists.
	if _, err := os.Stat(colonyDir); os.IsNotExist(err) {
		return fmt.Errorf("colony %q not found", colonyID)
	}

	if err := os.RemoveAll(colonyDir); err != nil {
		return fmt.Errorf("failed to delete colony directory: %w", err)
	}

	return nil
}

// ValidateAll validates all colony configs and returns validation errors per colony (RFD 050).
// Returns a map of colonyID to validation error (nil if valid).
func (l *Loader) ValidateAll() (map[string]error, error) {
	colonyIDs, err := l.ListColonies()
	if err != nil {
		return nil, fmt.Errorf("failed to list colonies: %w", err)
	}

	results := make(map[string]error)
	for _, colonyID := range colonyIDs {
		cfg, err := l.LoadColonyConfig(colonyID)
		if err != nil {
			results[colonyID] = fmt.Errorf("failed to load config: %w", err)
			continue
		}

		// Validate the colony config.
		if err := ValidateColonyConfig(cfg); err != nil {
			results[colonyID] = err
		} else {
			results[colonyID] = nil
		}
	}

	return results, nil
}

// ValidateColonyConfig performs validation on a colony config (RFD 050).
func ValidateColonyConfig(cfg *ColonyConfig) error {
	// Validate colony ID.
	if cfg.ColonyID == "" {
		return fmt.Errorf("colony_id is required")
	}

	// Validate application name.
	if cfg.ApplicationName == "" {
		return fmt.Errorf("application_name is required")
	}

	// Validate mesh subnet if set.
	if cfg.WireGuard.MeshNetworkIPv4 != "" {
		if _, err := ValidateMeshSubnet(cfg.WireGuard.MeshNetworkIPv4); err != nil {
			return fmt.Errorf("invalid mesh_network_ipv4: %w", err)
		}
	}

	// Validate WireGuard port.
	if cfg.WireGuard.Port < 0 || cfg.WireGuard.Port > 65535 {
		return fmt.Errorf("invalid wireguard port: %d", cfg.WireGuard.Port)
	}

	// Validate MTU.
	if cfg.WireGuard.MTU < 0 || cfg.WireGuard.MTU > 9000 {
		return fmt.Errorf("invalid MTU: %d (must be 0-9000)", cfg.WireGuard.MTU)
	}

	return nil
}

// LoadProjectConfig loads the project-local configuration.
// Looks for .coral/config.yaml in the current directory.
func LoadProjectConfig(projectDir string) (*ProjectConfig, error) {
	path := filepath.Join(projectDir, constants.DefaultDir, constants.ConfigFile)

	// Return nil if file doesn't exist (project config is optional)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read project config: %w", err)
	}

	var config ProjectConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse project config: %w", err)
	}

	return &config, nil
}

// SaveProjectConfig saves the project-local configuration.
func SaveProjectConfig(projectDir string, config *ProjectConfig) error {
	dir := filepath.Join(projectDir, constants.DefaultDir)
	path := filepath.Join(dir, constants.ConfigFile)

	// Ensure directory exists
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create .coral directory: %w", err)
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal project config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write project config: %w", err)
	}

	return nil
}
