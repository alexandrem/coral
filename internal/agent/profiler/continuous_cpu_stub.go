//go:build !linux

package profiler

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/internal/agent/debug"
)

// Config holds configuration for continuous CPU profiling.
type Config struct {
	Enabled           bool
	FrequencyHz       int
	Interval          time.Duration
	SampleRetention   time.Duration
	MetadataRetention time.Duration
}

// ServiceInfo contains information about a profiled service.
type ServiceInfo struct {
	ServiceID  string
	PID        int
	BinaryPath string
}

// ContinuousCPUProfiler stub for non-Linux platforms.
type ContinuousCPUProfiler struct{}

// NewContinuousCPUProfiler is not supported on non-Linux platforms.
func NewContinuousCPUProfiler(
	db *sql.DB,
	sessionManager *debug.SessionManager,
	logger zerolog.Logger,
	config Config,
) (*ContinuousCPUProfiler, error) {
	return nil, fmt.Errorf("continuous CPU profiling not supported on this platform")
}

// Start is a no-op on non-Linux platforms.
func (p *ContinuousCPUProfiler) Start(services []ServiceInfo) {}

// Stop is a no-op on non-Linux platforms.
func (p *ContinuousCPUProfiler) Stop() {}

// GetStorage is a no-op on non-Linux platforms.
func (p *ContinuousCPUProfiler) GetStorage() interface{} {
	return nil
}
