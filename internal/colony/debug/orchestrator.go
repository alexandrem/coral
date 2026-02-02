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
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	debugpb "github.com/coral-mesh/coral/coral/colony/v1"

	"github.com/coral-mesh/coral/internal/colony"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/registry"
	"github.com/coral-mesh/coral/internal/constants"
)

// Orchestrator manages debug sessions across agents.
type Orchestrator struct {
	logger             zerolog.Logger
	registry           *registry.Registry
	db                 *database.Database
	functionRegistry   *colony.FunctionRegistry
	clientFactory      func(connect.HTTPClient, string, ...connect.ClientOption) agentv1connect.AgentDebugServiceClient
	agentClientFactory func(connect.HTTPClient, string, ...connect.ClientOption) agentv1connect.AgentServiceClient

	// Components.
	sessionManager   *SessionManager
	eventPersister   *EventPersister
	agentCoordinator *AgentCoordinator
	queryRouter      *QueryRouter
}

// NewOrchestrator creates a new debug orchestrator.
func NewOrchestrator(logger zerolog.Logger, registry *registry.Registry, db *database.Database, functionRegistry *colony.FunctionRegistry) *Orchestrator {
	o := &Orchestrator{
		logger:             logger.With().Str("component", "debug_orchestrator").Logger(),
		registry:           registry,
		db:                 db,
		functionRegistry:   functionRegistry,
		clientFactory:      agentv1connect.NewAgentDebugServiceClient,
		agentClientFactory: agentv1connect.NewAgentServiceClient,
	}

	// Create agent coordinator with closure that uses orchestrator's factory.
	agentCoordinator := NewAgentCoordinator(
		logger,
		registry,
		func(client connect.HTTPClient, url string, opts ...connect.ClientOption) agentv1connect.AgentServiceClient {
			return o.agentClientFactory(client, url, opts...)
		},
	)

	// Create query router with closure that uses orchestrator's factory.
	queryRouter := NewQueryRouter(
		logger,
		registry,
		db,
		func(client connect.HTTPClient, url string, opts ...connect.ClientOption) agentv1connect.AgentDebugServiceClient {
			return o.clientFactory(client, url, opts...)
		},
	)

	// Create session manager with closure that uses orchestrator's factory.
	sessionManager := NewSessionManager(
		logger,
		registry,
		db,
		agentCoordinator,
		func(client connect.HTTPClient, url string, opts ...connect.ClientOption) agentv1connect.AgentDebugServiceClient {
			return o.clientFactory(client, url, opts...)
		},
	)

	// Create event persister.
	// Use background context for the event persister's lifecycle.
	eventPersister := NewEventPersister(
		context.Background(),
		logger,
		db,
		queryRouter,
	)

	// Assign components.
	o.sessionManager = sessionManager
	o.eventPersister = eventPersister
	o.agentCoordinator = agentCoordinator
	o.queryRouter = queryRouter

	// Start background event persistence for all sessions.
	eventPersister.Start()

	return o
}

// Stop gracefully stops the orchestrator's background tasks.
func (o *Orchestrator) Stop() {
	o.eventPersister.Stop()
}

// AttachUprobe starts a new debug session by attaching a uprobe to a function.
func (o *Orchestrator) AttachUprobe(
	ctx context.Context,
	req *connect.Request[debugpb.AttachUprobeRequest],
) (*connect.Response[debugpb.AttachUprobeResponse], error) {
	return o.sessionManager.AttachUprobe(ctx, req)
}

// DetachUprobe stops a debug session.
func (o *Orchestrator) DetachUprobe(
	ctx context.Context,
	req *connect.Request[debugpb.DetachUprobeRequest],
) (*connect.Response[debugpb.DetachUprobeResponse], error) {
	return o.sessionManager.DetachUprobe(ctx, req)
}

// QueryUprobeEvents retrieves events from a debug session.
func (o *Orchestrator) QueryUprobeEvents(
	ctx context.Context,
	req *connect.Request[debugpb.QueryUprobeEventsRequest],
) (*connect.Response[debugpb.QueryUprobeEventsResponse], error) {
	return o.queryRouter.QueryUprobeEvents(ctx, req)
}

// ListDebugSessions lists all active debug sessions.
func (o *Orchestrator) ListDebugSessions(
	ctx context.Context,
	req *connect.Request[debugpb.ListDebugSessionsRequest],
) (*connect.Response[debugpb.ListDebugSessionsResponse], error) {
	return o.sessionManager.ListDebugSessions(ctx, req)
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
		Config: &agentv1.UprobeConfig{
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
	return o.queryRouter.GetDebugResults(ctx, req)
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
			Config: &agentv1.UprobeConfig{
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
					if err := o.db.InsertDebugEvents(ctx, sessionID, queryResp.Msg.Events); err != nil {
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
		var err error
		agentID, err = o.agentCoordinator.FindAgentForService(ctx, req.Msg.ServiceName)
		if err != nil {
			return connect.NewResponse(&debugpb.ProfileCPUResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to find agent for service %s: %v", req.Msg.ServiceName, err),
			}), nil
		}
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
	targetPID, err := o.agentCoordinator.GetServicePID(ctx, agentID, req.Msg.ServiceName)
	if err != nil {
		return connect.NewResponse(&debugpb.ProfileCPUResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to get service PID: %v", err),
		}), nil
	}

	// Call agent to perform CPU profiling.
	debugClient := o.clientFactory(
		http.DefaultClient,
		fmt.Sprintf("http://%s", buildAgentAddress(entry.MeshIPv4)),
	)

	profileReq := connect.NewRequest(&agentv1.ProfileCPUAgentRequest{
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

	o.logger.Info().
		Str("service", req.Msg.ServiceName).
		Uint64("total_samples", profileResp.Msg.TotalSamples).
		Int("unique_stacks", len(profileResp.Msg.Samples)).
		Msg("CPU profiling completed")

	return connect.NewResponse(&debugpb.ProfileCPUResponse{
		Samples:      profileResp.Msg.Samples,
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
	var samples []*agentv1.StackSample
	totalSamples := uint64(0)

	for _, agg := range aggregated {
		totalSamples += agg.sampleCount

		// Decode stack frames.
		frameNames, err := o.db.DecodeStackFrames(ctx, agg.frameIDs)
		if err != nil {
			o.logger.Warn().Err(err).Msg("Failed to decode stack frames, skipping sample")
			continue
		}

		samples = append(samples, &agentv1.StackSample{
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

// ProfileMemory collects memory profile for a target service/pod (RFD 077).
func (o *Orchestrator) ProfileMemory(
	ctx context.Context,
	req *connect.Request[debugpb.ProfileMemoryRequest],
) (*connect.Response[debugpb.ProfileMemoryResponse], error) {
	o.logger.Info().
		Str("service", req.Msg.ServiceName).
		Int32("duration", req.Msg.DurationSeconds).
		Msg("Starting memory profiling")

	durationSeconds := req.Msg.DurationSeconds
	if durationSeconds <= 0 {
		durationSeconds = 30
	}
	if durationSeconds > 300 {
		durationSeconds = 300
	}

	// Find agent for service.
	agentID := req.Msg.AgentId
	if agentID == "" {
		var err error
		agentID, err = o.agentCoordinator.FindAgentForService(ctx, req.Msg.ServiceName)
		if err != nil {
			return connect.NewResponse(&debugpb.ProfileMemoryResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to find agent for service %s: %v", req.Msg.ServiceName, err),
			}), nil
		}
	}

	if agentID == "" {
		return connect.NewResponse(&debugpb.ProfileMemoryResponse{
			Success: false,
			Error:   "agent_id is required (could not resolve from service)",
		}), nil
	}

	entry, err := o.registry.Get(agentID)
	if err != nil {
		return connect.NewResponse(&debugpb.ProfileMemoryResponse{
			Success: false,
			Error:   fmt.Sprintf("agent not found: %v", err),
		}), nil
	}

	targetPID, err := o.agentCoordinator.GetServicePID(ctx, agentID, req.Msg.ServiceName)
	if err != nil {
		return connect.NewResponse(&debugpb.ProfileMemoryResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to get service PID: %v", err),
		}), nil
	}

	debugClient := o.clientFactory(
		http.DefaultClient,
		fmt.Sprintf("http://%s", buildAgentAddress(entry.MeshIPv4)),
	)

	profileReq := connect.NewRequest(&agentv1.ProfileMemoryAgentRequest{
		AgentId:         agentID,
		ServiceName:     req.Msg.ServiceName,
		Pid:             targetPID,
		DurationSeconds: durationSeconds,
		SampleRateBytes: req.Msg.SampleRateBytes,
	})

	profileResp, err := debugClient.ProfileMemory(ctx, profileReq)
	if err != nil {
		return connect.NewResponse(&debugpb.ProfileMemoryResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to collect memory profile: %v", err),
		}), nil
	}

	if !profileResp.Msg.Success {
		return connect.NewResponse(&debugpb.ProfileMemoryResponse{
			Success: false,
			Error:   profileResp.Msg.Error,
		}), nil
	}

	return connect.NewResponse(&debugpb.ProfileMemoryResponse{
		Samples:      profileResp.Msg.Samples,
		Stats:        profileResp.Msg.Stats,
		TopFunctions: profileResp.Msg.TopFunctions,
		TopTypes:     profileResp.Msg.TopTypes,
		Success:      true,
	}), nil
}

// QueryHistoricalMemoryProfile queries historical memory profiles from continuous profiling (RFD 077).
func (o *Orchestrator) QueryHistoricalMemoryProfile(
	ctx context.Context,
	req *connect.Request[debugpb.QueryHistoricalMemoryProfileRequest],
) (*connect.Response[debugpb.QueryHistoricalMemoryProfileResponse], error) {
	o.logger.Info().
		Str("service", req.Msg.ServiceName).
		Msg("Querying historical memory profiles")

	summaries, err := o.db.QueryMemoryProfileSummaries(
		ctx,
		req.Msg.ServiceName,
		req.Msg.StartTime.AsTime(),
		req.Msg.EndTime.AsTime(),
	)
	if err != nil {
		return connect.NewResponse(&debugpb.QueryHistoricalMemoryProfileResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to query historical memory profiles: %v", err),
		}), nil
	}

	if len(summaries) == 0 {
		return connect.NewResponse(&debugpb.QueryHistoricalMemoryProfileResponse{
			Success:         true,
			Samples:         nil,
			TotalAllocBytes: 0,
		}), nil
	}

	// Aggregate by stack hash.
	type stackAgg struct {
		frameIDs     []int64
		allocBytes   int64
		allocObjects int64
	}

	aggregated := make(map[string]*stackAgg)
	for _, summary := range summaries {
		if existing, exists := aggregated[summary.StackHash]; exists {
			existing.allocBytes += summary.AllocBytes
			existing.allocObjects += summary.AllocObjects
		} else {
			aggregated[summary.StackHash] = &stackAgg{
				frameIDs:     summary.StackFrameIDs,
				allocBytes:   summary.AllocBytes,
				allocObjects: summary.AllocObjects,
			}
		}
	}

	var samples []*agentv1.MemoryStackSample
	var totalAllocBytes int64

	for _, agg := range aggregated {
		totalAllocBytes += agg.allocBytes

		frameNames, err := o.db.DecodeStackFrames(ctx, agg.frameIDs)
		if err != nil {
			o.logger.Warn().Err(err).Msg("Failed to decode stack frames, skipping memory sample")
			continue
		}

		samples = append(samples, &agentv1.MemoryStackSample{
			FrameNames:   frameNames,
			AllocBytes:   agg.allocBytes,
			AllocObjects: agg.allocObjects,
		})
	}

	return connect.NewResponse(&debugpb.QueryHistoricalMemoryProfileResponse{
		Samples:         samples,
		TotalAllocBytes: totalAllocBytes,
		Success:         true,
	}), nil
}
