package discovery

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ProcFSProvider discovers processes via the Linux /proc filesystem. A
// socket-table scan (/proc/net/tcp[6]) finds listening servers and associates
// their ports with processes. Automatic discovery intentionally reports only
// those servers: treating every PID without a listening socket as a
// client-only workload generates instrumentation rules for kernel threads,
// system daemons, and Coral-managed processes such as Beyla itself.
type ProcFSProvider struct {
	// procRoot is the root of the /proc filesystem. Defaults to "/proc" via
	// root(); overridable in tests.
	procRoot string
}

// NewProcFSProvider creates a ProcFSProvider rooted at /proc.
func NewProcFSProvider() *ProcFSProvider {
	return &ProcFSProvider{procRoot: "/proc"}
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

// Discover scans the socket table for listening ports and returns the
// processes that own them. Client-only workloads can still be instrumented
// explicitly through static discovery or a configured Beyla service.
func (p *ProcFSProvider) Discover(_ context.Context) ([]ProcessCandidate, error) {
	root := p.root()

	pids, err := listPids(root)
	if err != nil {
		return nil, fmt.Errorf("failed to list processes: %w", err)
	}

	portsByPID, err := listeningPortsByPID(root, pids)
	if err != nil {
		return nil, fmt.Errorf("failed to scan socket table: %w", err)
	}

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

// listeningPortsByPID returns, for each PID with at least one listening
// socket, the set of local ports it is bound to. It cross-references the
// socket table (inode -> port) against each process's open file descriptors
// (fd -> socket inode).
func listeningPortsByPID(root string, pids []int) (map[int][]int, error) {
	inodeToPort, err := scanListeningSockets(root)
	if err != nil {
		return nil, err
	}
	result := make(map[int][]int)
	if len(inodeToPort) == 0 {
		return result, nil
	}

	for _, pid := range pids {
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
			if port, ok := inodeToPort[inode]; ok {
				result[pid] = appendUniquePort(result[pid], port)
			}
		}
	}

	return result, nil
}

// parseSocketInode extracts the inode from an fd symlink target of the form
// "socket:[12345]".
func parseSocketInode(link string) (string, bool) {
	if !strings.HasPrefix(link, "socket:[") || !strings.HasSuffix(link, "]") {
		return "", false
	}
	return strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]"), true
}

// scanListeningSockets parses /proc/net/tcp and /proc/net/tcp6 for entries
// in the LISTEN state, returning a map from socket inode to local port.
func scanListeningSockets(root string) (map[string]int, error) {
	result := make(map[string]int)

	for _, name := range []string{"net/tcp", "net/tcp6"} {
		entries, err := parseSocketTable(filepath.Join(root, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for inode, port := range entries {
			result[inode] = port
		}
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
	for _, p := range ports {
		if p == port {
			return ports
		}
	}
	return append(ports, port)
}
