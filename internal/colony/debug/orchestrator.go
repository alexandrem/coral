// Package debug provides debug session orchestration for the colony.
package debug

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
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
	logger           zerolog.Logger
	registry         *registry.Registry
	db               *database.Database
	functionRegistry *colony.FunctionRegistry
	clientFactory    func(connect.HTTPClient, string, ...connect.ClientOption) meshv1connect.DebugServiceClient
}

// NewOrchestrator creates a new debug orchestrator.
func NewOrchestrator(logger zerolog.Logger, registry *registry.Registry, db *database.Database, functionRegistry *colony.FunctionRegistry) *Orchestrator {
	return &Orchestrator{
		logger:           logger.With().Str("component", "debug_orchestrator").Logger(),
		registry:         registry,
		db:               db,
		functionRegistry: functionRegistry,
		clientFactory:    meshv1connect.NewDebugServiceClient,
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
		}
	}

	// Call agent to stop uprobe collector
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
		// Session still active - query from agent
		o.logger.Debug().
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
		o.logger.Error().Err(err).
			Str("session_id", req.Msg.SessionId).
			Msg("Failed to query debug session from database")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}
	if session == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("session not found: %s", req.Msg.SessionId))
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
				CurrentlyProbed: false, // TODO: Check active sessions
			},
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
	sessionID := uuid.New().String()
	var profileResults []*debugpb.ProfileResult
	successCount := 0
	failCount := 0

	for _, fn := range selectedFunctions {
		// Attach uprobe to each function
		attachReq := connect.NewRequest(&debugpb.AttachUprobeRequest{
			ServiceName:  fn.ServiceName,
			FunctionName: fn.FunctionName,
			AgentId:      fn.AgentID,
			Duration:     duration,
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

		profileResults = append(profileResults, &debugpb.ProfileResult{
			Function:        fn.FunctionName,
			ProbeSuccessful: true,
			// Metrics will be populated after collection
		})
		successCount++
	}

	// If async mode, return immediately with in_progress status
	if req.Msg.Async {
		return connect.NewResponse(&debugpb.ProfileFunctionsResponse{
			SessionId:   sessionID,
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
			Recommendation: "Profiling in progress. Use coral debug session list to check status.",
		}), nil
	}

	// Synchronous mode: wait for duration and collect results
	o.logger.Info().
		Dur("duration", duration.AsDuration()).
		Msg("Waiting for profiling data collection")

	time.Sleep(duration.AsDuration())

	// TODO: Collect and aggregate results from all sessions
	// TODO: Perform bottleneck analysis
	// For now, return basic response

	status := "completed"
	if failCount > 0 && successCount == 0 {
		status = "failed"
	} else if failCount > 0 {
		status = "partial_success"
	}

	recommendation := fmt.Sprintf("Profiled %d functions successfully.", successCount)
	if failCount > 0 {
		recommendation += fmt.Sprintf(" %d probe(s) failed to attach.", failCount)
	}

	return connect.NewResponse(&debugpb.ProfileFunctionsResponse{
		SessionId:   sessionID,
		Status:      status,
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
		Bottlenecks:    []*debugpb.Bottleneck{}, // TODO: Implement bottleneck analysis
		Recommendation: recommendation,
		NextSteps: []string{
			"Use 'coral debug session list' to view active sessions",
			"Run 'coral debug session events <session-id>' to see detailed metrics",
		},
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
