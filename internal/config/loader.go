// Package config provides configuration loading and management.
package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/coral-mesh/coral/internal/constants"
	"github.com/coral-mesh/coral/internal/privilege"
)

// Loader handles loading and saving configuration files.
type Loader struct {
	homeDir string
}

// NewLoader creates a new config loader.
// The base directory is resolved in this order:
//  1. CORAL_CONFIG environment variable (RFD 050).
//  2. User home directory (~/).
//  3. /tmp/coral-fallback (containerized environments without a home dir).
//
// The loader never returns an error. In minimal containers (e.g., scratch, distroless)
// where no home directory exists, the fallback ensures LoadGlobalConfig still returns
// defaults with env var overrides applied.
func NewLoader() (*Loader, error) {
	// Check for CORAL_CONFIG env var override (RFD 050).
	if baseDir := os.Getenv("CORAL_CONFIG"); baseDir != "" {
		return &Loader{
			homeDir: baseDir,
		}, nil
	}

	homeDir, err := os.UserHomeDir()
	if err == nil {
		return &Loader{
			homeDir: homeDir,
		}, nil
	}

	// Fallback for containerized environments without a home directory.
	// Config files won't exist here, so Load* methods return defaults + env overrides.
	return &Loader{
		homeDir: "/tmp/coral-fallback",
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
// Applies environment variable overrides for layered configuration.
func (l *Loader) LoadGlobalConfig() (*GlobalConfig, error) {
	path := l.GlobalConfigPath()

	var config *GlobalConfig
	// Load from file or use default
	if _, err := os.Stat(path); os.IsNotExist(err) {
		config = DefaultGlobalConfig()
	} else {
		//nolint:gosec // G304: Path is from trusted config directory.
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read global config: %w", err)
		}

		if err := yaml.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("failed to parse global config: %w", err)
		}
	}

	// Apply environment variable overrides (layered configuration).
	if err := MergeFromEnv(config); err != nil {
		return nil, fmt.Errorf("failed to load environment variables: %w", err)
	}

	return config, nil
}

// SaveGlobalConfig saves the global configuration.
func (l *Loader) SaveGlobalConfig(config *GlobalConfig) error {
	path := l.GlobalConfigPath()

	// Ensure directory exists
	dir := filepath.Dir(path)
	//nolint:gosec // G301: Directory needs standard permissions for traversal
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
	//nolint:gosec // G306: Global config file is not sensitive
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
	// 1. Check user-specific config: ~/.coral/colonies/<colony-id>/config.yaml
	userPath := l.ColonyConfigPath(colonyID)
	if _, err := os.Stat(userPath); err == nil {
		return loadColonyConfigFromFile(userPath)
	}

	// 2. Check system-wide multi-colony config: /etc/coral/colonies/<colony-id>.yaml
	systemMultiPath := filepath.Join("/etc/coral/colonies", fmt.Sprintf("%s.yaml", colonyID))
	if _, err := os.Stat(systemMultiPath); err == nil {
		return loadColonyConfigFromFile(systemMultiPath)
	}

	// 3. Check system-wide single-colony config: /etc/coral/colony.yaml
	// Only if it matches the requested colonyID
	systemSinglePath := "/etc/coral/colony.yaml"
	if _, err := os.Stat(systemSinglePath); err == nil {
		cfg, err := loadColonyConfigFromFile(systemSinglePath)
		if err == nil && cfg.ColonyID == colonyID {
			return cfg, nil
		}
		// If it exists but fails to load or ID doesn't match, we ignore it here
		// and fall through to "not found" error.
	}

	// 4. Config-less mode: if no config file exists but env vars provide the
	// necessary configuration, create a synthetic config and merge env vars.
	// This enables CLI usage with only CORAL_* env vars set (e.g. e2e tests).
	synthetic := DefaultColonyConfig(colonyID, colonyID, "remote")
	if err := MergeFromEnv(synthetic); err != nil {
		return nil, fmt.Errorf("colony %q not found and failed to merge env vars: %w", colonyID, err)
	}

	// Only use config-less mode if env vars actually provided useful config
	// (at minimum, an endpoint to connect to).
	if synthetic.Remote.Endpoint != "" {
		return synthetic, nil
	}

	return nil, fmt.Errorf("colony %q not found", colonyID)
}

func loadColonyConfigFromFile(path string) (*ColonyConfig, error) {
	//nolint:gosec // G304: Path is from trusted config directory.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read colony config from %s: %w", path, err)
	}

	var config ColonyConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse colony config from %s: %w", path, err)
	}

	if err := MergeFromEnv(&config); err != nil {
		return nil, fmt.Errorf("failed to load environment variables for colony config: %w", err)
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

	//nolint:gosec // G304: Path is from trusted project directory.
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
	//nolint:gosec // G301: Directory needs standard permissions for traversal
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create .coral directory: %w", err)
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal project config: %w", err)
	}

	//nolint:gosec // G306: Project config file is not sensitive
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write project config: %w", err)
	}

	return nil
}
