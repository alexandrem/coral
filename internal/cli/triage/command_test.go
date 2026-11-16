package triage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/auth"
	"github.com/coral-mesh/coral/internal/colony/httpapi"
)

func TestValidateFlags(t *testing.T) {
	t.Run("valid text format, no attach", func(t *testing.T) {
		assert.NoError(t, validateFlags("text", false, false))
	})

	t.Run("valid json format with attach", func(t *testing.T) {
		assert.NoError(t, validateFlags("json", true, true))
	})

	t.Run("unsupported format", func(t *testing.T) {
		err := validateFlags("csv", false, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported format")
	})

	t.Run("attach-duration without attach is an argument error", func(t *testing.T) {
		err := validateFlags("text", false, true)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--attach-duration requires --attach")
	})

	t.Run("attach-duration with attach is valid", func(t *testing.T) {
		assert.NoError(t, validateFlags("text", true, true))
	})
}

// triageRPCServer exercises the real CLI client, Connect routes and HTTP RBAC
// without requiring Linux probes or a running colony.
type triageRPCServer struct {
	colonyv1connect.UnimplementedColonyServiceHandler
	colonyv1connect.UnimplementedColonyDebugServiceHandler
	summaryCalls, queryCalls, attachCalls int
	attachedDuration                      time.Duration
}

func (s *triageRPCServer) QueryUnifiedSummary(_ context.Context, req *connect.Request[colonypb.QueryUnifiedSummaryRequest]) (*connect.Response[colonypb.QueryUnifiedSummaryResponse], error) {
	s.summaryCalls++
	if req.Msg.TimeRange == "invalid" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid time range"))
	}
	summary := summaryResult("api", "critical", 50, 900)
	summary.ProfilingSummary = &colonypb.ProfilingSummary{
		TopCpuHotspots: []*colonypb.CPUHotspot{{Frames: []string{"checkout.Handle", "main.serve"}}},
	}
	return connect.NewResponse(&colonypb.QueryUnifiedSummaryResponse{Summaries: []*colonypb.UnifiedSummaryResult{summary}}), nil
}

func (s *triageRPCServer) QueryFunctions(_ context.Context, req *connect.Request[colonypb.QueryFunctionsRequest]) (*connect.Response[colonypb.QueryFunctionsResponse], error) {
	s.queryCalls++
	if req.Msg.ServiceName != "api" || req.Msg.Query != "checkout.Handle" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unexpected candidate lookup: %v", req.Msg))
	}
	return connect.NewResponse(&colonypb.QueryFunctionsResponse{Results: []*colonypb.FunctionResult{
		functionResult("other.Handle", "other.go", 2, true, false),
		functionResult("example.com/app/checkout.Handle", "checkout.go", 118, true, false),
	}}), nil
}

func (s *triageRPCServer) AttachUprobe(_ context.Context, req *connect.Request[colonypb.AttachUprobeRequest]) (*connect.Response[colonypb.AttachUprobeResponse], error) {
	s.attachCalls++
	s.attachedDuration = req.Msg.Duration.AsDuration()
	if req.Msg.ServiceName != "api" || req.Msg.FunctionName != "example.com/app/checkout.Handle" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unexpected probe target: %v", req.Msg))
	}
	return connect.NewResponse(&colonypb.AttachUprobeResponse{Success: true, SessionId: "session-123"}), nil
}

func TestCommandConnectAndRBAC(t *testing.T) {
	for _, tt := range []struct {
		name        string
		permissions []auth.Permission
		args        []string
		wantAttach  string
		wantError   string
		wantCalls   [3]int
	}{
		{"read only", []auth.Permission{auth.PermissionQuery}, nil, AttachNotRequested, "", [3]int{1, 1, 0}},
		{"attach", []auth.Permission{auth.PermissionQuery, auth.PermissionDebug}, []string{"--attach", "--attach-duration", "1h"}, AttachAttached, "", [3]int{1, 1, 1}},
		{"attach denied", []auth.Permission{auth.PermissionQuery}, []string{"--attach"}, AttachFailed, "", [3]int{1, 1, 0}},
		{"summary denied", []auth.Permission{auth.PermissionStatus}, nil, "", "permission_denied", [3]int{0, 0, 0}},
		{"invalid time range", []auth.Permission{auth.PermissionQuery}, []string{"--since", "invalid"}, "", "invalid_argument", [3]int{1, 0, 0}},
		{"zero duration", []auth.Permission{auth.PermissionQuery}, []string{"--attach", "--attach-duration", "0s"}, "", "must be positive", [3]int{}},
		{"negative duration", []auth.Permission{auth.PermissionQuery}, []string{"--attach", "--attach-duration=-1s"}, "", "must be positive", [3]int{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rpc := &triageRPCServer{}
			mux := http.NewServeMux()
			mux.Handle(colonyv1connect.NewColonyServiceHandler(rpc))
			mux.Handle(colonyv1connect.NewColonyDebugServiceHandler(rpc))
			store := auth.NewTokenStore("")
			token, err := store.GenerateToken("test", tt.permissions, "")
			require.NoError(t, err)
			logger := zerolog.Nop()
			handler := httpapi.NewAuthMiddleware(store, logger).Handler(httpapi.NewRBACMiddleware(logger).Handler(mux))
			server := httptest.NewServer(handler)
			t.Cleanup(server.Close)
			t.Setenv("CORAL_CONFIG", t.TempDir())
			t.Setenv("CORAL_COLONY_ENDPOINT", server.URL)
			t.Setenv("CORAL_API_TOKEN", token.Token)
			var output bytes.Buffer
			cmd := NewCmd()
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SetArgs(append([]string{"api", "--format", "json"}, tt.args...))
			err = cmd.ExecuteContext(t.Context())
			assert.Equal(t, tt.wantCalls, [3]int{rpc.summaryCalls, rpc.queryCalls, rpc.attachCalls})
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				return
			}
			require.NoError(t, err)
			var result Result
			require.NoError(t, json.Unmarshal(output.Bytes(), &result))
			assert.Equal(t, OutcomeDegraded, result.Outcome)
			assert.Equal(t, CandidateFound, result.CandidateStatus)
			require.NotNil(t, result.CandidateFunction)
			assert.Equal(t, "example.com/app/checkout.Handle", result.CandidateFunction.Name)
			assert.Equal(t, SourceProfilingHotPath, result.CandidateFunction.Source)
			assert.Equal(t, tt.wantAttach, result.Attach.Status)
			switch tt.wantAttach {
			case AttachAttached:
				assert.Equal(t, "session-123", result.Attach.SessionID)
				assert.Equal(t, MaxAttachDuration, rpc.attachedDuration)
			case AttachFailed:
				assert.Contains(t, result.Attach.Error, "permission_denied")
				assert.Empty(t, result.Attach.SessionID)
			}
		})
	}
}

func TestCommandHonorsCancellation(t *testing.T) {
	t.Setenv("CORAL_COLONY_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("CORAL_CONFIG", t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cmd := NewCmd()
	cmd.SetArgs([]string{"api", "--format", "json"})
	err := cmd.ExecuteContext(ctx)
	require.ErrorContains(t, err, "canceled")
}

func TestNewCmd_FlagDefaults(t *testing.T) {
	cmd := NewCmd()
	assert.Equal(t, "triage [service]", cmd.Use)

	sinceFlag := cmd.Flags().Lookup("since")
	require.NotNil(t, sinceFlag)
	assert.Equal(t, DefaultSince, sinceFlag.DefValue)

	attachDurationFlag := cmd.Flags().Lookup("attach-duration")
	require.NotNil(t, attachDurationFlag)
	assert.Equal(t, DefaultAttachDuration.String(), attachDurationFlag.DefValue)

	formatFlag := cmd.Flags().Lookup("format")
	require.NotNil(t, formatFlag)
	assert.Equal(t, "text", formatFlag.DefValue)

	attachFlag := cmd.Flags().Lookup("attach")
	require.NotNil(t, attachFlag)
	assert.Equal(t, "false", attachFlag.DefValue)
}

func TestNewCmd_ArgsAcceptsZeroOrOneService(t *testing.T) {
	cmd := NewCmd()
	assert.NoError(t, cmd.Args(cmd, []string{}))
	assert.NoError(t, cmd.Args(cmd, []string{"api"}))
	assert.Error(t, cmd.Args(cmd, []string{"api", "extra"}))
}

func TestFormatText_Outcomes(t *testing.T) {
	t.Run("no_data", func(t *testing.T) {
		text := formatText(&Result{Service: "api", Outcome: OutcomeNoData, Attach: AttachOutcome{Status: AttachNotRequested}})
		assert.Contains(t, text, "No data available")
	})

	t.Run("nothing_degraded", func(t *testing.T) {
		text := formatText(&Result{Service: "", Outcome: OutcomeNothingDegraded, Attach: AttachOutcome{Status: AttachNotRequested}})
		assert.Contains(t, text, "No degraded or critical services")
	})

	t.Run("healthy", func(t *testing.T) {
		text := formatText(&Result{Service: "api", Outcome: OutcomeHealthy, Attach: AttachOutcome{Status: AttachNotRequested}})
		assert.Contains(t, text, "healthy")
	})

	t.Run("degraded with candidate and not-requested attach", func(t *testing.T) {
		text := formatText(&Result{
			Service:           "api",
			Outcome:           OutcomeDegraded,
			Summary:           &SummarySnapshot{Status: "critical", ErrorRate: 4.2, AvgLatencyMs: 892},
			CandidateStatus:   CandidateFound,
			CandidateFunction: &CandidateFunction{Name: "api.handleCheckout", File: "service/checkout.go", Line: 118, Source: SourceProfilingHotPath},
			Attach:            AttachOutcome{Status: AttachNotRequested},
		})
		assert.Contains(t, text, "api.handleCheckout")
		assert.Contains(t, text, "service/checkout.go:118")
		assert.Contains(t, text, "profiling hot path")
		assert.Contains(t, text, "not requested")
	})
}
