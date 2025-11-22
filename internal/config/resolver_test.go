package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RFD 050 - ResolveWithSource tests.

func TestResolver_ResolveWithSource_EnvVar(t *testing.T) {
	tmpHome := t.TempDir()
	tmpProject := t.TempDir()

	// Set up CORAL_CONFIG to use tmp home.
	originalCoralConfig := os.Getenv("CORAL_CONFIG")
	_ = os.Setenv("CORAL_CONFIG", tmpHome) // TODO: errcheck
	defer func(key, value string) {
		_ = os.Setenv(key, value) // TODO: errcheck
	}("CORAL_CONFIG", originalCoralConfig)

	// Set CORAL_COLONY_ID env var.
	originalColonyID := os.Getenv("CORAL_COLONY_ID")
	_ = os.Setenv("CORAL_COLONY_ID", "env-colony-123") // TODO: errcheck
	defer func(key, value string) {
		_ = os.Setenv(key, value) // TODO: errcheck
	}("CORAL_COLONY_ID", originalColonyID)

	// Create resolver with temp project dir.
	loader, err := NewLoader()
	require.NoError(t, err)

	resolver := &Resolver{
		loader:     loader,
		projectDir: tmpProject,
	}

	// Resolve should return env var value.
	colonyID, source, err := resolver.ResolveWithSource()
	require.NoError(t, err)
	assert.Equal(t, "env-colony-123", colonyID)
	assert.Equal(t, "env", source.Type)
	assert.Equal(t, "CORAL_COLONY_ID", source.Path)
}

func TestResolver_ResolveWithSource_ProjectConfig(t *testing.T) {
	tmpHome := t.TempDir()
	tmpProject := t.TempDir()

	// Set up CORAL_CONFIG to use tmp home.
	originalCoralConfig := os.Getenv("CORAL_CONFIG")
	os.Setenv("CORAL_CONFIG", tmpHome)
	defer os.Setenv("CORAL_CONFIG", originalCoralConfig)

	// Ensure CORAL_COLONY_ID is not set.
	originalColonyID := os.Getenv("CORAL_COLONY_ID")
	os.Unsetenv("CORAL_COLONY_ID")
	defer os.Setenv("CORAL_COLONY_ID", originalColonyID)

	// Create project config.
	projectConfig := &ProjectConfig{
		Version:  "1",
		ColonyID: "project-colony-456",
	}
	err := SaveProjectConfig(tmpProject, projectConfig)
	require.NoError(t, err)

	// Create resolver.
	loader, err := NewLoader()
	require.NoError(t, err)

	resolver := &Resolver{
		loader:     loader,
		projectDir: tmpProject,
	}

	// Resolve should return project config value.
	colonyID, source, err := resolver.ResolveWithSource()
	require.NoError(t, err)
	assert.Equal(t, "project-colony-456", colonyID)
	assert.Equal(t, "project", source.Type)
	assert.Contains(t, source.Path, ".coral/config.yaml")
}

func TestResolver_ResolveWithSource_GlobalDefault(t *testing.T) {
	tmpHome := t.TempDir()
	tmpProject := t.TempDir()

	// Set up CORAL_CONFIG to use tmp home.
	originalCoralConfig := os.Getenv("CORAL_CONFIG")
	_ = os.Setenv("CORAL_CONFIG", tmpHome) // TODO: errcheck
	defer func(key, value string) {
		_ = os.Setenv(key, value)
	}("CORAL_CONFIG", originalCoralConfig)

	// Ensure CORAL_COLONY_ID is not set.
	originalColonyID := os.Getenv("CORAL_COLONY_ID")
	_ = os.Unsetenv("CORAL_COLONY_ID") // TODO: errcheck
	defer func(key, value string) {
		_ = os.Setenv(key, value) // TODO: errcheck
	}("CORAL_COLONY_ID", originalColonyID)

	// Create loader and global config.
	loader, err := NewLoader()
	require.NoError(t, err)

	globalConfig := &GlobalConfig{
		Version:       "1",
		DefaultColony: "global-colony-789",
	}
	err = loader.SaveGlobalConfig(globalConfig)
	require.NoError(t, err)

	resolver := &Resolver{
		loader:     loader,
		projectDir: tmpProject, // No project config.
	}

	// Resolve should return global default.
	colonyID, source, err := resolver.ResolveWithSource()
	require.NoError(t, err)
	assert.Equal(t, "global-colony-789", colonyID)
	assert.Equal(t, "global", source.Type)
	assert.Contains(t, source.Path, "config.yaml")
}

func TestResolver_ResolveWithSource_NoColony(t *testing.T) {
	tmpHome := t.TempDir()
	tmpProject := t.TempDir()

	// Set up CORAL_CONFIG to use tmp home.
	originalCoralConfig := os.Getenv("CORAL_CONFIG")
	os.Setenv("CORAL_CONFIG", tmpHome)
	defer os.Setenv("CORAL_CONFIG", originalCoralConfig)

	// Ensure CORAL_COLONY_ID is not set.
	originalColonyID := os.Getenv("CORAL_COLONY_ID")
	os.Unsetenv("CORAL_COLONY_ID")
	defer os.Setenv("CORAL_COLONY_ID", originalColonyID)

	// Create resolver without any config.
	loader, err := NewLoader()
	require.NoError(t, err)

	resolver := &Resolver{
		loader:     loader,
		projectDir: tmpProject,
	}

	// Resolve should return error.
	_, _, err = resolver.ResolveWithSource()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no colony configured")
}

func TestResolver_ResolveWithSource_Priority(t *testing.T) {
	tmpHome := t.TempDir()
	tmpProject := t.TempDir()

	// Set up CORAL_CONFIG to use tmp home.
	originalCoralConfig := os.Getenv("CORAL_CONFIG")
	_ = os.Setenv("CORAL_CONFIG", tmpHome)
	defer os.Setenv("CORAL_CONFIG", originalCoralConfig)

	// Ensure CORAL_COLONY_ID is not set initially.
	originalColonyID := os.Getenv("CORAL_COLONY_ID")
	_ = os.Unsetenv("CORAL_COLONY_ID")
	defer os.Setenv("CORAL_COLONY_ID", originalColonyID)

	// Create all config sources.
	loader, err := NewLoader()
	require.NoError(t, err)

	// Global default.
	globalConfig := &GlobalConfig{
		Version:       "1",
		DefaultColony: "global-colony",
	}
	err = loader.SaveGlobalConfig(globalConfig)
	require.NoError(t, err)

	// Project config.
	projectConfig := &ProjectConfig{
		Version:  "1",
		ColonyID: "project-colony",
	}
	err = SaveProjectConfig(tmpProject, projectConfig)
	require.NoError(t, err)

	resolver := &Resolver{
		loader:     loader,
		projectDir: tmpProject,
	}

	// Without env var, project should win over global.
	colonyID, source, err := resolver.ResolveWithSource()
	require.NoError(t, err)
	assert.Equal(t, "project-colony", colonyID)
	assert.Equal(t, "project", source.Type)

	// With env var, env should win over project.
	os.Setenv("CORAL_COLONY_ID", "env-colony")

	colonyID, source, err = resolver.ResolveWithSource()
	require.NoError(t, err)
	assert.Equal(t, "env-colony", colonyID)
	assert.Equal(t, "env", source.Type)
}

func TestResolutionSource_String(t *testing.T) {
	tests := []struct {
		source   ResolutionSource
		expected string
	}{
		{ResolutionSource{Type: "env", Path: "CORAL_COLONY_ID"}, "env:CORAL_COLONY_ID"},
		{ResolutionSource{Type: "project", Path: "/path/to/.coral/config.yaml"}, "project:/path/to/.coral/config.yaml"},
		{ResolutionSource{Type: "global", Path: "~/.coral/config.yaml"}, "global:~/.coral/config.yaml"},
		{ResolutionSource{Type: "unknown", Path: ""}, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.source.String())
		})
	}
}

func TestResolver_ResolveColonyID_UsesResolveWithSource(t *testing.T) {
	tmpHome := t.TempDir()
	tmpProject := t.TempDir()

	// Set up CORAL_CONFIG to use tmp home.
	originalCoralConfig := os.Getenv("CORAL_CONFIG")
	os.Setenv("CORAL_CONFIG", tmpHome)
	defer os.Setenv("CORAL_CONFIG", originalCoralConfig)

	// Ensure CORAL_COLONY_ID is not set.
	originalColonyID := os.Getenv("CORAL_COLONY_ID")
	os.Unsetenv("CORAL_COLONY_ID")
	defer os.Setenv("CORAL_COLONY_ID", originalColonyID)

	// Create loader and global config.
	loader, err := NewLoader()
	require.NoError(t, err)

	globalConfig := &GlobalConfig{
		Version:       "1",
		DefaultColony: "test-colony",
	}
	err = loader.SaveGlobalConfig(globalConfig)
	require.NoError(t, err)

	// Create a colony config so it exists.
	colonyConfig := &ColonyConfig{
		Version:         "1",
		ColonyID:        "test-colony",
		ApplicationName: "test-app",
		CreatedAt:       time.Now(),
	}
	err = loader.SaveColonyConfig(colonyConfig)
	require.NoError(t, err)

	resolver := &Resolver{
		loader:     loader,
		projectDir: tmpProject,
	}

	// ResolveColonyID should return the same result as ResolveWithSource.
	colonyID, err := resolver.ResolveColonyID()
	require.NoError(t, err)
	assert.Equal(t, "test-colony", colonyID)

	colonyIDWithSource, _, err := resolver.ResolveWithSource()
	require.NoError(t, err)
	assert.Equal(t, colonyID, colonyIDWithSource)
}
