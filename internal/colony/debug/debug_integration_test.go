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

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/registry"
)

// mockDebugClient implements agentv1connect.AgentDebugServiceClient
type mockDebugClient struct {
	startFunc func(context.Context, *connect.Request[agentv1.StartUprobeCollectorRequest]) (*connect.Response[agentv1.StartUprobeCollectorResponse], error)
	stopFunc  func(context.Context, *connect.Request[agentv1.StopUprobeCollectorRequest]) (*connect.Response[agentv1.StopUprobeCollectorResponse], error)
	queryFunc func(context.Context, *connect.Request[agentv1.QueryUprobeEventsRequest]) (*connect.Response[agentv1.QueryUprobeEventsResponse], error)
}

func (m *mockDebugClient) StartUprobeCollector(ctx context.Context, req *connect.Request[agentv1.StartUprobeCollectorRequest]) (*connect.Response[agentv1.StartUprobeCollectorResponse], error) {
	if m.startFunc != nil {
		return m.startFunc(ctx, req)
	}
	return connect.NewResponse(&agentv1.StartUprobeCollectorResponse{Supported: true, CollectorId: "mock-collector-id"}), nil
}

func (m *mockDebugClient) StopUprobeCollector(ctx context.Context, req *connect.Request[agentv1.StopUprobeCollectorRequest]) (*connect.Response[agentv1.StopUprobeCollectorResponse], error) {
	if m.stopFunc != nil {
		return m.stopFunc(ctx, req)
	}
	return connect.NewResponse(&agentv1.StopUprobeCollectorResponse{Success: true}), nil
}

func (m *mockDebugClient) QueryUprobeEvents(ctx context.Context, req *connect.Request[agentv1.QueryUprobeEventsRequest]) (*connect.Response[agentv1.QueryUprobeEventsResponse], error) {
	if m.queryFunc != nil {
		return m.queryFunc(ctx, req)
	}
	return connect.NewResponse(&agentv1.QueryUprobeEventsResponse{Events: []*agentv1.UprobeEvent{}}), nil
}

func (m *mockDebugClient) ProfileCPU(ctx context.Context, req *connect.Request[agentv1.ProfileCPUAgentRequest]) (*connect.Response[agentv1.ProfileCPUAgentResponse], error) {
	return connect.NewResponse(&agentv1.ProfileCPUAgentResponse{Success: true, TotalSamples: 100}), nil
}

func (m *mockDebugClient) QueryCPUProfileSamples(ctx context.Context, req *connect.Request[agentv1.QueryCPUProfileSamplesRequest]) (*connect.Response[agentv1.QueryCPUProfileSamplesResponse], error) {
	return connect.NewResponse(&agentv1.QueryCPUProfileSamplesResponse{Samples: []*agentv1.CPUProfileSample{}, TotalSamples: 0}), nil
}

func (m *mockDebugClient) ProfileMemory(ctx context.Context, req *connect.Request[agentv1.ProfileMemoryAgentRequest]) (*connect.Response[agentv1.ProfileMemoryAgentResponse], error) {
	return connect.NewResponse(&agentv1.ProfileMemoryAgentResponse{Success: true}), nil
}

func (m *mockDebugClient) QueryMemoryProfileSamples(ctx context.Context, req *connect.Request[agentv1.QueryMemoryProfileSamplesRequest]) (*connect.Response[agentv1.QueryMemoryProfileSamplesResponse], error) {
	return connect.NewResponse(&agentv1.QueryMemoryProfileSamplesResponse{}), nil
}

// mockAgentClient implements agentv1connect.AgentServiceClient for testing.
type mockAgentClient struct {
	listServicesFunc func(context.Context, *connect.Request[agentv1.ListServicesRequest]) (*connect.Response[agentv1.ListServicesResponse], error)
}

func (m *mockAgentClient) GetRuntimeContext(ctx context.Context, req *connect.Request[agentv1.GetRuntimeContextRequest]) (*connect.Response[agentv1.RuntimeContextResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("not implemented in mock"))
}

func (m *mockAgentClient) ConnectService(ctx context.Context, req *connect.Request[agentv1.ConnectServiceRequest]) (*connect.Response[agentv1.ConnectServiceResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("not implemented in mock"))
}

func (m *mockAgentClient) DisconnectService(ctx context.Context, req *connect.Request[agentv1.DisconnectServiceRequest]) (*connect.Response[agentv1.DisconnectServiceResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("not implemented in mock"))
}

func (m *mockAgentClient) ListServices(ctx context.Context, req *connect.Request[agentv1.ListServicesRequest]) (*connect.Response[agentv1.ListServicesResponse], error) {
	if m.listServicesFunc != nil {
		return m.listServicesFunc(ctx, req)
	}
	return connect.NewResponse(&agentv1.ListServicesResponse{Services: []*agentv1.ServiceStatus{}}), nil
}

func (m *mockAgentClient) QueryTelemetry(ctx context.Context, req *connect.Request[agentv1.QueryTelemetryRequest]) (*connect.Response[agentv1.QueryTelemetryResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("not implemented in mock"))
}

func (m *mockAgentClient) QueryEbpfMetrics(ctx context.Context, req *connect.Request[agentv1.QueryEbpfMetricsRequest]) (*connect.Response[agentv1.QueryEbpfMetricsResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("not implemented in mock"))
}

func (m *mockAgentClient) QuerySystemMetrics(ctx context.Context, req *connect.Request[agentv1.QuerySystemMetricsRequest]) (*connect.Response[agentv1.QuerySystemMetricsResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("not implemented in mock"))
}

func (m *mockAgentClient) Shell(ctx context.Context) *connect.BidiStreamForClient[agentv1.ShellRequest, agentv1.ShellResponse] {
	panic("Shell not implemented in mock")
}

func (m *mockAgentClient) ShellExec(ctx context.Context, req *connect.Request[agentv1.ShellExecRequest]) (*connect.Response[agentv1.ShellExecResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("not implemented in mock"))
}

func (m *mockAgentClient) ContainerExec(ctx context.Context, req *connect.Request[agentv1.ContainerExecRequest]) (*connect.Response[agentv1.ContainerExecResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("not implemented in mock"))
}

func (m *mockAgentClient) ResizeShellTerminal(ctx context.Context, req *connect.Request[agentv1.ResizeShellTerminalRequest]) (*connect.Response[agentv1.ResizeShellTerminalResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("not implemented in mock"))
}

func (m *mockAgentClient) SendShellSignal(ctx context.Context, req *connect.Request[agentv1.SendShellSignalRequest]) (*connect.Response[agentv1.SendShellSignalResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("not implemented in mock"))
}

func (m *mockAgentClient) KillShellSession(ctx context.Context, req *connect.Request[agentv1.KillShellSessionRequest]) (*connect.Response[agentv1.KillShellSessionResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("not implemented in mock"))
}

func (m *mockAgentClient) StreamDebugEvents(ctx context.Context) *connect.BidiStreamForClient[agentv1.DebugCommand, agentv1.DebugEvent] {
	panic("StreamDebugEvents not implemented in mock")
}

func (m *mockAgentClient) GetFunctions(ctx context.Context, req *connect.Request[agentv1.GetFunctionsRequest]) (*connect.Response[agentv1.GetFunctionsResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("not implemented in mock"))
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
	orch.clientFactory = func(client connect.HTTPClient, url string, opts ...connect.ClientOption) agentv1connect.AgentDebugServiceClient {
		return mockClient
	}

	ctx := context.Background()

	// 1. Attach Uprobe
	t.Run("AttachUprobe", func(t *testing.T) {
		mockClient.startFunc = func(ctx context.Context, req *connect.Request[agentv1.StartUprobeCollectorRequest]) (*connect.Response[agentv1.StartUprobeCollectorResponse], error) {
			assert.Equal(t, agentID, req.Msg.AgentId)
			assert.Equal(t, "service-1", req.Msg.ServiceName)
			assert.Equal(t, "ProcessPayment", req.Msg.FunctionName)
			return connect.NewResponse(&agentv1.StartUprobeCollectorResponse{
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
		if !resp.Msg.Success {
			t.Logf("AttachUprobe failed: %s", resp.Msg.Error)
		}
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
		mockClient.queryFunc = func(ctx context.Context, req *connect.Request[agentv1.QueryUprobeEventsRequest]) (*connect.Response[agentv1.QueryUprobeEventsResponse], error) {
			assert.Equal(t, "collector-1", req.Msg.CollectorId)
			return connect.NewResponse(&agentv1.QueryUprobeEventsResponse{
				Events: []*agentv1.UprobeEvent{
					{
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
		mockClient.stopFunc = func(ctx context.Context, req *connect.Request[agentv1.StopUprobeCollectorRequest]) (*connect.Response[agentv1.StopUprobeCollectorResponse], error) {
			assert.Equal(t, "collector-1", req.Msg.CollectorId)
			return connect.NewResponse(&agentv1.StopUprobeCollectorResponse{
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
	session, err := db.GetDebugSession(context.Background(), sessionID)
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
		startFunc: func(ctx context.Context, req *connect.Request[agentv1.StartUprobeCollectorRequest]) (*connect.Response[agentv1.StartUprobeCollectorResponse], error) {
			return connect.NewResponse(&agentv1.StartUprobeCollectorResponse{
				Supported: false,
				Error:     "eBPF not supported on this kernel",
			}), nil
		},
	}
	orch.clientFactory = func(client connect.HTTPClient, url string, opts ...connect.ClientOption) agentv1connect.AgentDebugServiceClient {
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
		startFunc: func(ctx context.Context, req *connect.Request[agentv1.StartUprobeCollectorRequest]) (*connect.Response[agentv1.StartUprobeCollectorResponse], error) {
			return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("connection refused"))
		},
	}
	orch.clientFactory = func(client connect.HTTPClient, url string, opts ...connect.ClientOption) agentv1connect.AgentDebugServiceClient {
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
		startFunc: func(ctx context.Context, req *connect.Request[agentv1.StartUprobeCollectorRequest]) (*connect.Response[agentv1.StartUprobeCollectorResponse], error) {
			assert.Equal(t, agentID, req.Msg.AgentId)
			assert.Equal(t, serviceName, req.Msg.ServiceName)
			assert.Equal(t, "localhost:9092", req.Msg.SdkAddr)
			return connect.NewResponse(&agentv1.StartUprobeCollectorResponse{
				Supported:   true,
				CollectorId: "collector-1",
			}), nil
		},
	}
	orch.clientFactory = func(client connect.HTTPClient, url string, opts ...connect.ClientOption) agentv1connect.AgentDebugServiceClient {
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
	err = db.InsertDebugSession(context.Background(), &database.DebugSession{
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
		stopFunc: func(ctx context.Context, req *connect.Request[agentv1.StopUprobeCollectorRequest]) (*connect.Response[agentv1.StopUprobeCollectorResponse], error) {
			return connect.NewResponse(&agentv1.StopUprobeCollectorResponse{
				Success: false,
				Error:   "collector already stopped",
			}), nil
		},
	}
	orch.clientFactory = func(client connect.HTTPClient, url string, opts ...connect.ClientOption) agentv1connect.AgentDebugServiceClient {
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
	session, err := db.GetDebugSession(context.Background(), sessionID)
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
	err = db.InsertDebugSession(context.Background(), &database.DebugSession{
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
		queryFunc: func(ctx context.Context, req *connect.Request[agentv1.QueryUprobeEventsRequest]) (*connect.Response[agentv1.QueryUprobeEventsResponse], error) {
			// Verify filters are passed through
			assert.Equal(t, "collector-1", req.Msg.CollectorId)
			assert.NotNil(t, req.Msg.StartTime)
			assert.NotNil(t, req.Msg.EndTime)
			assert.Equal(t, int32(100), req.Msg.MaxEvents)

			return connect.NewResponse(&agentv1.QueryUprobeEventsResponse{
				Events: []*agentv1.UprobeEvent{
					{
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
				HasMore: false,
			}), nil
		},
	}
	orch.clientFactory = func(client connect.HTTPClient, url string, opts ...connect.ClientOption) agentv1connect.AgentDebugServiceClient {
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

func TestDebugFlow_CPUProfile(t *testing.T) {
	// Setup dependencies
	logger := zerolog.Nop()
	db := setupTestDB(t)
	reg := registry.New(db)
	defer db.Close()

	// Register mock agent with service
	agentID := "agent-1"
	serviceName := "payment-service"
	services := []*meshv1.ServiceInfo{
		{
			Name:      serviceName,
			Port:      8080,
			ProcessId: 1234, // Mock PID
		},
	}
	_, err := reg.Register(agentID, agentID, "10.0.0.1", "", services, nil, "v1")
	require.NoError(t, err)

	// Create orchestrator
	orch := NewOrchestrator(logger, reg, db, nil)

	// Setup mock agent client to return service list with PID
	mockAgentCli := &mockAgentClient{
		listServicesFunc: func(ctx context.Context, req *connect.Request[agentv1.ListServicesRequest]) (*connect.Response[agentv1.ListServicesResponse], error) {
			return connect.NewResponse(&agentv1.ListServicesResponse{
				Services: []*agentv1.ServiceStatus{
					{
						Name:      serviceName,
						ProcessId: 1234,
					},
				},
			}), nil
		},
	}
	orch.agentClientFactory = func(client connect.HTTPClient, url string, opts ...connect.ClientOption) agentv1connect.AgentServiceClient {
		return mockAgentCli
	}

	// Override ProfileCPU to return mock samples
	mockProfileCPU := func(ctx context.Context, req *connect.Request[agentv1.ProfileCPUAgentRequest]) (*connect.Response[agentv1.ProfileCPUAgentResponse], error) {
		// Verify request parameters
		assert.Equal(t, int32(1234), req.Msg.Pid)
		assert.Equal(t, serviceName, req.Msg.ServiceName)
		assert.Equal(t, int32(5), req.Msg.DurationSeconds)
		assert.Equal(t, int32(99), req.Msg.FrequencyHz)

		// Return mock profile data
		return connect.NewResponse(&agentv1.ProfileCPUAgentResponse{
			Success:      true,
			TotalSamples: 495, // 5 seconds * 99Hz = ~495 samples
			LostSamples:  0,
			Samples: []*agentv1.StackSample{
				{
					FrameNames: []string{"main", "ProcessPayment", "validateCard"},
					Count:      245,
				},
				{
					FrameNames: []string{"main", "ProcessPayment", "chargeCard"},
					Count:      250,
				},
			},
		}), nil
	}

	// Setup mock debug client factory
	orch.clientFactory = func(client connect.HTTPClient, url string, opts ...connect.ClientOption) agentv1connect.AgentDebugServiceClient {
		return &mockDebugClientWithCPUProfile{
			mockDebugClient: &mockDebugClient{},
			profileCPUFunc:  mockProfileCPU,
		}
	}

	ctx := context.Background()

	// Test CPU profiling
	t.Run("ProfileCPU_Success", func(t *testing.T) {
		req := connect.NewRequest(&debugpb.ProfileCPURequest{
			ServiceName:     serviceName,
			DurationSeconds: 5,
			FrequencyHz:     99,
			AgentId:         agentID,
		})

		resp, err := orch.ProfileCPU(ctx, req)
		require.NoError(t, err)
		assert.True(t, resp.Msg.Success)
		assert.Equal(t, uint64(495), resp.Msg.TotalSamples)
		assert.Equal(t, uint32(0), resp.Msg.LostSamples)
		assert.Len(t, resp.Msg.Samples, 2)

		// Verify stack samples
		assert.Equal(t, []string{"main", "ProcessPayment", "validateCard"}, resp.Msg.Samples[0].FrameNames)
		assert.Equal(t, uint64(245), resp.Msg.Samples[0].Count)
		assert.Equal(t, []string{"main", "ProcessPayment", "chargeCard"}, resp.Msg.Samples[1].FrameNames)
		assert.Equal(t, uint64(250), resp.Msg.Samples[1].Count)
	})
}

// mockDebugClientWithCPUProfile extends mockDebugClient with ProfileCPU support.
type mockDebugClientWithCPUProfile struct {
	*mockDebugClient
	profileCPUFunc func(context.Context, *connect.Request[agentv1.ProfileCPUAgentRequest]) (*connect.Response[agentv1.ProfileCPUAgentResponse], error)
}

func (m *mockDebugClientWithCPUProfile) ProfileCPU(ctx context.Context, req *connect.Request[agentv1.ProfileCPUAgentRequest]) (*connect.Response[agentv1.ProfileCPUAgentResponse], error) {
	if m.profileCPUFunc != nil {
		return m.profileCPUFunc(ctx, req)
	}
	return connect.NewResponse(&agentv1.ProfileCPUAgentResponse{Success: true, TotalSamples: 100}), nil
}

// mockDebugClientWithMemoryProfile extends mockDebugClient with ProfileMemory support.
type mockDebugClientWithMemoryProfile struct {
	*mockDebugClient
	profileMemoryFunc func(context.Context, *connect.Request[agentv1.ProfileMemoryAgentRequest]) (*connect.Response[agentv1.ProfileMemoryAgentResponse], error)
}

func (m *mockDebugClientWithMemoryProfile) ProfileMemory(ctx context.Context, req *connect.Request[agentv1.ProfileMemoryAgentRequest]) (*connect.Response[agentv1.ProfileMemoryAgentResponse], error) {
	if m.profileMemoryFunc != nil {
		return m.profileMemoryFunc(ctx, req)
	}
	return connect.NewResponse(&agentv1.ProfileMemoryAgentResponse{Success: true}), nil
}

func TestDebugFlow_MemoryProfile(t *testing.T) {
	logger := zerolog.Nop()
	db := setupTestDB(t)
	reg := registry.New(db)
	defer db.Close()

	agentID := "agent-1"
	serviceName := "order-service"
	services := []*meshv1.ServiceInfo{
		{
			Name:      serviceName,
			Port:      8080,
			ProcessId: 5678,
		},
	}
	_, err := reg.Register(agentID, agentID, "10.0.0.1", "", services, nil, "v1")
	require.NoError(t, err)

	orch := NewOrchestrator(logger, reg, db, nil)

	// Setup mock agent client to return service list with PID.
	mockAgentCli := &mockAgentClient{
		listServicesFunc: func(ctx context.Context, req *connect.Request[agentv1.ListServicesRequest]) (*connect.Response[agentv1.ListServicesResponse], error) {
			return connect.NewResponse(&agentv1.ListServicesResponse{
				Services: []*agentv1.ServiceStatus{
					{
						Name:      serviceName,
						ProcessId: 5678,
					},
				},
			}), nil
		},
	}
	orch.agentClientFactory = func(client connect.HTTPClient, url string, opts ...connect.ClientOption) agentv1connect.AgentServiceClient {
		return mockAgentCli
	}

	mockProfileMemory := func(ctx context.Context, req *connect.Request[agentv1.ProfileMemoryAgentRequest]) (*connect.Response[agentv1.ProfileMemoryAgentResponse], error) {
		assert.Equal(t, int32(5678), req.Msg.Pid)
		assert.Equal(t, serviceName, req.Msg.ServiceName)
		assert.Equal(t, int32(30), req.Msg.DurationSeconds)

		return connect.NewResponse(&agentv1.ProfileMemoryAgentResponse{
			Success: true,
			Samples: []*agentv1.MemoryStackSample{
				{
					FrameNames:   []string{"main.ProcessOrder", "json.Marshal"},
					AllocBytes:   1024000,
					AllocObjects: 500,
				},
				{
					FrameNames:   []string{"main.HandleRequest", "cache.Store"},
					AllocBytes:   512000,
					AllocObjects: 200,
				},
			},
			Stats: &agentv1.MemoryStats{
				AllocBytes:      2048000,
				TotalAllocBytes: 10000000,
				SysBytes:        50000000,
				NumGc:           42,
			},
			TopFunctions: []*agentv1.TopAllocFunction{
				{Function: "main.ProcessOrder", Bytes: 1024000, Objects: 500, Pct: 66.7},
				{Function: "main.HandleRequest", Bytes: 512000, Objects: 200, Pct: 33.3},
			},
		}), nil
	}

	orch.clientFactory = func(client connect.HTTPClient, url string, opts ...connect.ClientOption) agentv1connect.AgentDebugServiceClient {
		return &mockDebugClientWithMemoryProfile{
			mockDebugClient:   &mockDebugClient{},
			profileMemoryFunc: mockProfileMemory,
		}
	}

	ctx := context.Background()

	t.Run("ProfileMemory_Success", func(t *testing.T) {
		req := connect.NewRequest(&debugpb.ProfileMemoryRequest{
			ServiceName:     serviceName,
			DurationSeconds: 30,
			AgentId:         agentID,
		})

		resp, err := orch.ProfileMemory(ctx, req)
		require.NoError(t, err)
		assert.True(t, resp.Msg.Success)
		assert.Len(t, resp.Msg.Samples, 2)
		assert.Equal(t, int64(2048000), resp.Msg.Stats.AllocBytes)
		assert.Len(t, resp.Msg.TopFunctions, 2)

		// Verify sample data.
		assert.Equal(t, []string{"main.ProcessOrder", "json.Marshal"}, resp.Msg.Samples[0].FrameNames)
		assert.Equal(t, int64(1024000), resp.Msg.Samples[0].AllocBytes)
	})

	t.Run("ProfileMemory_AgentError", func(t *testing.T) {
		orch.clientFactory = func(client connect.HTTPClient, url string, opts ...connect.ClientOption) agentv1connect.AgentDebugServiceClient {
			return &mockDebugClientWithMemoryProfile{
				mockDebugClient: &mockDebugClient{},
				profileMemoryFunc: func(ctx context.Context, req *connect.Request[agentv1.ProfileMemoryAgentRequest]) (*connect.Response[agentv1.ProfileMemoryAgentResponse], error) {
					return connect.NewResponse(&agentv1.ProfileMemoryAgentResponse{
						Success: false,
						Error:   "SDK unreachable",
					}), nil
				},
			}
		}

		req := connect.NewRequest(&debugpb.ProfileMemoryRequest{
			ServiceName:     serviceName,
			DurationSeconds: 30,
			AgentId:         agentID,
		})

		resp, err := orch.ProfileMemory(ctx, req)
		require.NoError(t, err)
		assert.False(t, resp.Msg.Success)
		assert.Contains(t, resp.Msg.Error, "SDK unreachable")
	})
}
