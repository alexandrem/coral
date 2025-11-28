package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/colony/registry"
	"github.com/coral-mesh/coral/internal/logging"
)

// TestResolveAgent tests the agent resolution logic for RFD 044.
func TestResolveAgent(t *testing.T) {
	logger := logging.NewWithComponent(logging.Config{
		Level:  "error",
		Pretty: false,
	}, "mcp-test")

	t.Run("resolve by agent ID - success", func(t *testing.T) {
		reg := registry.New()
		services := []*meshv1.ServiceInfo{
			{Name: "api", Port: 8080},
		}
		_, err := reg.Register("agent-api-1", "api", "10.42.0.15", "fd42::15", services, nil, "v1.0.0")
		require.NoError(t, err)

		server := &Server{
			registry: reg,
			logger:   logger,
		}

		agentID := "agent-api-1"
		agent, err := server.resolveAgent(&agentID, "")
		require.NoError(t, err)
		assert.Equal(t, "agent-api-1", agent.AgentID)
		assert.Equal(t, "10.42.0.15", agent.MeshIPv4)
	})

	t.Run("resolve by agent ID - not found", func(t *testing.T) {
		reg := registry.New()
		server := &Server{
			registry: reg,
			logger:   logger,
		}

		agentID := "nonexistent-agent"
		agent, err := server.resolveAgent(&agentID, "")
		assert.Error(t, err)
		assert.Nil(t, agent)
		assert.Contains(t, err.Error(), "agent not found: nonexistent-agent")
	})

	t.Run("resolve by service - unique match", func(t *testing.T) {
		reg := registry.New()
		services := []*meshv1.ServiceInfo{
			{Name: "frontend", Port: 3000},
		}
		_, err := reg.Register("agent-frontend", "frontend", "10.42.0.20", "fd42::20", services, nil, "v1.0.0")
		require.NoError(t, err)

		server := &Server{
			registry: reg,
			logger:   logger,
		}

		agent, err := server.resolveAgent(nil, "frontend")
		require.NoError(t, err)
		assert.Equal(t, "agent-frontend", agent.AgentID)
		assert.Equal(t, "10.42.0.20", agent.MeshIPv4)
	})

	t.Run("resolve by service - multiple matches (disambiguation error)", func(t *testing.T) {
		reg := registry.New()

		// Register 3 agents all serving "api" service.
		services1 := []*meshv1.ServiceInfo{{Name: "api", Port: 8080}}
		services2 := []*meshv1.ServiceInfo{{Name: "api", Port: 8080}}
		services3 := []*meshv1.ServiceInfo{{Name: "api", Port: 8080}}

		_, err := reg.Register("agent-api-1", "api", "10.42.0.15", "fd42::15", services1, nil, "v1.0.0")
		require.NoError(t, err)
		_, err = reg.Register("agent-api-2", "api", "10.42.0.16", "fd42::16", services2, nil, "v1.0.0")
		require.NoError(t, err)
		_, err = reg.Register("agent-api-3", "api", "10.42.0.17", "fd42::17", services3, nil, "v1.0.0")
		require.NoError(t, err)

		server := &Server{
			registry: reg,
			logger:   logger,
		}

		agent, err := server.resolveAgent(nil, "api")
		assert.Error(t, err)
		assert.Nil(t, agent)
		assert.Contains(t, err.Error(), "multiple agents found for service 'api'")
		assert.Contains(t, err.Error(), "agent-api-1")
		assert.Contains(t, err.Error(), "agent-api-2")
		assert.Contains(t, err.Error(), "agent-api-3")
		assert.Contains(t, err.Error(), "Please specify agent_id parameter to disambiguate")
	})

	t.Run("resolve by service - no matches", func(t *testing.T) {
		reg := registry.New()
		services := []*meshv1.ServiceInfo{{Name: "api", Port: 8080}}
		_, err := reg.Register("agent-api", "api", "10.42.0.15", "fd42::15", services, nil, "v1.0.0")
		require.NoError(t, err)

		server := &Server{
			registry: reg,
			logger:   logger,
		}

		agent, err := server.resolveAgent(nil, "nonexistent-service")
		assert.Error(t, err)
		assert.Nil(t, agent)
		assert.Contains(t, err.Error(), "no agents found for service 'nonexistent-service'")
	})

	t.Run("agent ID takes precedence over service", func(t *testing.T) {
		reg := registry.New()

		// Register multiple agents with same service.
		services1 := []*meshv1.ServiceInfo{{Name: "api", Port: 8080}}
		services2 := []*meshv1.ServiceInfo{{Name: "api", Port: 8080}}

		_, err := reg.Register("agent-api-1", "api", "10.42.0.15", "fd42::15", services1, nil, "v1.0.0")
		require.NoError(t, err)
		_, err = reg.Register("agent-api-2", "api", "10.42.0.16", "fd42::16", services2, nil, "v1.0.0")
		require.NoError(t, err)

		server := &Server{
			registry: reg,
			logger:   logger,
		}

		// Even though "api" service would be ambiguous, specifying agent_id should resolve uniquely.
		agentID := "agent-api-2"
		agent, err := server.resolveAgent(&agentID, "api")
		require.NoError(t, err)
		assert.Equal(t, "agent-api-2", agent.AgentID)
		assert.Equal(t, "10.42.0.16", agent.MeshIPv4)
	})

	t.Run("multi-service agent - match by any service", func(t *testing.T) {
		reg := registry.New()

		// Agent monitoring multiple services.
		services := []*meshv1.ServiceInfo{
			{Name: "api", Port: 8080},
			{Name: "worker", Port: 9000},
		}
		_, err := reg.Register("agent-multi", "multi", "10.42.0.17", "fd42::17", services, nil, "v1.0.0")
		require.NoError(t, err)

		server := &Server{
			registry: reg,
			logger:   logger,
		}

		// Should match when searching for "api".
		agent1, err := server.resolveAgent(nil, "api")
		require.NoError(t, err)
		assert.Equal(t, "agent-multi", agent1.AgentID)

		// Should also match when searching for "worker".
		agent2, err := server.resolveAgent(nil, "worker")
		require.NoError(t, err)
		assert.Equal(t, "agent-multi", agent2.AgentID)
	})

	t.Run("pattern matching - wildcard", func(t *testing.T) {
		reg := registry.New()

		services1 := []*meshv1.ServiceInfo{{Name: "api-v1", Port: 8080}}
		services2 := []*meshv1.ServiceInfo{{Name: "api-v2", Port: 8081}}
		services3 := []*meshv1.ServiceInfo{{Name: "frontend", Port: 3000}}

		_, err := reg.Register("agent-api-v1", "api-v1", "10.42.0.15", "fd42::15", services1, nil, "v1.0.0")
		require.NoError(t, err)
		_, err = reg.Register("agent-api-v2", "api-v2", "10.42.0.16", "fd42::16", services2, nil, "v1.0.0")
		require.NoError(t, err)
		_, err = reg.Register("agent-frontend", "frontend", "10.42.0.20", "fd42::20", services3, nil, "v1.0.0")
		require.NoError(t, err)

		server := &Server{
			registry: reg,
			logger:   logger,
		}

		// Pattern "api*" should match both api-v1 and api-v2 (ambiguous).
		agent, err := server.resolveAgent(nil, "api*")
		assert.Error(t, err)
		assert.Nil(t, agent)
		assert.Contains(t, err.Error(), "multiple agents found")
		assert.Contains(t, err.Error(), "agent-api-v1")
		assert.Contains(t, err.Error(), "agent-api-v2")
	})
}

// TestExecuteCommandToolWithAgentID tests exec command tool with agent ID parameter (RFD 044).
func TestExecuteCommandToolWithAgentID(t *testing.T) {
	logger := logging.NewWithComponent(logging.Config{
		Level:  "error",
		Pretty: false,
	}, "mcp-test")

	t.Run("exec command with agent_id - unambiguous", func(t *testing.T) {
		reg := registry.New()

		// Register multiple agents with same service.
		services1 := []*meshv1.ServiceInfo{{Name: "api", Port: 8080}}
		services2 := []*meshv1.ServiceInfo{{Name: "api", Port: 8080}}

		_, err := reg.Register("agent-api-1", "api", "10.42.0.15", "fd42::15", services1, nil, "v1.0.0")
		require.NoError(t, err)
		_, err = reg.Register("agent-api-2", "api", "10.42.0.16", "fd42::16", services2, nil, "v1.0.0")
		require.NoError(t, err)

		server := &Server{
			registry: reg,
			config: Config{
				ColonyID: "test-colony",
			},
			logger: logger,
		}

		// Execute command with agent_id should target specific agent.
		agentID := "agent-api-1"
		agent, err := server.resolveAgent(&agentID, "api")
		require.NoError(t, err)
		assert.Equal(t, "agent-api-1", agent.AgentID)
		assert.Equal(t, "10.42.0.15", agent.MeshIPv4)

		// Note: Full execution test would require agent client setup.
		// Here we're testing the resolution logic only.
	})

	t.Run("exec command with service only - ambiguous error", func(t *testing.T) {
		reg := registry.New()

		// Register multiple agents with same service.
		services1 := []*meshv1.ServiceInfo{{Name: "api", Port: 8080}}
		services2 := []*meshv1.ServiceInfo{{Name: "api", Port: 8080}}

		_, err := reg.Register("agent-api-1", "api", "10.42.0.15", "fd42::15", services1, nil, "v1.0.0")
		require.NoError(t, err)
		_, err = reg.Register("agent-api-2", "api", "10.42.0.16", "fd42::16", services2, nil, "v1.0.0")
		require.NoError(t, err)

		server := &Server{
			registry: reg,
			config: Config{
				ColonyID: "test-colony",
			},
			logger: logger,
		}

		// Execute command with service only should fail with disambiguation error.
		agent, err := server.resolveAgent(nil, "api")
		assert.Error(t, err)
		assert.Nil(t, agent)
		assert.Contains(t, err.Error(), "multiple agents found")
		assert.Contains(t, err.Error(), "Please specify agent_id parameter")
	})
}

// TestServiceFiltering tests that service filtering uses Services[] array (RFD 044).
func TestServiceFiltering(t *testing.T) {
	logger := logging.NewWithComponent(logging.Config{
		Level:  "error",
		Pretty: false,
	}, "mcp-test")

	t.Run("filter by service from Services array, not ComponentName", func(t *testing.T) {
		reg := registry.New()

		// Agent with ComponentName "multi" but Services array contains specific services.
		services := []*meshv1.ServiceInfo{
			{Name: "api", Port: 8080},
			{Name: "worker", Port: 9000},
		}
		_, err := reg.Register("agent-multi", "multi", "10.42.0.17", "fd42::17", services, nil, "v1.0.0")
		require.NoError(t, err)

		server := &Server{
			registry: reg,
			logger:   logger,
		}

		// Searching for "api" should match even though ComponentName is "multi".
		agent, err := server.resolveAgent(nil, "api")
		require.NoError(t, err)
		assert.Equal(t, "agent-multi", agent.AgentID)

		// Searching for "multi" (Name) should NOT match.
		// (Because we're filtering by Services[], not Name).
		agent2, err := server.resolveAgent(nil, "multi")
		assert.Error(t, err)
		assert.Nil(t, agent2)
		assert.Contains(t, err.Error(), "no agents found for service 'multi'")
	})

	t.Run("agent with no services - no match", func(t *testing.T) {
		reg := registry.New()

		// Agent with no services in Services array.
		_, err := reg.Register("agent-empty", "empty", "10.42.0.18", "fd42::18", nil, nil, "v1.0.0")
		require.NoError(t, err)

		server := &Server{
			registry: reg,
			logger:   logger,
		}

		// Searching by any service should not match.
		agent, err := server.resolveAgent(nil, "any-service")
		assert.Error(t, err)
		assert.Nil(t, agent)
		assert.Contains(t, err.Error(), "no agents found")
	})
}

// Note: TestExecuteShellStartTool removed - replaced by coral_shell_exec in RFD 045.
// The old coral_shell_start was a discovery helper that has been scrapped.
// New tests for coral_shell_exec will be added when the agent-side implementation is complete.
