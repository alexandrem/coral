package agent

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/agent/beyla"
	"github.com/coral-mesh/coral/internal/agent/beyla/discovery"
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
		agent.services[serviceKey(8080, "")].Monitor.mu.Lock()
		agent.services[serviceKey(8080, "")].Monitor.status = ServiceStatusHealthy
		agent.services[serviceKey(8080, "")].Monitor.mu.Unlock()

		agent.services[serviceKey(8081, "")].Monitor.mu.Lock()
		agent.services[serviceKey(8081, "")].Monitor.status = ServiceStatusUnhealthy
		agent.services[serviceKey(8081, "")].Monitor.mu.Unlock()

		agent.services[serviceKey(8082, "")].Monitor.mu.Lock()
		agent.services[serviceKey(8082, "")].Monitor.status = ServiceStatusHealthy
		agent.services[serviceKey(8082, "")].Monitor.mu.Unlock()

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
	agent.services[serviceKey(8080, "")].Monitor.mu.Lock()
	agent.services[serviceKey(8080, "")].Monitor.status = ServiceStatusHealthy
	agent.services[serviceKey(8080, "")].Monitor.lastCheck = now
	agent.services[serviceKey(8080, "")].Monitor.mu.Unlock()

	agent.services[serviceKey(3000, "")].Monitor.mu.Lock()
	agent.services[serviceKey(3000, "")].Monitor.status = ServiceStatusUnhealthy
	agent.services[serviceKey(3000, "")].Monitor.lastCheck = now
	agent.services[serviceKey(3000, "")].Monitor.mu.Unlock()

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
	entry, ok := agent.services[serviceKey(3000, "")]
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
	entry := agent.services[serviceKey(3000, "")]
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
	entry := agent.services[serviceKey(6379, "")]
	agent.mu.RUnlock()

	assert.Equal(t, "redis", entry.AuthoritativeName)
	assert.Equal(t, NamingSourceAuthoritative, entry.NamingSource)
	assert.Equal(t, TierObserved, entry.Tier)
	assert.Nil(t, entry.Monitor)
}

// TestServiceMap_ExePatternStartsMonitor verifies a portless connect (RFD
// 111) gets a running ServiceMonitor even though it has no HealthEndpoint —
// unlike a bare port-only connect, which stays Tier 0 (see
// TestServiceMap_WatchWithoutHealthNoMonitor above). A portless service has
// no other way to be liveness-checked.
func TestServiceMap_ExePatternStartsMonitor(t *testing.T) {
	logger := zerolog.Nop()

	agent, err := New(Config{
		Context: context.Background(),
		AgentID: "test-agent",
		Logger:  logger,
	})
	require.NoError(t, err)

	err = agent.ConnectService(&meshv1.ServiceInfo{
		Name:       "worker",
		ExePattern: "python.*consumer.py",
	})
	require.NoError(t, err)

	agent.mu.RLock()
	entry := agent.services[serviceKey(0, "worker")]
	agent.mu.RUnlock()

	assert.Equal(t, "worker", entry.AuthoritativeName)
	assert.Equal(t, "python.*consumer.py", entry.ExePattern)
	assert.Equal(t, TierWatched, entry.Tier)
	require.NotNil(t, entry.Monitor)
}

// TestServiceMap_MultipleExePatternConnectsDontCollide verifies two
// portless connects don't collide in the port-keyed service map (RFD 111
// readiness gap: Port == 0 for every portless service).
func TestServiceMap_MultipleExePatternConnectsDontCollide(t *testing.T) {
	logger := zerolog.Nop()

	agent, err := New(Config{
		Context: context.Background(),
		AgentID: "test-agent",
		Logger:  logger,
	})
	require.NoError(t, err)

	require.NoError(t, agent.ConnectService(&meshv1.ServiceInfo{
		Name:       "worker-a",
		ExePattern: "python.*a.py",
	}))
	require.NoError(t, agent.ConnectService(&meshv1.ServiceInfo{
		Name:       "worker-b",
		ExePattern: "python.*b.py",
	}))

	assert.Equal(t, 2, agent.GetServiceCount())

	agent.mu.RLock()
	entryA, okA := agent.services[serviceKey(0, "worker-a")]
	entryB, okB := agent.services[serviceKey(0, "worker-b")]
	agent.mu.RUnlock()

	require.True(t, okA)
	require.True(t, okB)
	assert.Equal(t, "worker-a", entryA.AuthoritativeName)
	assert.Equal(t, "worker-b", entryB.AuthoritativeName)
}

// TestCollectDiscoveryCandidates_ExePattern verifies a portless connect
// produces a client-only ProcessCandidate carrying the literal exe pattern
// (RFD 111), not just the service name.
func TestCollectDiscoveryCandidates_ExePattern(t *testing.T) {
	logger := zerolog.Nop()

	agent, err := New(Config{
		Context: context.Background(),
		AgentID: "test-agent",
		Logger:  logger,
	})
	require.NoError(t, err)

	require.NoError(t, agent.ConnectService(&meshv1.ServiceInfo{
		Name:       "worker",
		ExePattern: "python.*consumer.py",
	}))

	agent.mu.RLock()
	candidates := agent.collectDiscoveryCandidatesLocked()
	agent.mu.RUnlock()

	require.Len(t, candidates, 1)
	assert.Equal(t, "worker", candidates[0].Name)
	assert.True(t, candidates[0].IsClientOnly)
	assert.Equal(t, "python.*consumer.py", candidates[0].ExePathPattern)
	assert.Empty(t, candidates[0].Ports)
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

// fakePortOwners is an in-memory portOwnerFinder. listening maps each port
// to the PID holding its listening socket. candidates instead lists a port's
// listeners, one per network namespace, filtered by the query's Accept.
type fakePortOwners struct {
	mu         sync.Mutex
	listening  map[int]int
	ambiguous  map[int]bool
	candidates map[int][]int
	queries    map[int]discovery.PortQuery
	scans      int
}

func newFakePortOwners() *fakePortOwners {
	return &fakePortOwners{listening: map[int]int{}, ambiguous: map[int]bool{}, candidates: map[int][]int{}}
}

func (f *fakePortOwners) set(port, pid int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listening[port] = pid
}

func (f *fakePortOwners) scanCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.scans
}

func (f *fakePortOwners) FindPortOwners(queries map[int]discovery.PortQuery) (map[int]discovery.PortOwner, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scans++
	f.queries = queries
	owners := make(map[int]discovery.PortOwner, len(queries))
	for port, query := range queries {
		switch {
		case len(f.candidates[port]) > 0:
			owner := discovery.PortOwner{Namespaces: len(f.candidates[port])}
			var matched []int
			for _, pid := range f.candidates[port] {
				if query.Accept == nil || query.Accept(pid) {
					matched = append(matched, pid)
				}
			}
			owner.Matching = len(matched)
			if len(matched) == 1 {
				owner.PID = matched[0]
			}
			owners[port] = owner
		case f.ambiguous[port]:
			owners[port] = discovery.PortOwner{Namespaces: 2, Matching: 2}
		case f.listening[port] != 0:
			owners[port] = discovery.PortOwner{PID: f.listening[port], Namespaces: 1, Matching: 1}
		default:
			owners[port] = discovery.PortOwner{}
		}
	}
	return owners, nil
}

// fakeLabels is an in-memory labelMatcher: each PID's container labels, or
// err for every lookup.
type fakeLabels struct {
	byPID map[int]map[string]string
	err   error
}

func (f fakeLabels) Matches(pid int, want map[string]string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	labels, ok := f.byPID[pid]
	if !ok {
		return false, errNotDockerContainer
	}
	for key, value := range want {
		if labels[key] != value {
			return false, nil
		}
	}
	return true, nil
}

func (f *fakePortOwners) ListensOn(pid, port int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return pid != 0 && f.listening[port] == pid
}

// newTestAgentWithPorts returns an agent whose port lookups use owners.
func newTestAgentWithPorts(t *testing.T, owners *fakePortOwners, services ...*meshv1.ServiceInfo) *Agent {
	t.Helper()
	agent, err := New(Config{
		Context:  context.Background(),
		AgentID:  "test-agent",
		Services: services,
		Logger:   zerolog.Nop(),
	})
	require.NoError(t, err)
	agent.portOwners = owners
	return agent
}

func watchedOnly(entry *ServiceEntry) bool {
	return entry.NamingSource == NamingSourceAuthoritative
}

func entryPID(a *Agent, port int32) int32 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.services[serviceKey(port, "")].PID
}

// TestRefreshServicePIDs_BarePortWatch verifies a port-only service from the
// config file gets its PID from the listening socket, without a monitor or
// any traffic, and that the PID follows restarts and exits.
func TestRefreshServicePIDs_BarePortWatch(t *testing.T) {
	owners := newFakePortOwners()
	agent := newTestAgentWithPorts(t, owners, &meshv1.ServiceInfo{Name: "api", Port: 8080})

	owners.set(8080, 4242)
	agent.refreshServicePIDs(watchedOnly)
	assert.EqualValues(t, 4242, entryPID(agent, 8080))
	assert.Equal(t, 1, owners.scanCount())

	// A PID that still listens is only re-verified, never re-scanned.
	agent.refreshServicePIDs(watchedOnly)
	assert.Equal(t, 1, owners.scanCount())

	handler := NewServiceHandler(agent, nil, nil, nil, nil, nil, nil)
	resp, err := handler.ListServices(context.Background(), connect.NewRequest(&agentv1.ListServicesRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Services, 1)
	assert.EqualValues(t, 4242, resp.Msg.Services[0].ProcessId)

	// The container restarts with a new PID.
	owners.set(8080, 5555)
	agent.refreshServicePIDs(watchedOnly)
	assert.EqualValues(t, 5555, entryPID(agent, 8080))

	// The service stops: the stale PID must not be kept.
	owners.set(8080, 0)
	agent.refreshServicePIDs(watchedOnly)
	assert.Zero(t, entryPID(agent, 8080))
}

// TestRefreshServicePIDs_AmbiguousPortLeftUnresolved verifies a port with
// listeners in several network namespaces is not attributed to either, and
// that the operator is warned once rather than on every refresh.
func TestRefreshServicePIDs_AmbiguousPortLeftUnresolved(t *testing.T) {
	owners := newFakePortOwners()
	owners.ambiguous[8080] = true
	agent := newTestAgentWithPorts(t, owners, &meshv1.ServiceInfo{Name: "api", Port: 8080})
	var logs bytes.Buffer
	agent.logger = zerolog.New(&logs)
	warnings := func() int { return strings.Count(logs.String(), "OTEL_SERVICE_NAME=api") }

	agent.refreshServicePIDs(watchedOnly)
	agent.refreshServicePIDs(watchedOnly)
	assert.Zero(t, entryPID(agent, 8080))
	assert.Equal(t, 1, warnings())

	// Once resolved, a later ambiguity is reported again.
	owners.ambiguous[8080] = false
	owners.set(8080, 4242)
	agent.refreshServicePIDs(watchedOnly)
	assert.EqualValues(t, 4242, entryPID(agent, 8080))

	owners.set(8080, 0)
	owners.ambiguous[8080] = true
	agent.refreshServicePIDs(watchedOnly)
	assert.Equal(t, 2, warnings())
}

// TestRefreshServicePIDs_UpdatesMonitor verifies a watched service with a
// health endpoint reports the PID resolved from its listening socket.
func TestRefreshServicePIDs_UpdatesMonitor(t *testing.T) {
	owners := newFakePortOwners()
	owners.set(3000, 777)
	agent := newTestAgentWithPorts(t, owners, &meshv1.ServiceInfo{Name: "web", Port: 3000, HealthEndpoint: "/health"})

	agent.refreshServicePIDs(watchedOnly)

	agent.mu.RLock()
	monitor := agent.services[serviceKey(3000, "")].Monitor
	agent.mu.RUnlock()
	require.NotNil(t, monitor)
	assert.EqualValues(t, 777, monitor.GetStatus().ProcessID)
}

// TestConnectService_RequestsPIDRefresh verifies a runtime watch asks the
// PID tracker to resolve the new service immediately.
func TestConnectService_RequestsPIDRefresh(t *testing.T) {
	agent := newTestAgentWithPorts(t, newFakePortOwners())

	require.NoError(t, agent.ConnectService(&meshv1.ServiceInfo{Name: "api", Port: 8080}))
	require.NoError(t, agent.ConnectService(&meshv1.ServiceInfo{Name: "db", Port: 5432}))

	// Requests coalesce; the tracker picks up both services in one refresh.
	assert.Len(t, agent.pidRefresh, 1)
}

func TestResolveServicePID(t *testing.T) {
	owners := newFakePortOwners()
	agent := newTestAgentWithPorts(t, owners, &meshv1.ServiceInfo{Name: "api", Port: 8080})

	t.Run("unknown service", func(t *testing.T) {
		_, err := agent.ResolveServicePID("missing")
		assert.True(t, errors.Is(err, errServiceNotFound), "err = %v", err)
	})

	t.Run("no listener", func(t *testing.T) {
		_, err := agent.ResolveServicePID("api")
		require.Error(t, err)
		assert.False(t, errors.Is(err, errServiceNotFound))
	})

	t.Run("resolves an unrecorded PID on demand", func(t *testing.T) {
		owners.set(8080, 100)
		pid, err := agent.ResolveServicePID("api")
		require.NoError(t, err)
		assert.EqualValues(t, 100, pid)
	})

	t.Run("verified PID is returned without a scan", func(t *testing.T) {
		before := owners.scanCount()
		pid, err := agent.ResolveServicePID("api")
		require.NoError(t, err)
		assert.EqualValues(t, 100, pid)
		assert.Equal(t, before, owners.scanCount())
	})

	t.Run("stale PID is replaced", func(t *testing.T) {
		owners.set(8080, 200)
		pid, err := agent.ResolveServicePID("api")
		require.NoError(t, err)
		assert.EqualValues(t, 200, pid)
		assert.EqualValues(t, 200, entryPID(agent, 8080))
	})
}

// TestResolveServicePID_AutoObservedStalePID verifies a PID reported by
// Beyla is checked too, not trusted blindly.
func TestResolveServicePID_AutoObservedStalePID(t *testing.T) {
	owners := newFakePortOwners()
	agent := newTestAgentWithPorts(t, owners)
	agent.onBeylaServiceObserved(6379, 111, "redis-server")

	agent.mu.RLock()
	name := agent.services[serviceKey(6379, "")].Name()
	agent.mu.RUnlock()

	owners.set(6379, 222)
	pid, err := agent.ResolveServicePID(name)
	require.NoError(t, err)
	assert.EqualValues(t, 222, pid)
}

// TestRefreshServicePIDs_Selectors verifies a watched service's process_env
// and docker_labels reach the port lookup and pick its container.
func TestRefreshServicePIDs_Selectors(t *testing.T) {
	owners := newFakePortOwners()
	owners.candidates[8080] = []int{100, 200, 300}
	agent := newTestAgentWithPorts(t, owners, &meshv1.ServiceInfo{
		Name:         "api",
		Port:         8080,
		ProcessEnv:   map[string]string{"APP_ENV": "production"},
		DockerLabels: map[string]string{"com.docker.swarm.service.name": "showup_api"},
	})
	agent.dockerLabels = fakeLabels{byPID: map[int]map[string]string{
		100: {"com.docker.compose.project": "showup-staging"},
		200: {"com.docker.swarm.service.name": "showup_api"},
		// PID 300 runs outside Docker.
	}}

	agent.refreshServicePIDs(watchedOnly)

	assert.EqualValues(t, 200, entryPID(agent, 8080))
	assert.Equal(t, map[string]string{"APP_ENV": "production"}, owners.queries[8080].Env)
	assert.Equal(t, "api", owners.queries[8080].Name)
}

// TestRefreshServicePIDs_SelectorWarnings verifies an unresolved PID is
// explained, once per distinct reason.
func TestRefreshServicePIDs_SelectorWarnings(t *testing.T) {
	owners := newFakePortOwners()
	owners.candidates[8080] = []int{100, 200}
	agent := newTestAgentWithPorts(t, owners, &meshv1.ServiceInfo{
		Name:         "api",
		Port:         8080,
		DockerLabels: map[string]string{"com.docker.swarm.service.name": "showup_api"},
	})
	var logs bytes.Buffer
	agent.logger = zerolog.New(&logs)

	agent.dockerLabels = fakeLabels{err: errors.New("dial unix /var/run/docker.sock: connect: no such file or directory")}
	agent.refreshServicePIDs(watchedOnly)
	agent.refreshServicePIDs(watchedOnly)
	assert.Zero(t, entryPID(agent, 8080))
	assert.Equal(t, 1, strings.Count(logs.String(), "Could not check the docker_labels"))

	agent.dockerLabels = fakeLabels{byPID: map[int]map[string]string{100: {}, 200: {}}}
	agent.refreshServicePIDs(watchedOnly)
	assert.Equal(t, 1, strings.Count(logs.String(), "matches the service's process_env and docker_labels"))
}

func TestConnectServiceRejectsSelectorsWithoutPort(t *testing.T) {
	agent := newTestAgentWithPorts(t, newFakePortOwners())
	handler := NewServiceHandler(agent, nil, nil, nil, nil, nil, nil)

	resp, err := handler.ConnectService(context.Background(), connect.NewRequest(&agentv1.ConnectServiceRequest{
		Name:       "worker",
		ExePattern: "consumer",
		ProcessEnv: map[string]string{"APP_ENV": "production"},
	}))
	require.NoError(t, err)
	assert.False(t, resp.Msg.Success)
	assert.Contains(t, resp.Msg.Error, "require a port")

	resp, err = handler.ConnectService(context.Background(), connect.NewRequest(&agentv1.ConnectServiceRequest{
		Name:         "api",
		Port:         8080,
		DockerLabels: map[string]string{"com.docker.compose.service": "api"},
	}))
	require.NoError(t, err)
	require.True(t, resp.Msg.Success, resp.Msg.Error)

	list, err := handler.ListServices(context.Background(), connect.NewRequest(&agentv1.ListServicesRequest{}))
	require.NoError(t, err)
	require.Len(t, list.Msg.Services, 1)
	assert.Equal(t, map[string]string{"com.docker.compose.service": "api"}, list.Msg.Services[0].DockerLabels)
}
