package agent

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/sys/proc"
)

// ServiceStatus represents the health status of a service.
type ServiceStatus string

const (
	ServiceStatusHealthy   ServiceStatus = "healthy"
	ServiceStatusUnhealthy ServiceStatus = "unhealthy"
	ServiceStatusUnknown   ServiceStatus = "unknown"
)

// ServiceStatusInfo contains status information for a service.
type ServiceStatusInfo struct {
	Status     ServiceStatus
	LastCheck  time.Time
	Error      string
	ProcessID  int32
	BinaryPath string
	BinaryHash string
}

// ServiceMonitor monitors a single service's health.
type ServiceMonitor struct {
	service         *meshv1.ServiceInfo
	sdkCapabilities *agentv1.ServiceSdkCapabilities // RFD 060
	status          ServiceStatus
	lastCheck       time.Time
	lastError       error
	processID       int32
	binaryPath      string
	binaryHash      string
	checkInterval   time.Duration
	checkTimeout    time.Duration
	functionCache   *FunctionCache // RFD 063: Function discovery cache
	logger          zerolog.Logger
	mu              sync.RWMutex
	ctx             context.Context
	cancel          context.CancelFunc
}

// NewServiceMonitor creates a new service monitor.
func NewServiceMonitor(service *meshv1.ServiceInfo, functionCache *FunctionCache, logger zerolog.Logger) *ServiceMonitor {
	ctx, cancel := context.WithCancel(context.Background())

	return &ServiceMonitor{
		service:       service,
		status:        ServiceStatusUnknown,
		checkInterval: 10 * time.Second,
		checkTimeout:  2 * time.Second,
		functionCache: functionCache,
		logger:        logger.With().Str("service", service.Name).Logger(),
		ctx:           ctx,
		cancel:        cancel,
	}
}

// Start begins monitoring the service.
func (m *ServiceMonitor) Start() {
	m.logger.Info().
		Int32("port", m.service.Port).
		Str("health_endpoint", m.service.HealthEndpoint).
		Str("type", m.service.ServiceType).
		Msg("Starting service monitor")

	go m.monitorLoop()
}

// Stop stops monitoring the service.
func (m *ServiceMonitor) Stop() {
	m.logger.Info().Msg("Stopping service monitor")
	m.cancel()
}

// GetStatus returns the current service status.
func (m *ServiceMonitor) GetStatus() ServiceStatusInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var errorMsg string
	if m.lastError != nil {
		errorMsg = m.lastError.Error()
	}

	return ServiceStatusInfo{
		Status:     m.status,
		LastCheck:  m.lastCheck,
		Error:      errorMsg,
		ProcessID:  m.processID,
		BinaryPath: m.binaryPath,
		BinaryHash: m.binaryHash,
	}
}

// monitorLoop runs the health check loop.
func (m *ServiceMonitor) monitorLoop() {
	// Add random initial delay (up to 30% of check interval) to prevent thundering
	// herd when multiple services start simultaneously.
	maxJitter := int64(m.checkInterval) * 30 / 100
	initialDelay := time.Duration(rand.Int64N(maxJitter))

	m.logger.Debug().
		Dur("initial_delay", initialDelay).
		Msg("Waiting before first health check to prevent thundering herd")

	select {
	case <-m.ctx.Done():
		return
	case <-time.After(initialDelay):
		// Continue to first check.
	}

	// Perform initial check after jitter delay.
	m.performHealthCheck()

	ticker := time.NewTicker(m.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.performHealthCheck()
			// Periodically attempt to discover process info if not already known or if it might have changed
			// (e.g. service restart). We do this less frequently than health checks to save resources?
			// For now, doing it on same interval is fine as it's lightweight enough.
			m.discoverProcessInfo()
		}
	}
}

// discoverProcessInfo attempts to find the PID and binary path for the service.
func (m *ServiceMonitor) discoverProcessInfo() {
	// If we have SDK capabilities, trust them first (unless we want to verify).
	// RFD says SDK integration is the source for SDK services.
	m.mu.RLock()
	hasSDK := m.sdkCapabilities != nil && m.sdkCapabilities.ProcessId != ""
	m.mu.RUnlock()

	if hasSDK {
		return
	}

	// Only supported on Linux for now
	if runtime.GOOS != "linux" {
		return
	}

	// findPidByPort finds the PID of the process listening on the given port.
	pid, err := proc.FindPidByPort(int(m.service.Port))
	if err != nil {
		// Don't log error on every check to avoid spam, unless debug logging is on
		m.logger.Debug().Err(err).Msg("Failed to discover process ID")
		return
	}

	if pid == 0 {
		return
	}

	// If PID changed or was unknown, update it
	m.mu.Lock()
	pidChanged := m.processID != pid
	if pidChanged {
		m.processID = pid
		m.logger.Info().Int32("pid", pid).Msg("Discovered service process")

		// Also try to get binary path
		if path, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid)); err == nil {
			m.binaryPath = path
			m.logger.Info().Str("path", path).Msg("Discovered binary path")

			// Trigger function discovery (RFD 063).
			// This happens once when service is first discovered, or when process restarts.
			if m.functionCache != nil {
				serviceName := m.service.Name
				binaryPath := path

				m.logger.Info().
					Str("service", serviceName).
					Str("binary", binaryPath).
					Msg("Triggering function discovery for newly discovered service")

				// Trigger async discovery (don't block the monitor loop).
				go func() {
					if err := m.functionCache.DiscoverAndCache(context.Background(), serviceName, binaryPath); err != nil {
						m.logger.Error().
							Err(err).
							Str("service", serviceName).
							Msg("Failed to discover and cache functions")
					} else {
						m.logger.Info().
							Str("service", serviceName).
							Msg("Function discovery completed successfully")
					}
				}()
			}
		}
	}
	m.mu.Unlock()
}

// performHealthCheck executes a health check for the service.
func (m *ServiceMonitor) performHealthCheck() {
	ctx, cancel := context.WithTimeout(m.ctx, m.checkTimeout)
	defer cancel()

	var err error
	var newStatus ServiceStatus

	// Determine check type based on service configuration.
	if m.service.HealthEndpoint != "" {
		// HTTP health check.
		err = m.checkHTTPHealth(ctx)
	} else {
		// TCP port check (basic connectivity).
		err = m.checkTCPHealth(ctx)
	}

	if err != nil {
		newStatus = ServiceStatusUnhealthy
		m.logger.Warn().Err(err).Msg("Health check failed")
	} else {
		newStatus = ServiceStatusHealthy
		m.logger.Debug().Msg("Health check passed")
	}

	m.mu.Lock()
	m.status = newStatus
	m.lastCheck = time.Now()
	m.lastError = err
	m.mu.Unlock()
}

// checkHTTPHealth performs an HTTP health check.
func (m *ServiceMonitor) checkHTTPHealth(ctx context.Context) error {
	url := fmt.Sprintf("http://localhost:%d%s", m.service.Port, m.service.HealthEndpoint)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	client := &http.Client{
		Timeout: m.checkTimeout,
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() // TODO: errcheck

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unhealthy status code: %d", resp.StatusCode)
	}

	return nil
}

// checkTCPHealth performs a TCP connectivity check.
func (m *ServiceMonitor) checkTCPHealth(ctx context.Context) error {
	address := fmt.Sprintf("localhost:%d", m.service.Port)

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("tcp connection failed: %w", err)
	}
	defer func() { _ = conn.Close() }() // TODO: errcheck

	return nil
}

// SetSdkCapabilities updates the SDK capabilities for the service.
func (m *ServiceMonitor) SetSdkCapabilities(caps *agentv1.ServiceSdkCapabilities) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sdkCapabilities = caps
	m.logger.Info().
		Str("sdk_version", caps.SdkVersion).
		Bool("has_dwarf", caps.HasDwarfSymbols).
		Msg("Updated SDK capabilities")

	// Update process info from SDK capabilities
	if caps.ProcessId != "" {
		// Parse PID string to int32
		var pid int32
		fmt.Sscanf(caps.ProcessId, "%d", &pid) // nolint:errcheck
		m.processID = pid
	}
	if caps.BinaryPath != "" {
		m.binaryPath = caps.BinaryPath
	}
	if caps.BinaryHash != "" {
		m.binaryHash = caps.BinaryHash
	}
}

// GetSdkCapabilities returns the SDK capabilities for the service.
func (m *ServiceMonitor) GetSdkCapabilities() *agentv1.ServiceSdkCapabilities {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sdkCapabilities
}
