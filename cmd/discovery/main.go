package main

import (
	"context"
	"encoding/json"
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

	"github.com/coral-mesh/coral/coral/discovery/v1/discoveryv1connect"
	"github.com/coral-mesh/coral/internal/constants"
	"github.com/coral-mesh/coral/internal/discovery"
	"github.com/coral-mesh/coral/internal/discovery/keys"
	"github.com/coral-mesh/coral/internal/discovery/registry"
	"github.com/coral-mesh/coral/internal/discovery/server"
	"github.com/coral-mesh/coral/internal/logging"
	"github.com/coral-mesh/coral/pkg/version"
)

func main() {
	// Parse flags
	var (
		port            = flag.Int("port", 8080, "Port to listen on")
		ttlSeconds      = flag.Int("ttl", 300, "Registration TTL in seconds")
		cleanupInterval = flag.Int("cleanup-interval", 60, "Cleanup interval in seconds")
		logLevel        = flag.String("log-level", "info", "Log level (debug, info, warn, error)")
		stunServersFlag = flag.String("stun-servers", "", "Comma-separated list of recommended fallback STUN servers returned to clients")
		keyStoragePath  = flag.String("key-storage", "discovery-keys.json", "Path to store JWK signing keys")
		keyRotationDays = flag.Int("key-rotation-days", 30, "Key rotation period in days")
	)
	flag.Parse()

	// Parse STUN servers from environment variable (overrides flag).
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

	// Initialize Key Manager
	keyMgr, err := keys.NewManager(*keyStoragePath, time.Duration(*keyRotationDays)*24*time.Hour)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to initialize key manager")
	}
	logger.Info().
		Str("storage_path", *keyStoragePath).
		Int("rotation_days", *keyRotationDays).
		Str("current_kid", keyMgr.CurrentKey().ID).
		Msg("Initialized Key Manager")

	// Initialize Token Manager with Key Manager
	tokenMgr := discovery.NewTokenManager(discovery.TokenConfig{
		KeyManager: keyMgr,
	})

	// Create server
	srv := server.New(reg, tokenMgr, version.Version, logger, stunServers)

	// Create HTTP handler
	mux := http.NewServeMux()
	path, handler := discoveryv1connect.NewDiscoveryServiceHandler(srv)

	// Wrap handler with middleware to inject remote address
	wrappedHandler := remoteAddrMiddleware(handler, logger)
	mux.Handle(path, wrappedHandler)

	// JWKS Endpoint
	mux.HandleFunc("/.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		jwks := keyMgr.GetJWKS()
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(jwks); err != nil {
			logger.Error().Err(err).Msg("Failed to encode JWKS")
		}
	})

	// Add basic health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "OK\n") // TODO: errcheck
	})

	// Create HTTP server with h2c support (HTTP/2 without TLS)
	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", *port),
		Handler:           h2c.NewHandler(mux, &http2.Server{}),
		ReadHeaderTimeout: 10 * time.Second,
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
