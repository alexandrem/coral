// Package httpapi implements the HTTP API endpoint for Colony (RFD 031).
package httpapi

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/coral-mesh/coral/internal/auth"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/constants"
)

// MCPServerInterface defines the interface for MCP server operations.
// This allows the httpapi package to work with MCP servers without importing the mcp package.
type MCPServerInterface interface {
	// ListToolNames returns the names of all available MCP tools.
	ListToolNames() []string

	// ExecuteTool executes an MCP tool by name with the given arguments.
	ExecuteTool(ctx context.Context, toolName, arguments string) (string, error)
}

// Server is the HTTP API endpoint server for Colony.
type Server struct {
	config     config.PublicEndpointConfig
	httpServer *http.Server
	tokenStore *auth.TokenStore
	mcpServer  MCPServerInterface
	logger     zerolog.Logger
}

// Config contains dependencies for creating an HTTP API endpoint server.
type Config struct {
	// PublicConfig is the public endpoint configuration.
	PublicConfig config.PublicEndpointConfig

	// ColonyPath is the URL path prefix for colony service (e.g., "/coral.colony.v1.ColonyService/").
	ColonyPath string

	// ColonyHandler is the HTTP handler for colony service.
	ColonyHandler http.Handler

	// DebugPath is the URL path prefix for debug service.
	DebugPath string

	// DebugHandler is the HTTP handler for debug service.
	DebugHandler http.Handler

	// MCPServer is the MCP server for SSE transport (optional).
	// Must implement MCPServerInterface.
	MCPServer MCPServerInterface

	// TokenStore is the token store for authentication.
	TokenStore *auth.TokenStore

	// ColonyDir is the directory path for colony config (for token storage).
	ColonyDir string

	// TLSCertificate is the pre-loaded TLS certificate (optional).
	// If provided, it overrides CertFile/KeyFile in PublicConfig.
	TLSCertificate *tls.Certificate

	// Logger is the logger instance.
	Logger zerolog.Logger
}

// New creates a new HTTP API endpoint server.
func New(cfg Config) (*Server, error) {
	if !cfg.PublicConfig.Enabled {
		return nil, fmt.Errorf("HTTP API endpoint is not enabled")
	}

	logger := cfg.Logger.With().Str("component", "httpapi").Logger()

	// Apply defaults.
	host := cfg.PublicConfig.Host
	if host == "" {
		host = constants.DefaultPublicEndpointHost
	}

	port := cfg.PublicConfig.Port
	if port == 0 {
		port = constants.DefaultPublicEndpointPort
	}

	// Initialize token store if not provided.
	tokenStore := cfg.TokenStore
	if tokenStore == nil {
		tokensFile := cfg.PublicConfig.Auth.TokensFile
		if tokensFile == "" && cfg.ColonyDir != "" {
			tokensFile = filepath.Join(cfg.ColonyDir, "tokens.yaml")
		}
		tokenStore = auth.NewTokenStore(tokensFile)
	}

	// Build middleware chain.
	authMw := NewAuthMiddleware(tokenStore, logger)
	rbacMw := NewRBACMiddleware(logger)
	rateMw := NewRateLimitMiddleware(logger)
	auditMw := NewAuditMiddleware(logger)

	// Create mux for routing.
	mux := http.NewServeMux()

	// Register Connect handlers.
	if cfg.ColonyHandler != nil {
		mux.Handle(cfg.ColonyPath, cfg.ColonyHandler)
		logger.Debug().Str("path", cfg.ColonyPath).Msg("Registered colony service handler")
	}

	if cfg.DebugHandler != nil {
		mux.Handle(cfg.DebugPath, cfg.DebugHandler)
		logger.Debug().Str("path", cfg.DebugPath).Msg("Registered debug service handler")
	}

	// Register MCP SSE endpoint if enabled.
	if cfg.PublicConfig.MCP.Enabled && cfg.MCPServer != nil {
		mcpPath := cfg.PublicConfig.MCP.Path
		if mcpPath == "" {
			mcpPath = constants.DefaultMCPSSEPath
		}
		mux.Handle(mcpPath, NewMCPSSEHandler(cfg.MCPServer, tokenStore, logger))
		logger.Info().Str("path", mcpPath).Msg("MCP SSE endpoint registered")
	}

	// Create health endpoint (bypasses authentication).
	healthMux := http.NewServeMux()
	healthMux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK\n"))
	})

	// Build handler chain: audit -> ratelimit -> rbac -> auth -> handler.
	// Health endpoint bypasses all middleware.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health endpoint bypasses auth.
		if r.URL.Path == "/health" {
			healthMux.ServeHTTP(w, r)
			return
		}

		// Check if auth is required.
		requireAuth := cfg.PublicConfig.Auth.Require
		// Default to requiring auth unless explicitly disabled.
		if !cfg.PublicConfig.Auth.Require && cfg.PublicConfig.Auth.TokensFile == "" {
			requireAuth = true
		}

		if requireAuth {
			// Apply full middleware chain.
			chain := auditMw.Handler(
				rateMw.Handler(
					rbacMw.Handler(
						authMw.Handler(mux),
					),
				),
			)
			chain.ServeHTTP(w, r)
		} else {
			// Audit only (no auth).
			chain := auditMw.Handler(mux)
			chain.ServeHTTP(w, r)
		}
	})

	// Configure HTTP server.
	addr := fmt.Sprintf("%s:%d", host, port)
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           h2c.NewHandler(handler, &http2.Server{}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Configure TLS if not localhost or if certificate provided.
	isLocalhost := host == "127.0.0.1" || host == "localhost" || host == "::1"
	if cfg.TLSCertificate != nil {
		httpServer.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{*cfg.TLSCertificate},
			MinVersion:   tls.VersionTLS13,
		}
	} else if !isLocalhost {
		if cfg.PublicConfig.TLS.CertFile == "" || cfg.PublicConfig.TLS.KeyFile == "" {
			return nil, fmt.Errorf("TLS cert and key required for non-localhost HTTP API endpoint (host=%s)", host)
		}

		cert, err := tls.LoadX509KeyPair(cfg.PublicConfig.TLS.CertFile, cfg.PublicConfig.TLS.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load TLS certificate: %w", err)
		}

		httpServer.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS13,
		}
	}

	return &Server{
		config:     cfg.PublicConfig,
		httpServer: httpServer,
		tokenStore: tokenStore,
		mcpServer:  cfg.MCPServer,
		logger:     logger,
	}, nil
}

// Start starts the HTTP API endpoint server in a background goroutine.
func (s *Server) Start() error {
	s.logger.Info().
		Str("addr", s.httpServer.Addr).
		Bool("tls", s.httpServer.TLSConfig != nil).
		Bool("mcp_enabled", s.config.MCP.Enabled).
		Msg("Starting HTTP API endpoint server")

	go func() {
		var err error
		if s.httpServer.TLSConfig != nil {
			err = s.httpServer.ListenAndServeTLS("", "")
		} else {
			err = s.httpServer.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error().Err(err).Msg("HTTP API endpoint server error")
		}
	}()

	return nil
}

// Stop gracefully stops the server.
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Info().Msg("Stopping HTTP API endpoint server")
	return s.httpServer.Shutdown(ctx)
}

// TokenStore returns the token store for CLI management.
func (s *Server) TokenStore() *auth.TokenStore {
	return s.tokenStore
}

// Addr returns the server address.
func (s *Server) Addr() string {
	return s.httpServer.Addr
}

// URL returns the server URL.
func (s *Server) URL() string {
	scheme := "http"
	if s.httpServer.TLSConfig != nil {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, s.httpServer.Addr)
}
