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

const (
	// gapRecoveryInterval is how often to check for pending gaps.
	gapRecoveryInterval = 5 * time.Minute

	// gapRecoveryMaxAttempts is the maximum number of recovery attempts per gap.
	gapRecoveryMaxAttempts = 3

	// gapRetention is how long to keep resolved gaps for auditing.
	gapRetention = 7 * 24 * time.Hour

	// gapMaxSize is maximum size for batch of records to process.
	gapMaxSize = 10000
)

// At the top of your file or inside your struct initializer
var recoveryRegistry = map[string]func(*GapRecoveryService, context.Context, *registry.Entry, *database.SequenceGap, int32) error{
	telemetryDataType:     (*GapRecoveryService).recoverTelemetry,
	systemMetricsDataType: (*GapRecoveryService).recoverSystemMetrics,
	beylaHTTPDataType:     (*GapRecoveryService).recoverBeylaHTTP,
	beylaGRPCDataType:     (*GapRecoveryService).recoverBeylaGRPC,
	beylaSQLDataType:      (*GapRecoveryService).recoverBeylaSQL,
	beylaTracesDataType:   (*GapRecoveryService).recoverBeylaTraces,
	cpuProfileDataType:    (*GapRecoveryService).recoverCPUProfile,
	memoryProfileDataType: (*GapRecoveryService).recoverMemoryProfile,
}

// GapRecoveryService periodically attempts to recover detected sequence gaps
// by re-querying agents for missing data (RFD 089).
type GapRecoveryService struct {
	*poller.BasePoller
	registry *registry.Registry
	db       *database.Database
	logger   zerolog.Logger
}

// NewGapRecoveryService creates a new gap recovery service.
func NewGapRecoveryService(
	ctx context.Context,
	registry *registry.Registry,
	db *database.Database,
	logger zerolog.Logger,
) *GapRecoveryService {
	componentLogger := logger.With().Str("component", "gap_recovery").Logger()

	base := poller.NewBasePoller(ctx, poller.Config{
		Name:         "gap_recovery",
		PollInterval: gapRecoveryInterval,
		Logger:       componentLogger,
	})

	return &GapRecoveryService{
		BasePoller: base,
		registry:   registry,
		db:         db,
		logger:     componentLogger,
	}
}

// Start begins the gap recovery polling loop.
func (s *GapRecoveryService) Start() error {
	return s.BasePoller.Start(s)
}

// Stop stops the gap recovery polling loop.
func (s *GapRecoveryService) Stop() error {
	return s.BasePoller.Stop()
}

// PollOnce processes pending sequence gaps.
// Implements the poller.Poller interface.
func (s *GapRecoveryService) PollOnce(ctx context.Context) error {
	gaps, err := s.db.GetPendingSequenceGaps(ctx, gapRecoveryMaxAttempts)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to get pending sequence gaps")
		return err
	}

	if len(gaps) == 0 {
		return nil
	}

	var recovered, permanent, skipped int

	for i := range gaps {
		gap := &gaps[i]

		// Look up agent in registry.
		agent, err := s.registry.Get(gap.AgentID)
		if err != nil {
			s.logger.Debug().
				Str("agent_id", gap.AgentID).
				Int("gap_id", gap.ID).
				Msg("Agent not found in registry, skipping gap recovery")
			skipped++
			continue
		}

		// Skip unhealthy agents.
		status := registry.DetermineStatus(agent.LastSeen, time.Now())
		if status == registry.StatusUnhealthy {
			s.logger.Debug().
				Str("agent_id", gap.AgentID).
				Int("gap_id", gap.ID).
				Msg("Agent unhealthy, skipping gap recovery")
			skipped++
			continue
		}

		// Increment attempt counter.
		if err := s.db.IncrementGapRecoveryAttempt(ctx, gap.ID); err != nil {
			s.logger.Error().Err(err).Int("gap_id", gap.ID).Msg("Failed to increment recovery attempt")
			continue
		}
		gap.RecoveryAttempts++

		// Attempt recovery.
		recoverErr := s.recoverGap(ctx, agent, gap)

		if recoverErr == nil {
			if err := s.db.MarkGapRecovered(ctx, gap.ID); err != nil {
				s.logger.Error().Err(err).Int("gap_id", gap.ID).Msg("Failed to mark gap as recovered")
			}
			s.logger.Info().
				Str("agent_id", gap.AgentID).
				Str("data_type", gap.DataType).
				Int("gap_id", gap.ID).
				Uint64("start_seq", gap.StartSeqID).
				Uint64("end_seq", gap.EndSeqID).
				Int("attempt", gap.RecoveryAttempts).
				Msg("Gap recovery succeeded")
			recovered++
		} else if gap.RecoveryAttempts >= gapRecoveryMaxAttempts {
			if err := s.db.MarkGapPermanent(ctx, gap.ID); err != nil {
				s.logger.Error().Err(err).Int("gap_id", gap.ID).Msg("Failed to mark gap as permanent")
			}
			s.logger.Error().
				Str("agent_id", gap.AgentID).
				Str("data_type", gap.DataType).
				Int("gap_id", gap.ID).
				Uint64("start_seq", gap.StartSeqID).
				Uint64("end_seq", gap.EndSeqID).
				Uint64("records_lost", gap.EndSeqID-gap.StartSeqID+1).
				Msg("PERMANENT DATA LOSS: gap recovery exhausted all attempts")
			permanent++
		} else {
			s.logger.Warn().
				Err(recoverErr).
				Str("agent_id", gap.AgentID).
				Str("data_type", gap.DataType).
				Int("gap_id", gap.ID).
				Int("attempts", gap.RecoveryAttempts).
				Int("max_attempts", gapRecoveryMaxAttempts).
				Msg("Gap recovery failed, will retry")
		}
	}

	s.logger.Info().
		Int("gaps_processed", len(gaps)).
		Int("recovered", recovered).
		Int("permanent", permanent).
		Int("skipped", skipped).
		Msg("Gap recovery cycle completed")

	return nil
}

// RunCleanup removes old resolved sequence gaps.
// Implements the poller.Poller interface.
func (s *GapRecoveryService) RunCleanup(ctx context.Context) error {
	return s.db.CleanupOldSequenceGaps(ctx, gapRetention)
}

// recoverGap attempts to recover data for a single sequence gap by re-querying the agent.
func (s *GapRecoveryService) recoverGap(ctx context.Context, agent *registry.Entry, gap *database.SequenceGap) error {
	queryCtx, cancel := context.WithTimeout(ctx, agentQueryTimeout)
	defer cancel()

	gapSize := gap.EndSeqID - gap.StartSeqID + 1
	maxRecords := int32(gapMaxSize)

	if gapSize < gapMaxSize {
		maxRecords = int32(gapSize) // #nosec G115 - gapSize fits in int32 safely
	} else {
		s.logger.Warn().
			Uint64("actual_gap", gapSize).
			Msgf("Gap size limited to %d", gapMaxSize)
	}

	// Dispatch via Registry
	if recoverFn, exists := recoveryRegistry[gap.DataType]; exists {
		return recoverFn(s, queryCtx, agent, gap, maxRecords)
	}

	s.logger.Warn().Str("type", gap.DataType).Msg("Unsupported data type")
	return nil
}

// recoverTelemetry recovers missing telemetry spans.
func (s *GapRecoveryService) recoverTelemetry(ctx context.Context, agent *registry.Entry, gap *database.SequenceGap, maxRecords int32) error {
	client := GetAgentClient(agent)
	resp, err := client.QueryTelemetry(ctx, connect.NewRequest(&agentv1.QueryTelemetryRequest{
		StartSeqId: gap.StartSeqID - 1,
		MaxRecords: maxRecords,
	}))
	if err != nil {
		return err
	}

	if len(resp.Msg.Spans) == 0 {
		return nil // Data may have aged out, but no error.
	}

	// Aggregate through TelemetryAggregator and store summaries.
	aggregator := NewTelemetryAggregator()
	aggregator.AddSpans(agent.AgentID, resp.Msg.Spans)
	summaries := aggregator.GetSummaries()

	if len(summaries) > 0 {
		return s.db.InsertTelemetrySummaries(ctx, summaries)
	}
	return nil
}

// recoverSystemMetrics recovers missing system metrics.
func (s *GapRecoveryService) recoverSystemMetrics(ctx context.Context, agent *registry.Entry, gap *database.SequenceGap, maxRecords int32) error {
	client := GetAgentClient(agent)
	resp, err := client.QuerySystemMetrics(ctx, connect.NewRequest(&agentv1.QuerySystemMetricsRequest{
		StartSeqId: gap.StartSeqID - 1,
		MaxRecords: maxRecords,
	}))
	if err != nil {
		return err
	}

	if len(resp.Msg.Metrics) == 0 {
		return nil
	}

	// Bucket by each metric's own timestamp: a gap may span many minutes.
	summaries := aggregateSystemMetrics(agent.AgentID, resp.Msg.Metrics, systemMetricMinute)
	if len(summaries) > 0 {
		return s.db.InsertSystemMetricsSummaries(ctx, summaries)
	}
	return nil
}

// recoverBeylaHTTP recovers missing Beyla HTTP metrics.
func (s *GapRecoveryService) recoverBeylaHTTP(ctx context.Context, agent *registry.Entry, gap *database.SequenceGap, maxRecords int32) error {
	client := GetAgentClient(agent)
	resp, err := client.QueryEbpfMetrics(ctx, connect.NewRequest(&agentv1.QueryEbpfMetricsRequest{
		HttpStartSeqId: gap.StartSeqID - 1,
		MaxRecords:     maxRecords,
	}))
	if err != nil {
		return err
	}

	if len(resp.Msg.HttpMetrics) > 0 {
		return s.db.InsertBeylaHTTPMetrics(ctx, agent.AgentID, resp.Msg.HttpMetrics)
	}
	return nil
}

// recoverBeylaGRPC recovers missing Beyla gRPC metrics.
func (s *GapRecoveryService) recoverBeylaGRPC(ctx context.Context, agent *registry.Entry, gap *database.SequenceGap, maxRecords int32) error {
	client := GetAgentClient(agent)
	resp, err := client.QueryEbpfMetrics(ctx, connect.NewRequest(&agentv1.QueryEbpfMetricsRequest{
		GrpcStartSeqId: gap.StartSeqID - 1,
		MaxRecords:     maxRecords,
	}))
	if err != nil {
		return err
	}

	if len(resp.Msg.GrpcMetrics) > 0 {
		return s.db.InsertBeylaGRPCMetrics(ctx, agent.AgentID, resp.Msg.GrpcMetrics)
	}
	return nil
}

// recoverBeylaSQL recovers missing Beyla SQL metrics.
func (s *GapRecoveryService) recoverBeylaSQL(ctx context.Context, agent *registry.Entry, gap *database.SequenceGap, maxRecords int32) error {
	client := GetAgentClient(agent)
	resp, err := client.QueryEbpfMetrics(ctx, connect.NewRequest(&agentv1.QueryEbpfMetricsRequest{
		SqlStartSeqId: gap.StartSeqID - 1,
		MaxRecords:    maxRecords,
	}))
	if err != nil {
		return err
	}

	if len(resp.Msg.SqlMetrics) > 0 {
		return s.db.InsertBeylaSQLMetrics(ctx, agent.AgentID, resp.Msg.SqlMetrics)
	}
	return nil
}

// recoverBeylaTraces recovers missing Beyla trace spans.
func (s *GapRecoveryService) recoverBeylaTraces(ctx context.Context, agent *registry.Entry, gap *database.SequenceGap, maxRecords int32) error {
	client := GetAgentClient(agent)
	resp, err := client.QueryEbpfMetrics(ctx, connect.NewRequest(&agentv1.QueryEbpfMetricsRequest{
		TracesStartSeqId: gap.StartSeqID - 1,
		MaxRecords:       maxRecords,
		IncludeTraces:    true,
	}))
	if err != nil {
		return err
	}

	if len(resp.Msg.TraceSpans) > 0 {
		return s.db.InsertBeylaTraces(ctx, agent.AgentID, resp.Msg.TraceSpans)
	}
	return nil
}

// recoverCPUProfile recovers missing CPU profile samples.
func (s *GapRecoveryService) recoverCPUProfile(ctx context.Context, agent *registry.Entry, gap *database.SequenceGap, maxRecords int32) error {
	client := GetDebugClient(nil, buildAgentURL(agent), connect.WithGRPC())
	resp, err := client.QueryCPUProfileSamples(ctx, connect.NewRequest(&agentv1.QueryCPUProfileSamplesRequest{
		StartSeqId: gap.StartSeqID - 1,
		MaxRecords: maxRecords,
	}))
	if err != nil {
		return err
	}

	if resp.Msg.Error != "" || len(resp.Msg.Samples) == 0 {
		return nil
	}

	summaries := aggregateCPUProfileSamples(ctx, s.db, s.logger, agent.AgentID, resp.Msg.Samples)
	if len(summaries) > 0 {
		return s.db.InsertCPUProfileSummaries(ctx, summaries)
	}
	return nil
}

// recoverMemoryProfile recovers missing memory profile samples.
func (s *GapRecoveryService) recoverMemoryProfile(ctx context.Context, agent *registry.Entry, gap *database.SequenceGap, maxRecords int32) error {
	client := GetDebugClient(nil, buildAgentURL(agent), connect.WithGRPC())
	resp, err := client.QueryMemoryProfileSamples(ctx, connect.NewRequest(&agentv1.QueryMemoryProfileSamplesRequest{
		StartSeqId: gap.StartSeqID - 1,
		MaxRecords: maxRecords,
	}))
	if err != nil {
		return err
	}

	if resp.Msg.Error != "" || len(resp.Msg.Samples) == 0 {
		return nil
	}

	summaries := aggregateMemoryProfileSamples(ctx, s.db, s.logger, agent.AgentID, resp.Msg.Samples)
	if len(summaries) > 0 {
		return s.db.InsertMemoryProfileSummaries(ctx, summaries)
	}
	return nil
}
