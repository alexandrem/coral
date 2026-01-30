package colony

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/poller"
	"github.com/coral-mesh/coral/internal/colony/registry"
	"github.com/coral-mesh/coral/internal/constants"
)

// CPUProfilePoller periodically queries agents for continuous CPU profile samples.
// This implements the colony-side aggregation from RFD 072.
type CPUProfilePoller struct {
	*poller.BasePoller
	registry        *registry.Registry
	db              *database.Database
	pollInterval    time.Duration
	retentionDays   int // How long to keep CPU profile summaries (default: 30 days).
	clientFactory   func(httpClient connect.HTTPClient, url string, opts ...connect.ClientOption) agentv1connect.AgentDebugServiceClient
	logger          zerolog.Logger
	lastPollTimes   map[string]time.Time // Track last poll time per agent.
	lastPollTimesMu sync.RWMutex
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
		lastPollTimes: make(map[string]time.Time),
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
	now := time.Now()

	totalSamples := 0
	allSummaries := make([]database.CPUProfileSummary, 0)

	// Track successful query times per agent for checkpoint updates.
	// Only update checkpoints AFTER successful storage (like Kafka consumer commits).
	queryEndTimes := make(map[string]time.Time)

	successCount, errorCount := poller.ForEachHealthyAgent(p.registry, p.logger, func(agent *registry.Entry) error {
		// Get last poll time for this agent.
		p.lastPollTimesMu.RLock()
		lastPoll, exists := p.lastPollTimes[agent.AgentID]
		p.lastPollTimesMu.RUnlock()

		// If first poll for this agent, look back far enough to catch samples.
		if !exists {
			lastPoll = now.Add(-2 * time.Minute)
		}

		// Ensure we always look back at least 30 seconds to catch samples whose
		// timestamps predate their storage time. The profiler collects for 15s then
		// stores; the sample timestamp is the collection start, not storage time.
		maxLookback := now.Add(-30 * time.Second)
		if lastPoll.After(maxLookback) {
			lastPoll = maxLookback
		}

		// Query agent for samples since last poll.
		queryEndTime := now
		samples, err := p.queryAgent(ctx, agent, lastPoll, queryEndTime)
		if err != nil {
			return err
		}

		// Aggregate samples into 1-minute buckets.
		summaries := p.aggregateSamples(ctx, agent.AgentID, samples)
		allSummaries = append(allSummaries, summaries...)

		// Track query end time for checkpoint update (only after successful storage).
		queryEndTimes[agent.AgentID] = queryEndTime

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
			// Storage succeeded - commit checkpoints (like Kafka consumer offset commit).
			p.lastPollTimesMu.Lock()
			for agentID, endTime := range queryEndTimes {
				p.lastPollTimes[agentID] = endTime
			}
			p.lastPollTimesMu.Unlock()

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

// queryAgent queries a single agent for CPU profile samples.
func (p *CPUProfilePoller) queryAgent(ctx context.Context,
	agent *registry.Entry,
	startTime, endTime time.Time,
) ([]*agentv1.CPUProfileSample, error) {
	// Create gRPC client for this agent.
	client := p.clientFactory(nil, buildAgentURL(agent), connect.WithGRPC())

	// Create query request (service_name is optional - query all services on agent).
	req := connect.NewRequest(&agentv1.QueryCPUProfileSamplesRequest{
		StartTime: timestamppb.New(startTime),
		EndTime:   timestamppb.New(endTime),
	})

	// Set timeout for the request.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Call agent's QueryCPUProfileSamples RPC.
	resp, err := client.QueryCPUProfileSamples(ctx, req)
	if err != nil {
		return nil, err
	}

	if resp.Msg.Error != "" {
		p.logger.Warn().
			Str("agent_id", agent.AgentID).
			Str("error", resp.Msg.Error).
			Msg("Agent returned error when querying CPU profile samples")
		return nil, nil
	}

	return resp.Msg.Samples, nil
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
	// Key format: "bucket_time_unix|build_id|stack_hash"
	type sampleKey struct {
		bucketTime time.Time
		buildID    string
		stackHash  string
	}

	type sampleGroup struct {
		serviceName   string
		stackFrameIDs []int64
		sampleCount   uint32 // Number of samples (matches protobuf)
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
			// New group - use service_name from sample (RFD 072 fix).
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
