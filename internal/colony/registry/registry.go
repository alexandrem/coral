// Package registry provides service registration and discovery for the colony.
package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/rs/zerolog/log"
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
	db      *database.Database
}

// New creates a new Registry.
func New(db *database.Database) *Registry {
	return &Registry{
		entries: make(map[string]*Entry),
		db:      db,
	}
}

// LoadFromDatabase loads persisted services from the database into the registry.
// This should be called on startup to restore the registry state after a restart.
func (r *Registry) LoadFromDatabase(ctx context.Context) error {
	if r.db == nil {
		log.Debug().Msg("No database connection, skipping registry load")
		return nil
	}

	services, err := r.db.ListAllServices(ctx)
	if err != nil {
		return fmt.Errorf("failed to list services from database: %w", err)
	}

	if len(services) == 0 {
		log.Debug().Msg("No persisted services found in database")
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Group services by agent_id.
	agentServices := make(map[string][]*meshv1.ServiceInfo)
	agentLastSeen := make(map[string]time.Time)

	for _, svc := range services {
		// Parse labels from JSON string.
		var labels map[string]string
		if svc.Labels != "" && svc.Labels != "{}" {
			if err := json.Unmarshal([]byte(svc.Labels), &labels); err != nil {
				log.Warn().Err(err).Str("service_id", svc.ID).Msg("Failed to parse service labels")
				labels = make(map[string]string)
			}
		} else {
			labels = make(map[string]string)
		}

		serviceInfo := &meshv1.ServiceInfo{
			Name:   svc.Name,
			Labels: labels,
		}

		agentServices[svc.AgentID] = append(agentServices[svc.AgentID], serviceInfo)

		// Track the most recent last_seen for each agent.
		if lastSeen, ok := agentLastSeen[svc.AgentID]; !ok || svc.LastSeen.After(lastSeen) {
			agentLastSeen[svc.AgentID] = svc.LastSeen
		}
	}

	// Create registry entries for each agent.
	loadedCount := 0
	skippedCount := 0
	for agentID, services := range agentServices {
		lastSeen := agentLastSeen[agentID]

		// Skip entries with zero timestamps (corrupt/stale data from before timestamp initialization fix).
		// If the agent is actually running, it will re-register with correct timestamps.
		if lastSeen.IsZero() {
			log.Debug().Str("agent_id", agentID).Msg("Skipping agent with zero timestamp from database")
			skippedCount++
			continue
		}

		// Don't overwrite existing entries (agents already connected).
		if _, exists := r.entries[agentID]; exists {
			log.Debug().Str("agent_id", agentID).Msg("Agent already in registry, skipping database load")
			continue
		}

		entry := &Entry{
			AgentID:      agentID,
			Name:         "", // Legacy field, not restored
			MeshIPv4:     "", // Will be updated when agent reconnects
			MeshIPv6:     "", // Will be updated when agent reconnects
			RegisteredAt: lastSeen,
			LastSeen:     lastSeen,
			Services:     services,
		}

		r.entries[agentID] = entry
		loadedCount++
	}

	log.Info().
		Int("agents_loaded", loadedCount).
		Int("agents_skipped", skippedCount).
		Int("total_services", len(services)).
		Msg("Loaded persisted services from database")

	return nil
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
	var entry *Entry
	if existing, ok := r.entries[agentID]; ok {
		// Update existing entry.
		existing.Name = name
		existing.MeshIPv4 = meshIPv4
		existing.MeshIPv6 = meshIPv6
		existing.LastSeen = now
		existing.Services = services
		existing.RuntimeContext = runtimeContext
		existing.ProtocolVersion = protocolVersion
		entry = existing
	} else {
		// Create new entry.
		entry = &Entry{
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
	}

	// Persist to database asynchronously.
	if r.db != nil {
		log.Debug().Msg("DB is connected, attempting to persist services")
		// Create a copy of the validated data to avoid race conditions.
		servicesCopy := make([]*meshv1.ServiceInfo, len(services))
		copy(servicesCopy, services)

		go func(agentID, agentName string, services []*meshv1.ServiceInfo, lastSeen time.Time) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			// Handle legacy agents that only provide ComponentName (name)
			if len(services) == 0 && agentName != "" {
				log.Debug().Str("agent_id", agentID).Str("component_name", agentName).Msg("Persisting legacy component as service")
				// Create a synthetic service for legacy component
				dbService := &database.Service{
					ID:       fmt.Sprintf("%s:%s", agentID, agentName),
					Name:     agentName,
					AgentID:  agentID,
					Labels:   "{}", // Empty labels for legacy
					LastSeen: lastSeen,
					Status:   "active",
					AppID:    agentName,
					Version:  "unknown",
				}
				if err := r.db.UpsertService(ctx, dbService); err != nil {
					log.Warn().Err(err).
						Str("agent_id", agentID).
						Str("service_name", agentName).
						Msg("Failed to persist legacy service registration")
				} else {
					log.Debug().Str("service", agentName).Msg("Successfully upserted legacy service")
				}
				return
			}

			log.Debug().Int("service_count", len(services)).Str("agent_id", agentID).Msg("Persisting services for agent")

			for _, s := range services {
				// Serialize labels.
				labelsBytes, _ := json.Marshal(s.Labels)

				dbService := &database.Service{
					ID:       fmt.Sprintf("%s:%s", agentID, s.Name),
					Name:     s.Name,
					AgentID:  agentID,
					Labels:   string(labelsBytes),
					LastSeen: lastSeen,
					Status:   "active",
					// AppID and Version are not currently populated in ServiceInfo
					AppID:   s.Name,
					Version: "unknown",
				}

				if err := r.db.UpsertService(ctx, dbService); err != nil {
					log.Warn().Err(err).
						Str("agent_id", agentID).
						Str("service_name", s.Name).
						Msg("Failed to persist service registration")
				} else {
					log.Debug().Str("service", s.Name).Msg("Successfully upserted service")
				}
			}
		}(agentID, name, servicesCopy, now)
	} else {
		log.Warn().Msg("Registry has no DB connection, skipping persistence")
	}

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

	// Update persistence.
	if r.db != nil {
		go func(agentID string, lastSeen time.Time) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if err := r.db.UpdateServiceLastSeen(ctx, agentID, lastSeen); err != nil {
				log.Debug().Err(err). // Debug level as this happens frequently
							Str("agent_id", agentID).
							Msg("Failed to update service heartbeat persistence")
			}
		}(agentID, entry.LastSeen)
	}

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

// CountByStatus returns the number of active (healthy) and degraded agents.
func (r *Registry) CountByStatus() (active, degraded int32) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	now := time.Now()
	for _, entry := range r.entries {
		status := DetermineStatus(entry.LastSeen, now)
		switch status {
		case StatusHealthy:
			active++
		case StatusDegraded:
			degraded++
		}
	}
	return active, degraded
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
