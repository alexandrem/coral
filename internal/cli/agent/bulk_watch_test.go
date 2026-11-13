package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/cli/fleet"
)

type bulkAgent struct {
	agentv1connect.UnimplementedAgentServiceHandler
	mu                  sync.Mutex
	calls               map[string]int
	registered          []*agentv1.ServiceStatus
	commitBeforeFailure bool
	ackOnly             bool
	denied              string
}

func (a *bulkAgent) ListServices(context.Context, *connect.Request[agentv1.ListServicesRequest]) (*connect.Response[agentv1.ListServicesResponse], error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return connect.NewResponse(&agentv1.ListServicesResponse{Services: append([]*agentv1.ServiceStatus{}, a.registered...)}), nil
}

func (a *bulkAgent) ConnectService(_ context.Context, req *connect.Request[agentv1.ConnectServiceRequest]) (*connect.Response[agentv1.ConnectServiceResponse], error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls[req.Msg.Name]++
	if req.Msg.Name == a.denied {
		return connect.NewResponse(&agentv1.ConnectServiceResponse{Success: false, Error: "registration denied"}), nil
	}
	if a.ackOnly {
		return connect.NewResponse(&agentv1.ConnectServiceResponse{Success: true}), nil
	}
	if a.commitBeforeFailure {
		a.registered = append(a.registered, &agentv1.ServiceStatus{Name: req.Msg.Name, Port: req.Msg.Port, NamingSource: agentv1.ServiceNamingSource_SERVICE_NAMING_SOURCE_AUTHORITATIVE})
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("response lost after registration"))
	}
	if req.Msg.Name == "second" && a.calls[req.Msg.Name] == 1 {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("temporary outage"))
	}
	a.registered = append(a.registered, &agentv1.ServiceStatus{Name: req.Msg.Name, Port: req.Msg.Port, HealthEndpoint: req.Msg.HealthEndpoint, ExePattern: req.Msg.ExePattern, ServiceType: req.Msg.ServiceType, Labels: req.Msg.Labels, HasMonitor: req.Msg.HealthEndpoint != "" || req.Msg.ExePattern != "", NamingSource: agentv1.ServiceNamingSource_SERVICE_NAMING_SOURCE_AUTHORITATIVE})
	return connect.NewResponse(&agentv1.ConnectServiceResponse{Success: true}), nil
}

func TestBulkWatchRetriesOnlyUnfinishedServiceRegistrations(t *testing.T) {
	mock := &bulkAgent{calls: map[string]int{}}
	_, handler := agentv1connect.NewAgentServiceHandler(mock)
	server := httptest.NewServer(handler)
	defer server.Close()
	client := agentv1connect.NewAgentServiceClient(http.DefaultClient, server.URL)
	requests := []*agentv1.ConnectServiceRequest{{Name: "first", Port: 8080}, {Name: "second", Port: 9090}}
	action := watchAction(requests, func(fleet.Target) (agentv1connect.AgentServiceClient, error) { return client, nil })
	plan := &fleet.Plan{Operation: "services watch", ColonyID: "test", Parameters: json.RawMessage(`[]`), Targets: []fleet.Target{{ID: "one", Agent: &colonyv1.Agent{AgentId: "one"}}}}
	report := plan.Run(context.Background(), fleet.Options{Concurrency: 1, Retries: 1, RequestTimeout: time.Second}, action)
	require.Equal(t, 1, report.Summary.Succeeded)
	require.Equal(t, 2, report.Results[0].Attempts)
	require.Equal(t, map[string]int{"first": 1, "second": 2}, mock.calls)
	var progress watchProgress
	require.NoError(t, json.Unmarshal(report.Results[0].Data, &progress))
	for _, service := range progress.Services {
		require.Equal(t, "connected", service.Status)
	}
}

func TestBulkWatchReconcilesAnUnacknowledgedRegistration(t *testing.T) {
	mock := &bulkAgent{calls: map[string]int{}, commitBeforeFailure: true}
	_, handler := agentv1connect.NewAgentServiceHandler(mock)
	server := httptest.NewServer(handler)
	defer server.Close()
	client := agentv1connect.NewAgentServiceClient(http.DefaultClient, server.URL)
	action := watchAction([]*agentv1.ConnectServiceRequest{{Name: "api", Port: 8080}}, func(fleet.Target) (agentv1connect.AgentServiceClient, error) { return client, nil })
	target := fleet.Target{ID: "one", Agent: &colonyv1.Agent{AgentId: "one"}}
	_, previous, err := action(context.Background(), target, nil)
	require.Error(t, err)
	_, data, err := action(context.Background(), target, previous)
	require.NoError(t, err)
	require.Equal(t, 1, mock.calls["api"], "do not repeat a registration when the first response was lost")
	var progress watchProgress
	require.NoError(t, json.Unmarshal(data, &progress))
	require.Equal(t, "unchanged", progress.Services[0].Status)
}

func TestBulkWatchConflictingConfigurationDoesNotReportSuccess(t *testing.T) {
	mock := &bulkAgent{calls: map[string]int{}, registered: []*agentv1.ServiceStatus{{Name: "api", Port: 8080, HealthEndpoint: "/old", HasMonitor: true, NamingSource: agentv1.ServiceNamingSource_SERVICE_NAMING_SOURCE_AUTHORITATIVE}}}
	_, handler := agentv1connect.NewAgentServiceHandler(mock)
	server := httptest.NewServer(handler)
	defer server.Close()
	client := agentv1connect.NewAgentServiceClient(http.DefaultClient, server.URL)
	action := watchAction([]*agentv1.ConnectServiceRequest{{Name: "api", Port: 8080, HealthEndpoint: "/new"}}, func(fleet.Target) (agentv1connect.AgentServiceClient, error) { return client, nil })
	_, data, err := action(context.Background(), fleet.Target{ID: "one"}, nil)
	require.ErrorContains(t, err, "conflicts")
	require.Empty(t, mock.calls)
	var progress watchProgress
	require.NoError(t, json.Unmarshal(data, &progress))
	require.Equal(t, "failed", progress.Services[0].Status)
}

func TestBulkWatchVerifiesAcknowledgedConfigurationAndPortConflicts(t *testing.T) {
	for _, ackOnly := range []bool{true, false} {
		t.Run(fmt.Sprint(ackOnly), func(t *testing.T) {
			mock := &bulkAgent{calls: map[string]int{}, ackOnly: ackOnly}
			_, handler := agentv1connect.NewAgentServiceHandler(mock)
			server := httptest.NewServer(handler)
			defer server.Close()
			client := agentv1connect.NewAgentServiceClient(http.DefaultClient, server.URL)
			requests := []*agentv1.ConnectServiceRequest{{Name: "first", Port: 8080}}
			if !ackOnly {
				requests = append(requests, &agentv1.ConnectServiceRequest{Name: "second", Port: 8080})
			}
			action := watchAction(requests, func(fleet.Target) (agentv1connect.AgentServiceClient, error) { return client, nil })
			_, _, err := action(context.Background(), fleet.Target{ID: "one"}, nil)
			require.Error(t, err)
			if ackOnly {
				require.ErrorContains(t, err, "not present")
			} else {
				require.ErrorContains(t, err, "conflicts")
				require.Zero(t, mock.calls["second"], "refresh the snapshot before configuring the next service")
			}
		})
	}
}

func TestBulkWatchKeepsPermanentServiceErrorsOutOfAutomaticRetries(t *testing.T) {
	mock := &bulkAgent{calls: map[string]int{}, denied: "denied"}
	_, handler := agentv1connect.NewAgentServiceHandler(mock)
	server := httptest.NewServer(handler)
	defer server.Close()
	client := agentv1connect.NewAgentServiceClient(http.DefaultClient, server.URL)
	requests := []*agentv1.ConnectServiceRequest{{Name: "first", Port: 8080}, {Name: "denied", Port: 8081}, {Name: "second", Port: 9090}}
	action := watchAction(requests, func(fleet.Target) (agentv1connect.AgentServiceClient, error) { return client, nil })
	plan := &fleet.Plan{Operation: "services watch", ColonyID: "test", Parameters: json.RawMessage(`[]`), Targets: []fleet.Target{{ID: "one", Agent: &colonyv1.Agent{AgentId: "one"}}}}
	options := fleet.Options{Concurrency: 1, Retries: 1, RequestTimeout: time.Second}
	report := plan.Run(context.Background(), options, action)
	require.Equal(t, 1, report.Summary.Failed)
	require.Equal(t, map[string]int{"first": 1, "denied": 1, "second": 2}, mock.calls)
	// An explicit new retry is allowed to try a formerly permanent error again.
	mock.mu.Lock()
	mock.denied = ""
	mock.mu.Unlock()
	plan.Targets[0].Previous = &report.Results[0]
	options.Retries = 0
	retry := plan.Run(context.Background(), options, action)
	require.Equal(t, 1, retry.Summary.Succeeded)
	require.Equal(t, map[string]int{"first": 1, "denied": 2, "second": 2}, mock.calls)
}
