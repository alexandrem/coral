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

// ResolveColonyID determines which colony to use.
// Priority: CORAL_COLONY_ID env var > project config > global default > error
func (r *Resolver) ResolveColonyID() (string, error) {
	// 1. Check environment variable
	if colonyID := os.Getenv("CORAL_COLONY_ID"); colonyID != "" {
		return colonyID, nil
	}

	// 2. Check project-local config
	projectConfig, err := LoadProjectConfig(r.projectDir)
	if err != nil {
		return "", err
	}
	if projectConfig != nil && projectConfig.ColonyID != "" {
		return projectConfig.ColonyID, nil
	}

	// 3. Check global default
	globalConfig, err := r.loader.LoadGlobalConfig()
	if err != nil {
		return "", err
	}
	if globalConfig.DefaultColony != "" {
		return globalConfig.DefaultColony, nil
	}

	return "", fmt.Errorf("no colony configured: run 'coral init' or set CORAL_COLONY_ID")
}

// ResolveConfig loads and merges configuration for a colony.
func (r *Resolver) ResolveConfig(colonyID string) (*ResolvedConfig, error) {
	// Load all config sources
	globalConfig, err := r.loader.LoadGlobalConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load global config: %w", err)
	}

	colonyConfig, err := r.loader.LoadColonyConfig(colonyID)
	if err != nil {
		return nil, fmt.Errorf("failed to load colony config: %w", err)
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

	// Apply environment variable overrides
	if secret := os.Getenv("CORAL_COLONY_SECRET"); secret != "" {
		resolved.ColonySecret = secret
	} else {
		resolved.ColonySecret = colonyConfig.ColonySecret
	}

	if discoveryURL := os.Getenv("CORAL_DISCOVERY_ENDPOINT"); discoveryURL != "" {
		resolved.DiscoveryURL = discoveryURL
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
