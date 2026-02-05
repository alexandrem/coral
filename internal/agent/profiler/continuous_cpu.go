//go:build linux

// Package profiler implements continuous CPU profiling for the agent.
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
	"github.com/coral-mesh/coral/internal/safe"
)

// ContinuousCPUProfiler continuously profiles CPU usage at low frequency.
type ContinuousCPUProfiler struct {
	storage          *Storage
	sessionManager   *debug.SessionManager
	logger           zerolog.Logger
	config           Config
	ctx              context.Context
	cancel           context.CancelFunc
	kernelSymbolizer *debug.KernelSymbolizer
	activeServices   map[string]struct{} // Tracks services with active profiling loops.
	activeServicesMu sync.Mutex
}

// Config holds configuration for continuous CPU profiling.
type Config struct {
	Enabled           bool          // Master switch
	FrequencyHz       int           // Sampling frequency (default: 19Hz)
	Interval          time.Duration // Collection interval (default: 15s)
	SampleRetention   time.Duration // Sample retention (default: 1 hour)
	MetadataRetention time.Duration // Binary metadata retention (default: 7 days)
}

// ServiceInfo contains information about a profiled service.
type ServiceInfo struct {
	ServiceID  string
	PID        int
	BinaryPath string
}

// NewContinuousCPUProfiler creates a new continuous CPU profiler.
func NewContinuousCPUProfiler(
	parentCtx context.Context,
	db *sql.DB,
	sessionManager *debug.SessionManager,
	logger zerolog.Logger,
	config Config,
) (*ContinuousCPUProfiler, error) {
	if !config.Enabled {
		return nil, fmt.Errorf("continuous profiling is disabled")
	}

	// Set defaults.
	if config.FrequencyHz == 0 {
		config.FrequencyHz = 19 // Default 19Hz (prime number)
	}
	if config.Interval == 0 {
		config.Interval = 15 * time.Second
	}
	if config.SampleRetention == 0 {
		config.SampleRetention = 1 * time.Hour
	}
	if config.MetadataRetention == 0 {
		config.MetadataRetention = 7 * 24 * time.Hour
	}

	storage, err := NewStorage(db, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage: %w", err)
	}

	// Initialize kernel symbolizer for CPU profiling.
	kernelSymbolizer, err := debug.NewKernelSymbolizer(logger)
	if err != nil {
		logger.Warn().Err(err).Msg("Failed to initialize kernel symbolizer, kernel stacks will show raw addresses")
		kernelSymbolizer = nil
	} else {
		logger.Info().Int("symbol_count", kernelSymbolizer.SymbolCount()).Msg("Kernel symbolizer initialized for continuous profiling")
	}

	if parentCtx == nil {
		return nil, fmt.Errorf("context is required")
	}

	ctx, cancel := context.WithCancel(parentCtx)

	profiler := &ContinuousCPUProfiler{
		storage:          storage,
		sessionManager:   sessionManager,
		logger:           logger.With().Str("component", "continuous_cpu_profiler").Logger(),
		config:           config,
		ctx:              ctx,
		cancel:           cancel,
		kernelSymbolizer: kernelSymbolizer,
		activeServices:   make(map[string]struct{}),
	}

	return profiler, nil
}

// Start starts the continuous profiling loop.
func (p *ContinuousCPUProfiler) Start(services []ServiceInfo) {
	p.logger.Info().
		Int("frequency_hz", p.config.FrequencyHz).
		Dur("interval", p.config.Interval).
		Int("service_count", len(services)).
		Msg("Starting continuous CPU profiling")

	// Start cleanup loop.
	go p.storage.RunCleanupLoop(p.ctx, p.config.SampleRetention, p.config.MetadataRetention)

	// Start profiling loop for each service.
	p.activeServicesMu.Lock()
	for _, service := range services {
		p.activeServices[service.ServiceID] = struct{}{}
		go p.profileServiceLoop(service)
	}
	p.activeServicesMu.Unlock()
}

// Stop stops the continuous profiling loop.
func (p *ContinuousCPUProfiler) Stop() {
	p.logger.Info().Msg("Stopping continuous CPU profiling")
	p.cancel()
}

// profileServiceLoop starts a persistent BPF session and periodically drains
// accumulated samples from the BPF maps. The BPF program runs continuously in
// the kernel, so no samples are lost between collection ticks.
func (p *ContinuousCPUProfiler) profileServiceLoop(service ServiceInfo) {
	p.logger.Info().
		Str("service_id", service.ServiceID).
		Int("pid", service.PID).
		Msg("Starting continuous profiling for service")

	// Start a persistent BPF session. The duration is irrelevant here since we
	// never call CollectProfile (which sleeps); we drain maps manually.
	session, err := debug.StartCPUProfile(
		service.PID,
		0, // Duration unused — we drain maps on our own schedule.
		p.config.FrequencyHz,
		p.kernelSymbolizer,
		p.logger,
	)
	if err != nil {
		p.logger.Error().
			Err(err).
			Str("service_id", service.ServiceID).
			Int("pid", service.PID).
			Msg("Failed to start persistent CPU profile session")
		return
	}
	defer func() { _ = session.Close() }()

	p.logger.Info().
		Str("service_id", service.ServiceID).
		Int("pid", service.PID).
		Int("frequency_hz", p.config.FrequencyHz).
		Msg("Persistent BPF session started, draining maps on interval")

	ticker := time.NewTicker(p.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			p.logger.Info().
				Str("service_id", service.ServiceID).
				Msg("Stopping continuous profiling for service")
			return
		case <-ticker.C:
			if err := p.drainAndStore(session, service); err != nil {
				p.logger.Error().
					Err(err).
					Str("service_id", service.ServiceID).
					Int("pid", service.PID).
					Msg("Failed to drain and store profile samples")
			}
		}
	}
}

// drainAndStore reads accumulated samples from the persistent BPF session's maps,
// clears them, and stores the results. This is non-blocking — the BPF program
// continues to collect samples in the background.
func (p *ContinuousCPUProfiler) drainAndStore(session *debug.CPUProfileSession, service ServiceInfo) error {
	startTime := time.Now()

	// Read and clear BPF maps (non-blocking — no sleep).
	result, err := session.DrainStackCounts()
	if err != nil {
		return fmt.Errorf("failed to drain stack counts: %w", err)
	}

	p.logger.Debug().
		Str("service_id", service.ServiceID).
		Uint64("total_samples", result.TotalSamples).
		Int("unique_stacks", len(result.Samples)).
		Dur("drain_time", time.Since(startTime)).
		Msg("Drained CPU profile samples")

	if result.TotalSamples == 0 {
		return nil
	}

	// Extract build ID from the binary.
	buildID, err := ExtractBuildIDFromPID(service.PID)
	if err != nil {
		p.logger.Warn().
			Err(err).
			Str("service_id", service.ServiceID).
			Int("pid", service.PID).
			Msg("Failed to extract build ID, using fallback")
		buildID = fmt.Sprintf("unknown_%d_%d", service.PID, time.Now().Unix())
	}

	// Update binary metadata.
	now := time.Now()
	metadata := BinaryMetadata{
		BuildID:      buildID,
		ServiceID:    service.ServiceID,
		BinaryPath:   service.BinaryPath,
		FirstSeen:    now,
		LastSeen:     now,
		HasDebugInfo: session.Symbolizer != nil,
	}

	if err := p.storage.UpsertBinaryMetadata(p.ctx, metadata); err != nil {
		p.logger.Warn().Err(err).Msg("Failed to update binary metadata")
	}

	// Convert and store samples.
	samples := make([]ProfileSample, 0, len(result.Samples))
	for i, sample := range result.Samples {
		if i < 3 {
			p.logger.Debug().
				Str("service_id", service.ServiceID).
				Int("frame_count", len(sample.FrameNames)).
				Uint64("count", sample.Count).
				Msg("Processing profile sample")
		}
		// Encode stack frames to integer IDs.
		frameIDs, err := p.storage.encodeStackFrames(p.ctx, sample.FrameNames)
		if err != nil {
			p.logger.Warn().
				Err(err).
				Str("stack", strings.Join(sample.FrameNames, ";")).
				Msg("Failed to encode stack frames, skipping sample")
			continue
		}

		// Safe conversion from uint64 (eBPF) to uint32 (storage) with overflow detection.
		sampleCount, clamped := safe.Uint64ToUint32(sample.Count)
		if clamped {
			p.logger.Warn().
				Uint64("original_count", sample.Count).
				Uint32("clamped_count", sampleCount).
				Str("service_id", service.ServiceID).
				Msg("Sample count exceeded uint32 max, clamped to MaxUint32")
		}

		samples = append(samples, ProfileSample{
			Timestamp:     startTime,
			ServiceID:     service.ServiceID,
			BuildID:       buildID,
			StackHash:     computeStackHash(frameIDs),
			StackFrameIDs: frameIDs,
			SampleCount:   sampleCount,
		})
	}

	// Store all samples in a single transaction.
	if err := p.storage.StoreSamples(p.ctx, samples); err != nil {
		return fmt.Errorf("failed to store samples: %w", err)
	}

	p.logger.Debug().
		Str("service_id", service.ServiceID).
		Str("build_id", buildID).
		Int("samples_stored", len(samples)).
		Msg("Stored continuous CPU profile samples")

	return nil
}

// AddService adds a service to be profiled continuously.
// It is safe to call multiple times for the same service; duplicates are ignored.
func (p *ContinuousCPUProfiler) AddService(serviceID string, pid int, binaryPath string) {
	p.activeServicesMu.Lock()
	if _, exists := p.activeServices[serviceID]; exists {
		p.activeServicesMu.Unlock()
		p.logger.Debug().
			Str("service_id", serviceID).
			Int("pid", pid).
			Msg("Service already being profiled, skipping")
		return
	}
	p.activeServices[serviceID] = struct{}{}
	p.activeServicesMu.Unlock()

	service := ServiceInfo{
		ServiceID:  serviceID,
		PID:        pid,
		BinaryPath: binaryPath,
	}
	go p.profileServiceLoop(service)
}

// RemoveService removes a service from continuous profiling.
// Note: This is a placeholder - actual implementation would need service tracking.
func (p *ContinuousCPUProfiler) RemoveService(serviceID string) {
	p.logger.Info().Str("service_id", serviceID).Msg("Service removal requested (not yet implemented)")
	// TODO: Implement service tracking and removal.
}

// GetStorage returns the profiler's storage instance.
func (p *ContinuousCPUProfiler) GetStorage() interface{} {
	return p.storage
}

// encodeStackFrames and computeStackHash are defined in storage.go (platform-independent).
