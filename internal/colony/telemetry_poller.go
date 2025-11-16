package colony

import (
	"context"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"

	agentv1 "github.com/coral-io/coral/coral/agent/v1"
	"github.com/coral-io/coral/internal/colony/database"
	"github.com/coral-io/coral/internal/colony/registry"
)

// TelemetryPoller periodically queries agents for telemetry data.
// This implements the pull-based telemetry architecture from RFD 025.
type TelemetryPoller struct {
	registry       *registry.Registry
	db             *database.Database
	pollInterval   time.Duration
	retentionHours int // How long to keep telemetry summaries (default: 24 hours)
	logger         zerolog.Logger
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	running        bool
	mu             sync.Mutex
}

// NewTelemetryPoller creates a new telemetry poller.
func NewTelemetryPoller(
	registry *registry.Registry,
	db *database.Database,
	pollInterval time.Duration,
	retentionHours int,
	logger zerolog.Logger,
) *TelemetryPoller {
	ctx, cancel := context.WithCancel(context.Background())

	// Default to 24 hours if not specified.
	if retentionHours <= 0 {
		retentionHours = 24
	}

	return &TelemetryPoller{
		registry:       registry,
		db:             db,
		pollInterval:   pollInterval,
		retentionHours: retentionHours,
		logger:         logger.With().Str("component", "telemetry_poller").Logger(),
		ctx:            ctx,
		cancel:         cancel,
	}
}

// Start begins the telemetry polling loop.
func (p *TelemetryPoller) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return nil
	}

	p.logger.Info().
		Dur("poll_interval", p.pollInterval).
		Msg("Starting telemetry poller")

	p.wg.Add(1)
	go p.pollLoop()

	p.running = true
	return nil
}

// Stop stops the telemetry polling loop.
func (p *TelemetryPoller) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return nil
	}

	p.logger.Info().Msg("Stopping telemetry poller")

	p.cancel()
	p.wg.Wait()

	p.running = false
	return nil
}

// pollLoop is the main polling loop.
func (p *TelemetryPoller) pollLoop() {
	defer p.wg.Done()

	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	// Start cleanup loop in a separate goroutine.
	// Cleanup runs every hour and removes summaries older than 24 hours (RFD 025).
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		cleanupTicker := time.NewTicker(1 * time.Hour)
		defer cleanupTicker.Stop()

		for {
			select {
			case <-p.ctx.Done():
				return
			case <-cleanupTicker.C:
				p.runCleanup()
			}
		}
	}()

	// Run an initial poll immediately.
	p.pollOnce()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.pollOnce()
		}
	}
}

// pollOnce performs a single polling cycle.
func (p *TelemetryPoller) pollOnce() {
	// Get all registered agents.
	agents := p.registry.ListAll()

	if len(agents) == 0 {
		p.logger.Debug().Msg("No agents registered, skipping poll")
		return
	}

	// Calculate time range for this poll cycle.
	// Query from last poll interval to now.
	now := time.Now()
	startTime := now.Add(-p.pollInterval)

	p.logger.Debug().
		Int("agent_count", len(agents)).
		Time("start_time", startTime).
		Time("end_time", now).
		Msg("Polling agents for telemetry")

	// Create aggregator for this polling cycle.
	aggregator := NewTelemetryAggregator()

	// Query each agent.
	successCount := 0
	errorCount := 0
	totalSpans := 0

	for _, agent := range agents {
		// Only query healthy or degraded agents.
		status := registry.DetermineStatus(agent.LastSeen, now)
		if status == registry.StatusUnhealthy {
			continue
		}

		spans, err := p.queryAgent(agent, startTime, now)
		if err != nil {
			p.logger.Warn().
				Err(err).
				Str("agent_id", agent.AgentID).
				Str("mesh_ip", agent.MeshIPv4).
				Msg("Failed to query agent for telemetry")
			errorCount++
			continue
		}

		// Add spans to aggregator.
		aggregator.AddSpans(agent.AgentID, spans)
		successCount++
		totalSpans += len(spans)
	}

	// Get aggregated summaries.
	summaries := aggregator.GetSummaries()

	// Store summaries in database.
	if len(summaries) > 0 {
		if err := p.db.InsertTelemetrySummaries(p.ctx, summaries); err != nil {
			p.logger.Error().
				Err(err).
				Int("summary_count", len(summaries)).
				Msg("Failed to store telemetry summaries")
		} else {
			p.logger.Info().
				Int("agents_queried", successCount).
				Int("agents_failed", errorCount).
				Int("total_spans", totalSpans).
				Int("summaries", len(summaries)).
				Msg("Telemetry poll completed")
		}
	} else {
		p.logger.Debug().
			Int("agents_queried", successCount).
			Msg("Telemetry poll completed with no data")
	}
}

// queryAgent queries a single agent for telemetry spans.
func (p *TelemetryPoller) queryAgent(
	agent *registry.Entry,
	startTime, endTime time.Time,
) ([]*agentv1.TelemetrySpan, error) {
	// Create gRPC client for this agent.
	client := GetAgentClient(agent)

	// Create query request.
	req := connect.NewRequest(&agentv1.QueryTelemetryRequest{
		StartTime:    startTime.Unix(),
		EndTime:      endTime.Unix(),
		ServiceNames: nil, // Query all services.
	})

	// Set timeout for the request.
	ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
	defer cancel()

	// Call agent's QueryTelemetry RPC.
	resp, err := client.QueryTelemetry(ctx, req)
	if err != nil {
		return nil, err
	}

	return resp.Msg.Spans, nil
}

// runCleanup performs telemetry database cleanup.
// Removes summaries older than configured retention period.
func (p *TelemetryPoller) runCleanup() {
	deleted, err := p.db.CleanupOldTelemetry(p.ctx, p.retentionHours)
	if err != nil {
		p.logger.Error().
			Err(err).
			Msg("Failed to cleanup old telemetry summaries")
		return
	}

	if deleted > 0 {
		p.logger.Info().
			Int64("deleted_count", deleted).
			Int("retention_hours", p.retentionHours).
			Msg("Cleaned up old telemetry summaries")
	} else {
		p.logger.Debug().Msg("No old telemetry summaries to clean up")
	}
}
