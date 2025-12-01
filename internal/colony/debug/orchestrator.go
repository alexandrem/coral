// Package debug provides debug session orchestration for the colony.
package debug

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"
	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/coral-mesh/coral/internal/colony/registry"

	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/colony"
)

// Orchestrator manages debug sessions across agents.
type Orchestrator struct {
	logger   zerolog.Logger
	registry *registry.Registry
	sessions map[string]*DebugSession
}

// DebugSession represents an active debug session.
type DebugSession struct {
	SessionID    string
	CollectorID  string
	ServiceName  string
	FunctionName string
	AgentID      string
	SDKAddr      string
	StartedAt    time.Time
	ExpiresAt    time.Time
	Status       string
}

// NewOrchestrator creates a new debug orchestrator.
func NewOrchestrator(logger zerolog.Logger, registry *registry.Registry) *Orchestrator {
	return &Orchestrator{
		logger:   logger.With().Str("component", "debug_orchestrator").Logger(),
		registry: registry,
		sessions: make(map[string]*DebugSession),
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

	// TODO Phase 3 Production:
	// - Query service registry for agent ID and SDK address
	// - Validate service has SDK enabled
	// For MVP, we require these in the request

	// Service Discovery (RFD 062)
	if req.Msg.AgentId == "" {
		// Lookup agent by service name
		entry, serviceInfo, err := o.registry.FindAgentForService(req.Msg.ServiceName)
		if err != nil {
			return connect.NewResponse(&debugpb.AttachUprobeResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to find agent for service %s: %v", req.Msg.ServiceName, err),
			}), nil
		}
		req.Msg.AgentId = entry.AgentID

		// Attempt to resolve SDK address from labels if not provided
		if req.Msg.SdkAddr == "" {
			if addr, ok := serviceInfo.Labels["coral.sdk.addr"]; ok {
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

	// Call agent to start uprobe collector
	agentClient := colony.GetAgentClient(entry)

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

	if !startResp.Msg.Success {
		return connect.NewResponse(&debugpb.AttachUprobeResponse{
			Success: false,
			Error:   fmt.Sprintf("agent failed to start collector: %s", startResp.Msg.Error),
		}), nil
	}

	// Create session
	session := &DebugSession{
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

	o.sessions[sessionID] = session

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

	session, ok := o.sessions[req.Msg.SessionId]
	if !ok {
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
	agentClient := colony.GetAgentClient(entry)

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

	// Update session status
	session.Status = "stopped"

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

	session, ok := o.sessions[req.Msg.SessionId]
	if !ok {
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
	agentClient := colony.GetAgentClient(entry)

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

	return connect.NewResponse(&debugpb.QueryUprobeEventsResponse{
		Events:  queryResp.Msg.Events,
		HasMore: queryResp.Msg.HasMore,
	}), nil
}

// ListDebugSessions lists all active debug sessions.
func (o *Orchestrator) ListDebugSessions(
	ctx context.Context,
	req *connect.Request[debugpb.ListDebugSessionsRequest],
) (*connect.Response[debugpb.ListDebugSessionsResponse], error) {
	o.logger.Debug().Msg("Listing debug sessions")

	var sessions []*debugpb.DebugSession
	for _, session := range o.sessions {
		// Filter by status if requested
		if req.Msg.Status != "" && session.Status != req.Msg.Status {
			continue
		}

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
		Msg("GetDebugResults not yet implemented")

	return connect.NewResponse(&debugpb.GetDebugResultsResponse{
		SessionId: req.Msg.SessionId,
		// Statistics and outliers would be populated here
	}), nil
}
