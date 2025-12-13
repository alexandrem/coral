package colony

import (
	"context"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/registry"
)

// BeylaPoller periodically queries agents for Beyla metrics data.
// This implements the pull-based telemetry architecture from RFD 032.
type BeylaPoller struct {
	registry           *registry.Registry
	db                 *database.Database
	pollInterval       time.Duration
	httpRetentionDays  int // HTTP/gRPC metrics retention in days (default: 30)
	grpcRetentionDays  int // gRPC metrics retention in days (default: 30)
	sqlRetentionDays   int // SQL metrics retention in days (default: 14)
	traceRetentionDays int // Trace retention in days (default: 7) (RFD 036)
	logger             zerolog.Logger
	ctx                context.Context
	cancel             context.CancelFunc
	wg                 sync.WaitGroup
	running            bool
	mu                 sync.Mutex
	lastPollTime       map[string]time.Time // Track last successful poll time per agent
}

// NewBeylaPoller creates a new Beyla metrics poller.
func NewBeylaPoller(
	registry *registry.Registry,
	db *database.Database,
	pollInterval time.Duration,
	httpRetentionDays int,
	grpcRetentionDays int,
	sqlRetentionDays int,
	traceRetentionDays int,
	logger zerolog.Logger,
) *BeylaPoller {
	ctx, cancel := context.WithCancel(context.Background())

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

	return &BeylaPoller{
		registry:           registry,
		db:                 db,
		pollInterval:       pollInterval,
		httpRetentionDays:  httpRetentionDays,
		grpcRetentionDays:  grpcRetentionDays,
		sqlRetentionDays:   sqlRetentionDays,
		traceRetentionDays: traceRetentionDays,
		logger:             logger.With().Str("component", "beyla_poller").Logger(),
		ctx:                ctx,
		cancel:             cancel,
		lastPollTime:       make(map[string]time.Time),
	}
}

// Start begins the Beyla metrics polling loop.
func (p *BeylaPoller) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return nil
	}

	p.logger.Info().
		Dur("poll_interval", p.pollInterval).
		Msg("Starting Beyla metrics poller")

	p.wg.Add(1)
	go p.pollLoop()

	p.running = true
	return nil
}

// Stop stops the Beyla metrics polling loop.
func (p *BeylaPoller) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return nil
	}

	p.logger.Info().Msg("Stopping Beyla metrics poller")

	p.cancel()
	p.wg.Wait()

	p.running = false
	return nil
}

// pollLoop is the main polling loop.
func (p *BeylaPoller) pollLoop() {
	defer p.wg.Done()

	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	// Start cleanup loop in a separate goroutine.
	// Cleanup runs every hour and removes old metrics (RFD 032).
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
func (p *BeylaPoller) pollOnce() {
	// Get all registered agents.
	agents := p.registry.ListAll()

	if len(agents) == 0 {
		p.logger.Debug().Msg("No agents registered, skipping poll")
		return
	}

	// Calculate time range for this poll cycle.
	// We use a safety delay to account for agent-side buffering (5s) and network latency.
	// This ensures we don't query for data that hasn't been written to the agent's DB yet.
	const safetyDelay = 15 * time.Second
	now := time.Now()
	endTime := now.Add(-safetyDelay)

	p.logger.Debug().
		Int("agent_count", len(agents)).
		Time("end_time", endTime).
		Msg("Polling agents for Beyla metrics")

	// Query each agent.
	successCount := 0
	errorCount := 0
	totalHTTPMetrics := 0
	totalGRPCMetrics := 0
	totalSQLMetrics := 0
	totalTraces := 0

	for _, agent := range agents {
		// Only query healthy or degraded agents.
		status := registry.DetermineStatus(agent.LastSeen, now)
		if status == registry.StatusUnhealthy {
			continue
		}

		// Determine start time for this agent.
		startTime, ok := p.lastPollTime[agent.AgentID]
		if !ok {
			// First time polling this agent, default to one interval ago.
			startTime = endTime.Add(-p.pollInterval)
		}

		httpMetrics, grpcMetrics, sqlMetrics, traceSpans, err := p.queryAgent(agent, startTime, endTime)
		if err != nil {
			p.logger.Warn().
				Err(err).
				Str("agent_id", agent.AgentID).
				Str("mesh_ip", agent.MeshIPv4).
				Msg("Failed to query agent for Beyla metrics")
			errorCount++
			continue
		}

		// Store metrics in database.
		if len(httpMetrics) > 0 {
			if err := p.db.InsertBeylaHTTPMetrics(p.ctx, agent.AgentID, httpMetrics); err != nil {
				p.logger.Error().
					Err(err).
					Str("agent_id", agent.AgentID).
					Int("metric_count", len(httpMetrics)).
					Msg("Failed to store Beyla HTTP metrics")
			} else {
				totalHTTPMetrics += len(httpMetrics)
			}
		}

		if len(grpcMetrics) > 0 {
			if err := p.db.InsertBeylaGRPCMetrics(p.ctx, agent.AgentID, grpcMetrics); err != nil {
				p.logger.Error().
					Err(err).
					Str("agent_id", agent.AgentID).
					Int("metric_count", len(grpcMetrics)).
					Msg("Failed to store Beyla gRPC metrics")
			} else {
				totalGRPCMetrics += len(grpcMetrics)
			}
		}

		if len(sqlMetrics) > 0 {
			if err := p.db.InsertBeylaSQLMetrics(p.ctx, agent.AgentID, sqlMetrics); err != nil {
				p.logger.Error().
					Err(err).
					Str("agent_id", agent.AgentID).
					Int("metric_count", len(sqlMetrics)).
					Msg("Failed to store Beyla SQL metrics")
			} else {
				totalSQLMetrics += len(sqlMetrics)
			}
		}

		// Store traces in database (RFD 036).
		if len(traceSpans) > 0 {
			if err := p.db.InsertBeylaTraces(p.ctx, agent.AgentID, traceSpans); err != nil {
				p.logger.Error().
					Err(err).
					Str("agent_id", agent.AgentID).
					Int("span_count", len(traceSpans)).
					Msg("Failed to store Beyla traces")
			} else {
				totalTraces += len(traceSpans)
			}
		}

		// Update last poll time for this agent.
		p.lastPollTime[agent.AgentID] = endTime

		successCount++
	}

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
}

// queryAgent queries a single agent for Beyla metrics.
func (p *BeylaPoller) queryAgent(
	agent *registry.Entry,
	startTime, endTime time.Time,
) ([]*agentv1.EbpfHttpMetric, []*agentv1.EbpfGrpcMetric, []*agentv1.EbpfSqlMetric, []*agentv1.EbpfTraceSpan, error) {
	// Create gRPC client for this agent.
	client := GetAgentClient(agent)

	// Create query request (RFD 036: include traces).
	req := connect.NewRequest(&agentv1.QueryEbpfMetricsRequest{
		StartTime:     startTime.Unix(),
		EndTime:       endTime.Unix(),
		ServiceNames:  nil,  // Query all services.
		MetricTypes:   nil,  // Query all metric types (HTTP, gRPC, SQL).
		IncludeTraces: true, // Request traces (RFD 036).
		MaxTraces:     1000, // Limit traces per poll.
		TraceId:       "",   // No specific trace ID filter.
	})

	// Set timeout for the request.
	ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
	defer cancel()

	// Call agent's QueryBeylaMetrics RPC.
	resp, err := client.QueryEbpfMetrics(ctx, req)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	return resp.Msg.HttpMetrics, resp.Msg.GrpcMetrics, resp.Msg.SqlMetrics, resp.Msg.TraceSpans, nil
}

// runCleanup performs Beyla metrics database cleanup.
// Removes metrics older than configured retention periods.
func (p *BeylaPoller) runCleanup() {
	deleted, err := p.db.CleanupOldBeylaMetrics(p.ctx, p.httpRetentionDays, p.grpcRetentionDays, p.sqlRetentionDays)
	if err != nil {
		p.logger.Error().
			Err(err).
			Msg("Failed to cleanup old Beyla metrics")
		return
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
	deletedTraces, err := p.db.CleanupOldBeylaTraces(p.ctx, p.traceRetentionDays)
	if err != nil {
		p.logger.Error().
			Err(err).
			Msg("Failed to cleanup old Beyla traces")
		return
	}

	if deletedTraces > 0 {
		p.logger.Info().
			Int64("deleted_count", deletedTraces).
			Int("trace_retention_days", p.traceRetentionDays).
			Msg("Cleaned up old Beyla traces")
	}
}
