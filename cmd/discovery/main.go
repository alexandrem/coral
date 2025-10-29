package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/coral-io/coral/coral/discovery/v1/discoveryv1connect"
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
	)
	flag.Parse()

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
		Msg("Starting Discovery Service")

	// Create registry
	reg := registry.New(time.Duration(*ttlSeconds) * time.Second)

	// Start cleanup goroutine
	stopCh := make(chan struct{})
	go reg.StartCleanup(time.Duration(*cleanupInterval)*time.Second, stopCh)

	// Create server
	srv := server.New(reg, version.Version, logger)

	// Create HTTP handler
	mux := http.NewServeMux()
	path, handler := discoveryv1connect.NewDiscoveryServiceHandler(srv)
	mux.Handle(path, handler)

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
