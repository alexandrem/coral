package heartbeat

import (
	"context"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
)

// mockMeshServiceClient implements meshv1connect.MeshServiceClient for testing.
type mockMeshServiceClient struct {
	mu                sync.Mutex
	heartbeats        []heartbeatCall
	shouldFail        bool
	heartbeatReceived chan struct{} // Signals when a heartbeat is received.
}

type heartbeatCall struct {
	agentID   string
	status    string
	timestamp time.Time
}

func newMockClient() *mockMeshServiceClient {
	return &mockMeshServiceClient{
		heartbeatReceived: make(chan struct{}, 100), // Buffered to avoid blocking.
	}
}

func (m *mockMeshServiceClient) Register(
	ctx context.Context,
	req *connect.Request[meshv1.RegisterRequest],
) (*connect.Response[meshv1.RegisterResponse], error) {
	return connect.NewResponse(&meshv1.RegisterResponse{
		Accepted:   true,
		AssignedIp: "100.64.0.2",
		MeshSubnet: "100.64.0.0/10",
	}), nil
}

func (m *mockMeshServiceClient) Heartbeat(
	ctx context.Context,
	req *connect.Request[meshv1.HeartbeatRequest],
) (*connect.Response[meshv1.HeartbeatResponse], error) {
	m.mu.Lock()
	shouldFail := m.shouldFail
	m.mu.Unlock()

	// Signal that a heartbeat attempt occurred (even if it fails).
	defer func() {
		select {
		case m.heartbeatReceived <- struct{}{}:
		default:
		}
	}()

	if shouldFail {
		return nil, connect.NewError(connect.CodeUnavailable, nil)
	}

	m.mu.Lock()
	m.heartbeats = append(m.heartbeats, heartbeatCall{
		agentID:   req.Msg.AgentId,
		status:    req.Msg.Status,
		timestamp: time.Now(),
	})
	m.mu.Unlock()

	return connect.NewResponse(&meshv1.HeartbeatResponse{
		Ok: true,
	}), nil
}

// awaitHeartbeats waits for N heartbeats to be received.
func (m *mockMeshServiceClient) awaitHeartbeats(n int) {
	for i := 0; i < n; i++ {
		<-m.heartbeatReceived
	}
}

func (m *mockMeshServiceClient) getHeartbeatCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.heartbeats)
}

func (m *mockMeshServiceClient) getHeartbeats() []heartbeatCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]heartbeatCall{}, m.heartbeats...)
}

func (m *mockMeshServiceClient) setFailure(shouldFail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shouldFail = shouldFail
}

func TestHeartbeatAgent(t *testing.T) {
	t.Run("sends heartbeats at configured interval", func(t *testing.T) {
		mockClient := newMockClient()
		agent := NewAgent("test-agent", mockClient)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		interval := 10 * time.Millisecond

		// Start heartbeat loop in background.
		go agent.StartHeartbeat(ctx, interval)

		// Await exactly 3 heartbeats - no guessing, no sleeps.
		mockClient.awaitHeartbeats(3)

		// Verify we got at least 3 heartbeats.
		count := mockClient.getHeartbeatCount()
		assert.GreaterOrEqual(t, count, 3, "expected at least 3 heartbeats")

		// Verify heartbeat content.
		heartbeats := mockClient.getHeartbeats()
		for _, hb := range heartbeats {
			assert.Equal(t, "test-agent", hb.agentID)
			assert.Equal(t, "healthy", hb.status)
		}
	})

	t.Run("stops when context is cancelled", func(t *testing.T) {
		mockClient := newMockClient()
		agent := NewAgent("test-agent-2", mockClient)

		ctx, cancel := context.WithCancel(context.Background())

		interval := 10 * time.Millisecond

		// Start heartbeat loop.
		go agent.StartHeartbeat(ctx, interval)

		// Await exactly 2 heartbeats.
		mockClient.awaitHeartbeats(2)

		countBeforeCancel := mockClient.getHeartbeatCount()
		require.GreaterOrEqual(t, countBeforeCancel, 2, "expected at least 2 heartbeats before cancel")

		// Cancel the context.
		cancel()

		// Give the goroutine a moment to notice the cancellation.
		time.Sleep(20 * time.Millisecond)

		countAfterCancel := mockClient.getHeartbeatCount()
		assert.Equal(t, countBeforeCancel, countAfterCancel, "no heartbeats should be sent after context cancellation")
	})

	t.Run("continues after failed heartbeat", func(t *testing.T) {
		mockClient := newMockClient()
		agent := NewAgent("test-agent-3", mockClient)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		interval := 10 * time.Millisecond

		// Start heartbeat loop.
		go agent.StartHeartbeat(ctx, interval)

		// Wait for first successful heartbeat.
		mockClient.awaitHeartbeats(1)

		countAfterFirst := mockClient.getHeartbeatCount()

		// Inject failure - heartbeats will still be attempted but will fail.
		mockClient.setFailure(true)

		// Await 2 failed heartbeat attempts (channel signals even on failure).
		mockClient.awaitHeartbeats(2)

		countAfterFailures := mockClient.getHeartbeatCount()
		assert.Equal(t, countAfterFirst, countAfterFailures, "failed heartbeats should not be recorded")

		// Re-enable heartbeats.
		mockClient.setFailure(false)

		// Await 2 more successful heartbeats.
		mockClient.awaitHeartbeats(2)

		countAfterRecovery := mockClient.getHeartbeatCount()

		// Should have recovered and sent more heartbeats.
		assert.Greater(t, countAfterRecovery, countAfterFailures, "heartbeat loop should continue after failures")
	})

	t.Run("SendHeartbeat sends single heartbeat", func(t *testing.T) {
		mockClient := &mockMeshServiceClient{}
		agent := NewAgent("test-agent-4", mockClient)

		ctx := context.Background()

		resp, err := agent.SendHeartbeat(ctx)
		require.NoError(t, err)
		assert.True(t, resp.Ok)

		count := mockClient.getHeartbeatCount()
		assert.Equal(t, 1, count)

		heartbeats := mockClient.getHeartbeats()
		assert.Equal(t, "test-agent-4", heartbeats[0].agentID)
		assert.Equal(t, "healthy", heartbeats[0].status)
	})

	t.Run("SendHeartbeat returns error on failure", func(t *testing.T) {
		mockClient := &mockMeshServiceClient{shouldFail: true}
		agent := NewAgent("test-agent-5", mockClient)

		ctx := context.Background()

		resp, err := agent.SendHeartbeat(ctx)
		assert.Error(t, err)
		assert.Nil(t, resp)
	})
}
