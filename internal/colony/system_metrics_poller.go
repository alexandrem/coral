package colony

import (
	"context"
	"math"
	"sort"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/poller"
	"github.com/coral-mesh/coral/internal/colony/registry"
	"github.com/coral-mesh/coral/internal/constants"
)

const systemMetricsDataType = "system_metrics"

// SystemMetricsPoller periodically queries agents for system metrics data.
// This implements the pull-based system metrics architecture from RFD 071.
// Uses sequence-based checkpoints for reliable polling (RFD 089).
type SystemMetricsPoller struct {
	*poller.BasePoller
	registry      *registry.Registry
	db            *database.Database
	pollInterval  time.Duration
	retentionDays int // How long to keep system metrics summaries (default: 30 days).
	logger        zerolog.Logger
}

// NewSystemMetricsPoller creates a new system metrics poller.
func NewSystemMetricsPoller(
	ctx context.Context,
	registry *registry.Registry,
	db *database.Database,
	pollInterval time.Duration,
	retentionDays int,
	logger zerolog.Logger,
) *SystemMetricsPoller {
	// Default to 30 days if not specified.
	if retentionDays <= 0 {
		retentionDays = constants.DefaultSystemMetricsRetentionDays
	}

	componentLogger := logger.With().Str("component", "system_metrics_poller").Logger()

	base := poller.NewBasePoller(ctx, poller.Config{
		Name:         "system_metrics_poller",
		PollInterval: pollInterval,
		Logger:       componentLogger,
	})

	return &SystemMetricsPoller{
		BasePoller:    base,
		registry:      registry,
		db:            db,
		pollInterval:  pollInterval,
		retentionDays: retentionDays,
		logger:        componentLogger,
	}
}

// Start begins the system metrics polling loop.
func (p *SystemMetricsPoller) Start() error {
	return p.BasePoller.Start(p)
}

// Stop stops the system metrics polling loop.
func (p *SystemMetricsPoller) Stop() error {
	return p.BasePoller.Stop()
}

// PollOnce performs a single polling cycle.
// Implements the poller.Poller interface.
func (p *SystemMetricsPoller) PollOnce(ctx context.Context) error {
	now := time.Now()
	startTime := now.Add(-p.pollInterval)

	totalMetrics := 0
	allSummaries := make([]database.SystemMetricsSummary, 0)

	// Track checkpoint updates per agent. Only commit after successful storage.
	type checkpointUpdate struct {
		sessionID string
		maxSeqID  uint64
	}
	pendingCheckpoints := make(map[string]checkpointUpdate)

	successCount, errorCount := poller.ForEachHealthyAgent(p.registry, p.logger, func(agent *registry.Entry) error {
		metrics, sessionID, maxSeqID, err := p.pollAgent(ctx, agent)
		if err != nil {
			return err
		}

		// Aggregate metrics into 1-minute buckets.
		summaries := p.aggregateMetrics(agent.AgentID, metrics, startTime, now)
		allSummaries = append(allSummaries, summaries...)

		if maxSeqID > 0 {
			pendingCheckpoints[agent.AgentID] = checkpointUpdate{sessionID: sessionID, maxSeqID: maxSeqID}
		}

		totalMetrics += len(metrics)
		return nil
	})

	// Store summaries in database.
	if len(allSummaries) > 0 {
		if err := p.db.InsertSystemMetricsSummaries(ctx, allSummaries); err != nil {
			p.logger.Error().
				Err(err).
				Int("summary_count", len(allSummaries)).
				Msg("Failed to store system metrics summaries")
			// DO NOT update checkpoints on storage failure - retry on next poll.
			return err
		}

		// Storage succeeded - commit checkpoints (RFD 089).
		for agentID, cp := range pendingCheckpoints {
			if err := p.db.UpdatePollingCheckpoint(ctx, agentID, systemMetricsDataType, cp.sessionID, cp.maxSeqID); err != nil {
				p.logger.Error().Err(err).Str("agent", agentID).Msg("Failed to update checkpoint")
			}
		}

		p.logger.Info().
			Int("agents_queried", successCount).
			Int("agents_failed", errorCount).
			Int("total_metrics", totalMetrics).
			Int("summaries", len(allSummaries)).
			Msg("System metrics poll completed")
	} else {
		p.logger.Debug().
			Int("agents_queried", successCount).
			Msg("System metrics poll completed with no data")
	}

	return nil
}

// pollAgent queries a single agent using checkpoint-based polling (RFD 089).
// Returns metrics, session_id, max_seq_id, and any error. The caller is
// responsible for committing the checkpoint only after the metrics have been
// durably stored.
func (p *SystemMetricsPoller) pollAgent(ctx context.Context, agent *registry.Entry) ([]*agentv1.SystemMetric, string, uint64, error) {
	// Get checkpoint for this agent.
	checkpoint, err := p.db.GetPollingCheckpoint(ctx, agent.AgentID, systemMetricsDataType)
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
	req := connect.NewRequest(&agentv1.QuerySystemMetricsRequest{
		StartSeqId:  startSeqID,
		MaxRecords:  int32(constants.DefaultColonyQueryMaxRecords),
		MetricNames: nil, // Query all metrics.
	})

	queryCtx, cancel := context.WithTimeout(ctx, agentQueryTimeout)
	defer cancel()

	resp, err := client.QuerySystemMetrics(queryCtx, req)
	if err != nil {
		return nil, "", 0, err
	}

	// Handle session_id mismatch (agent database was recreated).
	if storedSessionID != "" && resp.Msg.SessionId != "" && storedSessionID != resp.Msg.SessionId {
		p.logger.Warn().
			Str("agent", agent.AgentID).
			Str("stored_session", storedSessionID).
			Str("agent_session", resp.Msg.SessionId).
			Msg("Agent session changed (database recreated), resetting checkpoint")

		if err := p.db.ResetPollingCheckpoint(ctx, agent.AgentID, systemMetricsDataType); err != nil {
			p.logger.Error().Err(err).Str("agent", agent.AgentID).Msg("Failed to reset checkpoint")
		}

		// Re-query from the beginning with the new session.
		req.Msg.StartSeqId = 0
		queryCtx2, cancel2 := context.WithTimeout(ctx, agentQueryTimeout)
		defer cancel2()

		resp, err = client.QuerySystemMetrics(queryCtx2, req)
		if err != nil {
			return nil, "", 0, err
		}
	}

	// Detect gaps in sequence IDs (RFD 089).
	if len(resp.Msg.Metrics) > 0 {
		seqIDs := make([]uint64, len(resp.Msg.Metrics))
		timestamps := make([]int64, len(resp.Msg.Metrics))
		for i, m := range resp.Msg.Metrics {
			seqIDs[i] = m.SeqId
			timestamps[i] = m.Timestamp
		}
		for _, gap := range poller.DetectGaps(startSeqID, seqIDs, timestamps) {
			p.logger.Warn().
				Str("agent", agent.AgentID).
				Uint64("gap_start", gap.StartSeqID).
				Uint64("gap_end", gap.EndSeqID).
				Msg("Detected system metrics sequence gap")
			_ = p.db.RecordSequenceGap(ctx, agent.AgentID, systemMetricsDataType, gap.StartSeqID, gap.EndSeqID)
		}
	}

	return resp.Msg.Metrics, resp.Msg.SessionId, resp.Msg.MaxSeqId, nil
}

// aggregateMetrics aggregates one poll's metrics into 1-minute bucket summaries.
// All metrics are attributed to the bucket containing startTime.
func (p *SystemMetricsPoller) aggregateMetrics(
	agentID string,
	metrics []*agentv1.SystemMetric,
	startTime, _ time.Time,
) []database.SystemMetricsSummary {
	bucketTime := startTime.Truncate(time.Minute)
	return aggregateSystemMetrics(agentID, metrics, func(*agentv1.SystemMetric) time.Time {
		return bucketTime
	})
}

// systemMetricMinute returns the 1-minute bucket containing the metric's own timestamp.
func systemMetricMinute(m *agentv1.SystemMetric) time.Time {
	return time.UnixMilli(m.Timestamp).Truncate(time.Minute)
}

// aggregateSystemMetrics groups metrics by bucket, name and attributes, and
// computes min/max/avg/p95/delta for each group. bucketOf returns the bucket
// time of a metric. It is shared by SystemMetricsPoller and GapRecoveryService.
func aggregateSystemMetrics(
	agentID string,
	metrics []*agentv1.SystemMetric,
	bucketOf func(*agentv1.SystemMetric) time.Time,
) []database.SystemMetricsSummary {
	if len(metrics) == 0 {
		return nil
	}

	type metricKey struct {
		bucketTime time.Time
		name       string
		attributes string
	}

	type metricGroup struct {
		values     []float64
		unit       string
		metricType string
	}

	grouped := make(map[metricKey]*metricGroup)

	for _, metric := range metrics {
		key := metricKey{
			bucketTime: bucketOf(metric),
			name:       metric.Name,
			attributes: metric.Attributes,
		}

		group, exists := grouped[key]
		if !exists {
			group = &metricGroup{
				unit:       metric.Unit,
				metricType: metric.MetricType,
			}
			grouped[key] = group
		}

		group.values = append(group.values, metric.Value)
	}

	summaries := make([]database.SystemMetricsSummary, 0, len(grouped))

	for key, group := range grouped {
		// Sort values for percentile calculation. Each group owns its slice.
		sortedValues := group.values
		sort.Float64s(sortedValues)

		minVal := sortedValues[0]
		maxVal := sortedValues[len(sortedValues)-1]
		sum := 0.0
		for _, v := range sortedValues {
			sum += v
		}
		avgVal := sum / float64(len(sortedValues))

		p95Val := calculatePercentile(sortedValues, 0.95)

		// For counter metrics, delta is the difference between max and min (total change in window).
		deltaVal := 0.0
		if group.metricType == "counter" || group.metricType == "delta" {
			deltaVal = maxVal - minVal
		}

		summaries = append(summaries, database.SystemMetricsSummary{
			BucketTime:  key.bucketTime,
			AgentID:     agentID,
			MetricName:  key.name,
			MinValue:    minVal,
			MaxValue:    maxVal,
			AvgValue:    avgVal,
			P95Value:    p95Val,
			DeltaValue:  deltaVal,
			SampleCount: uint64(len(sortedValues)),
			Unit:        group.unit,
			MetricType:  group.metricType,
			Attributes:  key.attributes,
		})
	}

	return summaries
}

// calculatePercentile calculates the p-th percentile from a sorted slice of values.
// p should be between 0.0 and 1.0 (e.g., 0.95 for 95th percentile).
func calculatePercentile(sortedValues []float64, p float64) float64 {
	if len(sortedValues) == 0 {
		return 0
	}

	if len(sortedValues) == 1 {
		return sortedValues[0]
	}

	// Calculate index using linear interpolation.
	index := p * float64(len(sortedValues)-1)
	lowerIndex := int(math.Floor(index))
	upperIndex := int(math.Ceil(index))

	if lowerIndex == upperIndex {
		return sortedValues[lowerIndex]
	}

	// Linear interpolation between the two values.
	lowerValue := sortedValues[lowerIndex]
	upperValue := sortedValues[upperIndex]
	fraction := index - float64(lowerIndex)

	return lowerValue + (upperValue-lowerValue)*fraction
}

// RunCleanup performs system metrics database cleanup.
// Removes summaries older than configured retention period.
// Implements the poller.Poller interface.
func (p *SystemMetricsPoller) RunCleanup(ctx context.Context) error {
	deleted, err := p.db.CleanupOldSystemMetrics(ctx, p.retentionDays)
	if err != nil {
		p.logger.Error().
			Err(err).
			Msg("Failed to cleanup old system metrics summaries")
		return err
	}

	if deleted > 0 {
		p.logger.Info().
			Int64("deleted_count", deleted).
			Int("retention_days", p.retentionDays).
			Msg("Cleaned up old system metrics summaries")
	} else {
		p.logger.Debug().Msg("No old system metrics summaries to clean up")
	}

	return nil
}
