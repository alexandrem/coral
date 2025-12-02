package ebpf

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
)

// Manager handles eBPF collector lifecycle.
type Manager struct {
	logger     zerolog.Logger
	collectors map[string]*runningCollector
	mu         sync.RWMutex
	caps       *agentv1.EbpfCapabilities
}

// runningCollector tracks a single active collector instance.
type runningCollector struct {
	id          string
	kind        agentv1.EbpfCollectorKind
	collector   Collector
	ctx         context.Context
	cancel      context.CancelFunc
	expiresAt   time.Time
	serviceName string
	expired     bool // Set to true when expired, but events still available
}

// Config contains manager configuration.
type Config struct {
	Logger zerolog.Logger
}

// NewManager creates a new eBPF manager.
func NewManager(config Config) *Manager {
	caps := detectCapabilities()

	m := &Manager{
		logger:     config.Logger.With().Str("component", "ebpf_manager").Logger(),
		collectors: make(map[string]*runningCollector),
		caps:       caps,
	}

	// Start background janitor to clean up expired collectors.
	// NOTE: this will be stopped when process ends only. We should improve this.
	go m.janitor()

	return m
}

// GetCapabilities returns the eBPF capabilities of this system.
func (m *Manager) GetCapabilities() *agentv1.EbpfCapabilities {
	return m.caps
}

// StartCollector starts a new eBPF collector.
func (m *Manager) StartCollector(ctx context.Context, req *meshv1.StartEbpfCollectorRequest) (*meshv1.StartEbpfCollectorResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if eBPF is supported.
	if !m.caps.Supported {
		return &meshv1.StartEbpfCollectorResponse{
			Supported: false,
			Error:     "eBPF not supported on this system",
		}, nil
	}

	// Check if collector kind is supported.
	if !m.isCollectorSupported(req.Kind) {
		return &meshv1.StartEbpfCollectorResponse{
			Supported: false,
			Error:     fmt.Sprintf("collector kind %v not supported", req.Kind),
		}, nil
	}

	// Generate collector ID.
	collectorID := uuid.New().String()

	// Create collector based on kind.
	collector, err := m.createCollector(req.Kind, req.Config)
	if err != nil {
		return &meshv1.StartEbpfCollectorResponse{
			Supported: true,
			Error:     fmt.Sprintf("failed to create collector: %v", err),
		}, nil
	}

	// Calculate expiration time.
	expiresAt := time.Now().Add(5 * time.Minute) // default 5 minutes
	if req.Duration != nil {
		expiresAt = time.Now().Add(req.Duration.AsDuration())
	}

	// Create collector context.
	collectorCtx, cancel := context.WithDeadline(context.Background(), expiresAt)

	// Start collector.
	if err := collector.Start(collectorCtx); err != nil {
		cancel()
		return &meshv1.StartEbpfCollectorResponse{
			Supported: true,
			Error:     fmt.Sprintf("failed to start collector: %v", err),
		}, nil
	}

	// Track running collector.
	running := &runningCollector{
		id:          collectorID,
		kind:        req.Kind,
		collector:   collector,
		ctx:         collectorCtx,
		cancel:      cancel,
		expiresAt:   expiresAt,
		serviceName: req.ServiceName,
	}
	m.collectors[collectorID] = running

	m.logger.Info().
		Str("collector_id", collectorID).
		Int("total_collectors", len(m.collectors)).
		Msg("Added collector to tracking map")

	// Auto-stop on expiration.
	go m.autoStop(running)

	m.logger.Info().
		Str("collector_id", collectorID).
		Str("kind", req.Kind.String()).
		Str("service", req.ServiceName).
		Time("expires_at", expiresAt).
		Int("active_collectors", len(m.collectors)).
		Msg("Started eBPF collector")

	return &meshv1.StartEbpfCollectorResponse{
		CollectorId: collectorID,
		ExpiresAt:   timestamppb.New(expiresAt),
		Supported:   true,
	}, nil
}

// StopCollector stops a running collector.
func (m *Manager) StopCollector(collectorID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	running, ok := m.collectors[collectorID]
	if !ok {
		return fmt.Errorf("collector not found: %s", collectorID)
	}

	// Stop collector.
	running.cancel()
	if err := running.collector.Stop(); err != nil {
		m.logger.Error().Err(err).Str("collector_id", collectorID).Msg("Error stopping collector")
	}

	// Remove from tracking.
	delete(m.collectors, collectorID)

	m.logger.Info().Str("collector_id", collectorID).Msg("Stopped eBPF collector")

	return nil
}

// GetEvents retrieves events from a running collector.
func (m *Manager) GetEvents(collectorID string) ([]*meshv1.EbpfEvent, error) {
	m.mu.RLock()
	running, ok := m.collectors[collectorID]
	totalCollectors := len(m.collectors)

	// Log all collector IDs for debugging
	var collectorIDs []string
	for id := range m.collectors {
		collectorIDs = append(collectorIDs, id)
	}
	m.mu.RUnlock()

	m.logger.Debug().
		Str("requested_collector_id", collectorID).
		Int("total_collectors", totalCollectors).
		Strs("active_collector_ids", collectorIDs).
		Bool("found", ok).
		Msg("Looking up collector for GetEvents")

	if !ok {
		m.logger.Error().
			Str("collector_id", collectorID).
			Strs("available_collectors", collectorIDs).
			Msg("Collector not found in tracking map")
		return nil, fmt.Errorf("collector not found: %s", collectorID)
	}

	return running.collector.GetEvents()
}

// Stop stops all running collectors and shuts down the manager.
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, running := range m.collectors {
		running.cancel()
		if err := running.collector.Stop(); err != nil {
			m.logger.Error().Err(err).Str("collector_id", id).Msg("Error stopping collector")
		}
	}

	m.collectors = make(map[string]*runningCollector)
	return nil
}

// autoStop automatically marks a collector as expired when duration completes.
// The collector stays in memory with events available until explicitly stopped.
func (m *Manager) autoStop(running *runningCollector) {
	<-running.ctx.Done()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if still tracked (might have been manually stopped).
	if collector, ok := m.collectors[running.id]; ok {
		// Mark as expired but DON'T remove from tracking or stop collection.
		// Events remain available for fetching during detach/cleanup.
		collector.expired = true
		m.logger.Info().
			Str("collector_id", running.id).
			Msg("Collector expired - events still available until cleanup")
	}
}

// isCollectorSupported checks if a collector kind is supported.
func (m *Manager) isCollectorSupported(kind agentv1.EbpfCollectorKind) bool {
	for _, supported := range m.caps.AvailableCollectors {
		if supported == kind {
			return true
		}
	}
	return false
}

// createCollector creates a collector instance based on kind.
func (m *Manager) createCollector(kind agentv1.EbpfCollectorKind, config map[string]string) (Collector, error) {
	switch kind {
	case agentv1.EbpfCollectorKind_EBPF_COLLECTOR_KIND_UPROBE:
		// Parse uprobe configuration
		functionName, ok := config["function_name"]
		if !ok {
			return nil, fmt.Errorf("function_name is required for uprobe collector")
		}

		sdkAddr, ok := config["sdk_addr"]
		if !ok {
			return nil, fmt.Errorf("sdk_addr is required for uprobe collector")
		}

		serviceName := config["service_name"]

		uprobeConfig := &UprobeConfig{
			ServiceName:  serviceName,
			FunctionName: functionName,
			SDKAddr:      sdkAddr,
		}

		// Parse optional config
		if captureArgs, ok := config["capture_args"]; ok && captureArgs == "true" {
			uprobeConfig.CaptureArgs = true
		}
		if captureReturn, ok := config["capture_return"]; ok && captureReturn == "true" {
			uprobeConfig.CaptureReturn = true
		}
		if maxEvents, ok := config["max_events"]; ok {
			if _, err := fmt.Sscanf(maxEvents, "%d", &uprobeConfig.MaxEvents); err != nil {
				return nil, fmt.Errorf("unable to scan max_events: %w", err)
			}
		}

		return NewUprobeCollector(m.logger, uprobeConfig)

	case agentv1.EbpfCollectorKind_EBPF_COLLECTOR_KIND_SYSCALL_STATS:
		return NewSyscallStatsCollector(m.logger, config), nil
	default:
		return nil, fmt.Errorf("unsupported collector kind: %v", kind)
	}
}

// janitor periodically cleans up expired collectors that haven't been explicitly stopped.
// Gives a 1-hour grace period after expiration for event fetching before cleanup.
func (m *Manager) janitor() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		m.cleanupExpiredCollectors()
	}
}

// cleanupExpiredCollectors removes collectors that have been expired for > 1 hour.
func (m *Manager) cleanupExpiredCollectors() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	gracePeriod := 1 * time.Hour
	var cleaned []string

	for id, collector := range m.collectors {
		if collector.expired && now.After(collector.expiresAt.Add(gracePeriod)) {
			// Expired over 1 hour ago - clean up
			if err := collector.collector.Stop(); err != nil {
				m.logger.Error().Err(err).Str("collector_id", id).Msg("Error stopping expired collector during cleanup")
			}
			delete(m.collectors, id)
			cleaned = append(cleaned, id)
		}
	}

	if len(cleaned) > 0 {
		m.logger.Info().
			Strs("collector_ids", cleaned).
			Msg("Cleaned up expired collectors (grace period exceeded)")
	}
}
