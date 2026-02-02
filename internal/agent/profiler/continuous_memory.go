// Package profiler implements continuous profiling for the agent.
package profiler

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/internal/agent/debug"
)

// MemoryConfig holds configuration for continuous memory profiling (RFD 077).
type MemoryConfig struct {
	Enabled         bool          // Master switch.
	Interval        time.Duration // Collection interval (default: 60s).
	SampleRetention time.Duration // Sample retention (default: 1 hour).
}

// MemoryServiceInfo contains information about a profiled service for memory profiling.
type MemoryServiceInfo struct {
	ServiceID  string
	PID        int
	BinaryPath string
	SDKAddr    string // SDK debug server address (e.g., "localhost:9002").
}

// ContinuousMemoryProfiler continuously profiles memory allocation at low frequency (RFD 077).
type ContinuousMemoryProfiler struct {
	storage          *Storage
	logger           zerolog.Logger
	config           MemoryConfig
	ctx              context.Context
	cancel           context.CancelFunc
	activeServices   map[string]MemoryServiceInfo
	activeServicesMu sync.Mutex
}

// NewContinuousMemoryProfiler creates a new continuous memory profiler (RFD 077).
func NewContinuousMemoryProfiler(
	parentCtx context.Context,
	db *sql.DB,
	logger zerolog.Logger,
	config MemoryConfig,
) (*ContinuousMemoryProfiler, error) {
	if !config.Enabled {
		return nil, fmt.Errorf("continuous memory profiling is disabled")
	}

	// Set defaults.
	if config.Interval == 0 {
		config.Interval = 60 * time.Second
	}
	if config.SampleRetention == 0 {
		config.SampleRetention = 1 * time.Hour
	}

	storage, err := NewStorage(db, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage: %w", err)
	}

	if parentCtx == nil {
		return nil, fmt.Errorf("context is required")
	}

	ctx, cancel := context.WithCancel(parentCtx)

	profiler := &ContinuousMemoryProfiler{
		storage:        storage,
		logger:         logger.With().Str("component", "continuous_memory_profiler").Logger(),
		config:         config,
		ctx:            ctx,
		cancel:         cancel,
		activeServices: make(map[string]MemoryServiceInfo),
	}

	return profiler, nil
}

// Start starts the continuous memory profiling loop.
func (p *ContinuousMemoryProfiler) Start(services []MemoryServiceInfo) {
	p.logger.Info().
		Dur("interval", p.config.Interval).
		Int("service_count", len(services)).
		Msg("Starting continuous memory profiling")

	p.activeServicesMu.Lock()
	for _, service := range services {
		p.activeServices[service.ServiceID] = service
		go p.profileServiceLoop(service)
	}
	p.activeServicesMu.Unlock()
}

// Stop stops the continuous memory profiling loop.
func (p *ContinuousMemoryProfiler) Stop() {
	p.logger.Info().Msg("Stopping continuous memory profiling")
	p.cancel()
}

// AddService adds a service to be memory-profiled continuously (RFD 077).
func (p *ContinuousMemoryProfiler) AddService(serviceID string, pid int, binaryPath string, sdkAddr string) {
	p.activeServicesMu.Lock()
	if _, exists := p.activeServices[serviceID]; exists {
		p.activeServicesMu.Unlock()
		return
	}
	service := MemoryServiceInfo{
		ServiceID:  serviceID,
		PID:        pid,
		BinaryPath: binaryPath,
		SDKAddr:    sdkAddr,
	}
	p.activeServices[serviceID] = service
	p.activeServicesMu.Unlock()

	go p.profileServiceLoop(service)
}

// GetStorage returns the profiler's storage instance.
func (p *ContinuousMemoryProfiler) GetStorage() interface{} {
	return p.storage
}

// profileServiceLoop collects heap snapshots at regular intervals.
func (p *ContinuousMemoryProfiler) profileServiceLoop(service MemoryServiceInfo) {
	p.logger.Info().
		Str("service_id", service.ServiceID).
		Str("sdk_addr", service.SDKAddr).
		Msg("Starting continuous memory profiling for service")

	ticker := time.NewTicker(p.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			p.logger.Info().
				Str("service_id", service.ServiceID).
				Msg("Stopping continuous memory profiling for service")
			return
		case <-ticker.C:
			if err := p.collectAndStore(service); err != nil {
				p.logger.Error().
					Err(err).
					Str("service_id", service.ServiceID).
					Msg("Failed to collect and store memory profile")
			}
		}
	}
}

// collectAndStore fetches a heap snapshot from the SDK and stores it.
func (p *ContinuousMemoryProfiler) collectAndStore(service MemoryServiceInfo) error {
	startTime := time.Now()

	result, err := debug.CollectHeapSnapshot(service.SDKAddr, p.logger)
	if err != nil {
		return fmt.Errorf("failed to collect heap snapshot: %w", err)
	}

	p.logger.Debug().
		Str("service_id", service.ServiceID).
		Int("sample_count", len(result.Samples)).
		Dur("collect_time", time.Since(startTime)).
		Msg("Collected memory profile snapshot")

	if len(result.Samples) == 0 {
		return nil
	}

	// Extract build ID from the binary.
	buildID, err := ExtractBuildIDFromPID(service.PID)
	if err != nil {
		p.logger.Warn().
			Err(err).
			Str("service_id", service.ServiceID).
			Msg("Failed to extract build ID, using fallback")
		buildID = fmt.Sprintf("unknown_%d_%d", service.PID, time.Now().Unix())
	}

	// Convert and store samples.
	samples := make([]MemoryProfileSample, 0, len(result.Samples))
	for _, sample := range result.Samples {
		frameIDs, err := p.storage.encodeStackFrames(p.ctx, sample.FrameNames)
		if err != nil {
			p.logger.Warn().
				Err(err).
				Str("stack", strings.Join(sample.FrameNames, ";")).
				Msg("Failed to encode stack frames, skipping memory sample")
			continue
		}

		samples = append(samples, MemoryProfileSample{
			Timestamp:     startTime,
			ServiceID:     service.ServiceID,
			BuildID:       buildID,
			StackHash:     computeStackHash(frameIDs),
			StackFrameIDs: frameIDs,
			AllocBytes:    sample.AllocBytes,
			AllocObjects:  sample.AllocObjects,
		})
	}

	if err := p.storage.StoreMemorySamples(p.ctx, samples); err != nil {
		return fmt.Errorf("failed to store memory samples: %w", err)
	}

	p.logger.Debug().
		Str("service_id", service.ServiceID).
		Int("samples_stored", len(samples)).
		Msg("Stored continuous memory profile samples")

	return nil
}
