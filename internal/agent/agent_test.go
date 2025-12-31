package agent

import (
	"context"
	"testing"
	"time"

	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/agent/beyla"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	logger := zerolog.Nop()

	t.Run("successful creation", func(t *testing.T) {
		services := []*meshv1.ServiceInfo{
			{Name: "api", Port: 8080},
		}

		agent, err := New(Config{
			Context:  context.Background(),
			AgentID:  "test-agent",
			Services: services,
			Logger:   logger,
		})

		require.NoError(t, err)
		assert.Equal(t, "test-agent", agent.id)
		assert.Equal(t, 1, agent.GetServiceCount())
	})

	t.Run("multiple services", func(t *testing.T) {
		services := []*meshv1.ServiceInfo{
			{Name: "api", Port: 8080},
			{Name: "frontend", Port: 3000},
			{Name: "redis", Port: 6379},
		}

		agent, err := New(Config{
			Context:  context.Background(),
			AgentID:  "test-agent",
			Services: services,
			Logger:   logger,
		})

		require.NoError(t, err)
		assert.Equal(t, 3, agent.GetServiceCount())
	})

	t.Run("empty agent_id", func(t *testing.T) {
		services := []*meshv1.ServiceInfo{
			{Name: "api", Port: 8080},
		}

		_, err := New(Config{
			Context:  context.Background(),
			AgentID:  "",
			Services: services,
			Logger:   logger,
		})

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "agent_id is required")
	})

	t.Run("no services (passive mode)", func(t *testing.T) {
		agent, err := New(Config{
			Context:  context.Background(),
			AgentID:  "test-agent",
			Services: []*meshv1.ServiceInfo{},
			Logger:   logger,
		})

		assert.NoError(t, err)
		assert.NotNil(t, agent)
		assert.Equal(t, "test-agent", agent.id)
		assert.Equal(t, 0, len(agent.monitors))
	})
}

func TestAgent_StartStop(t *testing.T) {
	logger := zerolog.Nop()

	services := []*meshv1.ServiceInfo{
		{Name: "api", Port: 8080},
		{Name: "frontend", Port: 3000},
	}

	agent, err := New(Config{
		Context:  context.Background(),
		AgentID:  "test-agent",
		Services: services,
		Logger:   logger,
	})
	require.NoError(t, err)

	// Start the agent.
	err = agent.Start()
	assert.NoError(t, err)

	// Give monitors a moment to initialize.
	time.Sleep(100 * time.Millisecond)

	// Stop the agent.
	err = agent.Stop()
	assert.NoError(t, err)
}

func TestAgent_GetStatus(t *testing.T) {
	logger := zerolog.Nop()

	t.Run("all services healthy", func(t *testing.T) {
		services := []*meshv1.ServiceInfo{
			{Name: "service1", Port: 8080},
			{Name: "service2", Port: 8081},
		}

		agent, err := New(Config{
			Context:  context.Background(),
			AgentID:  "test-agent",
			Services: services,
			Logger:   logger,
		})
		require.NoError(t, err)

		// Manually set all monitors to healthy.
		for _, monitor := range agent.monitors {
			monitor.mu.Lock()
			monitor.status = ServiceStatusHealthy
			monitor.mu.Unlock()
		}

		status := agent.GetStatus()
		assert.Equal(t, AgentStatusHealthy, status)
	})

	t.Run("all services unhealthy", func(t *testing.T) {
		services := []*meshv1.ServiceInfo{
			{Name: "service1", Port: 8080},
			{Name: "service2", Port: 8081},
		}

		agent, err := New(Config{
			Context:  context.Background(),
			AgentID:  "test-agent",
			Services: services,
			Logger:   logger,
		})
		require.NoError(t, err)

		// Manually set all monitors to unhealthy.
		for _, monitor := range agent.monitors {
			monitor.mu.Lock()
			monitor.status = ServiceStatusUnhealthy
			monitor.mu.Unlock()
		}

		status := agent.GetStatus()
		assert.Equal(t, AgentStatusUnhealthy, status)
	})

	t.Run("some services unhealthy - degraded", func(t *testing.T) {
		services := []*meshv1.ServiceInfo{
			{Name: "service1", Port: 8080},
			{Name: "service2", Port: 8081},
			{Name: "service3", Port: 8082},
		}

		agent, err := New(Config{
			Context:  context.Background(),
			AgentID:  "test-agent",
			Services: services,
			Logger:   logger,
		})
		require.NoError(t, err)

		// Set mixed statuses.
		agent.monitors["service1"].mu.Lock()
		agent.monitors["service1"].status = ServiceStatusHealthy
		agent.monitors["service1"].mu.Unlock()

		agent.monitors["service2"].mu.Lock()
		agent.monitors["service2"].status = ServiceStatusUnhealthy
		agent.monitors["service2"].mu.Unlock()

		agent.monitors["service3"].mu.Lock()
		agent.monitors["service3"].status = ServiceStatusHealthy
		agent.monitors["service3"].mu.Unlock()

		status := agent.GetStatus()
		assert.Equal(t, AgentStatusDegraded, status)
	})
}

func TestAgent_GetServiceStatuses(t *testing.T) {
	logger := zerolog.Nop()

	services := []*meshv1.ServiceInfo{
		{Name: "api", Port: 8080},
		{Name: "frontend", Port: 3000},
	}

	agent, err := New(Config{
		Context:  context.Background(),
		AgentID:  "test-agent",
		Services: services,
		Logger:   logger,
	})
	require.NoError(t, err)

	// Set known statuses.
	now := time.Now()
	agent.monitors["api"].mu.Lock()
	agent.monitors["api"].status = ServiceStatusHealthy
	agent.monitors["api"].lastCheck = now
	agent.monitors["api"].mu.Unlock()

	agent.monitors["frontend"].mu.Lock()
	agent.monitors["frontend"].status = ServiceStatusUnhealthy
	agent.monitors["frontend"].lastCheck = now
	agent.monitors["frontend"].mu.Unlock()

	statuses := agent.GetServiceStatuses()

	assert.Len(t, statuses, 2)
	assert.Equal(t, ServiceStatusHealthy, statuses["api"].Status)
	assert.Equal(t, ServiceStatusUnhealthy, statuses["frontend"].Status)
	assert.False(t, statuses["api"].LastCheck.IsZero())
	assert.False(t, statuses["frontend"].LastCheck.IsZero())
}

// TestAgent_BeylaIntegration tests Beyla integration with agent (RFD 032).
func TestAgent_BeylaIntegration(t *testing.T) {
	logger := zerolog.Nop()

	t.Run("agent with Beyla enabled", func(t *testing.T) {
		services := []*meshv1.ServiceInfo{
			{Name: "api", Port: 8080},
		}

		beylaConfig := &beyla.Config{
			Enabled:      true,
			OTLPEndpoint: "localhost:4318",
			SamplingRate: 1.0,
			Discovery: beyla.DiscoveryConfig{
				OpenPorts: []int{8080},
			},
			Protocols: beyla.ProtocolsConfig{
				HTTPEnabled: true,
				GRPCEnabled: true,
			},
			Attributes: map[string]string{
				"colony.id": "test-colony",
			},
		}

		agent, err := New(Config{
			Context:     context.Background(),
			AgentID:     "test-agent",
			Services:    services,
			BeylaConfig: beylaConfig,
			Logger:      logger,
		})

		require.NoError(t, err)
		assert.NotNil(t, agent)
		assert.NotNil(t, agent.GetBeylaManager())

		// Start agent (should start Beyla).
		err = agent.Start()
		assert.NoError(t, err)

		// Beyla manager should be running.
		assert.True(t, agent.GetBeylaManager().IsRunning())

		// Stop agent (should stop Beyla).
		err = agent.Stop()
		assert.NoError(t, err)

		// Beyla manager should be stopped.
		assert.False(t, agent.GetBeylaManager().IsRunning())
	})

	t.Run("agent with Beyla disabled", func(t *testing.T) {
		services := []*meshv1.ServiceInfo{
			{Name: "api", Port: 8080},
		}

		beylaConfig := &beyla.Config{
			Enabled: false,
		}

		agent, err := New(Config{
			Context:     context.Background(),
			AgentID:     "test-agent",
			Services:    services,
			BeylaConfig: beylaConfig,
			Logger:      logger,
		})

		require.NoError(t, err)
		assert.NotNil(t, agent)
		assert.NotNil(t, agent.GetBeylaManager())

		// Start agent.
		err = agent.Start()
		assert.NoError(t, err)

		// Beyla manager should not be running (disabled).
		assert.False(t, agent.GetBeylaManager().IsRunning())

		// Stop agent.
		err = agent.Stop()
		assert.NoError(t, err)
	})

	t.Run("agent without Beyla config", func(t *testing.T) {
		services := []*meshv1.ServiceInfo{
			{Name: "api", Port: 8080},
		}

		agent, err := New(Config{
			Context:     context.Background(),
			AgentID:     "test-agent",
			Services:    services,
			BeylaConfig: nil,
			Logger:      logger,
		})

		require.NoError(t, err)
		assert.NotNil(t, agent)
		assert.Nil(t, agent.GetBeylaManager())

		// Start and stop should work without Beyla.
		err = agent.Start()
		assert.NoError(t, err)

		err = agent.Stop()
		assert.NoError(t, err)
	})
}
