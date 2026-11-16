// Package triage implements the coral triage composite diagnosis command
// (RFD 114). It combines the existing health summary, profiling data, and
// function registry into one bounded, read-by-default result instead of
// requiring an agent to compose several separate queries.
package triage

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/cli/helpers"
)

// Stable outcome values (RFD 114 User Interface section).
const (
	OutcomeDegraded        = "degraded"
	OutcomeHealthy         = "healthy"
	OutcomeUnknown         = "unknown"
	OutcomeNothingDegraded = "nothing_degraded"
	OutcomeNoData          = "no_data"
)

// Stable candidate statuses.
const (
	CandidateFound       = "found"
	CandidateNotFound    = "not_found"
	CandidateUnavailable = "unavailable"
)

// Stable attach statuses.
const (
	AttachNotRequested = "not_requested"
	AttachAttached     = "attached"
	AttachSkipped      = "skipped"
	AttachFailed       = "failed"
)

// Candidate sources.
const (
	SourceProfilingHotPath   = "profiling_hot_path"
	SourceSemanticIssueMatch = "semantic_issue_match"
)

// Health status values reported by QueryUnifiedSummary.
const (
	statusCritical = "critical"
	statusDegraded = "degraded"
	statusHealthy  = "healthy"
)

// DefaultSince is the time range used when the caller doesn't override it.
const DefaultSince = "5m"

// DefaultAttachDuration is the bounded probe lifetime used when --attach is
// passed without --attach-duration.
const DefaultAttachDuration = 30 * time.Second

// MaxAttachDuration matches the AttachUprobe server's maximum probe lifetime.
const MaxAttachDuration = 10 * time.Minute

// client is the narrow RPC surface triage needs, kept interface-based so
// service selection and candidate/partial-result behavior can be unit tested
// without a live colony.
type client interface {
	QueryUnifiedSummary(ctx context.Context, req *colonypb.QueryUnifiedSummaryRequest) (*colonypb.QueryUnifiedSummaryResponse, error)
	QueryFunctions(ctx context.Context, req *colonypb.QueryFunctionsRequest) (*colonypb.QueryFunctionsResponse, error)
	AttachUprobe(ctx context.Context, req *colonypb.AttachUprobeRequest) (*colonypb.AttachUprobeResponse, error)
}

// colonyClient adapts the generated Connect clients (one for QueryUnifiedSummary
// on ColonyService, one for QueryFunctions/AttachUprobe on ColonyDebugService)
// to the triage client interface.
type colonyClient struct {
	service colonyv1connect.ColonyServiceClient
	debug   colonyv1connect.ColonyDebugServiceClient
}

// newColonyClient builds the real client using shared config resolution.
func newColonyClient(colonyID string) (*colonyClient, error) {
	service, err := helpers.GetColonyClient(colonyID)
	if err != nil {
		return nil, fmt.Errorf("failed to create colony client: %w", err)
	}
	debug, err := helpers.GetColonyDebugClient(colonyID)
	if err != nil {
		return nil, fmt.Errorf("failed to create colony debug client: %w", err)
	}
	return &colonyClient{service: service, debug: debug}, nil
}

func (c *colonyClient) QueryUnifiedSummary(ctx context.Context, req *colonypb.QueryUnifiedSummaryRequest) (*colonypb.QueryUnifiedSummaryResponse, error) {
	resp, err := c.service.QueryUnifiedSummary(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (c *colonyClient) QueryFunctions(ctx context.Context, req *colonypb.QueryFunctionsRequest) (*colonypb.QueryFunctionsResponse, error) {
	resp, err := c.debug.QueryFunctions(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (c *colonyClient) AttachUprobe(ctx context.Context, req *colonypb.AttachUprobeRequest) (*colonypb.AttachUprobeResponse, error) {
	resp, err := c.debug.AttachUprobe(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// Options configures a Diagnose call.
type Options struct {
	// Service scopes diagnosis to one service. Empty selects the worst
	// service across the fleet.
	Service string
	// Since is the summary time range (default DefaultSince).
	Since string
	// Attach requests an AttachUprobe call when a candidate is resolved.
	Attach bool
	// AttachDuration is the bounded probe lifetime (default DefaultAttachDuration).
	AttachDuration time.Duration
}

// SummarySnapshot is the subset of QueryUnifiedSummary fields surfaced in a
// triage Result.
type SummarySnapshot struct {
	Status       string  `json:"status"`
	ErrorRate    float64 `json:"error_rate"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
}

// CandidateFunction identifies a resolved probe target.
type CandidateFunction struct {
	Name   string `json:"name"`
	File   string `json:"file,omitempty"`
	Line   int32  `json:"line,omitempty"`
	Source string `json:"source"`
}

// AttachOutcome reports whether an eBPF probe was attached.
type AttachOutcome struct {
	Status    string `json:"status"`
	SessionID string `json:"session_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Result is the stable triage output schema (RFD 114 User Interface section).
type Result struct {
	Service           string             `json:"service"`
	Outcome           string             `json:"outcome"`
	Summary           *SummarySnapshot   `json:"summary,omitempty"`
	CandidateStatus   string             `json:"candidate_status,omitempty"`
	CandidateFunction *CandidateFunction `json:"candidate_function,omitempty"`
	CandidateError    string             `json:"candidate_error,omitempty"`
	Attach            AttachOutcome      `json:"attach"`
}

// Diagnose runs the triage stages against a live colony connection.
func Diagnose(ctx context.Context, colonyID string, opts Options) (*Result, error) {
	c, err := newColonyClient(colonyID)
	if err != nil {
		return nil, err
	}
	return diagnose(ctx, c, opts)
}

// diagnose is the client-agnostic implementation, exercised directly by unit
// tests against a fake client.
func diagnose(ctx context.Context, c client, opts Options) (*Result, error) {
	if opts.AttachDuration < 0 {
		return nil, fmt.Errorf("attach duration must be positive")
	}
	since := opts.Since
	if since == "" {
		since = DefaultSince
	}

	resp, err := c.QueryUnifiedSummary(ctx, &colonypb.QueryUnifiedSummaryRequest{
		Service:   opts.Service,
		TimeRange: since,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query summary: %w", err)
	}

	attachStatus := AttachNotRequested
	if opts.Attach {
		attachStatus = AttachSkipped
	}
	if len(resp.Summaries) == 0 {
		return &Result{Service: opts.Service, Outcome: OutcomeNoData, Attach: AttachOutcome{Status: attachStatus}}, nil
	}

	var target *colonypb.UnifiedSummaryResult
	if opts.Service != "" {
		target = resp.Summaries[0]
	} else {
		target = selectWorstService(resp.Summaries)
	}

	result := &Result{
		Service: target.ServiceName,
		Summary: &SummarySnapshot{
			Status:       target.Status,
			ErrorRate:    target.ErrorRate,
			AvgLatencyMs: target.AvgLatencyMs,
		},
		Attach: AttachOutcome{Status: attachStatus},
	}

	if target.Status != statusCritical && target.Status != statusDegraded {
		if opts.Service != "" {
			if target.Status == statusHealthy {
				result.Outcome = OutcomeHealthy
			} else {
				result.Outcome = OutcomeUnknown
			}
		} else {
			result.Outcome = OutcomeNothingDegraded
		}
		return result, nil
	}
	result.Outcome = OutcomeDegraded

	candidate, candidateStatus, err := resolveCandidate(ctx, c, target)
	result.CandidateStatus = candidateStatus
	if err != nil {
		result.CandidateStatus = CandidateUnavailable
		result.CandidateError = err.Error()
	} else {
		result.CandidateFunction = candidate
	}

	attachDuration := opts.AttachDuration
	if attachDuration == 0 {
		attachDuration = DefaultAttachDuration
	}
	if attachDuration > MaxAttachDuration {
		attachDuration = MaxAttachDuration
	}
	result.Attach = maybeAttach(ctx, c, target.ServiceName, candidate, opts.Attach, attachDuration)

	return result, nil
}

// selectWorstService picks the worst service using the total order
// critical > degraded > healthy > unknown-or-empty, tie-broken by error
// rate, then average latency, then service name for deterministic output.
func selectWorstService(summaries []*colonypb.UnifiedSummaryResult) *colonypb.UnifiedSummaryResult {
	sorted := make([]*colonypb.UnifiedSummaryResult, len(summaries))
	copy(sorted, summaries)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if ra, rb := statusRank(a.Status), statusRank(b.Status); ra != rb {
			return ra < rb
		}
		if a.ErrorRate != b.ErrorRate {
			return a.ErrorRate > b.ErrorRate
		}
		if a.AvgLatencyMs != b.AvgLatencyMs {
			return a.AvgLatencyMs > b.AvgLatencyMs
		}
		return a.ServiceName < b.ServiceName
	})
	return sorted[0]
}

func statusRank(status string) int {
	switch status {
	case statusCritical:
		return 0
	case statusDegraded:
		return 1
	case statusHealthy:
		return 2
	default:
		return 3 // unknown or empty
	}
}

// resolveCandidate implements the two mutually exclusive resolution rules:
// an exact hot-path walk when profiling data is available, or a semantic
// query built from issues and regressions otherwise.
func resolveCandidate(ctx context.Context, c client, summary *colonypb.UnifiedSummaryResult) (*CandidateFunction, string, error) {
	if frames := hotPathFrames(summary); len(frames) > 0 {
		return resolveFromHotPath(ctx, c, summary.ServiceName, frames)
	}
	return resolveFromSemanticQuery(ctx, c, summary)
}

// hotPathFrames returns the hottest CPU stack, leaf to root, or nil when no
// profiling data is available.
func hotPathFrames(summary *colonypb.UnifiedSummaryResult) []string {
	ps := summary.ProfilingSummary
	if ps == nil || len(ps.TopCpuHotspots) == 0 {
		return nil
	}
	return ps.TopCpuHotspots[0].Frames
}

// resolveFromHotPath walks the hot path from leaf (the deepest, most likely
// candidate) to caller, querying the registry for an exact, service-scoped,
// probeable, not-already-probed match.
func resolveFromHotPath(ctx context.Context, c client, serviceName string, frames []string) (*CandidateFunction, string, error) {
	for _, frame := range frames {
		if strings.TrimSpace(frame) == "" {
			continue
		}
		cand, err := lookupExactFunction(ctx, c, serviceName, frame)
		if err != nil {
			return nil, CandidateUnavailable, err
		}
		if cand != nil {
			return cand, CandidateFound, nil
		}
	}
	return nil, CandidateNotFound, nil
}

// lookupExactFunction queries the registry, scoped to serviceName, and
// returns the first result whose name exactly matches frame (after
// normalization) and is selectable. The service scope is enforced
// server-side, so a match can never come from another service.
func lookupExactFunction(ctx context.Context, c client, serviceName, frame string) (*CandidateFunction, error) {
	resp, err := c.QueryFunctions(ctx, &colonypb.QueryFunctionsRequest{
		ServiceName: serviceName,
		Query:       frame,
		MaxResults:  20,
	})
	if err != nil {
		return nil, err
	}
	for _, r := range resp.Results {
		if r.Function == nil || !exactFunctionMatch(r.Function.Name, frame) {
			continue
		}
		if !isSelectable(r) {
			continue
		}
		return functionFromResult(r, SourceProfilingHotPath), nil
	}
	return nil, nil
}

// exactFunctionMatch compares a registry function name and a profile frame
// while retaining package and receiver qualifiers present in the frame. A
// fully qualified registry name can match a shorter profile name, but two
// distinct qualified functions with the same final identifier cannot match.
func exactFunctionMatch(registryName, frame string) bool {
	if registryName == "" || frame == "" {
		return false
	}
	return registryName == frame || strings.HasSuffix(registryName, "/"+frame) ||
		(!strings.Contains(frame, "/") && strings.HasSuffix(registryName, "."+frame))
}

// resolveFromSemanticQuery builds a query from the summary's issues and
// regression messages and selects the first selectable, service-scoped
// registry result, preserving the registry's own result ordering.
func resolveFromSemanticQuery(ctx context.Context, c client, summary *colonypb.UnifiedSummaryResult) (*CandidateFunction, string, error) {
	query := semanticQuery(summary)
	if query == "" {
		return nil, CandidateNotFound, nil
	}

	resp, err := c.QueryFunctions(ctx, &colonypb.QueryFunctionsRequest{
		ServiceName: summary.ServiceName,
		Query:       query,
		MaxResults:  20,
	})
	if err != nil {
		return nil, CandidateUnavailable, err
	}
	for _, r := range resp.Results {
		if r.Function == nil || !isSelectable(r) {
			continue
		}
		return functionFromResult(r, SourceSemanticIssueMatch), CandidateFound, nil
	}
	return nil, CandidateNotFound, nil
}

func semanticQuery(summary *colonypb.UnifiedSummaryResult) string {
	parts := make([]string, 0, len(summary.Issues)+len(summary.Regressions))
	for _, issue := range summary.Issues {
		if issue = strings.TrimSpace(issue); issue != "" {
			parts = append(parts, issue)
		}
	}
	for _, r := range summary.Regressions {
		if message := strings.TrimSpace(r.GetMessage()); message != "" {
			parts = append(parts, message)
		}
	}
	return strings.Join(parts, " ")
}

func isSelectable(r *colonypb.FunctionResult) bool {
	return r.Instrumentation != nil && r.Instrumentation.IsProbeable && !r.Instrumentation.CurrentlyProbed
}

func functionFromResult(r *colonypb.FunctionResult, source string) *CandidateFunction {
	return &CandidateFunction{
		Name:   r.Function.Name,
		File:   r.Function.File,
		Line:   r.Function.Line,
		Source: source,
	}
}

// maybeAttach calls AttachUprobe only when attach was requested and a
// candidate was resolved; otherwise the system is left unchanged.
func maybeAttach(ctx context.Context, c client, serviceName string, candidate *CandidateFunction, attach bool, duration time.Duration) AttachOutcome {
	if !attach {
		return AttachOutcome{Status: AttachNotRequested}
	}
	if candidate == nil {
		return AttachOutcome{Status: AttachSkipped}
	}

	resp, err := c.AttachUprobe(ctx, &colonypb.AttachUprobeRequest{
		ServiceName:  serviceName,
		FunctionName: candidate.Name,
		Duration:     durationpb.New(duration),
	})
	if err != nil {
		return AttachOutcome{Status: AttachFailed, Error: err.Error()}
	}
	if !resp.Success {
		return AttachOutcome{Status: AttachFailed, Error: resp.Error}
	}
	return AttachOutcome{Status: AttachAttached, SessionID: resp.SessionId}
}
