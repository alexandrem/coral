package sdk

import (
	"fmt"

	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/pkg/sdk/debug"
)

// SDK represents the Coral SDK instance embedded in an application.
type SDK struct {
	logger           zerolog.Logger
	serviceName      string
	debugServer      *debug.Server
	metadataProvider *debug.FunctionMetadataProvider
}

// Config contains SDK configuration options.
type Config struct {
	// ServiceName is the name of the service (required).
	ServiceName string

	// EnableDebug enables the debug server for uprobe attachment.
	EnableDebug bool

	// Logger is the logger instance (optional, defaults to zerolog.Nop()).
	Logger zerolog.Logger
}

// New creates a new Coral SDK instance.
func New(config Config) (*SDK, error) {
	if config.ServiceName == "" {
		return nil, fmt.Errorf("service name is required")
	}

	logger := config.Logger
	if logger.GetLevel() == zerolog.Disabled {
		logger = zerolog.Nop()
	}

	logger = logger.With().Str("component", "coral-sdk").Str("service", config.ServiceName).Logger()

	sdk := &SDK{
		logger:      logger,
		serviceName: config.ServiceName,
	}

	// Initialize debug server if enabled.
	if config.EnableDebug {
		if err := sdk.initializeDebugServer(); err != nil {
			return nil, fmt.Errorf("failed to initialize debug server: %w", err)
		}
	}

	logger.Info().
		Str("service", config.ServiceName).
		Bool("debug_enabled", config.EnableDebug).
		Msg("Coral SDK initialized")

	return sdk, nil
}

// Close shuts down the SDK and releases resources.
func (s *SDK) Close() error {
	s.logger.Info().Msg("Shutting down Coral SDK")

	if s.debugServer != nil {
		if err := s.debugServer.Stop(); err != nil {
			s.logger.Error().Err(err).Msg("Failed to stop debug server")
		}
	}

	if s.metadataProvider != nil {
		if err := s.metadataProvider.Close(); err != nil {
			s.logger.Error().Err(err).Msg("Failed to close metadata provider")
		}
	}

	return nil
}

// DebugAddr returns the debug server address (for agent registration).
// Returns empty string if debug is not enabled.
func (s *SDK) DebugAddr() string {
	if s.debugServer == nil {
		return ""
	}
	return s.debugServer.Addr()
}

// initializeDebugServer sets up the debug server and metadata provider.
func (s *SDK) initializeDebugServer() error {
	s.logger.Info().Msg("Initializing debug server")

	// Create metadata provider.
	provider, err := debug.NewFunctionMetadataProvider(s.logger)
	if err != nil {
		return fmt.Errorf("failed to create metadata provider: %w", err)
	}
	s.metadataProvider = provider

	// Create debug server.
	server, err := debug.NewServer(s.logger, provider)
	if err != nil {
		provider.Close()
		return fmt.Errorf("failed to create debug server: %w", err)
	}
	s.debugServer = server

	// Start the server.
	if err := server.Start(); err != nil {
		provider.Close()
		return fmt.Errorf("failed to start debug server: %w", err)
	}

	s.logger.Info().
		Str("addr", server.Addr()).
		Msg("Debug server started")

	return nil
}
