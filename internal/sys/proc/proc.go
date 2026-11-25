// Package proc reads process, socket, and kernel information from the Linux
// /proc filesystem.
package proc

import (
	"bufio"
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// FS is a procfs mount, identified by its root directory. The zero value
// reads the host's /proc; tests point an FS at a fake tree instead.
type FS string

// root returns the directory fs reads from.
func (fs FS) root() string {
	if fs == "" {
		return "/proc"
	}
	return string(fs)
}

// path joins elem onto the directory of pid.
func (fs FS) path(pid int, elem ...string) string {
	return filepath.Join(append([]string{fs.root(), strconv.Itoa(pid)}, elem...)...)
}

// FindPidByExePattern finds the PID of the first running process whose name
// matches the given regex pattern (RFD 111). Each candidate PID (in
// ascending order, from ListPids) is matched against /proc/<pid>/comm
// first; if that doesn't match, it falls back to /proc/<pid>/cmdline. This
// lets a pattern target either the bare executable name (e.g. "python") or
// its full invocation (e.g. "python.*consumer.py"). Returns 0 with no error
// if no process matches.
func FindPidByExePattern(pattern string) (int32, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return 0, fmt.Errorf("invalid exe pattern %q: %w", pattern, err)
	}

	var fs FS
	pids, err := fs.ListPids()
	if err != nil {
		return 0, err
	}

	for _, pid := range pids {
		if comm, ok := fs.Comm(pid); ok && re.MatchString(comm) {
			//nolint:gosec // G109: PID conversion is safe, sourced from ListPids.
			return int32(pid), nil
		}
		if cmdline, ok := fs.cmdline(pid); ok && re.MatchString(cmdline) {
			//nolint:gosec // G109: PID conversion is safe, sourced from ListPids.
			return int32(pid), nil
		}
	}

	return 0, nil // Not found.
}

// Comm reads the process name from /proc/<pid>/comm. The second return value
// is false if the process has exited or the file is unreadable.
func (fs FS) Comm(pid int) (string, bool) {
	//nolint:gosec // G304: path is built from a procfs root and a numeric PID, not user input.
	data, err := os.ReadFile(fs.path(pid, "comm"))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(data)), true
}

// cmdline reads the full command line from /proc/<pid>/cmdline, joining the
// NUL-separated arguments with spaces. The second return value is false if
// the process has exited or the file is unreadable.
func (fs FS) cmdline(pid int) (string, bool) {
	//nolint:gosec // G304: path is built from a procfs root and a numeric PID, not user input.
	data, err := os.ReadFile(fs.path(pid, "cmdline"))
	if err != nil {
		return "", false
	}
	args := strings.Split(strings.Trim(string(data), "\x00"), "\x00")
	return strings.Join(args, " "), true
}

// GetKernelVersion reads the kernel version from /proc/version.
func GetKernelVersion() string {
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return "unknown"
	}

	// Parse version from output like "Linux version 5.15.0-xxx...".
	version := string(data)
	if idx := strings.Index(version, "Linux version "); idx >= 0 {
		version = version[idx+14:] // Skip "Linux version ".
		if idx := strings.Index(version, " "); idx >= 0 {
			version = version[:idx]
		}
		return version
	}

	return "unknown"
}

// GetBinaryPath returns the path to the executable for the given PID.
// When the resolved path doesn't exist locally (e.g., the target runs in a
// different mount namespace), the function falls back to accessing the binary
// through /proc/PID/root which crosses namespace boundaries.
func GetBinaryPath(pid int) (string, error) {
	exeLink := fmt.Sprintf("/proc/%d/exe", pid)
	target, err := os.Readlink(exeLink)
	if err != nil {
		return "", fmt.Errorf("failed to readlink %s: %w", exeLink, err)
	}

	// If the resolved path exists locally, use it directly.
	if _, err := os.Stat(target); err == nil {
		return target, nil
	}

	// Fall back to /proc/PID/root/<path> to access the binary across
	// mount namespace boundaries (e.g., agent profiling a container).
	namespacedPath := fmt.Sprintf("/proc/%d/root%s", pid, target)
	if _, err := os.Stat(namespacedPath); err == nil {
		return namespacedPath, nil
	}

	// Last resort: return /proc/PID/exe itself which is always a valid
	// file descriptor to the binary.
	return exeLink, nil
}

// InNamespacePath strips the /proc/PID/root prefix from a path returned by
// GetBinaryPath, recovering the original in-namespace path as seen by the
// target process. This is useful when matching against /proc/PID/maps entries
// which use in-namespace paths.
func InNamespacePath(pid int, path string) string {
	prefix := fmt.Sprintf("/proc/%d/root", pid)
	if strings.HasPrefix(path, prefix) {
		return strings.TrimPrefix(path, prefix)
	}
	return path
}

// ListPids returns the ID of every running process, sorted in ascending
// order.
func (fs FS) ListPids() ([]int, error) {
	entries, err := os.ReadDir(fs.root())
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", fs.root(), err)
	}

	var pids []int
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Parse PID from directory name.
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue // Not a numeric directory.
		}

		if pid > 0 {
			pids = append(pids, pid)
		}
	}
	// Sort PIDs (lowest first).
	sort.Ints(pids)

	return pids, nil
}

// Environ reads the initial environment of pid from /proc/<pid>/environ,
// as NUL-separated KEY=value entries. Search it with LookupEnv.
func (fs FS) Environ(pid int) ([]byte, error) {
	//nolint:gosec // G304: path is built from a procfs root and a numeric PID, not user input.
	return os.ReadFile(fs.path(pid, "environ"))
}

// LookupEnv returns the value of key in environ, as read by Environ. When
// key appears more than once, the last occurrence wins. It scans environ in
// place rather than building a map, since callers may run it for every
// process on the host. Entries without '=' are skipped.
func LookupEnv(environ []byte, key string) (string, bool) {
	var value []byte
	found := false
	for entry := range bytes.SplitSeq(environ, []byte{0}) {
		if len(entry) > len(key) && entry[len(key)] == '=' && string(entry[:len(key)]) == key {
			value, found = entry[len(key)+1:], true
		}
	}
	return string(value), found
}

// NetNamespace identifies a Linux network namespace by the target of a
// /proc/<pid>/ns/net symlink (e.g. "net:[4026531993]"). Socket inode numbers
// are only unique within a namespace, so any join across processes must key
// on the namespace as well as the inode.
type NetNamespace string

// NetNamespace resolves the network namespace of pid. The error is non-nil
// if the symlink cannot be read, e.g. the process exited or the caller lacks
// permission.
func (fs FS) NetNamespace(pid int) (NetNamespace, error) {
	link, err := os.Readlink(fs.path(pid, "ns", "net"))
	if err != nil {
		return "", err
	}
	return NetNamespace(link), nil
}

// SelfNetNamespace resolves the network namespace of the calling process.
func (fs FS) SelfNetNamespace() (NetNamespace, error) {
	link, err := os.Readlink(filepath.Join(fs.root(), "self", "ns", "net"))
	if err != nil {
		return "", err
	}
	return NetNamespace(link), nil
}

// SocketTable maps the inode of each listening TCP socket in one network
// namespace to its local port.
type SocketTable map[string]int

// ListeningSockets parses /proc/<pid>/net/tcp and /proc/<pid>/net/tcp6,
// which describe the sockets of pid's network namespace, and returns the
// entries in the LISTEN state. A missing table, e.g. tcp6 on a host without
// IPv6, contributes no entries.
func (fs FS) ListeningSockets(pid int) (SocketTable, error) {
	result := make(SocketTable)
	for _, name := range []string{"tcp", "tcp6"} {
		entries, err := parseSocketTable(fs.path(pid, "net", name))
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

// parseSocketTable parses a /proc/net/tcp[6]-formatted file, returning every
// entry in the LISTEN (0A) state. Local addresses are formatted
// "<hex IP>:<hex port>" for both IPv4 and IPv6, so the same hex-port parsing
// applies to both tables.
func parseSocketTable(path string) (SocketTable, error) {
	//nolint:gosec // G304: path is a procfs location, not user input.
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	result := make(SocketTable)
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
		_, hexPort, ok := strings.Cut(fields[1], ":")
		if !ok {
			continue
		}
		port, err := strconv.ParseInt(hexPort, 16, 32)
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

// HeldPorts returns the ports of the sockets in table that pid holds a file
// descriptor for. The second return value is false if its fd directory could
// not be read, e.g. the process exited or the caller lacks permission.
func (fs FS) HeldPorts(pid int, table SocketTable) ([]int, bool) {
	fdDir := fs.path(pid, "fd")
	entries, err := os.ReadDir(fdDir)
	if err != nil {
		return nil, false
	}

	var ports []int
	for _, entry := range entries {
		link, err := os.Readlink(filepath.Join(fdDir, entry.Name()))
		if err != nil {
			continue
		}
		inode, ok := strings.CutPrefix(link, "socket:[")
		if !ok {
			continue
		}
		inode, ok = strings.CutSuffix(inode, "]")
		if !ok {
			continue
		}
		if port, ok := table[inode]; ok && !slices.Contains(ports, port) {
			ports = append(ports, port)
		}
	}
	return ports, true
}

// Listeners is the result of a listener scan.
type Listeners struct {
	// Ports lists, for each PID holding at least one listening socket, the
	// local ports of those sockets.
	Ports map[int][]int

	// Namespaces maps every PID whose network namespace could be read to
	// that namespace.
	Namespaces map[int]NetNamespace

	// PermissionDenied counts PIDs whose network namespace could not be read
	// for lack of permission, as opposed to having exited. Their listeners
	// are missing from Ports.
	PermissionDenied int

	// Unreadable maps each namespace whose socket table could not be read
	// through any of its processes to the last error seen. Its listeners are
	// missing from Ports.
	Unreadable map[NetNamespace]error
}

// ScanListeners finds the processes holding listening TCP sockets in every
// network namespace visible through fs, so containerized listeners are
// found alongside host ones. It groups processes by namespace, reads one
// socket table per namespace, and walks the file descriptors of every
// process in a namespace that has at least one listener. ListenerScanner
// avoids repeating that walk for idle processes.
func (fs FS) ScanListeners() (Listeners, error) {
	return fs.scanListeners(nil)
}

// listenerFullScanEvery is how many ListenerScanner scans may skip idle
// processes before one walks every process's descriptors again. It bounds
// how long a listening socket can go unnoticed when it reaches a process
// considered idle without changing its namespace's socket table, e.g. via
// SCM_RIGHTS fd passing.
const listenerFullScanEvery = 10

// ListenerScanner runs repeated listener scans, remembering which processes
// held none of their namespace's listening sockets so a stable host does not
// walk every descriptor of every process on each scan. A process is skipped
// only while it stays idle in the same namespace and that namespace's socket
// table is unchanged: a new listener always has a new inode, so any bind or
// close forces a walk of the whole namespace. Processes that do hold a
// listener, and processes not seen on the previous scan, are always walked,
// so a skipped walk can never make a process appear to listen. Every
// listenerFullScanEvery scans, every process is walked again.
//
// A ListenerScanner is safe for concurrent use; scans are serialized.
type ListenerScanner struct {
	fs FS

	mu sync.Mutex
	// tables is the previous scan's socket table per network namespace.
	tables map[NetNamespace]SocketTable
	// idle maps each PID found holding no listening socket on the previous
	// scan to the namespace it was in.
	idle map[int]NetNamespace
	// scans counts the scans run so far.
	scans int
}

// NewListenerScanner creates a ListenerScanner reading fs.
func NewListenerScanner(fs FS) *ListenerScanner {
	return &ListenerScanner{fs: fs}
}

// Scan is ScanListeners, skipping the descriptor walk of processes that are
// still idle since the previous scan.
func (s *ListenerScanner) Scan() (Listeners, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fs.scanListeners(s)
}

// advance starts a scan against the namespaces' current socket tables. It
// returns the namespaces whose table is unchanged since the previous scan
// and the previous scan's idle PIDs. Every listenerFullScanEvery scans it
// reports no namespace as unchanged, forcing a full walk. A nil scanner
// returns nil maps, so every process is walked.
func (s *ListenerScanner) advance(tables map[NetNamespace]SocketTable) (map[NetNamespace]bool, map[int]NetNamespace) {
	if s == nil {
		return nil, nil
	}
	unchanged := make(map[NetNamespace]bool)
	if s.scans%listenerFullScanEvery != 0 {
		for ns, table := range tables {
			if prev, ok := s.tables[ns]; ok && maps.Equal(prev, table) {
				unchanged[ns] = true
			}
		}
	}
	s.scans++
	s.tables = tables
	return unchanged, s.idle
}

// setIdle records the PIDs found idle in the scan started by advance.
func (s *ListenerScanner) setIdle(idle map[int]NetNamespace) {
	if s != nil {
		s.idle = idle
	}
}

// scanListeners implements ScanListeners. A non-nil scanner skips the
// processes it reports as still idle.
func (fs FS) scanListeners(scanner *ListenerScanner) (Listeners, error) {
	pids, err := fs.ListPids()
	if err != nil {
		return Listeners{}, err
	}

	result := Listeners{
		Ports:      make(map[int][]int),
		Namespaces: make(map[int]NetNamespace, len(pids)),
	}
	groups := make(map[NetNamespace][]int)
	for _, pid := range pids {
		ns, err := fs.NetNamespace(pid)
		if err != nil {
			if os.IsPermission(err) {
				result.PermissionDenied++
			}
			continue
		}
		groups[ns] = append(groups[ns], pid)
		result.Namespaces[pid] = ns
	}

	tables := make(map[NetNamespace]SocketTable, len(groups))
	for ns, members := range groups {
		table, err := fs.namespaceSocketTable(members)
		if err != nil {
			if result.Unreadable == nil {
				result.Unreadable = make(map[NetNamespace]error)
			}
			result.Unreadable[ns] = err
			continue
		}
		tables[ns] = table
	}

	unchanged, prevIdle := scanner.advance(tables)
	idle := make(map[int]NetNamespace)
	for _, pid := range pids {
		ns, ok := result.Namespaces[pid]
		if !ok {
			continue
		}
		table := tables[ns]
		if len(table) == 0 {
			continue
		}
		if unchanged[ns] && prevIdle[pid] == ns {
			idle[pid] = ns
			continue
		}

		ports, ok := fs.HeldPorts(pid, table)
		if !ok {
			// Process exited or fd dir unreadable (permission denied);
			// skip it rather than failing the whole scan.
			continue
		}
		if len(ports) == 0 {
			idle[pid] = ns
			continue
		}
		result.Ports[pid] = ports
	}
	scanner.setIdle(idle)

	return result, nil
}

// namespaceSocketTable reads the socket table of a network namespace by
// trying each of its member PIDs, given in ascending order, as a
// representative. It moves on to the next PID whenever the current one has
// exited or its table cannot be read, so one racing or unreadable process
// does not hide an entire namespace's listeners. It returns the last error
// when no member could be read.
func (fs FS) namespaceSocketTable(pids []int) (SocketTable, error) {
	var lastErr error
	for _, pid := range pids {
		// ListeningSockets treats missing tables as empty, so check the
		// process still exists before trusting an empty result.
		if _, err := os.Stat(fs.path(pid)); err != nil {
			lastErr = err
			continue
		}
		table, err := fs.ListeningSockets(pid)
		if err != nil {
			lastErr = err
			continue
		}
		return table, nil
	}
	return nil, lastErr
}

// ListThreads returns the thread IDs (TIDs) for all threads in the given process.
// It reads /proc/<pid>/task/ to enumerate threads.
func ListThreads(pid int) ([]int, error) {
	taskDir := fmt.Sprintf("/proc/%d/task", pid)
	entries, err := os.ReadDir(taskDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", taskDir, err)
	}

	tids := make([]int, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		tid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		if tid > 0 {
			tids = append(tids, tid)
		}
	}
	sort.Ints(tids)
	return tids, nil
}

// KernelSymbol represents a kernel symbol from /proc/kallsyms.
type KernelSymbol struct {
	Address uint64
	Type    byte
	Name    string
	Module  string // Empty for core kernel, module name for loadable modules
}

// ReadKallsyms reads and parses /proc/kallsyms.
// It returns a list of symbols and the count of zero addresses found (indicating permission issues).
func ReadKallsyms() ([]KernelSymbol, int, error) {
	file, err := os.Open("/proc/kallsyms")
	if err != nil {
		return nil, 0, fmt.Errorf("failed to open /proc/kallsyms: %w", err)
	}
	defer file.Close() // nolint:errcheck

	var symbols []KernelSymbol
	scanner := bufio.NewScanner(file)
	zeroAddresses := 0

	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}

		// Parse address
		var addr uint64
		if _, err := fmt.Sscanf(parts[0], "%x", &addr); err != nil {
			continue
		}

		// Check for zero addresses (means insufficient permissions)
		if addr == 0 {
			zeroAddresses++
			continue
		}

		// Parse symbol type and name
		symType := parts[1][0]
		symName := parts[2]

		// Parse optional module name [module_name]
		var module string
		if len(parts) > 3 && strings.HasPrefix(parts[3], "[") && strings.HasSuffix(parts[3], "]") {
			module = strings.Trim(parts[3], "[]")
		}

		symbols = append(symbols, KernelSymbol{
			Address: addr,
			Type:    symType,
			Name:    symName,
			Module:  module,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, zeroAddresses, fmt.Errorf("failed to read /proc/kallsyms: %w", err)
	}

	return symbols, zeroAddresses, nil
}
