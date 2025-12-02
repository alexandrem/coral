// Package debug provides debug session orchestration for the colony.
package debug

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/coral/mesh/v1/meshv1connect"

	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/registry"
	"github.com/coral-mesh/coral/internal/constants"
)

// Orchestrator manages debug sessions across agents.
type Orchestrator struct {
	logger        zerolog.Logger
	registry      *registry.Registry
	db            *database.Database
	clientFactory func(connect.HTTPClient, string, ...connect.ClientOption) meshv1connect.DebugServiceClient
}

// NewOrchestrator creates a new debug orchestrator.
func NewOrchestrator(logger zerolog.Logger, registry *registry.Registry, db *database.Database) *Orchestrator {
	return &Orchestrator{
		logger:        logger.With().Str("component", "debug_orchestrator").Logger(),
		registry:      registry,
		db:            db,
		clientFactory: meshv1connect.NewDebugServiceClient,
	}
}

// AttachUprobe starts a new debug session by attaching a uprobe to a function.
func (o *Orchestrator) AttachUprobe(
	ctx context.Context,
	req *connect.Request[debugpb.AttachUprobeRequest],
) (*connect.Response[debugpb.AttachUprobeResponse], error) {
	o.logger.Info().
		Str("service", req.Msg.ServiceName).
		Str("function", req.Msg.FunctionName).
		Msg("Attaching uprobe")

	// Service Discovery (RFD 062)
	if req.Msg.AgentId == "" {
		// Lookup agent by service name
		// Note: registry.FindAgentForService uses cached data which may not have services populated.
		// We need to query agents in real-time to find the service.
		entries := o.registry.ListAll()

		var foundEntry *registry.Entry
		var foundService *meshv1.ServiceInfo

		for _, entry := range entries {
			// Query agent in real-time for services
			agentURL := fmt.Sprintf("http://%s:9001", entry.MeshIPv4)
			client := agentv1connect.NewAgentServiceClient(http.DefaultClient, agentURL)

			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			resp, err := client.ListServices(ctx, connect.NewRequest(&agentv1.ListServicesRequest{}))
			cancel()

			if err != nil {
				o.logger.Debug().
					Err(err).
					Str("agent_id", entry.AgentID).
					Msg("Failed to query agent services")
				continue
			}

			// Check if this agent has the service
			for _, svcStatus := range resp.Msg.Services {
				if svcStatus.Name == req.Msg.ServiceName {
					foundEntry = entry
					foundService = &meshv1.ServiceInfo{
						Name:           svcStatus.Name,
						Port:           svcStatus.Port,
						HealthEndpoint: svcStatus.HealthEndpoint,
						ServiceType:    svcStatus.ServiceType,
						Labels:         svcStatus.Labels,
					}
					break
				}
			}

			if foundEntry != nil {
				break
			}
		}

		if foundEntry == nil {
			return connect.NewResponse(&debugpb.AttachUprobeResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to find agent for service %s: service not found", req.Msg.ServiceName),
			}), nil
		}

		req.Msg.AgentId = foundEntry.AgentID

		// Attempt to resolve SDK address from labels if not provided
		if req.Msg.SdkAddr == "" {
			if addr, ok := foundService.Labels["coral.sdk.addr"]; ok {
				req.Msg.SdkAddr = addr
			}
		}
	}

	if req.Msg.AgentId == "" {
		return connect.NewResponse(&debugpb.AttachUprobeResponse{
			Success: false,
			Error:   "agent_id is required (could not resolve from service)",
		}), nil
	}

	if req.Msg.SdkAddr == "" {
		return connect.NewResponse(&debugpb.AttachUprobeResponse{
			Success: false,
			Error:   "sdk_addr is required (could not resolve from service labels)",
		}), nil
	}

	// Generate session ID
	sessionID := uuid.New().String()

	// Calculate expiration
	duration := req.Msg.Duration
	if duration == nil || duration.AsDuration() > 10*time.Minute {
		duration = durationpb.New(60 * time.Second) // Default: 60s, Max: 10min
	}
	expiresAt := time.Now().Add(duration.AsDuration())

	// Get agent entry from registry
	entry, err := o.registry.Get(req.Msg.AgentId)
	if err != nil {
		o.logger.Error().Err(err).
			Str("agent_id", req.Msg.AgentId).
			Msg("Failed to get agent from registry")
		return connect.NewResponse(&debugpb.AttachUprobeResponse{
			Success: false,
			Error:   fmt.Sprintf("agent not found: %v", err),
		}), nil
	}

	// Call agent to start uprobe collector
	// Call agent to start uprobe collector
	// Call agent to start uprobe collector
	agentAddr := buildAgentAddress(entry.MeshIPv4)
	agentClient := o.clientFactory(
		http.DefaultClient,
		fmt.Sprintf("http://%s", agentAddr),
	)

	startReq := connect.NewRequest(&meshv1.StartUprobeCollectorRequest{
		AgentId:      req.Msg.AgentId,
		ServiceName:  req.Msg.ServiceName,
		FunctionName: req.Msg.FunctionName,
		Duration:     duration,
		Config:       req.Msg.Config,
		SdkAddr:      req.Msg.SdkAddr,
	})

	startResp, err := agentClient.StartUprobeCollector(ctx, startReq)
	if err != nil {
		o.logger.Error().Err(err).
			Str("agent_id", req.Msg.AgentId).
			Str("function", req.Msg.FunctionName).
			Msg("Failed to start uprobe collector on agent")
		return connect.NewResponse(&debugpb.AttachUprobeResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to start uprobe collector: %v", err),
		}), nil
	}

	if !startResp.Msg.Supported || startResp.Msg.Error != "" {
		return connect.NewResponse(&debugpb.AttachUprobeResponse{
			Success: false,
			Error:   fmt.Sprintf("agent failed to start collector: %s", startResp.Msg.Error),
		}), nil
	}

	// Create session
	session := &database.DebugSession{
		SessionID:    sessionID,
		CollectorID:  startResp.Msg.CollectorId,
		ServiceName:  req.Msg.ServiceName,
		FunctionName: req.Msg.FunctionName,
		AgentID:      req.Msg.AgentId,
		SDKAddr:      req.Msg.SdkAddr,
		StartedAt:    time.Now(),
		ExpiresAt:    expiresAt,
		Status:       "active",
	}

	// Insert session into database
	if err := o.db.InsertDebugSession(session); err != nil {
		o.logger.Error().Err(err).
			Str("session_id", sessionID).
			Msg("Failed to insert debug session into database")
		return connect.NewResponse(&debugpb.AttachUprobeResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to store session: %v", err),
		}), nil
	}

	o.logger.Info().
		Str("session_id", sessionID).
		Str("function", req.Msg.FunctionName).
		Time("expires_at", expiresAt).
		Msg("Debug session created")

	return connect.NewResponse(&debugpb.AttachUprobeResponse{
		SessionId: sessionID,
		ExpiresAt: timestamppb.New(expiresAt),
		Success:   true,
	}), nil
}

// DetachUprobe stops a debug session.
func (o *Orchestrator) DetachUprobe(
	ctx context.Context,
	req *connect.Request[debugpb.DetachUprobeRequest],
) (*connect.Response[debugpb.DetachUprobeResponse], error) {
	o.logger.Info().
		Str("session_id", req.Msg.SessionId).
		Msg("Detaching uprobe")

	// Query session from database
	session, err := o.db.GetDebugSession(req.Msg.SessionId)
	if err != nil {
		o.logger.Error().Err(err).
			Str("session_id", req.Msg.SessionId).
			Msg("Failed to query debug session from database")
		return connect.NewResponse(&debugpb.DetachUprobeResponse{
			Success: false,
			Error:   fmt.Sprintf("database error: %v", err),
		}), nil
	}
	if session == nil {
		return connect.NewResponse(&debugpb.DetachUprobeResponse{
			Success: false,
			Error:   fmt.Sprintf("session not found: %s", req.Msg.SessionId),
		}), nil
	}

	// Get agent entry from registry
	entry, err := o.registry.Get(session.AgentID)
	if err != nil {
		o.logger.Error().Err(err).
			Str("session_id", req.Msg.SessionId).
			Str("agent_id", session.AgentID).
			Msg("Failed to get agent from registry")
		return connect.NewResponse(&debugpb.DetachUprobeResponse{
			Success: false,
			Error:   fmt.Sprintf("agent not found: %v", err),
		}), nil
	}

	// Call agent to stop uprobe collector
	// Call agent to stop uprobe collector
	agentAddr := buildAgentAddress(entry.MeshIPv4)
	agentClient := o.clientFactory(
		http.DefaultClient,
		fmt.Sprintf("http://%s", agentAddr),
	)

	stopReq := connect.NewRequest(&meshv1.StopUprobeCollectorRequest{
		CollectorId: session.CollectorID,
	})

	stopResp, err := agentClient.StopUprobeCollector(ctx, stopReq)
	if err != nil {
		o.logger.Error().Err(err).
			Str("session_id", req.Msg.SessionId).
			Str("collector_id", session.CollectorID).
			Msg("Failed to stop uprobe collector on agent")
		return connect.NewResponse(&debugpb.DetachUprobeResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to stop uprobe collector: %v", err),
		}), nil
	}

	if !stopResp.Msg.Success {
		return connect.NewResponse(&debugpb.DetachUprobeResponse{
			Success: false,
			Error:   fmt.Sprintf("agent failed to stop collector: %s", stopResp.Msg.Error),
		}), nil
	}

	// Update session status in database
	if err := o.db.UpdateDebugSessionStatus(req.Msg.SessionId, "stopped"); err != nil {
		o.logger.Error().Err(err).
			Str("session_id", req.Msg.SessionId).
			Msg("Failed to update session status in database")
		// Don't fail the operation if database update fails
	}

	o.logger.Info().
		Str("session_id", req.Msg.SessionId).
		Msg("Debug session detached")

	return connect.NewResponse(&debugpb.DetachUprobeResponse{
		Success: true,
	}), nil
}

// QueryUprobeEvents retrieves events from a debug session.
func (o *Orchestrator) QueryUprobeEvents(
	ctx context.Context,
	req *connect.Request[debugpb.QueryUprobeEventsRequest],
) (*connect.Response[debugpb.QueryUprobeEventsResponse], error) {
	o.logger.Debug().
		Str("session_id", req.Msg.SessionId).
		Msg("Querying uprobe events")

	// Query session from database
	session, err := o.db.GetDebugSession(req.Msg.SessionId)
	if err != nil {
		o.logger.Error().Err(err).
			Str("session_id", req.Msg.SessionId).
			Msg("Failed to query debug session from database")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}
	if session == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("session not found: %s", req.Msg.SessionId))
	}

	// Check if session has expired
	if time.Now().After(session.ExpiresAt) {
		o.logger.Warn().
			Str("session_id", req.Msg.SessionId).
			Time("expired_at", session.ExpiresAt).
			Msg("Attempted to query events from expired session")
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("session expired at %s (events are no longer available)", session.ExpiresAt.Format(time.RFC3339)))
	}

	// Get agent entry from registry
	entry, err := o.registry.Get(session.AgentID)
	if err != nil {
		o.logger.Error().Err(err).
			Str("session_id", req.Msg.SessionId).
			Str("agent_id", session.AgentID).
			Msg("Failed to get agent from registry")
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("agent not found: %v", err))
	}

	// Call agent to query events
	// Call agent to query events
	agentAddr := buildAgentAddress(entry.MeshIPv4)
	agentClient := o.clientFactory(
		http.DefaultClient,
		fmt.Sprintf("http://%s", agentAddr),
	)

	queryReq := connect.NewRequest(&meshv1.QueryUprobeEventsRequest{
		CollectorId: session.CollectorID,
		StartTime:   req.Msg.StartTime,
		EndTime:     req.Msg.EndTime,
		MaxEvents:   req.Msg.MaxEvents,
	})

	queryResp, err := agentClient.QueryUprobeEvents(ctx, queryReq)
	if err != nil {
		o.logger.Error().Err(err).
			Str("session_id", req.Msg.SessionId).
			Str("collector_id", session.CollectorID).
			Msg("Failed to query uprobe events from agent")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to query events: %v", err))
	}

	o.logger.Debug().
		Str("session_id", req.Msg.SessionId).
		Str("agent_id", session.AgentID).
		Int("event_count", len(queryResp.Msg.Events)).
		Msg("Retrieved uprobe events from agent")

	// Extract UprobeEvent from EbpfEvent wrapper
	var uprobeEvents []*meshv1.UprobeEvent
	for _, ebpfEvent := range queryResp.Msg.Events {
		if ebpfEvent.GetUprobeEvent() != nil {
			uprobeEvents = append(uprobeEvents, ebpfEvent.GetUprobeEvent())
		}
	}

	return connect.NewResponse(&debugpb.QueryUprobeEventsResponse{
		Events:  uprobeEvents,
		HasMore: queryResp.Msg.HasMore,
	}), nil
}

// ListDebugSessions lists all active debug sessions.
func (o *Orchestrator) ListDebugSessions(
	ctx context.Context,
	req *connect.Request[debugpb.ListDebugSessionsRequest],
) (*connect.Response[debugpb.ListDebugSessionsResponse], error) {
	o.logger.Debug().Msg("Listing debug sessions")

	// List sessions from database
	filters := database.DebugSessionFilters{
		ServiceName: req.Msg.ServiceName,
		Status:      req.Msg.Status,
	}

	dbSessions, err := o.db.ListDebugSessions(filters)
	if err != nil {
		o.logger.Error().Err(err).Msg("Failed to list debug sessions from database")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	var sessions []*debugpb.DebugSession
	for _, session := range dbSessions {
		sessions = append(sessions, &debugpb.DebugSession{
			SessionId:    session.SessionID,
			ServiceName:  session.ServiceName,
			FunctionName: session.FunctionName,
			AgentId:      session.AgentID,
			StartedAt:    timestamppb.New(session.StartedAt),
			ExpiresAt:    timestamppb.New(session.ExpiresAt),
			Status:       session.Status,
		})
	}

	return connect.NewResponse(&debugpb.ListDebugSessionsResponse{
		Sessions: sessions,
	}), nil
}

// TraceRequestPath initiates a trace for a specific request path.
func (o *Orchestrator) TraceRequestPath(
	ctx context.Context,
	req *connect.Request[debugpb.TraceRequestPathRequest],
) (*connect.Response[debugpb.TraceRequestPathResponse], error) {
	o.logger.Info().
		Str("service", req.Msg.ServiceName).
		Str("path", req.Msg.Path).
		Msg("TraceRequestPath not yet implemented")

	return connect.NewResponse(&debugpb.TraceRequestPathResponse{
		Success: false,
		Error:   "TraceRequestPath not yet implemented",
	}), nil
}

// GetDebugResults retrieves aggregated debug results.
func (o *Orchestrator) GetDebugResults(
	ctx context.Context,
	req *connect.Request[debugpb.GetDebugResultsRequest],
) (*connect.Response[debugpb.GetDebugResultsResponse], error) {
	o.logger.Info().
		Str("session_id", req.Msg.SessionId).
		Str("format", req.Msg.Format).
		Msg("Getting debug results")

	// Query session from database
	session, err := o.db.GetDebugSession(req.Msg.SessionId)
	if err != nil {
		o.logger.Error().Err(err).
			Str("session_id", req.Msg.SessionId).
			Msg("Failed to query debug session from database")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}
	if session == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("session not found: %s", req.Msg.SessionId))
	}

	// Get agent entry from registry
	entry, err := o.registry.Get(session.AgentID)
	if err != nil {
		o.logger.Error().Err(err).
			Str("session_id", req.Msg.SessionId).
			Str("agent_id", session.AgentID).
			Msg("Failed to get agent from registry")
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("agent not found: %v", err))
	}

	// Call agent to query uprobe events
	// Call agent to query uprobe events
	agentAddr := buildAgentAddress(entry.MeshIPv4)
	agentClient := o.clientFactory(
		http.DefaultClient,
		fmt.Sprintf("http://%s", agentAddr),
	)

	queryReq := connect.NewRequest(&meshv1.QueryUprobeEventsRequest{
		CollectorId: session.CollectorID,
		StartTime:   timestamppb.New(session.StartedAt),
		EndTime:     timestamppb.New(session.ExpiresAt),
		MaxEvents:   10000, // Limit to prevent overwhelming response
	})

	queryResp, err := agentClient.QueryUprobeEvents(ctx, queryReq)
	if err != nil {
		o.logger.Error().Err(err).
			Str("session_id", req.Msg.SessionId).
			Str("collector_id", session.CollectorID).
			Msg("Failed to query uprobe events from agent")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to query events: %v", err))
	}

	// Extract UprobeEvent from EbpfEvent wrapper
	var uprobeEvents []*meshv1.UprobeEvent
	for _, ebpfEvent := range queryResp.Msg.Events {
		if ebpfEvent.GetUprobeEvent() != nil {
			uprobeEvents = append(uprobeEvents, ebpfEvent.GetUprobeEvent())
		}
	}

	o.logger.Info().
		Str("session_id", req.Msg.SessionId).
		Int("event_count", len(uprobeEvents)).
		Msg("Retrieved uprobe events from agent")

	// Aggregate statistics
	statistics := AggregateStatistics(uprobeEvents)

	// Find slow outliers
	p95Duration := time.Duration(0)
	if statistics.DurationP95 != nil {
		p95Duration = statistics.DurationP95.AsDuration()
	}
	slowOutliers := FindSlowOutliers(uprobeEvents, p95Duration)

	// Build call tree
	callTree := BuildCallTreeFromEvents(uprobeEvents, p95Duration)

	// Calculate session duration
	sessionDuration := session.ExpiresAt.Sub(session.StartedAt)

	return connect.NewResponse(&debugpb.GetDebugResultsResponse{
		SessionId:    req.Msg.SessionId,
		Function:     session.FunctionName,
		Duration:     durationpb.New(sessionDuration),
		Statistics:   statistics,
		SlowOutliers: slowOutliers,
		CallTree:     callTree,
	}), nil
}

// buildAgentAddress constructs the agent address from the mesh IP.
func buildAgentAddress(meshIP string) string {
	return net.JoinHostPort(meshIP, fmt.Sprintf("%d", constants.DefaultAgentPort))
}
