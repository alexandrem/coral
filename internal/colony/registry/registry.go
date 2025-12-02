// Package registry provides service registration and discovery for the colony.
package registry

import (
	"fmt"
	"sync"
	"time"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
)

const (
	// Status thresholds based on last_seen timestamp.
	StatusHealthyThreshold  = 30 * time.Second
	StatusDegradedThreshold = 2 * time.Minute
)

// AgentStatus represents the health status of an agent.
type AgentStatus string

const (
	StatusHealthy   AgentStatus = "healthy"
	StatusDegraded  AgentStatus = "degraded"
	StatusUnhealthy AgentStatus = "unhealthy"
)

// Entry represents a registered agent in the colony.
type Entry struct {
	AgentID         string
	Name            string // Deprecated: Use Services field for multi-service agents
	MeshIPv4        string
	MeshIPv6        string
	RegisteredAt    time.Time
	LastSeen        time.Time
	Services        []*meshv1.ServiceInfo           // RFD 011: Multi-service support
	RuntimeContext  *agentv1.RuntimeContextResponse // RFD 018: Runtime context
	ProtocolVersion string                          // RFD 018: Protocol version
}

// Registry is an in-memory store for agent registrations.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]*Entry
}

// New creates a new Registry.
func New() *Registry {
	return &Registry{
		entries: make(map[string]*Entry),
	}
}

// Register adds or updates an agent registration.
// For backward compatibility, componentName can be provided for single-service agents.
// Multi-service agents should provide services instead.
// Agents can register without services and add them later.
func (r *Registry) Register(
	agentID, name, meshIPv4, meshIPv6 string,
	services []*meshv1.ServiceInfo,
	runtimeContext *agentv1.RuntimeContextResponse,
	protocolVersion string,
) (*Entry, error) {
	if agentID == "" {
		return nil, fmt.Errorf("agent_id cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()

	// Check if agent already exists.
	if existing, ok := r.entries[agentID]; ok {
		// Update existing entry.
		existing.Name = name
		existing.MeshIPv4 = meshIPv4
		existing.MeshIPv6 = meshIPv6
		existing.LastSeen = now
		existing.Services = services
		existing.RuntimeContext = runtimeContext
		existing.ProtocolVersion = protocolVersion
		return existing, nil
	}

	// Create new entry.
	entry := &Entry{
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

	r.entries[agentID] = entry
	return entry, nil
}

// UpdateHeartbeat updates the last_seen timestamp for an agent.
func (r *Registry) UpdateHeartbeat(agentID string) error {
	if agentID == "" {
		return fmt.Errorf("agent_id cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.entries[agentID]
	if !ok {
		return fmt.Errorf("agent not found: %s", agentID)
	}

	entry.LastSeen = time.Now()
	return nil
}

// Get retrieves an agent registration by agent ID.
func (r *Registry) Get(agentID string) (*Entry, error) {
	if agentID == "" {
		return nil, fmt.Errorf("agent_id cannot be empty")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.entries[agentID]
	if !ok {
		return nil, fmt.Errorf("agent not found: %s", agentID)
	}

	return entry, nil
}

// ListAll returns all registered agents.
func (r *Registry) ListAll() []*Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entries := make([]*Entry, 0, len(r.entries))
	for _, entry := range r.entries {
		entries = append(entries, entry)
	}
	return entries
}

// CountActive returns the number of agents with healthy or degraded status.
func (r *Registry) CountActive() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	now := time.Now()
	count := 0
	for _, entry := range r.entries {
		status := DetermineStatus(entry.LastSeen, now)
		if status == StatusHealthy || status == StatusDegraded {
			count++
		}
	}
	return count
}

// Count returns the total number of registered agents.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}

// DetermineStatus calculates agent status based on last_seen timestamp.
func DetermineStatus(lastSeen, now time.Time) AgentStatus {
	elapsed := now.Sub(lastSeen)

	if elapsed < StatusHealthyThreshold {
		return StatusHealthy
	} else if elapsed < StatusDegradedThreshold {
		return StatusDegraded
	}
	return StatusUnhealthy
}

// FindAgentForService returns the first agent running the specified service.
func (r *Registry) FindAgentForService(serviceName string) (*Entry, *meshv1.ServiceInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, entry := range r.entries {
		for _, service := range entry.Services {
			if service.Name == serviceName {
				return entry, service, nil
			}
		}
	}

	return nil, nil, fmt.Errorf("service not found: %s", serviceName)
}
