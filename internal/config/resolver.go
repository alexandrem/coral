package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// Resolver handles configuration resolution with priority order.
//
// Order:
//  1. Environment variables (highest)
//  2. Project-local config
//  3. Per-colony config
//  4. Global config
//  5. Defaults (lowest)
type Resolver struct {
	loader     *Loader
	projectDir string
}

// NewResolver creates a new config resolver.
func NewResolver() (*Resolver, error) {
	loader, err := NewLoader()
	if err != nil {
		return nil, err
	}

	// Get current working directory for project config
	projectDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get working directory: %w", err)
	}

	return &Resolver{
		loader:     loader,
		projectDir: projectDir,
	}, nil
}

// ResolutionSource describes where a colony ID was resolved from (RFD 050).
type ResolutionSource struct {
	Type string // "env", "project", "global"
	Path string // Full path or env var name
}

// String returns a human-readable description of the resolution source.
func (s ResolutionSource) String() string {
	switch s.Type {
	case "env":
		return fmt.Sprintf("env:%s", s.Path)
	case "project":
		return fmt.Sprintf("project:%s", s.Path)
	case "global":
		return fmt.Sprintf("global:%s", s.Path)
	default:
		return "unknown"
	}
}

// ResolveColonyID determines which colony to use.
// Priority: CORAL_COLONY_ID env var > project config > global default > error
func (r *Resolver) ResolveColonyID() (string, error) {
	colonyID, _, err := r.ResolveWithSource()
	return colonyID, err
}

// ResolveWithSource determines which colony to use and returns the resolution source (RFD 050).
// Priority: CORAL_COLONY_ID env var > project config > global default > error
// Returns: (colonyID, source, error)
func (r *Resolver) ResolveWithSource() (string, ResolutionSource, error) {
	// 1. Check environment variable.
	if colonyID := os.Getenv("CORAL_COLONY_ID"); colonyID != "" {
		return colonyID, ResolutionSource{Type: "env", Path: "CORAL_COLONY_ID"}, nil
	}

	// 2. Check project-local config.
	projectConfig, err := LoadProjectConfig(r.projectDir)
	if err != nil {
		return "", ResolutionSource{}, err
	}
	if projectConfig != nil && projectConfig.ColonyID != "" {
		projectConfigPath := filepath.Join(r.projectDir, ".coral/config.yaml")
		return projectConfig.ColonyID, ResolutionSource{Type: "project", Path: projectConfigPath}, nil
	}

	// 3. Check global default.
	globalConfig, err := r.loader.LoadGlobalConfig()
	if err != nil {
		return "", ResolutionSource{}, err
	}
	if globalConfig.DefaultColony != "" {
		globalConfigPath := r.loader.GlobalConfigPath()
		return globalConfig.DefaultColony, ResolutionSource{Type: "global", Path: globalConfigPath}, nil
	}

	return "", ResolutionSource{}, fmt.Errorf("no colony configured: run 'coral init' or set CORAL_COLONY_ID")
}

// ResolveConfig loads and merges configuration for a colony.
// For containerized agents, supports "config-less" mode where only CORAL_COLONY_ID
// and CORAL_CA_FINGERPRINT env vars are required (no colony config file needed).
func (r *Resolver) ResolveConfig(colonyID string) (*ResolvedConfig, error) {
	// Load all config sources
	globalConfig, err := r.loader.LoadGlobalConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load global config: %w", err)
	}

	colonyConfig, err := r.loader.LoadColonyConfig(colonyID)
	if err != nil {
		// Config-less mode: if colony config file doesn't exist but we have
		// CORAL_CA_FINGERPRINT env var, create a synthetic config for agents.
		// This supports containerized agents that bootstrap via Discovery (RFD 048).
		if os.Getenv("CORAL_CA_FINGERPRINT") != "" {
			colonyConfig = DefaultColonyConfig(colonyID, colonyID, "container")
		} else {
			return nil, fmt.Errorf("failed to load colony config: %w", err)
		}
	}

	projectConfig, err := LoadProjectConfig(r.projectDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load project config: %w", err)
	}

	// Build resolved config with priority
	resolved := &ResolvedConfig{
		ColonyID:        colonyConfig.ColonyID,
		ApplicationName: colonyConfig.ApplicationName,
		Environment:     colonyConfig.Environment,
		WireGuard:       colonyConfig.WireGuard,
		StoragePath:     colonyConfig.StoragePath,
		DiscoveryURL:    globalConfig.Discovery.Endpoint,
	}

	// Resolve mesh subnet with environment variable override support
	meshSubnet, colonyIP, err := ResolveMeshSubnet(colonyConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve mesh subnet: %w", err)
	}
	resolved.WireGuard.MeshNetworkIPv4 = meshSubnet
	resolved.WireGuard.MeshIPv4 = colonyIP

	if discoveryURL := os.Getenv("CORAL_DISCOVERY_ENDPOINT"); discoveryURL != "" {
		resolved.DiscoveryURL = discoveryURL
	}

	if storagePath := os.Getenv("CORAL_STORAGE_PATH"); storagePath != "" {
		resolved.StoragePath = storagePath
	}

	if storagePath := os.Getenv("CORAL_STORAGE_PATH"); storagePath != "" {
		resolved.StoragePath = storagePath
	}

	// Apply project config overrides
	if projectConfig != nil {
		if projectConfig.Dashboard.Port > 0 {
			resolved.Dashboard = projectConfig.Dashboard
		}
		if projectConfig.Storage.Path != "" {
			// Make storage path absolute if relative
			if !filepath.IsAbs(projectConfig.Storage.Path) {
				resolved.StoragePath = filepath.Join(r.projectDir, projectConfig.Storage.Path)
			} else {
				resolved.StoragePath = projectConfig.Storage.Path
			}
		}
	}

	return resolved, nil
}

// GetLoader returns the underlying config loader.
func (r *Resolver) GetLoader() *Loader {
	return r.loader
}
