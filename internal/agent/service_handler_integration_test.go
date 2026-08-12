package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListServicesRPCIntegration verifies the RFD 105 surface through a real
// Connect RPC server and client, rather than by calling the handler directly.
func TestListServicesRPCIntegration(t *testing.T) {
	ctx := context.Background()
	agent, err := New(Config{
		Context: ctx,
		AgentID: "test-agent",
		Logger:  zerolog.Nop(),
	})
	require.NoError(t, err)
	require.NoError(t, agent.Start())
	t.Cleanup(func() { require.NoError(t, agent.Stop()) })

	path, rpcHandler := agentv1connect.NewAgentServiceHandler(
		NewServiceHandler(agent, nil, nil, nil, nil, nil, nil),
	)
	mux := http.NewServeMux()
	mux.Handle(path, rpcHandler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := agentv1connect.NewAgentServiceClient(http.DefaultClient, server.URL)

	// Simulate default-on observation of two services before either is watched.
	agent.onBeylaServiceObserved(3000, 12345, "node")
	agent.onBeylaServiceObserved(6379, 23456, "redis-server")

	initial, err := client.ListServices(ctx, connect.NewRequest(&agentv1.ListServicesRequest{}))
	require.NoError(t, err)
	require.Len(t, initial.Msg.Services, 2)
	for _, service := range initial.Msg.Services {
		assert.Equal(t, agentv1.ServiceNamingSource_SERVICE_NAMING_SOURCE_AUTO, service.NamingSource)
		assert.Zero(t, service.ObservationTier)
		assert.False(t, service.HasMonitor)
	}

	connected, err := client.ConnectService(ctx, connect.NewRequest(&agentv1.ConnectServiceRequest{
		Name:           "frontend",
		Port:           3000,
		HealthEndpoint: "/health",
	}))
	require.NoError(t, err)
	require.True(t, connected.Msg.Success)

	afterWatch, err := client.ListServices(ctx, connect.NewRequest(&agentv1.ListServicesRequest{}))
	require.NoError(t, err)
	require.Len(t, afterWatch.Msg.Services, 2)

	servicesByPort := make(map[int32]*agentv1.ServiceStatus, len(afterWatch.Msg.Services))
	for _, service := range afterWatch.Msg.Services {
		servicesByPort[service.Port] = service
	}

	frontend := servicesByPort[3000]
	require.NotNil(t, frontend)
	assert.Equal(t, "frontend", frontend.Name)
	assert.Equal(t, "frontend", frontend.AuthoritativeName)
	assert.Equal(t, agentv1.ServiceNamingSource_SERVICE_NAMING_SOURCE_AUTHORITATIVE, frontend.NamingSource)
	assert.True(t, frontend.HasMonitor)
	assert.EqualValues(t, TierWatched, frontend.ObservationTier)

	redis := servicesByPort[6379]
	require.NotNil(t, redis)
	assert.Equal(t, agentv1.ServiceNamingSource_SERVICE_NAMING_SOURCE_AUTO, redis.NamingSource)
	assert.False(t, redis.HasMonitor)

	autoSource := agentv1.ServiceNamingSource_SERVICE_NAMING_SOURCE_AUTO
	autoOnly, err := client.ListServices(ctx, connect.NewRequest(&agentv1.ListServicesRequest{
		SourceFilter: &autoSource,
	}))
	require.NoError(t, err)
	require.Len(t, autoOnly.Msg.Services, 1)
	assert.Equal(t, int32(6379), autoOnly.Msg.Services[0].Port)
}
