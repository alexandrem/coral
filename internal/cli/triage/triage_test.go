package triage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
)

// fakeClient is a test double for the triage client interface, recording
// every QueryFunctions and AttachUprobe call so tests can assert on both
// return values and call counts (RFD 114).
type fakeClient struct {
	summaryResp *colonypb.QueryUnifiedSummaryResponse
	summaryErr  error

	// queryFunctionsResp maps a request's Query string to the canned response.
	// A Query with no entry returns an empty result set.
	queryFunctionsResp  map[string]*colonypb.QueryFunctionsResponse
	queryFunctionsErr   error
	queryFunctionsCalls []*colonypb.QueryFunctionsRequest

	attachResp  *colonypb.AttachUprobeResponse
	attachErr   error
	attachCalls []*colonypb.AttachUprobeRequest
}

func (f *fakeClient) QueryUnifiedSummary(context.Context, *colonypb.QueryUnifiedSummaryRequest) (*colonypb.QueryUnifiedSummaryResponse, error) {
	return f.summaryResp, f.summaryErr
}

func (f *fakeClient) QueryFunctions(_ context.Context, req *colonypb.QueryFunctionsRequest) (*colonypb.QueryFunctionsResponse, error) {
	f.queryFunctionsCalls = append(f.queryFunctionsCalls, req)
	if f.queryFunctionsErr != nil {
		return nil, f.queryFunctionsErr
	}
	if resp, ok := f.queryFunctionsResp[req.Query]; ok {
		return resp, nil
	}
	return &colonypb.QueryFunctionsResponse{}, nil
}

func (f *fakeClient) AttachUprobe(_ context.Context, req *colonypb.AttachUprobeRequest) (*colonypb.AttachUprobeResponse, error) {
	f.attachCalls = append(f.attachCalls, req)
	if f.attachErr != nil {
		return nil, f.attachErr
	}
	return f.attachResp, nil
}

func summaryResult(service, status string, errorRate, avgLatency float64) *colonypb.UnifiedSummaryResult {
	return &colonypb.UnifiedSummaryResult{
		ServiceName:  service,
		Status:       status,
		ErrorRate:    errorRate,
		AvgLatencyMs: avgLatency,
	}
}

func functionResult(name, file string, line int32, probeable, probed bool) *colonypb.FunctionResult {
	return &colonypb.FunctionResult{
		Function:        &colonypb.FunctionMetadata{Name: name, File: file, Line: line},
		Instrumentation: &colonypb.InstrumentationInfo{IsProbeable: probeable, CurrentlyProbed: probed},
	}
}

func TestSelectWorstService(t *testing.T) {
	t.Run("critical beats degraded, healthy, and unknown", func(t *testing.T) {
		summaries := []*colonypb.UnifiedSummaryResult{
			summaryResult("healthy-svc", "healthy", 0, 10),
			summaryResult("unknown-svc", "", 0, 0),
			summaryResult("degraded-svc", "degraded", 5, 200),
			summaryResult("critical-svc", "critical", 50, 900),
		}
		got := selectWorstService(summaries)
		assert.Equal(t, "critical-svc", got.ServiceName)
	})

	t.Run("degraded beats healthy and unknown", func(t *testing.T) {
		summaries := []*colonypb.UnifiedSummaryResult{
			summaryResult("unknown-svc", "", 0, 0),
			summaryResult("healthy-svc", "healthy", 0, 10),
			summaryResult("degraded-svc", "degraded", 5, 200),
		}
		got := selectWorstService(summaries)
		assert.Equal(t, "degraded-svc", got.ServiceName)
	})

	t.Run("healthy beats unknown/empty status", func(t *testing.T) {
		summaries := []*colonypb.UnifiedSummaryResult{
			summaryResult("unknown-svc", "idle", 0, 0),
			summaryResult("healthy-svc", "healthy", 0, 10),
		}
		got := selectWorstService(summaries)
		assert.Equal(t, "healthy-svc", got.ServiceName)
	})

	t.Run("same status — tie-break by error rate", func(t *testing.T) {
		summaries := []*colonypb.UnifiedSummaryResult{
			summaryResult("low-error", "critical", 10, 900),
			summaryResult("high-error", "critical", 90, 900),
		}
		got := selectWorstService(summaries)
		assert.Equal(t, "high-error", got.ServiceName)
	})

	t.Run("same status and error rate — tie-break by avg latency", func(t *testing.T) {
		summaries := []*colonypb.UnifiedSummaryResult{
			summaryResult("fast", "critical", 50, 100),
			summaryResult("slow", "critical", 50, 900),
		}
		got := selectWorstService(summaries)
		assert.Equal(t, "slow", got.ServiceName)
	})

	t.Run("same status, error rate, and latency — tie-break by name", func(t *testing.T) {
		summaries := []*colonypb.UnifiedSummaryResult{
			summaryResult("zebra", "critical", 50, 500),
			summaryResult("alpha", "critical", 50, 500),
		}
		got := selectWorstService(summaries)
		assert.Equal(t, "alpha", got.ServiceName)
	})

	t.Run("stable and deterministic across repeated calls", func(t *testing.T) {
		summaries := []*colonypb.UnifiedSummaryResult{
			summaryResult("b", "degraded", 5, 200),
			summaryResult("a", "degraded", 5, 200),
			summaryResult("c", "critical", 5, 200),
		}
		for i := 0; i < 5; i++ {
			got := selectWorstService(summaries)
			assert.Equal(t, "c", got.ServiceName)
		}
	})
}

func TestDiagnose_NoData(t *testing.T) {
	c := &fakeClient{summaryResp: &colonypb.QueryUnifiedSummaryResponse{}}
	result, err := diagnose(context.Background(), c, Options{Service: "api"})
	require.NoError(t, err)
	assert.Equal(t, OutcomeNoData, result.Outcome)
	assert.Equal(t, AttachNotRequested, result.Attach.Status)
	assert.Empty(t, c.queryFunctionsCalls)
	assert.Empty(t, c.attachCalls)
}

func TestDiagnose_SummaryQueryError(t *testing.T) {
	c := &fakeClient{summaryErr: errors.New("colony unreachable")}
	result, err := diagnose(context.Background(), c, Options{})
	require.Error(t, err)
	assert.Nil(t, result)
}

func TestDiagnose_AllHealthyFleet_NeverQueriesOrAttaches(t *testing.T) {
	c := &fakeClient{summaryResp: &colonypb.QueryUnifiedSummaryResponse{
		Summaries: []*colonypb.UnifiedSummaryResult{
			summaryResult("api", "healthy", 0, 10),
			summaryResult("worker", "healthy", 0, 20),
		},
	}}
	result, err := diagnose(context.Background(), c, Options{Attach: true})
	require.NoError(t, err)
	assert.Equal(t, OutcomeNothingDegraded, result.Outcome)
	assert.Empty(t, c.queryFunctionsCalls)
	assert.Empty(t, c.attachCalls)
	assert.Equal(t, AttachNotRequested, result.Attach.Status)
}

func TestDiagnose_NamedServiceHealthy_NeverQueriesOrAttaches(t *testing.T) {
	c := &fakeClient{summaryResp: &colonypb.QueryUnifiedSummaryResponse{
		Summaries: []*colonypb.UnifiedSummaryResult{summaryResult("api", "healthy", 0, 10)},
	}}
	result, err := diagnose(context.Background(), c, Options{Service: "api", Attach: true})
	require.NoError(t, err)
	assert.Equal(t, OutcomeHealthy, result.Outcome)
	assert.Empty(t, c.queryFunctionsCalls)
	assert.Empty(t, c.attachCalls)
}

func TestDiagnose_NamedServiceUnknownStatus(t *testing.T) {
	c := &fakeClient{summaryResp: &colonypb.QueryUnifiedSummaryResponse{
		Summaries: []*colonypb.UnifiedSummaryResult{summaryResult("api", "idle", 0, 0)},
	}}
	result, err := diagnose(context.Background(), c, Options{Service: "api"})
	require.NoError(t, err)
	assert.Equal(t, OutcomeUnknown, result.Outcome)
	assert.Empty(t, c.queryFunctionsCalls)
}

func TestDiagnose_HotPathResolution(t *testing.T) {
	summary := summaryResult("api", "critical", 50, 900)
	summary.ProfilingSummary = &colonypb.ProfilingSummary{
		TopCpuHotspots: []*colonypb.CPUHotspot{
			{Frames: []string{"main.a", "main.b", "main.c"}}, // root...leaf
		},
	}
	c := &fakeClient{
		summaryResp: &colonypb.QueryUnifiedSummaryResponse{Summaries: []*colonypb.UnifiedSummaryResult{summary}},
		queryFunctionsResp: map[string]*colonypb.QueryFunctionsResponse{
			"main.c": {Results: []*colonypb.FunctionResult{functionResult("main.c", "svc/c.go", 10, true, false)}},
		},
	}
	result, err := diagnose(context.Background(), c, Options{Service: "api"})
	require.NoError(t, err)
	assert.Equal(t, OutcomeDegraded, result.Outcome)
	assert.Equal(t, CandidateFound, result.CandidateStatus)
	require.NotNil(t, result.CandidateFunction)
	assert.Equal(t, "main.c", result.CandidateFunction.Name)
	assert.Equal(t, SourceProfilingHotPath, result.CandidateFunction.Source)
	// The leaf frame matched on the first lookup — no need to walk further.
	require.Len(t, c.queryFunctionsCalls, 1)
	assert.Equal(t, "main.c", c.queryFunctionsCalls[0].Query)
	assert.Equal(t, "api", c.queryFunctionsCalls[0].ServiceName)
}

func TestDiagnose_HotPathSkipsUnprobeableAndAlreadyProbedFrames(t *testing.T) {
	summary := summaryResult("api", "critical", 50, 900)
	summary.ProfilingSummary = &colonypb.ProfilingSummary{
		TopCpuHotspots: []*colonypb.CPUHotspot{
			{Frames: []string{"main.a", "main.b", "main.c"}},
		},
	}
	c := &fakeClient{
		summaryResp: &colonypb.QueryUnifiedSummaryResponse{Summaries: []*colonypb.UnifiedSummaryResult{summary}},
		queryFunctionsResp: map[string]*colonypb.QueryFunctionsResponse{
			// Leaf "main.c" matches but is already probed — must be skipped.
			"main.c": {Results: []*colonypb.FunctionResult{functionResult("main.c", "svc/c.go", 10, true, true)}},
			// "main.b" matches but is not probeable — must be skipped.
			"main.b": {Results: []*colonypb.FunctionResult{functionResult("main.b", "svc/b.go", 20, false, false)}},
			// "main.a" (the caller/root) matches and is selectable.
			"main.a": {Results: []*colonypb.FunctionResult{functionResult("main.a", "svc/a.go", 30, true, false)}},
		},
	}
	result, err := diagnose(context.Background(), c, Options{Service: "api"})
	require.NoError(t, err)
	assert.Equal(t, CandidateFound, result.CandidateStatus)
	assert.Equal(t, "main.a", result.CandidateFunction.Name)
	require.Len(t, c.queryFunctionsCalls, 3)
}

func TestDiagnose_SemanticFallback_PreservesRegistryOrder(t *testing.T) {
	summary := summaryResult("api", "critical", 50, 900)
	summary.Issues = []string{"high latency in checkout path"}
	query := "high latency in checkout path"
	c := &fakeClient{
		summaryResp: &colonypb.QueryUnifiedSummaryResponse{Summaries: []*colonypb.UnifiedSummaryResult{summary}},
		queryFunctionsResp: map[string]*colonypb.QueryFunctionsResponse{
			query: {Results: []*colonypb.FunctionResult{
				functionResult("api.alreadyProbed", "svc/x.go", 1, true, true), // skipped: already probed
				functionResult("api.handleCheckout", "svc/checkout.go", 118, true, false),
				functionResult("api.unrelated", "svc/y.go", 2, true, false), // never reached
			}},
		},
	}
	result, err := diagnose(context.Background(), c, Options{Service: "api"})
	require.NoError(t, err)
	assert.Equal(t, CandidateFound, result.CandidateStatus)
	assert.Equal(t, "api.handleCheckout", result.CandidateFunction.Name)
	assert.Equal(t, SourceSemanticIssueMatch, result.CandidateFunction.Source)
	require.Len(t, c.queryFunctionsCalls, 1)
	assert.Equal(t, query, c.queryFunctionsCalls[0].Query)
}

func TestDiagnose_SemanticFallback_UsesRegressionMessages(t *testing.T) {
	summary := summaryResult("api", "degraded", 10, 300)
	summary.Regressions = []*colonypb.RegressionIndicator{{Message: "new hotspot in handler"}}
	c := &fakeClient{
		summaryResp: &colonypb.QueryUnifiedSummaryResponse{Summaries: []*colonypb.UnifiedSummaryResult{summary}},
		queryFunctionsResp: map[string]*colonypb.QueryFunctionsResponse{
			"new hotspot in handler": {Results: []*colonypb.FunctionResult{
				functionResult("api.handler", "svc/handler.go", 5, true, false),
			}},
		},
	}
	result, err := diagnose(context.Background(), c, Options{Service: "api"})
	require.NoError(t, err)
	assert.Equal(t, CandidateFound, result.CandidateStatus)
	assert.Equal(t, "api.handler", result.CandidateFunction.Name)
}

func TestDiagnose_NoHotPathNoIssues_NotFoundWithoutQuerying(t *testing.T) {
	summary := summaryResult("api", "critical", 50, 900)
	c := &fakeClient{summaryResp: &colonypb.QueryUnifiedSummaryResponse{Summaries: []*colonypb.UnifiedSummaryResult{summary}}}
	result, err := diagnose(context.Background(), c, Options{Service: "api"})
	require.NoError(t, err)
	assert.Equal(t, CandidateNotFound, result.CandidateStatus)
	assert.Empty(t, c.queryFunctionsCalls, "must not select from an unfiltered arbitrary list")
}

func TestDiagnose_CandidateUnavailable_QueryFunctionsError(t *testing.T) {
	summary := summaryResult("api", "critical", 50, 900)
	summary.Issues = []string{"errors spiking"}
	c := &fakeClient{
		summaryResp:       &colonypb.QueryUnifiedSummaryResponse{Summaries: []*colonypb.UnifiedSummaryResult{summary}},
		queryFunctionsErr: errors.New("registry unavailable"),
	}
	result, err := diagnose(context.Background(), c, Options{Service: "api"})
	require.NoError(t, err, "candidate lookup failures are structured partial results, not Go errors")
	assert.Equal(t, OutcomeDegraded, result.Outcome)
	assert.Equal(t, CandidateUnavailable, result.CandidateStatus)
	assert.NotEmpty(t, result.CandidateError)
	assert.Nil(t, result.CandidateFunction)
	assert.Equal(t, AttachNotRequested, result.Attach.Status)
}

func TestDiagnose_AttachNotRequestedByDefault(t *testing.T) {
	summary := summaryResult("api", "critical", 50, 900)
	summary.Issues = []string{"errors spiking"}
	c := &fakeClient{
		summaryResp: &colonypb.QueryUnifiedSummaryResponse{Summaries: []*colonypb.UnifiedSummaryResult{summary}},
		queryFunctionsResp: map[string]*colonypb.QueryFunctionsResponse{
			"errors spiking": {Results: []*colonypb.FunctionResult{functionResult("api.f", "svc/f.go", 1, true, false)}},
		},
	}
	result, err := diagnose(context.Background(), c, Options{Service: "api"})
	require.NoError(t, err)
	assert.Equal(t, AttachNotRequested, result.Attach.Status)
	assert.Empty(t, c.attachCalls)
}

func TestDiagnose_AttachRequestedAndSucceeds(t *testing.T) {
	summary := summaryResult("api", "critical", 50, 900)
	summary.Issues = []string{"errors spiking"}
	c := &fakeClient{
		summaryResp: &colonypb.QueryUnifiedSummaryResponse{Summaries: []*colonypb.UnifiedSummaryResult{summary}},
		queryFunctionsResp: map[string]*colonypb.QueryFunctionsResponse{
			"errors spiking": {Results: []*colonypb.FunctionResult{functionResult("api.handleCheckout", "svc/checkout.go", 118, true, false)}},
		},
		attachResp: &colonypb.AttachUprobeResponse{Success: true, SessionId: "sess-123"},
	}
	result, err := diagnose(context.Background(), c, Options{Service: "api", Attach: true, AttachDuration: 15 * time.Second})
	require.NoError(t, err)
	require.Len(t, c.attachCalls, 1)
	assert.Equal(t, "api", c.attachCalls[0].ServiceName)
	assert.Equal(t, "api.handleCheckout", c.attachCalls[0].FunctionName)
	assert.Equal(t, 15*time.Second, c.attachCalls[0].Duration.AsDuration())
	assert.Equal(t, AttachAttached, result.Attach.Status)
	assert.Equal(t, "sess-123", result.Attach.SessionID)
}

func TestDiagnose_AttachRequestedButNoCandidate_Skipped(t *testing.T) {
	summary := summaryResult("api", "critical", 50, 900)
	c := &fakeClient{summaryResp: &colonypb.QueryUnifiedSummaryResponse{Summaries: []*colonypb.UnifiedSummaryResult{summary}}}
	result, err := diagnose(context.Background(), c, Options{Service: "api", Attach: true})
	require.NoError(t, err)
	assert.Equal(t, CandidateNotFound, result.CandidateStatus)
	assert.Equal(t, AttachSkipped, result.Attach.Status)
	assert.Empty(t, c.attachCalls)
}

func TestDiagnose_AttachFails(t *testing.T) {
	t.Run("RPC error", func(t *testing.T) {
		summary := summaryResult("api", "critical", 50, 900)
		summary.Issues = []string{"errors spiking"}
		c := &fakeClient{
			summaryResp: &colonypb.QueryUnifiedSummaryResponse{Summaries: []*colonypb.UnifiedSummaryResult{summary}},
			queryFunctionsResp: map[string]*colonypb.QueryFunctionsResponse{
				"errors spiking": {Results: []*colonypb.FunctionResult{functionResult("api.f", "svc/f.go", 1, true, false)}},
			},
			attachErr: errors.New("permission denied"),
		}
		result, err := diagnose(context.Background(), c, Options{Service: "api", Attach: true})
		require.NoError(t, err, "attach failures are structured partial results, not Go errors")
		assert.Equal(t, AttachFailed, result.Attach.Status)
		assert.NotEmpty(t, result.Attach.Error)
		assert.Empty(t, result.Attach.SessionID)
	})

	t.Run("server-reported failure", func(t *testing.T) {
		summary := summaryResult("api", "critical", 50, 900)
		summary.Issues = []string{"errors spiking"}
		c := &fakeClient{
			summaryResp: &colonypb.QueryUnifiedSummaryResponse{Summaries: []*colonypb.UnifiedSummaryResult{summary}},
			queryFunctionsResp: map[string]*colonypb.QueryFunctionsResponse{
				"errors spiking": {Results: []*colonypb.FunctionResult{functionResult("api.f", "svc/f.go", 1, true, false)}},
			},
			attachResp: &colonypb.AttachUprobeResponse{Success: false, Error: "max probes reached"},
		}
		result, err := diagnose(context.Background(), c, Options{Service: "api", Attach: true})
		require.NoError(t, err)
		assert.Equal(t, AttachFailed, result.Attach.Status)
		assert.Equal(t, "max probes reached", result.Attach.Error)
	})
}

func TestFormatText_SurfacesFailuresProminently(t *testing.T) {
	result := &Result{
		Service:         "api",
		Outcome:         OutcomeDegraded,
		Summary:         &SummarySnapshot{Status: "critical", ErrorRate: 50, AvgLatencyMs: 900},
		CandidateStatus: CandidateFound,
		CandidateFunction: &CandidateFunction{
			Name: "api.handleCheckout", File: "svc/checkout.go", Line: 118, Source: SourceProfilingHotPath,
		},
		Attach: AttachOutcome{Status: AttachFailed, Error: "permission denied"},
	}
	text := formatText(result)
	assert.Contains(t, text, "FAILED")
	assert.Contains(t, text, "permission denied")
	assert.NotContains(t, text, "Attach: attached", "a failed attachment must never be described as successful")
}
