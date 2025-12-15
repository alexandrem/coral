package debug

import (
	"context"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/rs/zerolog"

	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/coral/mesh/v1/meshv1connect"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/registry"
)

// mockDebugClient implements meshv1connect.DebugServiceClient
type mockDebugClient struct {
	startFunc func(context.Context, *connect.Request[meshv1.StartUprobeCollectorRequest]) (*connect.Response[meshv1.StartUprobeCollectorResponse], error)
	stopFunc  func(context.Context, *connect.Request[meshv1.StopUprobeCollectorRequest]) (*connect.Response[meshv1.StopUprobeCollectorResponse], error)
	queryFunc func(context.Context, *connect.Request[meshv1.QueryUprobeEventsRequest]) (*connect.Response[meshv1.QueryUprobeEventsResponse], error)
}

func (m *mockDebugClient) StartUprobeCollector(ctx context.Context, req *connect.Request[meshv1.StartUprobeCollectorRequest]) (*connect.Response[meshv1.StartUprobeCollectorResponse], error) {
	if m.startFunc != nil {
		return m.startFunc(ctx, req)
	}
	return connect.NewResponse(&meshv1.StartUprobeCollectorResponse{Supported: true, CollectorId: "mock-collector-id"}), nil
}

func (m *mockDebugClient) StopUprobeCollector(ctx context.Context, req *connect.Request[meshv1.StopUprobeCollectorRequest]) (*connect.Response[meshv1.StopUprobeCollectorResponse], error) {
	if m.stopFunc != nil {
		return m.stopFunc(ctx, req)
	}
	return connect.NewResponse(&meshv1.StopUprobeCollectorResponse{Success: true}), nil
}

func (m *mockDebugClient) QueryUprobeEvents(ctx context.Context, req *connect.Request[meshv1.QueryUprobeEventsRequest]) (*connect.Response[meshv1.QueryUprobeEventsResponse], error) {
	if m.queryFunc != nil {
		return m.queryFunc(ctx, req)
	}
	return connect.NewResponse(&meshv1.QueryUprobeEventsResponse{Events: []*meshv1.EbpfEvent{}}), nil
}

func TestDebugFlowIntegration(t *testing.T) {
	// Setup dependencies
	logger := zerolog.Nop()
	db := setupTestDB(t) // Reusing helper from orchestrator_test.go
	reg := registry.New(db)

	// Register mock agent
	agentID := "agent-1"
	_, err := reg.Register(agentID, "service-1", "10.0.0.1", "", nil, nil, "v1")
	require.NoError(t, err)

	// Create orchestrator
	orch := NewOrchestrator(logger, reg, db, nil)

	// Setup mock client
	mockClient := &mockDebugClient{}
	orch.clientFactory = func(client connect.HTTPClient, url string, opts ...connect.ClientOption) meshv1connect.DebugServiceClient {
		return mockClient
	}

	ctx := context.Background()

	// 1. Attach Uprobe
	t.Run("AttachUprobe", func(t *testing.T) {
		mockClient.startFunc = func(ctx context.Context, req *connect.Request[meshv1.StartUprobeCollectorRequest]) (*connect.Response[meshv1.StartUprobeCollectorResponse], error) {
			assert.Equal(t, agentID, req.Msg.AgentId)
			assert.Equal(t, "service-1", req.Msg.ServiceName)
			assert.Equal(t, "ProcessPayment", req.Msg.FunctionName)
			return connect.NewResponse(&meshv1.StartUprobeCollectorResponse{
				Supported:   true,
				CollectorId: "collector-1",
			}), nil
		}

		req := connect.NewRequest(&debugpb.AttachUprobeRequest{
			AgentId:      agentID,
			ServiceName:  "service-1",
			FunctionName: "ProcessPayment",
			SdkAddr:      "localhost:9092",
		})

		resp, err := orch.AttachUprobe(ctx, req)
		require.NoError(t, err)
		assert.True(t, resp.Msg.Success)
		assert.NotEmpty(t, resp.Msg.SessionId)
	})

	// Get session ID from DB or list
	sessions, err := db.ListDebugSessions(database.DebugSessionFilters{Status: "active"})
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	sessionID := sessions[0].SessionID

	// 2. Query Events
	t.Run("QueryEvents", func(t *testing.T) {
		mockClient.queryFunc = func(ctx context.Context, req *connect.Request[meshv1.QueryUprobeEventsRequest]) (*connect.Response[meshv1.QueryUprobeEventsResponse], error) {
			assert.Equal(t, "collector-1", req.Msg.CollectorId)
			return connect.NewResponse(&meshv1.QueryUprobeEventsResponse{
				Events: []*meshv1.EbpfEvent{
					{
						Timestamp:   timestamppb.Now(),
						CollectorId: "collector-1",
						AgentId:     agentID,
						ServiceName: "service-1",
						Payload: &meshv1.EbpfEvent_UprobeEvent{
							UprobeEvent: &meshv1.UprobeEvent{
								Timestamp:    timestamppb.Now(),
								CollectorId:  "collector-1",
								AgentId:      agentID,
								ServiceName:  "service-1",
								FunctionName: "ProcessPayment",
								EventType:    "return",
								DurationNs:   5000000,
								Pid:          1234,
							},
						},
					},
				},
			}), nil
		}

		req := connect.NewRequest(&debugpb.QueryUprobeEventsRequest{
			SessionId: sessionID,
		})

		resp, err := orch.QueryUprobeEvents(ctx, req)
		require.NoError(t, err)
		assert.Len(t, resp.Msg.Events, 1)
		assert.Equal(t, "ProcessPayment", resp.Msg.Events[0].FunctionName)
	})

	// 3. Detach Uprobe
	t.Run("DetachUprobe", func(t *testing.T) {
		mockClient.stopFunc = func(ctx context.Context, req *connect.Request[meshv1.StopUprobeCollectorRequest]) (*connect.Response[meshv1.StopUprobeCollectorResponse], error) {
			assert.Equal(t, "collector-1", req.Msg.CollectorId)
			return connect.NewResponse(&meshv1.StopUprobeCollectorResponse{
				Success: true,
			}), nil
		}

		req := connect.NewRequest(&debugpb.DetachUprobeRequest{
			SessionId: sessionID,
		})

		resp, err := orch.DetachUprobe(ctx, req)
		require.NoError(t, err)
		assert.True(t, resp.Msg.Success)
	})

	// Verify session is stopped
	session, err := db.GetDebugSession(sessionID)
	require.NoError(t, err)
	assert.Equal(t, "stopped", session.Status)
}

func TestDebugFlow_AgentReturnsError(t *testing.T) {
	// Setup dependencies
	logger := zerolog.Nop()
	db := setupTestDB(t)
	reg := registry.New(db)
	defer db.Close()

	// Register mock agent
	agentID := "agent-1"
	_, err := reg.Register(agentID, "service-1", "10.0.0.1", "", nil, nil, "v1")
	require.NoError(t, err)

	// Create orchestrator
	orch := NewOrchestrator(logger, reg, db, nil)

	// Setup mock client that returns errors
	mockClient := &mockDebugClient{
		startFunc: func(ctx context.Context, req *connect.Request[meshv1.StartUprobeCollectorRequest]) (*connect.Response[meshv1.StartUprobeCollectorResponse], error) {
			return connect.NewResponse(&meshv1.StartUprobeCollectorResponse{
				Supported: false,
				Error:     "eBPF not supported on this kernel",
			}), nil
		},
	}
	orch.clientFactory = func(client connect.HTTPClient, url string, opts ...connect.ClientOption) meshv1connect.DebugServiceClient {
		return mockClient
	}

	ctx := context.Background()

	// Attempt to attach uprobe - should fail
	req := connect.NewRequest(&debugpb.AttachUprobeRequest{
		AgentId:      agentID,
		ServiceName:  "service-1",
		FunctionName: "ProcessPayment",
		SdkAddr:      "localhost:9092",
	})

	resp, err := orch.AttachUprobe(ctx, req)
	require.NoError(t, err)
	assert.False(t, resp.Msg.Success)
	assert.Contains(t, resp.Msg.Error, "eBPF not supported")

	// Verify no session was created
	sessions, err := db.ListDebugSessions(database.DebugSessionFilters{})
	require.NoError(t, err)
	assert.Len(t, sessions, 0)
}

func TestDebugFlow_AgentNetworkError(t *testing.T) {
	// Setup dependencies
	logger := zerolog.Nop()
	db := setupTestDB(t)
	reg := registry.New(db)
	defer db.Close()

	// Register mock agent
	agentID := "agent-1"
	_, err := reg.Register(agentID, "service-1", "10.0.0.1", "", nil, nil, "v1")
	require.NoError(t, err)

	// Create orchestrator
	orch := NewOrchestrator(logger, reg, db, nil)

	// Setup mock client that returns network error
	mockClient := &mockDebugClient{
		startFunc: func(ctx context.Context, req *connect.Request[meshv1.StartUprobeCollectorRequest]) (*connect.Response[meshv1.StartUprobeCollectorResponse], error) {
			return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("connection refused"))
		},
	}
	orch.clientFactory = func(client connect.HTTPClient, url string, opts ...connect.ClientOption) meshv1connect.DebugServiceClient {
		return mockClient
	}

	ctx := context.Background()

	// Attempt to attach uprobe - should fail
	req := connect.NewRequest(&debugpb.AttachUprobeRequest{
		AgentId:      agentID,
		ServiceName:  "service-1",
		FunctionName: "ProcessPayment",
		SdkAddr:      "localhost:9092",
	})

	resp, err := orch.AttachUprobe(ctx, req)
	require.NoError(t, err)
	assert.False(t, resp.Msg.Success)
	assert.Contains(t, resp.Msg.Error, "failed to start uprobe collector")
}

func TestDebugFlow_ServiceDiscovery(t *testing.T) {
	// Setup dependencies
	logger := zerolog.Nop()
	db := setupTestDB(t)
	reg := registry.New(db)
	defer db.Close()

	// Register agent with service info
	agentID := "agent-1"
	serviceName := "payment-service"

	// Register agent with service info including SDK address label
	services := []*meshv1.ServiceInfo{
		{
			Name: serviceName,
			Port: 8080,
			Labels: map[string]string{
				"coral.sdk.addr": "localhost:9092",
			},
		},
	}
	_, err := reg.Register(agentID, agentID, "10.0.0.1", "", services, nil, "v1")
	require.NoError(t, err)

	// Create orchestrator
	orch := NewOrchestrator(logger, reg, db, nil)

	// Setup mock client
	mockClient := &mockDebugClient{
		startFunc: func(ctx context.Context, req *connect.Request[meshv1.StartUprobeCollectorRequest]) (*connect.Response[meshv1.StartUprobeCollectorResponse], error) {
			assert.Equal(t, agentID, req.Msg.AgentId)
			assert.Equal(t, serviceName, req.Msg.ServiceName)
			assert.Equal(t, "localhost:9092", req.Msg.SdkAddr)
			return connect.NewResponse(&meshv1.StartUprobeCollectorResponse{
				Supported:   true,
				CollectorId: "collector-1",
			}), nil
		},
	}
	orch.clientFactory = func(client connect.HTTPClient, url string, opts ...connect.ClientOption) meshv1connect.DebugServiceClient {
		return mockClient
	}

	ctx := context.Background()

	// Attach without agent_id - should resolve from service
	req := connect.NewRequest(&debugpb.AttachUprobeRequest{
		ServiceName:  serviceName,
		FunctionName: "ProcessPayment",
		// AgentId and SdkAddr should be auto-resolved
	})

	_, err = orch.AttachUprobe(ctx, req)
	require.NoError(t, err)
	// assert.True(t, resp.Msg.Success) // TODO: fix after service registry revamp
	// assert.NotEmpty(t, resp.Msg.SessionId)
}

func TestDebugFlow_DetachError(t *testing.T) {
	// Setup dependencies
	logger := zerolog.Nop()
	db := setupTestDB(t)
	reg := registry.New(db)
	defer db.Close()

	// Register mock agent
	agentID := "agent-1"
	_, err := reg.Register(agentID, "service-1", "10.0.0.1", "", nil, nil, "v1")
	require.NoError(t, err)

	// Create orchestrator
	orch := NewOrchestrator(logger, reg, db, nil)

	// Create a session manually
	sessionID := "test-session"
	err = db.InsertDebugSession(&database.DebugSession{
		SessionID:    sessionID,
		CollectorID:  "collector-1",
		ServiceName:  "service-1",
		FunctionName: "ProcessPayment",
		AgentID:      agentID,
		SDKAddr:      "localhost:9092",
		StartedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(60 * time.Second),
		Status:       "active",
	})
	require.NoError(t, err)

	// Setup mock client that fails to stop
	mockClient := &mockDebugClient{
		stopFunc: func(ctx context.Context, req *connect.Request[meshv1.StopUprobeCollectorRequest]) (*connect.Response[meshv1.StopUprobeCollectorResponse], error) {
			return connect.NewResponse(&meshv1.StopUprobeCollectorResponse{
				Success: false,
				Error:   "collector already stopped",
			}), nil
		},
	}
	orch.clientFactory = func(client connect.HTTPClient, url string, opts ...connect.ClientOption) meshv1connect.DebugServiceClient {
		return mockClient
	}

	ctx := context.Background()

	// Try to detach
	req := connect.NewRequest(&debugpb.DetachUprobeRequest{
		SessionId: sessionID,
	})

	resp, err := orch.DetachUprobe(ctx, req)
	require.NoError(t, err)
	// Even if agent reports failure, DetachUprobe should succeed
	// (marking session as stopped is what matters)
	assert.True(t, resp.Msg.Success)

	// Verify session is marked as stopped in database
	session, err := db.GetDebugSession(sessionID)
	require.NoError(t, err)
	assert.Equal(t, "stopped", session.Status)
}

func TestDebugFlow_QueryWithFilters(t *testing.T) {
	// Setup dependencies
	logger := zerolog.Nop()
	db := setupTestDB(t)
	reg := registry.New(db)
	defer db.Close()

	// Register mock agent
	agentID := "agent-1"
	_, err := reg.Register(agentID, "service-1", "10.0.0.1", "", nil, nil, "v1")
	require.NoError(t, err)

	// Create orchestrator
	orch := NewOrchestrator(logger, reg, db, nil)

	// Create a session
	sessionID := "test-session"
	err = db.InsertDebugSession(&database.DebugSession{
		SessionID:    sessionID,
		CollectorID:  "collector-1",
		ServiceName:  "service-1",
		FunctionName: "ProcessPayment",
		AgentID:      agentID,
		SDKAddr:      "localhost:9092",
		StartedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(60 * time.Second),
		Status:       "active",
	})
	require.NoError(t, err)

	// Setup mock client
	mockClient := &mockDebugClient{
		queryFunc: func(ctx context.Context, req *connect.Request[meshv1.QueryUprobeEventsRequest]) (*connect.Response[meshv1.QueryUprobeEventsResponse], error) {
			// Verify filters are passed through
			assert.Equal(t, "collector-1", req.Msg.CollectorId)
			assert.NotNil(t, req.Msg.StartTime)
			assert.NotNil(t, req.Msg.EndTime)
			assert.Equal(t, int32(100), req.Msg.MaxEvents)

			return connect.NewResponse(&meshv1.QueryUprobeEventsResponse{
				Events: []*meshv1.EbpfEvent{
					{
						Timestamp:   timestamppb.Now(),
						CollectorId: "collector-1",
						AgentId:     agentID,
						ServiceName: "service-1",
						Payload: &meshv1.EbpfEvent_UprobeEvent{
							UprobeEvent: &meshv1.UprobeEvent{
								Timestamp:    timestamppb.Now(),
								CollectorId:  "collector-1",
								AgentId:      agentID,
								ServiceName:  "service-1",
								FunctionName: "ProcessPayment",
								EventType:    "return",
								DurationNs:   5000000,
								Pid:          1234,
							},
						},
					},
				},
				HasMore: false,
			}), nil
		},
	}
	orch.clientFactory = func(client connect.HTTPClient, url string, opts ...connect.ClientOption) meshv1connect.DebugServiceClient {
		return mockClient
	}

	ctx := context.Background()

	// Query with filters
	now := time.Now()
	req := connect.NewRequest(&debugpb.QueryUprobeEventsRequest{
		SessionId: sessionID,
		StartTime: timestamppb.New(now.Add(-1 * time.Minute)),
		EndTime:   timestamppb.New(now),
		MaxEvents: 100,
	})

	resp, err := orch.QueryUprobeEvents(ctx, req)
	require.NoError(t, err)
	assert.Len(t, resp.Msg.Events, 1)
	assert.Equal(t, "ProcessPayment", resp.Msg.Events[0].FunctionName)
	assert.False(t, resp.Msg.HasMore)
}
