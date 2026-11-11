package colony

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
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

var errGapDataUnavailable = errors.New("gap data unavailable at source")

// recoveryRegistry dispatches recovery by data type.
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
	registry   *registry.Registry
	db         *database.Database
	logger     zerolog.Logger
	httpClient connect.HTTPClient
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
		httpClient: http.DefaultClient,
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
				continue
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
		} else if errors.Is(recoverErr, errGapDataUnavailable) || gap.RecoveryAttempts >= gapRecoveryMaxAttempts {
			if err := s.db.MarkGapPermanent(ctx, gap.ID); err != nil {
				s.logger.Error().Err(err).Int("gap_id", gap.ID).Msg("Failed to mark gap as permanent")
				continue
			}
			s.logger.Error().
				Err(recoverErr).
				Str("agent_id", gap.AgentID).
				Str("data_type", gap.DataType).
				Int("gap_id", gap.ID).
				Uint64("start_seq", gap.StartSeqID).
				Uint64("end_seq", gap.EndSeqID).
				Uint64("records_lost", gap.EndSeqID-gap.StartSeqID+1).
				Msg("PERMANENT DATA LOSS: gap data unavailable or recovery attempts exhausted")
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
	if gap.StartSeqID == 0 || gap.EndSeqID < gap.StartSeqID {
		return fmt.Errorf("invalid gap range %d-%d", gap.StartSeqID, gap.EndSeqID)
	}
	if recoverFn, exists := recoveryRegistry[gap.DataType]; exists {
		return recoverFn(s, ctx, agent, gap, gapMaxSize)
	}
	return fmt.Errorf("unsupported gap data type %q", gap.DataType)
}

// queryGapRecords retrieves the entire range before aggregation. Aggregating pages
// separately would overwrite earlier pages in the same summary bucket on upsert.
// Each request has its own timeout and advances only over contiguous sequence IDs.
func queryGapRecords[T interface{ GetSeqId() uint64 }](ctx context.Context, gap *database.SequenceGap, maxRecords int32, query func(context.Context, uint64, int32) ([]T, error)) ([]T, error) {
	if gap.StartSeqID == 0 || gap.EndSeqID < gap.StartSeqID || maxRecords <= 0 {
		return nil, fmt.Errorf("invalid gap range or page size")
	}
	var records []T
	next := gap.StartSeqID
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		limit := uint64(maxRecords)
		if remaining := gap.EndSeqID - next + 1; remaining < limit {
			limit = remaining
		}
		queryCtx, cancel := context.WithTimeout(ctx, agentQueryTimeout)
		page, err := query(queryCtx, next-1, int32(limit))
		cancel()
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return nil, fmt.Errorf("%w: sequence %d", errGapDataUnavailable, next)
		}
		for _, record := range page {
			seq := record.GetSeqId()
			if seq > next {
				return nil, fmt.Errorf("%w: expected sequence %d, got %d", errGapDataUnavailable, next, seq)
			}
			if seq < next {
				return nil, fmt.Errorf("invalid recovery response: expected sequence %d, got %d", next, seq)
			}
			records = append(records, record)
			if seq == gap.EndSeqID {
				return records, nil
			}
			next = seq + 1
		}
	}
}

// recoverTelemetry recovers missing telemetry spans.
func (s *GapRecoveryService) recoverTelemetry(ctx context.Context, agent *registry.Entry, gap *database.SequenceGap, maxRecords int32) error {
	client := agentv1connect.NewAgentServiceClient(s.httpClient, buildAgentURL(agent))
	records, err := queryGapRecords(ctx, gap, maxRecords, func(ctx context.Context, start uint64, limit int32) ([]*agentv1.TelemetrySpan, error) {
		resp, err := client.QueryTelemetry(ctx, connect.NewRequest(&agentv1.QueryTelemetryRequest{
			StartSeqId: start,
			MaxRecords: limit,
		}))
		if err != nil {
			return nil, err
		}
		return resp.Msg.Spans, nil
	})
	if err != nil {
		return err
	}

	// Aggregate through TelemetryAggregator and store summaries.
	aggregator := NewTelemetryAggregator()
	aggregator.AddSpans(agent.AgentID, records)
	summaries := aggregator.GetSummaries()

	if len(summaries) > 0 {
		return s.db.InsertTelemetrySummaries(ctx, summaries)
	}
	return nil
}

// recoverSystemMetrics recovers missing system metrics.
func (s *GapRecoveryService) recoverSystemMetrics(ctx context.Context, agent *registry.Entry, gap *database.SequenceGap, maxRecords int32) error {
	client := agentv1connect.NewAgentServiceClient(s.httpClient, buildAgentURL(agent))
	records, err := queryGapRecords(ctx, gap, maxRecords, func(ctx context.Context, start uint64, limit int32) ([]*agentv1.SystemMetric, error) {
		resp, err := client.QuerySystemMetrics(ctx, connect.NewRequest(&agentv1.QuerySystemMetricsRequest{
			StartSeqId: start,
			MaxRecords: limit,
		}))
		if err != nil {
			return nil, err
		}
		return resp.Msg.Metrics, nil
	})
	if err != nil {
		return err
	}

	// Bucket by each metric's own timestamp: a gap may span many minutes.
	summaries := aggregateSystemMetrics(agent.AgentID, records)
	if len(summaries) > 0 {
		return s.db.InsertSystemMetricsSummaries(ctx, summaries)
	}
	return nil
}

// recoverBeylaHTTP recovers missing Beyla HTTP metrics.
func (s *GapRecoveryService) recoverBeylaHTTP(ctx context.Context, agent *registry.Entry, gap *database.SequenceGap, maxRecords int32) error {
	client := agentv1connect.NewAgentServiceClient(s.httpClient, buildAgentURL(agent))
	records, err := queryGapRecords(ctx, gap, maxRecords, func(ctx context.Context, start uint64, limit int32) ([]*agentv1.EbpfHttpMetric, error) {
		resp, err := client.QueryEbpfMetrics(ctx, connect.NewRequest(&agentv1.QueryEbpfMetricsRequest{
			HttpStartSeqId: start,
			MaxRecords:     limit,
		}))
		if err != nil {
			return nil, err
		}
		return resp.Msg.HttpMetrics, nil
	})
	if err != nil {
		return err
	}

	return s.db.InsertBeylaHTTPMetrics(ctx, agent.AgentID, records)
}

// recoverBeylaGRPC recovers missing Beyla gRPC metrics.
func (s *GapRecoveryService) recoverBeylaGRPC(ctx context.Context, agent *registry.Entry, gap *database.SequenceGap, maxRecords int32) error {
	client := agentv1connect.NewAgentServiceClient(s.httpClient, buildAgentURL(agent))
	records, err := queryGapRecords(ctx, gap, maxRecords, func(ctx context.Context, start uint64, limit int32) ([]*agentv1.EbpfGrpcMetric, error) {
		resp, err := client.QueryEbpfMetrics(ctx, connect.NewRequest(&agentv1.QueryEbpfMetricsRequest{
			GrpcStartSeqId: start,
			MaxRecords:     limit,
		}))
		if err != nil {
			return nil, err
		}
		return resp.Msg.GrpcMetrics, nil
	})
	if err != nil {
		return err
	}

	return s.db.InsertBeylaGRPCMetrics(ctx, agent.AgentID, records)
}

// recoverBeylaSQL recovers missing Beyla SQL metrics.
func (s *GapRecoveryService) recoverBeylaSQL(ctx context.Context, agent *registry.Entry, gap *database.SequenceGap, maxRecords int32) error {
	client := agentv1connect.NewAgentServiceClient(s.httpClient, buildAgentURL(agent))
	records, err := queryGapRecords(ctx, gap, maxRecords, func(ctx context.Context, start uint64, limit int32) ([]*agentv1.EbpfSqlMetric, error) {
		resp, err := client.QueryEbpfMetrics(ctx, connect.NewRequest(&agentv1.QueryEbpfMetricsRequest{
			SqlStartSeqId: start,
			MaxRecords:    limit,
		}))
		if err != nil {
			return nil, err
		}
		return resp.Msg.SqlMetrics, nil
	})
	if err != nil {
		return err
	}

	return s.db.InsertBeylaSQLMetrics(ctx, agent.AgentID, records)
}

// recoverBeylaTraces recovers missing Beyla trace spans.
func (s *GapRecoveryService) recoverBeylaTraces(ctx context.Context, agent *registry.Entry, gap *database.SequenceGap, maxRecords int32) error {
	client := agentv1connect.NewAgentServiceClient(s.httpClient, buildAgentURL(agent))
	records, err := queryGapRecords(ctx, gap, maxRecords, func(ctx context.Context, start uint64, limit int32) ([]*agentv1.EbpfTraceSpan, error) {
		resp, err := client.QueryEbpfMetrics(ctx, connect.NewRequest(&agentv1.QueryEbpfMetricsRequest{
			TracesStartSeqId: start,
			MaxRecords:       limit,
			IncludeTraces:    true,
		}))
		if err != nil {
			return nil, err
		}
		return resp.Msg.TraceSpans, nil
	})
	if err != nil {
		return err
	}

	return s.db.InsertBeylaTraces(ctx, agent.AgentID, records)
}

// recoverCPUProfile recovers missing CPU profile samples.
func (s *GapRecoveryService) recoverCPUProfile(ctx context.Context, agent *registry.Entry, gap *database.SequenceGap, maxRecords int32) error {
	client := GetDebugClient(s.httpClient, buildAgentURL(agent), connect.WithGRPC())
	records, err := queryGapRecords(ctx, gap, maxRecords, func(ctx context.Context, start uint64, limit int32) ([]*agentv1.CPUProfileSample, error) {
		resp, err := client.QueryCPUProfileSamples(ctx, connect.NewRequest(&agentv1.QueryCPUProfileSamplesRequest{
			StartSeqId: start,
			MaxRecords: limit,
		}))
		if err != nil {
			return nil, err
		}
		if resp.Msg.Error != "" {
			return nil, fmt.Errorf("agent profile query failed: %s", resp.Msg.Error)
		}
		return resp.Msg.Samples, nil
	})
	if err != nil {
		return err
	}

	summaries := aggregateCPUProfileSamples(ctx, s.db, s.logger, agent.AgentID, records)
	if len(summaries) > 0 {
		return s.db.InsertCPUProfileSummaries(ctx, summaries)
	}
	return nil
}

// recoverMemoryProfile recovers missing memory profile samples.
func (s *GapRecoveryService) recoverMemoryProfile(ctx context.Context, agent *registry.Entry, gap *database.SequenceGap, maxRecords int32) error {
	client := GetDebugClient(s.httpClient, buildAgentURL(agent), connect.WithGRPC())
	records, err := queryGapRecords(ctx, gap, maxRecords, func(ctx context.Context, start uint64, limit int32) ([]*agentv1.MemoryProfileSample, error) {
		resp, err := client.QueryMemoryProfileSamples(ctx, connect.NewRequest(&agentv1.QueryMemoryProfileSamplesRequest{
			StartSeqId: start,
			MaxRecords: limit,
		}))
		if err != nil {
			return nil, err
		}
		if resp.Msg.Error != "" {
			return nil, fmt.Errorf("agent profile query failed: %s", resp.Msg.Error)
		}
		return resp.Msg.Samples, nil
	})
	if err != nil {
		return err
	}

	summaries := aggregateMemoryProfileSamples(ctx, s.db, s.logger, agent.AgentID, records)
	if len(summaries) > 0 {
		return s.db.InsertMemoryProfileSummaries(ctx, summaries)
	}
	return nil
}
