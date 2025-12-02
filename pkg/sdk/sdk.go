package sdk

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"connectrpc.com/connect"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	"github.com/coral-mesh/coral/pkg/sdk/debug"
)

// SDK represents the Coral SDK instance embedded in an application.
type SDK struct {
	logger           *slog.Logger
	serviceName      string
	debugServer      *debug.Server
	metadataProvider *debug.FunctionMetadataProvider

	// Registration info
	agentAddr      string
	appPort        int
	healthEndpoint string
}

// Config contains SDK configuration options.
type Config struct {
	// ServiceName is the name of the service (required).
	ServiceName string

	// EnableDebug enables the debug server for uprobe attachment.
	EnableDebug bool

	// Logger is the logger instance (optional, defaults to slog.Default()).
	Logger *slog.Logger
}

// New creates a new Coral SDK instance.
func New(config Config) (*SDK, error) {
	if config.ServiceName == "" {
		return nil, fmt.Errorf("service name is required")
	}

	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}

	logger = logger.With("component", "coral-sdk", "service", config.ServiceName)

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

	logger.Info("Coral SDK initialized",
		"service", config.ServiceName,
		"debug_enabled", config.EnableDebug)

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

	// Start the server.
	if err := server.Start(); err != nil {
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

// Options contains configuration for RegisterService.
type Options struct {
	Port           int    // Application listen port
	HealthEndpoint string // Health check endpoint
	AgentAddr      string // Agent gRPC address (default: localhost:9091)
	SdkListenAddr  string // Address for SDK gRPC server (default: :9092)
}

// RegisterService registers the application with Coral agent.
func RegisterService(name string, opts Options) error {
	globalSDKMu.Lock()
	defer globalSDKMu.Unlock()

	if globalSDK != nil {
		return fmt.Errorf("SDK already initialized")
	}

	// Set defaults
	if opts.AgentAddr == "" {
		opts.AgentAddr = "localhost:9091"
	}
	if opts.SdkListenAddr == "" {
		opts.SdkListenAddr = ":9092"
	}

	// Create SDK instance
	sdk, err := New(Config{
		ServiceName: name,
		EnableDebug: true, // Always enable debug for runtime monitoring
		Logger:      slog.Default(),
	})
	if err != nil {
		return err
	}

	sdk.agentAddr = opts.AgentAddr
	sdk.appPort = opts.Port
	sdk.healthEndpoint = opts.HealthEndpoint

	globalSDK = sdk
	return nil
}

// EnableRuntimeMonitoring starts background goroutine that discovers function offsets
// and serves gRPC API for agent queries.
func EnableRuntimeMonitoring() error {
	globalSDKMu.Lock()
	sdk := globalSDK
	globalSDKMu.Unlock()

	if sdk == nil {
		return fmt.Errorf("SDK not initialized; call RegisterService first")
	}

	// Start debug server if not already started (New() starts it if EnableDebug is true)
	if sdk.debugServer == nil {
		if err := sdk.initializeDebugServer(); err != nil {
			return err
		}
	}

	// Register with Agent in background
	go sdk.registerWithAgent()

	return nil
}

// registerWithAgent attempts to register the service with the local Agent.
func (s *SDK) registerWithAgent() {
	client := agentv1connect.NewAgentServiceClient(
		http.DefaultClient,
		"http://"+s.agentAddr,
	)

	// Retry loop for registration
	for {
		s.logger.Info("Attempting to register with Agent...", "agent", s.agentAddr)

		// Calculate binary hash (best effort)
		binHash, err := s.metadataProvider.GetBinaryHash()
		if err != nil {
			s.logger.Warn("Failed to calculate binary hash", "error", err)
		}

		// Gather capabilities
		caps := &agentv1.ServiceSdkCapabilities{
			ServiceName:     s.serviceName,
			ProcessId:       fmt.Sprintf("%d", os.Getpid()),
			SdkEnabled:      true,
			SdkVersion:      "v0.1.0", // TODO: Get from version package
			SdkAddr:         s.DebugAddr(),
			HasDwarfSymbols: s.metadataProvider.HasDWARF(),
			BinaryPath:      s.metadataProvider.BinaryPath(),
			FunctionCount:   uint32(s.metadataProvider.GetFunctionCount()),
			BinaryHash:      binHash,
		}

		req := connect.NewRequest(&agentv1.ConnectServiceRequest{
			Name:            s.serviceName,
			Port:            int32(s.appPort),
			HealthEndpoint:  s.healthEndpoint,
			ServiceType:     "go",
			SdkCapabilities: caps,
		})

		resp, err := client.ConnectService(context.Background(), req)
		if err == nil && resp.Msg.Success {
			s.logger.Info("Successfully registered with Agent")
			return
		}

		if err != nil {
			s.logger.Warn("Failed to register with Agent, retrying in 5s...", "error", err)
		} else {
			s.logger.Warn("Agent rejected registration, retrying in 5s...", "error", resp.Msg.Error)
		}

		time.Sleep(5 * time.Second)
	}
}
