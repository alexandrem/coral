package agent

import (
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
)

func TestServiceMonitor_SetSdkCapabilities(t *testing.T) {
	logger := zerolog.Nop()
	service := &meshv1.ServiceInfo{
		Name: "test-service",
		Port: 8080,
	}

	monitor := NewServiceMonitor(service, logger)

	// Initial state
	status := monitor.GetStatus()
	assert.Equal(t, int32(0), status.ProcessID)
	assert.Equal(t, "", status.BinaryPath)

	// Update capabilities with process info
	caps := &agentv1.ServiceSdkCapabilities{
		SdkVersion:      "1.0.0",
		HasDwarfSymbols: true,
		ProcessId:       "12345",
		BinaryPath:      "/usr/local/bin/app",
		BinaryHash:      "sha256:abcdef",
	}

	monitor.SetSdkCapabilities(caps)

	// Verify updates
	status = monitor.GetStatus()
	assert.Equal(t, int32(12345), status.ProcessID)
	assert.Equal(t, "/usr/local/bin/app", status.BinaryPath)
	assert.Equal(t, "sha256:abcdef", status.BinaryHash)

	// Verify stored capabilities
	storedCaps := monitor.GetSdkCapabilities()
	assert.Equal(t, caps, storedCaps)
}

func TestServiceMonitor_GetStatus(t *testing.T) {
	logger := zerolog.Nop()
	service := &meshv1.ServiceInfo{
		Name: "test-service",
		Port: 8080,
	}

	monitor := NewServiceMonitor(service, logger)

	// Set some state
	monitor.mu.Lock()
	monitor.status = ServiceStatusHealthy
	monitor.lastCheck = time.Now()
	monitor.processID = 999
	monitor.binaryPath = "/bin/test"
	monitor.mu.Unlock()

	status := monitor.GetStatus()

	assert.Equal(t, ServiceStatusHealthy, status.Status)
	assert.Equal(t, int32(999), status.ProcessID)
	assert.Equal(t, "/bin/test", status.BinaryPath)
	assert.False(t, status.LastCheck.IsZero())
}
