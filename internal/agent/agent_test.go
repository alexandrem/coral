package agent

import (
	"testing"
	"time"

	meshv1 "github.com/coral-io/coral/coral/mesh/v1"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	logger := zerolog.Nop()

	t.Run("successful creation", func(t *testing.T) {
		services := []*meshv1.ServiceInfo{
			{ComponentName: "api", Port: 8080},
		}

		agent, err := New(Config{
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
			{ComponentName: "api", Port: 8080},
			{ComponentName: "frontend", Port: 3000},
			{ComponentName: "redis", Port: 6379},
		}

		agent, err := New(Config{
			AgentID:  "test-agent",
			Services: services,
			Logger:   logger,
		})

		require.NoError(t, err)
		assert.Equal(t, 3, agent.GetServiceCount())
	})

	t.Run("empty agent_id", func(t *testing.T) {
		services := []*meshv1.ServiceInfo{
			{ComponentName: "api", Port: 8080},
		}

		_, err := New(Config{
			AgentID:  "",
			Services: services,
			Logger:   logger,
		})

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "agent_id is required")
	})

	t.Run("no services", func(t *testing.T) {
		_, err := New(Config{
			AgentID:  "test-agent",
			Services: []*meshv1.ServiceInfo{},
			Logger:   logger,
		})

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "at least one service is required")
	})
}

func TestAgent_StartStop(t *testing.T) {
	logger := zerolog.Nop()

	services := []*meshv1.ServiceInfo{
		{ComponentName: "api", Port: 8080},
		{ComponentName: "frontend", Port: 3000},
	}

	agent, err := New(Config{
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
			{ComponentName: "service1", Port: 8080},
			{ComponentName: "service2", Port: 8081},
		}

		agent, err := New(Config{
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
			{ComponentName: "service1", Port: 8080},
			{ComponentName: "service2", Port: 8081},
		}

		agent, err := New(Config{
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
			{ComponentName: "service1", Port: 8080},
			{ComponentName: "service2", Port: 8081},
			{ComponentName: "service3", Port: 8082},
		}

		agent, err := New(Config{
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
		{ComponentName: "api", Port: 8080},
		{ComponentName: "frontend", Port: 3000},
	}

	agent, err := New(Config{
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
