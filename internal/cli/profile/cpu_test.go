package profile

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	agentpb "github.com/coral-mesh/coral/coral/agent/v1"
	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
)

type cpuProfileServer struct {
	colonyv1connect.UnimplementedColonyDebugServiceHandler
	request  *colonypb.ProfileCPURequest
	response *colonypb.ProfileCPUResponse
	err      error
}

func (s *cpuProfileServer) ProfileCPU(_ context.Context, req *connect.Request[colonypb.ProfileCPURequest]) (*connect.Response[colonypb.ProfileCPUResponse], error) {
	s.request = req.Msg
	if s.err != nil {
		return nil, s.err
	}
	return connect.NewResponse(s.response), nil
}

func TestCPUCommandSVG(t *testing.T) {
	server := &cpuProfileServer{response: &colonypb.ProfileCPUResponse{
		Success: true, TotalSamples: 10,
		Samples: []*agentpb.StackSample{
			{FrameNames: []string{"leaf<&", "worker", "main"}, Count: 7},
			{FrameNames: []string{"other", "main"}, Count: 3},
			{Count: 8},
		},
	}}
	_, handler := colonyv1connect.NewColonyDebugServiceHandler(server)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	t.Setenv("CORAL_CONFIG", t.TempDir())
	t.Setenv("CORAL_COLONY_ENDPOINT", httpServer.URL)
	cmd := NewCPUCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--service", "api", "--pod", "api-1", "--duration", "1", "--frequency", "49", "--format", "svg"})
	require.NoError(t, cmd.Execute())
	require.NotNil(t, server.request)
	require.Equal(t, "api", server.request.ServiceName)
	require.Equal(t, "api-1", server.request.PodName)
	require.EqualValues(t, 1, server.request.DurationSeconds)
	require.EqualValues(t, 49, server.request.FrequencyHz)
	require.Contains(t, stderr.String(), "Profiling CPU")
	require.NotContains(t, stdout.String(), "Profiling CPU")
	var svg struct {
		XMLName xml.Name `xml:"svg"`
		Frames  []struct {
			Title string `xml:"title"`
			Rect  struct {
				Y     float64 `xml:"y,attr"`
				Width float64 `xml:"width,attr"`
			} `xml:"rect"`
		} `xml:"g"`
	}
	require.NoError(t, xml.Unmarshal(stdout.Bytes(), &svg))
	require.Len(t, svg.Frames, 4)
	require.Equal(t, "main (10 samples, 100.00%)", svg.Frames[0].Title)
	require.Equal(t, "worker (7 samples, 70.00%)", svg.Frames[2].Title)
	require.Equal(t, "leaf<& (7 samples, 70.00%)", svg.Frames[3].Title)
	require.Greater(t, svg.Frames[0].Rect.Y, svg.Frames[2].Rect.Y)
	require.Greater(t, svg.Frames[2].Rect.Y, svg.Frames[3].Rect.Y)
	require.InDelta(t, 1180, svg.Frames[0].Rect.Width, 0.1)
	require.InDelta(t, 826, svg.Frames[3].Rect.Width, 0.1)
}

func TestCPUCommandSVGErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response *colonypb.ProfileCPUResponse
		err      error
		want     string
	}{
		{name: "empty profile", response: &colonypb.ProfileCPUResponse{Success: true}, want: "no stack data"},
		{name: "unresolved frames", response: &colonypb.ProfileCPUResponse{Success: true, Samples: []*agentpb.StackSample{{Count: 10}}}, want: "no stack data"},
		{name: "failed capture", response: &colonypb.ProfileCPUResponse{Error: "perf unavailable"}, want: "perf unavailable"},
		{name: "RPC failure", err: connect.NewError(connect.CodeUnavailable, errors.New("agent offline")), want: "agent offline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := &cpuProfileServer{response: tc.response, err: tc.err}
			_, handler := colonyv1connect.NewColonyDebugServiceHandler(server)
			httpServer := httptest.NewServer(handler)
			defer httpServer.Close()
			t.Setenv("CORAL_CONFIG", t.TempDir())
			t.Setenv("CORAL_COLONY_ENDPOINT", httpServer.URL)
			cmd := NewCPUCmd()
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs([]string{"--service", "api", "--duration", "1", "--format", "svg"})
			require.ErrorContains(t, cmd.Execute(), tc.want)
			require.NotContains(t, stdout.String(), "<svg")
		})
	}
}

func TestCPUCommandRejectsUnknownFormat(t *testing.T) {
	cmd := NewCPUCmd()
	cmd.SetArgs([]string{"--service", "api", "--format", "svgg"})
	require.ErrorContains(t, cmd.Execute(), "unknown format")
}

func TestCPUSamplesToFoldedPreservesInput(t *testing.T) {
	frames := []string{"leaf", "worker", "main"}
	stacks := cpuSamplesToFolded([]*agentpb.StackSample{{FrameNames: frames, Count: 7}, {Count: 10}})
	require.Len(t, stacks, 1)
	require.Equal(t, []string{"main", "worker", "leaf"}, stacks[0].Frames)
	require.EqualValues(t, 7, stacks[0].Value)
	require.Equal(t, []string{"leaf", "worker", "main"}, frames)
	stacks[0].Frames[0] = "changed"
	require.Equal(t, "main", frames[2], "converted frames must not alias the input")
}
