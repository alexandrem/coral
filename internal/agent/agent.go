// Package agent implements the coral agent that runs on each node.
package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/agent/beyla"
	"github.com/coral-mesh/coral/internal/agent/beyla/discovery"
	"github.com/coral-mesh/coral/internal/agent/correlation"
	"github.com/coral-mesh/coral/internal/agent/debug"
	"github.com/coral-mesh/coral/internal/agent/ebpf"
	"github.com/coral-mesh/coral/internal/agent/naming"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/sys/proc"
)

// AgentStatus represents the overall agent health status.
type AgentStatus string

const (
	AgentStatusHealthy   AgentStatus = "healthy"
	AgentStatusDegraded  AgentStatus = "degraded"
	AgentStatusUnhealthy AgentStatus = "unhealthy"
)

// Lifecycle is implemented by any component the agent starts and stops.
type Lifecycle interface {
	Start() error
	Stop() error
}

// cpuProfiler is the subset of profiler.ContinuousCPUProfiler used by the agent.
// Defined as an interface to support Linux/non-Linux builds without import cycles.
type cpuProfiler interface {
	AddService(serviceID string, pid int, binaryPath string)
	RemoveService(serviceID string)
	Stop()
}

// memProfiler is the subset of profiler.ContinuousMemoryProfiler used by the agent.
type memProfiler interface {
	AddService(serviceID string, pid int, binaryPath string, sdkAddr string)
	RemoveService(serviceID string)
	Stop()
}

// Agent represents a Coral agent that monitors multiple services.
type Agent struct {
	id                       string
	services                 map[string]*ServiceEntry  // RFD 104/111: unified service map, keyed by serviceKey (port, or name for portless).
	nameAdaptor              naming.ServiceNameAdaptor // RFD 104: derives names for auto-observed services.
	components               []Lifecycle               // Ordered list of managed components; stopped in reverse.
	ebpfManager              *ebpf.Manager
	beylaManager             *beyla.Manager
	debugManager             *debug.SessionManager
	correlationEngine        *correlation.Engine // RFD 091: probe correlation.
	continuousProfiler       cpuProfiler         // RFD 072: Continuous CPU profiler.
	continuousMemoryProfiler memProfiler         // RFD 077: Continuous memory profiler.
	functionCache            *FunctionCache      // RFD 063: Function discovery cache
	logger                   zerolog.Logger
	mu                       sync.RWMutex
	ctx                      context.Context
	cancel                   context.CancelFunc
}

// Config contains agent configuration.
type Config struct {
	Context       context.Context // Parent context for lifecycle management.
	AgentID       string
	Services      []*meshv1.ServiceInfo
	BeylaConfig   *beyla.Config
	DebugConfig   config.DebugConfig
	FunctionCache *FunctionCache // RFD 063: Optional function cache
	Logger        zerolog.Logger
}

// New creates a new agent.
func New(config Config) (*Agent, error) {
	if config.AgentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}
	if config.Context == nil {
		return nil, fmt.Errorf("context is required")
	}

	ctx, cancel := context.WithCancel(config.Context)

	// Initialize eBPF manager.
	ebpfManager := ebpf.NewManager(ebpf.Config{
		Logger: config.Logger,
	})

	// Initialize Beyla manager (RFD 032/110).
	var beylaManager *beyla.Manager
	if config.BeylaConfig != nil {
		// Populate initial service map from provided services.
		if config.BeylaConfig.Discovery.ServiceMap == nil {
			config.BeylaConfig.Discovery.ServiceMap = make(map[int]string)
		}
		for _, svc := range config.Services {
			config.BeylaConfig.Discovery.ServiceMap[int(svc.Port)] = svc.Name
			config.BeylaConfig.Discovery.OpenPorts = append(config.BeylaConfig.Discovery.OpenPorts, int(svc.Port))
		}

		var err error
		beylaManager, err = beyla.NewManager(ctx, config.BeylaConfig, config.Logger)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("failed to create Beyla manager: %w", err)
		}
	}

	// Initialize correlation engine (RFD 091).
	corrEngine := correlation.NewEngine(config.AgentID, config.Logger)

	// Build the ordered component list. ebpfManager starts first so it is
	// available to collectors before Beyla (RFD 032) begins discovery.
	components := []Lifecycle{ebpfManager}
	if beylaManager != nil {
		components = append(components, beylaManager)
	}

	agent := &Agent{
		id:                config.AgentID,
		services:          make(map[string]*ServiceEntry),
		nameAdaptor:       naming.Chain{naming.NewProcessNameAdaptor()},
		components:        components,
		ebpfManager:       ebpfManager,
		beylaManager:      beylaManager,
		correlationEngine: corrEngine,
		functionCache:     config.FunctionCache,
		logger:            config.Logger.With().Str("agent_id", config.AgentID).Logger(),
		ctx:               ctx,
		cancel:            cancel,
	}

	// Wire correlation engine as the eBPF event subscriber (RFD 091).
	ebpfManager.SetEventSubscriber(agent.routeEventsToCorrelation)

	// Wire OTLP feedback so the agent learns about newly observed processes
	// without polling (RFD 103).
	if beylaManager != nil {
		beylaManager.SetServiceObservedHandler(agent.onBeylaServiceObserved)
	}

	// Initialize SessionManager (RFD 061).
	agent.debugManager = debug.NewSessionManager(config.DebugConfig, config.Logger, agent)
	if config.FunctionCache != nil {
		agent.debugManager.SetFunctionCache(config.FunctionCache)
	}

	// Register each explicitly configured service as an authoritative
	// ServiceEntry (RFD 104). Monitors are constructed but not started here;
	// Start() below starts them once the agent is fully assembled.
	for _, service := range config.Services {
		if err := agent.connectServiceLocked(service, false); err != nil {
			agent.logger.Warn().Err(err).Str("service", service.Name).Msg("Failed to register initial service")
		}
	}

	return agent, nil
}

// Start begins monitoring all services.
func (a *Agent) Start() error {
	a.logger.Info().
		Int("service_count", len(a.services)).
		Msg("Starting agent")

	// Start managed components. Failures are logged but non-fatal; components
	// such as Beyla are supplementary to core monitoring.
	for _, c := range a.components {
		if err := c.Start(); err != nil {
			a.logger.Error().Err(err).Msg("Failed to start component")
		}
	}

	// Start all watched (Tier 1) service monitors.
	for _, entry := range a.services {
		if entry.Monitor == nil {
			continue
		}
		a.logger.Debug().Str("service", entry.Name()).Msg("Starting service monitor")
		if err := entry.Monitor.Start(); err != nil {
			a.logger.Error().Err(err).Str("service", entry.Name()).Msg("Failed to start service monitor")
		}
	}

	return nil
}

// Stop stops the agent and all service monitors.
func (a *Agent) Stop() error {
	a.logger.Info().Msg("Stopping agent")

	// Stop all watched service monitors first.
	for _, entry := range a.services {
		if entry.Monitor == nil {
			continue
		}
		if err := entry.Monitor.Stop(); err != nil {
			a.logger.Error().Err(err).Msg("Failed to stop service monitor")
		}
	}

	// Stop managed components in reverse start order (LIFO).
	for i := len(a.components) - 1; i >= 0; i-- {
		if err := a.components[i].Stop(); err != nil {
			a.logger.Error().Err(err).Msg("Failed to stop component")
		}
	}

	// Stop continuous profiler (RFD 072).
	if a.continuousProfiler != nil {
		a.continuousProfiler.Stop()
	}

	// Stop continuous memory profiler (RFD 077).
	if a.continuousMemoryProfiler != nil {
		a.continuousMemoryProfiler.Stop()
	}

	a.cancel()
	return nil
}

// SetContinuousProfiler sets the continuous CPU profiler (RFD 072).
func (a *Agent) SetContinuousProfiler(profiler cpuProfiler) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.continuousProfiler = profiler
}

// SetContinuousMemoryProfiler sets the continuous memory profiler (RFD 077).
func (a *Agent) SetContinuousMemoryProfiler(profiler memProfiler) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.continuousMemoryProfiler = profiler
}

// onProcessDiscovered is called when a service's PID is discovered by a monitor.
// It adds the service to continuous profiling if enabled (RFD 072).
func (a *Agent) onProcessDiscovered(serviceName string, pid int32, binaryPath string) {
	a.mu.RLock()
	profiler := a.continuousProfiler
	a.mu.RUnlock()

	if profiler == nil {
		return
	}

	a.logger.Info().
		Str("service", serviceName).
		Int32("pid", pid).
		Str("binary", binaryPath).
		Msg("Adding service to continuous CPU profiling")

	profiler.AddService(serviceName, int(pid), binaryPath)
}

// onSDKDiscovered is called when a service's SDK capabilities are set (RFD 077).
// It adds the service to continuous memory profiling if enabled.
func (a *Agent) onSDKDiscovered(serviceName string, pid int32, sdkAddr string) {
	a.mu.RLock()
	memProfiler := a.continuousMemoryProfiler
	a.mu.RUnlock()

	if memProfiler == nil {
		return
	}

	a.logger.Info().
		Str("service", serviceName).
		Int32("pid", pid).
		Str("sdk_addr", sdkAddr).
		Msg("Adding service to continuous memory profiling")

	memProfiler.AddService(serviceName, int(pid), fmt.Sprintf("/proc/%d/exe", pid), sdkAddr)
}

// onBeylaServiceObserved is called the first time Beyla's OTLP ingest
// reports a given port within this agent's session (RFD 103). It creates or
// updates the port's ServiceEntry and, unless the port was already
// authoritatively connected, derives a name via the ServiceNameAdaptor chain
// (RFD 104).
func (a *Agent) onBeylaServiceObserved(port int32, pid int32, observedName string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Beyla OTLP feedback only ever reports processes with a listening
	// socket, so this path always uses a port-keyed entry.
	key := serviceKey(port, "")
	entry, exists := a.services[key]
	if !exists {
		entry = &ServiceEntry{Port: port}
		a.services[key] = entry
	}
	entry.PID = pid

	binaryPath := ""
	if pid > 0 {
		if path, err := proc.GetBinaryPath(int(pid)); err == nil {
			binaryPath = path
			entry.BinaryPath = path
		}
	}

	if entry.NamingSource != NamingSourceAuthoritative {
		if name, ok := a.nameAdaptor.Resolve(port, pid, binaryPath); ok {
			entry.AutoName = name
		} else if entry.AutoName == "" {
			entry.AutoName = observedName
		}
		entry.NamingSource = NamingSourceAuto
	}

	a.logger.Debug().
		Int32("port", port).
		Int32("pid", pid).
		Str("name", entry.Name()).
		Str("naming_source", string(entry.NamingSource)).
		Msg("Observed service via Beyla OTLP feedback")
}

// GetContext returns the agent's context.
func (a *Agent) GetContext() context.Context {
	return a.ctx
}

// GetDebugManager returns the debug session manager (RFD 072).
func (a *Agent) GetDebugManager() *debug.SessionManager {
	return a.debugManager
}

// GetStatus returns the aggregated agent status, based only on Tier 1
// (watched) services — auto-observed services have no health signal.
func (a *Agent) GetStatus() AgentStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()

	healthyCount := 0
	unhealthyCount := 0
	unknownCount := 0
	totalServices := 0

	for _, entry := range a.services {
		if entry.Monitor == nil {
			continue
		}
		totalServices++
		switch entry.Monitor.GetStatus().Status {
		case ServiceStatusHealthy:
			healthyCount++
		case ServiceStatusUnhealthy:
			unhealthyCount++
		case ServiceStatusUnknown:
			unknownCount++
		}
	}

	// Agent status logic:
	// - Healthy: All services are healthy
	// - Degraded: Some services are healthy, some are unhealthy or unknown
	// - Unhealthy: All services are unhealthy or unknown

	if healthyCount == totalServices {
		return AgentStatusHealthy
	}

	if unhealthyCount == totalServices || (unhealthyCount+unknownCount) == totalServices {
		return AgentStatusUnhealthy
	}

	return AgentStatusDegraded
}

// GetServiceStatuses returns the status of all watched (Tier 1) services.
func (a *Agent) GetServiceStatuses() map[string]ServiceStatusInfo {
	a.mu.RLock()
	defer a.mu.RUnlock()

	statuses := make(map[string]ServiceStatusInfo)

	for _, entry := range a.services {
		if entry.Monitor == nil {
			continue
		}
		statuses[entry.Name()] = entry.Monitor.GetStatus()
	}

	return statuses
}

// GetServiceCount returns the number of services the agent knows about,
// including auto-observed (Tier 0) services (RFD 104).
func (a *Agent) GetServiceCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.services)
}

// connectServiceLocked creates or promotes the ServiceEntry for service to
// authoritative naming, starting a ServiceMonitor when a health endpoint is
// given (RFD 104). If startMonitor is true, the monitor is started
// immediately (dynamic runtime connect); otherwise it is left for the
// caller to start later (initial construction in New, before Agent.Start).
// Caller must hold a.mu.
func (a *Agent) connectServiceLocked(service *meshv1.ServiceInfo, startMonitor bool) error {
	key := serviceKey(service.Port, service.Name)
	entry, exists := a.services[key]
	if exists && entry.NamingSource == NamingSourceAuthoritative {
		return status.Errorf(codes.AlreadyExists, "service %s already connected", service.Name)
	}
	if !exists {
		entry = &ServiceEntry{Port: service.Port}
		a.services[key] = entry
	}

	entry.AuthoritativeName = service.Name
	entry.NamingSource = NamingSourceAuthoritative
	entry.ExePattern = service.ExePattern

	// A monitor is started for any explicitly connected service that has a
	// health endpoint (network health check) or an exe_pattern (RFD 111:
	// process-liveness check, since a portless service can't be network
	// checked). A bare port with neither stays Tier 0 (no active check).
	if service.HealthEndpoint == "" && service.ExePattern == "" {
		return nil
	}

	monitor := NewServiceMonitor(a.ctx, service, a.functionCache, a.logger)
	// Set callbacks for continuous profiling (RFD 072, RFD 077); these only
	// fire once a service reaches Tier 1 (has a monitor).
	monitor.onProcessDiscovered = a.onProcessDiscovered
	monitor.onSDKDiscovered = a.onSDKDiscovered
	if startMonitor {
		if err := monitor.Start(); err != nil {
			return fmt.Errorf("failed to start monitor for service %s: %w", service.Name, err)
		}
	}

	entry.Monitor = monitor
	entry.Tier = TierWatched
	return nil
}

// ConnectService dynamically adds a new service to monitor.
func (a *Agent) ConnectService(service *meshv1.ServiceInfo) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := a.connectServiceLocked(service, true); err != nil {
		return err
	}

	a.logger.Info().
		Str("service", service.Name).
		Int32("port", service.Port).
		Msg("Service connected")

	// Update Beyla discovery with the new service (RFD 053/102).
	if a.beylaManager != nil {
		candidates := a.collectDiscoveryCandidatesLocked()
		if err := a.beylaManager.SetStaticCandidates(candidates); err != nil {
			a.logger.Error().
				Err(err).
				Msg("Failed to update Beyla discovery after service connect")
			// Don't fail the connect operation if Beyla update fails
		}
	}

	return nil
}

// DisconnectService removes a service from monitoring.
func (a *Agent) DisconnectService(serviceName string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	entry, exists := a.findByNameLocked(serviceName)
	if !exists {
		return fmt.Errorf("service %s not found", serviceName)
	}

	// Stop monitoring.
	if entry.Monitor != nil {
		if err := entry.Monitor.Stop(); err != nil {
			a.logger.Error().Err(err).Str("service", serviceName).Msg("Failed to stop service monitor")
		}
	}
	delete(a.services, serviceKey(entry.Port, entry.Name()))

	if a.continuousProfiler != nil {
		a.continuousProfiler.RemoveService(serviceName)
	}
	if a.continuousMemoryProfiler != nil {
		a.continuousMemoryProfiler.RemoveService(serviceName)
	}

	a.logger.Info().
		Str("service", serviceName).
		Msg("Service disconnected")

	// Update Beyla discovery with the remaining services (RFD 053/102).
	if a.beylaManager != nil {
		candidates := a.collectDiscoveryCandidatesLocked()
		if err := a.beylaManager.SetStaticCandidates(candidates); err != nil {
			a.logger.Error().
				Err(err).
				Msg("Failed to update Beyla discovery after service disconnect")
			// Don't fail the disconnect operation if Beyla update fails
		}
	}

	return nil
}

// GetEbpfManager returns the eBPF manager for this agent.
func (a *Agent) GetEbpfManager() *ebpf.Manager {
	return a.ebpfManager
}

// GetBeylaManager returns the Beyla manager for this agent (RFD 032).
func (a *Agent) GetBeylaManager() *beyla.Manager {
	return a.beylaManager
}

// findByNameLocked returns the ServiceEntry whose current name (authoritative
// if set, else auto-derived) matches name. Caller must hold a.mu.
func (a *Agent) findByNameLocked(name string) (*ServiceEntry, bool) {
	for _, entry := range a.services {
		if entry.Name() == name {
			return entry, true
		}
	}
	return nil, false
}

// collectDiscoveryCandidatesLocked builds one discovery.ProcessCandidate per
// authoritatively connected service, for Manager.SetStaticCandidates
// (RFD 102). Auto-observed (Tier 0) services are excluded; they are already
// found by Beyla's own discovery providers. Caller must hold a.mu lock.
func (a *Agent) collectDiscoveryCandidatesLocked() []discovery.ProcessCandidate {
	candidates := make([]discovery.ProcessCandidate, 0, len(a.services))
	for _, entry := range a.services {
		if entry.NamingSource != NamingSourceAuthoritative {
			continue
		}
		if entry.ExePattern != "" {
			candidates = append(candidates, discovery.ProcessCandidate{
				Name:           entry.AuthoritativeName,
				IsClientOnly:   true,
				ExePathPattern: entry.ExePattern,
			})
			continue
		}
		candidates = append(candidates, discovery.ProcessCandidate{
			Ports:        []int{int(entry.Port)},
			Name:         entry.AuthoritativeName,
			IsClientOnly: false,
		})
	}
	return candidates
}

// serviceKey returns Agent.services' map key for a service. RFD 104 keys
// the unified service map by port; RFD 111 extends it to portless
// (exe_pattern) services, which have no port and so key by name instead —
// a separate keyspace, so multiple exe_pattern connects never collide with
// each other or with a port-based entry.
func serviceKey(port int32, name string) string {
	if port != 0 {
		return fmt.Sprintf("port:%d", port)
	}
	return "exe:" + name
}

// Resolve resolves service name to address (ServiceResolver interface).
func (a *Agent) Resolve(serviceName string) (string, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	entry, ok := a.findByNameLocked(serviceName)
	if !ok {
		return "", fmt.Errorf("service not found: %s", serviceName)
	}

	// TODO: Support remote pods (Node Agent mode)
	// For now, assume sidecar mode (localhost)
	return fmt.Sprintf("localhost:%d", entry.Port), nil
}

// ResolveSDK resolves service name to SDK debug address (ServiceResolver interface).
func (a *Agent) ResolveSDK(serviceName string) (string, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	entry, ok := a.findByNameLocked(serviceName)
	if !ok || entry.Monitor == nil {
		return "", fmt.Errorf("service not found: %s", serviceName)
	}

	caps := entry.Monitor.GetSdkCapabilities()
	if caps == nil || caps.SdkAddr == "" {
		return "", fmt.Errorf("SDK capabilities not available for service %s", serviceName)
	}

	return caps.SdkAddr, nil
}

// routeEventsToCorrelation is the eBPF event subscriber that feeds UprobeEvents
// to the correlation engine (RFD 091). It is called from GetEvents on the
// eBPF manager whenever new events are returned.
func (a *Agent) routeEventsToCorrelation(ebpfEvents []*meshv1.EbpfEvent) {
	if a.correlationEngine == nil {
		return
	}
	for _, e := range ebpfEvents {
		ue, ok := e.Payload.(*meshv1.EbpfEvent_UprobeEvent)
		if !ok || ue.UprobeEvent == nil {
			continue
		}
		_ = a.correlationEngine.OnEvent(ue.UprobeEvent)
		// Actions from synchronous strategies are discarded here; callers that
		// need to act on them (e.g., goroutine snapshots) should use the
		// DeployCorrelation handler which registers a dedicated callback.
	}
}

// GetCorrelationEngine returns the agent's correlation engine (RFD 091).
func (a *Agent) GetCorrelationEngine() *correlation.Engine {
	return a.correlationEngine
}

// StartDebugSession starts a debug session for a service.
func (a *Agent) StartDebugSession(sessionID, serviceName, functionName string) error {
	if a.debugManager == nil {
		return fmt.Errorf("debug manager not initialized")
	}
	return a.debugManager.StartSession(sessionID, serviceName, functionName)
}

// StopDebugSession stops a debug session.
func (a *Agent) StopDebugSession(sessionID string) error {
	if a.debugManager == nil {
		return fmt.Errorf("debug manager not initialized")
	}
	return a.debugManager.CloseSession(sessionID)
}
