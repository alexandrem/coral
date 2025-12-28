//go:build linux

// Package profiler implements continuous CPU profiling for the agent.
package profiler

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/internal/agent/debug"
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

	ctx, cancel := context.WithCancel(context.Background())

	profiler := &ContinuousCPUProfiler{
		storage:          storage,
		sessionManager:   sessionManager,
		logger:           logger.With().Str("component", "continuous_cpu_profiler").Logger(),
		config:           config,
		ctx:              ctx,
		cancel:           cancel,
		kernelSymbolizer: kernelSymbolizer,
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
	for _, service := range services {
		go p.profileServiceLoop(service)
	}
}

// Stop stops the continuous profiling loop.
func (p *ContinuousCPUProfiler) Stop() {
	p.logger.Info().Msg("Stopping continuous CPU profiling")
	p.cancel()
}

// profileServiceLoop continuously profiles a single service.
func (p *ContinuousCPUProfiler) profileServiceLoop(service ServiceInfo) {
	ticker := time.NewTicker(p.config.Interval)
	defer ticker.Stop()

	p.logger.Info().
		Str("service_id", service.ServiceID).
		Int("pid", service.PID).
		Msg("Starting continuous profiling for service")

	for {
		select {
		case <-p.ctx.Done():
			p.logger.Info().
				Str("service_id", service.ServiceID).
				Msg("Stopping continuous profiling for service")
			return
		case <-ticker.C:
			if err := p.collectAndStore(service); err != nil {
				p.logger.Error().
					Err(err).
					Str("service_id", service.ServiceID).
					Int("pid", service.PID).
					Msg("Failed to collect and store profile sample")
			}
		}
	}
}

// collectAndStore collects a single profile sample and stores it.
func (p *ContinuousCPUProfiler) collectAndStore(service ServiceInfo) error {
	startTime := time.Now()

	// Calculate duration in seconds from interval.
	durationSeconds := int(p.config.Interval.Seconds())
	if durationSeconds < 1 {
		durationSeconds = 1
	}

	// Start CPU profiling session (reuses RFD 070 infrastructure).
	session, err := debug.StartCPUProfile(
		service.PID,
		durationSeconds,
		p.config.FrequencyHz,
		p.kernelSymbolizer,
		p.logger,
	)
	if err != nil {
		return fmt.Errorf("failed to start CPU profile: %w", err)
	}
	defer func() { _ = session.Close() }()

	// Collect profile (blocks for the duration).
	result, err := session.CollectProfile()
	if err != nil {
		return fmt.Errorf("failed to collect CPU profile: %w", err)
	}

	p.logger.Debug().
		Str("service_id", service.ServiceID).
		Uint64("total_samples", result.TotalSamples).
		Int("unique_stacks", len(result.Samples)).
		Dur("collection_time", time.Since(startTime)).
		Msg("Collected CPU profile sample")

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
	for _, sample := range result.Samples {
		// Encode stack frames to integer IDs.
		frameIDs, err := p.storage.encodeStackFrames(p.ctx, sample.FrameNames)
		if err != nil {
			p.logger.Warn().
				Err(err).
				Str("stack", strings.Join(sample.FrameNames, ";")).
				Msg("Failed to encode stack frames, skipping sample")
			continue
		}

		samples = append(samples, ProfileSample{
			Timestamp:     startTime,
			ServiceID:     service.ServiceID,
			BuildID:       buildID,
			StackHash:     computeStackHash(frameIDs),
			StackFrameIDs: frameIDs,
			SampleCount:   int(sample.Count),
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
		Msg("Stored continuous CPU profile sample")

	return nil
}

// AddService adds a service to be profiled continuously.
func (p *ContinuousCPUProfiler) AddService(service ServiceInfo) {
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
