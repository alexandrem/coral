package registry

import (
	"fmt"
	"sync"
	"time"

	discoveryv1 "github.com/coral-io/coral/coral/discovery/v1"
)

// Entry represents a registered colony in the discovery service.
type Entry struct {
	MeshID           string
	PubKey           string
	Endpoints        []string
	MeshIPv4         string
	MeshIPv6         string
	ConnectPort      uint32
	Metadata         map[string]string
	LastSeen         time.Time
	ExpiresAt        time.Time
	ObservedEndpoint *discoveryv1.Endpoint // NAT traversal: observed public endpoint
	NatHint          discoveryv1.NatHint   // NAT traversal: detected NAT type
}

// RelaySession represents an active relay allocation.
type RelaySession struct {
	SessionID     string
	MeshID        string
	AgentPubKey   string
	ColonyPubKey  string
	RelayEndpoint *discoveryv1.Endpoint
	RelayID       string
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

// Registry is an in-memory store for colony registrations and relay sessions.
type Registry struct {
	mu            sync.RWMutex
	entries       map[string]*Entry
	relaySessions map[string]*RelaySession // keyed by session ID
	ttl           time.Duration
	relayTTL      time.Duration
}

// New creates a new Registry with the specified TTL.
func New(ttl time.Duration) *Registry {
	return &Registry{
		entries:       make(map[string]*Entry),
		relaySessions: make(map[string]*RelaySession),
		ttl:           ttl,
		relayTTL:      30 * time.Minute, // Default relay session TTL
	}
}

// Register adds or updates a colony registration.
func (r *Registry) Register(
	meshID, pubkey string,
	endpoints []string,
	meshIPv4, meshIPv6 string,
	connectPort uint32,
	metadata map[string]string,
	observedEndpoint *discoveryv1.Endpoint,
	natHint discoveryv1.NatHint,
) (*Entry, error) {
	if meshID == "" {
		return nil, fmt.Errorf("mesh_id cannot be empty")
	}
	if pubkey == "" {
		return nil, fmt.Errorf("pubkey cannot be empty")
	}
	// Require at least one endpoint OR an observed endpoint (for agents behind NAT).
	// Colonies should have static endpoints, but agents may only have observed endpoints from STUN.
	if len(endpoints) == 0 && observedEndpoint == nil {
		return nil, fmt.Errorf("at least one endpoint or observed endpoint is required")
	}

	// If no static endpoints, validate that observed endpoint has a port.
	// HTTP-extracted endpoints (from X-Forwarded-For) don't have port info.
	// Only STUN-discovered endpoints have valid port information.
	if len(endpoints) == 0 && observedEndpoint != nil && observedEndpoint.Port == 0 {
		return nil, fmt.Errorf("observed endpoint must have a valid port when no static endpoints are provided (use STUN discovery, not HTTP headers)")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()

	// Check for existing active registration (split-brain detection).
	// Allow updates to expired registrations or same pubkey (renewal/update).
	if existing, ok := r.entries[meshID]; ok {
		if now.Before(existing.ExpiresAt) {
			// Active registration exists. Allow update if same pubkey (renewal).
			if existing.PubKey != pubkey {
				r.mu.Unlock()
				return nil, fmt.Errorf(
					"colony/agent '%s' already registered with different public key until %v (existing: %s, new: %s). "+
						"This may indicate a split-brain scenario. Wait for lease expiration or use a different ID",
					meshID,
					existing.ExpiresAt,
					existing.PubKey,
					pubkey,
				)
			}
			// Same pubkey - this is a renewal/update, allow it to proceed.
		}
	}

	entry := &Entry{
		MeshID:           meshID,
		PubKey:           pubkey,
		Endpoints:        endpoints,
		MeshIPv4:         meshIPv4,
		MeshIPv6:         meshIPv6,
		ConnectPort:      connectPort,
		Metadata:         metadata,
		LastSeen:         now,
		ExpiresAt:        now.Add(r.ttl),
		ObservedEndpoint: observedEndpoint,
		NatHint:          natHint,
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

// StartCleanup runs periodic cleanup in the background.
func (r *Registry) StartCleanup(interval time.Duration, stopCh <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			removed := r.Cleanup()
			relayRemoved := r.CleanupRelaySessions()
			if removed > 0 || relayRemoved > 0 {
				// Could add logging here
				_ = removed
				_ = relayRemoved
			}
		case <-stopCh:
			return
		}
	}
}

// AllocateRelay creates a new relay session.
func (r *Registry) AllocateRelay(sessionID, meshID, agentPubKey, colonyPubKey string, relayEndpoint *discoveryv1.Endpoint, relayID string) (*RelaySession, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session_id cannot be empty")
	}
	if meshID == "" {
		return nil, fmt.Errorf("mesh_id cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	session := &RelaySession{
		SessionID:     sessionID,
		MeshID:        meshID,
		AgentPubKey:   agentPubKey,
		ColonyPubKey:  colonyPubKey,
		RelayEndpoint: relayEndpoint,
		RelayID:       relayID,
		CreatedAt:     now,
		ExpiresAt:     now.Add(r.relayTTL),
	}

	r.relaySessions[sessionID] = session
	return session, nil
}

// LookupRelaySession retrieves a relay session by session ID.
func (r *Registry) LookupRelaySession(sessionID string) (*RelaySession, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session_id cannot be empty")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	session, ok := r.relaySessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("relay session not found: %s", sessionID)
	}

	// Check if session has expired
	if time.Now().After(session.ExpiresAt) {
		return nil, fmt.Errorf("relay session expired: %s", sessionID)
	}

	return session, nil
}

// ReleaseRelay removes a relay session.
func (r *Registry) ReleaseRelay(sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("session_id cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.relaySessions[sessionID]; !ok {
		return fmt.Errorf("relay session not found: %s", sessionID)
	}

	delete(r.relaySessions, sessionID)
	return nil
}

// CleanupRelaySessions removes expired relay sessions.
func (r *Registry) CleanupRelaySessions() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	removed := 0
	for sessionID, session := range r.relaySessions {
		if now.After(session.ExpiresAt) {
			delete(r.relaySessions, sessionID)
			removed++
		}
	}
	return removed
}
