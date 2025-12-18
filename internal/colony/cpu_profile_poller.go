package colony

import (
	"context"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/timestamppb"

	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/coral/mesh/v1/meshv1connect"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/registry"
)

// CPUProfilePoller periodically queries agents for continuous CPU profile samples.
// This implements the colony-side aggregation from RFD 072.
type CPUProfilePoller struct {
	registry        *registry.Registry
	db              *database.Database
	pollInterval    time.Duration
	retentionDays   int // How long to keep CPU profile summaries (default: 30 days).
	clientFactory   func(httpClient connect.HTTPClient, url string, opts ...connect.ClientOption) meshv1connect.DebugServiceClient
	logger          zerolog.Logger
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	running         bool
	mu              sync.Mutex
	lastPollTimes   map[string]time.Time // Track last poll time per agent.
	lastPollTimesMu sync.RWMutex
}

// NewCPUProfilePoller creates a new CPU profile poller.
func NewCPUProfilePoller(
	registry *registry.Registry,
	db *database.Database,
	pollInterval time.Duration,
	retentionDays int,
	logger zerolog.Logger,
) *CPUProfilePoller {
	ctx, cancel := context.WithCancel(context.Background())

	// Default to 30 days if not specified.
	if retentionDays <= 0 {
		retentionDays = 30
	}

	// Default to 30 seconds poll interval (captures 2 agent samples per poll).
	if pollInterval == 0 {
		pollInterval = 30 * time.Second
	}

	return &CPUProfilePoller{
		registry:      registry,
		db:            db,
		pollInterval:  pollInterval,
		retentionDays: retentionDays,
		clientFactory: GetDebugClient, // Default to production client.
		logger:        logger.With().Str("component", "cpu_profile_poller").Logger(),
		ctx:           ctx,
		cancel:        cancel,
		lastPollTimes: make(map[string]time.Time),
	}
}

// Start begins the CPU profile polling loop.
func (p *CPUProfilePoller) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return nil
	}

	p.logger.Info().
		Dur("poll_interval", p.pollInterval).
		Int("retention_days", p.retentionDays).
		Msg("Starting CPU profile poller")

	p.wg.Add(1)
	go p.pollLoop()

	p.running = true
	return nil
}

// Stop stops the CPU profile polling loop.
func (p *CPUProfilePoller) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return nil
	}

	p.logger.Info().Msg("Stopping CPU profile poller")

	p.cancel()
	p.wg.Wait()

	p.running = false
	return nil
}

// pollLoop is the main polling loop.
func (p *CPUProfilePoller) pollLoop() {
	defer p.wg.Done()

	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	// Start cleanup loop in a separate goroutine.
	// Cleanup runs every 1 hour and removes summaries older than configured retention period (RFD 072).
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		cleanupTicker := time.NewTicker(1 * time.Hour)
		defer cleanupTicker.Stop()

		for {
			select {
			case <-p.ctx.Done():
				return
			case <-cleanupTicker.C:
				p.runCleanup()
			}
		}
	}()

	// Run an initial poll immediately.
	p.pollOnce()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.pollOnce()
		}
	}
}

// pollOnce performs a single polling cycle.
func (p *CPUProfilePoller) pollOnce() {
	// Get all registered agents.
	agents := p.registry.ListAll()

	if len(agents) == 0 {
		p.logger.Debug().Msg("No agents registered, skipping poll")
		return
	}

	now := time.Now()

	p.logger.Debug().
		Int("agent_count", len(agents)).
		Msg("Polling agents for CPU profile samples")

	// Query each agent and aggregate results.
	successCount := 0
	errorCount := 0
	totalSamples := 0
	allSummaries := make([]database.CPUProfileSummary, 0)

	for _, agent := range agents {
		// Only query healthy or degraded agents.
		status := registry.DetermineStatus(agent.LastSeen, now)
		if status == registry.StatusUnhealthy {
			continue
		}

		// Get last poll time for this agent.
		p.lastPollTimesMu.RLock()
		lastPoll, exists := p.lastPollTimes[agent.AgentID]
		p.lastPollTimesMu.RUnlock()

		// If first poll for this agent, query last poll interval.
		if !exists {
			lastPoll = now.Add(-p.pollInterval)
		}

		// Query agent for samples since last poll.
		samples, err := p.queryAgent(agent, lastPoll, now)
		if err != nil {
			p.logger.Warn().
				Err(err).
				Str("agent_id", agent.AgentID).
				Str("mesh_ip", agent.MeshIPv4).
				Msg("Failed to query agent for CPU profile samples")
			errorCount++
			continue
		}

		// Aggregate samples into 1-minute buckets.
		summaries := p.aggregateSamples(agent.AgentID, samples)
		allSummaries = append(allSummaries, summaries...)

		// Update last poll time.
		p.lastPollTimesMu.Lock()
		p.lastPollTimes[agent.AgentID] = now
		p.lastPollTimesMu.Unlock()

		successCount++
		totalSamples += len(samples)
	}

	// Store summaries in database.
	if len(allSummaries) > 0 {
		if err := p.db.InsertCPUProfileSummaries(p.ctx, allSummaries); err != nil {
			p.logger.Error().
				Err(err).
				Int("summary_count", len(allSummaries)).
				Msg("Failed to store CPU profile summaries")
		} else {
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
}

// queryAgent queries a single agent for CPU profile samples.
func (p *CPUProfilePoller) queryAgent(
	agent *registry.Entry,
	startTime, endTime time.Time,
) ([]*meshv1.CPUProfileSample, error) {
	// Create gRPC client for this agent.
	client := p.clientFactory(nil, buildAgentURL(agent), connect.WithGRPC())

	// Create query request (service_name is optional - query all services on agent).
	req := connect.NewRequest(&meshv1.QueryCPUProfileSamplesRequest{
		StartTime: timestamppb.New(startTime),
		EndTime:   timestamppb.New(endTime),
	})

	// Set timeout for the request.
	ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
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
func (p *CPUProfilePoller) aggregateSamples(
	agentID string,
	samples []*meshv1.CPUProfileSample,
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
		sampleCount   int32
	}

	grouped := make(map[sampleKey]*sampleGroup)

	for _, sample := range samples {
		// Truncate timestamp to minute boundary for aggregation.
		bucketTime := sample.Timestamp.AsTime().Truncate(time.Minute)

		// Encode stack frames to integer IDs using colony's frame dictionary.
		frameIDs, err := p.db.EncodeStackFrames(p.ctx, sample.StackFrames)
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
			existing.sampleCount += int32(sample.SampleCount) // #nosec G115
		} else {
			// New group.
			grouped[key] = &sampleGroup{
				serviceName:   "", // Will be populated when we support multi-service queries.
				stackFrameIDs: frameIDs,
				sampleCount:   int32(sample.SampleCount), // #nosec G115
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

// runCleanup performs CPU profile database cleanup.
// Removes summaries older than configured retention period.
func (p *CPUProfilePoller) runCleanup() {
	deleted, err := p.db.CleanupOldCPUProfiles(p.ctx, p.retentionDays)
	if err != nil {
		p.logger.Error().
			Err(err).
			Msg("Failed to cleanup old CPU profile summaries")
		return
	}

	if deleted > 0 {
		p.logger.Info().
			Int64("deleted_count", deleted).
			Int("retention_days", p.retentionDays).
			Msg("Cleaned up old CPU profile summaries")
	}

	// Also cleanup orphaned frame dictionary entries.
	deletedFrames, err := p.db.CleanupOrphanedFrames(p.ctx)
	if err != nil {
		p.logger.Error().
			Err(err).
			Msg("Failed to cleanup orphaned frame dictionary entries")
		return
	}

	if deletedFrames > 0 {
		p.logger.Info().
			Int64("deleted_frames", deletedFrames).
			Msg("Cleaned up orphaned frame dictionary entries")
	}
}

// buildAgentURL constructs the agent gRPC URL from registry entry.
func buildAgentURL(agent *registry.Entry) string {
	// Use mesh IP for communication.
	return "https://" + agent.MeshIPv4 + ":8081"
}
