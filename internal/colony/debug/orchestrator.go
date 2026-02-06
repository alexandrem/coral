// Package debug provides debug session orchestration for the colony.
package debug

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/colony"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/registry"
	"github.com/coral-mesh/coral/internal/constants"
	"github.com/coral-mesh/coral/internal/safe"
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
	functionProfiler *FunctionProfiler
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
	o.functionProfiler = NewFunctionProfiler(logger, o, o, db)

	// Start background event persistence for all sessions.
	eventPersister.Start()

	return o
}

// Stop gracefully stops the orchestrator's background tasks.
func (o *Orchestrator) Stop() {
	o.eventPersister.Stop()
}

// getFunctionRegistry returns the orchestrator's function registry.
func (o *Orchestrator) getFunctionRegistry() *colony.FunctionRegistry {
	return o.functionRegistry
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
	return o.functionProfiler.Profile(ctx, req)
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
	agentID, err := o.agentCoordinator.FindAgentForService(ctx, req.Msg.ServiceName)
	if err != nil {
		return connect.NewResponse(&debugpb.ProfileCPUResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to find agent for service %s: %v", req.Msg.ServiceName, err),
		}), nil
	}

	if agentID == "" {
		return connect.NewResponse(&debugpb.ProfileCPUResponse{
			Success: false,
			Error:   fmt.Sprintf("no agent found for service %s", req.Msg.ServiceName),
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

	// Service Discovery: Find agent for service.
	agentID, err := o.agentCoordinator.FindAgentForService(ctx, req.Msg.ServiceName)
	if err != nil {
		return connect.NewResponse(&debugpb.ProfileMemoryResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to find agent for service %s: %v", req.Msg.ServiceName, err),
		}), nil
	}

	if agentID == "" {
		return connect.NewResponse(&debugpb.ProfileMemoryResponse{
			Success: false,
			Error:   fmt.Sprintf("no agent found for service %s", req.Msg.ServiceName),
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

	// Track function and type aggregations for summaries.
	funcBytes := make(map[string]int64)
	funcObjects := make(map[string]int64)
	typeBytes := make(map[string]int64)
	typeObjects := make(map[string]int64)

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

		// Aggregate by function for top functions summary.
		for _, fn := range frameNames {
			funcBytes[fn] += agg.allocBytes
			funcObjects[fn] += agg.allocObjects
		}

		// Aggregate by allocation type (from leaf function).
		if len(frameNames) > 0 {
			typeName := classifyMemoryAllocType(frameNames[0])
			typeBytes[typeName] += agg.allocBytes
			typeObjects[typeName] += agg.allocObjects
		}
	}

	// Compute top functions (sorted by bytes, limited to top 20).
	topFunctions := computeTopFunctions(funcBytes, funcObjects, totalAllocBytes, 20)

	// Compute top types (sorted by bytes, limited to top 10).
	topTypes := computeTopTypes(typeBytes, typeObjects, totalAllocBytes, 10)

	numSamples, clamped := safe.IntToInt32(len(samples))
	if clamped {
		o.logger.Warn().
			Int32("num_samples", numSamples).
			Msg("Abnormal samples size, clamped to int32")
	}

	return connect.NewResponse(&debugpb.QueryHistoricalMemoryProfileResponse{
		Samples:         samples,
		TotalAllocBytes: totalAllocBytes,
		TopFunctions:    topFunctions,
		TopTypes:        topTypes,
		UniqueStacks:    numSamples,
		Success:         true,
	}), nil
}

// computeTopFunctions builds a sorted list of top allocating functions.
func computeTopFunctions(funcBytes, funcObjects map[string]int64, totalBytes int64, limit int) []*agentv1.TopAllocFunction {
	type funcEntry struct {
		name    string
		bytes   int64
		objects int64
	}
	var entries []funcEntry
	for fn, bytes := range funcBytes {
		entries = append(entries, funcEntry{name: fn, bytes: bytes, objects: funcObjects[fn]})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].bytes > entries[j].bytes })
	if len(entries) > limit {
		entries = entries[:limit]
	}

	result := make([]*agentv1.TopAllocFunction, 0, len(entries))
	for _, e := range entries {
		pct := 0.0
		if totalBytes > 0 {
			pct = float64(e.bytes) / float64(totalBytes) * 100
		}
		result = append(result, &agentv1.TopAllocFunction{
			Function: shortenFuncName(e.name),
			Bytes:    e.bytes,
			Objects:  e.objects,
			Pct:      pct,
		})
	}
	return result
}

// computeTopTypes builds a sorted list of top allocation types.
func computeTopTypes(typeBytes, typeObjects map[string]int64, totalBytes int64, limit int) []*agentv1.TopAllocType {
	type typeEntry struct {
		name    string
		bytes   int64
		objects int64
	}
	var entries []typeEntry
	for tn, bytes := range typeBytes {
		entries = append(entries, typeEntry{name: tn, bytes: bytes, objects: typeObjects[tn]})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].bytes > entries[j].bytes })
	if len(entries) > limit {
		entries = entries[:limit]
	}

	result := make([]*agentv1.TopAllocType, 0, len(entries))
	for _, e := range entries {
		pct := 0.0
		if totalBytes > 0 {
			pct = float64(e.bytes) / float64(totalBytes) * 100
		}
		result = append(result, &agentv1.TopAllocType{
			TypeName: e.name,
			Bytes:    e.bytes,
			Objects:  e.objects,
			Pct:      pct,
		})
	}
	return result
}

// classifyMemoryAllocType maps a leaf function name to an allocation type category.
func classifyMemoryAllocType(funcName string) string {
	switch {
	case strings.Contains(funcName, "makeslice") || strings.Contains(funcName, "growslice"):
		return "slice"
	case strings.Contains(funcName, "makemap") || strings.Contains(funcName, "mapassign"):
		return "map"
	case strings.Contains(funcName, "newobject") || strings.Contains(funcName, "mallocgc"):
		return "object"
	case strings.Contains(funcName, "concatstrings") || strings.Contains(funcName, "slicebytetostring") || strings.Contains(funcName, "stringtoslicebyte"):
		return "string"
	case strings.Contains(funcName, "makechan"):
		return "channel"
	case strings.Contains(funcName, "newproc") || strings.Contains(funcName, "mstart"):
		return "goroutine"
	default:
		return shortenFuncName(funcName)
	}
}

// shortenFuncName shortens a fully qualified function name for readability.
// github.com/coral-mesh/coral/pkg/sdk.(*SDK).initializeDebugServer -> sdk.(*SDK).initializeDebugServer.
func shortenFuncName(fullName string) string {
	lastSlash := strings.LastIndex(fullName, "/")
	if lastSlash >= 0 {
		return fullName[lastSlash+1:]
	}
	return fullName
}
