// Package startup provides agent server initialization and lifecycle management.
package startup

import (
	"context"
	"fmt"
	"time"

	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/agent"
	"github.com/coral-mesh/coral/internal/agent/enrollmentstate"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/logging"
	"github.com/coral-mesh/coral/pkg/version"
)

// AgentServer represents a running agent server.
type AgentServer struct {
	AgentInstance        *agent.Agent
	RuntimeService       *agent.RuntimeService
	OTLPReceiver         *agent.TelemetryReceiver
	SystemMetricsHandler *agent.SystemMetricsHandler
	ConnectionManager    *ConnectionManager
	BootstrapResult      *BootstrapResult // RFD 048: Certificate bootstrap result.
	NetworkResult        *NetworkResult
	StorageResult        *StorageResult
	ServicesResult       *ServicesResult
	MeshPingServer       *agent.MeshPingServer
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

	// Stop mesh ping echo receiver.
	if as.MeshPingServer != nil {
		if err := as.MeshPingServer.Stop(); err != nil {
			as.Logger.Error().Err(err).Msg("Failed to stop mesh ping echo receiver")
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
	noMonitorAll     bool
	ctx              context.Context

	// Phase results.
	configResult    *ConfigResult
	bootstrapResult *BootstrapResult // RFD 048: Certificate bootstrap result.
	networkResult   *NetworkResult
	storageResult   *StorageResult
	servicesResult  *ServicesResult

	// Components.
	agentID           string
	runtimeService    *agent.RuntimeService
	connectionManager *ConnectionManager
	agentInstance     *agent.Agent
}

// NewAgentServerBuilder creates a new agent server builder. noMonitorAll
// corresponds to the --no-monitor-all opt-out flag (RFD 103).
func NewAgentServerBuilder(
	ctx context.Context,
	logger logging.Logger,
	configFile string,
	colonyIDOverride string,
	noMonitorAll bool,
) *AgentServerBuilder {
	return &AgentServerBuilder{
		ctx:              ctx,
		logger:           logger,
		configFile:       configFile,
		colonyIDOverride: colonyIDOverride,
		noMonitorAll:     noMonitorAll,
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
	configValidator := NewConfigValidator(b.logger, b.configFile, b.colonyIDOverride, b.noMonitorAll)
	configResult, err := configValidator.Validate()
	if err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}
	b.configResult = configResult

	// Generate agent ID early.
	b.agentID = GenerateAgentID(configResult.ServiceSpecs)

	return nil
}

// InitializeBootstrap performs certificate bootstrap (RFD 048/RFD 109).
// Network identity and runtime detection must already be initialized so a
// rendezvous fallback can perform compound mesh enrollment.
// Bootstrap is required - agents must have CA fingerprint configured.
func (b *AgentServerBuilder) InitializeBootstrap() error {
	if b.configResult == nil || b.networkResult == nil || b.runtimeService == nil {
		return fmt.Errorf("must initialize network and runtime before InitializeBootstrap()")
	}

	serviceInfos := make([]*meshv1.ServiceInfo, len(b.configResult.ServiceSpecs))
	for i, spec := range b.configResult.ServiceSpecs {
		serviceInfos[i] = spec.ToProto()
	}

	bootstrapPhase := NewBootstrapPhase(
		b.logger,
		b.configResult.AgentConfig,
		b.configResult.Config.ColonyID,
		b.agentID,
		b.networkResult.AgentKeys,
		serviceInfos,
		b.runtimeService.GetCachedContext(),
		version.Version,
		b.networkResult.AgentObservedEndpoint,
	)

	// Check if bootstrap is configured.
	if !bootstrapPhase.ShouldBootstrap() {
		return fmt.Errorf("certificate bootstrap required: set ca_fingerprint or CORAL_CA_FINGERPRINT")
	}

	// Execute bootstrap phase.
	result, err := bootstrapPhase.Execute(b.ctx)
	if err != nil {
		return fmt.Errorf("certificate bootstrap failed: %w", err)
	}

	b.bootstrapResult = result
	return nil
}

// InitializeRuntime starts runtime detection before bootstrap so RFD 109 can
// include the same runtime context ordinary registration sends.
func (b *AgentServerBuilder) InitializeRuntime() error {
	if b.configResult == nil {
		return fmt.Errorf("must call Validate() before InitializeRuntime()")
	}
	if b.runtimeService != nil {
		return nil
	}

	runtimeService, err := agent.NewRuntimeService(agent.RuntimeServiceConfig{
		Context:         b.ctx,
		AgentID:         b.agentID,
		Logger:          b.logger,
		Version:         version.Version,
		RefreshInterval: 5 * time.Minute,
	})
	if err != nil {
		return fmt.Errorf("failed to create runtime service: %w", err)
	}
	if err := runtimeService.Start(); err != nil {
		return fmt.Errorf("failed to start runtime service: %w", err)
	}
	b.runtimeService = runtimeService
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
	if b.configResult == nil || b.storageResult == nil || b.runtimeService == nil {
		return fmt.Errorf("must initialize runtime and storage before CreateAgentInstance()")
	}

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

	// RFD 109 already registered the Agent while the NAT-local Colony held
	// the reverse rendezvous connection. Configure the returned assignment
	// with an empty peer endpoint and skip ordinary HTTP registration, which
	// cannot reach a loopback/mesh-only Colony before WireGuard is up.
	if b.bootstrapResult != nil && b.bootstrapResult.Registration != nil {
		registration := b.bootstrapResult.Registration
		if err := connMgr.ApplyBootstrapRegistration(registration); err != nil {
			return fmt.Errorf("invalid compound bootstrap registration: %w", err)
		}

		// InitializeNetwork tolerates an early Discovery miss, while bootstrap
		// retries its own lookup. Keep retrying here if bootstrap outlived that
		// miss; configuring the dynamic Colony peer still needs its public key
		// and mesh addresses. A temporarily absent Colony must not terminate an
		// otherwise enrolled Agent.
		if b.networkResult.ColonyInfo == nil {
			colonyInfo, err := connMgr.WaitForDiscovery(b.ctx)
			if err != nil {
				return fmt.Errorf("stopped while waiting to refresh colony information after compound bootstrap: %w", err)
			}
			b.networkResult.ColonyInfo = colonyInfo
		}

		networkInitializer := NewNetworkInitializer(
			b.logger,
			b.configResult.Config,
			b.configResult.AgentConfig,
			b.configResult.ServiceSpecs,
			b.agentID,
		)
		if err := networkInitializer.ConfigureMesh(
			b.networkResult,
			registration.AssignedIp,
			registration.MeshSubnet,
			"",
		); err != nil {
			return fmt.Errorf("failed to configure mesh from compound bootstrap registration: %w", err)
		}

		b.logger.Info().
			Str("agent_id", b.agentID).
			Str("mesh_ip", registration.AssignedIp).
			Msg("Agent enrolled through rendezvous bootstrap; skipped ordinary colony registration")
		return nil
	}

	// Attempt initial registration with colony.
	meshIPStr, meshSubnetStr, err := connMgr.AttemptRegistration()
	if err != nil {
		b.logger.Warn().
			Err(err).
			Msg("Failed initial registration with colony - will retry in background")
		return nil // Continue, reconnection loop will handle retries
	}

	// Commit the enrollment checkpoint for ordinary (non-rendezvous)
	// registration too, so a restart after this point reuses the same
	// WireGuard identity and mesh assignment instead of re-registering.
	if b.bootstrapResult != nil && b.bootstrapResult.CertManager != nil {
		if err := b.commitOrdinaryEnrollment(meshIPStr, meshSubnetStr); err != nil {
			b.logger.Warn().Err(err).Msg("Failed to commit enrollment checkpoint after colony registration")
		}
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

// commitOrdinaryEnrollment persists the enrollment checkpoint after an
// ordinary (non-RFD-109-rendezvous) colony registration succeeds, mirroring
// the commit BootstrapPhase performs for compound enrollment.
func (b *AgentServerBuilder) commitOrdinaryEnrollment(meshIP, meshSubnet string) error {
	certManager := b.bootstrapResult.CertManager
	info := certManager.GetCertificateInfo()
	if info == nil {
		return fmt.Errorf("no certificate info available to commit enrollment checkpoint")
	}

	certSHA256, err := certSHA256Hex(certManager.GetCertPath())
	if err != nil {
		return fmt.Errorf("failed to hash certificate: %w", err)
	}

	store := enrollmentstate.NewStore(certManager.GetCertsDir(), b.logger)
	_, err = store.CommitEnrollment(
		b.agentID,
		b.configResult.Config.ColonyID,
		b.networkResult.AgentKeys.PublicKey,
		meshIP,
		meshSubnet,
		certSHA256,
		info.SerialNumber,
	)
	return err
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
		b.storageResult.SessionID,
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
		BootstrapResult:      b.bootstrapResult, // RFD 048
		NetworkResult:        b.networkResult,
		StorageResult:        b.storageResult,
		ServicesResult:       b.servicesResult,
		MeshPingServer:       b.servicesResult.MeshPingServer,
		Logger:               b.logger,
	}
}

// Config returns the loaded agent config.
// You must call Validate prior to this to ensure the config is loaded.
// This will return nil otherwise.
func (b *AgentServerBuilder) Config() *config.AgentConfig {
	if b.configResult == nil {
		return nil
	}
	return b.configResult.AgentConfig
}
