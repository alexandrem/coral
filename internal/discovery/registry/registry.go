package registry

import (
	"fmt"
	"sync"
	"time"
)

// Entry represents a registered colony in the discovery service.
type Entry struct {
	MeshID      string
	PubKey      string
	Endpoints   []string
	MeshIPv4    string
	MeshIPv6    string
	ConnectPort uint32
	Metadata    map[string]string
	LastSeen    time.Time
	ExpiresAt   time.Time
}

// Registry is an in-memory store for colony registrations
type Registry struct {
	mu      sync.RWMutex
	entries map[string]*Entry
	ttl     time.Duration
}

// New creates a new Registry with the specified TTL
func New(ttl time.Duration) *Registry {
	return &Registry{
		entries: make(map[string]*Entry),
		ttl:     ttl,
	}
}

// Register adds or updates a colony registration.
func (r *Registry) Register(meshID, pubkey string, endpoints []string, meshIPv4, meshIPv6 string, connectPort uint32, metadata map[string]string) (*Entry, error) {
	if meshID == "" {
		return nil, fmt.Errorf("mesh_id cannot be empty")
	}
	if pubkey == "" {
		return nil, fmt.Errorf("pubkey cannot be empty")
	}
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("at least one endpoint is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	entry := &Entry{
		MeshID:      meshID,
		PubKey:      pubkey,
		Endpoints:   endpoints,
		MeshIPv4:    meshIPv4,
		MeshIPv6:    meshIPv6,
		ConnectPort: connectPort,
		Metadata:    metadata,
		LastSeen:    now,
		ExpiresAt:   now.Add(r.ttl),
	}

	r.entries[meshID] = entry
	return entry, nil
}

// Lookup retrieves a colony registration by mesh ID
func (r *Registry) Lookup(meshID string) (*Entry, error) {
	if meshID == "" {
		return nil, fmt.Errorf("mesh_id cannot be empty")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.entries[meshID]
	if !ok {
		return nil, fmt.Errorf("colony not found: %s", meshID)
	}

	// Check if entry has expired
	if time.Now().After(entry.ExpiresAt) {
		return nil, fmt.Errorf("colony registration expired: %s", meshID)
	}

	return entry, nil
}

// Count returns the number of registered colonies (including expired)
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}

// CountActive returns the number of active (non-expired) registrations
func (r *Registry) CountActive() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	now := time.Now()
	count := 0
	for _, entry := range r.entries {
		if now.Before(entry.ExpiresAt) {
			count++
		}
	}
	return count
}

// Cleanup removes expired entries from the registry
func (r *Registry) Cleanup() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	removed := 0
	for meshID, entry := range r.entries {
		if now.After(entry.ExpiresAt) {
			delete(r.entries, meshID)
			removed++
		}
	}
	return removed
}

// StartCleanup runs periodic cleanup in the background
func (r *Registry) StartCleanup(interval time.Duration, stopCh <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			removed := r.Cleanup()
			if removed > 0 {
				// Could add logging here
				_ = removed
			}
		case <-stopCh:
			return
		}
	}
}
