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
	return p.BasePoller.Start(p)
}

// Stop stops the telemetry polling loop.
func (p *TelemetryPoller) Stop() error {
	return p.BasePoller.Stop()
}

// PollOnce performs a single polling cycle.
// Implements the poller.Poller interface.
func (p *TelemetryPoller) PollOnce(ctx context.Context) error {
	// Calculate time range for this poll cycle.
	now := time.Now()
	startTime := now.Add(-p.pollInterval)

	// Create aggregator for this polling cycle.
	aggregator := NewTelemetryAggregator()
	totalSpans := 0

	successCount, errorCount := poller.ForEachHealthyAgent(p.registry, p.logger, func(agent *registry.Entry) error {
		spans, err := p.queryAgent(ctx, agent, startTime, now)
		if err != nil {
			return err
		}

		aggregator.AddSpans(agent.AgentID, spans)
		totalSpans += len(spans)
		return nil
	})

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
