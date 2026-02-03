// Package agent implements the coral agent that runs on each node.
package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/rs/zerolog"

	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/agent/beyla"
	"github.com/coral-mesh/coral/internal/agent/debug"
	"github.com/coral-mesh/coral/internal/agent/ebpf"
	"github.com/coral-mesh/coral/internal/config"
)

// AgentStatus represents the overall agent health status.
type AgentStatus string

const (
	AgentStatusHealthy   AgentStatus = "healthy"
	AgentStatusDegraded  AgentStatus = "degraded"
	AgentStatusUnhealthy AgentStatus = "unhealthy"
)

// Agent represents a Coral agent that monitors multiple services.
type Agent struct {
	id                       string
	monitors                 map[string]*ServiceMonitor
	ebpfManager              *ebpf.Manager
	beylaManager             *beyla.Manager
	debugManager             *debug.SessionManager
	continuousProfiler       interface{}    // RFD 072: Continuous CPU profiler (uses interface to support Linux/non-Linux builds).
	continuousMemoryProfiler interface{}    // RFD 077: Continuous memory profiler.
	functionCache            *FunctionCache // RFD 063: Function discovery cache
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

	// Initialize Beyla manager (RFD 032).
	var beylaManager *beyla.Manager
	if config.BeylaConfig != nil {
		var err error
		beylaManager, err = beyla.NewManager(ctx, config.BeylaConfig, config.Logger)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("failed to create Beyla manager: %w", err)
		}
	}

	agent := &Agent{
		id:            config.AgentID,
		monitors:      make(map[string]*ServiceMonitor),
		ebpfManager:   ebpfManager,
		beylaManager:  beylaManager,
		functionCache: config.FunctionCache,
		logger:        config.Logger.With().Str("agent_id", config.AgentID).Logger(),
		ctx:           ctx,
		cancel:        cancel,
	}

	// Initialize SessionManager (RFD 061).
	agent.debugManager = debug.NewSessionManager(config.DebugConfig, config.Logger, agent)

	// Create monitors for each service (if any provided).
	for _, service := range config.Services {
		monitor := NewServiceMonitor(ctx, service, config.FunctionCache, config.Logger)
		// Set callbacks for continuous profiling (RFD 072, RFD 077).
		monitor.onProcessDiscovered = agent.onProcessDiscovered
		monitor.onSDKDiscovered = agent.onSDKDiscovered
		agent.monitors[service.Name] = monitor
	}

	return agent, nil
}

// Start begins monitoring all services.
func (a *Agent) Start() error {
	a.logger.Info().
		Int("service_count", len(a.monitors)).
		Msg("Starting agent")

	// Start Beyla manager (RFD 032).
	if a.beylaManager != nil {
		if err := a.beylaManager.Start(); err != nil {
			a.logger.Error().Err(err).Msg("Failed to start Beyla manager")
			// Continue even if Beyla fails - it's supplementary to core monitoring
		} else {
			a.logger.Info().Msg("Beyla manager started successfully")
		}
	}

	// Start all service monitors.
	for name, monitor := range a.monitors {
		a.logger.Debug().Str("service", name).Msg("Starting service monitor")
		monitor.Start()
	}

	return nil
}

// Stop stops the agent and all service monitors.
func (a *Agent) Stop() error {
	a.logger.Info().Msg("Stopping agent")

	// Stop all service monitors.
	for _, monitor := range a.monitors {
		monitor.Stop()
	}

	// Stop Beyla manager (RFD 032).
	if a.beylaManager != nil {
		if err := a.beylaManager.Stop(); err != nil {
			a.logger.Error().Err(err).Msg("Failed to stop Beyla manager")
		}
	}

	// Stop eBPF manager.
	if a.ebpfManager != nil {
		if err := a.ebpfManager.Stop(); err != nil {
			a.logger.Error().Err(err).Msg("Failed to stop eBPF manager")
		}
	}

	// Stop continuous profiler (RFD 072).
	if a.continuousProfiler != nil {
		if profiler, ok := a.continuousProfiler.(interface{ Stop() }); ok {
			profiler.Stop()
		}
	}

	// Stop continuous memory profiler (RFD 077).
	if a.continuousMemoryProfiler != nil {
		if profiler, ok := a.continuousMemoryProfiler.(interface{ Stop() }); ok {
			profiler.Stop()
		}
	}

	a.cancel()
	return nil
}

// SetContinuousProfiler sets the continuous CPU profiler (RFD 072).
func (a *Agent) SetContinuousProfiler(profiler interface{}) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.continuousProfiler = profiler
}

// SetContinuousMemoryProfiler sets the continuous memory profiler (RFD 077).
func (a *Agent) SetContinuousMemoryProfiler(profiler interface{}) {
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

	// Type assert to get the AddService method.
	type profilerWithAddService interface {
		AddService(serviceID string, pid int, binaryPath string)
	}

	if p, ok := profiler.(profilerWithAddService); ok {
		a.logger.Info().
			Str("service", serviceName).
			Int32("pid", pid).
			Str("binary", binaryPath).
			Msg("Adding service to continuous CPU profiling")

		p.AddService(serviceName, int(pid), binaryPath)
	}
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

	// Type assert to get the AddService method.
	type memProfilerWithAddService interface {
		AddService(serviceID string, pid int, binaryPath string, sdkAddr string)
	}

	if p, ok := memProfiler.(memProfilerWithAddService); ok {
		a.logger.Info().
			Str("service", serviceName).
			Int32("pid", pid).
			Str("sdk_addr", sdkAddr).
			Msg("Adding service to continuous memory profiling")

		p.AddService(serviceName, int(pid), fmt.Sprintf("/proc/%d/exe", pid), sdkAddr)
	}
}

// GetContext returns the agent's context.
func (a *Agent) GetContext() context.Context {
	return a.ctx
}

// GetDebugManager returns the debug session manager (RFD 072).
func (a *Agent) GetDebugManager() *debug.SessionManager {
	return a.debugManager
}

// GetStatus returns the aggregated agent status.
func (a *Agent) GetStatus() AgentStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()

	healthyCount := 0
	unhealthyCount := 0
	unknownCount := 0

	for _, monitor := range a.monitors {
		statusInfo := monitor.GetStatus()
		switch statusInfo.Status {
		case ServiceStatusHealthy:
			healthyCount++
		case ServiceStatusUnhealthy:
			unhealthyCount++
		case ServiceStatusUnknown:
			unknownCount++
		}
	}

	totalServices := len(a.monitors)

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

// GetServiceStatuses returns the status of all monitored services.
func (a *Agent) GetServiceStatuses() map[string]ServiceStatusInfo {
	a.mu.RLock()
	defer a.mu.RUnlock()

	statuses := make(map[string]ServiceStatusInfo)

	for name, monitor := range a.monitors {
		statuses[name] = monitor.GetStatus()
	}

	return statuses
}

// GetServiceCount returns the number of services being monitored.
func (a *Agent) GetServiceCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.monitors)
}

// ConnectService dynamically adds a new service to monitor.
func (a *Agent) ConnectService(service *meshv1.ServiceInfo) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Check if service already exists.
	if _, exists := a.monitors[service.Name]; exists {
		return fmt.Errorf("service %s already connected", service.Name)
	}

	// Create and start new monitor.
	monitor := NewServiceMonitor(a.ctx, service, a.functionCache, a.logger)
	// Set callbacks for continuous profiling (RFD 072, RFD 077).
	monitor.onProcessDiscovered = a.onProcessDiscovered
	monitor.onSDKDiscovered = a.onSDKDiscovered
	monitor.Start()

	a.monitors[service.Name] = monitor

	a.logger.Info().
		Str("service", service.Name).
		Int32("port", service.Port).
		Msg("Service connected")

	// Update Beyla discovery with new port (RFD 053).
	if a.beylaManager != nil {
		ports := a.collectPortsLocked()
		if err := a.beylaManager.UpdateDiscovery(ports); err != nil {
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

	monitor, exists := a.monitors[serviceName]
	if !exists {
		return fmt.Errorf("service %s not found", serviceName)
	}

	// Stop monitoring.
	monitor.Stop()
	delete(a.monitors, serviceName)

	a.logger.Info().
		Str("service", serviceName).
		Msg("Service disconnected")

	// Update Beyla discovery with remaining ports (RFD 053).
	if a.beylaManager != nil {
		ports := a.collectPortsLocked()
		if err := a.beylaManager.UpdateDiscovery(ports); err != nil {
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

// collectPortsLocked collects all service ports from monitors (RFD 053).
// Caller must hold a.mu lock.
func (a *Agent) collectPortsLocked() []int {
	ports := make([]int, 0, len(a.monitors))
	for _, monitor := range a.monitors {
		ports = append(ports, int(monitor.service.Port))
	}
	return ports
}

// Resolve resolves service name to address (ServiceResolver interface).
func (a *Agent) Resolve(serviceName string) (string, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	monitor, ok := a.monitors[serviceName]
	if !ok {
		return "", fmt.Errorf("service not found: %s", serviceName)
	}

	// TODO: Support remote pods (Node Agent mode)
	// For now, assume sidecar mode (localhost)
	return fmt.Sprintf("localhost:%d", monitor.service.Port), nil
}

// ResolveSDK resolves service name to SDK debug address (ServiceResolver interface).
func (a *Agent) ResolveSDK(serviceName string) (string, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	monitor, ok := a.monitors[serviceName]
	if !ok {
		return "", fmt.Errorf("service not found: %s", serviceName)
	}

	caps := monitor.GetSdkCapabilities()
	if caps == nil || caps.SdkAddr == "" {
		return "", fmt.Errorf("SDK capabilities not available for service %s", serviceName)
	}

	return caps.SdkAddr, nil
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
