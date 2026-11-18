package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	networkv1 "github.com/coral-mesh/coral/coral/network/v1"
)

type snapshotAgent struct {
	agentv1connect.UnimplementedAgentServiceHandler
	services []*agentv1.ServiceStatus
	err      error
	runtime  *agentv1.RuntimeContextResponse
}

func (a *snapshotAgent) ListServices(context.Context, *connect.Request[agentv1.ListServicesRequest]) (*connect.Response[agentv1.ListServicesResponse], error) {
	if a.err != nil {
		return nil, a.err
	}
	return connect.NewResponse(&agentv1.ListServicesResponse{Services: a.services}), nil
}

func (a *snapshotAgent) GetRuntimeContext(context.Context, *connect.Request[agentv1.GetRuntimeContextRequest]) (*connect.Response[agentv1.RuntimeContextResponse], error) {
	if a.runtime == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("runtime context unavailable"))
	}
	return connect.NewResponse(a.runtime), nil
}

func TestRefreshAgentRuntime(t *testing.T) {
	registered := &agentv1.RuntimeContextResponse{
		RuntimeType: agentv1.RuntimeContext_RUNTIME_CONTEXT_DOCKER,
		Version:     "v1",
	}
	live := &agentv1.RuntimeContextResponse{
		RuntimeType: agentv1.RuntimeContext_RUNTIME_CONTEXT_NATIVE,
		Version:     "v2",
		Wireguard:   &networkv1.MeshTelemetry{},
	}
	for _, tt := range []struct {
		name       string
		registered *agentv1.RuntimeContextResponse
		live       *agentv1.RuntimeContextResponse
		want       *agentv1.RuntimeContextResponse
	}{
		{name: "restored enrollment without runtime context", live: live, want: live},
		{name: "agent upgrade with stale registration", registered: registered, live: live, want: live},
		{name: "runtime RPC unavailable", registered: registered, want: registered},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, handler := agentv1connect.NewAgentServiceHandler(&snapshotAgent{runtime: tt.live})
			server := httptest.NewServer(handler)
			defer server.Close()
			agent := &colonyv1.Agent{RuntimeContext: tt.registered}

			refreshAgentRuntime(context.Background(), agent, agentv1connect.NewAgentServiceClient(server.Client(), server.URL))

			require.NotNil(t, agent.RuntimeContext)
			require.Equal(t, tt.want.GetRuntimeType(), agent.RuntimeContext.GetRuntimeType())
			require.Equal(t, tt.want.GetVersion(), agent.RuntimeContext.GetVersion())
			require.Nil(t, agent.RuntimeContext.GetWireguard())
		})
	}
}

func TestServiceSnapshotDoesNotConfuseChecksWithHeartbeat(t *testing.T) {
	checkTime := timestamppb.New(time.Now().Add(-time.Minute))
	for _, tt := range []struct {
		name, heartbeat string
		err             error
		live            bool
	}{
		{"passing query, failing check", "healthy", nil, true},
		{"query unavailable", "healthy", connect.NewError(connect.CodeUnavailable, errors.New("agent unreachable")), false},
		{"unhealthy heartbeat skips live query", "unhealthy", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mock := &snapshotAgent{services: []*agentv1.ServiceStatus{{Name: "api", HasMonitor: true, Status: "unhealthy", LastCheck: checkTime}}, err: tt.err}
			_, handler := agentv1connect.NewAgentServiceHandler(mock)
			server := httptest.NewServer(handler)
			defer server.Close()
			agent := &colonyv1.Agent{AgentId: "agent-1", Status: tt.heartbeat, Services: []*meshv1.ServiceInfo{{Name: "cached-name"}}}
			refreshAgentServices(context.Background(), agent, agentv1connect.NewAgentServiceClient(http.DefaultClient, server.URL))
			require.Equal(t, tt.heartbeat, agent.Status)
			if tt.live {
				require.Equal(t, "live", agent.ServicesSource)
				require.Empty(t, agent.ServicesError)
				require.NotNil(t, agent.ServicesObservedAt)
				require.Equal(t, "api", agent.Services[0].Name)
				require.Equal(t, "unhealthy", agent.ServiceStatuses[0].Status)
				require.Equal(t, checkTime.AsTime(), agent.ServiceStatuses[0].LastCheck.AsTime())
			} else {
				require.Equal(t, "cached", agent.ServicesSource)
				require.NotEmpty(t, agent.ServicesError)
				require.Nil(t, agent.ServicesObservedAt, "a heartbeat must not be used as a service observation timestamp")
				require.Empty(t, agent.ServiceStatuses)
				require.Equal(t, "cached-name", agent.Services[0].Name)
			}
		})
	}
}
