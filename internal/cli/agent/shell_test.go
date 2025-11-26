package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"

	colonyv1 "github.com/coral-io/coral/coral/colony/v1"
	"github.com/coral-io/coral/coral/colony/v1/colonyv1connect"
)

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
