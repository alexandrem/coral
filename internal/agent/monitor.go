package agent

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"sync"
	"time"

	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/rs/zerolog"
)

// ServiceStatus represents the health status of a service.
type ServiceStatus string

const (
	ServiceStatusHealthy   ServiceStatus = "healthy"
	ServiceStatusUnhealthy ServiceStatus = "unhealthy"
	ServiceStatusUnknown   ServiceStatus = "unknown"
)

// ServiceMonitor monitors a single service's health.
type ServiceMonitor struct {
	service       *meshv1.ServiceInfo
	status        ServiceStatus
	lastCheck     time.Time
	lastError     error
	checkInterval time.Duration
	checkTimeout  time.Duration
	logger        zerolog.Logger
	mu            sync.RWMutex
	ctx           context.Context
	cancel        context.CancelFunc
}

// NewServiceMonitor creates a new service monitor.
func NewServiceMonitor(service *meshv1.ServiceInfo, logger zerolog.Logger) *ServiceMonitor {
	ctx, cancel := context.WithCancel(context.Background())

	return &ServiceMonitor{
		service:       service,
		status:        ServiceStatusUnknown,
		checkInterval: 10 * time.Second,
		checkTimeout:  2 * time.Second,
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
func (m *ServiceMonitor) GetStatus() (ServiceStatus, time.Time, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status, m.lastCheck, m.lastError
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
		}
	}
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
