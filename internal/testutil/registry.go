package testutil

import (
	"fmt"
	"sync"
	"time"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/colony/registry"
)

// MockRegistry provides a simple mock implementation of the agent registry.
type MockRegistry struct {
	mu      sync.RWMutex
	entries map[string]*registry.Entry
}

// NewMockRegistry creates a new mock registry with optional initial entries.
func NewMockRegistry(agents ...*registry.Entry) *MockRegistry {
	entries := make(map[string]*registry.Entry)
	for _, agent := range agents {
		entries[agent.AgentID] = agent
	}
	return &MockRegistry{
		entries: entries,
	}
}

// Register adds or updates an agent in the mock registry.
func (m *MockRegistry) Register(
	agentID, name, meshIPv4, meshIPv6 string,
	services []*meshv1.ServiceInfo,
	runtimeContext *agentv1.RuntimeContextResponse,
	protocolVersion string,
) (*registry.Entry, error) {
	if agentID == "" {
		return nil, fmt.Errorf("agent_id cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()

	var entry *registry.Entry
	if existing, ok := m.entries[agentID]; ok {
		existing.Name = name
		existing.MeshIPv4 = meshIPv4
		existing.MeshIPv6 = meshIPv6
		existing.LastSeen = now
		existing.Services = services
		existing.RuntimeContext = runtimeContext
		existing.ProtocolVersion = protocolVersion
		entry = existing
	} else {
		entry = &registry.Entry{
			AgentID:         agentID,
			Name:            name,
			MeshIPv4:        meshIPv4,
			MeshIPv6:        meshIPv6,
			RegisteredAt:    now,
			LastSeen:        now,
			Services:        services,
			RuntimeContext:  runtimeContext,
			ProtocolVersion: protocolVersion,
		}
		m.entries[agentID] = entry
	}

	return entry, nil
}

// Get retrieves an agent by ID.
func (m *MockRegistry) Get(agentID string) (*registry.Entry, error) {
	if agentID == "" {
		return nil, fmt.Errorf("agent_id cannot be empty")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.entries[agentID]
	if !ok {
		return nil, fmt.Errorf("agent not found: %s", agentID)
	}

	return entry, nil
}

// ListAll returns all registered agents.
func (m *MockRegistry) ListAll() []*registry.Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entries := make([]*registry.Entry, 0, len(m.entries))
	for _, entry := range m.entries {
		entries = append(entries, entry)
	}
	return entries
}

// UpdateHeartbeat updates the last_seen timestamp for an agent.
func (m *MockRegistry) UpdateHeartbeat(agentID string) error {
	if agentID == "" {
		return fmt.Errorf("agent_id cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.entries[agentID]
	if !ok {
		return fmt.Errorf("agent not found: %s", agentID)
	}

	entry.LastSeen = time.Now()
	return nil
}

// Count returns the total number of registered agents.
func (m *MockRegistry) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.entries)
}
