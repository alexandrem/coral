package sdk

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/coral-mesh/coral/pkg/sdk/debug"
)

// SDK represents the Coral SDK instance embedded in an application.
type SDK struct {
	logger           *slog.Logger
	debugServer      *debug.Server
	metadataProvider *debug.FunctionMetadataProvider
	debugAddr        string
}

// Config contains SDK configuration options.
type Config struct {
	// DebugAddr is the address to listen on for the debug server (default: ":9002").
	DebugAddr string

	// Logger is the logger instance (optional, defaults to slog.Default()).
	Logger *slog.Logger
}

// New creates a new Coral SDK instance.
func New(config Config) (*SDK, error) {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}

	logger = logger.With("component", "coral-sdk")

	// Default to :9002 if not specified
	debugAddr := config.DebugAddr
	if debugAddr == "" {
		debugAddr = ":9002"
	}

	sdk := &SDK{
		logger:    logger,
		debugAddr: debugAddr,
	}

	logger.Info("Coral SDK initialized", "debug_addr", debugAddr)

	return sdk, nil
}

// Close shuts down the SDK and releases resources.
func (s *SDK) Close() error {
	s.logger.Info("Shutting down Coral SDK")

	if s.debugServer != nil {
		if err := s.debugServer.Stop(); err != nil {
			s.logger.Error("Failed to stop debug server", "error", err)
		}
	}

	if s.metadataProvider != nil {
		if err := s.metadataProvider.Close(); err != nil {
			s.logger.Error("Failed to close metadata provider", "error", err)
		}
	}

	return nil
}

// DebugAddr returns the address the debug server is listening on.
func (s *SDK) DebugAddr() string {
	if s.debugServer != nil {
		return s.debugServer.Addr()
	}
	return s.debugAddr
}

// initializeDebugServer sets up the debug server and metadata provider.
func (s *SDK) initializeDebugServer() error {
	s.logger.Info("Initializing debug server")

	// Create metadata provider.
	provider, err := debug.NewFunctionMetadataProvider(s.logger)
	if err != nil {
		return fmt.Errorf("failed to create metadata provider: %w", err)
	}
	s.metadataProvider = provider

	// Create debug server.
	server, err := debug.NewServer(s.logger, provider)
	if err != nil {
		if err := provider.Close(); err != nil {
			s.logger.Error("Failed to close debug server", "error", err)
		}
		return fmt.Errorf("failed to create debug server: %w", err)
	}
	s.debugServer = server

	// Start the server with configured listen address.
	if err := server.Start(s.debugAddr); err != nil {
		if err := provider.Close(); err != nil {
			s.logger.Error("Failed to close debug server", "error", err)
		}
		return fmt.Errorf("failed to start debug server: %w", err)
	}

	s.logger.Info("Debug server started", "addr", server.Addr())

	return nil
}

// Global SDK instance
var globalSDK *SDK
var globalSDKMu sync.Mutex

// Options contains configuration for EnableRuntimeMonitoring.
type Options struct {
	// DebugAddr is the address to listen on for the debug server (default: ":9002").
	DebugAddr string
}

// EnableRuntimeMonitoring starts the HTTP debug server.
func EnableRuntimeMonitoring(opts Options) error {
	globalSDKMu.Lock()
	defer globalSDKMu.Unlock()

	if globalSDK != nil {
		return fmt.Errorf("SDK already initialized")
	}

	// Create SDK instance
	sdk, err := New(Config{
		DebugAddr: opts.DebugAddr,
		Logger:    slog.Default(),
	})
	if err != nil {
		return err
	}

	// Start debug server
	if err := sdk.initializeDebugServer(); err != nil {
		return err
	}

	globalSDK = sdk
	return nil
}
