package helpers

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
// Connection priority:
//  1. CORAL_COLONY_ENDPOINT env var (highest)
//  2. Colony config remote.endpoint
//  3. localhost:{connectPort} (default)
//
// TLS configuration priority:
//  1. CORAL_INSECURE=true env var (skip TLS verification)
//  2. CORAL_CA_FILE env var (path to CA certificate)
//  3. Colony config remote.insecure_skip_tls_verify
//  4. Colony config remote.certificate_authority_data (base64)
//  5. Colony config remote.certificate_authority (file path)
//  6. System CA pool (default)

// GetColonyURL returns the colony URL using config resolution.
// Priority: CORAL_COLONY_ENDPOINT env var > remote.endpoint config > localhost:{connectPort}.
func GetColonyURL(colonyID string) (string, error) {
	// 1. Check environment variable override (RFD 031).
	if endpoint := os.Getenv("CORAL_COLONY_ENDPOINT"); endpoint != "" {
		return endpoint, nil
	}

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

	// 2. Check remote endpoint in config.
	if colonyConfig.Remote.Endpoint != "" {
		return colonyConfig.Remote.Endpoint, nil
	}

	// 3. Fall back to localhost URL (CLI commands run on same host as colony).
	connectPort := colonyConfig.Services.ConnectPort
	if connectPort == 0 {
		connectPort = 9000
	}

	return fmt.Sprintf("http://localhost:%d", connectPort), nil
}

// GetColonyClient creates a colony service client for the specified colony.
// If colonyID is empty, uses the default colony from config.
// Supports CORAL_API_TOKEN for authentication (RFD 031).
// Supports custom TLS configuration via config or environment variables.
func GetColonyClient(colonyID string) (colonyv1connect.ColonyServiceClient, error) {
	url, err := GetColonyURL(colonyID)
	if err != nil {
		return nil, err
	}

	// Build HTTP client with appropriate TLS configuration.
	httpClient, err := buildHTTPClient(colonyID, url)
	if err != nil {
		return nil, fmt.Errorf("failed to build HTTP client: %w", err)
	}

	// Prepare interceptors for authentication.
	var opts []connect.ClientOption
	if token := os.Getenv("CORAL_API_TOKEN"); token != "" {
		interceptor := connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
			return connect.UnaryFunc(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
				req.Header().Set("Authorization", "Bearer "+token)
				return next(ctx, req)
			})
		}))
		opts = append(opts, interceptor)
	}

	client := colonyv1connect.NewColonyServiceClient(
		httpClient,
		url,
		opts...,
	)

	return client, nil
}

// buildHTTPClient creates an HTTP client with appropriate TLS configuration.
// For HTTPS endpoints, it configures TLS based on env vars and colony config.
func buildHTTPClient(colonyID string, url string) (*http.Client, error) {
	// For non-HTTPS URLs, use default client.
	if !strings.HasPrefix(url, "https://") {
		return http.DefaultClient, nil
	}

	// Load colony config to check for TLS settings.
	var colonyConfig *config.ColonyConfig
	if colonyID != "" || os.Getenv("CORAL_COLONY_ENDPOINT") == "" {
		// Only load config if we might need TLS settings from it.
		cfg, _, err := ResolveColonyConfig(colonyID)
		if err == nil {
			colonyConfig = cfg
		}
		// If config load fails, continue with env vars only.
	}

	tlsConfig, err := buildTLSConfig(colonyConfig)
	if err != nil {
		return nil, err
	}

	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
		Timeout: 30 * time.Second,
	}, nil
}

// buildTLSConfig creates TLS configuration based on env vars and colony config.
func buildTLSConfig(colonyConfig *config.ColonyConfig) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	// 1. Check CORAL_INSECURE env var (highest priority).
	if insecure := os.Getenv("CORAL_INSECURE"); insecure == "true" || insecure == "1" {
		tlsConfig.InsecureSkipVerify = true
		return tlsConfig, nil
	}

	// 2. Check CORAL_CA_FILE env var.
	if caFile := os.Getenv("CORAL_CA_FILE"); caFile != "" {
		certPool, err := loadCACertFromFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load CA from CORAL_CA_FILE: %w", err)
		}
		tlsConfig.RootCAs = certPool
		return tlsConfig, nil
	}

	// 3. Check colony config for TLS settings.
	if colonyConfig != nil {
		remote := colonyConfig.Remote

		// Check insecure flag in config.
		if remote.InsecureSkipTLSVerify {
			tlsConfig.InsecureSkipVerify = true
			return tlsConfig, nil
		}

		// Check for CA data (base64-encoded, takes precedence).
		if remote.CertificateAuthorityData != "" {
			certPool, err := loadCACertFromData(remote.CertificateAuthorityData)
			if err != nil {
				return nil, fmt.Errorf("failed to load CA from certificate_authority_data: %w", err)
			}
			tlsConfig.RootCAs = certPool
			return tlsConfig, nil
		}

		// Check for CA file path in config.
		if remote.CertificateAuthority != "" {
			caPath := expandPath(remote.CertificateAuthority)
			certPool, err := loadCACertFromFile(caPath)
			if err != nil {
				return nil, fmt.Errorf("failed to load CA from certificate_authority: %w", err)
			}
			tlsConfig.RootCAs = certPool
			return tlsConfig, nil
		}
	}

	// 4. Use system CA pool (default).
	return tlsConfig, nil
}

// loadCACertFromFile loads a CA certificate from a file path.
func loadCACertFromFile(path string) (*x509.CertPool, error) {
	caCert, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate file %s: %w", path, err)
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA certificate from %s", path)
	}

	return certPool, nil
}

// loadCACertFromData loads a CA certificate from base64-encoded data.
func loadCACertFromData(data string) (*x509.CertPool, error) {
	caCert, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 CA data: %w", err)
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA certificate from data")
	}

	return certPool, nil
}

// expandPath expands ~ to home directory.
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
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

	// Build HTTP client (with TLS support if needed).
	httpClient, err := buildHTTPClient(colonyID, "https://placeholder")
	if err != nil {
		// Fall back to default client if TLS config fails.
		httpClient = http.DefaultClient
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

	// Try remote endpoint if configured.
	if colonyConfig.Remote.Endpoint != "" {
		remoteURL := colonyConfig.Remote.Endpoint
		client = colonyv1connect.NewColonyServiceClient(httpClient, remoteURL)

		ctxWithTimeout2, cancel2 := context.WithTimeout(ctx, 5*time.Second)
		defer cancel2()

		_, err = client.ListAgents(ctxWithTimeout2, connect.NewRequest(&colonyv1.ListAgentsRequest{}))
		if err == nil {
			return client, remoteURL, nil
		}
	}

	// Fallback to mesh IP.
	meshIP := colonyConfig.WireGuard.MeshIPv4
	if meshIP == "" {
		meshIP = "10.42.0.1"
	}
	meshURL := fmt.Sprintf("http://%s:%d", meshIP, connectPort)
	client = colonyv1connect.NewColonyServiceClient(http.DefaultClient, meshURL)

	ctxWithTimeout3, cancel3 := context.WithTimeout(ctx, 5*time.Second)
	defer cancel3()

	_, err = client.ListAgents(ctxWithTimeout3, connect.NewRequest(&colonyv1.ListAgentsRequest{}))
	if err != nil {
		return nil, "", fmt.Errorf("failed to connect to colony (tried localhost, remote, and mesh IP): %w", err)
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
