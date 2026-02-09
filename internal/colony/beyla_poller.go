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

// Beyla checkpoint data types (RFD 089).
const (
	beylaHTTPDataType   = "beyla_http"
	beylaGRPCDataType   = "beyla_grpc"
	beylaSQLDataType    = "beyla_sql"
	beylaTracesDataType = "beyla_traces"
)

// BeylaPoller periodically queries agents for Beyla metrics data.
// This implements the pull-based telemetry architecture from RFD 032.
// Uses sequence-based checkpoints for reliable polling (RFD 089).
type BeylaPoller struct {
	*poller.BasePoller
	registry           *registry.Registry
	db                 *database.Database
	pollInterval       time.Duration
	httpRetentionDays  int // HTTP/gRPC metrics retention in days (default: 30).
	grpcRetentionDays  int // gRPC metrics retention in days (default: 30).
	sqlRetentionDays   int // SQL metrics retention in days (default: 14).
	traceRetentionDays int // Trace retention in days (default: 7) (RFD 036).
	logger             zerolog.Logger
}

// NewBeylaPoller creates a new Beyla metrics poller.
func NewBeylaPoller(
	ctx context.Context,
	registry *registry.Registry,
	db *database.Database,
	pollInterval time.Duration,
	httpRetentionDays int,
	grpcRetentionDays int,
	sqlRetentionDays int,
	traceRetentionDays int,
	logger zerolog.Logger,
) *BeylaPoller {
	// Apply defaults if not specified.
	if httpRetentionDays <= 0 {
		httpRetentionDays = 30
	}
	if grpcRetentionDays <= 0 {
		grpcRetentionDays = 30
	}
	if sqlRetentionDays <= 0 {
		sqlRetentionDays = 14
	}
	if traceRetentionDays <= 0 {
		traceRetentionDays = 7 // Default: 7 days (RFD 036)
	}

	componentLogger := logger.With().Str("component", "beyla_poller").Logger()

	base := poller.NewBasePoller(ctx, poller.Config{
		Name:         "beyla_poller",
		PollInterval: pollInterval,
		Logger:       componentLogger,
	})

	return &BeylaPoller{
		BasePoller:         base,
		registry:           registry,
		db:                 db,
		pollInterval:       pollInterval,
		httpRetentionDays:  httpRetentionDays,
		grpcRetentionDays:  grpcRetentionDays,
		sqlRetentionDays:   sqlRetentionDays,
		traceRetentionDays: traceRetentionDays,
		logger:             componentLogger,
	}
}

// Start begins the Beyla metrics polling loop.
func (p *BeylaPoller) Start() error {
	return p.BasePoller.Start(p)
}

// Stop stops the Beyla metrics polling loop.
func (p *BeylaPoller) Stop() error {
	return p.BasePoller.Stop()
}

// PollOnce performs a single polling cycle.
// Implements the poller.Poller interface.
func (p *BeylaPoller) PollOnce(ctx context.Context) error {
	totalHTTPMetrics := 0
	totalGRPCMetrics := 0
	totalSQLMetrics := 0
	totalTraces := 0

	successCount, errorCount := poller.ForEachHealthyAgent(p.registry, p.logger, func(agent *registry.Entry) error {
		httpCount, grpcCount, sqlCount, traceCount, err := p.pollAgent(ctx, agent)
		if err != nil {
			return err
		}

		totalHTTPMetrics += httpCount
		totalGRPCMetrics += grpcCount
		totalSQLMetrics += sqlCount
		totalTraces += traceCount
		return nil
	})

	if totalHTTPMetrics > 0 || totalGRPCMetrics > 0 || totalSQLMetrics > 0 || totalTraces > 0 {
		p.logger.Info().
			Int("agents_queried", successCount).
			Int("agents_failed", errorCount).
			Int("http_metrics", totalHTTPMetrics).
			Int("grpc_metrics", totalGRPCMetrics).
			Int("sql_metrics", totalSQLMetrics).
			Int("trace_spans", totalTraces).
			Msg("Beyla metrics and traces poll completed")
	} else {
		p.logger.Debug().
			Int("agents_queried", successCount).
			Msg("Beyla metrics poll completed with no data")
	}

	return nil
}

// pollAgent queries a single agent using checkpoint-based polling (RFD 089).
// Returns counts of metrics/traces stored for each type.
func (p *BeylaPoller) pollAgent(ctx context.Context, agent *registry.Entry) (httpCount, grpcCount, sqlCount, traceCount int, err error) {
	// Get checkpoints for all 4 data types.
	httpCheckpoint, _ := p.db.GetPollingCheckpoint(ctx, agent.AgentID, beylaHTTPDataType)
	grpcCheckpoint, _ := p.db.GetPollingCheckpoint(ctx, agent.AgentID, beylaGRPCDataType)
	sqlCheckpoint, _ := p.db.GetPollingCheckpoint(ctx, agent.AgentID, beylaSQLDataType)
	tracesCheckpoint, _ := p.db.GetPollingCheckpoint(ctx, agent.AgentID, beylaTracesDataType)

	var httpSeqID, grpcSeqID, sqlSeqID, tracesSeqID uint64
	var storedSessionID string
	if httpCheckpoint != nil {
		httpSeqID = httpCheckpoint.LastSeqID
		storedSessionID = httpCheckpoint.SessionID
	}
	if grpcCheckpoint != nil {
		grpcSeqID = grpcCheckpoint.LastSeqID
		if storedSessionID == "" {
			storedSessionID = grpcCheckpoint.SessionID
		}
	}
	if sqlCheckpoint != nil {
		sqlSeqID = sqlCheckpoint.LastSeqID
	}
	if tracesCheckpoint != nil {
		tracesSeqID = tracesCheckpoint.LastSeqID
	}

	// Query agent with sequence-based request.
	client := GetAgentClient(agent)
	req := connect.NewRequest(&agentv1.QueryEbpfMetricsRequest{
		HttpStartSeqId:   httpSeqID,
		GrpcStartSeqId:   grpcSeqID,
		SqlStartSeqId:    sqlSeqID,
		TracesStartSeqId: tracesSeqID,
		MaxRecords:       10000,
		ServiceNames:     nil,  // Query all services.
		MetricTypes:      nil,  // Query all metric types.
		IncludeTraces:    true, // Request traces (RFD 036).
	})

	queryCtx, cancel := context.WithTimeout(ctx, agentQueryTimeout)
	defer cancel()

	resp, err := client.QueryEbpfMetrics(queryCtx, req)
	if err != nil {
		return 0, 0, 0, 0, err
	}

	// Handle session_id mismatch (agent database was recreated).
	if storedSessionID != "" && resp.Msg.SessionId != "" && storedSessionID != resp.Msg.SessionId {
		p.logger.Warn().
			Str("agent", agent.AgentID).
			Str("stored_session", storedSessionID).
			Str("agent_session", resp.Msg.SessionId).
			Msg("Agent session changed (database recreated), resetting Beyla checkpoints")

		// Reset all 4 checkpoints.
		for _, dt := range []string{beylaHTTPDataType, beylaGRPCDataType, beylaSQLDataType, beylaTracesDataType} {
			_ = p.db.ResetPollingCheckpoint(ctx, agent.AgentID, dt)
		}

		// Re-query from the beginning.
		req.Msg.HttpStartSeqId = 0
		req.Msg.GrpcStartSeqId = 0
		req.Msg.SqlStartSeqId = 0
		req.Msg.TracesStartSeqId = 0

		queryCtx2, cancel2 := context.WithTimeout(ctx, agentQueryTimeout)
		defer cancel2()

		resp, err = client.QueryEbpfMetrics(queryCtx2, req)
		if err != nil {
			return 0, 0, 0, 0, err
		}
	}

	sessionID := resp.Msg.SessionId

	// Detect gaps and store metrics per type (RFD 089).

	// HTTP metrics.
	if len(resp.Msg.HttpMetrics) > 0 {
		seqIDs := make([]uint64, len(resp.Msg.HttpMetrics))
		timestamps := make([]int64, len(resp.Msg.HttpMetrics))
		for i, m := range resp.Msg.HttpMetrics {
			seqIDs[i] = m.SeqId
			timestamps[i] = m.Timestamp
		}
		for _, gap := range poller.DetectGaps(httpSeqID, seqIDs, timestamps) {
			p.logger.Warn().Str("agent", agent.AgentID).Uint64("gap_start", gap.StartSeqID).Uint64("gap_end", gap.EndSeqID).Msg("Detected Beyla HTTP sequence gap")
			_ = p.db.RecordSequenceGap(ctx, agent.AgentID, beylaHTTPDataType, gap.StartSeqID, gap.EndSeqID)
		}

		if storeErr := p.db.InsertBeylaHTTPMetrics(ctx, agent.AgentID, resp.Msg.HttpMetrics); storeErr != nil {
			p.logger.Error().Err(storeErr).Str("agent_id", agent.AgentID).Msg("Failed to store Beyla HTTP metrics")
		} else {
			httpCount = len(resp.Msg.HttpMetrics)
			if resp.Msg.HttpMaxSeqId > 0 {
				_ = p.db.UpdatePollingCheckpoint(ctx, agent.AgentID, beylaHTTPDataType, sessionID, resp.Msg.HttpMaxSeqId)
			}
		}
	}

	// gRPC metrics.
	if len(resp.Msg.GrpcMetrics) > 0 {
		seqIDs := make([]uint64, len(resp.Msg.GrpcMetrics))
		timestamps := make([]int64, len(resp.Msg.GrpcMetrics))
		for i, m := range resp.Msg.GrpcMetrics {
			seqIDs[i] = m.SeqId
			timestamps[i] = m.Timestamp
		}
		for _, gap := range poller.DetectGaps(grpcSeqID, seqIDs, timestamps) {
			p.logger.Warn().Str("agent", agent.AgentID).Uint64("gap_start", gap.StartSeqID).Uint64("gap_end", gap.EndSeqID).Msg("Detected Beyla gRPC sequence gap")
			_ = p.db.RecordSequenceGap(ctx, agent.AgentID, beylaGRPCDataType, gap.StartSeqID, gap.EndSeqID)
		}

		if storeErr := p.db.InsertBeylaGRPCMetrics(ctx, agent.AgentID, resp.Msg.GrpcMetrics); storeErr != nil {
			p.logger.Error().Err(storeErr).Str("agent_id", agent.AgentID).Msg("Failed to store Beyla gRPC metrics")
		} else {
			grpcCount = len(resp.Msg.GrpcMetrics)
			if resp.Msg.GrpcMaxSeqId > 0 {
				_ = p.db.UpdatePollingCheckpoint(ctx, agent.AgentID, beylaGRPCDataType, sessionID, resp.Msg.GrpcMaxSeqId)
			}
		}
	}

	// SQL metrics.
	if len(resp.Msg.SqlMetrics) > 0 {
		seqIDs := make([]uint64, len(resp.Msg.SqlMetrics))
		timestamps := make([]int64, len(resp.Msg.SqlMetrics))
		for i, m := range resp.Msg.SqlMetrics {
			seqIDs[i] = m.SeqId
			timestamps[i] = m.Timestamp
		}
		for _, gap := range poller.DetectGaps(sqlSeqID, seqIDs, timestamps) {
			p.logger.Warn().Str("agent", agent.AgentID).Uint64("gap_start", gap.StartSeqID).Uint64("gap_end", gap.EndSeqID).Msg("Detected Beyla SQL sequence gap")
			_ = p.db.RecordSequenceGap(ctx, agent.AgentID, beylaSQLDataType, gap.StartSeqID, gap.EndSeqID)
		}

		if storeErr := p.db.InsertBeylaSQLMetrics(ctx, agent.AgentID, resp.Msg.SqlMetrics); storeErr != nil {
			p.logger.Error().Err(storeErr).Str("agent_id", agent.AgentID).Msg("Failed to store Beyla SQL metrics")
		} else {
			sqlCount = len(resp.Msg.SqlMetrics)
			if resp.Msg.SqlMaxSeqId > 0 {
				_ = p.db.UpdatePollingCheckpoint(ctx, agent.AgentID, beylaSQLDataType, sessionID, resp.Msg.SqlMaxSeqId)
			}
		}
	}

	// Trace spans.
	if len(resp.Msg.TraceSpans) > 0 {
		seqIDs := make([]uint64, len(resp.Msg.TraceSpans))
		timestamps := make([]int64, len(resp.Msg.TraceSpans))
		for i, s := range resp.Msg.TraceSpans {
			seqIDs[i] = s.SeqId
			timestamps[i] = s.StartTime
		}
		for _, gap := range poller.DetectGaps(tracesSeqID, seqIDs, timestamps) {
			p.logger.Warn().Str("agent", agent.AgentID).Uint64("gap_start", gap.StartSeqID).Uint64("gap_end", gap.EndSeqID).Msg("Detected Beyla traces sequence gap")
			_ = p.db.RecordSequenceGap(ctx, agent.AgentID, beylaTracesDataType, gap.StartSeqID, gap.EndSeqID)
		}

		if storeErr := p.db.InsertBeylaTraces(ctx, agent.AgentID, resp.Msg.TraceSpans); storeErr != nil {
			p.logger.Error().Err(storeErr).Str("agent_id", agent.AgentID).Msg("Failed to store Beyla traces")
		} else {
			traceCount = len(resp.Msg.TraceSpans)
			if resp.Msg.TracesMaxSeqId > 0 {
				_ = p.db.UpdatePollingCheckpoint(ctx, agent.AgentID, beylaTracesDataType, sessionID, resp.Msg.TracesMaxSeqId)
			}
		}
	}

	return httpCount, grpcCount, sqlCount, traceCount, nil
}

// RunCleanup performs Beyla metrics database cleanup.
// Removes metrics older than configured retention periods.
// Implements the poller.Poller interface.
func (p *BeylaPoller) RunCleanup(ctx context.Context) error {
	deleted, err := p.db.CleanupOldBeylaMetrics(ctx, p.httpRetentionDays, p.grpcRetentionDays, p.sqlRetentionDays)
	if err != nil {
		p.logger.Error().
			Err(err).
			Msg("Failed to cleanup old Beyla metrics")
		return err
	}

	if deleted > 0 {
		p.logger.Info().
			Int64("deleted_count", deleted).
			Int("http_retention_days", p.httpRetentionDays).
			Int("grpc_retention_days", p.grpcRetentionDays).
			Int("sql_retention_days", p.sqlRetentionDays).
			Msg("Cleaned up old Beyla metrics")
	}

	// Cleanup traces (RFD 036).
	deletedTraces, err := p.db.CleanupOldBeylaTraces(ctx, p.traceRetentionDays)
	if err != nil {
		p.logger.Error().
			Err(err).
			Msg("Failed to cleanup old Beyla traces")
		return err
	}

	if deletedTraces > 0 {
		p.logger.Info().
			Int64("deleted_count", deletedTraces).
			Int("trace_retention_days", p.traceRetentionDays).
			Msg("Cleaned up old Beyla traces")
	}

	return nil
}
