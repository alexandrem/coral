package agent

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
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
		assert.Equal(t, 0, len(agent.services))
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
			{Name: "service1", Port: 8080, HealthEndpoint: "/health"},
			{Name: "service2", Port: 8081, HealthEndpoint: "/health"},
		}

		agent, err := New(Config{
			Context:  context.Background(),
			AgentID:  "test-agent",
			Services: services,
			Logger:   logger,
		})
		require.NoError(t, err)

		// Manually set all monitors to healthy.
		for _, entry := range agent.services {
			entry.Monitor.mu.Lock()
			entry.Monitor.status = ServiceStatusHealthy
			entry.Monitor.mu.Unlock()
		}

		status := agent.GetStatus()
		assert.Equal(t, AgentStatusHealthy, status)
	})

	t.Run("all services unhealthy", func(t *testing.T) {
		services := []*meshv1.ServiceInfo{
			{Name: "service1", Port: 8080, HealthEndpoint: "/health"},
			{Name: "service2", Port: 8081, HealthEndpoint: "/health"},
		}

		agent, err := New(Config{
			Context:  context.Background(),
			AgentID:  "test-agent",
			Services: services,
			Logger:   logger,
		})
		require.NoError(t, err)

		// Manually set all monitors to unhealthy.
		for _, entry := range agent.services {
			entry.Monitor.mu.Lock()
			entry.Monitor.status = ServiceStatusUnhealthy
			entry.Monitor.mu.Unlock()
		}

		status := agent.GetStatus()
		assert.Equal(t, AgentStatusUnhealthy, status)
	})

	t.Run("some services unhealthy - degraded", func(t *testing.T) {
		services := []*meshv1.ServiceInfo{
			{Name: "service1", Port: 8080, HealthEndpoint: "/health"},
			{Name: "service2", Port: 8081, HealthEndpoint: "/health"},
			{Name: "service3", Port: 8082, HealthEndpoint: "/health"},
		}

		agent, err := New(Config{
			Context:  context.Background(),
			AgentID:  "test-agent",
			Services: services,
			Logger:   logger,
		})
		require.NoError(t, err)

		// Set mixed statuses.
		agent.services[8080].Monitor.mu.Lock()
		agent.services[8080].Monitor.status = ServiceStatusHealthy
		agent.services[8080].Monitor.mu.Unlock()

		agent.services[8081].Monitor.mu.Lock()
		agent.services[8081].Monitor.status = ServiceStatusUnhealthy
		agent.services[8081].Monitor.mu.Unlock()

		agent.services[8082].Monitor.mu.Lock()
		agent.services[8082].Monitor.status = ServiceStatusHealthy
		agent.services[8082].Monitor.mu.Unlock()

		status := agent.GetStatus()
		assert.Equal(t, AgentStatusDegraded, status)
	})
}

func TestAgent_GetServiceStatuses(t *testing.T) {
	logger := zerolog.Nop()

	services := []*meshv1.ServiceInfo{
		{Name: "api", Port: 8080, HealthEndpoint: "/health"},
		{Name: "frontend", Port: 3000, HealthEndpoint: "/health"},
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
	agent.services[8080].Monitor.mu.Lock()
	agent.services[8080].Monitor.status = ServiceStatusHealthy
	agent.services[8080].Monitor.lastCheck = now
	agent.services[8080].Monitor.mu.Unlock()

	agent.services[3000].Monitor.mu.Lock()
	agent.services[3000].Monitor.status = ServiceStatusUnhealthy
	agent.services[3000].Monitor.lastCheck = now
	agent.services[3000].Monitor.mu.Unlock()

	statuses := agent.GetServiceStatuses()

	assert.Len(t, statuses, 2)
	assert.Equal(t, ServiceStatusHealthy, statuses["api"].Status)
	assert.Equal(t, ServiceStatusUnhealthy, statuses["frontend"].Status)
	assert.False(t, statuses["api"].LastCheck.IsZero())
	assert.False(t, statuses["frontend"].LastCheck.IsZero())
}

// TestServiceMap_AutoFromOTLP verifies that onBeylaServiceObserved (RFD 103
// feedback) creates a Tier 0 ServiceEntry with no monitor and an
// auto-derived name (RFD 104).
func TestServiceMap_AutoFromOTLP(t *testing.T) {
	logger := zerolog.Nop()

	agent, err := New(Config{
		Context: context.Background(),
		AgentID: "test-agent",
		Logger:  logger,
	})
	require.NoError(t, err)

	agent.onBeylaServiceObserved(3000, 12345, "node")

	agent.mu.RLock()
	entry, ok := agent.services[3000]
	agent.mu.RUnlock()

	require.True(t, ok)
	assert.Equal(t, NamingSourceAuto, entry.NamingSource)
	assert.Equal(t, TierObserved, entry.Tier)
	assert.Nil(t, entry.Monitor)
	assert.NotEmpty(t, entry.AutoName)
	assert.Equal(t, int32(12345), entry.PID)
}

// TestServiceMap_WatchWithHealthEnriches verifies that ConnectService with a
// health endpoint promotes an existing auto-observed entry to Tier 1,
// setting the authoritative name and starting a ServiceMonitor (RFD 104).
func TestServiceMap_WatchWithHealthEnriches(t *testing.T) {
	logger := zerolog.Nop()

	agent, err := New(Config{
		Context: context.Background(),
		AgentID: "test-agent",
		Logger:  logger,
	})
	require.NoError(t, err)

	agent.onBeylaServiceObserved(3000, 12345, "node")

	err = agent.ConnectService(&meshv1.ServiceInfo{
		Name:           "frontend",
		Port:           3000,
		HealthEndpoint: "/health",
	})
	require.NoError(t, err)

	agent.mu.RLock()
	entry := agent.services[3000]
	agent.mu.RUnlock()

	assert.Equal(t, "frontend", entry.AuthoritativeName)
	assert.Equal(t, NamingSourceAuthoritative, entry.NamingSource)
	assert.Equal(t, TierWatched, entry.Tier)
	assert.Equal(t, "frontend", entry.Name())
	require.NotNil(t, entry.Monitor)

	require.NoError(t, agent.DisconnectService("frontend"))
}

// TestServiceMap_WatchWithoutHealthNoMonitor verifies that ConnectService
// without a health endpoint records an authoritative entry but does not
// start a ServiceMonitor (RFD 104).
func TestServiceMap_WatchWithoutHealthNoMonitor(t *testing.T) {
	logger := zerolog.Nop()

	agent, err := New(Config{
		Context: context.Background(),
		AgentID: "test-agent",
		Logger:  logger,
	})
	require.NoError(t, err)

	err = agent.ConnectService(&meshv1.ServiceInfo{
		Name: "redis",
		Port: 6379,
	})
	require.NoError(t, err)

	agent.mu.RLock()
	entry := agent.services[6379]
	agent.mu.RUnlock()

	assert.Equal(t, "redis", entry.AuthoritativeName)
	assert.Equal(t, NamingSourceAuthoritative, entry.NamingSource)
	assert.Equal(t, TierObserved, entry.Tier)
	assert.Nil(t, entry.Monitor)
}

func TestListServicesIncludesUnifiedServiceMapAndFiltersByNamingSource(t *testing.T) {
	logger := zerolog.Nop()
	agent, err := New(Config{
		Context: context.Background(),
		AgentID: "test-agent",
		Logger:  logger,
	})
	require.NoError(t, err)

	// One observed service is promoted to watched; the other remains Tier 0.
	agent.onBeylaServiceObserved(3000, 12345, "node")
	agent.onBeylaServiceObserved(6379, 23456, "redis-server")
	require.NoError(t, agent.ConnectService(&meshv1.ServiceInfo{
		Name:           "frontend",
		Port:           3000,
		HealthEndpoint: "/health",
	}))

	handler := NewServiceHandler(agent, nil, nil, nil, nil, nil, nil)
	all, err := handler.ListServices(context.Background(), connect.NewRequest(&agentv1.ListServicesRequest{}))
	require.NoError(t, err)
	require.Len(t, all.Msg.Services, 2)

	servicesByPort := make(map[int32]*agentv1.ServiceStatus, len(all.Msg.Services))
	for _, service := range all.Msg.Services {
		servicesByPort[service.Port] = service
	}

	auto := servicesByPort[6379]
	require.NotNil(t, auto)
	assert.NotEmpty(t, auto.Name)
	assert.Equal(t, auto.AutoName, auto.Name)
	assert.Empty(t, auto.AuthoritativeName)
	assert.Equal(t, agentv1.ServiceNamingSource_SERVICE_NAMING_SOURCE_AUTO, auto.NamingSource)
	assert.False(t, auto.HasMonitor)
	assert.Zero(t, auto.ObservationTier)
	assert.Empty(t, auto.Status)

	watched := servicesByPort[3000]
	require.NotNil(t, watched)
	assert.Equal(t, "frontend", watched.Name)
	assert.NotEmpty(t, watched.AutoName)
	assert.Equal(t, "frontend", watched.AuthoritativeName)
	assert.Equal(t, agentv1.ServiceNamingSource_SERVICE_NAMING_SOURCE_AUTHORITATIVE, watched.NamingSource)
	assert.True(t, watched.HasMonitor)
	assert.EqualValues(t, TierWatched, watched.ObservationTier)

	autoSource := agentv1.ServiceNamingSource_SERVICE_NAMING_SOURCE_AUTO
	autoOnly, err := handler.ListServices(context.Background(), connect.NewRequest(&agentv1.ListServicesRequest{
		SourceFilter: &autoSource,
	}))
	require.NoError(t, err)
	require.Len(t, autoOnly.Msg.Services, 1)
	assert.Equal(t, int32(6379), autoOnly.Msg.Services[0].Port)

	authoritativeSource := agentv1.ServiceNamingSource_SERVICE_NAMING_SOURCE_AUTHORITATIVE
	authoritativeOnly, err := handler.ListServices(context.Background(), connect.NewRequest(&agentv1.ListServicesRequest{
		SourceFilter: &authoritativeSource,
	}))
	require.NoError(t, err)
	require.Len(t, authoritativeOnly.Msg.Services, 1)
	assert.Equal(t, int32(3000), authoritativeOnly.Msg.Services[0].Port)
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
