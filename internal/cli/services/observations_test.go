package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/config"
)

func TestServiceObservationsSeparateChecksFromRegistration(t *testing.T) {
	checkedAt := timestamppb.New(time.Now().Add(-45 * time.Minute))
	active := colonyv1.ServiceStatus_SERVICE_STATUS_ACTIVE
	service := &colonyv1.ServiceSummary{Name: "api", InstanceCount: 2, Status: &active}
	agents := []*colonyv1.Agent{
		{AgentId: "live", Status: "healthy", ServicesSource: "live", Services: []*meshv1.ServiceInfo{{Name: "api"}}, ServiceStatuses: []*agentv1.ServiceStatus{{Name: "api", HasMonitor: true, Status: "unhealthy", LastCheck: checkedAt}}},
		{AgentId: "cached", Status: "healthy", ServicesSource: "cached", Services: []*meshv1.ServiceInfo{{Name: "api"}}},
	}
	view := observeService(service, agents)
	require.Equal(t, "mixed", view.DataSource)
	require.Equal(t, "unhealthy", view.Health, "active registration and healthy heartbeat must not override a failing service check")
	require.Equal(t, 1, view.Checks.Failing)
	require.Equal(t, 1, view.Checks.Unknown)
	require.Same(t, checkedAt, view.Checks.LastCheck)
	require.Equal(t, []string{"cached", "live"}, view.AgentIDs)

	view = observeService(service, agents[1:])
	require.Equal(t, "unknown", view.Health)
	require.Equal(t, "cached", view.DataSource)
	require.Equal(t, 2, view.Checks.Unknown)
	require.Nil(t, view.Checks.LastCheck)

	service.Source = colonyv1.ServiceSource_SERVICE_SOURCE_OBSERVED
	view = observeService(service, nil)
	require.Equal(t, "telemetry", view.DataSource)
	require.Equal(t, "unknown", view.Health)

	data, err := json.Marshal(view)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, "api", decoded["name"], "preserve existing service fields in JSON")
	require.Equal(t, "unknown", decoded["health"])
}

type inventoryColony struct {
	colonyv1connect.UnimplementedColonyServiceHandler
	window string
}

func (s *inventoryColony) GetStatus(context.Context, *connect.Request[colonyv1.GetStatusRequest]) (*connect.Response[colonyv1.GetStatusResponse], error) {
	return connect.NewResponse(&colonyv1.GetStatusResponse{}), nil
}

func (s *inventoryColony) ListServices(_ context.Context, req *connect.Request[colonyv1.ListServicesRequest]) (*connect.Response[colonyv1.ListServicesResponse], error) {
	s.window = req.Msg.TimeRange
	return connect.NewResponse(&colonyv1.ListServicesResponse{Services: []*colonyv1.ServiceSummary{{Name: "api", InstanceCount: 2}, {Name: "cache", InstanceCount: 1}}}), nil
}

func (s *inventoryColony) ListAgents(context.Context, *connect.Request[colonyv1.ListAgentsRequest]) (*connect.Response[colonyv1.ListAgentsResponse], error) {
	return nil, connect.NewError(connect.CodeUnavailable, errors.New("live lookup failed"))
}

func TestInventoryKeepsServicesWhenLiveLookupFails(t *testing.T) {
	t.Setenv("CORAL_CONFIG", t.TempDir())
	t.Setenv("CORAL_COLONY_ENDPOINT", "")
	t.Chdir(t.TempDir())
	mock := &inventoryColony{}
	_, handler := colonyv1connect.NewColonyServiceHandler(mock)
	server := httptest.NewServer(handler)
	defer server.Close()
	loader, err := config.NewLoader()
	require.NoError(t, err)
	cfg := config.DefaultColonyConfig("inventory", "app", "prod")
	cfg.Remote.Endpoint = server.URL
	require.NoError(t, loader.SaveColonyConfig(cfg))
	for _, format := range []string{"json", "table"} {
		cmd := NewCmd()
		var output, stderr bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"list", "--colony", "inventory", "--name", "api", "--since", "24h", "--format", format})
		require.NoError(t, cmd.Execute())
		require.Equal(t, "24h", mock.window)
		if format == "json" {
			var result struct {
				Services []serviceObservation `json:"services"`
				Warnings []string             `json:"warnings"`
				Window   string               `json:"telemetry_window"`
			}
			require.NoError(t, json.Unmarshal(output.Bytes(), &result))
			require.Len(t, result.Services, 1)
			require.Equal(t, "api", result.Services[0].Name)
			require.Equal(t, "unknown", result.Services[0].Health)
			require.Equal(t, "24h", result.Window)
			require.Len(t, result.Warnings, 1)
			require.Empty(t, stderr.String(), "JSON must carry warnings without adding prose")
		} else {
			require.Contains(t, output.String(), "api")
			require.NotContains(t, output.String(), "cache")
			require.Contains(t, output.String(), "HEALTH CHECK")
			require.Contains(t, stderr.String(), "live lookup failed")
		}
	}
}
