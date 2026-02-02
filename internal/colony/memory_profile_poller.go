package colony

import (
	"context"
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
)

// MemoryProfilePoller periodically queries agents for continuous memory profile samples (RFD 077).
type MemoryProfilePoller struct {
	*poller.BasePoller
	registry        *registry.Registry
	db              *database.Database
	pollInterval    time.Duration
	retentionDays   int
	clientFactory   func(httpClient connect.HTTPClient, url string, opts ...connect.ClientOption) agentv1connect.AgentDebugServiceClient
	logger          zerolog.Logger
	lastPollTimes   map[string]time.Time
	lastPollTimesMu sync.RWMutex
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
		lastPollTimes: make(map[string]time.Time),
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
	now := time.Now()

	allSummaries := make([]database.MemoryProfileSummary, 0)
	queryEndTimes := make(map[string]time.Time)

	successCount, errorCount := poller.ForEachHealthyAgent(p.registry, p.logger, func(agent *registry.Entry) error {
		p.lastPollTimesMu.RLock()
		lastPoll, exists := p.lastPollTimes[agent.AgentID]
		p.lastPollTimesMu.RUnlock()

		if !exists {
			lastPoll = now.Add(-2 * time.Minute)
		}

		// Ensure lookback buffer for samples stored after their timestamp.
		maxLookback := now.Add(-60 * time.Second)
		if lastPoll.After(maxLookback) {
			lastPoll = maxLookback
		}

		queryEndTime := now
		samples, err := p.queryAgent(ctx, agent, lastPoll, queryEndTime)
		if err != nil {
			return err
		}

		summaries := p.aggregateSamples(ctx, agent.AgentID, samples)
		allSummaries = append(allSummaries, summaries...)
		queryEndTimes[agent.AgentID] = queryEndTime

		return nil
	})

	if len(allSummaries) > 0 {
		if err := p.db.InsertMemoryProfileSummaries(ctx, allSummaries); err != nil {
			p.logger.Error().
				Err(err).
				Int("summary_count", len(allSummaries)).
				Msg("Failed to store memory profile summaries")
		} else {
			p.lastPollTimesMu.Lock()
			for agentID, endTime := range queryEndTimes {
				p.lastPollTimes[agentID] = endTime
			}
			p.lastPollTimesMu.Unlock()

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

// queryAgent queries a single agent for memory profile samples.
func (p *MemoryProfilePoller) queryAgent(ctx context.Context,
	agent *registry.Entry,
	startTime, endTime time.Time,
) ([]*agentv1.MemoryProfileSample, error) {
	client := p.clientFactory(nil, buildAgentURL(agent), connect.WithGRPC())

	req := connect.NewRequest(&agentv1.QueryMemoryProfileSamplesRequest{
		StartTime: timestamppb.New(startTime),
		EndTime:   timestamppb.New(endTime),
	})

	ctx, cancel := context.WithTimeout(ctx, agentQueryTimeout)
	defer cancel()

	resp, err := client.QueryMemoryProfileSamples(ctx, req)
	if err != nil {
		return nil, err
	}

	if resp.Msg.Error != "" {
		p.logger.Warn().
			Str("agent_id", agent.AgentID).
			Str("error", resp.Msg.Error).
			Msg("Agent returned error when querying memory profile samples")
		return nil, nil
	}

	return resp.Msg.Samples, nil
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
