package colony

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/poller"
	"github.com/coral-mesh/coral/internal/colony/registry"
	"github.com/coral-mesh/coral/internal/constants"
)

const cpuProfileDataType = "cpu_profile"

// CPUProfilePoller periodically queries agents for continuous CPU profile samples.
// This implements the colony-side aggregation from RFD 072.
// Uses sequence-based checkpoints for reliable polling (RFD 089).
type CPUProfilePoller struct {
	*poller.BasePoller
	registry      *registry.Registry
	db            *database.Database
	pollInterval  time.Duration
	retentionDays int // How long to keep CPU profile summaries (default: 30 days).
	clientFactory func(httpClient connect.HTTPClient, url string, opts ...connect.ClientOption) agentv1connect.AgentDebugServiceClient
	logger        zerolog.Logger
}

// NewCPUProfilePoller creates a new CPU profile poller.
func NewCPUProfilePoller(
	ctx context.Context,
	registry *registry.Registry,
	db *database.Database,
	pollInterval time.Duration,
	retentionDays int,
	logger zerolog.Logger,
) *CPUProfilePoller {
	// Default to 30 days if not specified.
	if retentionDays <= 0 {
		retentionDays = 30
	}

	// Default to 30 seconds poll interval (captures 2 agent samples per poll).
	if pollInterval == 0 {
		pollInterval = 30 * time.Second
	}

	componentLogger := logger.With().Str("component", "cpu_profile_poller").Logger()

	base := poller.NewBasePoller(ctx, poller.Config{
		Name:         "cpu_profile_poller",
		PollInterval: pollInterval,
		Logger:       componentLogger,
	})

	return &CPUProfilePoller{
		BasePoller:    base,
		registry:      registry,
		db:            db,
		pollInterval:  pollInterval,
		retentionDays: retentionDays,
		clientFactory: GetDebugClient, // Default to production client.
		logger:        componentLogger,
	}
}

// Start begins the CPU profile polling loop.
func (p *CPUProfilePoller) Start() error {
	return p.BasePoller.Start(p)
}

// Stop stops the CPU profile polling loop.
func (p *CPUProfilePoller) Stop() error {
	return p.BasePoller.Stop()
}

// PollOnce performs a single polling cycle.
// Implements the poller.Poller interface.
func (p *CPUProfilePoller) PollOnce(ctx context.Context) error {
	totalSamples := 0
	allSummaries := make([]database.CPUProfileSummary, 0)

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

		// Aggregate samples into 1-minute buckets.
		summaries := p.aggregateSamples(ctx, agent.AgentID, samples)
		allSummaries = append(allSummaries, summaries...)

		// Track checkpoint for commit after storage.
		if maxSeqID > 0 {
			pendingCheckpoints[agent.AgentID] = checkpointUpdate{sessionID: sessionID, maxSeqID: maxSeqID}
		}

		totalSamples += len(samples)
		return nil
	})

	// Store summaries in database.
	if len(allSummaries) > 0 {
		if err := p.db.InsertCPUProfileSummaries(ctx, allSummaries); err != nil {
			p.logger.Error().
				Err(err).
				Int("summary_count", len(allSummaries)).
				Msg("Failed to store CPU profile summaries")
			// DO NOT update checkpoints on storage failure - retry on next poll.
		} else {
			// Storage succeeded - commit checkpoints (RFD 089).
			for agentID, cp := range pendingCheckpoints {
				if err := p.db.UpdatePollingCheckpoint(ctx, agentID, cpuProfileDataType, cp.sessionID, cp.maxSeqID); err != nil {
					p.logger.Error().Err(err).Str("agent", agentID).Msg("Failed to update checkpoint")
				}
			}

			p.logger.Info().
				Int("agents_queried", successCount).
				Int("agents_failed", errorCount).
				Int("total_samples", totalSamples).
				Int("summaries", len(allSummaries)).
				Msg("CPU profile poll completed")
		}
	} else {
		p.logger.Debug().
			Int("agents_queried", successCount).
			Msg("CPU profile poll completed with no data")
	}

	return nil
}

// pollAgent queries a single agent using checkpoint-based polling (RFD 089).
// Returns samples, session_id, max_seq_id, and any error.
func (p *CPUProfilePoller) pollAgent(ctx context.Context, agent *registry.Entry) ([]*agentv1.CPUProfileSample, string, uint64, error) {
	// Get checkpoint for this agent.
	checkpoint, _ := p.db.GetPollingCheckpoint(ctx, agent.AgentID, cpuProfileDataType)

	var startSeqID uint64
	var storedSessionID string
	if checkpoint != nil {
		startSeqID = checkpoint.LastSeqID
		storedSessionID = checkpoint.SessionID
	}

	// Query agent with sequence-based request.
	client := p.clientFactory(nil, buildAgentURL(agent), connect.WithGRPC())
	req := connect.NewRequest(&agentv1.QueryCPUProfileSamplesRequest{
		StartSeqId: startSeqID,
		MaxRecords: 5000,
	})

	queryCtx, cancel := context.WithTimeout(ctx, agentQueryTimeout)
	defer cancel()

	resp, err := client.QueryCPUProfileSamples(queryCtx, req)
	if err != nil {
		return nil, "", 0, err
	}

	if resp.Msg.Error != "" {
		p.logger.Warn().
			Str("agent_id", agent.AgentID).
			Str("error", resp.Msg.Error).
			Msg("Agent returned error when querying CPU profile samples")
		return nil, "", 0, nil
	}

	// Handle session_id mismatch (agent database was recreated).
	if storedSessionID != "" && resp.Msg.SessionId != "" && storedSessionID != resp.Msg.SessionId {
		p.logger.Warn().
			Str("agent", agent.AgentID).
			Str("stored_session", storedSessionID).
			Str("agent_session", resp.Msg.SessionId).
			Msg("Agent session changed (database recreated), resetting checkpoint")

		_ = p.db.ResetPollingCheckpoint(ctx, agent.AgentID, cpuProfileDataType)

		// Re-query from the beginning.
		req.Msg.StartSeqId = 0
		queryCtx2, cancel2 := context.WithTimeout(ctx, agentQueryTimeout)
		defer cancel2()

		resp, err = client.QueryCPUProfileSamples(queryCtx2, req)
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
				Msg("Detected CPU profile sequence gap")
			_ = p.db.RecordSequenceGap(ctx, agent.AgentID, cpuProfileDataType, gap.StartSeqID, gap.EndSeqID)
		}
	}

	return resp.Msg.Samples, resp.Msg.SessionId, resp.Msg.MaxSeqId, nil
}

// aggregateSamples aggregates CPU profile samples into 1-minute buckets.
// Samples with the same stack (within the same minute bucket) are merged by summing sample counts.
func (p *CPUProfilePoller) aggregateSamples(ctx context.Context,
	agentID string,
	samples []*agentv1.CPUProfileSample,
) []database.CPUProfileSummary {
	if len(samples) == 0 {
		return nil
	}

	// Group samples by (bucket_time, build_id, stack_frames).
	type sampleKey struct {
		bucketTime time.Time
		buildID    string
		stackHash  string
	}

	type sampleGroup struct {
		serviceName   string
		stackFrameIDs []int64
		sampleCount   uint32 // Number of samples (matches protobuf).
	}

	grouped := make(map[sampleKey]*sampleGroup)

	for _, sample := range samples {
		// Truncate timestamp to minute boundary for aggregation.
		bucketTime := sample.Timestamp.AsTime().Truncate(time.Minute)

		// Encode stack frames to integer IDs using colony's frame dictionary.
		frameIDs, err := p.db.EncodeStackFrames(ctx, sample.StackFrames)
		if err != nil {
			p.logger.Warn().
				Err(err).
				Int("stack_depth", len(sample.StackFrames)).
				Msg("Failed to encode stack frames, skipping sample")
			continue
		}

		// Compute stack hash for deduplication.
		stackHash := database.ComputeStackHash(frameIDs)

		key := sampleKey{
			bucketTime: bucketTime,
			buildID:    sample.BuildId,
			stackHash:  stackHash,
		}

		if existing, exists := grouped[key]; exists {
			// Merge: sum sample counts.
			existing.sampleCount += sample.SampleCount
		} else {
			grouped[key] = &sampleGroup{
				serviceName:   sample.ServiceName,
				stackFrameIDs: frameIDs,
				sampleCount:   sample.SampleCount,
			}
		}
	}

	// Convert grouped samples to summaries.
	summaries := make([]database.CPUProfileSummary, 0, len(grouped))

	for key, group := range grouped {
		summary := database.CPUProfileSummary{
			Timestamp:     key.bucketTime,
			AgentID:       agentID,
			ServiceName:   group.serviceName,
			BuildID:       key.buildID,
			StackHash:     key.stackHash,
			StackFrameIDs: group.stackFrameIDs,
			SampleCount:   group.sampleCount,
		}

		summaries = append(summaries, summary)
	}

	return summaries
}

// RunCleanup performs CPU profile database cleanup.
// Removes summaries older than configured retention period.
// Implements the poller.Poller interface.
func (p *CPUProfilePoller) RunCleanup(ctx context.Context) error {
	deleted, err := p.db.CleanupOldCPUProfiles(ctx, p.retentionDays)
	if err != nil {
		p.logger.Error().
			Err(err).
			Msg("Failed to cleanup old CPU profile summaries")
		return err
	}

	if deleted > 0 {
		p.logger.Info().
			Int64("deleted_count", deleted).
			Int("retention_days", p.retentionDays).
			Msg("Cleaned up old CPU profile summaries")
	}

	// Also cleanup orphaned frame dictionary entries.
	deletedFrames, err := p.db.CleanupOrphanedFrames(ctx)
	if err != nil {
		p.logger.Error().
			Err(err).
			Msg("Failed to cleanup orphaned frame dictionary entries")
		return err
	}

	if deletedFrames > 0 {
		p.logger.Info().
			Int64("deleted_frames", deletedFrames).
			Msg("Cleaned up orphaned frame dictionary entries")
	}

	return nil
}

// buildAgentURL constructs the agent gRPC URL from registry entry.
// Uses the same pattern as GetAgentClient for consistency.
func buildAgentURL(agent *registry.Entry) string {
	agentAddr := net.JoinHostPort(agent.MeshIPv4, strconv.Itoa(constants.DefaultAgentPort))
	return fmt.Sprintf("http://%s", agentAddr)
}
