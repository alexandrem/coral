package colony

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/poller"
	"github.com/coral-mesh/coral/internal/colony/registry"
)

// TelemetryPoller periodically queries agents for telemetry data.
// This implements the pull-based telemetry architecture from RFD 025.
type TelemetryPoller struct {
	*poller.BasePoller
	registry       *registry.Registry
	db             *database.Database
	pollInterval   time.Duration
	retentionHours int // How long to keep telemetry summaries (default: 24 hours)
	logger         zerolog.Logger
}

// NewTelemetryPoller creates a new telemetry poller.
func NewTelemetryPoller(
	ctx context.Context,
	registry *registry.Registry,
	db *database.Database,
	pollInterval time.Duration,
	retentionHours int,
	logger zerolog.Logger,
) *TelemetryPoller {
	// Default to 24 hours if not specified.
	if retentionHours <= 0 {
		retentionHours = 24
	}

	componentLogger := logger.With().Str("component", "telemetry_poller").Logger()

	base := poller.NewBasePoller(ctx, poller.Config{
		Name:         "telemetry_poller",
		PollInterval: pollInterval,
		Logger:       componentLogger,
	})

	return &TelemetryPoller{
		BasePoller:     base,
		registry:       registry,
		db:             db,
		pollInterval:   pollInterval,
		retentionHours: retentionHours,
		logger:         componentLogger,
	}
}

// Start begins the telemetry polling loop.
func (p *TelemetryPoller) Start() error {
	p.logger.Info().
		Dur("poll_interval", p.pollInterval).
		Msg("Starting telemetry poller")

	return p.BasePoller.Start(p)
}

// Stop stops the telemetry polling loop.
func (p *TelemetryPoller) Stop() error {
	return p.BasePoller.Stop()
}

// PollOnce performs a single polling cycle.
// Implements the poller.Poller interface.
func (p *TelemetryPoller) PollOnce(ctx context.Context) error {
	// Get all registered agents.
	agents := p.registry.ListAll()

	if len(agents) == 0 {
		p.logger.Debug().Msg("No agents registered, skipping poll")
		return nil
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

		spans, err := p.queryAgent(ctx, agent, startTime, now)
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
		if err := p.db.InsertTelemetrySummaries(ctx, summaries); err != nil {
			p.logger.Error().
				Err(err).
				Int("summary_count", len(summaries)).
				Msg("Failed to store telemetry summaries")
			return err
		}

		p.logger.Info().
			Int("agents_queried", successCount).
			Int("agents_failed", errorCount).
			Int("total_spans", totalSpans).
			Int("summaries", len(summaries)).
			Msg("Telemetry poll completed")
	} else {
		p.logger.Debug().
			Int("agents_queried", successCount).
			Msg("Telemetry poll completed with no data")
	}

	return nil
}

// queryAgent queries a single agent for telemetry spans.
func (p *TelemetryPoller) queryAgent(
	ctx context.Context,
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
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Call agent's QueryTelemetry RPC.
	resp, err := client.QueryTelemetry(queryCtx, req)
	if err != nil {
		return nil, err
	}

	return resp.Msg.Spans, nil
}

// RunCleanup performs telemetry database cleanup.
// Removes summaries older than configured retention period.
// Implements the poller.Poller interface.
func (p *TelemetryPoller) RunCleanup(ctx context.Context) error {
	deleted, err := p.db.CleanupOldTelemetry(ctx, p.retentionHours)
	if err != nil {
		p.logger.Error().
			Err(err).
			Msg("Failed to cleanup old telemetry summaries")
		return err
	}

	if deleted > 0 {
		p.logger.Info().
			Int64("deleted_count", deleted).
			Int("retention_hours", p.retentionHours).
			Msg("Cleaned up old telemetry summaries")
	} else {
		p.logger.Debug().Msg("No old telemetry summaries to clean up")
	}

	return nil
}
