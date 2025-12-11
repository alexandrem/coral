// Package proxy implements the local proxy server for agent connections.
package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// Config holds the proxy server configuration.
type Config struct {
	// ListenAddr is the local address to bind to (e.g., "localhost:8000").
	ListenAddr string

	// ColonyID is the target colony identifier.
	ColonyID string

	// ColonyMeshIPv4 is the colony's mesh IPv4 address.
	ColonyMeshIPv4 string

	// ColonyMeshIPv6 is the colony's mesh IPv6 address.
	ColonyMeshIPv6 string

	// ColonyConnectPort is the colony's Buf Connect HTTP/2 port.
	ColonyConnectPort uint32

	// Logger for structured logging.
	Logger zerolog.Logger
}

// Server is the local proxy server that forwards requests to colonies over WireGuard.
type Server struct {
	config     Config
	httpServer *http.Server
	proxy      *httputil.ReverseProxy
	logger     zerolog.Logger

	mu      sync.RWMutex
	running bool
}

// New creates a new proxy server instance.
func New(config Config) *Server {
	return &Server{
		config: config,
		logger: config.Logger.With().Str("component", "proxy-server").Logger(),
	}
}

// Start starts the proxy server.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("proxy server already running")
	}
	s.running = true
	s.mu.Unlock()

	// Build target URL (prefer IPv4, fallback to IPv6).
	var targetURL string
	if s.config.ColonyMeshIPv4 != "" {
		targetURL = fmt.Sprintf("http://%s:%d", s.config.ColonyMeshIPv4, s.config.ColonyConnectPort)
	} else if s.config.ColonyMeshIPv6 != "" {
		targetURL = fmt.Sprintf("http://[%s]:%d", s.config.ColonyMeshIPv6, s.config.ColonyConnectPort)
	} else {
		return fmt.Errorf("no colony mesh IP configured")
	}

	target, err := url.Parse(targetURL)
	if err != nil {
		return fmt.Errorf("invalid target URL: %w", err)
	}

	s.logger.Info().
		Str("target", targetURL).
		Str("listen_addr", s.config.ListenAddr).
		Msg("Initializing proxy server")

	// Create reverse proxy.
	s.proxy = httputil.NewSingleHostReverseProxy(target)
	s.proxy.ErrorLog = nil // Suppress default error log.

	// Custom error handler.
	s.proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		s.logger.Error().
			Err(err).
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Msg("Proxy error")
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
	}

	// Create HTTP/2 server with h2c support.
	mux := http.NewServeMux()
	mux.Handle("/", s.proxy)

	h2s := &http2.Server{}
	s.httpServer = &http.Server{
		Addr:              s.config.ListenAddr,
		Handler:           h2c.NewHandler(mux, h2s),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}

	// Start listening.
	listener, err := net.Listen("tcp", s.config.ListenAddr)
	if err != nil {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		return fmt.Errorf("failed to listen on %s: %w", s.config.ListenAddr, err)
	}

	s.logger.Info().
		Str("listen_addr", s.config.ListenAddr).
		Str("target", targetURL).
		Msg("Proxy server started")

	// Serve in background.
	go func() {
		if err := s.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			s.logger.Error().Err(err).Msg("HTTP server error")
		}
	}()

	return nil
}

// Stop gracefully stops the proxy server.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return fmt.Errorf("proxy server not running")
	}
	s.running = false
	s.mu.Unlock()

	s.logger.Info().Msg("Stopping proxy server")

	if s.httpServer != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("failed to shutdown HTTP server: %w", err)
		}
	}

	s.logger.Info().Msg("Proxy server stopped")
	return nil
}

// IsRunning returns whether the server is currently running.
func (s *Server) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}
