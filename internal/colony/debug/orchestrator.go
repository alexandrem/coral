// Package debug provides debug session orchestration for the colony.
package debug

import (
	"context"
	"fmt"
	"time"

	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Orchestrator manages debug sessions across agents.
type Orchestrator struct {
	logger   zerolog.Logger
	sessions map[string]*DebugSession // In-memory for Phase 3 MVP
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
func NewOrchestrator(logger zerolog.Logger) *Orchestrator {
	return &Orchestrator{
		logger:   logger.With().Str("component", "debug_orchestrator").Logger(),
		sessions: make(map[string]*DebugSession),
	}
}

// AttachUprobe starts a new debug session by attaching a uprobe to a function.
func (o *Orchestrator) AttachUprobe(
	ctx context.Context,
	req *debugpb.AttachUprobeRequest,
) (*debugpb.AttachUprobeResponse, error) {
	o.logger.Info().
		Str("service", req.ServiceName).
		Str("function", req.FunctionName).
		Msg("Attaching uprobe")

	// TODO Phase 3 Production:
	// - Query service registry for agent ID and SDK address
	// - Validate service has SDK enabled
	// For MVP, we require these in the request

	if req.AgentId == "" {
		return &debugpb.AttachUprobeResponse{
			Success: false,
			Error:   "agent_id is required (service discovery not yet implemented)",
		}, nil
	}

	if req.SdkAddr == "" {
		return &debugpb.AttachUprobeResponse{
			Success: false,
			Error:   "sdk_addr is required (service discovery not yet implemented)",
		}, nil
	}

	// Generate session ID
	sessionID := uuid.New().String()

	// Calculate expiration
	duration := req.Duration
	if duration == nil || duration.AsDuration() > 10*time.Minute {
		duration = durationpb.New(60 * time.Second) // Default: 60s, Max: 10min
	}
	expiresAt := time.Now().Add(duration.AsDuration())

	// TODO Phase 3 Production:
	// - Get agent client from agent registry
	// - Send RPC to agent
	// For MVP, we'll just create a session record

	// Create session
	session := &DebugSession{
		SessionID:    sessionID,
		CollectorID:  "", // Would be set by agent response
		ServiceName:  req.ServiceName,
		FunctionName: req.FunctionName,
		AgentID:      req.AgentId,
		SDKAddr:      req.SdkAddr,
		StartedAt:    time.Now(),
		ExpiresAt:    expiresAt,
		Status:       "pending", // Would be "active" after agent confirms
	}

	o.sessions[sessionID] = session

	o.logger.Info().
		Str("session_id", sessionID).
		Str("function", req.FunctionName).
		Time("expires_at", expiresAt).
		Msg("Debug session created")

	return &debugpb.AttachUprobeResponse{
		SessionId: sessionID,
		ExpiresAt: timestamppb.New(expiresAt),
		Success:   true,
	}, nil
}

// DetachUprobe stops a debug session.
func (o *Orchestrator) DetachUprobe(
	ctx context.Context,
	req *debugpb.DetachUprobeRequest,
) (*debugpb.DetachUprobeResponse, error) {
	o.logger.Info().
		Str("session_id", req.SessionId).
		Msg("Detaching uprobe")

	session, ok := o.sessions[req.SessionId]
	if !ok {
		return &debugpb.DetachUprobeResponse{
			Success: false,
			Error:   fmt.Sprintf("session not found: %s", req.SessionId),
		}, nil
	}

	// TODO Phase 3 Production:
	// - Get agent client
	// - Send StopUprobeCollector RPC to agent
	// For MVP, just mark as detached

	session.Status = "detached"

	o.logger.Info().
		Str("session_id", req.SessionId).
		Msg("Debug session detached")

	return &debugpb.DetachUprobeResponse{
		Success: true,
	}, nil
}

// QueryUprobeEvents retrieves events from a debug session.
func (o *Orchestrator) QueryUprobeEvents(
	ctx context.Context,
	req *debugpb.QueryUprobeEventsRequest,
) (*debugpb.QueryUprobeEventsResponse, error) {
	o.logger.Debug().
		Str("session_id", req.SessionId).
		Msg("Querying uprobe events")

	session, ok := o.sessions[req.SessionId]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", req.SessionId)
	}

	// TODO Phase 3 Production:
	// - Get agent client for session.AgentID
	// - Send QueryUprobeEvents RPC to agent
	// - Return events from agent
	// For MVP, return empty events

	o.logger.Debug().
		Str("session_id", req.SessionId).
		Str("agent_id", session.AgentID).
		Msg("Would query agent for events (not yet implemented)")

	return &debugpb.QueryUprobeEventsResponse{
		Events:  []*meshv1.UprobeEvent{},
		HasMore: false,
	}, nil
}

// ListDebugSessions lists all active debug sessions.
func (o *Orchestrator) ListDebugSessions(
	ctx context.Context,
	req *debugpb.ListDebugSessionsRequest,
) (*debugpb.ListDebugSessionsResponse, error) {
	o.logger.Debug().Msg("Listing debug sessions")

	var sessions []*debugpb.DebugSession
	for _, session := range o.sessions {
		// Filter by status if requested
		if req.Status != "" && session.Status != req.Status {
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

	return &debugpb.ListDebugSessionsResponse{
		Sessions: sessions,
	}, nil
}
