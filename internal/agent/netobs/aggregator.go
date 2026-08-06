// Package netobs provides L4 network connection observation for the Coral agent (RFD 033).
// It observes outbound TCP connections via netstat/ss polling (all platforms) or eBPF
// tcp_v4_connect hooks (Linux), aggregates per-edge metrics, and streams batches to
// the colony's ReportConnections RPC.
package netobs

import (
	"sync"
	"time"
)

// ConnectionEntry holds the accumulated state for a single directed L4 edge.
// Keyed by (destIP, destPort, protocol) within a single agent.
type ConnectionEntry struct {
	RemoteIP      string
	RemotePort    uint32
	Protocol      string
	BytesSent     uint64
	BytesReceived uint64
	Retransmits   uint32
	RTTUS         uint32 // 0 on netstat fallback path.
	FirstObserved time.Time
	LastObserved  time.Time
}

// edgeKey identifies a unique directed connection edge.
type edgeKey struct {
	remoteIP   string
	remotePort uint32
	protocol   string
}

// Aggregator deduplicates and accumulates L4 connection observations.
// Multiple observations of the same edge within a flush window are merged
// into a single ConnectionEntry with accumulated counters.
type Aggregator struct {
	mu      sync.Mutex
	entries map[edgeKey]*ConnectionEntry
}

// newAggregator creates an empty Aggregator.
func newAggregator() *Aggregator {
	return &Aggregator{
		entries: make(map[edgeKey]*ConnectionEntry),
	}
}

// Record adds or updates the entry for a connection edge.
// The provided entry's counters are accumulated into the existing entry when
// one already exists for the same (remoteIP, remotePort, protocol) triple.
func (a *Aggregator) Record(e ConnectionEntry) {
	k := edgeKey{remoteIP: e.RemoteIP, remotePort: e.RemotePort, protocol: e.Protocol}

	a.mu.Lock()
	defer a.mu.Unlock()

	existing, ok := a.entries[k]
	if !ok {
		copy := e
		a.entries[k] = &copy
		return
	}

	existing.BytesSent += e.BytesSent
	existing.BytesReceived += e.BytesReceived
	existing.Retransmits += e.Retransmits
	if e.RTTUS > 0 {
		existing.RTTUS = e.RTTUS
	}
	if e.LastObserved.After(existing.LastObserved) {
		existing.LastObserved = e.LastObserved
	}
}

// Flush atomically returns all accumulated entries and resets the aggregator.
func (a *Aggregator) Flush() []ConnectionEntry {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.entries) == 0 {
		return nil
	}

	out := make([]ConnectionEntry, 0, len(a.entries))
	for _, e := range a.entries {
		out = append(out, *e)
	}

	a.entries = make(map[edgeKey]*ConnectionEntry)
	return out
}
