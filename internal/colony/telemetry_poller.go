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

const telemetryDataType = "telemetry"

// TelemetryPoller periodically queries agents for telemetry data.
// This implements the pull-based telemetry architecture from RFD 025.
// Uses sequence-based checkpoints for reliable polling (RFD 089).
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
	// Create aggregator for this polling cycle.
	aggregator := NewTelemetryAggregator()
	totalSpans := 0

	successCount, errorCount := poller.ForEachHealthyAgent(p.registry, p.logger, func(agent *registry.Entry) error {
		spans, err := p.pollAgent(ctx, agent)
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

// pollAgent queries a single agent using checkpoint-based polling (RFD 089).
func (p *TelemetryPoller) pollAgent(ctx context.Context, agent *registry.Entry) ([]*agentv1.TelemetrySpan, error) {
	// Get checkpoint for this agent.
	checkpoint, err := p.db.GetPollingCheckpoint(ctx, agent.AgentID, telemetryDataType)
	if err != nil {
		p.logger.Warn().Err(err).Str("agent", agent.AgentID).Msg("Failed to get checkpoint, polling from beginning")
	}

	var startSeqID uint64
	var storedSessionID string
	if checkpoint != nil {
		startSeqID = checkpoint.LastSeqID
		storedSessionID = checkpoint.SessionID
	}

	// Query agent with sequence-based request.
	client := GetAgentClient(agent)
	req := connect.NewRequest(&agentv1.QueryTelemetryRequest{
		StartSeqId:   startSeqID,
		MaxRecords:   10000,
		ServiceNames: nil, // Query all services.
	})

	queryCtx, cancel := context.WithTimeout(ctx, agentQueryTimeout)
	defer cancel()

	resp, err := client.QueryTelemetry(queryCtx, req)
	if err != nil {
		return nil, err
	}

	// Handle session_id mismatch (agent database was recreated).
	if storedSessionID != "" && resp.Msg.SessionId != "" && storedSessionID != resp.Msg.SessionId {
		p.logger.Warn().
			Str("agent", agent.AgentID).
			Str("stored_session", storedSessionID).
			Str("agent_session", resp.Msg.SessionId).
			Msg("Agent session changed (database recreated), resetting checkpoint")

		if err := p.db.ResetPollingCheckpoint(ctx, agent.AgentID, telemetryDataType); err != nil {
			p.logger.Error().Err(err).Str("agent", agent.AgentID).Msg("Failed to reset checkpoint")
		}

		// Re-query from the beginning with the new session.
		req.Msg.StartSeqId = 0
		queryCtx2, cancel2 := context.WithTimeout(ctx, agentQueryTimeout)
		defer cancel2()

		resp, err = client.QueryTelemetry(queryCtx2, req)
		if err != nil {
			return nil, err
		}
	}

	// Detect gaps in sequence IDs (RFD 089).
	if len(resp.Msg.Spans) > 0 {
		seqIDs := make([]uint64, len(resp.Msg.Spans))
		timestamps := make([]int64, len(resp.Msg.Spans))
		for i, span := range resp.Msg.Spans {
			seqIDs[i] = span.SeqId
			timestamps[i] = span.Timestamp
		}
		for _, gap := range poller.DetectGaps(startSeqID, seqIDs, timestamps) {
			p.logger.Warn().
				Str("agent", agent.AgentID).
				Uint64("gap_start", gap.StartSeqID).
				Uint64("gap_end", gap.EndSeqID).
				Msg("Detected telemetry sequence gap")
			_ = p.db.RecordSequenceGap(ctx, agent.AgentID, telemetryDataType, gap.StartSeqID, gap.EndSeqID)
		}
	}

	// Update checkpoint if we got data.
	if resp.Msg.MaxSeqId > 0 {
		if err := p.db.UpdatePollingCheckpoint(ctx, agent.AgentID, telemetryDataType, resp.Msg.SessionId, resp.Msg.MaxSeqId); err != nil {
			p.logger.Error().Err(err).Str("agent", agent.AgentID).Msg("Failed to update checkpoint")
		}
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
