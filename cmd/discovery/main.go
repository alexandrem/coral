package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/rs/zerolog"

	"github.com/coral-io/coral/coral/discovery/v1/discoveryv1connect"
	"github.com/coral-io/coral/internal/constants"
	"github.com/coral-io/coral/internal/discovery"
	"github.com/coral-io/coral/internal/discovery/registry"
	"github.com/coral-io/coral/internal/discovery/server"
	"github.com/coral-io/coral/internal/logging"
	"github.com/coral-io/coral/pkg/version"
)

func main() {
	// Parse flags
	var (
		port            = flag.Int("port", 8080, "Port to listen on")
		ttlSeconds      = flag.Int("ttl", 300, "Registration TTL in seconds")
		cleanupInterval = flag.Int("cleanup-interval", 60, "Cleanup interval in seconds")
		logLevel        = flag.String("log-level", "info", "Log level (debug, info, warn, error)")
		stunServersFlag = flag.String("stun-servers", "", "Comma-separated list of recommended fallback STUN servers returned to clients (e.g., stun.cloudflare.com:3478,stun.l.google.com:19302)")
		jwtSigningKey   = flag.String("jwt-signing-key", "", "JWT signing key for referral tickets (base64 or hex encoded, min 32 bytes). Can also use CORAL_JWT_SIGNING_KEY env var or CORAL_JWT_SIGNING_KEY_FILE for file path")
	)
	flag.Parse()

	// Parse STUN servers from environment variable (overrides flag).
	// These are recommended fallback STUN servers returned to clients during registration.
	// Clients may ignore these and use their own configured STUN servers.
	// Primary STUN mechanism is colony-based STUN (RFD 029), not public STUN servers.
	stunServersEnv := os.Getenv("CORAL_STUN_SERVERS")
	var stunServers []string
	if stunServersEnv != "" {
		stunServers = strings.Split(stunServersEnv, ",")
		for i := range stunServers {
			stunServers[i] = strings.TrimSpace(stunServers[i])
		}
	} else if *stunServersFlag != "" {
		stunServers = strings.Split(*stunServersFlag, ",")
		for i := range stunServers {
			stunServers[i] = strings.TrimSpace(stunServers[i])
		}
	} else {
		// Use default STUN server
		stunServers = []string{constants.DefaultSTUNServer}
	}

	// Initialize logger
	logger := logging.NewWithComponent(logging.Config{
		Level:  *logLevel,
		Pretty: true,
	}, "discovery")

	logger.Info().
		Str("version", version.Version).
		Int("port", *port).
		Int("ttl_seconds", *ttlSeconds).
		Int("cleanup_interval_seconds", *cleanupInterval).
		Strs("stun_servers", stunServers).
		Msg("Starting Discovery Service")

	// Create registry
	reg := registry.New(time.Duration(*ttlSeconds) * time.Second)

	// Start cleanup goroutine
	stopCh := make(chan struct{})
	go reg.StartCleanup(time.Duration(*cleanupInterval)*time.Second, stopCh)

	// Initialize token manager with JWT signing key.
	signingKey, err := loadJWTSigningKey(*jwtSigningKey, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to load JWT signing key")
	}

	tokenMgr := discovery.NewTokenManager(discovery.TokenConfig{
		SigningKey: signingKey,
	})

	// Create server
	srv := server.New(reg, tokenMgr, version.Version, logger, stunServers)

	// Create HTTP handler
	mux := http.NewServeMux()
	path, handler := discoveryv1connect.NewDiscoveryServiceHandler(srv)

	// Wrap handler with middleware to inject remote address
	wrappedHandler := remoteAddrMiddleware(handler, logger)
	mux.Handle(path, wrappedHandler)

	// Add basic health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "OK\n")
	})

	// Create HTTP server with h2c support (HTTP/2 without TLS)
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", *port),
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	logger.Info().Msg("Ready to accept registrations")

	// Start server in goroutine
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal().Err(err).Msg("Failed to start server")
		}
	}()

	// Wait for interrupt signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	// Graceful shutdown
	logger.Info().Msg("Shutting down...")
	close(stopCh)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Fatal().Err(err).Msg("Server shutdown failed")
	}

	logger.Info().Msg("Server stopped")
}

// remoteAddrMiddleware adds the remote address to the request headers.
// This allows the gRPC handler to extract the client's observed IP address.
func remoteAddrMiddleware(next http.Handler, logger zerolog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Add the remote address as a custom header
		// RemoteAddr format is "IP:port"
		remoteAddr := r.RemoteAddr

		// Strip port if present
		if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
			remoteAddr = host
		}

		// Set custom header for the handler to read
		r.Header.Set("X-Observed-Addr", remoteAddr)

		logger.Debug().
			Str("remote_addr", r.RemoteAddr).
			Str("observed_addr", remoteAddr).
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Msg("Request received")

		next.ServeHTTP(w, r)
	})
}

// loadJWTSigningKey loads the JWT signing key from various sources.
// Priority order: flag > CORAL_JWT_SIGNING_KEY env > CORAL_JWT_SIGNING_KEY_FILE env > auto-generate.
func loadJWTSigningKey(flagValue string, logger zerolog.Logger) ([]byte, error) {
	const minKeyLength = 32 // Minimum 256 bits for HS256.

	// 1. Check command-line flag.
	if flagValue != "" {
		key, err := decodeKey(flagValue)
		if err != nil {
			return nil, fmt.Errorf("invalid --jwt-signing-key: %w", err)
		}
		if len(key) < minKeyLength {
			return nil, fmt.Errorf("JWT signing key too short: got %d bytes, need at least %d", len(key), minKeyLength)
		}
		logger.Info().Int("key_length", len(key)).Msg("Loaded JWT signing key from flag")
		return key, nil
	}

	// 2. Check CORAL_JWT_SIGNING_KEY environment variable.
	if envKey := os.Getenv("CORAL_JWT_SIGNING_KEY"); envKey != "" {
		key, err := decodeKey(envKey)
		if err != nil {
			return nil, fmt.Errorf("invalid CORAL_JWT_SIGNING_KEY: %w", err)
		}
		if len(key) < minKeyLength {
			return nil, fmt.Errorf("JWT signing key too short: got %d bytes, need at least %d", len(key), minKeyLength)
		}
		logger.Info().Int("key_length", len(key)).Msg("Loaded JWT signing key from CORAL_JWT_SIGNING_KEY")
		return key, nil
	}

	// 3. Check CORAL_JWT_SIGNING_KEY_FILE environment variable.
	if keyFile := os.Getenv("CORAL_JWT_SIGNING_KEY_FILE"); keyFile != "" {
		data, err := os.ReadFile(keyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read key file %s: %w", keyFile, err)
		}
		// Try to decode as base64/hex, otherwise use raw bytes.
		key, err := decodeKey(strings.TrimSpace(string(data)))
		if err != nil {
			// Use raw bytes if not encoded.
			key = data
		}
		if len(key) < minKeyLength {
			return nil, fmt.Errorf("JWT signing key too short: got %d bytes, need at least %d", len(key), minKeyLength)
		}
		logger.Info().
			Str("file", keyFile).
			Int("key_length", len(key)).
			Msg("Loaded JWT signing key from file")
		return key, nil
	}

	// 4. Auto-generate key (development mode).
	logger.Warn().Msg("No JWT signing key configured - generating random key (NOT RECOMMENDED FOR PRODUCTION)")
	logger.Warn().Msg("Set CORAL_JWT_SIGNING_KEY or CORAL_JWT_SIGNING_KEY_FILE for production deployments")

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate random key: %w", err)
	}

	// Log the generated key so it can be saved for development.
	logger.Info().
		Str("generated_key_base64", base64.StdEncoding.EncodeToString(key)).
		Msg("Generated JWT signing key (save this for consistent development)")

	return key, nil
}

// decodeKey attempts to decode a key from base64 or hex format.
func decodeKey(encoded string) ([]byte, error) {
	// Try base64 first (more common for keys).
	if key, err := base64.StdEncoding.DecodeString(encoded); err == nil && len(key) > 0 {
		return key, nil
	}
	// Try base64 URL encoding.
	if key, err := base64.URLEncoding.DecodeString(encoded); err == nil && len(key) > 0 {
		return key, nil
	}
	// Try hex encoding.
	if key, err := hex.DecodeString(encoded); err == nil && len(key) > 0 {
		return key, nil
	}
	// Use raw string as bytes (for simple passwords, though not recommended).
	if len(encoded) >= 32 {
		return []byte(encoded), nil
	}
	return nil, fmt.Errorf("could not decode key (tried base64, hex) and raw string too short")
}
