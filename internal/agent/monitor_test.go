package agent

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/constants"
)

func TestServiceMonitor_SetSdkCapabilities(t *testing.T) {
	logger := zerolog.Nop()
	service := &meshv1.ServiceInfo{
		Name: "test-service",
		Port: 8080,
	}

	monitor := NewServiceMonitor(context.Background(), service, nil, logger)

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

	monitor := NewServiceMonitor(context.Background(), service, nil, logger)

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

// TestServiceMonitor_LivenessCheck_UnknownBeforeFirstCheck verifies a
// portless (RFD 111) monitor starts Unknown, matching the network-checked
// monitor's initial state.
func TestServiceMonitor_LivenessCheck_UnknownBeforeFirstCheck(t *testing.T) {
	logger := zerolog.Nop()
	service := &meshv1.ServiceInfo{
		Name:       "worker",
		ExePattern: "python.*consumer.py",
	}

	monitor := NewServiceMonitor(context.Background(), service, nil, logger)

	assert.Equal(t, ServiceStatusUnknown, monitor.GetStatus().Status)
}

// TestServiceMonitor_LivenessCheck_UnhealthyAfterThreshold verifies a
// portless service is only marked unhealthy after
// DefaultMissedLivenessThreshold consecutive misses, not on the first one
// (RFD 111 debounces brief PID-table races).
func TestServiceMonitor_LivenessCheck_UnhealthyAfterThreshold(t *testing.T) {
	logger := zerolog.Nop()
	service := &meshv1.ServiceInfo{
		Name:       "worker",
		ExePattern: "definitely-not-a-real-process-name-xyz123",
	}

	monitor := NewServiceMonitor(context.Background(), service, nil, logger)
	monitor.mu.Lock()
	monitor.status = ServiceStatusHealthy
	monitor.mu.Unlock()

	for i := 1; i < constants.DefaultMissedLivenessThreshold; i++ {
		monitor.performLivenessCheck()
		assert.Equal(t, ServiceStatusHealthy, monitor.GetStatus().Status,
			"status should be held (not flipped to unhealthy) below the miss threshold")
	}

	monitor.performLivenessCheck()
	assert.Equal(t, ServiceStatusUnhealthy, monitor.GetStatus().Status,
		"status should flip to unhealthy once the miss threshold is reached")
}

// TestServiceMonitor_LivenessCheck_RecoversOnMatch matches the running test
// binary via its cmdline (see proc.FindPidByExePattern), verifying a
// portless monitor recovers to healthy and resets its miss count once a
// matching process is found again.
func TestServiceMonitor_LivenessCheck_RecoversOnMatch(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires /proc")
	}

	logger := zerolog.Nop()
	service := &meshv1.ServiceInfo{
		Name:       "self",
		ExePattern: regexp.QuoteMeta(filepath.Base(os.Args[0])),
	}

	monitor := NewServiceMonitor(context.Background(), service, nil, logger)
	monitor.mu.Lock()
	monitor.status = ServiceStatusUnhealthy
	monitor.missedLiveness = constants.DefaultMissedLivenessThreshold
	monitor.mu.Unlock()

	monitor.performLivenessCheck()

	status := monitor.GetStatus()
	assert.Equal(t, ServiceStatusHealthy, status.Status)
	assert.Equal(t, 0, monitor.missedLiveness)
}
