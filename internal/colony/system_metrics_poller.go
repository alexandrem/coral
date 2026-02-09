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
		retentionDays = 30
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

	successCount, errorCount := poller.ForEachHealthyAgent(p.registry, p.logger, func(agent *registry.Entry) error {
		metrics, err := p.pollAgent(ctx, agent)
		if err != nil {
			return err
		}

		// Aggregate metrics into 1-minute buckets.
		summaries := p.aggregateMetrics(agent.AgentID, metrics, startTime, now)
		allSummaries = append(allSummaries, summaries...)

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
			return err
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
func (p *SystemMetricsPoller) pollAgent(ctx context.Context, agent *registry.Entry) ([]*agentv1.SystemMetric, error) {
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
		MaxRecords:  10000,
		MetricNames: nil, // Query all metrics.
	})

	queryCtx, cancel := context.WithTimeout(ctx, agentQueryTimeout)
	defer cancel()

	resp, err := client.QuerySystemMetrics(queryCtx, req)
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

		if err := p.db.ResetPollingCheckpoint(ctx, agent.AgentID, systemMetricsDataType); err != nil {
			p.logger.Error().Err(err).Str("agent", agent.AgentID).Msg("Failed to reset checkpoint")
		}

		// Re-query from the beginning with the new session.
		req.Msg.StartSeqId = 0
		queryCtx2, cancel2 := context.WithTimeout(ctx, agentQueryTimeout)
		defer cancel2()

		resp, err = client.QuerySystemMetrics(queryCtx2, req)
		if err != nil {
			return nil, err
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

	// Update checkpoint if we got data.
	if resp.Msg.MaxSeqId > 0 {
		if err := p.db.UpdatePollingCheckpoint(ctx, agent.AgentID, systemMetricsDataType, resp.Msg.SessionId, resp.Msg.MaxSeqId); err != nil {
			p.logger.Error().Err(err).Str("agent", agent.AgentID).Msg("Failed to update checkpoint")
		}
	}

	return resp.Msg.Metrics, nil
}

// aggregateMetrics aggregates metrics into 1-minute buckets with min/max/avg/p95 calculations.
func (p *SystemMetricsPoller) aggregateMetrics(
	agentID string,
	metrics []*agentv1.SystemMetric,
	startTime, endTime time.Time,
) []database.SystemMetricsSummary {
	if len(metrics) == 0 {
		return nil
	}

	// Group metrics by metric name and attributes.
	// Key format: "metric_name|attributes_json"
	type metricKey struct {
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
			name:       metric.Name,
			attributes: metric.Attributes,
		}

		if _, exists := grouped[key]; !exists {
			grouped[key] = &metricGroup{
				values:     make([]float64, 0),
				unit:       metric.Unit,
				metricType: metric.MetricType,
			}
		}

		grouped[key].values = append(grouped[key].values, metric.Value)
	}

	// Aggregate each group into a summary.
	// Bucket time is truncated to the start of the minute for consistency.
	bucketTime := startTime.Truncate(time.Minute)

	summaries := make([]database.SystemMetricsSummary, 0, len(grouped))

	for key, group := range grouped {
		if len(group.values) == 0 {
			continue
		}

		// Sort values for percentile calculation.
		sortedValues := make([]float64, len(group.values))
		copy(sortedValues, group.values)
		sort.Float64s(sortedValues)

		// Calculate min, max, avg.
		minVal := sortedValues[0]
		maxVal := sortedValues[len(sortedValues)-1]
		sum := 0.0
		for _, v := range sortedValues {
			sum += v
		}
		avgVal := sum / float64(len(sortedValues))

		// Calculate p95.
		p95Val := calculatePercentile(sortedValues, 0.95)

		// Calculate delta for counters.
		// For counter metrics, delta is the difference between max and min (total change in window).
		deltaVal := 0.0
		if group.metricType == "counter" || group.metricType == "delta" {
			deltaVal = maxVal - minVal
		}

		summary := database.SystemMetricsSummary{
			BucketTime:  bucketTime,
			AgentID:     agentID,
			MetricName:  key.name,
			MinValue:    minVal,
			MaxValue:    maxVal,
			AvgValue:    avgVal,
			P95Value:    p95Val,
			DeltaValue:  deltaVal,
			SampleCount: uint64(len(group.values)),
			Unit:        group.unit,
			MetricType:  group.metricType,
			Attributes:  key.attributes,
		}

		summaries = append(summaries, summary)
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
