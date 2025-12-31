// Package debug provides debug session orchestration for the colony.
package debug

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/coral/mesh/v1/meshv1connect"

	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/registry"
)

// SessionManager manages the lifecycle of debug sessions.
type SessionManager struct {
	logger           zerolog.Logger
	registry         *registry.Registry
	db               *database.Database
	agentCoordinator *AgentCoordinator
	clientFactory    func(connect.HTTPClient, string, ...connect.ClientOption) meshv1connect.DebugServiceClient
}

// NewSessionManager creates a new session manager.
func NewSessionManager(
	logger zerolog.Logger,
	registry *registry.Registry,
	db *database.Database,
	agentCoordinator *AgentCoordinator,
	clientFactory func(connect.HTTPClient, string, ...connect.ClientOption) meshv1connect.DebugServiceClient,
) *SessionManager {
	return &SessionManager{
		logger:           logger.With().Str("component", "session_manager").Logger(),
		registry:         registry,
		db:               db,
		agentCoordinator: agentCoordinator,
		clientFactory:    clientFactory,
	}
}

// AttachUprobe starts a new debug session by attaching a uprobe to a function.
func (sm *SessionManager) AttachUprobe(
	ctx context.Context,
	req *connect.Request[debugpb.AttachUprobeRequest],
) (*connect.Response[debugpb.AttachUprobeResponse], error) {
	sm.logger.Info().
		Str("service", req.Msg.ServiceName).
		Str("function", req.Msg.FunctionName).
		Msg("Attaching uprobe")

	// Service Discovery (RFD 062).
	if req.Msg.AgentId == "" {
		agentID, err := sm.agentCoordinator.FindAgentForService(ctx, req.Msg.ServiceName)
		if err != nil {
			return connect.NewResponse(&debugpb.AttachUprobeResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to find agent for service %s: %v", req.Msg.ServiceName, err),
			}), nil
		}
		req.Msg.AgentId = agentID
	}

	if req.Msg.AgentId == "" {
		return connect.NewResponse(&debugpb.AttachUprobeResponse{
			Success: false,
			Error:   "agent_id is required (could not resolve from service)",
		}), nil
	}

	// Generate session ID.
	sessionID := uuid.New().String()

	// Calculate expiration.
	duration := req.Msg.Duration
	if duration == nil || duration.AsDuration() > 10*time.Minute {
		duration = durationpb.New(60 * time.Second) // Default: 60s, Max: 10min
	}
	expiresAt := time.Now().Add(duration.AsDuration())

	// Get agent entry from registry.
	entry, err := sm.registry.Get(req.Msg.AgentId)
	if err != nil {
		sm.logger.Error().Err(err).
			Str("agent_id", req.Msg.AgentId).
			Msg("Failed to get agent from registry")
		return connect.NewResponse(&debugpb.AttachUprobeResponse{
			Success: false,
			Error:   fmt.Sprintf("agent not found: %v", err),
		}), nil
	}

	// Call agent to start uprobe collector.
	agentAddr := buildAgentAddress(entry.MeshIPv4)
	agentClient := sm.clientFactory(
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
		sm.logger.Error().Err(err).
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

	// Create session.
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

	// Insert session into database.
	if err := sm.db.InsertDebugSession(ctx, session); err != nil {
		sm.logger.Error().Err(err).
			Str("session_id", sessionID).
			Msg("Failed to insert debug session into database")
		return connect.NewResponse(&debugpb.AttachUprobeResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to store session: %v", err),
		}), nil
	}

	sm.logger.Info().
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
func (sm *SessionManager) DetachUprobe(
	ctx context.Context,
	req *connect.Request[debugpb.DetachUprobeRequest],
) (*connect.Response[debugpb.DetachUprobeResponse], error) {
	sm.logger.Info().
		Str("session_id", req.Msg.SessionId).
		Msg("Detaching uprobe")

	// Query session from database.
	session, err := sm.db.GetDebugSession(ctx, req.Msg.SessionId)
	if err != nil {
		sm.logger.Error().Err(err).
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

	// Get agent entry from registry.
	entry, err := sm.registry.Get(session.AgentID)
	agentAvailable := err == nil

	if !agentAvailable {
		sm.logger.Warn().Err(err).
			Str("session_id", req.Msg.SessionId).
			Str("agent_id", session.AgentID).
			Msg("Agent not in registry - will mark session as stopped without contacting agent")
	}

	// Try to fetch and persist events if agent is available.
	if agentAvailable {
		// Setup agent client.
		agentAddr := buildAgentAddress(entry.MeshIPv4)
		agentClient := sm.clientFactory(
			http.DefaultClient,
			fmt.Sprintf("http://%s", agentAddr),
		)

		// Fetch and persist events before stopping collector (RFD 062 - event persistence).
		queryReq := connect.NewRequest(&meshv1.QueryUprobeEventsRequest{
			CollectorId: session.CollectorID,
			StartTime:   timestamppb.New(session.StartedAt),
			EndTime:     timestamppb.New(time.Now()),
			MaxEvents:   100000, // Fetch all events
		})

		queryResp, err := agentClient.QueryUprobeEvents(ctx, queryReq)
		if err != nil {
			sm.logger.Warn().Err(err).
				Str("session_id", req.Msg.SessionId).
				Str("collector_id", session.CollectorID).
				Msg("Failed to fetch events before detaching (continuing with detach)")
			// Continue with detach even if event fetch fails
		} else {
			// Extract UprobeEvent from EbpfEvent wrapper.
			var uprobeEvents []*meshv1.UprobeEvent
			for _, ebpfEvent := range queryResp.Msg.Events {
				if ebpfEvent.GetUprobeEvent() != nil {
					uprobeEvents = append(uprobeEvents, ebpfEvent.GetUprobeEvent())
				}
			}

			// Persist events to database.
			if len(uprobeEvents) > 0 {
				if err := sm.db.InsertDebugEvents(ctx, req.Msg.SessionId, uprobeEvents); err != nil {
					sm.logger.Error().Err(err).
						Str("session_id", req.Msg.SessionId).
						Int("event_count", len(uprobeEvents)).
						Msg("Failed to persist debug events (continuing with detach)")
				} else {
					sm.logger.Info().
						Str("session_id", req.Msg.SessionId).
						Int("event_count", len(uprobeEvents)).
						Msg("Persisted debug events to database")
				}
			} else {
				sm.logger.Debug().
					Str("session_id", req.Msg.SessionId).
					Msg("No events to persist from collector")
			}
		}

		// Call agent to stop uprobe collector.
		stopReq := connect.NewRequest(&meshv1.StopUprobeCollectorRequest{
			CollectorId: session.CollectorID,
		})

		stopResp, err := agentClient.StopUprobeCollector(ctx, stopReq)
		if err != nil {
			sm.logger.Warn().Err(err).
				Str("session_id", req.Msg.SessionId).
				Str("collector_id", session.CollectorID).
				Msg("Failed to stop uprobe collector on agent (will mark session as stopped anyway)")
			// Continue to mark session as stopped even if agent call fails
		} else if !stopResp.Msg.Success {
			sm.logger.Warn().
				Str("session_id", req.Msg.SessionId).
				Str("collector_id", session.CollectorID).
				Str("error", stopResp.Msg.Error).
				Msg("Agent reported failure stopping uprobe collector (will mark session as stopped anyway)")
		}
	}

	// Update session status in database.
	if err := sm.db.UpdateDebugSessionStatus(ctx, req.Msg.SessionId, "stopped"); err != nil {
		sm.logger.Error().Err(err).
			Str("session_id", req.Msg.SessionId).
			Msg("Failed to update session status in database")
		// Don't fail the operation if database update fails
	}

	sm.logger.Info().
		Str("session_id", req.Msg.SessionId).
		Msg("Debug session detached")

	return connect.NewResponse(&debugpb.DetachUprobeResponse{
		Success: true,
	}), nil
}

// ListDebugSessions lists all active debug sessions.
func (sm *SessionManager) ListDebugSessions(
	ctx context.Context,
	req *connect.Request[debugpb.ListDebugSessionsRequest],
) (*connect.Response[debugpb.ListDebugSessionsResponse], error) {
	sm.logger.Debug().Msg("Listing debug sessions")

	// List sessions from database.
	filters := database.DebugSessionFilters{
		ServiceName: req.Msg.ServiceName,
		Status:      req.Msg.Status,
	}

	dbSessions, err := sm.db.ListDebugSessions(filters)
	if err != nil {
		sm.logger.Error().Err(err).Msg("Failed to list debug sessions from database")
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
