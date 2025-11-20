package main

import (
	"context"
	"database/sql"
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

	_ "github.com/marcboeker/go-duckdb"

	"github.com/coral-io/coral/coral/discovery/v1/discoveryv1connect"
	"github.com/coral-io/coral/internal/constants"
	"github.com/coral-io/coral/internal/discovery"
	"github.com/coral-io/coral/internal/discovery/registry"
	"github.com/coral-io/coral/internal/discovery/server"
	"github.com/coral-io/coral/internal/logging"
	"github.com/coral-io/coral/pkg/version"
	"github.com/rs/zerolog"
)

func main() {
	// Parse flags
	var (
		port            = flag.Int("port", 8080, "Port to listen on")
		ttlSeconds      = flag.Int("ttl", 300, "Registration TTL in seconds")
		cleanupInterval = flag.Int("cleanup-interval", 60, "Cleanup interval in seconds")
		logLevel        = flag.String("log-level", "info", "Log level (debug, info, warn, error)")
		stunServersFlag = flag.String("stun-servers", "", "Comma-separated list of recommended fallback STUN servers returned to clients (e.g., stun.cloudflare.com:3478,stun.l.google.com:19302)")
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

	// Initialize database for bootstrap tokens (RFD 022).
	// TODO: Make database path configurable.
	dbPath := os.Getenv("CORAL_DISCOVERY_DB_PATH")
	if dbPath == "" {
		dbPath = "/tmp/coral-discovery.db"
	}
	db, err := sql.Open("duckdb", dbPath)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to open discovery database")
	}
	defer db.Close()

	// Initialize bootstrap tokens table.
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS bootstrap_tokens (
			token_id TEXT PRIMARY KEY,
			jwt_hash TEXT NOT NULL UNIQUE,
			reef_id TEXT NOT NULL,
			colony_id TEXT NOT NULL,
			agent_id TEXT NOT NULL,
			intent TEXT NOT NULL,
			issued_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP NOT NULL,
			consumed_at TIMESTAMP,
			consumed_by TEXT,
			status TEXT NOT NULL DEFAULT 'active'
		)
	`)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to initialize bootstrap tokens table")
	}

	// Create token manager (RFD 022).
	// TODO: Load JWT signing key from config or generate securely.
	jwtSigningKey := []byte(os.Getenv("CORAL_JWT_SIGNING_KEY"))
	if len(jwtSigningKey) == 0 {
		jwtSigningKey = []byte("temporary-insecure-key-change-in-production")
		logger.Warn().Msg("Using temporary JWT signing key - set CORAL_JWT_SIGNING_KEY environment variable in production")
	}
	tokenMgr := discovery.NewTokenManager(db, discovery.TokenConfig{
		SigningKey: jwtSigningKey,
	})

	// Start token cleanup goroutine.
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := tokenMgr.CleanupExpiredTokens(); err != nil {
					logger.Error().Err(err).Msg("Failed to cleanup expired tokens")
				}
			case <-stopCh:
				return
			}
		}
	}()

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
