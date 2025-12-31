package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Layer represents a configuration layer source.
type Layer string

const (
	// LayerDefaults represents default configuration values.
	LayerDefaults Layer = "defaults"

	// LayerFile represents configuration from a file.
	LayerFile Layer = "file"

	// LayerEnv represents configuration from environment variables.
	LayerEnv Layer = "env"

	// LayerFlags represents configuration from command-line flags.
	LayerFlags Layer = "flags"
)

// LayeredLoader provides layered configuration loading.
// Configuration is loaded in the following order:
// 1. Defaults - hardcoded default values
// 2. File - configuration file (YAML)
// 3. Environment - environment variables
// 4. Flags - command-line flags (optional, application-specific)
//
// Each layer overrides values from previous layers.
type LayeredLoader struct {
	enabledLayers map[Layer]bool
}

// NewLayeredLoader creates a new layered configuration loader.
// By default, all layers except flags are enabled.
func NewLayeredLoader() *LayeredLoader {
	return &LayeredLoader{
		enabledLayers: map[Layer]bool{
			LayerDefaults: true,
			LayerFile:     true,
			LayerEnv:      true,
			LayerFlags:    false, // Flags are application-specific
		},
	}
}

// EnableLayer enables a specific configuration layer.
func (l *LayeredLoader) EnableLayer(layer Layer) {
	l.enabledLayers[layer] = true
}

// DisableLayer disables a specific configuration layer.
func (l *LayeredLoader) DisableLayer(layer Layer) {
	l.enabledLayers[layer] = false
}

// LoadAgentConfig loads agent configuration with layered precedence.
//
// Layer precedence (later layers override earlier ones):
// 1. Defaults - DefaultAgentConfig()
// 2. File - Load from configPath (if provided and exists)
// 3. Environment - Load from environment variables
// 4. Flags - Not implemented (application-specific)
func (l *LayeredLoader) LoadAgentConfig(configPath string) (*AgentConfig, error) {
	var cfg *AgentConfig

	// Layer 1: Defaults
	if l.enabledLayers[LayerDefaults] {
		cfg = DefaultAgentConfig()
	} else {
		cfg = &AgentConfig{}
	}

	// Layer 2: File
	if l.enabledLayers[LayerFile] && configPath != "" {
		if err := l.mergeFromFile(cfg, configPath); err != nil {
			// If file doesn't exist, it's not an error - just skip this layer
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("failed to load config from file: %w", err)
			}
		}
	}

	// Layer 3: Environment
	if l.enabledLayers[LayerEnv] {
		if err := LoadFromEnv(cfg); err != nil {
			return nil, fmt.Errorf("failed to load config from environment: %w", err)
		}
	}

	// Layer 4: Flags (not implemented - application-specific)
	// Applications can further modify the config after loading

	return cfg, nil
}

// LoadGlobalConfig loads global configuration with layered precedence.
func (l *LayeredLoader) LoadGlobalConfig(configPath string) (*GlobalConfig, error) {
	var cfg *GlobalConfig

	// Layer 1: Defaults
	if l.enabledLayers[LayerDefaults] {
		cfg = DefaultGlobalConfig()
	} else {
		cfg = &GlobalConfig{}
	}

	// Layer 2: File
	if l.enabledLayers[LayerFile] && configPath != "" {
		if err := l.mergeFromFile(cfg, configPath); err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("failed to load config from file: %w", err)
			}
		}
	}

	// Layer 3: Environment
	if l.enabledLayers[LayerEnv] {
		if err := LoadFromEnv(cfg); err != nil {
			return nil, fmt.Errorf("failed to load config from environment: %w", err)
		}
	}

	return cfg, nil
}

// LoadColonyConfig loads colony configuration with layered precedence.
func (l *LayeredLoader) LoadColonyConfig(configPath string, colonyID, appName, env string) (*ColonyConfig, error) {
	var cfg *ColonyConfig

	// Layer 1: Defaults
	if l.enabledLayers[LayerDefaults] {
		cfg = DefaultColonyConfig(colonyID, appName, env)
	} else {
		cfg = &ColonyConfig{}
	}

	// Layer 2: File
	if l.enabledLayers[LayerFile] && configPath != "" {
		if err := l.mergeFromFile(cfg, configPath); err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("failed to load config from file: %w", err)
			}
		}
	}

	// Layer 3: Environment
	if l.enabledLayers[LayerEnv] {
		if err := LoadFromEnv(cfg); err != nil {
			return nil, fmt.Errorf("failed to load config from environment: %w", err)
		}
	}

	return cfg, nil
}

// LoadProjectConfig loads project configuration with layered precedence.
func (l *LayeredLoader) LoadProjectConfig(configPath string, colonyID string) (*ProjectConfig, error) {
	var cfg *ProjectConfig

	// Layer 1: Defaults
	if l.enabledLayers[LayerDefaults] {
		cfg = DefaultProjectConfig(colonyID)
	} else {
		cfg = &ProjectConfig{}
	}

	// Layer 2: File
	if l.enabledLayers[LayerFile] && configPath != "" {
		if err := l.mergeFromFile(cfg, configPath); err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("failed to load config from file: %w", err)
			}
		}
	}

	// Layer 3: Environment
	if l.enabledLayers[LayerEnv] {
		if err := LoadFromEnv(cfg); err != nil {
			return nil, fmt.Errorf("failed to load config from environment: %w", err)
		}
	}

	return cfg, nil
}

// mergeFromFile loads configuration from a YAML file and merges it into cfg.
func (l *LayeredLoader) mergeFromFile(cfg interface{}, filePath string) error {
	// #nosec G304 -- filePath is provided by the application configuration system, not user input.
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("failed to parse YAML: %w", err)
	}

	return nil
}

// ValidateConfig validates a configuration and returns detailed errors.
func (l *LayeredLoader) ValidateConfig(cfg Validator) error {
	return cfg.Validate()
}
