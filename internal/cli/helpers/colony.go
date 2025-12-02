package helpers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"connectrpc.com/connect"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/config"
)

// Colony client helpers for CLI commands.
//
// This package provides shared utilities for connecting to colonies, eliminating
// duplication across CLI commands (debug, agent, status, duckdb, etc.).
//
// Migration: Previously, each command had its own getColonyURL/getColonyClient
// implementation. These have been consolidated here to avoid duplication and
// ensure consistent connection behavior across all CLI commands.

// GetColonyURL returns the colony URL using config resolution.
// Returns http://localhost:{connectPort} for local connections.
func GetColonyURL(colonyID string) (string, error) {
	// Create resolver.
	resolver, err := config.NewResolver()
	if err != nil {
		return "", fmt.Errorf("failed to create config resolver: %w", err)
	}

	// Resolve colony ID if not specified.
	if colonyID == "" {
		colonyID, err = resolver.ResolveColonyID()
		if err != nil {
			// Check if config exists at all.
			home, homeErr := os.UserHomeDir()
			if homeErr == nil {
				configPath := filepath.Join(home, ".coral", "config.yaml")
				if _, statErr := os.Stat(configPath); statErr != nil {
					return "", fmt.Errorf("colony config not found: run 'coral init' first")
				}
			}
			return "", fmt.Errorf("failed to resolve colony: %w", err)
		}
	}

	// Load colony configuration.
	loader := resolver.GetLoader()
	colonyConfig, err := loader.LoadColonyConfig(colonyID)
	if err != nil {
		return "", fmt.Errorf("failed to load colony config: %w", err)
	}

	// Get connect port (default: 9000).
	connectPort := colonyConfig.Services.ConnectPort
	if connectPort == 0 {
		connectPort = 9000
	}

	// Return localhost URL (CLI commands run on same host as colony).
	return fmt.Sprintf("http://localhost:%d", connectPort), nil
}

// GetColonyClient creates a colony service client for the specified colony.
// If colonyID is empty, uses the default colony from config.
func GetColonyClient(colonyID string) (colonyv1connect.ColonyServiceClient, error) {
	url, err := GetColonyURL(colonyID)
	if err != nil {
		return nil, err
	}

	client := colonyv1connect.NewColonyServiceClient(
		http.DefaultClient,
		url,
	)

	return client, nil
}

// GetColonyClientWithFallback creates a colony service client with automatic fallback.
// Tries localhost first, then falls back to mesh IP if localhost fails.
// Returns the client and the successful URL.
func GetColonyClientWithFallback(ctx context.Context, colonyID string) (colonyv1connect.ColonyServiceClient, string, error) {
	// Create resolver.
	resolver, err := config.NewResolver()
	if err != nil {
		return nil, "", fmt.Errorf("failed to create config resolver: %w", err)
	}

	// Resolve colony ID if not specified.
	if colonyID == "" {
		colonyID, err = resolver.ResolveColonyID()
		if err != nil {
			return nil, "", fmt.Errorf("failed to resolve colony: %w\n\nRun 'coral init <app-name>' to create a colony", err)
		}
	}

	// Load colony configuration.
	loader := resolver.GetLoader()
	colonyConfig, err := loader.LoadColonyConfig(colonyID)
	if err != nil {
		return nil, "", fmt.Errorf("failed to load colony config: %w", err)
	}

	// Get connect port (default: 9000).
	connectPort := colonyConfig.Services.ConnectPort
	if connectPort == 0 {
		connectPort = 9000
	}

	// Try localhost first.
	localhostURL := fmt.Sprintf("http://localhost:%d", connectPort)
	client := colonyv1connect.NewColonyServiceClient(http.DefaultClient, localhostURL)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req := connect.NewRequest(&colonyv1.ListAgentsRequest{})
	_, err = client.ListAgents(ctxWithTimeout, req)
	if err == nil {
		// Localhost worked.
		return client, localhostURL, nil
	}

	// Fallback to mesh IP.
	meshIP := colonyConfig.WireGuard.MeshIPv4
	if meshIP == "" {
		meshIP = "10.42.0.1"
	}
	meshURL := fmt.Sprintf("http://%s:%d", meshIP, connectPort)
	client = colonyv1connect.NewColonyServiceClient(http.DefaultClient, meshURL)

	ctxWithTimeout2, cancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer cancel2()

	_, err = client.ListAgents(ctxWithTimeout2, connect.NewRequest(&colonyv1.ListAgentsRequest{}))
	if err != nil {
		return nil, "", fmt.Errorf("failed to connect to colony (tried localhost and mesh IP): %w", err)
	}

	return client, meshURL, nil
}

// ResolveColonyConfig loads colony configuration for the specified colony ID.
// If colonyID is empty, uses the default colony from config.
// Returns the colony config and the resolved colony ID.
func ResolveColonyConfig(colonyID string) (*config.ColonyConfig, string, error) {
	// Create resolver.
	resolver, err := config.NewResolver()
	if err != nil {
		return nil, "", fmt.Errorf("failed to create config resolver: %w", err)
	}

	// Resolve colony ID if not specified.
	if colonyID == "" {
		colonyID, err = resolver.ResolveColonyID()
		if err != nil {
			return nil, "", fmt.Errorf("failed to resolve colony: %w", err)
		}
	}

	// Load colony configuration.
	loader := resolver.GetLoader()
	colonyConfig, err := loader.LoadColonyConfig(colonyID)
	if err != nil {
		return nil, "", fmt.Errorf("failed to load colony config: %w", err)
	}

	return colonyConfig, colonyID, nil
}
