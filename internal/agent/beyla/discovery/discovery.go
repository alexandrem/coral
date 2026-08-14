// Package discovery implements pluggable process discovery for Beyla (RFD 102).
//
// A ProcessDiscoveryProvider inspects the host and reports ProcessCandidate
// entries eligible for Beyla instrumentation. DiscoveryManager runs a
// priority-ordered set of providers on a poll interval, merges their results,
// and notifies a callback when the merged set changes.
package discovery

import "context"

// ProcessCandidate represents a single process eligible for Beyla
// instrumentation, as reported by a ProcessDiscoveryProvider or pinned via
// DiscoveryManager.SetStaticCandidates.
type ProcessCandidate struct {
	// PID is the process ID, when known. Zero means the candidate is not
	// (yet) tied to a running process, e.g. a static candidate registered by
	// a `coral connect` call before the process has been observed.
	PID int

	// Ports lists the TCP ports this process listens on. Empty for
	// client-only processes.
	Ports []int

	// Name is the resolved service name hint for this candidate.
	Name string

	// Source identifies the ProcessDiscoveryProvider (or "static") that
	// produced this candidate.
	Source string

	// Labels carries provider-specific metadata (e.g. container labels).
	Labels map[string]string

	// IsClientOnly is true when the process makes outbound calls but does
	// not bind a listening socket.
	IsClientOnly bool

	// ExePathPattern is an explicit regex to match against the process's
	// executable path, used as the Beyla exe_path rule verbatim instead of
	// one derived from Name (RFD 111). Only meaningful when IsClientOnly is
	// true; empty means derive the rule from Name as before.
	ExePathPattern string
}

// ProcessDiscoveryProvider abstracts a source of process discovery
// information. Implementations should be safe for concurrent use.
type ProcessDiscoveryProvider interface {
	// Name returns a short, stable identifier for the provider (e.g.
	// "procfs", "envvar").
	Name() string

	// Probe reports whether this provider can run in the current
	// environment. DiscoveryManager skips providers whose Probe returns
	// false.
	Probe() bool

	// Discover returns the current set of process candidates.
	Discover(ctx context.Context) ([]ProcessCandidate, error)
}
