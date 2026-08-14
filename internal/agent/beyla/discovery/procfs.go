package discovery

import (
	"bufio"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/rs/zerolog"
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
	// procRoot is the root of the /proc filesystem. Defaults to "/proc" via
	// root(); overridable in tests.
	procRoot string

	logger zerolog.Logger
}

// NewProcFSProvider creates a ProcFSProvider rooted at /proc.
func NewProcFSProvider(logger zerolog.Logger) *ProcFSProvider {
	return &ProcFSProvider{
		procRoot: "/proc",
		logger:   logger.With().Str("component", "procfs_provider").Logger(),
	}
}

// Name returns the provider's identifier.
func (p *ProcFSProvider) Name() string { return "procfs" }

// Probe always returns true. /proc access degrades gracefully to an empty
// result set when unavailable, and Coral's agent only runs on Linux.
func (p *ProcFSProvider) Probe() bool { return true }

func (p *ProcFSProvider) root() string {
	if p.procRoot != "" {
		return p.procRoot
	}
	return "/proc"
}

// Discover scans every visible network namespace's socket table for
// listening ports and returns the processes that own them, regardless of
// whether they run in the host namespace or a container's. Client-only
// workloads can still be instrumented explicitly through static discovery
// or a configured Beyla service.
func (p *ProcFSProvider) Discover(_ context.Context) ([]ProcessCandidate, error) {
	root := p.root()

	pids, err := listPids(root)
	if err != nil {
		return nil, fmt.Errorf("failed to list processes: %w", err)
	}

	nsGroups, pidNamespace, permissionDenied := groupPIDsByNamespace(root, pids)
	if permissionDenied > 0 {
		// Aggregated into one warning per cycle rather than one per PID, to
		// avoid a log storm on a host running many containers the agent
		// cannot inspect (RFD 112: report this as a capability warning
		// rather than silently under-reporting container discovery).
		p.logger.Warn().
			Int("processes", permissionDenied).
			Msg("Insufficient permission to read network namespace for some processes; container discovery may be incomplete")
	}
	portsByPID := p.listeningPortsByPID(root, nsGroups, pidNamespace)

	candidates := make([]ProcessCandidate, 0, len(pids))
	for _, pid := range pids {
		name, ok := readComm(root, pid)
		if !ok {
			// Process exited between listing PIDs and reading its comm
			// file; skip it rather than reporting a nameless candidate.
			continue
		}

		ports := portsByPID[pid]
		if len(ports) == 0 {
			continue
		}
		candidates = append(candidates, ProcessCandidate{
			PID:   pid,
			Ports: ports,
			Name:  name,
		})
	}

	return candidates, nil
}

// listPids returns every numeric directory entry under root, sorted
// ascending.
func listPids(root string) ([]int, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	pids := make([]int, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	return pids, nil
}

// readComm reads the process name from <root>/<pid>/comm. The second return
// value is false if the process has exited (or the file is otherwise
// unreadable), so the caller can skip it gracefully.
func readComm(root string, pid int) (string, bool) {
	//nolint:gosec // G304: path is built from a fixed /proc root and a numeric PID, not user input.
	data, err := os.ReadFile(filepath.Join(root, strconv.Itoa(pid), "comm"))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(data)), true
}

// netNamespaceID identifies a Linux network namespace by the stable string
// exposed through the /proc/<pid>/ns/net symlink target (e.g.
// "net:[4026531993]"). Socket inode numbers are only unique within a
// namespace, so any join across processes must key on (netNamespaceID,
// inode), never on inode alone.
type netNamespaceID string

// readNetNamespace resolves the network namespace identity of pid via its
// /proc/<pid>/ns/net symlink. The returned error is nil unless the symlink
// cannot be read, e.g. the process exited or the caller lacks permission.
func readNetNamespace(root string, pid int) (netNamespaceID, error) {
	link, err := os.Readlink(filepath.Join(root, strconv.Itoa(pid), "ns", "net"))
	if err != nil {
		return "", err
	}
	return netNamespaceID(link), nil
}

// groupPIDsByNamespace resolves each PID's network namespace and groups PIDs
// sharing one namespace together. PIDs whose namespace cannot be read are
// omitted from both return values; they cannot be attributed to a listening
// socket this cycle. The third return value counts PIDs skipped specifically
// due to a permission error, distinct from processes that simply exited, so
// the caller can surface a capability warning instead of silently
// under-reporting container discovery.
func groupPIDsByNamespace(root string, pids []int) (map[netNamespaceID][]int, map[int]netNamespaceID, int) {
	groups := make(map[netNamespaceID][]int)
	pidNamespace := make(map[int]netNamespaceID, len(pids))
	permissionDenied := 0

	for _, pid := range pids {
		ns, err := readNetNamespace(root, pid)
		if err != nil {
			if os.IsPermission(err) {
				permissionDenied++
			}
			continue
		}
		groups[ns] = append(groups[ns], pid)
		pidNamespace[pid] = ns
	}

	return groups, pidNamespace, permissionDenied
}

// socketKey identifies a listening socket by the composite of its owning
// network namespace and inode number, since inodes are only meaningful
// within one namespace.
type socketKey struct {
	ns    netNamespaceID
	inode string
}

// listeningPortsByPID returns, for each PID with at least one listening
// socket, the set of local ports it is bound to. It scans one socket table
// per network namespace, then cross-references each process's open file
// descriptors (fd -> socket inode) against the namespace-scoped inode->port
// map for the namespace that process belongs to.
func (p *ProcFSProvider) listeningPortsByPID(root string, nsGroups map[netNamespaceID][]int, pidNamespace map[int]netNamespaceID) map[int][]int {
	socketPorts := p.scanNamespaces(root, nsGroups)

	result := make(map[int][]int)
	if len(socketPorts) == 0 {
		return result
	}

	pids := make([]int, 0, len(pidNamespace))
	for pid := range pidNamespace {
		pids = append(pids, pid)
	}
	sort.Ints(pids)

	for _, pid := range pids {
		ns := pidNamespace[pid]
		fdDir := filepath.Join(root, strconv.Itoa(pid), "fd")
		entries, err := os.ReadDir(fdDir)
		if err != nil {
			// Process exited or fd dir unreadable (permission denied);
			// skip gracefully rather than failing the whole scan.
			continue
		}

		for _, entry := range entries {
			link, err := os.Readlink(filepath.Join(fdDir, entry.Name()))
			if err != nil {
				continue
			}
			inode, ok := parseSocketInode(link)
			if !ok {
				continue
			}
			if port, ok := socketPorts[socketKey{ns: ns, inode: inode}]; ok {
				result[pid] = appendUniquePort(result[pid], port)
			}
		}
	}

	return result
}

// scanNamespaces reads one listening-socket table per network namespace in
// nsGroups and returns their union, keyed by (namespace, inode). Namespaces
// whose socket table cannot be read from any candidate PID are omitted; the
// caller degrades to reporting no listeners for that namespace this cycle
// rather than failing discovery for the other namespaces.
func (p *ProcFSProvider) scanNamespaces(root string, nsGroups map[netNamespaceID][]int) map[socketKey]int {
	result := make(map[socketKey]int)

	nsIDs := make([]netNamespaceID, 0, len(nsGroups))
	for ns := range nsGroups {
		nsIDs = append(nsIDs, ns)
	}
	slices.Sort(nsIDs)

	for _, ns := range nsIDs {
		pids := append([]int{}, nsGroups[ns]...)
		sort.Ints(pids)

		table, ok := p.scanNamespaceSocketTable(root, ns, pids)
		if !ok {
			continue
		}
		for inode, port := range table {
			result[socketKey{ns: ns, inode: inode}] = port
		}
	}

	return result
}

// scanNamespaceSocketTable reads the listening-socket table for a network
// namespace by trying each of its member PIDs, in ascending order, as a
// representative. It retries the next PID whenever the current one has
// exited or its socket table cannot be read, so one racing or unreadable
// process does not hide an entire namespace's listeners. It returns false
// only when no PID in the namespace could be read this cycle.
func (p *ProcFSProvider) scanNamespaceSocketTable(root string, ns netNamespaceID, pids []int) (map[string]int, bool) {
	var lastErr error

	for _, pid := range pids {
		pidRoot := filepath.Join(root, strconv.Itoa(pid))
		if _, err := os.Stat(pidRoot); err != nil {
			lastErr = err
			continue
		}

		table, err := scanListeningSockets(pidRoot)
		if err != nil {
			lastErr = err
			continue
		}
		return table, true
	}

	p.logger.Debug().
		Str("namespace", string(ns)).
		AnErr("reason", lastErr).
		Msg("Unable to read socket table for any process in network namespace; skipping namespace for this discovery cycle")
	return nil, false
}

// parseSocketInode extracts the inode from an fd symlink target of the form
// "socket:[12345]".
func parseSocketInode(link string) (string, bool) {
	if !strings.HasPrefix(link, "socket:[") || !strings.HasSuffix(link, "]") {
		return "", false
	}
	return strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]"), true
}

// scanListeningSockets parses <pidRoot>/net/tcp and <pidRoot>/net/tcp6 for
// entries in the LISTEN state, returning a map from socket inode to local
// port. pidRoot is a process-specific procfs directory (e.g.
// "<root>/<pid>"), so the returned table reflects that process's network
// namespace.
func scanListeningSockets(pidRoot string) (map[string]int, error) {
	result := make(map[string]int)

	for _, name := range []string{"net/tcp", "net/tcp6"} {
		entries, err := parseSocketTable(filepath.Join(pidRoot, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		maps.Copy(result, entries)
	}

	return result, nil
}

// parseSocketTable parses a /proc/net/tcp[6]-formatted file, returning a map
// from socket inode to local port for every entry in the LISTEN (0A) state.
// Local addresses are formatted "<hex IP>:<hex port>" for both IPv4 and
// IPv6, so the same hex-port parsing applies to both tables.
func parseSocketTable(path string) (map[string]int, error) {
	//nolint:gosec // G304: path is a fixed /proc location, not user input.
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	result := make(map[string]int)
	scanner := bufio.NewScanner(f)
	scanner.Scan() // Skip header line.

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			continue
		}

		// Field 3 (st): 0A is LISTEN.
		if fields[3] != "0A" {
			continue
		}

		// Field 1 (local_address): "<hex IP>:<hex port>".
		parts := strings.Split(fields[1], ":")
		if len(parts) != 2 {
			continue
		}
		port, err := strconv.ParseInt(parts[1], 16, 32)
		if err != nil {
			continue
		}

		// Field 9: inode.
		result[fields[9]] = int(port)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

// appendUniquePort appends port to ports if not already present.
func appendUniquePort(ports []int, port int) []int {
	if slices.Contains(ports, port) {
		return ports
	}
	return append(ports, port)
}
