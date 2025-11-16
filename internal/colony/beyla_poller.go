package colony

import (
	"context"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"

	agentv1 "github.com/coral-io/coral/coral/agent/v1"
	"github.com/coral-io/coral/internal/colony/database"
	"github.com/coral-io/coral/internal/colony/registry"
)

// BeylaPoller periodically queries agents for Beyla metrics data.
// This implements the pull-based telemetry architecture from RFD 032.
type BeylaPoller struct {
	registry          *registry.Registry
	db                *database.Database
	pollInterval      time.Duration
	httpRetentionDays int // HTTP/gRPC metrics retention in days (default: 30)
	grpcRetentionDays int // gRPC metrics retention in days (default: 30)
	sqlRetentionDays  int // SQL metrics retention in days (default: 14)
	logger            zerolog.Logger
	ctx               context.Context
	cancel            context.CancelFunc
	wg                sync.WaitGroup
	running           bool
	mu                sync.Mutex
}

// NewBeylaPoller creates a new Beyla metrics poller.
func NewBeylaPoller(
	registry *registry.Registry,
	db *database.Database,
	pollInterval time.Duration,
	httpRetentionDays int,
	grpcRetentionDays int,
	sqlRetentionDays int,
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

	return &BeylaPoller{
		registry:          registry,
		db:                db,
		pollInterval:      pollInterval,
		httpRetentionDays: httpRetentionDays,
		grpcRetentionDays: grpcRetentionDays,
		sqlRetentionDays:  sqlRetentionDays,
		logger:            logger.With().Str("component", "beyla_poller").Logger(),
		ctx:               ctx,
		cancel:            cancel,
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
		p.logger.Debug().Msg("No agents registered, skipping Beyla poll")
		return
	}

	// Calculate time range for this poll cycle.
	// Query from last poll interval to now.
	now := time.Now()
	startTime := now.Add(-p.pollInterval)

	p.logger.Debug().
		Int("agent_count", len(agents)).
		Time("start_time", startTime).
		Time("end_time", now).
		Msg("Polling agents for Beyla metrics")

	// Query each agent.
	successCount := 0
	errorCount := 0
	totalHTTPMetrics := 0
	totalGRPCMetrics := 0
	totalSQLMetrics := 0

	for _, agent := range agents {
		// Only query healthy or degraded agents.
		status := registry.DetermineStatus(agent.LastSeen, now)
		if status == registry.StatusUnhealthy {
			continue
		}

		httpMetrics, grpcMetrics, sqlMetrics, err := p.queryAgent(agent, startTime, now)
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

		successCount++
	}

	if totalHTTPMetrics > 0 || totalGRPCMetrics > 0 || totalSQLMetrics > 0 {
		p.logger.Info().
			Int("agents_queried", successCount).
			Int("agents_failed", errorCount).
			Int("http_metrics", totalHTTPMetrics).
			Int("grpc_metrics", totalGRPCMetrics).
			Int("sql_metrics", totalSQLMetrics).
			Msg("Beyla metrics poll completed")
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
) ([]*agentv1.BeylaHttpMetric, []*agentv1.BeylaGrpcMetric, []*agentv1.BeylaSqlMetric, error) {
	// Create gRPC client for this agent.
	client := GetAgentClient(agent)

	// Create query request.
	req := connect.NewRequest(&agentv1.QueryBeylaMetricsRequest{
		StartTime:    startTime.Unix(),
		EndTime:      endTime.Unix(),
		ServiceNames: nil, // Query all services.
		MetricTypes:  nil, // Query all metric types (HTTP, gRPC, SQL).
	})

	// Set timeout for the request.
	ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
	defer cancel()

	// Call agent's QueryBeylaMetrics RPC.
	resp, err := client.QueryBeylaMetrics(ctx, req)
	if err != nil {
		return nil, nil, nil, err
	}

	return resp.Msg.HttpMetrics, resp.Msg.GrpcMetrics, resp.Msg.SqlMetrics, nil
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
}
