// Package startup provides agent server initialization and lifecycle management.
package startup

import (
	"context"
	"fmt"
	"time"

	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/agent"
	"github.com/coral-mesh/coral/internal/logging"
)

// AgentServer represents a running agent server.
type AgentServer struct {
	AgentInstance        *agent.Agent
	RuntimeService       *agent.RuntimeService
	OTLPReceiver         *agent.TelemetryReceiver
	SystemMetricsHandler *agent.SystemMetricsHandler
	ConnectionManager    *ConnectionManager
	NetworkResult        *NetworkResult
	StorageResult        *StorageResult
	ServicesResult       *ServicesResult
	Logger               logging.Logger
}

// Stop gracefully stops the agent server.
func (as *AgentServer) Stop() error {
	as.Logger.Info().Msg("Stopping agent server...")

	// Cancel context to stop background operations.
	if as.ServicesResult != nil && as.ServicesResult.CancelFunc != nil {
		as.ServicesResult.CancelFunc()
	}

	// Shutdown HTTP servers.
	if as.ServicesResult != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if as.ServicesResult.MeshServer != nil {
			if err := as.ServicesResult.MeshServer.Shutdown(shutdownCtx); err != nil {
				as.Logger.Error().Err(err).Msg("Failed to shutdown mesh API server")
			}
		}

		if as.ServicesResult.LocalhostServer != nil {
			if err := as.ServicesResult.LocalhostServer.Shutdown(shutdownCtx); err != nil {
				as.Logger.Error().Err(err).Msg("Failed to shutdown localhost API server")
			}
		}
	}

	// Stop OTLP receiver.
	if as.OTLPReceiver != nil {
		if err := as.OTLPReceiver.Stop(); err != nil {
			as.Logger.Error().Err(err).Msg("Failed to stop OTLP receiver")
		}
	}

	// Stop agent instance.
	if as.AgentInstance != nil {
		if err := as.AgentInstance.Stop(); err != nil {
			as.Logger.Error().Err(err).Msg("Failed to stop agent instance")
		}
	}

	// Stop runtime service.
	if as.RuntimeService != nil {
		if err := as.RuntimeService.Stop(); err != nil {
			as.Logger.Error().Err(err).Msg("Failed to stop runtime service")
		}
	}

	// Stop WireGuard device.
	if as.NetworkResult != nil && as.NetworkResult.WireGuardDevice != nil {
		if err := as.NetworkResult.WireGuardDevice.Stop(); err != nil {
			as.Logger.Error().Err(err).Msg("Failed to stop WireGuard device")
		}
	}

	// Close shared database.
	if as.StorageResult != nil && as.StorageResult.SharedDB != nil {
		as.Logger.Info().Msg("Closing shared database")
		if err := as.StorageResult.SharedDB.Close(); err != nil {
			as.Logger.Error().Err(err).Msg("Failed to close shared database")
		} else {
			as.Logger.Info().Msg("Closed shared database")
		}
	}

	as.Logger.Info().Msg("Agent server stopped")
	return nil
}

// AgentServerBuilder builds an agent server using the builder pattern.
type AgentServerBuilder struct {
	logger           logging.Logger
	configFile       string
	colonyIDOverride string
	connectServices  []string
	monitorAll       bool
	ctx              context.Context

	// Phase results.
	configResult   *ConfigResult
	networkResult  *NetworkResult
	storageResult  *StorageResult
	servicesResult *ServicesResult

	// Components.
	agentID           string
	runtimeService    *agent.RuntimeService
	connectionManager *ConnectionManager
	agentInstance     *agent.Agent
}

// NewAgentServerBuilder creates a new agent server builder.
func NewAgentServerBuilder(
	ctx context.Context,
	logger logging.Logger,
	configFile string,
	colonyIDOverride string,
	connectServices []string,
	monitorAll bool,
) *AgentServerBuilder {
	return &AgentServerBuilder{
		ctx:              ctx,
		logger:           logger,
		configFile:       configFile,
		colonyIDOverride: colonyIDOverride,
		connectServices:  connectServices,
		monitorAll:       monitorAll,
	}
}

// Validate performs preflight checks and config validation.
func (b *AgentServerBuilder) Validate() error {
	// Phase 1a: Preflight checks.
	preflightValidator := NewPreflightValidator(b.logger)
	if err := preflightValidator.Validate(); err != nil {
		return fmt.Errorf("preflight validation failed: %w", err)
	}

	// Phase 1b: Config validation.
	configValidator := NewConfigValidator(b.logger, b.configFile, b.colonyIDOverride, b.connectServices, b.monitorAll)
	configResult, err := configValidator.Validate()
	if err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}
	b.configResult = configResult

	// Generate agent ID early.
	b.agentID = GenerateAgentID(configResult.ServiceSpecs)

	return nil
}

// InitializeNetwork performs network setup (WireGuard, STUN, discovery).
func (b *AgentServerBuilder) InitializeNetwork() error {
	if b.configResult == nil {
		return fmt.Errorf("must call Validate() before InitializeNetwork()")
	}

	networkInitializer := NewNetworkInitializer(
		b.logger,
		b.configResult.Config,
		b.configResult.AgentConfig,
		b.configResult.ServiceSpecs,
		b.agentID,
	)

	networkResult, err := networkInitializer.Initialize()
	if err != nil {
		return fmt.Errorf("network initialization failed: %w", err)
	}
	b.networkResult = networkResult

	return nil
}

// InitializeStorage performs storage setup (DuckDB, function cache).
func (b *AgentServerBuilder) InitializeStorage() error {
	if b.configResult == nil {
		return fmt.Errorf("must call Validate() before InitializeStorage()")
	}

	storageManager := NewStorageManager(
		b.logger,
		b.configResult.AgentConfig,
		b.configResult.ServiceSpecs,
		b.monitorAll,
		b.agentID,
	)

	storageResult, err := storageManager.Initialize()
	if err != nil {
		return fmt.Errorf("storage initialization failed: %w", err)
	}
	b.storageResult = storageResult

	return nil
}

// CreateAgentInstance creates the agent instance and runtime service.
func (b *AgentServerBuilder) CreateAgentInstance() error {
	if b.configResult == nil || b.storageResult == nil {
		return fmt.Errorf("must call Validate() and InitializeStorage() before CreateAgentInstance()")
	}

	// Create runtime service early (RFD 018).
	runtimeService, err := agent.NewRuntimeService(agent.RuntimeServiceConfig{
		Context:         b.ctx,
		AgentID:         b.agentID,
		Logger:          b.logger,
		Version:         "dev", // TODO: Get version from build info
		RefreshInterval: 5 * time.Minute,
	})
	if err != nil {
		return fmt.Errorf("failed to create runtime service: %w", err)
	}

	if err := runtimeService.Start(); err != nil {
		return fmt.Errorf("failed to start runtime service: %w", err)
	}
	b.runtimeService = runtimeService

	// Create agent instance.
	serviceInfos := make([]*meshv1.ServiceInfo, len(b.configResult.ServiceSpecs))
	for i, spec := range b.configResult.ServiceSpecs {
		serviceInfos[i] = spec.ToProto()
	}

	agentInstance, err := agent.New(agent.Config{
		Context:       b.ctx,
		AgentID:       b.agentID,
		Services:      serviceInfos,
		BeylaConfig:   b.storageResult.BeylaConfig,
		FunctionCache: b.storageResult.FunctionCache,
		Logger:        b.logger,
	})
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}

	if err := agentInstance.Start(); err != nil {
		return fmt.Errorf("failed to start agent: %w", err)
	}
	b.agentInstance = agentInstance

	return nil
}

// RegisterWithColony performs colony registration and mesh configuration.
func (b *AgentServerBuilder) RegisterWithColony() error {
	if b.configResult == nil || b.networkResult == nil || b.runtimeService == nil {
		return fmt.Errorf("must initialize network and create agent instance before registering with colony")
	}

	// Create connection manager.
	connMgr := NewConnectionManager(
		b.agentID,
		b.networkResult.ColonyInfo,
		b.configResult.Config,
		b.configResult.ServiceSpecs,
		b.networkResult.AgentKeys.PublicKey,
		b.networkResult.WireGuardDevice,
		b.runtimeService,
		b.logger,
	)
	b.connectionManager = connMgr

	// Attempt initial registration with colony.
	meshIPStr, meshSubnetStr, err := connMgr.AttemptRegistration()
	if err != nil {
		b.logger.Warn().
			Err(err).
			Msg("Failed initial registration with colony - will retry in background")
		return nil // Continue, reconnection loop will handle retries
	}

	// Configure mesh network.
	colonyEndpoint := connMgr.GetColonyEndpoint()
	if colonyEndpoint == "" {
		return fmt.Errorf("no colony endpoint available for mesh configuration")
	}

	networkInitializer := NewNetworkInitializer(
		b.logger,
		b.configResult.Config,
		b.configResult.AgentConfig,
		b.configResult.ServiceSpecs,
		b.agentID,
	)

	if err := networkInitializer.ConfigureMesh(b.networkResult, meshIPStr, meshSubnetStr, colonyEndpoint); err != nil {
		return fmt.Errorf("failed to configure mesh: %w", err)
	}

	// Log connection status.
	currentIP, _ := connMgr.GetAssignedIP()
	currentState := connMgr.GetState()
	if currentIP != "" {
		b.logger.Info().
			Str("agent_id", b.agentID).
			Str("mesh_ip", currentIP).
			Int("service_count", len(b.configResult.ServiceSpecs)).
			Str("state", currentState.String()).
			Msg("Agent connected successfully")
	} else if currentState == StateWaitingDiscovery {
		b.logger.Info().
			Str("agent_id", b.agentID).
			Int("service_count", len(b.configResult.ServiceSpecs)).
			Str("state", currentState.String()).
			Msg("Agent started (waiting for discovery service - will connect when available)")
	} else {
		b.logger.Info().
			Str("agent_id", b.agentID).
			Int("service_count", len(b.configResult.ServiceSpecs)).
			Str("state", currentState.String()).
			Msg("Agent started (unregistered - attempting reconnection in background)")
	}

	return nil
}

// RegisterServices creates and registers all services.
func (b *AgentServerBuilder) RegisterServices() error {
	if b.agentInstance == nil || b.runtimeService == nil || b.storageResult == nil {
		return fmt.Errorf("must create agent instance before registering services")
	}

	meshIP := ""
	meshSubnet := ""
	if b.networkResult != nil {
		meshIP = b.networkResult.MeshIP
		meshSubnet = b.networkResult.MeshSubnet
	}

	serviceRegistry := NewServiceRegistry(
		b.agentInstance.GetContext(),
		b.logger,
		b.configResult.AgentConfig,
		b.configResult.Config,
		b.configResult.ServiceSpecs,
		b.agentID,
		b.storageResult.SharedDB,
		b.storageResult.SharedDBPath,
		b.storageResult.FunctionCache,
		b.agentInstance,
		b.networkResult.WireGuardDevice,
		b.networkResult.ColonyInfo,
		meshIP,
		meshSubnet,
		b.connectionManager,
	)

	servicesResult, err := serviceRegistry.Register(b.runtimeService)
	if err != nil {
		return fmt.Errorf("service registration failed: %w", err)
	}
	b.servicesResult = servicesResult

	// Log initial status.
	if len(b.configResult.ServiceSpecs) > 0 {
		b.logger.Info().
			Str("status", string(b.agentInstance.GetStatus())).
			Msg("Agent status")

		for name, status := range b.agentInstance.GetServiceStatuses() {
			b.logger.Info().
				Str("service", name).
				Str("status", string(status.Status)).
				Msg("Service status")
		}
	} else {
		b.logger.Info().Msg("Agent started in passive mode - waiting for service connections via 'coral connect'")
	}

	return nil
}

// Build creates and returns the agent server.
func (b *AgentServerBuilder) Build() *AgentServer {
	return &AgentServer{
		AgentInstance:        b.agentInstance,
		RuntimeService:       b.runtimeService,
		OTLPReceiver:         b.servicesResult.OTLPReceiver,
		SystemMetricsHandler: b.servicesResult.SystemMetricsHandler,
		ConnectionManager:    b.connectionManager,
		NetworkResult:        b.networkResult,
		StorageResult:        b.storageResult,
		ServicesResult:       b.servicesResult,
		Logger:               b.logger,
	}
}
