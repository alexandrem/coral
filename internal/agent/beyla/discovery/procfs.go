package discovery

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/internal/sys/proc"
)

// ProcFSProvider discovers processes via the Linux /proc filesystem. A
// socket-table scan (/proc/<pid>/net/tcp[6]) finds listening servers and
// associates their ports with processes, scanning every network namespace
// visible to the agent so containerized listeners (e.g. Docker Compose
// applications) are discovered alongside host processes (RFD 112). Automatic
// discovery intentionally reports only those servers: treating every PID
// without a listening socket as a client-only workload generates
// instrumentation rules for kernel threads, system daemons, and
// Coral-managed processes such as Beyla itself.
type ProcFSProvider struct {
	fs proc.FS

	// listeners serves Discover, skipping the descriptor walk of processes
	// that stay idle across cycles.
	listeners *proc.ListenerScanner

	logger zerolog.Logger
}

// NewProcFSProvider creates a ProcFSProvider reading the host's /proc.
func NewProcFSProvider(logger zerolog.Logger) *ProcFSProvider {
	return newProcFSProvider("", logger)
}

// newProcFSProvider creates a ProcFSProvider reading fs.
func newProcFSProvider(fs proc.FS, logger zerolog.Logger) *ProcFSProvider {
	return &ProcFSProvider{
		fs:        fs,
		listeners: proc.NewListenerScanner(fs),
		logger:    logger.With().Str("component", "procfs_provider").Logger(),
	}
}

// Name returns the provider's identifier.
func (p *ProcFSProvider) Name() string { return "procfs" }

// Probe always returns true. /proc access degrades gracefully to an empty
// result set when unavailable, and Coral's agent only runs on Linux.
func (p *ProcFSProvider) Probe() bool { return true }

// Discover scans every visible network namespace's socket table for
// listening ports and returns the processes that own them, regardless of
// whether they run in the host namespace or a container's. Client-only
// workloads can still be instrumented explicitly through static discovery
// or a configured Beyla service.
func (p *ProcFSProvider) Discover(_ context.Context) ([]ProcessCandidate, error) {
	listeners, err := p.listeners.Scan()
	if err != nil {
		return nil, fmt.Errorf("failed to list processes: %w", err)
	}
	if listeners.PermissionDenied > 0 {
		// Aggregated into one warning per cycle rather than one per PID, to
		// avoid a log storm on a host running many containers the agent
		// cannot inspect (RFD 112: report this as a capability warning
		// rather than silently under-reporting container discovery).
		p.logger.Warn().
			Int("processes", listeners.PermissionDenied).
			Msg("Insufficient permission to read network namespace for some processes; container discovery may be incomplete")
	}
	p.logUnreadable(listeners)

	candidates := make([]ProcessCandidate, 0, len(listeners.Ports))
	for _, pid := range slices.Sorted(maps.Keys(listeners.Ports)) {
		name, ok := p.fs.Comm(pid)
		if !ok {
			// Process exited between the socket scan and reading its comm
			// file; skip it rather than reporting a nameless candidate.
			continue
		}
		candidates = append(candidates, ProcessCandidate{
			PID:   pid,
			Ports: listeners.Ports[pid],
			Name:  name,
		})
	}

	return candidates, nil
}

// logUnreadable reports the namespaces whose socket table could not be read
// this scan. Their listeners are missing until a later scan reads them.
func (p *ProcFSProvider) logUnreadable(listeners proc.Listeners) {
	for _, ns := range slices.Sorted(maps.Keys(listeners.Unreadable)) {
		p.logger.Debug().
			Str("namespace", string(ns)).
			AnErr("reason", listeners.Unreadable[ns]).
			Msg("Unable to read socket table for any process in network namespace; skipping namespace for this discovery cycle")
	}
}

// portForwarders lists process names that listen on a port only to relay
// connections to the real application, or to reserve it for one. Docker's
// userland proxy relays a published container port, and dockerd itself holds
// a never-accepted listener for each port a Swarm service publishes. They
// never own a service.
var portForwarders = map[string]bool{
	"docker-proxy": true,
	"dockerd":      true,
	"rootlessport": true,
	"rootlesskit":  true,
	"slirp4netns":  true,
	"pasta":        true,
}

// PortQuery describes the service expected on a port, so FindPortOwners can
// tell it apart from other processes listening on the same port in other
// network namespaces.
type PortQuery struct {
	// Name is the service's name. It breaks a tie between listeners through
	// their OTEL_SERVICE_NAME, or else SERVICE_NAME, environment variable.
	Name string

	// Env, when non-empty, restricts the port to listeners whose initial
	// environment has every variable with exactly this value.
	Env map[string]string

	// Accept, when set, further restricts the port to listeners for which it
	// returns true, such as those in a Docker container with given labels.
	Accept func(pid int) bool
}

// PortOwner is the process that owns a listening port, as resolved by
// FindPortOwners.
type PortOwner struct {
	// PID is the lowest PID listening on the port in the network namespace
	// that owns it. It is 0 when no listener matches the query, or when
	// several do and none of the tie-breaks singles one out.
	PID int

	// Namespaces counts the distinct network namespaces with a listener on
	// the port.
	Namespaces int

	// Matching counts the namespaces whose listener matches the query's Env
	// and Accept. It equals Namespaces when the query sets neither.
	Matching int
}

// FindPortOwners resolves each port in queries to the process listening on
// it, scanning every visible network namespace so containerized listeners
// are found alongside host processes. Port forwarders and the calling
// process are ignored. Within a namespace, the lowest PID wins, which is the
// parent when pre-fork workers share one socket.
//
// Listeners in other namespaces, such as containers that all use the same
// internal port, are narrowed down in order. Listeners not matching the
// query's Env and Accept are dropped. If several remain, the one in the
// caller's own network namespace wins, since that is the listener a
// connection to 127.0.0.1:<port> reaches. Failing that, the port resolves
// only if exactly one remaining listener's service-name variable equals the
// query's Name. Every requested port has an entry in the result.
func (p *ProcFSProvider) FindPortOwners(queries map[int]PortQuery) (map[int]PortOwner, error) {
	// Not p.listeners: connect-time resolution walks every descriptor so an
	// ownership change since the last discovery cycle is never missed.
	listeners, err := p.fs.ScanListeners()
	if err != nil {
		return nil, fmt.Errorf("failed to list processes: %w", err)
	}
	p.logUnreadable(listeners)

	wanted := func(port int) bool {
		_, ok := queries[port]
		return ok
	}

	// listeners[port][ns] is the lowest PID listening on port within ns.
	byPort := make(map[int]map[proc.NetNamespace]int)
	self := os.Getpid()
	for pid, pidPorts := range listeners.Ports {
		if pid == self || !slices.ContainsFunc(pidPorts, wanted) {
			continue
		}
		if name, ok := p.fs.Comm(pid); !ok || portForwarders[name] {
			continue
		}
		ns := listeners.Namespaces[pid]
		for _, port := range pidPorts {
			if !wanted(port) {
				continue
			}
			if byPort[port] == nil {
				byPort[port] = make(map[proc.NetNamespace]int)
			}
			if cur, ok := byPort[port][ns]; !ok || pid < cur {
				byPort[port][ns] = pid
			}
		}
	}

	// An unreadable namespace link only disables the own-namespace preference.
	selfNS, _ := p.fs.SelfNetNamespace()

	owners := make(map[int]PortOwner, len(queries))
	for port, query := range queries {
		byNS := byPort[port]
		matching := make(map[proc.NetNamespace]int, len(byNS))
		for ns, pid := range byNS {
			if matchesEnv(p.fs, pid, query.Env) && (query.Accept == nil || query.Accept(pid)) {
				matching[ns] = pid
			}
		}
		owners[port] = PortOwner{
			PID:        pickListener(p.fs, matching, selfNS, query.Name),
			Namespaces: len(byNS),
			Matching:   len(matching),
		}
	}
	return owners, nil
}

// matchesEnv reports whether pid's initial environment has every variable in
// want with exactly that value. An empty want matches any process.
func matchesEnv(fs proc.FS, pid int, want map[string]string) bool {
	if len(want) == 0 {
		return true
	}
	environ, err := fs.Environ(pid)
	if err != nil {
		return false
	}
	for key, value := range want {
		if got, ok := proc.LookupEnv(environ, key); !ok || got != value {
			return false
		}
	}
	return true
}

// pickListener chooses among one port's matching listeners, given as the
// lowest PID in each network namespace. A single namespace wins outright,
// then the listener in selfNS, then the one listener whose service-name
// environment variable equals name. It returns 0 when none of these applies.
func pickListener(fs proc.FS, byNS map[proc.NetNamespace]int, selfNS proc.NetNamespace, name string) int {
	if len(byNS) == 1 {
		for _, pid := range byNS {
			return pid
		}
	}
	if pid, ok := byNS[selfNS]; ok && selfNS != "" {
		return pid
	}
	if name == "" {
		return 0
	}

	match := 0
	for _, pid := range byNS {
		if envName, _ := readServiceNameEnv(fs, pid); envName != name {
			continue
		}
		if match != 0 {
			return 0
		}
		match = pid
	}
	return match
}

// ListensOn reports whether pid currently holds a listening socket on port
// in its own network namespace. It reads only that process's socket table
// and descriptors, so it is a cheap way to confirm that a PID returned by
// FindPortOwners still belongs to the same listener.
func (p *ProcFSProvider) ListensOn(pid, port int) bool {
	table, err := p.fs.ListeningSockets(pid)
	if err != nil || !slices.Contains(slices.Collect(maps.Values(table)), port) {
		return false
	}
	ports, _ := p.fs.HeldPorts(pid, table)
	return slices.Contains(ports, port)
}
