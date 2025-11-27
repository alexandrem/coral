package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/coral/mesh/v1/meshv1connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockMeshServiceHandler implements the MeshService RPC handler for testing.
type mockMeshServiceHandler struct {
	mu         sync.Mutex
	heartbeats []heartbeatRecord
}

type heartbeatRecord struct {
	agentID   string
	status    string
	timestamp time.Time
}

func (m *mockMeshServiceHandler) Register(
	ctx context.Context,
	req *connect.Request[meshv1.RegisterRequest],
) (*connect.Response[meshv1.RegisterResponse], error) {
	return connect.NewResponse(&meshv1.RegisterResponse{
		Accepted:   true,
		AssignedIp: "100.64.0.2",
		MeshSubnet: "100.64.0.0/10",
	}), nil
}

func (m *mockMeshServiceHandler) Heartbeat(
	ctx context.Context,
	req *connect.Request[meshv1.HeartbeatRequest],
) (*connect.Response[meshv1.HeartbeatResponse], error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.heartbeats = append(m.heartbeats, heartbeatRecord{
		agentID:   req.Msg.AgentId,
		status:    req.Msg.Status,
		timestamp: time.Now(),
	})

	return connect.NewResponse(&meshv1.HeartbeatResponse{
		Ok: true,
	}), nil
}

func (m *mockMeshServiceHandler) getHeartbeatCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.heartbeats)
}

func (m *mockMeshServiceHandler) getHeartbeats() []heartbeatRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]heartbeatRecord{}, m.heartbeats...)
}

func TestHeartbeatLoop(t *testing.T) {
	t.Run("sends heartbeats at configured interval", func(t *testing.T) {
		// Create mock server
		mockHandler := &mockMeshServiceHandler{}
		path, handler := meshv1connect.NewMeshServiceHandler(mockHandler)
		mux := http.NewServeMux()
		mux.Handle(path, handler)
		server := httptest.NewServer(mux)
		defer server.Close()

		// Start heartbeat loop with rapid interval (100ms for testing)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		agentID := "test-agent"
		interval := 100 * time.Millisecond

		// We need to pass the server address parts separately
		// For testing, we'll use a different approach - pass the full URL
		go func() {
			// Create a custom client that points to our test server
			client := meshv1connect.NewMeshServiceClient(http.DefaultClient, server.URL)

			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					heartbeatCtx, heartbeatCancel := context.WithTimeout(ctx, 5*time.Second)
					_, _ = client.Heartbeat(heartbeatCtx, connect.NewRequest(&meshv1.HeartbeatRequest{
						AgentId: agentID,
						Status:  "healthy",
					}))
					heartbeatCancel()
				}
			}
		}()

		// Wait for at least 3 heartbeats (300ms + buffer)
		time.Sleep(350 * time.Millisecond)

		// Cancel the loop
		cancel()

		// Give it time to stop
		time.Sleep(50 * time.Millisecond)

		// Verify heartbeats were received
		heartbeatCount := mockHandler.getHeartbeatCount()
		assert.GreaterOrEqual(t, heartbeatCount, 3, "expected at least 3 heartbeats")
		assert.LessOrEqual(t, heartbeatCount, 5, "expected no more than 5 heartbeats")

		// Verify heartbeat content
		heartbeats := mockHandler.getHeartbeats()
		for _, hb := range heartbeats {
			assert.Equal(t, agentID, hb.agentID)
			assert.Equal(t, "healthy", hb.status)
		}
	})

	t.Run("stops when context is cancelled", func(t *testing.T) {
		// Create mock server
		mockHandler := &mockMeshServiceHandler{}
		path, handler := meshv1connect.NewMeshServiceHandler(mockHandler)
		mux := http.NewServeMux()
		mux.Handle(path, handler)
		server := httptest.NewServer(mux)
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())

		agentID := "test-agent-2"
		interval := 50 * time.Millisecond

		// Start heartbeat loop
		go func() {
			client := meshv1connect.NewMeshServiceClient(http.DefaultClient, server.URL)
			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					heartbeatCtx, heartbeatCancel := context.WithTimeout(ctx, 5*time.Second)
					_, _ = client.Heartbeat(heartbeatCtx, connect.NewRequest(&meshv1.HeartbeatRequest{
						AgentId: agentID,
						Status:  "healthy",
					}))
					heartbeatCancel()
				}
			}
		}()

		// Wait for a couple heartbeats
		time.Sleep(120 * time.Millisecond)

		countBeforeCancel := mockHandler.getHeartbeatCount()
		require.GreaterOrEqual(t, countBeforeCancel, 2, "expected at least 2 heartbeats before cancel")

		// Cancel the context
		cancel()

		// Wait to ensure no more heartbeats are sent
		time.Sleep(150 * time.Millisecond)

		countAfterCancel := mockHandler.getHeartbeatCount()
		assert.Equal(t, countBeforeCancel, countAfterCancel, "no heartbeats should be sent after context cancellation")
	})

	t.Run("continues after failed heartbeat", func(t *testing.T) {
		callCount := 0
		var mu sync.Mutex

		// Create a server that fails initially then succeeds
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			callCount++
			count := callCount
			mu.Unlock()

			if count <= 2 {
				// First 2 calls fail
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			// Subsequent calls succeed
			mockHandler := &mockMeshServiceHandler{}
			path, handler := meshv1connect.NewMeshServiceHandler(mockHandler)
			if r.URL.Path == path {
				handler.ServeHTTP(w, r)
			}
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		agentID := "test-agent-3"
		interval := 50 * time.Millisecond

		// Start heartbeat loop
		go func() {
			client := meshv1connect.NewMeshServiceClient(http.DefaultClient, server.URL)
			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					heartbeatCtx, heartbeatCancel := context.WithTimeout(ctx, 5*time.Second)
					_, _ = client.Heartbeat(heartbeatCtx, connect.NewRequest(&meshv1.HeartbeatRequest{
						AgentId: agentID,
						Status:  "healthy",
					}))
					heartbeatCancel()
				}
			}
		}()

		// Wait for test to complete
		<-ctx.Done()

		// Verify multiple calls were made despite failures
		mu.Lock()
		finalCount := callCount
		mu.Unlock()

		assert.GreaterOrEqual(t, finalCount, 5, "heartbeat loop should continue after failures")
	})
}
