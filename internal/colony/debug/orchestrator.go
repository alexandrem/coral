// Package debug provides debug session orchestration for the colony.
package debug

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
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

	"github.com/coral-mesh/coral/internal/colony"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/registry"
	"github.com/coral-mesh/coral/internal/constants"
)

// Orchestrator manages debug sessions across agents.
type Orchestrator struct {
	logger                  zerolog.Logger
	registry                *registry.Registry
	db                      *database.Database
	functionRegistry        *colony.FunctionRegistry
	clientFactory           func(connect.HTTPClient, string, ...connect.ClientOption) meshv1connect.DebugServiceClient
	agentClientFactory      func(connect.HTTPClient, string, ...connect.ClientOption) agentv1connect.AgentServiceClient
	stopBackgroundPersist   chan struct{}
	timestampsMu            sync.RWMutex
	lastPersistedTimestamps map[string]time.Time // sessionID -> last persisted event timestamp
}

// NewOrchestrator creates a new debug orchestrator.
func NewOrchestrator(logger zerolog.Logger, registry *registry.Registry, db *database.Database, functionRegistry *colony.FunctionRegistry) *Orchestrator {
	o := &Orchestrator{
		logger:                  logger.With().Str("component", "debug_orchestrator").Logger(),
		registry:                registry,
		db:                      db,
		functionRegistry:        functionRegistry,
		clientFactory:           meshv1connect.NewDebugServiceClient,
		agentClientFactory:      agentv1connect.NewAgentServiceClient,
		stopBackgroundPersist:   make(chan struct{}),
		lastPersistedTimestamps: make(map[string]time.Time),
	}

	// Start background event persistence for all sessions
	go o.runBackgroundEventPersistence()

	return o
}

// runBackgroundEventPersistence continuously persists events from all active sessions.
// This ensures events are always in the database, even if DetachUprobe is never called.
func (o *Orchestrator) runBackgroundEventPersistence() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	o.logger.Info().Msg("Started background event persistence for all debug sessions")

	for {
		select {
		case <-ticker.C:
			o.persistEventsFromActiveSessions()
		case <-o.stopBackgroundPersist:
			o.logger.Info().Msg("Stopped background event persistence")
			return
		}
	}
}

// persistEventsFromActiveSessions queries and persists events from all active sessions.
func (o *Orchestrator) persistEventsFromActiveSessions() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Get all active sessions from database
	sessions, err := o.db.ListDebugSessions(database.DebugSessionFilters{
		Status: "active",
	})
	if err != nil {
		o.logger.Error().Err(err).Msg("Failed to list active sessions for background persistence")
		return
	}

	if len(sessions) == 0 {
		return
	}

	o.logger.Debug().
		Int("session_count", len(sessions)).
		Msg("Persisting events from active sessions")

	persistedCount := 0
	for _, session := range sessions {
		// Skip expired sessions
		if time.Now().After(session.ExpiresAt) {
			continue
		}

		// Query new events since last persistence
		o.timestampsMu.RLock()
		lastTime := o.lastPersistedTimestamps[session.SessionID]
		o.timestampsMu.RUnlock()

		queryReq := connect.NewRequest(&debugpb.QueryUprobeEventsRequest{
			SessionId: session.SessionID,
			StartTime: timestamppb.New(lastTime),
			MaxEvents: 10000,
		})

		queryResp, err := o.QueryUprobeEvents(ctx, queryReq)
		if err != nil {
			o.logger.Debug().
				Err(err).
				Str("session_id", session.SessionID).
				Msg("Failed to query events for background persistence")
			continue
		}

		if len(queryResp.Msg.Events) > 0 {
			// Persist new events to database
			if err := o.db.InsertDebugEvents(session.SessionID, queryResp.Msg.Events); err != nil {
				o.logger.Error().
					Err(err).
					Str("session_id", session.SessionID).
					Int("event_count", len(queryResp.Msg.Events)).
					Msg("Failed to persist events in background")
			} else {
				persistedCount += len(queryResp.Msg.Events)
				// Update last persisted timestamp
				lastEvent := queryResp.Msg.Events[len(queryResp.Msg.Events)-1]
				o.timestampsMu.Lock()
				o.lastPersistedTimestamps[session.SessionID] = lastEvent.Timestamp.AsTime()
				o.timestampsMu.Unlock()
			}
		}
	}

	if persistedCount > 0 {
		o.logger.Info().
			Int("event_count", persistedCount).
			Int("session_count", len(sessions)).
			Msg("Background event persistence completed")
	}
}

// Stop gracefully stops the orchestrator's background tasks.
func (o *Orchestrator) Stop() {
	close(o.stopBackgroundPersist)
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

		req.Msg.AgentId = foundEntry.AgentID

	}

	if req.Msg.AgentId == "" {
		return connect.NewResponse(&debugpb.AttachUprobeResponse{
			Success: false,
			Error:   "agent_id is required (could not resolve from service)",
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
	agentAvailable := err == nil

	if !agentAvailable {
		o.logger.Warn().Err(err).
			Str("session_id", req.Msg.SessionId).
			Str("agent_id", session.AgentID).
			Msg("Agent not in registry - will mark session as stopped without contacting agent")
	}

	// Try to fetch and persist events if agent is available
	if agentAvailable {
		// Setup agent client
		agentAddr := buildAgentAddress(entry.MeshIPv4)
		agentClient := o.clientFactory(
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
			o.logger.Warn().Err(err).
				Str("session_id", req.Msg.SessionId).
				Str("collector_id", session.CollectorID).
				Msg("Failed to fetch events before detaching (continuing with detach)")
			// Continue with detach even if event fetch fails
		} else {
			// Extract UprobeEvent from EbpfEvent wrapper
			var uprobeEvents []*meshv1.UprobeEvent
			for _, ebpfEvent := range queryResp.Msg.Events {
				if ebpfEvent.GetUprobeEvent() != nil {
					uprobeEvents = append(uprobeEvents, ebpfEvent.GetUprobeEvent())
				}
			}

			// Persist events to database
			if len(uprobeEvents) > 0 {
				if err := o.db.InsertDebugEvents(req.Msg.SessionId, uprobeEvents); err != nil {
					o.logger.Error().Err(err).
						Str("session_id", req.Msg.SessionId).
						Int("event_count", len(uprobeEvents)).
						Msg("Failed to persist debug events (continuing with detach)")
				} else {
					o.logger.Info().
						Str("session_id", req.Msg.SessionId).
						Int("event_count", len(uprobeEvents)).
						Msg("Persisted debug events to database")
				}
			} else {
				o.logger.Debug().
					Str("session_id", req.Msg.SessionId).
					Msg("No events to persist from collector")
			}
		}

		// Call agent to stop uprobe collector
		stopReq := connect.NewRequest(&meshv1.StopUprobeCollectorRequest{
			CollectorId: session.CollectorID,
		})

		stopResp, err := agentClient.StopUprobeCollector(ctx, stopReq)
		if err != nil {
			o.logger.Warn().Err(err).
				Str("session_id", req.Msg.SessionId).
				Str("collector_id", session.CollectorID).
				Msg("Failed to stop uprobe collector on agent (will mark session as stopped anyway)")
			// Continue to mark session as stopped even if agent call fails
		} else if !stopResp.Msg.Success {
			o.logger.Warn().
				Str("session_id", req.Msg.SessionId).
				Str("collector_id", session.CollectorID).
				Str("error", stopResp.Msg.Error).
				Msg("Agent reported failure stopping uprobe collector (will mark session as stopped anyway)")
		}
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
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("session not found: %s", req.Msg.SessionId))
		}
		o.logger.Error().Err(err).
			Str("session_id", req.Msg.SessionId).
			Msg("Failed to query debug session from database")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	// Determine if we should query from agent or database
	var uprobeEvents []*meshv1.UprobeEvent
	sessionExpired := time.Now().After(session.ExpiresAt) || session.Status == "stopped"

	if sessionExpired {
		// Session expired or stopped - query from database (RFD 062 - event persistence).
		o.logger.Debug().
			Str("session_id", req.Msg.SessionId).
			Msg("Querying events from database (session expired or stopped)")

		events, err := o.db.GetDebugEvents(req.Msg.SessionId)
		if err != nil {
			o.logger.Error().Err(err).
				Str("session_id", req.Msg.SessionId).
				Msg("Failed to query events from database")
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to query events from database: %v", err))
		}

		// Filter events by time range if specified
		for _, event := range events {
			if req.Msg.StartTime != nil && event.Timestamp.AsTime().Before(req.Msg.StartTime.AsTime()) {
				continue
			}
			if req.Msg.EndTime != nil && event.Timestamp.AsTime().After(req.Msg.EndTime.AsTime()) {
				continue
			}
			uprobeEvents = append(uprobeEvents, event)

			// Apply MaxEvents limit
			if req.Msg.MaxEvents > 0 && len(uprobeEvents) >= int(req.Msg.MaxEvents) {
				break
			}
		}

		o.logger.Debug().
			Str("session_id", req.Msg.SessionId).
			Int("event_count", len(uprobeEvents)).
			Msg("Retrieved events from database")
	} else {
		// Session still active - try to query from agent first, fallback to database
		o.logger.Debug().
			Str("session_id", req.Msg.SessionId).
			Msg("Querying events from agent (session active)")

		var agentQueryFailed bool

		entry, err := o.registry.Get(session.AgentID)
		if err != nil {
			o.logger.Warn().Err(err).
				Str("session_id", req.Msg.SessionId).
				Str("agent_id", session.AgentID).
				Msg("Agent not found in registry, will fallback to database")
			agentQueryFailed = true
		}

		if !agentQueryFailed {
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
				o.logger.Warn().Err(err).
					Str("session_id", req.Msg.SessionId).
					Str("collector_id", session.CollectorID).
					Msg("Failed to query uprobe events from agent, will fallback to database")
				agentQueryFailed = true
			} else {
				// Extract UprobeEvent from EbpfEvent wrapper
				for _, ebpfEvent := range queryResp.Msg.Events {
					if ebpfEvent.GetUprobeEvent() != nil {
						uprobeEvents = append(uprobeEvents, ebpfEvent.GetUprobeEvent())
					}
				}

				o.logger.Debug().
					Str("session_id", req.Msg.SessionId).
					Str("agent_id", session.AgentID).
					Int("event_count", len(uprobeEvents)).
					Msg("Retrieved uprobe events from agent")
			}
		}

		// Fallback to database if agent query failed
		if agentQueryFailed {
			o.logger.Debug().
				Str("session_id", req.Msg.SessionId).
				Msg("Falling back to database query for events")

			events, err := o.db.GetDebugEvents(req.Msg.SessionId)
			if err != nil {
				o.logger.Error().Err(err).
					Str("session_id", req.Msg.SessionId).
					Msg("Failed to query events from database")
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to query events from database: %v", err))
			}

			// Filter events by time range if specified
			for _, event := range events {
				if req.Msg.StartTime != nil && event.Timestamp.AsTime().Before(req.Msg.StartTime.AsTime()) {
					continue
				}
				if req.Msg.EndTime != nil && event.Timestamp.AsTime().After(req.Msg.EndTime.AsTime()) {
					continue
				}
				uprobeEvents = append(uprobeEvents, event)

				// Apply MaxEvents limit
				if req.Msg.MaxEvents > 0 && len(uprobeEvents) >= int(req.Msg.MaxEvents) {
					break
				}
			}

			o.logger.Debug().
				Str("session_id", req.Msg.SessionId).
				Int("event_count", len(uprobeEvents)).
				Msg("Retrieved events from database (fallback)")
		}
	}

	return connect.NewResponse(&debugpb.QueryUprobeEventsResponse{
		Events:  uprobeEvents,
		HasMore: false, // Database queries return all events at once
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
		Msg("Starting trace for request path")

	// For now, use simple heuristic to infer function name from path.
	// TODO(RFD 063): Implement automatic function discovery from Beyla traces.
	// /checkout -> main.ProcessCheckout, /api/payment -> main.ProcessPayment
	functionName := inferFunctionNameFromPath(req.Msg.Path)

	o.logger.Info().
		Str("service", req.Msg.ServiceName).
		Str("path", req.Msg.Path).
		Str("function", functionName).
		Msg("Discovered function for path")

	// Start uprobe session on the discovered function.
	attachReq := connect.NewRequest(&debugpb.AttachUprobeRequest{
		ServiceName:  req.Msg.ServiceName,
		FunctionName: functionName,
		Duration:     req.Msg.Duration,
		SdkAddr:      req.Msg.SdkAddr,
		Config: &meshv1.UprobeConfig{
			CaptureArgs:   false, // Don't capture args for traces (too much data)
			CaptureReturn: true,  // Capture return for duration measurement
		},
	})

	attachResp, err := o.AttachUprobe(ctx, attachReq)
	if err != nil {
		o.logger.Error().Err(err).
			Str("service", req.Msg.ServiceName).
			Str("function", functionName).
			Msg("Failed to attach uprobe for trace")
		return connect.NewResponse(&debugpb.TraceRequestPathResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to attach uprobe: %v", err),
		}), nil
	}

	if !attachResp.Msg.Success {
		return connect.NewResponse(&debugpb.TraceRequestPathResponse{
			Success: false,
			Error:   attachResp.Msg.Error,
		}), nil
	}

	return connect.NewResponse(&debugpb.TraceRequestPathResponse{
		SessionId: attachResp.Msg.SessionId,
		Path:      req.Msg.Path,
		Success:   true,
	}), nil
}

// inferFunctionNameFromPath uses simple heuristics to guess a function name from HTTP path.
func inferFunctionNameFromPath(path string) string {
	// Remove leading slash and split by slash.
	path = strings.TrimPrefix(path, "/")
	parts := strings.Split(path, "/")

	if len(parts) == 0 || parts[0] == "" {
		return "main.ProcessRequest"
	}

	// Take the first meaningful path segment.
	segment := parts[0]

	// Remove common prefixes like "api", "v1", etc.
	if segment == "api" && len(parts) > 1 {
		segment = parts[1]
	}

	// Capitalize first letter and add Process prefix.
	// /checkout -> main.ProcessCheckout, /payment -> main.ProcessPayment
	if len(segment) > 0 {
		segment = strings.Title(strings.ToLower(segment))
		return "main.Process" + segment
	}

	return "main.ProcessRequest"
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
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("session not found: %s", req.Msg.SessionId))
		}
		o.logger.Error().Err(err).
			Str("session_id", req.Msg.SessionId).
			Msg("Failed to query debug session from database")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	// Determine if we should query from agent or database
	var uprobeEvents []*meshv1.UprobeEvent
	var processID int32
	var binaryPath string

	// Try to resolve process info from registry if agent is available
	if entry, err := o.registry.Get(session.AgentID); err == nil {
		for _, svc := range entry.Services {
			if svc.Name == session.ServiceName {
				processID = svc.ProcessId
				binaryPath = svc.BinaryPath
				break
			}
		}
	}

	sessionExpired := time.Now().After(session.ExpiresAt) || session.Status == "stopped"

	if sessionExpired {
		// Session expired or stopped - query from database (RFD 062 - event persistence).
		o.logger.Info().
			Str("session_id", req.Msg.SessionId).
			Bool("expired", time.Now().After(session.ExpiresAt)).
			Str("status", session.Status).
			Msg("Querying events from database (session expired or stopped)")

		events, err := o.db.GetDebugEvents(req.Msg.SessionId)
		if err != nil {
			o.logger.Error().Err(err).
				Str("session_id", req.Msg.SessionId).
				Msg("Failed to query events from database")
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to query events from database: %v", err))
		}
		uprobeEvents = events

		o.logger.Info().
			Str("session_id", req.Msg.SessionId).
			Int("event_count", len(uprobeEvents)).
			Msg("Retrieved events from database")
	} else {
		// Session still active - query from agent
		o.logger.Info().
			Str("session_id", req.Msg.SessionId).
			Msg("Querying events from agent (session active)")

		entry, err := o.registry.Get(session.AgentID)
		if err != nil {
			o.logger.Error().Err(err).
				Str("session_id", req.Msg.SessionId).
				Str("agent_id", session.AgentID).
				Msg("Failed to get agent from registry")
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("agent not found: %v", err))
		}

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
		for _, ebpfEvent := range queryResp.Msg.Events {
			if ebpfEvent.GetUprobeEvent() != nil {
				uprobeEvents = append(uprobeEvents, ebpfEvent.GetUprobeEvent())
			}
		}

		o.logger.Info().
			Str("session_id", req.Msg.SessionId).
			Int("event_count", len(uprobeEvents)).
			Msg("Retrieved uprobe events from agent")
	}

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
		ProcessId:    processID,
		BinaryPath:   binaryPath,
	}), nil
}

// buildAgentAddress constructs the agent address from the mesh IP.
func buildAgentAddress(meshIP string) string {
	return net.JoinHostPort(meshIP, fmt.Sprintf("%d", constants.DefaultAgentPort))
}

// QueryFunctions implements function discovery with semantic search (RFD 069).
func (o *Orchestrator) QueryFunctions(
	ctx context.Context,
	req *connect.Request[debugpb.QueryFunctionsRequest],
) (*connect.Response[debugpb.QueryFunctionsResponse], error) {
	o.logger.Debug().
		Str("service", req.Msg.ServiceName).
		Str("query", req.Msg.Query).
		Int32("max_results", req.Msg.MaxResults).
		Msg("Querying functions")

	// Set defaults
	maxResults := int(req.Msg.MaxResults)
	if maxResults <= 0 {
		maxResults = 20 // Default
	}
	if maxResults > 50 {
		maxResults = 50 // Max limit
	}

	// Check if function registry is available
	if o.functionRegistry == nil {
		return nil, connect.NewError(connect.CodeUnimplemented,
			fmt.Errorf("function registry not available"))
	}

	// Query the function registry
	colonyFunctions, err := o.functionRegistry.QueryFunctions(ctx, req.Msg.ServiceName, req.Msg.Query, maxResults)
	if err != nil {
		o.logger.Error().Err(err).
			Str("service", req.Msg.ServiceName).
			Str("query", req.Msg.Query).
			Msg("Failed to query functions")
		return nil, connect.NewError(connect.CodeInternal,
			fmt.Errorf("failed to query functions: %w", err))
	}

	// Query for active debug sessions to check if functions are currently probed
	activeSessions := make(map[string]*database.DebugSession) // functionName -> session
	if req.Msg.ServiceName != "" {
		sessionFilters := database.DebugSessionFilters{
			ServiceName: req.Msg.ServiceName,
			Status:      "active",
		}
		dbSessions, err := o.db.ListDebugSessions(sessionFilters)
		if err != nil {
			o.logger.Warn().Err(err).Msg("Failed to query active sessions for function status")
		} else {
			for _, session := range dbSessions {
				// Only track non-expired sessions
				if time.Now().Before(session.ExpiresAt) {
					activeSessions[session.FunctionName] = session
				}
			}
		}
	}

	// Convert function results to protobuf format
	var results []*debugpb.FunctionResult
	for _, fn := range colonyFunctions {
		// Extract values from sql.Null types
		packageName := ""
		if fn.PackageName.Valid {
			packageName = fn.PackageName.String
		}
		filePath := ""
		if fn.FilePath.Valid {
			filePath = fn.FilePath.String
		}
		lineNumber := int32(0)
		if fn.LineNumber.Valid {
			lineNumber = fn.LineNumber.Int32
		}
		offset := int64(0)
		if fn.Offset.Valid {
			offset = fn.Offset.Int64
		}

		// Check if this function is currently probed
		activeSession, isProbed := activeSessions[fn.FunctionName]

		result := &debugpb.FunctionResult{
			Function: &debugpb.FunctionMetadata{
				Id:      fmt.Sprintf("%s/%s", fn.ServiceName, fn.FunctionName),
				Name:    fn.FunctionName,
				Package: packageName,
				File:    filePath,
				Line:    lineNumber,
				Offset:  fmt.Sprintf("0x%x", offset),
			},
			Search: &debugpb.SearchInfo{
				Score:  1.0, // TODO: Get actual similarity score from registry
				Reason: "Semantic match",
			},
			Instrumentation: &debugpb.InstrumentationInfo{
				IsProbeable:     fn.HasDwarf,
				HasDwarf:        fn.HasDwarf,
				CurrentlyProbed: isProbed,
			},
		}

		// If function is currently probed and metrics are requested, fetch live probe data
		if isProbed && req.Msg.IncludeMetrics {
			resultsReq := connect.NewRequest(&debugpb.GetDebugResultsRequest{
				SessionId: activeSession.SessionID,
				Format:    "summary",
			})
			resultsResp, err := o.GetDebugResults(ctx, resultsReq)
			if err == nil && resultsResp.Msg.Statistics != nil {
				stats := resultsResp.Msg.Statistics
				// Attach live probe metrics
				result.Metrics = &debugpb.FunctionMetrics{
					Source:      "live_probe",
					P50:         stats.DurationP50,
					P95:         stats.DurationP95,
					P99:         stats.DurationP99,
					CallsPerMin: float64(stats.TotalCalls) / resultsResp.Msg.Duration.AsDuration().Minutes(),
					ErrorRate:   0.0, // TODO: Track error rate in probe data
				}
			}
		}

		results = append(results, result)
	}

	// Calculate data coverage (how many results have metrics)
	// TODO: Implement metrics storage and retrieval
	dataCoveragePct := int32(0)

	// Generate suggestion if data coverage is low
	var suggestion string
	if dataCoveragePct < 50 && len(results) > 0 {
		suggestion = fmt.Sprintf("Low data coverage (%d%%). Consider running coral_profile_functions to collect metrics.", dataCoveragePct)
	}

	o.logger.Info().
		Str("service", req.Msg.ServiceName).
		Str("query", req.Msg.Query).
		Int("result_count", len(results)).
		Msg("Function query completed")

	return connect.NewResponse(&debugpb.QueryFunctionsResponse{
		ServiceName:     req.Msg.ServiceName,
		Query:           req.Msg.Query,
		DataCoveragePct: dataCoveragePct,
		Results:         results,
		Suggestion:      suggestion,
	}), nil
}

// ProfileFunctions implements batch profiling with automatic analysis (RFD 069).
func (o *Orchestrator) ProfileFunctions(
	ctx context.Context,
	req *connect.Request[debugpb.ProfileFunctionsRequest],
) (*connect.Response[debugpb.ProfileFunctionsResponse], error) {
	o.logger.Info().
		Str("service", req.Msg.ServiceName).
		Str("query", req.Msg.Query).
		Str("strategy", req.Msg.Strategy).
		Msg("Starting batch profiling")

	// Set defaults
	maxFunctions := int(req.Msg.MaxFunctions)
	if maxFunctions <= 0 {
		maxFunctions = 20 // Default
	}
	if maxFunctions > 50 {
		maxFunctions = 50 // Max limit
	}

	duration := req.Msg.Duration
	if duration == nil || duration.AsDuration() > 5*time.Minute {
		duration = durationpb.New(60 * time.Second) // Default: 60s, Max: 5min
	}

	strategy := req.Msg.Strategy
	if strategy == "" {
		strategy = "critical_path" // Default strategy
	}

	// Check if function registry is available
	if o.functionRegistry == nil {
		return nil, connect.NewError(connect.CodeUnimplemented,
			fmt.Errorf("function registry not available"))
	}

	// Step 1: Discover functions matching the query
	colonyFunctions, err := o.functionRegistry.QueryFunctions(ctx, req.Msg.ServiceName, req.Msg.Query, maxFunctions)
	if err != nil {
		o.logger.Error().Err(err).
			Str("service", req.Msg.ServiceName).
			Str("query", req.Msg.Query).
			Msg("Failed to discover functions for profiling")
		return connect.NewResponse(&debugpb.ProfileFunctionsResponse{
			Status: "failed",
			Summary: &debugpb.ProfileSummary{
				FunctionsSelected: 0,
				FunctionsProbed:   0,
				ProbesFailed:      0,
			},
			Recommendation: fmt.Sprintf("Failed to discover functions: %v", err),
		}), nil
	}

	if len(colonyFunctions) == 0 {
		return connect.NewResponse(&debugpb.ProfileFunctionsResponse{
			Status: "failed",
			Summary: &debugpb.ProfileSummary{
				FunctionsSelected: 0,
				FunctionsProbed:   0,
				ProbesFailed:      0,
			},
			Recommendation: "No functions found matching query. Try a different search query.",
		}), nil
	}

	// Step 2: Apply selection strategy
	selectedFunctions := applySelectionStrategy(colonyFunctions, strategy)
	if len(selectedFunctions) > maxFunctions {
		selectedFunctions = selectedFunctions[:maxFunctions]
	}

	o.logger.Info().
		Int("discovered", len(colonyFunctions)).
		Int("selected", len(selectedFunctions)).
		Str("strategy", strategy).
		Msg("Function selection completed")

	// Step 3: Attach probes to all selected functions
	// For synchronous mode (async=false), we attach probes and wait
	// For async mode, we return immediately with in_progress status
	var profileResults []*debugpb.ProfileResult
	var sessionIDs []string // Track all session IDs
	successCount := 0
	failCount := 0

	// Add a buffer to session duration to ensure we can query events before expiration
	const sessionBuffer = 30 * time.Second
	sessionDuration := durationpb.New(duration.AsDuration() + sessionBuffer)

	for _, fn := range selectedFunctions {
		// Attach uprobe to each function with extended duration
		attachReq := connect.NewRequest(&debugpb.AttachUprobeRequest{
			ServiceName:  fn.ServiceName,
			FunctionName: fn.FunctionName,
			AgentId:      fn.AgentID,
			Duration:     sessionDuration, // Extended duration with buffer
			Config: &meshv1.UprobeConfig{
				CaptureArgs:   false,
				CaptureReturn: true,
				SampleRate:    uint32(req.Msg.SampleRate * 100),
			},
		})

		attachResp, err := o.AttachUprobe(ctx, attachReq)
		if err != nil || !attachResp.Msg.Success {
			o.logger.Warn().
				Err(err).
				Str("function", fn.FunctionName).
				Msg("Failed to attach probe")

			profileResults = append(profileResults, &debugpb.ProfileResult{
				Function:        fn.FunctionName,
				ProbeSuccessful: false,
			})
			failCount++
			continue
		}

		o.logger.Debug().
			Str("function", fn.FunctionName).
			Str("session_id", attachResp.Msg.SessionId).
			Msg("Probe attached successfully")

		// Track the session ID
		sessionIDs = append(sessionIDs, attachResp.Msg.SessionId)

		profileResults = append(profileResults, &debugpb.ProfileResult{
			Function:        fn.FunctionName,
			ProbeSuccessful: true,
			// Metrics will be populated after collection
		})
		successCount++
	}

	// If async mode, return immediately with in_progress status
	if req.Msg.Async {
		// Return the first session ID as the primary session
		primarySessionID := ""
		if len(sessionIDs) > 0 {
			primarySessionID = sessionIDs[0]
		}

		nextSteps := []string{"Use 'coral debug session list' to view active sessions"}
		for _, sid := range sessionIDs {
			nextSteps = append(nextSteps, fmt.Sprintf("Run 'coral debug session events %s' to see events", sid))
		}

		return connect.NewResponse(&debugpb.ProfileFunctionsResponse{
			SessionId:   primarySessionID,
			Status:      "in_progress",
			ServiceName: req.Msg.ServiceName,
			Query:       req.Msg.Query,
			Strategy:    strategy,
			Summary: &debugpb.ProfileSummary{
				FunctionsSelected: int32(len(selectedFunctions)),
				FunctionsProbed:   int32(successCount),
				ProbesFailed:      int32(failCount),
				Duration:          duration,
			},
			Results:        profileResults,
			Recommendation: fmt.Sprintf("Profiling in progress. Created %d session(s).", len(sessionIDs)),
			NextSteps:      nextSteps,
		}), nil
	}

	// Synchronous mode: continuously persist events during profiling
	// This prevents data loss if agent crashes or disconnects
	o.logger.Info().
		Dur("duration", duration.AsDuration()).
		Int("session_count", len(sessionIDs)).
		Msg("Starting profiling with continuous event persistence")

	// Continuously query and persist events during profiling
	const pollInterval = 5 * time.Second
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	deadline := time.Now().Add(duration.AsDuration())
	var totalEvents int64

	// Track last persisted event timestamp per session to avoid duplicates
	lastPersistedTime := make(map[string]time.Time)

	for time.Now().Before(deadline) {
		select {
		case <-ticker.C:
			// Query and persist events from all sessions
			for _, sessionID := range sessionIDs {
				queryReq := connect.NewRequest(&debugpb.QueryUprobeEventsRequest{
					SessionId: sessionID,
					StartTime: timestamppb.New(lastPersistedTime[sessionID]),
					MaxEvents: 10000,
				})

				queryResp, err := o.QueryUprobeEvents(ctx, queryReq)
				if err != nil {
					o.logger.Debug().
						Err(err).
						Str("session_id", sessionID).
						Msg("Failed to query events during profiling")
					continue
				}

				if len(queryResp.Msg.Events) > 0 {
					// Persist events to database immediately
					if err := o.db.InsertDebugEvents(sessionID, queryResp.Msg.Events); err != nil {
						o.logger.Error().
							Err(err).
							Str("session_id", sessionID).
							Int("event_count", len(queryResp.Msg.Events)).
							Msg("Failed to persist events during profiling")
					} else {
						totalEvents += int64(len(queryResp.Msg.Events))
						// Update last persisted timestamp
						if len(queryResp.Msg.Events) > 0 {
							lastEvent := queryResp.Msg.Events[len(queryResp.Msg.Events)-1]
							lastPersistedTime[sessionID] = lastEvent.Timestamp.AsTime()
						}
						o.logger.Debug().
							Str("session_id", sessionID).
							Int("event_count", len(queryResp.Msg.Events)).
							Int64("total_events", totalEvents).
							Msg("Persisted events during profiling")
					}
				}
			}
		case <-ctx.Done():
			goto done
		}
	}

done:
	o.logger.Info().
		Int64("total_events", totalEvents).
		Msg("Profiling collection completed")

	// Final query to catch any remaining events and compute statistics
	var bottlenecks []*debugpb.Bottleneck

	for i, sessionID := range sessionIDs {
		// Query all events from database (already persisted)
		events, err := o.db.GetDebugEvents(sessionID)
		if err != nil {
			o.logger.Warn().
				Err(err).
				Str("session_id", sessionID).
				Msg("Failed to query events from database")
			continue
		}

		eventCount := int64(len(events))
		if eventCount == 0 {
			continue
		}

		// Update profile result with metrics
		if i < len(profileResults) && profileResults[i].ProbeSuccessful {
			// Calculate statistics for this function
			stats := AggregateStatistics(events)

			profileResults[i].Metrics = &debugpb.FunctionMetrics{
				Source:      "probe_history",
				P50:         stats.DurationP50,
				P95:         stats.DurationP95,
				P99:         stats.DurationP99,
				CallsPerMin: float64(stats.TotalCalls) / duration.AsDuration().Minutes(),
				ErrorRate:   0.0, // TODO: Calculate error rate from events
				SampleSize:  stats.TotalCalls,
			}

			// Identify bottlenecks (functions with high P95 latency)
			if stats.DurationP95 != nil && stats.DurationP95.AsDuration() > 100*time.Millisecond {
				severity := "minor"
				if stats.DurationP95.AsDuration() > 1*time.Second {
					severity = "critical"
				} else if stats.DurationP95.AsDuration() > 500*time.Millisecond {
					severity = "major"
				}

				bottlenecks = append(bottlenecks, &debugpb.Bottleneck{
					Function:        profileResults[i].Function,
					P95:             stats.DurationP95,
					ContributionPct: 100, // TODO: Calculate actual contribution in calling context
					Severity:        severity,
					Impact:          fmt.Sprintf("P95 latency: %s", stats.DurationP95.AsDuration().String()),
					Recommendation:  fmt.Sprintf("High latency detected. Captured %d events with P95=%s", eventCount, stats.DurationP95.AsDuration().String()),
				})
			}
		}

		o.logger.Debug().
			Str("session_id", sessionID).
			Int("event_count", len(events)).
			Msg("Collected events from session")
	}

	// Detach all sessions to persist events and clean up collectors
	for _, sessionID := range sessionIDs {
		detachReq := connect.NewRequest(&debugpb.DetachUprobeRequest{
			SessionId: sessionID,
		})

		detachResp, err := o.DetachUprobe(ctx, detachReq)
		if err != nil || !detachResp.Msg.Success {
			o.logger.Warn().
				Err(err).
				Str("session_id", sessionID).
				Msg("Failed to detach session after profiling")
			// Continue anyway - events were already collected
		}
	}

	status := "completed"
	if failCount > 0 && successCount == 0 {
		status = "failed"
	} else if failCount > 0 {
		status = "partial_success"
	}

	recommendation := fmt.Sprintf("Profiled %d function(s) successfully. Collected %d total events.", successCount, totalEvents)
	if failCount > 0 {
		recommendation += fmt.Sprintf(" %d probe(s) failed to attach.", failCount)
	}
	if len(bottlenecks) > 0 {
		recommendation += fmt.Sprintf(" Found %d bottleneck(s).", len(bottlenecks))
	}

	// Build next steps
	nextSteps := []string{}
	if totalEvents == 0 {
		nextSteps = append(nextSteps, "No events captured. Ensure the functions are being called during the profiling window.")
	}
	if len(bottlenecks) > 0 {
		nextSteps = append(nextSteps, "Investigate high-latency functions identified in bottlenecks")
	}
	for _, sid := range sessionIDs {
		nextSteps = append(nextSteps, fmt.Sprintf("Run 'coral debug session events %s' for detailed event data", sid))
	}

	// Return the first session ID as the primary session
	primarySessionID := ""
	if len(sessionIDs) > 0 {
		primarySessionID = sessionIDs[0]
	}

	return connect.NewResponse(&debugpb.ProfileFunctionsResponse{
		SessionId:   primarySessionID,
		Status:      status,
		ServiceName: req.Msg.ServiceName,
		Query:       req.Msg.Query,
		Strategy:    strategy,
		Summary: &debugpb.ProfileSummary{
			FunctionsSelected:   int32(len(selectedFunctions)),
			FunctionsProbed:     int32(successCount),
			ProbesFailed:        int32(failCount),
			TotalEventsCaptured: totalEvents,
			Duration:            duration,
		},
		Results:        profileResults,
		Bottlenecks:    bottlenecks,
		Recommendation: recommendation,
		NextSteps:      nextSteps,
	}), nil
}

// applySelectionStrategy filters functions based on the selection strategy.
func applySelectionStrategy(functions []*colony.FunctionInfo, strategy string) []*colony.FunctionInfo {
	switch strategy {
	case "all":
		return functions
	case "entry_points":
		// Filter for entry points (HTTP handlers, RPC methods)
		// TODO: Implement proper entry point detection
		var filtered []*colony.FunctionInfo
		for _, fn := range functions {
			if strings.Contains(strings.ToLower(fn.FunctionName), "handle") ||
				strings.Contains(strings.ToLower(fn.FunctionName), "serve") {
				filtered = append(filtered, fn)
			}
		}
		if len(filtered) > 0 {
			return filtered
		}
		// Fallback to all if no entry points found
		return functions
	case "leaf_functions":
		// TODO: Implement leaf function detection using call graph
		return functions
	case "critical_path":
		fallthrough
	default:
		// For critical_path, return all discovered functions
		// TODO: Implement call graph analysis to identify critical path
		return functions
	}
}

// ProfileCPU collects CPU profile samples for a target service/pod (RFD 070).
func (o *Orchestrator) ProfileCPU(
	ctx context.Context,
	req *connect.Request[debugpb.ProfileCPURequest],
) (*connect.Response[debugpb.ProfileCPUResponse], error) {
	o.logger.Info().
		Str("service", req.Msg.ServiceName).
		Str("pod", req.Msg.PodName).
		Int32("duration", req.Msg.DurationSeconds).
		Int32("frequency", req.Msg.FrequencyHz).
		Msg("Starting CPU profiling")

	// Set defaults.
	durationSeconds := req.Msg.DurationSeconds
	if durationSeconds <= 0 {
		durationSeconds = 30 // Default 30 seconds
	}
	if durationSeconds > 300 {
		durationSeconds = 300 // Max 5 minutes
	}

	frequencyHz := req.Msg.FrequencyHz
	if frequencyHz <= 0 {
		frequencyHz = 99 // Default 99Hz
	}
	if frequencyHz > 1000 {
		frequencyHz = 1000 // Max 1000Hz
	}

	// Service Discovery: Find agent for service.
	agentID := req.Msg.AgentId
	if agentID == "" {
		entries := o.registry.ListAll()
		var foundEntry *registry.Entry

		for _, entry := range entries {
			// Query agent in real-time for services.
			agentURL := fmt.Sprintf("http://%s:9001", entry.MeshIPv4)
			client := o.agentClientFactory(http.DefaultClient, agentURL)

			queryCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			resp, err := client.ListServices(queryCtx, connect.NewRequest(&agentv1.ListServicesRequest{}))
			cancel()

			if err != nil {
				o.logger.Debug().
					Err(err).
					Str("agent_id", entry.AgentID).
					Msg("Failed to query agent services")
				continue
			}

			// Check if this agent has the service.
			for _, svcStatus := range resp.Msg.Services {
				if svcStatus.Name == req.Msg.ServiceName {
					foundEntry = entry
					break
				}
			}

			if foundEntry != nil {
				break
			}
		}

		if foundEntry == nil {
			return connect.NewResponse(&debugpb.ProfileCPUResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to find agent for service %s: service not found", req.Msg.ServiceName),
			}), nil
		}

		agentID = foundEntry.AgentID
	}

	if agentID == "" {
		return connect.NewResponse(&debugpb.ProfileCPUResponse{
			Success: false,
			Error:   "agent_id is required (could not resolve from service)",
		}), nil
	}

	// Get agent entry from registry.
	entry, err := o.registry.Get(agentID)
	if err != nil {
		o.logger.Error().Err(err).
			Str("agent_id", agentID).
			Msg("Failed to get agent from registry")
		return connect.NewResponse(&debugpb.ProfileCPUResponse{
			Success: false,
			Error:   fmt.Sprintf("agent not found: %v", err),
		}), nil
	}

	// Get PID for the service.
	// Query agent for service details to get PID.
	agentURL := fmt.Sprintf("http://%s:9001", entry.MeshIPv4)
	agentClient := o.agentClientFactory(http.DefaultClient, agentURL)

	servicesResp, err := agentClient.ListServices(ctx, connect.NewRequest(&agentv1.ListServicesRequest{}))
	if err != nil {
		return connect.NewResponse(&debugpb.ProfileCPUResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to query agent services: %v", err),
		}), nil
	}

	var targetPID int32
	for _, svc := range servicesResp.Msg.Services {
		if svc.Name == req.Msg.ServiceName {
			targetPID = svc.ProcessId
			break
		}
	}

	if targetPID == 0 {
		return connect.NewResponse(&debugpb.ProfileCPUResponse{
			Success: false,
			Error:   fmt.Sprintf("service %s not found on agent %s", req.Msg.ServiceName, agentID),
		}), nil
	}

	// Call agent to perform CPU profiling.
	debugClient := o.clientFactory(
		http.DefaultClient,
		fmt.Sprintf("http://%s", buildAgentAddress(entry.MeshIPv4)),
	)

	profileReq := connect.NewRequest(&meshv1.ProfileCPUAgentRequest{
		AgentId:         agentID,
		ServiceName:     req.Msg.ServiceName,
		Pid:             targetPID,
		DurationSeconds: durationSeconds,
		FrequencyHz:     frequencyHz,
	})

	profileResp, err := debugClient.ProfileCPU(ctx, profileReq)
	if err != nil {
		o.logger.Error().Err(err).
			Str("agent_id", agentID).
			Str("service", req.Msg.ServiceName).
			Msg("Failed to collect CPU profile from agent")
		return connect.NewResponse(&debugpb.ProfileCPUResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to collect CPU profile: %v", err),
		}), nil
	}

	if !profileResp.Msg.Success {
		return connect.NewResponse(&debugpb.ProfileCPUResponse{
			Success: false,
			Error:   profileResp.Msg.Error,
		}), nil
	}

	// Convert agent response to colony response.
	var samples []*debugpb.StackSample
	for _, sample := range profileResp.Msg.Samples {
		samples = append(samples, &debugpb.StackSample{
			FrameNames: sample.FrameNames,
			Count:      sample.Count,
		})
	}

	o.logger.Info().
		Str("service", req.Msg.ServiceName).
		Uint64("total_samples", profileResp.Msg.TotalSamples).
		Int("unique_stacks", len(samples)).
		Msg("CPU profiling completed")

	return connect.NewResponse(&debugpb.ProfileCPUResponse{
		Samples:      samples,
		TotalSamples: profileResp.Msg.TotalSamples,
		LostSamples:  profileResp.Msg.LostSamples,
		Success:      true,
	}), nil
}

// QueryHistoricalCPUProfile queries historical CPU profiles from continuous profiling (RFD 072).
func (o *Orchestrator) QueryHistoricalCPUProfile(
	ctx context.Context,
	req *connect.Request[debugpb.QueryHistoricalCPUProfileRequest],
) (*connect.Response[debugpb.QueryHistoricalCPUProfileResponse], error) {
	o.logger.Info().
		Str("service", req.Msg.ServiceName).
		Time("start_time", req.Msg.StartTime.AsTime()).
		Time("end_time", req.Msg.EndTime.AsTime()).
		Msg("Querying historical CPU profiles")

	// Query CPU profile summaries from colony database.
	summaries, err := o.db.QueryCPUProfileSummaries(
		ctx,
		req.Msg.ServiceName,
		req.Msg.StartTime.AsTime(),
		req.Msg.EndTime.AsTime(),
	)
	if err != nil {
		o.logger.Error().Err(err).
			Str("service", req.Msg.ServiceName).
			Msg("Failed to query CPU profile summaries")
		return connect.NewResponse(&debugpb.QueryHistoricalCPUProfileResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to query historical profiles: %v", err),
		}), nil
	}

	if len(summaries) == 0 {
		o.logger.Info().
			Str("service", req.Msg.ServiceName).
			Msg("No historical CPU profile data found")
		return connect.NewResponse(&debugpb.QueryHistoricalCPUProfileResponse{
			Success:      true,
			Samples:      nil,
			TotalSamples: 0,
		}), nil
	}

	// Aggregate samples by stack (sum counts across time).
	type stackKey struct {
		stackHash string
	}

	aggregated := make(map[stackKey]struct {
		frameIDs    []int64
		sampleCount uint64
	})

	for _, summary := range summaries {
		key := stackKey{stackHash: summary.StackHash}

		if existing, exists := aggregated[key]; exists {
			// Merge: sum sample counts.
			existing.sampleCount += uint64(summary.SampleCount)
			aggregated[key] = existing
		} else {
			// New stack.
			aggregated[key] = struct {
				frameIDs    []int64
				sampleCount uint64
			}{
				frameIDs:    summary.StackFrameIDs,
				sampleCount: uint64(summary.SampleCount),
			}
		}
	}

	// Decode frame IDs to frame names and build response.
	var samples []*debugpb.StackSample
	totalSamples := uint64(0)

	for _, agg := range aggregated {
		totalSamples += agg.sampleCount

		// Decode stack frames.
		frameNames, err := o.db.DecodeStackFrames(ctx, agg.frameIDs)
		if err != nil {
			o.logger.Warn().Err(err).Msg("Failed to decode stack frames, skipping sample")
			continue
		}

		samples = append(samples, &debugpb.StackSample{
			FrameNames: frameNames,
			Count:      agg.sampleCount,
		})
	}

	o.logger.Info().
		Str("service", req.Msg.ServiceName).
		Uint64("total_samples", totalSamples).
		Int("unique_stacks", len(samples)).
		Msg("Historical CPU profile query completed")

	return connect.NewResponse(&debugpb.QueryHistoricalCPUProfileResponse{
		Samples:      samples,
		TotalSamples: totalSamples,
		Success:      true,
	}), nil
}
