package discovery

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// defaultPollInterval is used when DiscoveryManager is created with a
// non-positive poll interval.
const defaultPollInterval = 30 * time.Second

// DiscoveryManager runs a priority-ordered set of ProcessDiscoveryProvider
// implementations on a poll interval, merges their results with any pinned
// static candidates, and invokes a callback when the merged set changes.
//
// Providers are consulted in registration order: the first provider to
// report a non-empty name for a given process wins ("first non-empty name
// wins, in priority order"). Static candidates registered via
// SetStaticCandidates (e.g. from `coral connect`) always outrank every
// provider and are re-applied on every poll, so they survive cycles in
// which no provider currently reports the same process.
type DiscoveryManager struct {
	logger zerolog.Logger

	mu               sync.Mutex
	providers        []ProcessDiscoveryProvider
	staticCandidates []ProcessCandidate
	current          map[string]ProcessCandidate // last merged set, by mergeKey

	onChange func([]ProcessCandidate)

	pollInterval time.Duration
	cancel       context.CancelFunc
}

// NewDiscoveryManager creates a DiscoveryManager. onChange is invoked with
// the full merged candidate list whenever a poll produces a set that differs
// from the previous one. A non-positive pollInterval falls back to
// defaultPollInterval.
func NewDiscoveryManager(logger zerolog.Logger, pollInterval time.Duration, onChange func([]ProcessCandidate)) *DiscoveryManager {
	if pollInterval <= 0 {
		pollInterval = defaultPollInterval
	}
	return &DiscoveryManager{
		logger:       logger.With().Str("component", "discovery_manager").Logger(),
		pollInterval: pollInterval,
		onChange:     onChange,
		current:      make(map[string]ProcessCandidate),
	}
}

// RegisterProvider adds a provider to the priority chain. Providers
// registered earlier take priority over providers registered later.
func (m *DiscoveryManager) RegisterProvider(p ProcessDiscoveryProvider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providers = append(m.providers, p)
}

// SetStaticCandidates replaces the pinned candidate set. Static candidates
// always outrank provider-discovered candidates for the same process and are
// re-applied on every poll cycle, regardless of what providers currently
// report.
func (m *DiscoveryManager) SetStaticCandidates(candidates []ProcessCandidate) {
	m.mu.Lock()
	defer m.mu.Unlock()

	stamped := make([]ProcessCandidate, len(candidates))
	for i, c := range candidates {
		c.Source = "static"
		stamped[i] = c
	}
	m.staticCandidates = stamped
}

// Run starts the poll loop: it polls immediately, then on every
// pollInterval tick, until ctx is done. Run blocks until ctx is done or
// Stop is called.
func (m *DiscoveryManager) Run(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)

	m.mu.Lock()
	m.cancel = cancel
	interval := m.pollInterval
	m.mu.Unlock()

	m.Poll(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.Poll(ctx)
		}
	}
}

// Stop cancels the poll loop started by Run.
func (m *DiscoveryManager) Stop() {
	m.mu.Lock()
	cancel := m.cancel
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

// Poll runs a single discovery round: gathers candidates from every active
// provider plus the pinned static set, merges them by priority, and invokes
// onChange if the merged set differs from the previous round.
func (m *DiscoveryManager) Poll(ctx context.Context) {
	m.mu.Lock()
	providers := append([]ProcessDiscoveryProvider{}, m.providers...)
	static := append([]ProcessCandidate{}, m.staticCandidates...)
	m.mu.Unlock()

	// priorityGroups[0] is the pinned static set (highest priority),
	// followed by one group per provider in registration order.
	priorityGroups := make([][]ProcessCandidate, 0, len(providers)+1)
	priorityGroups = append(priorityGroups, static)

	for _, p := range providers {
		if !p.Probe() {
			continue
		}
		candidates, err := p.Discover(ctx)
		if err != nil {
			m.logger.Warn().Err(err).Str("provider", p.Name()).Msg("Provider discovery failed")
			continue
		}
		for i := range candidates {
			candidates[i].Source = p.Name()
		}
		priorityGroups = append(priorityGroups, candidates)
	}

	merged := merge(priorityGroups)

	m.mu.Lock()
	changed := !equalCandidateSets(m.current, merged)
	if changed {
		m.current = merged
	}
	m.mu.Unlock()

	if changed && m.onChange != nil {
		m.onChange(candidateValues(merged))
	}
}

// Current returns the most recently merged candidate set.
func (m *DiscoveryManager) Current() []ProcessCandidate {
	m.mu.Lock()
	defer m.mu.Unlock()
	return candidateValues(m.current)
}

// mergeKey returns the fallback join key for a candidate, used once neither
// the PID index nor the port index (see merge) resolves an existing group.
// Candidates with a known PID are joined by PID, so that, e.g., an
// EnvVarProvider name hint and a ProcFSProvider port list for the same
// running process collapse into one entry. Candidates without a known PID
// (typically static candidates registered before the underlying process has
// been observed) fall back to their declared name, then their first port —
// either keeps repeated registrations idempotent and distinct static
// candidates from colliding with each other, since a name or a port is
// stable across calls for the same `coral connect` registration.
func mergeKey(c ProcessCandidate) string {
	if c.PID != 0 {
		return fmt.Sprintf("pid:%d", c.PID)
	}
	if c.Name != "" {
		return fmt.Sprintf("static:name:%s", c.Name)
	}
	if len(c.Ports) > 0 {
		return fmt.Sprintf("static:port:%d", c.Ports[0])
	}
	return "static:unnamed"
}

// merge combines priority-ordered candidate groups (highest priority first)
// into a single map keyed by mergeKey.
//
// Static candidates (PID unknown, e.g. a `coral connect` registration made
// before the process was observed) are additionally joined onto whichever
// group already claims one of their ports, once a provider discovers the
// real process there. Static groups are always processed first (they
// occupy priorityGroups[0]), so their port claims are registered before any
// provider group is processed; a later provider candidate for that same
// port — even carrying its own PID — joins the existing static-rooted group
// instead of creating a second entry for the same port, which would
// otherwise produce two conflicting Beyla rules for one process. Ports from
// provider candidates are deliberately not indexed: the same numeric port
// can be owned by different processes in different network namespaces. A
// PID index tracks joins so any further candidate for the same PID (e.g. a
// second provider with no port data of its own) also lands in that group.
func merge(priorityGroups [][]ProcessCandidate) map[string]ProcessCandidate {
	grouped := make(map[string][]ProcessCandidate)
	staticPortToKey := make(map[int]string)
	pidToKey := make(map[int]string)
	var order []string

	for groupIndex, group := range priorityGroups {
		for _, c := range group {
			key := ""

			if c.PID != 0 {
				if k, ok := pidToKey[c.PID]; ok {
					key = k
				}
			}
			if key == "" {
				for _, port := range c.Ports {
					if k, ok := staticPortToKey[port]; ok {
						key = k
						break
					}
				}
			}
			if key == "" {
				key = mergeKey(c)
			}

			if _, seen := grouped[key]; !seen {
				order = append(order, key)
			}
			grouped[key] = append(grouped[key], c)

			if c.PID != 0 {
				if _, exists := pidToKey[c.PID]; !exists {
					pidToKey[c.PID] = key
				}
			}
			if groupIndex == 0 {
				for _, port := range c.Ports {
					if _, exists := staticPortToKey[port]; !exists {
						staticPortToKey[port] = key
					}
				}
			}
		}
	}

	result := make(map[string]ProcessCandidate, len(order))
	for _, key := range order {
		result[key] = mergeCandidates(grouped[key])
	}
	return result
}

// mergeCandidates collapses priority-ordered candidates sharing one
// mergeKey into a single ProcessCandidate. Name and ExePathPattern take the
// first non-empty value in priority order; Ports takes the first non-empty
// list. IsClientOnly is derived from the merged Ports rather than copied, so
// a name-only candidate (e.g. from EnvVarProvider) never masks port data
// reported by a lower-priority candidate for the same process.
func mergeCandidates(candidates []ProcessCandidate) ProcessCandidate {
	var merged ProcessCandidate

	for _, c := range candidates {
		if merged.PID == 0 {
			merged.PID = c.PID
		}
		if merged.Name == "" {
			merged.Name = c.Name
		}
		if merged.ExePathPattern == "" {
			merged.ExePathPattern = c.ExePathPattern
		}
		if len(merged.Ports) == 0 && len(c.Ports) > 0 {
			merged.Ports = append([]int{}, c.Ports...)
		}
		if merged.Source == "" {
			merged.Source = c.Source
		}
		for k, v := range c.Labels {
			if merged.Labels == nil {
				merged.Labels = make(map[string]string)
			}
			if _, exists := merged.Labels[k]; !exists {
				merged.Labels[k] = v
			}
		}
	}

	merged.IsClientOnly = len(merged.Ports) == 0
	return merged
}

func candidateValues(m map[string]ProcessCandidate) []ProcessCandidate {
	result := make([]ProcessCandidate, 0, len(m))
	for _, c := range m {
		result = append(result, c)
	}
	return result
}

func equalCandidateSets(a, b map[string]ProcessCandidate) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok || !candidateEqual(av, bv) {
			return false
		}
	}
	return true
}

func candidateEqual(a, b ProcessCandidate) bool {
	if a.PID != b.PID || a.Name != b.Name || a.ExePathPattern != b.ExePathPattern || a.IsClientOnly != b.IsClientOnly {
		return false
	}
	if len(a.Ports) != len(b.Ports) {
		return false
	}
	for i := range a.Ports {
		if a.Ports[i] != b.Ports[i] {
			return false
		}
	}
	if len(a.Labels) != len(b.Labels) {
		return false
	}
	for k, v := range a.Labels {
		if b.Labels[k] != v {
			return false
		}
	}
	return true
}
