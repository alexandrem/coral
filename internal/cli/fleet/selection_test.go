package fleet

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/config"
)

type selectionColony struct {
	colonyv1connect.UnimplementedColonyServiceHandler
}

func (*selectionColony) GetStatus(context.Context, *connect.Request[colonyv1.GetStatusRequest]) (*connect.Response[colonyv1.GetStatusResponse], error) {
	return connect.NewResponse(&colonyv1.GetStatusResponse{ColonyId: "fleet-test"}), nil
}

func (*selectionColony) ListAgents(context.Context, *connect.Request[colonyv1.ListAgentsRequest]) (*connect.Response[colonyv1.ListAgentsResponse], error) {
	return connect.NewResponse(&colonyv1.ListAgentsResponse{Agents: []*colonyv1.Agent{
		{AgentId: "one", Status: "healthy", Services: []*meshv1.ServiceInfo{{Name: "api"}}, RuntimeContext: &agentv1.RuntimeContextResponse{RuntimeType: agentv1.RuntimeContext_RUNTIME_CONTEXT_DOCKER, Version: "v1"}},
		{AgentId: "two", Status: "degraded", Services: []*meshv1.ServiceInfo{{Name: "api"}}},
		{AgentId: "three", Status: "unhealthy"},
	}}), nil
}

func TestPreparePinsSelectionAndRetryIdentities(t *testing.T) {
	t.Setenv("CORAL_CONFIG", t.TempDir())
	t.Setenv("CORAL_COLONY_ENDPOINT", "")
	t.Chdir(t.TempDir())
	_, handler := colonyv1connect.NewColonyServiceHandler(&selectionColony{})
	server := httptest.NewServer(handler)
	defer server.Close()
	loader, err := config.NewLoader()
	require.NoError(t, err)
	cfg := config.DefaultColonyConfig("fleet-test", "app", "test")
	cfg.Remote.Endpoint = server.URL
	require.NoError(t, loader.SaveColonyConfig(cfg))
	options := testOptions()
	options.Selector = Selector{Statuses: []string{"healthy", "degraded"}, Services: []string{"api"}, Runtimes: []string{"docker"}, Versions: []string{"v1"}}
	plan, err := options.Prepare(context.Background(), "fleet-test", "test", struct{}{})
	require.NoError(t, err)
	require.Len(t, plan.Targets, 1)
	require.Equal(t, "one", plan.Targets[0].ID)

	options.Selector = Selector{IDs: []string{"one", "missing", "one"}}
	plan, err = options.Prepare(context.Background(), "fleet-test", "test", struct{}{})
	require.NoError(t, err)
	require.Len(t, plan.Targets, 2)
	require.Equal(t, "missing", plan.Targets[0].ID)
	require.Nil(t, plan.Targets[0].Agent)

	report := Report{Version: 1, Operation: "test", ColonyID: "fleet-test", Parameters: json.RawMessage(`{}`), Results: []Result{{AgentID: "one", Status: "failed"}, {AgentID: "two", Status: "succeeded"}}}
	options.RetryFailed = filepath.Join(t.TempDir(), "retry.json")
	require.NoError(t, saveReport(options.RetryFailed, report))
	options.Selector = Selector{}
	plan, err = options.Prepare(context.Background(), "", "test", struct{}{})
	require.NoError(t, err)
	require.Len(t, plan.Targets, 1)
	require.Equal(t, "one", plan.Targets[0].ID, "retry must not re-evaluate the original heartbeat filter")
	require.NotNil(t, plan.Targets[0].Previous)
	_, err = options.Prepare(context.Background(), "other-colony", "test", struct{}{})
	require.ErrorContains(t, err, "belongs to colony")
	options.Selector.IDs = []string{"two"}
	_, err = options.Prepare(context.Background(), "fleet-test", "test", struct{}{})
	require.ErrorContains(t, err, "cannot be combined")
}
