package query

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	agentpb "github.com/coral-mesh/coral/coral/agent/v1"
	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
)

type historicalCPUProfileServer struct {
	colonyv1connect.UnimplementedColonyDebugServiceHandler
	request  *colonypb.QueryHistoricalCPUProfileRequest
	response *colonypb.QueryHistoricalCPUProfileResponse
	err      error
}

func (s *historicalCPUProfileServer) QueryHistoricalCPUProfile(_ context.Context, req *connect.Request[colonypb.QueryHistoricalCPUProfileRequest]) (*connect.Response[colonypb.QueryHistoricalCPUProfileResponse], error) {
	s.request = req.Msg
	if s.err != nil {
		return nil, s.err
	}
	return connect.NewResponse(s.response), nil
}

func TestHistoricalCPUProfileFormats(t *testing.T) {
	for _, format := range []string{"svg", "folded"} {
		t.Run(format, func(t *testing.T) {
			server := &historicalCPUProfileServer{response: &colonypb.QueryHistoricalCPUProfileResponse{
				Success: true, TotalSamples: 10,
				Samples: []*agentpb.StackSample{{FrameNames: []string{"leaf<&", "worker", "main"}, Count: 7}, {FrameNames: []string{"other", "main"}, Count: 3}, {Count: 8}},
			}}
			_, handler := colonyv1connect.NewColonyDebugServiceHandler(server)
			httpServer := httptest.NewServer(handler)
			defer httpServer.Close()
			t.Setenv("CORAL_CONFIG", t.TempDir())
			t.Setenv("CORAL_COLONY_ENDPOINT", httpServer.URL)
			cmd := NewCPUProfileCmd()
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs([]string{"--service", "api", "--since", "2h", "--until", "1h", "--format", format})
			before := time.Now()
			require.NoError(t, cmd.Execute())
			require.Equal(t, "api", server.request.ServiceName)
			require.WithinDuration(t, before.Add(-2*time.Hour), server.request.StartTime.AsTime(), time.Second)
			require.WithinDuration(t, before.Add(-time.Hour), server.request.EndTime.AsTime(), time.Second)
			require.Contains(t, stderr.String(), "Total samples: 10")
			require.NotContains(t, stdout.String(), "Querying historical")
			if format == "folded" {
				require.Equal(t, "main;worker;leaf<& 7\nmain;other 3\n", stdout.String())
				return
			}
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
			require.InDelta(t, 826, svg.Frames[3].Rect.Width, 0.1)
		})
	}
}

func TestHistoricalCPUProfileErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response *colonypb.QueryHistoricalCPUProfileResponse
		err      error
		want     string
	}{
		{name: "no historical data", response: &colonypb.QueryHistoricalCPUProfileResponse{Success: true}},
		{name: "unresolved frames", response: &colonypb.QueryHistoricalCPUProfileResponse{Success: true, Samples: []*agentpb.StackSample{{Count: 10}}}, want: "no stack data"},
		{name: "failed query", response: &colonypb.QueryHistoricalCPUProfileResponse{Error: "storage unavailable"}, want: "storage unavailable"},
		{name: "RPC failure", err: connect.NewError(connect.CodeUnavailable, errors.New("colony offline")), want: "colony offline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := &historicalCPUProfileServer{response: tc.response, err: tc.err}
			_, handler := colonyv1connect.NewColonyDebugServiceHandler(server)
			httpServer := httptest.NewServer(handler)
			defer httpServer.Close()
			t.Setenv("CORAL_CONFIG", t.TempDir())
			t.Setenv("CORAL_COLONY_ENDPOINT", httpServer.URL)
			cmd := NewCPUProfileCmd()
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs([]string{"--service", "api", "--format", "svg"})
			err := cmd.Execute()
			if tc.want == "" {
				require.NoError(t, err)
				require.Contains(t, stderr.String(), "No historical CPU profile data")
			} else {
				require.ErrorContains(t, err, tc.want)
			}
			require.NotContains(t, stdout.String(), "<svg")
		})
	}
}

func TestHistoricalCPUProfileRejectsUnknownFormat(t *testing.T) {
	cmd := NewCPUProfileCmd()
	cmd.SetArgs([]string{"--service", "api", "--format", "svgg"})
	require.ErrorContains(t, cmd.Execute(), "unknown format")
}
