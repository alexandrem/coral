package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
)

func TestOpenShellStreamConnectionFailure(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := openShellStream(ctx, server.URL, "test-user", 80, 24)
	require.Error(t, err)
	assert.Nil(t, stream)
	assert.Contains(t, err.Error(), "failed to start shell:")
	assert.Contains(t, err.Error(), "dial tcp")
	assert.NotContains(t, err.Error(), "write envelope: EOF")
}

func TestOpenShellStream(t *testing.T) {
	handler := &mockShellHandler{start: make(chan *agentv1.ShellStart, 1)}
	path, rpcHandler := agentv1connect.NewAgentServiceHandler(handler)
	mux := http.NewServeMux()
	mux.Handle(path, rpcHandler)
	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := openShellStream(ctx, server.URL, "test-user", 120, 40)
	require.NoError(t, err)
	defer func() { _ = stream.CloseResponse() }()
	defer func() { _ = stream.CloseRequest() }()

	resp, err := stream.Receive()
	require.NoError(t, err)
	assert.Equal(t, "test-session", resp.GetExit().GetSessionId())
	start := <-handler.start
	assert.Equal(t, "/bin/bash", start.Shell)
	assert.Equal(t, "test-user", start.UserId)
	assert.Equal(t, uint32(120), start.Size.Cols)
	assert.Equal(t, uint32(40), start.Size.Rows)
}

type mockShellHandler struct {
	agentv1connect.UnimplementedAgentServiceHandler
	start chan *agentv1.ShellStart
}

func (h *mockShellHandler) Shell(ctx context.Context, stream *connect.BidiStream[agentv1.ShellRequest, agentv1.ShellResponse]) error {
	req, err := stream.Receive()
	if err != nil {
		return err
	}
	h.start <- req.GetStart()
	return stream.Send(&agentv1.ShellResponse{
		Payload: &agentv1.ShellResponse_Exit{Exit: &agentv1.ShellExit{SessionId: "test-session"}},
	})
}

// TestResolveAgentID tests agent ID to mesh IP resolution via colony registry (RFD 044).
func TestResolveAgentID(t *testing.T) {
	t.Run("resolve existing agent - success", func(t *testing.T) {
		// Create mock colony server.
		mockColony := createMockColonyServer([]*colonyv1.Agent{
			{
				AgentId:       "agent-api-1",
				MeshIpv4:      "10.42.0.15",
				MeshIpv6:      "fd42::15",
				Status:        "healthy",
				ComponentName: "api",
			},
			{
				AgentId:       "agent-frontend",
				MeshIpv4:      "10.42.0.20",
				MeshIpv6:      "fd42::20",
				Status:        "healthy",
				ComponentName: "frontend",
			},
		})
		defer mockColony.Close()

		// Test resolution using the mock server.
		ctx := context.Background()
		resolved := resolveAgentIDWithURL(ctx, "agent-api-1", mockColony.URL)
		assert.Equal(t, "10.42.0.15:9001", resolved)

		resolved = resolveAgentIDWithURL(ctx, "agent-frontend", mockColony.URL)
		assert.Equal(t, "10.42.0.20:9001", resolved)
	})

	t.Run("resolve non-existent agent - error", func(t *testing.T) {
		// Create mock colony server with some agents.
		mockColony := createMockColonyServer([]*colonyv1.Agent{
			{
				AgentId:       "agent-api-1",
				MeshIpv4:      "10.42.0.15",
				Status:        "healthy",
				ComponentName: "api",
			},
		})
		defer mockColony.Close()

		// Try to resolve non-existent agent.
		ctx := context.Background()
		resolved := resolveAgentIDWithURL(ctx, "nonexistent-agent", mockColony.URL)
		assert.Empty(t, resolved)
	})

	t.Run("empty agent list - error", func(t *testing.T) {
		// Create mock colony server with no agents.
		mockColony := createMockColonyServer([]*colonyv1.Agent{})
		defer mockColony.Close()

		// Try to resolve any agent.
		ctx := context.Background()
		resolved := resolveAgentIDWithURL(ctx, "any-agent", mockColony.URL)
		assert.Empty(t, resolved)
	})

	t.Run("multiple agents - correct selection", func(t *testing.T) {
		// Create mock colony server with multiple agents.
		mockColony := createMockColonyServer([]*colonyv1.Agent{
			{AgentId: "agent-api-1", MeshIpv4: "10.42.0.15", Status: "healthy", ComponentName: "api"},
			{AgentId: "agent-api-2", MeshIpv4: "10.42.0.16", Status: "healthy", ComponentName: "api"},
			{AgentId: "agent-api-3", MeshIpv4: "10.42.0.17", Status: "healthy", ComponentName: "api"},
			{AgentId: "agent-frontend", MeshIpv4: "10.42.0.20", Status: "healthy", ComponentName: "frontend"},
		})
		defer mockColony.Close()

		// Test that each agent ID resolves to the correct mesh IP.
		ctx := context.Background()

		resolved := resolveAgentIDWithURL(ctx, "agent-api-1", mockColony.URL)
		assert.Equal(t, "10.42.0.15:9001", resolved)

		resolved = resolveAgentIDWithURL(ctx, "agent-api-2", mockColony.URL)
		assert.Equal(t, "10.42.0.16:9001", resolved)

		resolved = resolveAgentIDWithURL(ctx, "agent-api-3", mockColony.URL)
		assert.Equal(t, "10.42.0.17:9001", resolved)

		resolved = resolveAgentIDWithURL(ctx, "agent-frontend", mockColony.URL)
		assert.Equal(t, "10.42.0.20:9001", resolved)
	})
}

// TestFormatAvailableAgents tests formatting of available agents for error messages.
func TestFormatAvailableAgents(t *testing.T) {
	t.Run("no agents", func(t *testing.T) {
		formatted := formatAvailableAgents([]*colonyv1.Agent{})
		assert.Equal(t, "  (no agents connected)", formatted)
	})

	t.Run("single agent", func(t *testing.T) {
		agents := []*colonyv1.Agent{
			{AgentId: "agent-api", MeshIpv4: "10.42.0.15"},
		}
		formatted := formatAvailableAgents(agents)
		assert.Contains(t, formatted, "agent-api")
		assert.Contains(t, formatted, "10.42.0.15")
	})

	t.Run("multiple agents", func(t *testing.T) {
		agents := []*colonyv1.Agent{
			{AgentId: "agent-api-1", MeshIpv4: "10.42.0.15"},
			{AgentId: "agent-api-2", MeshIpv4: "10.42.0.16"},
			{AgentId: "agent-frontend", MeshIpv4: "10.42.0.20"},
		}
		formatted := formatAvailableAgents(agents)
		assert.Contains(t, formatted, "agent-api-1")
		assert.Contains(t, formatted, "10.42.0.15")
		assert.Contains(t, formatted, "agent-api-2")
		assert.Contains(t, formatted, "10.42.0.16")
		assert.Contains(t, formatted, "agent-frontend")
		assert.Contains(t, formatted, "10.42.0.20")
	})
}

// createMockColonyServer creates a mock colony HTTP server for testing.
func createMockColonyServer(agents []*colonyv1.Agent) *httptest.Server {
	mux := http.NewServeMux()

	// Create the Colony service handler.
	handler := &mockColonyHandler{agents: agents}
	path, handler2 := colonyv1connect.NewColonyServiceHandler(handler)
	mux.Handle(path, handler2)

	// Start test server.
	server := httptest.NewServer(mux)
	return server
}

// mockColonyHandler implements the ColonyService interface for testing.
type mockColonyHandler struct {
	colonyv1connect.UnimplementedColonyServiceHandler
	agents []*colonyv1.Agent
}

// ListAgents returns the mock agent list.
func (h *mockColonyHandler) ListAgents(
	ctx context.Context,
	req *connect.Request[colonyv1.ListAgentsRequest],
) (*connect.Response[colonyv1.ListAgentsResponse], error) {
	return connect.NewResponse(&colonyv1.ListAgentsResponse{
		Agents: h.agents,
	}), nil
}

// resolveAgentIDWithURL is a test helper that resolves agent ID using a specific colony URL.
// This bypasses the config loading and directly queries the specified URL.
func resolveAgentIDWithURL(ctx context.Context, agentID, colonyURL string) string {
	// Create RPC client.
	client := colonyv1connect.NewColonyServiceClient(http.DefaultClient, colonyURL)

	// Call ListAgents RPC.
	req := connect.NewRequest(&colonyv1.ListAgentsRequest{})
	resp, err := client.ListAgents(ctx, req)
	if err != nil {
		return ""
	}

	// Find agent with matching ID.
	for _, agent := range resp.Msg.Agents {
		if agent.AgentId == agentID {
			return fmt.Sprintf("%s:9001", agent.MeshIpv4)
		}
	}

	return ""
}
