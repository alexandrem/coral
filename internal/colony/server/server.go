package server

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/timestamppb"

	colonyv1 "github.com/coral-io/coral/coral/colony/v1"
	"github.com/coral-io/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-io/coral/internal/colony/database"
	"github.com/coral-io/coral/internal/colony/registry"
	"github.com/coral-io/coral/internal/colony/storage"
)

// Config contains configuration for the colony server.
type Config struct {
	ColonyID           string
	ApplicationName    string
	Environment        string
	DashboardPort      int
	StoragePath        string
	WireGuardPort      int
	WireGuardPublicKey string
	WireGuardEndpoints []string
	ConnectPort        int
	MeshIPv4           string
	MeshIPv6           string
}

// Server implements the ColonyService.
type Server struct {
	registry  *registry.Registry
	database  *database.Database
	config    Config
	startTime time.Time
	logger    zerolog.Logger
}

// New creates a new colony server.
func New(reg *registry.Registry, db *database.Database, config Config, logger zerolog.Logger) *Server {
	return &Server{
		registry:  reg,
		database:  db,
		config:    config,
		startTime: time.Now(),
		logger:    logger,
	}
}

// Ensure Server implements the ColonyServiceHandler interface.
var _ colonyv1connect.ColonyServiceHandler = (*Server)(nil)

// GetStatus handles colony status requests.
func (s *Server) GetStatus(
	ctx context.Context,
	req *connect.Request[colonyv1.GetStatusRequest],
) (*connect.Response[colonyv1.GetStatusResponse], error) {
	s.logger.Debug().Msg("Colony status request received")

	// Calculate uptime.
	uptime := time.Since(s.startTime)
	uptimeSeconds := int64(uptime.Seconds())

	// Get agent count.
	agentCount := int32(s.registry.Count())
	activeCount := s.registry.CountActive()

	// Determine colony status based on agent health.
	status := s.determineColonyStatus()

	// Calculate storage size.
	storageBytes, err := storage.CalculateSize(s.config.StoragePath)
	if err != nil {
		s.logger.Warn().Err(err).Msg("Failed to calculate storage size")
		storageBytes = 0
	}

	// Build dashboard URL.
	dashboardURL := ""
	if s.config.DashboardPort > 0 {
		dashboardURL = fmt.Sprintf("http://localhost:%d", s.config.DashboardPort)
	}

	// Build response.
	resp := &colonyv1.GetStatusResponse{
		ColonyId:           s.config.ColonyID,
		AppName:            s.config.ApplicationName,
		Environment:        s.config.Environment,
		Status:             status,
		StartedAt:          timestamppb.New(s.startTime),
		UptimeSeconds:      uptimeSeconds,
		AgentCount:         agentCount,
		DashboardUrl:       dashboardURL,
		StorageBytes:       storageBytes,
		WireguardPort:      int32(s.config.WireGuardPort),
		WireguardPublicKey: s.config.WireGuardPublicKey,
		WireguardEndpoints: s.config.WireGuardEndpoints,
		ConnectPort:        int32(s.config.ConnectPort),
		MeshIpv4:           s.config.MeshIPv4,
		MeshIpv6:           s.config.MeshIPv6,
	}

	s.logger.Debug().
		Str("status", status).
		Int32("agent_count", agentCount).
		Int("active_count", activeCount).
		Int64("uptime_seconds", uptimeSeconds).
		Msg("Colony status response prepared")

	return connect.NewResponse(resp), nil
}

// ListAgents handles agent list requests.
func (s *Server) ListAgents(
	ctx context.Context,
	req *connect.Request[colonyv1.ListAgentsRequest],
) (*connect.Response[colonyv1.ListAgentsResponse], error) {
	s.logger.Debug().Msg("List agents request received")

	// Get all registered agents.
	entries := s.registry.ListAll()

	// Convert registry entries to protobuf agents.
	agents := make([]*colonyv1.Agent, 0, len(entries))
	now := time.Now()

	for _, entry := range entries {
		status := registry.DetermineStatus(entry.LastSeen, now)

		agent := &colonyv1.Agent{
			AgentId:        entry.AgentID,
			ComponentName:  entry.ComponentName,
			MeshIpv4:       entry.MeshIPv4,
			MeshIpv6:       entry.MeshIPv6,
			LastSeen:       timestamppb.New(entry.LastSeen),
			Status:         string(status),
			Services:       entry.Services,       // RFD 011: Multi-service support
			RuntimeContext: entry.RuntimeContext, // RFD 018: Runtime context
		}
		agents = append(agents, agent)
	}

	resp := &colonyv1.ListAgentsResponse{
		Agents: agents,
	}

	s.logger.Debug().
		Int("agent_count", len(agents)).
		Msg("List agents response prepared")

	return connect.NewResponse(resp), nil
}

// GetTopology handles topology request.
func (s *Server) GetTopology(
	ctx context.Context,
	req *connect.Request[colonyv1.GetTopologyRequest],
) (*connect.Response[colonyv1.GetTopologyResponse], error) {
	s.logger.Debug().Msg("Get topology request received")

	// Get all registered agents.
	entries := s.registry.ListAll()

	// Convert registry entries to protobuf agents.
	agents := make([]*colonyv1.Agent, 0, len(entries))
	now := time.Now()

	for _, entry := range entries {
		status := registry.DetermineStatus(entry.LastSeen, now)

		agent := &colonyv1.Agent{
			AgentId:        entry.AgentID,
			ComponentName:  entry.ComponentName,
			MeshIpv4:       entry.MeshIPv4,
			MeshIpv6:       entry.MeshIPv6,
			LastSeen:       timestamppb.New(entry.LastSeen),
			Status:         string(status),
			Services:       entry.Services,       // RFD 011: Multi-service support
			RuntimeContext: entry.RuntimeContext, // RFD 018: Runtime context
		}
		agents = append(agents, agent)
	}

	// Return empty connections list for now (topology discovery is a future enhancement).
	resp := &colonyv1.GetTopologyResponse{
		ColonyId:    s.config.ColonyID,
		Agents:      agents,
		Connections: []*colonyv1.Connection{},
	}

	s.logger.Debug().
		Int("agent_count", len(agents)).
		Msg("Get topology response prepared")

	return connect.NewResponse(resp), nil
}

// determineColonyStatus calculates overall colony status based on agent health.
func (s *Server) determineColonyStatus() string {
	entries := s.registry.ListAll()

	// If no agents, colony is running but idle (waiting for agents).
	if len(entries) == 0 {
		return "running"
	}

	now := time.Now()
	hasUnhealthy := false
	hasDegraded := false

	for _, entry := range entries {
		status := registry.DetermineStatus(entry.LastSeen, now)
		switch status {
		case registry.StatusUnhealthy:
			hasUnhealthy = true
		case registry.StatusDegraded:
			hasDegraded = true
		}
	}

	// Unhealthy if any agent is unhealthy.
	if hasUnhealthy {
		return "unhealthy"
	}

	// Degraded if any agent is degraded.
	if hasDegraded {
		return "degraded"
	}

	// All agents are healthy.
	return "running"
}

// IngestTelemetry handles telemetry ingestion from agents (RFD 025).
func (s *Server) IngestTelemetry(
	ctx context.Context,
	req *connect.Request[colonyv1.IngestTelemetryRequest],
) (*connect.Response[colonyv1.IngestTelemetryResponse], error) {
	s.logger.Debug().
		Int("bucket_count", len(req.Msg.Buckets)).
		Msg("Telemetry ingestion request received")

	if len(req.Msg.Buckets) == 0 {
		return connect.NewResponse(&colonyv1.IngestTelemetryResponse{
			Accepted: 0,
			Rejected: 0,
			Message:  "No buckets provided",
		}), nil
	}

	// Convert protobuf buckets to database buckets.
	dbBuckets := make([]database.TelemetryBucket, 0, len(req.Msg.Buckets))
	for _, pbBucket := range req.Msg.Buckets {
		dbBucket := database.TelemetryBucket{
			BucketTime:   time.Unix(pbBucket.BucketTime, 0),
			AgentID:      pbBucket.AgentId,
			ServiceName:  pbBucket.ServiceName,
			SpanKind:     pbBucket.SpanKind,
			P50Ms:        pbBucket.P50Ms,
			P95Ms:        pbBucket.P95Ms,
			P99Ms:        pbBucket.P99Ms,
			ErrorCount:   pbBucket.ErrorCount,
			TotalSpans:   pbBucket.TotalSpans,
			SampleTraces: pbBucket.SampleTraces,
		}
		dbBuckets = append(dbBuckets, dbBucket)
	}

	// Insert buckets into database.
	if err := s.database.InsertTelemetryBuckets(ctx, dbBuckets); err != nil {
		s.logger.Error().
			Err(err).
			Msg("Failed to insert telemetry buckets")

		return connect.NewResponse(&colonyv1.IngestTelemetryResponse{
			Accepted: 0,
			Rejected: int32(len(req.Msg.Buckets)),
			Message:  fmt.Sprintf("Failed to insert buckets: %v", err),
		}), nil
	}

	s.logger.Info().
		Int("bucket_count", len(dbBuckets)).
		Msg("Telemetry buckets ingested successfully")

	return connect.NewResponse(&colonyv1.IngestTelemetryResponse{
		Accepted: int32(len(dbBuckets)),
		Rejected: 0,
		Message:  "Success",
	}), nil
}
