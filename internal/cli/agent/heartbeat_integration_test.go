package agent

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/coral/mesh/v1/meshv1connect"
	"github.com/coral-mesh/coral/internal/cli/agent/startup"
	"github.com/coral-mesh/coral/internal/config"
	discoveryclient "github.com/coral-mesh/coral/internal/discovery/client"
	"github.com/coral-mesh/coral/internal/logging"
)

// mockColonyServer simulates a colony that receives heartbeats.
type mockColonyServer struct {
	mu                sync.Mutex
	heartbeats        []string // Agent IDs that sent heartbeats.
	shouldFail        bool
	heartbeatReceived chan struct{} // Signals when a heartbeat is received.
}

func newMockColonyServer() *mockColonyServer {
	return &mockColonyServer{
		heartbeatReceived: make(chan struct{}, 100),
	}
}

func (m *mockColonyServer) Register(
	ctx context.Context,
	req *connect.Request[meshv1.RegisterRequest],
) (*connect.Response[meshv1.RegisterResponse], error) {
	return connect.NewResponse(&meshv1.RegisterResponse{
		Accepted:   true,
		AssignedIp: "100.64.0.2",
		MeshSubnet: "100.64.0.0/10",
	}), nil
}

func (m *mockColonyServer) Heartbeat(
	ctx context.Context,
	req *connect.Request[meshv1.HeartbeatRequest],
) (*connect.Response[meshv1.HeartbeatResponse], error) {
	m.mu.Lock()
	shouldFail := m.shouldFail
	m.mu.Unlock()

	// Signal that a heartbeat was received.
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
	m.heartbeats = append(m.heartbeats, req.Msg.AgentId)
	m.mu.Unlock()

	return connect.NewResponse(&meshv1.HeartbeatResponse{
		Ok: true,
	}), nil
}

func (m *mockColonyServer) awaitHeartbeats(n int) {
	for i := 0; i < n; i++ {
		<-m.heartbeatReceived
	}
}

func (m *mockColonyServer) getHeartbeatCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.heartbeats)
}

func (m *mockColonyServer) setFailure(shouldFail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shouldFail = shouldFail
}

// TestConnectionManager_StartHeartbeatLoop tests the integration between
// ConnectionManager and the heartbeat agent.
func TestConnectionManager_StartHeartbeatLoop(t *testing.T) {
	t.Run("sends heartbeats and transitions to healthy state", func(t *testing.T) {
		// Create mock colony server.
		mockColony := newMockColonyServer()
		path, handler := meshv1connect.NewMeshServiceHandler(mockColony)
		mux := http.NewServeMux()
		mux.Handle(path, handler)
		server := httptest.NewServer(mux)
		defer server.Close()

		// Parse server address.
		host, portStr, err := net.SplitHostPort(server.Listener.Addr().String())
		require.NoError(t, err)

		var port uint32
		_, err = fmt.Sscanf(portStr, "%d", &port)
		require.NoError(t, err)

		// Create colony info pointing to our test server.
		colonyInfo := &discoveryclient.LookupColonyResponse{
			MeshIPv4:    host,
			ConnectPort: port,
			Endpoints:   []string{server.Listener.Addr().String()},
		}

		// Create ConnectionManager.
		logger := logging.NewWithComponent(logging.Config{Level: "error", Pretty: false}, "test")
		cfg := &config.ResolvedConfig{
			ColonyID: "test-colony",
		}

		cm := NewConnectionManager(
			"test-agent",
			colonyInfo,
			cfg,
			nil, // serviceSpecs
			"test-pubkey",
			nil, // wgDevice
			nil, // runtimeService
			logger,
		)

		// Verify initial state.
		initialState := cm.GetState()
		assert.Equal(t, startup.StateUnregistered, initialState)

		// Start heartbeat loop.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		interval := 20 * time.Millisecond
		go cm.StartHeartbeatLoop(ctx, interval)

		// Await at least 2 heartbeats using channel synchronization.
		mockColony.awaitHeartbeats(2)

		// Verify heartbeats were received.
		count := mockColony.getHeartbeatCount()
		assert.GreaterOrEqual(t, count, 2, "expected at least 2 heartbeats")

		// Verify state transitioned to Healthy.
		state := cm.GetState()
		assert.Equal(t, startup.StateHealthy, state)
	})

	t.Run("stops sending heartbeats when context is cancelled", func(t *testing.T) {
		// Create mock colony server.
		mockColony := newMockColonyServer()
		path, handler := meshv1connect.NewMeshServiceHandler(mockColony)
		mux := http.NewServeMux()
		mux.Handle(path, handler)
		server := httptest.NewServer(mux)
		defer server.Close()

		host, portStr, err := net.SplitHostPort(server.Listener.Addr().String())
		require.NoError(t, err)

		var port uint32
		_, err = fmt.Sscanf(portStr, "%d", &port)
		require.NoError(t, err)

		colonyInfo := &discoveryclient.LookupColonyResponse{
			MeshIPv4:    host,
			ConnectPort: port,
			Endpoints:   []string{server.Listener.Addr().String()},
		}

		logger := logging.NewWithComponent(logging.Config{Level: "error", Pretty: false}, "test")
		cfg := &config.ResolvedConfig{ColonyID: "test-colony"}

		cm := NewConnectionManager("test-agent-2", colonyInfo, cfg, nil, "pubkey", nil, nil, logger)

		ctx, cancel := context.WithCancel(context.Background())

		interval := 20 * time.Millisecond
		go cm.StartHeartbeatLoop(ctx, interval)

		// Await 2 heartbeats.
		mockColony.awaitHeartbeats(2)

		countBeforeCancel := mockColony.getHeartbeatCount()
		require.GreaterOrEqual(t, countBeforeCancel, 2)

		// Cancel context.
		cancel()

		// Wait a bit to ensure no more heartbeats are sent.
		time.Sleep(100 * time.Millisecond)

		countAfterCancel := mockColony.getHeartbeatCount()
		assert.Equal(t, countBeforeCancel, countAfterCancel, "no heartbeats should be sent after cancellation")
	})

	t.Run("tracks consecutive failures and triggers reconnection", func(t *testing.T) {
		// Create mock colony server that will fail.
		mockColony := newMockColonyServer()
		mockColony.setFailure(true) // Start in failure mode.

		path, handler := meshv1connect.NewMeshServiceHandler(mockColony)
		mux := http.NewServeMux()
		mux.Handle(path, handler)
		server := httptest.NewServer(mux)
		defer server.Close()

		host, portStr, err := net.SplitHostPort(server.Listener.Addr().String())
		require.NoError(t, err)

		var port uint32
		_, err = fmt.Sscanf(portStr, "%d", &port)
		require.NoError(t, err)

		colonyInfo := &discoveryclient.LookupColonyResponse{
			MeshIPv4:    host,
			ConnectPort: port,
			Endpoints:   []string{server.Listener.Addr().String()},
		}

		logger := logging.NewWithComponent(logging.Config{Level: "error", Pretty: false}, "test")
		cfg := &config.ResolvedConfig{ColonyID: "test-colony"}

		cm := NewConnectionManager("test-agent-3", colonyInfo, cfg, nil, "pubkey", nil, nil, logger)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		interval := 20 * time.Millisecond
		go cm.StartHeartbeatLoop(ctx, interval)

		// Await 3 failed heartbeat attempts (they still signal).
		mockColony.awaitHeartbeats(3)

		// After 3 consecutive failures, state should transition to Unregistered.
		// Give it a moment to process.
		time.Sleep(50 * time.Millisecond)

		state := cm.GetState()
		assert.Equal(t, startup.StateUnregistered, state, "state should transition to Unregistered after 3 failures")
	})

	t.Run("recovers after successful heartbeat", func(t *testing.T) {
		// Create mock colony server.
		mockColony := newMockColonyServer()
		path, handler := meshv1connect.NewMeshServiceHandler(mockColony)
		mux := http.NewServeMux()
		mux.Handle(path, handler)
		server := httptest.NewServer(mux)
		defer server.Close()

		host, portStr, err := net.SplitHostPort(server.Listener.Addr().String())
		require.NoError(t, err)

		var port uint32
		_, err = fmt.Sscanf(portStr, "%d", &port)
		require.NoError(t, err)

		colonyInfo := &discoveryclient.LookupColonyResponse{
			MeshIPv4:    host,
			ConnectPort: port,
			Endpoints:   []string{server.Listener.Addr().String()},
		}

		logger := logging.NewWithComponent(logging.Config{Level: "error", Pretty: false}, "test")
		cfg := &config.ResolvedConfig{ColonyID: "test-colony"}

		cm := NewConnectionManager("test-agent-4", colonyInfo, cfg, nil, "pubkey", nil, nil, logger)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		interval := 20 * time.Millisecond
		go cm.StartHeartbeatLoop(ctx, interval)

		// Wait for first successful heartbeat.
		mockColony.awaitHeartbeats(1)

		// Inject failures.
		mockColony.setFailure(true)

		// Await 2 failed attempts.
		mockColony.awaitHeartbeats(2)

		// Re-enable heartbeats.
		mockColony.setFailure(false)

		// Await recovery heartbeat.
		mockColony.awaitHeartbeats(1)

		// State should be healthy again.
		state := cm.GetState()
		assert.Equal(t, startup.StateHealthy, state, "state should recover to Healthy after successful heartbeat")
	})
}
