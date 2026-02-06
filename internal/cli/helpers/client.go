package helpers

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
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
	"github.com/coral-mesh/coral/internal/constants"
)

// Colony client helpers for CLI commands.
//
// This package provides shared utilities for connecting to colonies, eliminating
// duplication across CLI commands (debug, agent, status, duckdb, etc.).
//
// Connection priority:
//  1. Colony config remote.endpoint (env var CORAL_COLONY_ENDPOINT merged via struct tag)
//  2. localhost:{connectPort} (default)
//
// TLS configuration priority (env vars merged into config via struct tags):
//  1. Colony config remote.insecure_skip_tls_verify (env: CORAL_INSECURE)
//  2. Colony config remote.certificate_authority_data (env: CORAL_CA_DATA)
//  3. Colony config remote.certificate_authority (env: CORAL_CA_FILE)
//  4. System CA pool (default)

// GetColonyURL returns the colony URL using config resolution.
// Special case: If CORAL_COLONY_ENDPOINT is set, it can be used standalone without colony config.
// Otherwise, env vars are merged into config via struct tags (CORAL_COLONY_ENDPOINT -> remote.endpoint).
// Priority: CORAL_COLONY_ENDPOINT standalone > remote.endpoint config > localhost:{connectPort}.
func GetColonyURL(colonyID string) (string, error) {
	// Special case: CORAL_COLONY_ENDPOINT can be used without colony config (remote-only mode).
	// This allows connecting to remote colonies without local config files.
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

	// Load colony configuration (env vars merged via MergeFromEnv).
	loader := resolver.GetLoader()
	colonyConfig, err := loader.LoadColonyConfig(colonyID)
	if err != nil {
		return "", fmt.Errorf("failed to load colony config: %w", err)
	}

	// Check remote endpoint in config (CORAL_COLONY_ENDPOINT would also be merged here,
	// but we already handled it above for the remote-only use case).
	if colonyConfig.Remote.Endpoint != "" {
		return colonyConfig.Remote.Endpoint, nil
	}

	// Fall back to localhost URL (CLI commands run on same host as colony).
	connectPort := colonyConfig.Services.ConnectPort
	if connectPort == 0 {
		connectPort = constants.DefaultColonyPort
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
// For HTTPS endpoints, it configures TLS based on colony config (with env vars merged).
func buildHTTPClient(colonyID string, url string) (*http.Client, error) {
	// For non-HTTPS URLs, use default client.
	if !strings.HasPrefix(url, "https://") {
		return http.DefaultClient, nil
	}

	// Load colony config to get TLS settings (env vars merged via MergeFromEnv).
	colonyConfig, _, err := ResolveColonyConfig(colonyID)
	if err != nil {
		// If config load fails, use system CA pool.
		return &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					MinVersion: tls.VersionTLS12,
				},
			},
			Timeout: 30 * time.Second,
		}, nil
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

// buildTLSConfig creates TLS configuration from colony config.
// Env vars (CORAL_INSECURE, CORAL_CA_FILE, CORAL_CA_DATA) are merged into colonyConfig.Remote
// via struct tags by LoadColonyConfig's MergeFromEnv call.
func buildTLSConfig(colonyConfig *config.ColonyConfig) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	if colonyConfig == nil {
		// Use system CA pool (default).
		return tlsConfig, nil
	}

	remote := colonyConfig.Remote

	// Check insecure flag (CORAL_INSECURE env var merged here).
	if remote.InsecureSkipTLSVerify {
		tlsConfig.InsecureSkipVerify = true
		return tlsConfig, nil
	}

	// Check for CA data (CORAL_CA_DATA env var merged here, takes precedence).
	if remote.CertificateAuthorityData != "" {
		caCert, err := base64.StdEncoding.DecodeString(remote.CertificateAuthorityData)
		if err != nil {
			return nil, fmt.Errorf("failed to decode CA from certificate_authority_data: %w", err)
		}

		// Verify fingerprint if configured (RFD 085).
		if err := verifyCAFingerprint(caCert, remote.CAFingerprint); err != nil {
			return nil, fmt.Errorf("CA certificate verification failed: %w\n\nThe local CA certificate may have been tampered with.\nRe-run 'coral colony add-remote' with the correct --ca-fingerprint", err)
		}

		certPool := x509.NewCertPool()
		if !certPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate from certificate_authority_data")
		}
		tlsConfig.RootCAs = certPool
		return tlsConfig, nil
	}

	// Check for CA file path (CORAL_CA_FILE env var merged here).
	if remote.CertificateAuthority != "" {
		caPath := expandPath(remote.CertificateAuthority)
		caCert, err := os.ReadFile(caPath) // #nosec G304 - path from config
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate from %s: %w", caPath, err)
		}

		// Verify fingerprint if configured (RFD 085).
		if err := verifyCAFingerprint(caCert, remote.CAFingerprint); err != nil {
			return nil, fmt.Errorf("CA certificate verification failed: %w\n\nThe local CA certificate may have been tampered with.\nRe-run 'coral colony add-remote' with the correct --ca-fingerprint", err)
		}

		certPool := x509.NewCertPool()
		if !certPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate from %s", caPath)
		}
		tlsConfig.RootCAs = certPool
		return tlsConfig, nil
	}

	// Use system CA pool (default).
	return tlsConfig, nil
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

// verifyCAFingerprint verifies the CA certificate matches the stored fingerprint (RFD 085).
// This protects against local CA file tampering.
func verifyCAFingerprint(caCert []byte, fpConfig *config.CAFingerprintConfig) error {
	if fpConfig == nil {
		// No fingerprint configured, skip verification.
		return nil
	}

	if fpConfig.Algorithm != "sha256" {
		return fmt.Errorf("unsupported fingerprint algorithm: %s (only sha256 is supported)", fpConfig.Algorithm)
	}

	// Compute SHA256 fingerprint of the CA certificate.
	computed := sha256.Sum256(caCert)
	computedHex := hex.EncodeToString(computed[:])

	if computedHex != fpConfig.Value {
		return fmt.Errorf("fingerprint mismatch: expected sha256:%s, got sha256:%s", fpConfig.Value, computedHex)
	}

	return nil
}

// GetColonyClientWithFallback creates a colony service client with automatic fallback.
// Tries remote endpoint (with env override), then localhost, then mesh IP.
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

	// Load colony configuration (CORAL_COLONY_ENDPOINT env var merged into remote.endpoint).
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
		connectPort = constants.DefaultColonyPort
	}

	// Try remote endpoint first if configured (CORAL_COLONY_ENDPOINT env var merged here).
	if colonyConfig.Remote.Endpoint != "" {
		remoteURL := colonyConfig.Remote.Endpoint
		client := colonyv1connect.NewColonyServiceClient(httpClient, remoteURL)

		ctxWithTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		_, err = client.GetStatus(ctxWithTimeout, connect.NewRequest(&colonyv1.GetStatusRequest{}))
		if err == nil {
			return client, remoteURL, nil
		}

		// When a remote endpoint is explicitly configured, fail immediately without fallback.
		// This ensures certificate errors and other issues are surfaced to the user.
		return nil, "", fmt.Errorf("failed to connect to remote endpoint %s: %w", remoteURL, err)
	}

	// Try localhost.
	localhostURL := fmt.Sprintf("http://localhost:%d", connectPort)
	client := colonyv1connect.NewColonyServiceClient(http.DefaultClient, localhostURL)

	ctxWithTimeout2, cancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer cancel2()

	_, err = client.GetStatus(ctxWithTimeout2, connect.NewRequest(&colonyv1.GetStatusRequest{}))
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

	ctxWithTimeout3, cancel3 := context.WithTimeout(ctx, 5*time.Second)
	defer cancel3()

	_, err = client.GetStatus(ctxWithTimeout3, connect.NewRequest(&colonyv1.GetStatusRequest{}))
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
