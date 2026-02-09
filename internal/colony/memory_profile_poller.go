package colony

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/poller"
	"github.com/coral-mesh/coral/internal/colony/registry"
)

const memoryProfileDataType = "memory_profile"

// MemoryProfilePoller periodically queries agents for continuous memory profile samples (RFD 077).
// Uses sequence-based checkpoints for reliable polling (RFD 089).
type MemoryProfilePoller struct {
	*poller.BasePoller
	registry      *registry.Registry
	db            *database.Database
	pollInterval  time.Duration
	retentionDays int
	clientFactory func(httpClient connect.HTTPClient, url string, opts ...connect.ClientOption) agentv1connect.AgentDebugServiceClient
	logger        zerolog.Logger
}

// NewMemoryProfilePoller creates a new memory profile poller (RFD 077).
func NewMemoryProfilePoller(
	ctx context.Context,
	registry *registry.Registry,
	db *database.Database,
	pollInterval time.Duration,
	retentionDays int,
	logger zerolog.Logger,
) *MemoryProfilePoller {
	if retentionDays <= 0 {
		retentionDays = 30
	}

	// Default to 60 seconds poll interval (matches agent's 60s snapshot interval).
	if pollInterval == 0 {
		pollInterval = 60 * time.Second
	}

	componentLogger := logger.With().Str("component", "memory_profile_poller").Logger()

	base := poller.NewBasePoller(ctx, poller.Config{
		Name:         "memory_profile_poller",
		PollInterval: pollInterval,
		Logger:       componentLogger,
	})

	return &MemoryProfilePoller{
		BasePoller:    base,
		registry:      registry,
		db:            db,
		pollInterval:  pollInterval,
		retentionDays: retentionDays,
		clientFactory: GetDebugClient,
		logger:        componentLogger,
	}
}

// Start begins the memory profile polling loop.
func (p *MemoryProfilePoller) Start() error {
	return p.BasePoller.Start(p)
}

// Stop stops the memory profile polling loop.
func (p *MemoryProfilePoller) Stop() error {
	return p.BasePoller.Stop()
}

// PollOnce performs a single polling cycle.
func (p *MemoryProfilePoller) PollOnce(ctx context.Context) error {
	allSummaries := make([]database.MemoryProfileSummary, 0)

	// Track checkpoint updates per agent. Only commit after successful storage.
	type checkpointUpdate struct {
		sessionID string
		maxSeqID  uint64
	}
	pendingCheckpoints := make(map[string]checkpointUpdate)

	successCount, errorCount := poller.ForEachHealthyAgent(p.registry, p.logger, func(agent *registry.Entry) error {
		samples, sessionID, maxSeqID, err := p.pollAgent(ctx, agent)
		if err != nil {
			return err
		}

		summaries := p.aggregateSamples(ctx, agent.AgentID, samples)
		allSummaries = append(allSummaries, summaries...)

		if maxSeqID > 0 {
			pendingCheckpoints[agent.AgentID] = checkpointUpdate{sessionID: sessionID, maxSeqID: maxSeqID}
		}

		return nil
	})

	if len(allSummaries) > 0 {
		if err := p.db.InsertMemoryProfileSummaries(ctx, allSummaries); err != nil {
			p.logger.Error().
				Err(err).
				Int("summary_count", len(allSummaries)).
				Msg("Failed to store memory profile summaries")
			// DO NOT update checkpoints on storage failure.
		} else {
			// Storage succeeded - commit checkpoints (RFD 089).
			for agentID, cp := range pendingCheckpoints {
				if err := p.db.UpdatePollingCheckpoint(ctx, agentID, memoryProfileDataType, cp.sessionID, cp.maxSeqID); err != nil {
					p.logger.Error().Err(err).Str("agent", agentID).Msg("Failed to update checkpoint")
				}
			}

			p.logger.Info().
				Int("agents_queried", successCount).
				Int("agents_failed", errorCount).
				Int("summaries", len(allSummaries)).
				Msg("Memory profile poll completed")
		}
	} else {
		p.logger.Debug().
			Int("agents_queried", successCount).
			Msg("Memory profile poll completed with no data")
	}

	return nil
}

// pollAgent queries a single agent using checkpoint-based polling (RFD 089).
// Returns samples, session_id, max_seq_id, and any error.
func (p *MemoryProfilePoller) pollAgent(ctx context.Context, agent *registry.Entry) ([]*agentv1.MemoryProfileSample, string, uint64, error) {
	// Get checkpoint for this agent.
	checkpoint, _ := p.db.GetPollingCheckpoint(ctx, agent.AgentID, memoryProfileDataType)

	var startSeqID uint64
	var storedSessionID string
	if checkpoint != nil {
		startSeqID = checkpoint.LastSeqID
		storedSessionID = checkpoint.SessionID
	}

	// Query agent with sequence-based request.
	client := p.clientFactory(nil, buildAgentURL(agent), connect.WithGRPC())
	req := connect.NewRequest(&agentv1.QueryMemoryProfileSamplesRequest{
		StartSeqId: startSeqID,
		MaxRecords: 5000,
	})

	queryCtx, cancel := context.WithTimeout(ctx, agentQueryTimeout)
	defer cancel()

	resp, err := client.QueryMemoryProfileSamples(queryCtx, req)
	if err != nil {
		return nil, "", 0, err
	}

	if resp.Msg.Error != "" {
		p.logger.Warn().
			Str("agent_id", agent.AgentID).
			Str("error", resp.Msg.Error).
			Msg("Agent returned error when querying memory profile samples")
		return nil, "", 0, nil
	}

	// Handle session_id mismatch (agent database was recreated).
	if storedSessionID != "" && resp.Msg.SessionId != "" && storedSessionID != resp.Msg.SessionId {
		p.logger.Warn().
			Str("agent", agent.AgentID).
			Str("stored_session", storedSessionID).
			Str("agent_session", resp.Msg.SessionId).
			Msg("Agent session changed (database recreated), resetting checkpoint")

		_ = p.db.ResetPollingCheckpoint(ctx, agent.AgentID, memoryProfileDataType)

		// Re-query from the beginning.
		req.Msg.StartSeqId = 0
		queryCtx2, cancel2 := context.WithTimeout(ctx, agentQueryTimeout)
		defer cancel2()

		resp, err = client.QueryMemoryProfileSamples(queryCtx2, req)
		if err != nil {
			return nil, "", 0, err
		}
	}

	// Detect gaps in sequence IDs (RFD 089).
	if len(resp.Msg.Samples) > 0 {
		seqIDs := make([]uint64, len(resp.Msg.Samples))
		timestamps := make([]int64, len(resp.Msg.Samples))
		for i, s := range resp.Msg.Samples {
			seqIDs[i] = s.SeqId
			if s.Timestamp != nil {
				timestamps[i] = s.Timestamp.AsTime().UnixMilli()
			}
		}
		for _, gap := range poller.DetectGaps(startSeqID, seqIDs, timestamps) {
			p.logger.Warn().
				Str("agent", agent.AgentID).
				Uint64("gap_start", gap.StartSeqID).
				Uint64("gap_end", gap.EndSeqID).
				Msg("Detected memory profile sequence gap")
			_ = p.db.RecordSequenceGap(ctx, agent.AgentID, memoryProfileDataType, gap.StartSeqID, gap.EndSeqID)
		}
	}

	return resp.Msg.Samples, resp.Msg.SessionId, resp.Msg.MaxSeqId, nil
}

// aggregateSamples aggregates memory profile samples into 1-minute buckets.
func (p *MemoryProfilePoller) aggregateSamples(ctx context.Context,
	agentID string,
	samples []*agentv1.MemoryProfileSample,
) []database.MemoryProfileSummary {
	if len(samples) == 0 {
		return nil
	}

	type sampleKey struct {
		bucketTime time.Time
		buildID    string
		stackHash  string
	}

	type sampleGroup struct {
		serviceName   string
		stackFrameIDs []int64
		allocBytes    int64
		allocObjects  int64
	}

	grouped := make(map[sampleKey]*sampleGroup)

	for _, sample := range samples {
		bucketTime := sample.Timestamp.AsTime().Truncate(time.Minute)

		frameIDs, err := p.db.EncodeStackFrames(ctx, sample.StackFrames)
		if err != nil {
			p.logger.Warn().
				Err(err).
				Int("stack_depth", len(sample.StackFrames)).
				Msg("Failed to encode stack frames, skipping memory sample")
			continue
		}

		stackHash := database.ComputeStackHash(frameIDs)

		key := sampleKey{
			bucketTime: bucketTime,
			buildID:    sample.BuildId,
			stackHash:  stackHash,
		}

		if existing, exists := grouped[key]; exists {
			existing.allocBytes += sample.AllocBytes
			existing.allocObjects += sample.AllocObjects
		} else {
			grouped[key] = &sampleGroup{
				serviceName:   sample.ServiceName,
				stackFrameIDs: frameIDs,
				allocBytes:    sample.AllocBytes,
				allocObjects:  sample.AllocObjects,
			}
		}
	}

	summaries := make([]database.MemoryProfileSummary, 0, len(grouped))
	for key, group := range grouped {
		summaries = append(summaries, database.MemoryProfileSummary{
			Timestamp:     key.bucketTime,
			AgentID:       agentID,
			ServiceName:   group.serviceName,
			BuildID:       key.buildID,
			StackHash:     key.stackHash,
			StackFrameIDs: group.stackFrameIDs,
			AllocBytes:    group.allocBytes,
			AllocObjects:  group.allocObjects,
		})
	}

	return summaries
}

// RunCleanup performs memory profile database cleanup (RFD 077).
func (p *MemoryProfilePoller) RunCleanup(ctx context.Context) error {
	deleted, err := p.db.CleanupOldMemoryProfiles(ctx, p.retentionDays)
	if err != nil {
		p.logger.Error().Err(err).Msg("Failed to cleanup old memory profile summaries")
		return err
	}

	if deleted > 0 {
		p.logger.Info().
			Int64("deleted_count", deleted).
			Int("retention_days", p.retentionDays).
			Msg("Cleaned up old memory profile summaries")
	}

	return nil
}
