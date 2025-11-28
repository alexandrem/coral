// Package agent implements the coral agent that runs on each node.
package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/agent/beyla"
	"github.com/coral-mesh/coral/internal/agent/ebpf"
	"github.com/rs/zerolog"
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
	id           string
	monitors     map[string]*ServiceMonitor
	ebpfManager  *ebpf.Manager
	beylaManager *beyla.Manager
	logger       zerolog.Logger
	mu           sync.RWMutex
	ctx          context.Context
	cancel       context.CancelFunc
}

// Config contains agent configuration.
type Config struct {
	AgentID     string
	Services    []*meshv1.ServiceInfo
	BeylaConfig *beyla.Config
	Logger      zerolog.Logger
}

// New creates a new agent.
func New(config Config) (*Agent, error) {
	if config.AgentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}

	ctx, cancel := context.WithCancel(context.Background())

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
		id:           config.AgentID,
		monitors:     make(map[string]*ServiceMonitor),
		ebpfManager:  ebpfManager,
		beylaManager: beylaManager,
		logger:       config.Logger.With().Str("agent_id", config.AgentID).Logger(),
		ctx:          ctx,
		cancel:       cancel,
	}

	// Create monitors for each service (if any provided).
	for _, service := range config.Services {
		monitor := NewServiceMonitor(service, config.Logger)
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

	a.cancel()
	return nil
}

// GetStatus returns the aggregated agent status.
func (a *Agent) GetStatus() AgentStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()

	healthyCount := 0
	unhealthyCount := 0
	unknownCount := 0

	for _, monitor := range a.monitors {
		status, _, _ := monitor.GetStatus()
		switch status {
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
		status, lastCheck, lastError := monitor.GetStatus()

		var errorMsg string
		if lastError != nil {
			errorMsg = lastError.Error()
		}

		statuses[name] = ServiceStatusInfo{
			Status:    status,
			LastCheck: lastCheck,
			Error:     errorMsg,
		}
	}

	return statuses
}

// ServiceStatusInfo contains status information for a service.
type ServiceStatusInfo struct {
	Status    ServiceStatus
	LastCheck time.Time
	Error     string
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
	monitor := NewServiceMonitor(service, a.logger)
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
