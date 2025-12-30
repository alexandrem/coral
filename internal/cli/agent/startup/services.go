package startup

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	discoverypb "github.com/coral-mesh/coral/coral/discovery/v1"
	"github.com/coral-mesh/coral/coral/mesh/v1/meshv1connect"
	"github.com/coral-mesh/coral/internal/agent"
	"github.com/coral-mesh/coral/internal/agent/collector"
	"github.com/coral-mesh/coral/internal/agent/profiler"
	"github.com/coral-mesh/coral/internal/agent/telemetry"
	"github.com/coral-mesh/coral/internal/cli/agent/types"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/duckdb"
	"github.com/coral-mesh/coral/internal/logging"
	"github.com/coral-mesh/coral/internal/wireguard"
)

// ServicesResult contains the results of service initialization.
type ServicesResult struct {
	AgentInstance        *agent.Agent
	RuntimeService       *agent.RuntimeService
	OTLPReceiver         *agent.TelemetryReceiver
	SystemMetricsHandler *agent.SystemMetricsHandler
	MeshServer           *http.Server
	LocalhostServer      *http.Server
	Context              context.Context
	CancelFunc           context.CancelFunc
}

// ServiceRegistry handles service registration and HTTP servers.
type ServiceRegistry struct {
	logger        logging.Logger
	agentCfg      *config.AgentConfig
	cfg           *config.ResolvedConfig
	serviceSpecs  []*types.ServiceSpec
	agentID       string
	sharedDB      *sql.DB
	sharedDBPath  string
	functionCache *agent.FunctionCache
	agentInstance *agent.Agent
	wgDevice      *wireguard.Device
	colonyInfo    *discoverypb.LookupColonyResponse
	meshIP        string
	meshSubnet    string
}

// NewServiceRegistry creates a new service registry.
func NewServiceRegistry(
	logger logging.Logger,
	agentCfg *config.AgentConfig,
	cfg *config.ResolvedConfig,
	serviceSpecs []*types.ServiceSpec,
	agentID string,
	sharedDB *sql.DB,
	sharedDBPath string,
	functionCache *agent.FunctionCache,
	agentInstance *agent.Agent,
	wgDevice *wireguard.Device,
	colonyInfo *discoverypb.LookupColonyResponse,
	meshIP string,
	meshSubnet string,
) *ServiceRegistry {
	return &ServiceRegistry{
		logger:        logger,
		agentCfg:      agentCfg,
		cfg:           cfg,
		serviceSpecs:  serviceSpecs,
		agentID:       agentID,
		sharedDB:      sharedDB,
		sharedDBPath:  sharedDBPath,
		functionCache: functionCache,
		agentInstance: agentInstance,
		wgDevice:      wgDevice,
		colonyInfo:    colonyInfo,
		meshIP:        meshIP,
		meshSubnet:    meshSubnet,
	}
}

// Register initializes and registers all services.
func (s *ServiceRegistry) Register(runtimeService *agent.RuntimeService) (*ServicesResult, error) {
	result := &ServicesResult{
		RuntimeService: runtimeService,
		AgentInstance:  s.agentInstance,
	}

	// Create context for background operations.
	ctx, cancel := context.WithCancel(context.Background())
	result.Context = ctx
	result.CancelFunc = cancel

	// Start OTLP receiver if telemetry is not disabled (RFD 025).
	var otlpReceiver *agent.TelemetryReceiver
	if !s.agentCfg.Telemetry.Disabled && s.sharedDB != nil {
		s.logger.Info().Msg("Starting OTLP receiver for telemetry collection")

		telemetryConfig := s.buildTelemetryConfig()

		otlpReceiver, err := agent.NewTelemetryReceiverWithSharedDB(telemetryConfig, s.sharedDB, s.sharedDBPath, s.logger)
		if err != nil {
			s.logger.Warn().Err(err).Msg("Failed to create OTLP receiver - telemetry disabled")
		} else {
			if err := otlpReceiver.Start(ctx); err != nil {
				s.logger.Warn().Err(err).Msg("Failed to start OTLP receiver - telemetry disabled")
			} else {
				s.logger.Info().
					Str("grpc_endpoint", telemetryConfig.GRPCEndpoint).
					Str("http_endpoint", telemetryConfig.HTTPEndpoint).
					Float64("sample_rate", telemetryConfig.Filters.SampleRate).
					Float64("latency_threshold_ms", telemetryConfig.Filters.HighLatencyThresholdMs).
					Msg("OTLP receiver started successfully")
				result.OTLPReceiver = otlpReceiver
			}
		}
	} else if s.agentCfg.Telemetry.Disabled {
		s.logger.Info().Msg("Telemetry collection is disabled")
	} else {
		s.logger.Warn().Msg("Telemetry disabled - shared database not available")
	}

	// Start system metrics collector if enabled (RFD 071).
	if !s.agentCfg.SystemMetrics.Disabled && s.sharedDB != nil {
		systemMetricsHandler, err := s.startSystemMetricsCollector(ctx)
		if err != nil {
			s.logger.Warn().Err(err).Msg("Failed to start system metrics collector")
		} else {
			result.SystemMetricsHandler = systemMetricsHandler
		}
	} else if s.agentCfg.SystemMetrics.Disabled {
		s.logger.Info().Msg("System metrics collection is disabled")
	} else {
		s.logger.Warn().Msg("System metrics disabled - shared database not available")
	}

	// Initialize continuous CPU profiling (RFD 072).
	if s.sharedDB != nil && !s.agentCfg.ContinuousProfiling.Disabled && !s.agentCfg.ContinuousProfiling.CPU.Disabled {
		if err := s.startContinuousCPUProfiler(); err != nil {
			s.logger.Warn().Err(err).Msg("Failed to start continuous CPU profiler")
		}
	} else if !s.agentCfg.ContinuousProfiling.Disabled && s.agentCfg.ContinuousProfiling.CPU.Disabled {
		s.logger.Info().Msg("Continuous CPU profiling is disabled via configuration")
	} else if s.agentCfg.ContinuousProfiling.Disabled {
		s.logger.Info().Msg("Continuous profiling is disabled")
	} else {
		s.logger.Warn().Msg("Continuous profiling disabled - shared database not available")
	}

	// Create and register HTTP servers.
	meshServer, localhostServer, err := s.createHTTPServers(runtimeService, otlpReceiver, result.SystemMetricsHandler)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP servers: %w", err)
	}

	result.MeshServer = meshServer
	result.LocalhostServer = localhostServer

	return result, nil
}

// buildTelemetryConfig creates telemetry configuration from agent config.
func (s *ServiceRegistry) buildTelemetryConfig() telemetry.Config {
	telemetryConfig := telemetry.Config{
		Disabled:              s.agentCfg.Telemetry.Disabled,
		GRPCEndpoint:          s.agentCfg.Telemetry.GRPCEndpoint,
		HTTPEndpoint:          s.agentCfg.Telemetry.HTTPEndpoint,
		DatabasePath:          s.sharedDBPath,
		StorageRetentionHours: s.agentCfg.Telemetry.StorageRetentionHours,
		AgentID:               s.agentID,
	}

	// Set filter config with defaults if not specified.
	if s.agentCfg.Telemetry.Filters.AlwaysCaptureErrors {
		telemetryConfig.Filters.AlwaysCaptureErrors = true
	} else {
		telemetryConfig.Filters.AlwaysCaptureErrors = true // Default: true
	}

	if s.agentCfg.Telemetry.Filters.HighLatencyThresholdMs > 0 {
		telemetryConfig.Filters.HighLatencyThresholdMs = s.agentCfg.Telemetry.Filters.HighLatencyThresholdMs
	} else {
		telemetryConfig.Filters.HighLatencyThresholdMs = 500.0 // Default: 500ms
	}

	if s.agentCfg.Telemetry.Filters.SampleRate > 0 {
		telemetryConfig.Filters.SampleRate = s.agentCfg.Telemetry.Filters.SampleRate
	} else {
		telemetryConfig.Filters.SampleRate = 0.10 // Default: 10%
	}

	// Set default endpoints if not specified.
	if telemetryConfig.GRPCEndpoint == "" {
		telemetryConfig.GRPCEndpoint = "0.0.0.0:4317"
	}
	if telemetryConfig.HTTPEndpoint == "" {
		telemetryConfig.HTTPEndpoint = "0.0.0.0:4318"
	}
	if telemetryConfig.StorageRetentionHours == 0 {
		telemetryConfig.StorageRetentionHours = 1 // Default: 1 hour
	}

	return telemetryConfig
}

// startSystemMetricsCollector initializes and starts system metrics collection.
func (s *ServiceRegistry) startSystemMetricsCollector(ctx context.Context) (*agent.SystemMetricsHandler, error) {
	s.logger.Info().Msg("Initializing system metrics collector")

	// Create collector config from agent config.
	collectorConfig := collector.Config{
		Enabled:        !s.agentCfg.SystemMetrics.Disabled,
		Interval:       s.agentCfg.SystemMetrics.Interval,
		CPUEnabled:     s.agentCfg.SystemMetrics.CPUEnabled,
		MemoryEnabled:  s.agentCfg.SystemMetrics.MemoryEnabled,
		DiskEnabled:    s.agentCfg.SystemMetrics.DiskEnabled,
		NetworkEnabled: s.agentCfg.SystemMetrics.NetworkEnabled,
	}

	// Create storage for system metrics.
	metricsStorage, err := collector.NewStorage(s.sharedDB, s.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create system metrics storage: %w", err)
	}

	// Create and start collector.
	systemCollector := collector.NewSystemCollector(metricsStorage, collectorConfig, s.logger)

	// Start collector in background.
	go func() {
		if err := systemCollector.Start(ctx); err != nil && err != context.Canceled {
			s.logger.Error().Err(err).Msg("System metrics collector stopped with error")
		}
	}()

	// Start cleanup goroutine for old metrics (1 hour retention).
	go func() {
		cleanupTicker := time.NewTicker(10 * time.Minute)
		defer cleanupTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-cleanupTicker.C:
				if err := metricsStorage.CleanupOldMetrics(ctx, s.agentCfg.SystemMetrics.Retention); err != nil {
					s.logger.Error().Err(err).Msg("Failed to cleanup old system metrics")
				} else {
					s.logger.Debug().Msg("Cleaned up old system metrics")
				}
			}
		}
	}()

	// Create system metrics handler for RPC queries.
	systemMetricsHandler := agent.NewSystemMetricsHandler(metricsStorage)

	s.logger.Info().
		Dur("interval", collectorConfig.Interval).
		Dur("retention", s.agentCfg.SystemMetrics.Retention).
		Bool("cpu", collectorConfig.CPUEnabled).
		Bool("memory", collectorConfig.MemoryEnabled).
		Bool("disk", collectorConfig.DiskEnabled).
		Bool("network", collectorConfig.NetworkEnabled).
		Msg("System metrics collector started successfully")

	return systemMetricsHandler, nil
}

// startContinuousCPUProfiler initializes and starts continuous CPU profiling.
func (s *ServiceRegistry) startContinuousCPUProfiler() error {
	s.logger.Info().Msg("Initializing continuous CPU profiling")

	profilerConfig := profiler.Config{
		Enabled:           !s.agentCfg.ContinuousProfiling.CPU.Disabled,
		FrequencyHz:       s.agentCfg.ContinuousProfiling.CPU.FrequencyHz,
		Interval:          s.agentCfg.ContinuousProfiling.CPU.Interval,
		SampleRetention:   s.agentCfg.ContinuousProfiling.CPU.Retention,
		MetadataRetention: s.agentCfg.ContinuousProfiling.CPU.MetadataRetention,
	}

	// Get debug manager for kernel symbolizer access.
	debugManager := s.agentInstance.GetDebugManager()

	cpuProfiler, err := profiler.NewContinuousCPUProfiler(
		s.sharedDB,
		debugManager,
		s.logger,
		profilerConfig,
	)
	if err != nil {
		return fmt.Errorf("failed to create continuous CPU profiler: %w", err)
	}

	// Build list of services to profile.
	var profilingServices []profiler.ServiceInfo
	for _, spec := range s.serviceSpecs {
		serviceInfo := spec.ToProto()
		if serviceInfo.ProcessId > 0 {
			profilingServices = append(profilingServices, profiler.ServiceInfo{
				ServiceID:  serviceInfo.Name,
				PID:        int(serviceInfo.ProcessId),
				BinaryPath: fmt.Sprintf("/proc/%d/exe", serviceInfo.ProcessId),
			})
		}
	}

	// Set profiler on agent for RPC access.
	s.agentInstance.SetContinuousProfiler(cpuProfiler)

	// Start profiling.
	if len(profilingServices) > 0 {
		cpuProfiler.Start(profilingServices)
		s.logger.Info().
			Int("frequency_hz", profilerConfig.FrequencyHz).
			Dur("interval", profilerConfig.Interval).
			Int("service_count", len(profilingServices)).
			Msg("Continuous CPU profiling started successfully")
	} else {
		s.logger.Info().Msg("No services with PIDs to profile - continuous profiling ready for new services")
	}

	return nil
}

// createHTTPServers creates mesh and localhost HTTP servers.
func (s *ServiceRegistry) createHTTPServers(
	runtimeService *agent.RuntimeService,
	otlpReceiver *agent.TelemetryReceiver,
	systemMetricsHandler *agent.SystemMetricsHandler,
) (*http.Server, *http.Server, error) {
	// Create shell handler (RFD 026).
	shellHandler := agent.NewShellHandler(s.logger)

	// Create container handler (RFD 056).
	containerHandler := agent.NewContainerHandler(s.logger)

	// Create service handler and HTTP server for gRPC API.
	serviceHandler := agent.NewServiceHandler(s.agentInstance, runtimeService, otlpReceiver, shellHandler, containerHandler, s.functionCache, systemMetricsHandler)
	path, handler := agentv1connect.NewAgentServiceHandler(serviceHandler)

	// Create debug service handler (RFD 059).
	debugService := agent.NewDebugService(s.agentInstance, s.logger)
	debugAdapter := agent.NewDebugServiceAdapter(debugService)
	debugPath, debugHandler := meshv1connect.NewDebugServiceHandler(debugAdapter)

	mux := http.NewServeMux()
	mux.Handle(path, handler)
	mux.Handle(debugPath, debugHandler)

	// Add /status endpoint.
	mux.HandleFunc("/status", s.createStatusHandler(runtimeService))

	// Add /duckdb/ endpoint for serving DuckDB files (RFD 039).
	s.registerDuckDBHandler(mux)

	// Enable HTTP/2 Cleartext (h2c) for bidirectional streaming (RFD 026).
	h2s := &http2.Server{}
	httpHandler := h2c.NewHandler(mux, h2s)

	// Create mesh server.
	var meshServer *http.Server
	if s.meshIP != "" {
		meshAddr := net.JoinHostPort(s.meshIP, "9001")
		//nolint:gosec // G112: ReadHeaderTimeout will be added in future refactoring
		meshServer = &http.Server{
			Addr:    meshAddr,
			Handler: httpHandler,
		}

		go func() {
			s.logger.Info().
				Str("addr", meshAddr).
				Msg("Agent API listening on WireGuard mesh")

			if err := meshServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				s.logger.Error().
					Err(err).
					Str("addr", meshAddr).
					Msg("Mesh API server error")
			}
		}()
	} else {
		s.logger.Warn().Msg("No mesh IP available, skipping mesh server (agent not registered)")
	}

	// Create localhost server.
	localhostAddr := "127.0.0.1:9001"
	if os.Getenv("CORAL_AGENT_BIND_ALL") == "true" {
		localhostAddr = "0.0.0.0:9001"
	}
	localhostServer := &http.Server{
		Addr:              localhostAddr,
		Handler:           httpHandler,
		ReadHeaderTimeout: 30 * time.Second,
	}

	go func() {
		s.logger.Info().
			Str("addr", localhostAddr).
			Msg("Agent API listening on localhost")

		if err := localhostServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error().
				Err(err).
				Str("addr", localhostAddr).
				Msg("Localhost API server error")
		}
	}()

	return meshServer, localhostServer, nil
}

// createStatusHandler creates the /status endpoint handler.
func (s *ServiceRegistry) createStatusHandler(runtimeService *agent.RuntimeService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get runtime context from cache directly.
		runtimeCtx := runtimeService.GetCachedContext()
		if runtimeCtx == nil {
			http.Error(w, "runtime context not yet detected", http.StatusServiceUnavailable)
			return
		}

		// Gather mesh network information for debugging.
		meshInfo := s.gatherMeshNetworkInfo()

		// Combine runtime context with mesh info.
		status := map[string]interface{}{
			"runtime": runtimeCtx,
			"mesh":    meshInfo,
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(status); err != nil {
			s.logger.Error().Err(err).Msg("Failed to encode status response")
		}
	}
}

// registerDuckDBHandler registers the DuckDB HTTP handler.
func (s *ServiceRegistry) registerDuckDBHandler(mux *http.ServeMux) {
	duckdbHandler := duckdb.NewDuckDBHandler(s.logger)
	registeredCount := 0

	// Register shared metrics database (if using file-based storage).
	if s.sharedDBPath != "" {
		if err := duckdbHandler.RegisterDatabase("metrics.duckdb", s.sharedDBPath); err != nil {
			s.logger.Warn().Err(err).Msg("Failed to register metrics database for HTTP serving")
		} else {
			s.logger.Info().
				Str("db_name", "metrics.duckdb").
				Str("db_path", s.sharedDBPath).
				Msg("Shared metrics database registered for HTTP serving")
			registeredCount++
		}
	}

	if registeredCount == 0 {
		s.logger.Warn().Msg("No DuckDB databases registered for HTTP serving (all using in-memory storage)")
	} else {
		s.logger.Info().
			Int("count", registeredCount).
			Msg("DuckDB databases available for remote queries")
	}

	mux.Handle("/duckdb/", duckdbHandler)
}

// gatherMeshNetworkInfo collects mesh network debugging information.
func (s *ServiceRegistry) gatherMeshNetworkInfo() map[string]interface{} {
	info := make(map[string]interface{})

	// Basic mesh info.
	info["agent_id"] = s.agentID
	info["mesh_ip"] = s.meshIP
	info["mesh_subnet"] = s.meshSubnet

	// WireGuard interface info.
	if s.wgDevice != nil {
		wgInfo := make(map[string]interface{})
		wgInfo["interface_name"] = s.wgDevice.InterfaceName()
		wgInfo["listen_port"] = s.wgDevice.ListenPort()

		// Get interface status.
		iface := s.wgDevice.Interface()
		if iface != nil {
			wgInfo["interface_exists"] = true

			// Try to get IP addresses.
			//nolint:gosec // G204: Interface name is from controlled WireGuard device
			if addrs, err := exec.Command("ip", "addr", "show", s.wgDevice.InterfaceName()).Output(); err == nil {
				wgInfo["ip_addresses"] = string(addrs)
			}

			// Try to get link status.
			//nolint:gosec // G204: Diagnostic command with validated interface name.
			if link, err := exec.Command("ip", "link", "show", s.wgDevice.InterfaceName()).Output(); err == nil {
				wgInfo["link_status"] = string(link)
			}
		} else {
			wgInfo["interface_exists"] = false
		}

		// Get peer information.
		peers := s.wgDevice.ListPeers()
		peerInfos := make([]map[string]interface{}, 0, len(peers))
		for _, peer := range peers {
			peerInfo := make(map[string]interface{})
			peerInfo["public_key"] = peer.PublicKey[:16] + "..."
			peerInfo["endpoint"] = peer.Endpoint
			peerInfo["allowed_ips"] = peer.AllowedIPs
			peerInfo["persistent_keepalive"] = peer.PersistentKeepalive
			peerInfos = append(peerInfos, peerInfo)
		}
		wgInfo["peers"] = peerInfos
		wgInfo["peer_count"] = len(peers)

		info["wireguard"] = wgInfo
	}

	// Colony info.
	if s.colonyInfo != nil {
		colonyInfoMap := make(map[string]interface{})
		colonyInfoMap["id"] = s.colonyInfo.MeshId
		colonyInfoMap["mesh_ipv4"] = s.colonyInfo.MeshIpv4
		colonyInfoMap["mesh_ipv6"] = s.colonyInfo.MeshIpv6
		colonyInfoMap["connect_port"] = s.colonyInfo.ConnectPort
		colonyInfoMap["endpoints"] = s.colonyInfo.Endpoints

		// Add observed endpoints.
		observedEps := make([]map[string]interface{}, 0, len(s.colonyInfo.ObservedEndpoints))
		for _, ep := range s.colonyInfo.ObservedEndpoints {
			if ep != nil {
				observedEps = append(observedEps, map[string]interface{}{
					"ip":       ep.Ip,
					"port":     ep.Port,
					"protocol": ep.Protocol,
				})
			}
		}
		colonyInfoMap["observed_endpoints"] = observedEps

		info["colony"] = colonyInfoMap
	}

	// Route information.
	if s.wgDevice != nil && s.wgDevice.Interface() != nil {
		//nolint:gosec // G204: Diagnostic command with validated interface name.
		routes, err := exec.Command("ip", "route", "show", "dev", s.wgDevice.InterfaceName()).Output()
		if err == nil {
			info["routes"] = string(routes)
		} else {
			info["routes_error"] = err.Error()
		}

		// Also get all routes for reference.
		allRoutes, err := exec.Command("ip", "route", "show").Output()
		if err == nil {
			info["all_routes"] = string(allRoutes)
		}
	}

	// Connectivity test to colony mesh IP.
	if s.colonyInfo != nil && s.colonyInfo.MeshIpv4 != "" {
		connectPort := s.colonyInfo.ConnectPort
		if connectPort == 0 {
			connectPort = 9000
		}
		meshAddr := net.JoinHostPort(s.colonyInfo.MeshIpv4, fmt.Sprintf("%d", connectPort))

		connTest := make(map[string]interface{})
		connTest["target"] = meshAddr

		// Quick connectivity check.
		conn, err := net.DialTimeout("tcp", meshAddr, 2*time.Second)
		if err != nil {
			connTest["reachable"] = false
			connTest["error"] = err.Error()
		} else {
			connTest["reachable"] = true
			_ = conn.Close() // TODO: errcheck
		}

		info["colony_connectivity"] = connTest

		// Ping test (if available).
		//nolint:gosec // G204: Diagnostic ping command with validated colony mesh IP.
		if pingOut, err := exec.Command("ping", "-c", "1", "-W", "1", s.colonyInfo.MeshIpv4).CombinedOutput(); err == nil {
			info["ping_result"] = string(pingOut)
		} else {
			info["ping_error"] = err.Error()
			info["ping_output"] = string(pingOut)
		}
	}

	return info
}
