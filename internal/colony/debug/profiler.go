// Package debug provides debug session orchestration for the colony.
package debug

import (
	"context"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/colony"
	"github.com/coral-mesh/coral/internal/colony/database"
)

// probeAttacher defines the interface for attaching and detaching probes.
type probeAttacher interface {
	AttachUprobe(ctx context.Context, req *connect.Request[debugpb.AttachUprobeRequest]) (*connect.Response[debugpb.AttachUprobeResponse], error)
	DetachUprobe(ctx context.Context, req *connect.Request[debugpb.DetachUprobeRequest]) (*connect.Response[debugpb.DetachUprobeResponse], error)
	QueryUprobeEvents(ctx context.Context, req *connect.Request[debugpb.QueryUprobeEventsRequest]) (*connect.Response[debugpb.QueryUprobeEventsResponse], error)
}

// FunctionProfiler handles batch function profiling with automatic analysis.
type FunctionProfiler struct {
	logger           zerolog.Logger
	functionRegistry *colony.FunctionRegistry
	probeAttacher    probeAttacher
	db               *database.Database
}

// NewFunctionProfiler creates a new function profiler.
func NewFunctionProfiler(
	logger zerolog.Logger,
	functionRegistry *colony.FunctionRegistry,
	probeAttacher probeAttacher,
	db *database.Database,
) *FunctionProfiler {
	return &FunctionProfiler{
		logger:           logger.With().Str("component", "function_profiler").Logger(),
		functionRegistry: functionRegistry,
		probeAttacher:    probeAttacher,
		db:               db,
	}
}

// Profile executes a batch profiling session for functions matching the query.
func (fp *FunctionProfiler) Profile(
	ctx context.Context,
	req *connect.Request[debugpb.ProfileFunctionsRequest],
) (*connect.Response[debugpb.ProfileFunctionsResponse], error) {
	fp.logger.Info().
		Str("service", req.Msg.ServiceName).
		Str("query", req.Msg.Query).
		Str("strategy", req.Msg.Strategy).
		Msg("Starting batch profiling")

	cfg := parseProfileConfig(req.Msg)

	// Validate function registry is available.
	if fp.functionRegistry == nil {
		return nil, connect.NewError(connect.CodeUnimplemented,
			fmt.Errorf("function registry not available"))
	}

	// Discover functions matching the query.
	functions, err := fp.discoverFunctions(ctx, cfg)
	if err != nil {
		return connect.NewResponse(fp.errorResponse(cfg, err.Error())), nil
	}
	if len(functions) == 0 {
		return connect.NewResponse(fp.errorResponse(cfg, "No functions found matching query. Try a different search query.")), nil
	}

	// Attach probes to discovered functions.
	state := fp.attachAllProbes(ctx, functions, cfg)

	// For async mode, return immediately.
	if cfg.Async {
		return connect.NewResponse(fp.buildAsyncResponse(cfg, state)), nil
	}

	// Synchronous mode: collect events, compute stats, cleanup.
	totalEvents := fp.collectEventsSync(ctx, state, cfg.Duration)
	bottlenecks := fp.computeBottlenecks(ctx, state, cfg.Duration)
	fp.detachAllSessions(ctx, state.SessionIDs)

	return connect.NewResponse(fp.buildSyncResponse(cfg, state, bottlenecks, totalEvents)), nil
}

// discoverFunctions queries the function registry and applies the selection strategy.
func (fp *FunctionProfiler) discoverFunctions(ctx context.Context, cfg *profileConfig) ([]*colony.FunctionInfo, error) {
	functions, err := fp.functionRegistry.QueryFunctions(ctx, cfg.ServiceName, cfg.Query, cfg.MaxFunctions)
	if err != nil {
		fp.logger.Error().Err(err).
			Str("service", cfg.ServiceName).
			Str("query", cfg.Query).
			Msg("Failed to discover functions for profiling")
		return nil, fmt.Errorf("failed to discover functions: %w", err)
	}

	// Apply selection strategy.
	selected := applySelectionStrategy(functions, cfg.Strategy)
	if len(selected) > cfg.MaxFunctions {
		selected = selected[:cfg.MaxFunctions]
	}

	fp.logger.Info().
		Int("discovered", len(functions)).
		Int("selected", len(selected)).
		Str("strategy", cfg.Strategy).
		Msg("Function selection completed")

	return selected, nil
}

// attachAllProbes attaches uprobes to all selected functions.
func (fp *FunctionProfiler) attachAllProbes(ctx context.Context, functions []*colony.FunctionInfo, cfg *profileConfig) *profileState {
	state := &profileState{
		SessionIDs: make([]string, 0, len(functions)),
		Results:    make([]*debugpb.ProfileResult, 0, len(functions)),
	}

	// Add buffer to session duration to ensure we can query events before expiration.
	sessionDuration := durationpb.New(cfg.Duration + sessionBuffer)

	for _, fn := range functions {
		result, sessionID := fp.attachProbe(ctx, fn, cfg, sessionDuration)
		state.Results = append(state.Results, result)

		if result.ProbeSuccessful {
			state.SessionIDs = append(state.SessionIDs, sessionID)
			state.SuccessCount++
		} else {
			state.FailCount++
		}
	}

	return state
}

// attachProbe attaches a single uprobe and returns the result and session ID.
func (fp *FunctionProfiler) attachProbe(
	ctx context.Context,
	fn *colony.FunctionInfo,
	cfg *profileConfig,
	sessionDuration *durationpb.Duration,
) (*debugpb.ProfileResult, string) {
	attachReq := connect.NewRequest(&debugpb.AttachUprobeRequest{
		ServiceName:  fn.ServiceName,
		FunctionName: fn.FunctionName,
		AgentId:      fn.AgentID,
		Duration:     sessionDuration,
		Config: &agentv1.UprobeConfig{
			CaptureArgs:   false,
			CaptureReturn: true,
			SampleRate:    uint32(cfg.SampleRate * 100),
		},
	})

	attachResp, err := fp.probeAttacher.AttachUprobe(ctx, attachReq)
	if err != nil || !attachResp.Msg.Success {
		fp.logger.Warn().
			Err(err).
			Str("function", fn.FunctionName).
			Msg("Failed to attach probe")

		return &debugpb.ProfileResult{
			Function:        fn.FunctionName,
			ProbeSuccessful: false,
		}, ""
	}

	fp.logger.Debug().
		Str("function", fn.FunctionName).
		Str("session_id", attachResp.Msg.SessionId).
		Msg("Probe attached successfully")

	return &debugpb.ProfileResult{
		Function:        fn.FunctionName,
		ProbeSuccessful: true,
	}, attachResp.Msg.SessionId
}

// collectEventsSync continuously queries and persists events during the profiling window.
func (fp *FunctionProfiler) collectEventsSync(ctx context.Context, state *profileState, duration time.Duration) int64 {
	fp.logger.Info().
		Dur("duration", duration).
		Int("session_count", len(state.SessionIDs)).
		Msg("Starting profiling with continuous event persistence")

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	deadline := time.Now().Add(duration)
	var totalEvents int64
	lastPersistedTime := make(map[string]time.Time)

	for time.Now().Before(deadline) {
		select {
		case <-ticker.C:
			events, updated := fp.persistEventsForSessions(ctx, state.SessionIDs, lastPersistedTime)
			totalEvents += events
			lastPersistedTime = updated
		case <-ctx.Done():
			fp.logger.Info().
				Int64("total_events", totalEvents).
				Msg("Profiling collection interrupted by context")
			return totalEvents
		}
	}

	fp.logger.Info().
		Int64("total_events", totalEvents).
		Msg("Profiling collection completed")

	return totalEvents
}

// persistEventsForSessions queries and persists events from all sessions.
func (fp *FunctionProfiler) persistEventsForSessions(
	ctx context.Context,
	sessionIDs []string,
	lastPersistedTime map[string]time.Time,
) (int64, map[string]time.Time) {
	var totalEvents int64
	updated := make(map[string]time.Time)
	for k, v := range lastPersistedTime {
		updated[k] = v
	}

	for _, sessionID := range sessionIDs {
		queryReq := connect.NewRequest(&debugpb.QueryUprobeEventsRequest{
			SessionId: sessionID,
			StartTime: timestamppb.New(lastPersistedTime[sessionID]),
			MaxEvents: 10000,
		})

		queryResp, err := fp.probeAttacher.QueryUprobeEvents(ctx, queryReq)
		if err != nil {
			fp.logger.Debug().
				Err(err).
				Str("session_id", sessionID).
				Msg("Failed to query events during profiling")
			continue
		}

		if len(queryResp.Msg.Events) == 0 {
			continue
		}

		if err := fp.db.InsertDebugEvents(ctx, sessionID, queryResp.Msg.Events); err != nil {
			fp.logger.Error().
				Err(err).
				Str("session_id", sessionID).
				Int("event_count", len(queryResp.Msg.Events)).
				Msg("Failed to persist events during profiling")
			continue
		}

		totalEvents += int64(len(queryResp.Msg.Events))
		lastEvent := queryResp.Msg.Events[len(queryResp.Msg.Events)-1]
		updated[sessionID] = lastEvent.Timestamp.AsTime()

		fp.logger.Debug().
			Str("session_id", sessionID).
			Int("event_count", len(queryResp.Msg.Events)).
			Int64("total_events", totalEvents).
			Msg("Persisted events during profiling")
	}

	return totalEvents, updated
}

// computeBottlenecks calculates statistics and identifies bottlenecks from collected events.
func (fp *FunctionProfiler) computeBottlenecks(ctx context.Context, state *profileState, duration time.Duration) []*debugpb.Bottleneck {
	var bottlenecks []*debugpb.Bottleneck

	for i, result := range state.Results {
		if !result.ProbeSuccessful || i >= len(state.SessionIDs) {
			continue
		}

		sessionID := state.SessionIDs[i]
		events, err := fp.db.GetDebugEvents(sessionID)
		if err != nil {
			fp.logger.Warn().
				Err(err).
				Str("session_id", sessionID).
				Msg("Failed to query events from database")
			continue
		}

		if len(events) == 0 {
			continue
		}

		// Calculate statistics for this function.
		stats := AggregateStatistics(events)
		result.Metrics = &debugpb.FunctionMetrics{
			Source:      "probe_history",
			P50:         stats.DurationP50,
			P95:         stats.DurationP95,
			P99:         stats.DurationP99,
			CallsPerMin: float64(stats.TotalCalls) / duration.Minutes(),
			ErrorRate:   0.0,
			SampleSize:  stats.TotalCalls,
		}

		// Identify bottlenecks (functions with high P95 latency).
		if stats.DurationP95 != nil && stats.DurationP95.AsDuration() > bottleneckMinorThreshold {
			p95Duration := stats.DurationP95.AsDuration()
			bottlenecks = append(bottlenecks, &debugpb.Bottleneck{
				Function:        result.Function,
				P95:             stats.DurationP95,
				ContributionPct: 100,
				Severity:        severityFromDuration(p95Duration),
				Impact:          fmt.Sprintf("P95 latency: %s", p95Duration.String()),
				Recommendation:  fmt.Sprintf("High latency detected. Captured %d events with P95=%s", len(events), p95Duration.String()),
			})
		}

		fp.logger.Debug().
			Str("session_id", sessionID).
			Int("event_count", len(events)).
			Msg("Collected events from session")
	}

	return bottlenecks
}

// detachAllSessions detaches all probes and cleans up sessions.
func (fp *FunctionProfiler) detachAllSessions(ctx context.Context, sessionIDs []string) {
	for _, sessionID := range sessionIDs {
		detachReq := connect.NewRequest(&debugpb.DetachUprobeRequest{
			SessionId: sessionID,
		})

		detachResp, err := fp.probeAttacher.DetachUprobe(ctx, detachReq)
		if err != nil || !detachResp.Msg.Success {
			fp.logger.Warn().
				Err(err).
				Str("session_id", sessionID).
				Msg("Failed to detach session after profiling")
		}
	}
}

// errorResponse builds a failed response with the given error message.
func (fp *FunctionProfiler) errorResponse(cfg *profileConfig, errMsg string) *debugpb.ProfileFunctionsResponse {
	return &debugpb.ProfileFunctionsResponse{
		Status: "failed",
		Summary: &debugpb.ProfileSummary{
			FunctionsSelected: 0,
			FunctionsProbed:   0,
			ProbesFailed:      0,
		},
		Recommendation: errMsg,
	}
}

// buildAsyncResponse builds the response for async profiling mode.
func (fp *FunctionProfiler) buildAsyncResponse(cfg *profileConfig, state *profileState) *debugpb.ProfileFunctionsResponse {
	primarySessionID := ""
	if len(state.SessionIDs) > 0 {
		primarySessionID = state.SessionIDs[0]
	}

	nextSteps := []string{"Use 'coral debug session list' to view active sessions"}
	for _, sid := range state.SessionIDs {
		nextSteps = append(nextSteps, fmt.Sprintf("Run 'coral debug session events %s' to see events", sid))
	}

	return &debugpb.ProfileFunctionsResponse{
		SessionId:   primarySessionID,
		Status:      "in_progress",
		ServiceName: cfg.ServiceName,
		Query:       cfg.Query,
		Strategy:    cfg.Strategy,
		Summary: &debugpb.ProfileSummary{
			FunctionsSelected: int32(state.SuccessCount + state.FailCount),
			FunctionsProbed:   int32(state.SuccessCount),
			ProbesFailed:      int32(state.FailCount),
			Duration:          durationpb.New(cfg.Duration),
		},
		Results:        state.Results,
		Recommendation: fmt.Sprintf("Profiling in progress. Created %d session(s).", len(state.SessionIDs)),
		NextSteps:      nextSteps,
	}
}

// buildSyncResponse builds the response for synchronous profiling mode.
func (fp *FunctionProfiler) buildSyncResponse(
	cfg *profileConfig,
	state *profileState,
	bottlenecks []*debugpb.Bottleneck,
	totalEvents int64,
) *debugpb.ProfileFunctionsResponse {
	primarySessionID := ""
	if len(state.SessionIDs) > 0 {
		primarySessionID = state.SessionIDs[0]
	}

	status := fp.determineStatus(state)
	recommendation := fp.buildRecommendation(state, bottlenecks, totalEvents)
	nextSteps := fp.buildNextSteps(state.SessionIDs, bottlenecks, totalEvents)

	return &debugpb.ProfileFunctionsResponse{
		SessionId:   primarySessionID,
		Status:      status,
		ServiceName: cfg.ServiceName,
		Query:       cfg.Query,
		Strategy:    cfg.Strategy,
		Summary: &debugpb.ProfileSummary{
			FunctionsSelected:   int32(state.SuccessCount + state.FailCount),
			FunctionsProbed:     int32(state.SuccessCount),
			ProbesFailed:        int32(state.FailCount),
			TotalEventsCaptured: totalEvents,
			Duration:            durationpb.New(cfg.Duration),
		},
		Results:        state.Results,
		Bottlenecks:    bottlenecks,
		Recommendation: recommendation,
		NextSteps:      nextSteps,
	}
}

// determineStatus returns the profiling status based on success/failure counts.
func (fp *FunctionProfiler) determineStatus(state *profileState) string {
	switch {
	case state.FailCount > 0 && state.SuccessCount == 0:
		return "failed"
	case state.FailCount > 0:
		return "partial_success"
	default:
		return "completed"
	}
}

// buildRecommendation constructs the recommendation message.
func (fp *FunctionProfiler) buildRecommendation(state *profileState, bottlenecks []*debugpb.Bottleneck, totalEvents int64) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("Profiled %d function(s) successfully. Collected %d total events.", state.SuccessCount, totalEvents))

	if state.FailCount > 0 {
		parts = append(parts, fmt.Sprintf("%d probe(s) failed to attach.", state.FailCount))
	}
	if len(bottlenecks) > 0 {
		parts = append(parts, fmt.Sprintf("Found %d bottleneck(s).", len(bottlenecks)))
	}

	return strings.Join(parts, " ")
}

// buildNextSteps constructs the list of suggested next steps.
func (fp *FunctionProfiler) buildNextSteps(sessionIDs []string, bottlenecks []*debugpb.Bottleneck, totalEvents int64) []string {
	var nextSteps []string

	if totalEvents == 0 {
		nextSteps = append(nextSteps, "No events captured. Ensure the functions are being called during the profiling window.")
	}
	if len(bottlenecks) > 0 {
		nextSteps = append(nextSteps, "Investigate high-latency functions identified in bottlenecks")
	}
	for _, sid := range sessionIDs {
		nextSteps = append(nextSteps, fmt.Sprintf("Run 'coral debug session events %s' for detailed event data", sid))
	}

	return nextSteps
}
