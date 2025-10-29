package main

import (
	"context"
	"flag"
	"fmt"
	"log"
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
	"github.com/coral-io/coral/pkg/version"
)

func main() {
	// Parse flags
	var (
		port            = flag.Int("port", 8080, "Port to listen on")
		ttlSeconds      = flag.Int("ttl", 300, "Registration TTL in seconds")
		cleanupInterval = flag.Int("cleanup-interval", 60, "Cleanup interval in seconds")
	)
	flag.Parse()

	// Create registry
	reg := registry.New(time.Duration(*ttlSeconds) * time.Second)

	// Start cleanup goroutine
	stopCh := make(chan struct{})
	go reg.StartCleanup(time.Duration(*cleanupInterval)*time.Second, stopCh)

	// Create server
	srv := server.New(reg, version.Version)

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

	// Print startup message
	fmt.Printf("Discovery Service %s\n", version.Version)
	fmt.Printf("gRPC server listening on :%d\n", *port)
	fmt.Printf("TTL: %d seconds\n", *ttlSeconds)
	fmt.Printf("Cleanup interval: %d seconds\n", *cleanupInterval)
	fmt.Println("Ready to accept registrations")

	// Start server in goroutine
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Wait for interrupt signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	// Graceful shutdown
	fmt.Println("\nShutting down...")
	close(stopCh)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Fatalf("Server shutdown failed: %v", err)
	}

	fmt.Println("Server stopped")
}
