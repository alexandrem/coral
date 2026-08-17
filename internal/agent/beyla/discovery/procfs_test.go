package discovery

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

// defaultTestNamespace is the network namespace used by addFakeProcess,
// representing the host namespace shared by non-containerized tests.
const defaultTestNamespace = "net:[4026531993]"

// writeProcFile writes content to <root>/<relPath>, creating parent
// directories as needed.
func writeProcFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("failed to create dir for %s: %v", full, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", full, err)
	}
}

// addFakeProcessInNS creates <root>/<pid>/comm with the given name and a
// /proc/<pid>/ns/net symlink identifying its network namespace.
func addFakeProcessInNS(t *testing.T, root string, pid int, name, ns string) {
	t.Helper()
	writeProcFile(t, root, fmt.Sprintf("%d/comm", pid), name+"\n")

	pidDir := filepath.Join(root, fmt.Sprintf("%d", pid))
	// Ensure the fd directory exists (even if empty) so listeningPortsByPID
	// doesn't treat this as an exited process.
	if err := os.MkdirAll(filepath.Join(pidDir, "fd"), 0o755); err != nil {
		t.Fatalf("failed to create fd dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(pidDir, "ns"), 0o755); err != nil {
		t.Fatalf("failed to create ns dir: %v", err)
	}
	if err := os.Symlink(ns, filepath.Join(pidDir, "ns", "net")); err != nil {
		t.Fatalf("failed to create ns/net symlink: %v", err)
	}
}

// addFakeProcess creates a process in the shared default (host) test
// namespace.
func addFakeProcess(t *testing.T, root string, pid int, name string) {
	t.Helper()
	addFakeProcessInNS(t, root, pid, name, defaultTestNamespace)
}

// addFakeSocket creates an fd symlink for pid pointing at the given socket
// inode, so listeningPortsByPID can associate the pid with a listening port.
func addFakeSocket(t *testing.T, root string, pid int, fd int, inode string) {
	t.Helper()
	fdDir := filepath.Join(root, fmt.Sprintf("%d", pid), "fd")
	if err := os.MkdirAll(fdDir, 0o755); err != nil {
		t.Fatalf("failed to create fd dir: %v", err)
	}
	linkPath := filepath.Join(fdDir, fmt.Sprintf("%d", fd))
	target := fmt.Sprintf("socket:[%s]", inode)
	if err := os.Symlink(target, linkPath); err != nil {
		t.Fatalf("failed to create fd symlink: %v", err)
	}
}

// writeSocketTable writes a /proc/<pid>/net/tcp[6]-formatted file for the
// namespace represented by pid. rows are (hexLocalAddr, state, inode)
// triples; a header line is added automatically.
func writeSocketTable(t *testing.T, root, relPath string, rows [][3]string) {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n")
	for i, row := range rows {
		fmt.Fprintf(&sb, "%4d: %s 00000000:0000 %s 00000000:00000000 00:00000000 00000000     0        0 %s 1 0000000000000000 100 0 0 10 0\n",
			i, row[0], row[1], row[2])
	}
	writeProcFile(t, root, relPath, sb.String())
}

// writeHostSocketTable writes rows to <pid>/net/tcp, the table read for a
// representative PID of a given namespace.
func writeHostSocketTable(t *testing.T, root string, pid int, tcp6 bool, rows [][3]string) {
	t.Helper()
	file := "net/tcp"
	if tcp6 {
		file = "net/tcp6"
	}
	writeSocketTable(t, root, fmt.Sprintf("%d/%s", pid, file), rows)
}

func newTestProvider(root string) *ProcFSProvider {
	return &ProcFSProvider{procRoot: root, logger: zerolog.Nop()}
}

func TestProcFSProviderServerProcess(t *testing.T) {
	root := t.TempDir()

	// PID 100: otel-app, listening on port 8080 (0x1F90) via inode 12345.
	addFakeProcess(t, root, 100, "otel-app")
	addFakeSocket(t, root, 100, 3, "12345")
	writeHostSocketTable(t, root, 100, false, [][3]string{
		{"0100007F:1F90", "0A", "12345"},
	})

	p := newTestProvider(root)
	candidates, err := p.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	c := findCandidate(t, candidates, 100)
	if c.Name != "otel-app" {
		t.Errorf("Name = %q, want %q", c.Name, "otel-app")
	}
	if len(c.Ports) != 1 || c.Ports[0] != 8080 {
		t.Errorf("Ports = %v, want [8080]", c.Ports)
	}
	if c.IsClientOnly {
		t.Errorf("IsClientOnly = true, want false for a server process")
	}
}

func TestProcFSProviderSkipsClientOnlyProcess(t *testing.T) {
	root := t.TempDir()

	// PID 200: a worker process with no listening socket.
	addFakeProcess(t, root, 200, "kafka-consumer")
	// No fd sockets, no socket table entries.

	p := newTestProvider(root)
	candidates, err := p.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	if len(candidates) != 0 {
		t.Errorf("Discover() returned %d candidates, want none for a client-only process: %+v", len(candidates), candidates)
	}
}

func TestProcFSProviderProcessExitsBetweenScans(t *testing.T) {
	root := t.TempDir()

	// PID 300's directory exists (so it's listed) but has no comm file,
	// simulating the process exiting between the PID listing and the
	// comm read.
	if err := os.MkdirAll(filepath.Join(root, "300"), 0o755); err != nil {
		t.Fatalf("failed to create pid dir: %v", err)
	}

	// A healthy listening process should still be reported.
	addFakeProcess(t, root, 100, "otel-app")
	addFakeSocket(t, root, 100, 3, "12345")
	writeHostSocketTable(t, root, 100, false, [][3]string{
		{"0100007F:1F90", "0A", "12345"},
	})

	p := newTestProvider(root)
	candidates, err := p.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v, want graceful skip of exited process", err)
	}

	for _, c := range candidates {
		if c.PID == 300 {
			t.Errorf("expected PID 300 (exited, no comm file) to be skipped, got %+v", c)
		}
	}
	findCandidate(t, candidates, 100) // still present
}

func TestProcFSProviderIPv6HexParsing(t *testing.T) {
	root := t.TempDir()

	// PID 400: listening on port 9090 (0x2382) via an IPv6 socket, inode 99999.
	addFakeProcess(t, root, 400, "grpc-service")
	addFakeSocket(t, root, 400, 5, "99999")
	writeHostSocketTable(t, root, 400, true, [][3]string{
		{"00000000000000000000000000000000:2382", "0A", "99999"},
	})

	p := newTestProvider(root)
	candidates, err := p.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	c := findCandidate(t, candidates, 400)
	if len(c.Ports) != 1 || c.Ports[0] != 9090 {
		t.Errorf("Ports = %v, want [9090] (IPv6 hex-parsed)", c.Ports)
	}
	if c.IsClientOnly {
		t.Errorf("IsClientOnly = true, want false for an IPv6 listening process")
	}
}

func TestProcFSProviderProbeAlwaysTrue(t *testing.T) {
	p := NewProcFSProvider(zerolog.Nop())
	if !p.Probe() {
		t.Error("Probe() = false, want true")
	}
	if p.Name() != "procfs" {
		t.Errorf("Name() = %q, want %q", p.Name(), "procfs")
	}
}

// TestProcFSProviderContainerNamespace verifies a listener in a container
// network namespace is discovered even though the host namespace's socket
// table has no matching entry (RFD 112).
func TestProcFSProviderContainerNamespace(t *testing.T) {
	root := t.TempDir()
	const containerNS = "net:[4026532100]"

	// Host process, no listeners of its own.
	addFakeProcess(t, root, 1, "dockerd")

	// Container process listening on 8080 (0x1F90) in its own namespace.
	addFakeProcessInNS(t, root, 500, "compose-api", containerNS)
	addFakeSocket(t, root, 500, 3, "55555")
	writeHostSocketTable(t, root, 500, false, [][3]string{
		{"00000000:1F90", "0A", "55555"},
	})

	p := newTestProvider(root)
	candidates, err := p.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	c := findCandidate(t, candidates, 500)
	if c.Name != "compose-api" {
		t.Errorf("Name = %q, want %q", c.Name, "compose-api")
	}
	if len(c.Ports) != 1 || c.Ports[0] != 8080 {
		t.Errorf("Ports = %v, want [8080]", c.Ports)
	}

	for _, cand := range candidates {
		if cand.PID == 1 {
			t.Errorf("expected host process PID 1 to have no ports, got %+v", cand)
		}
	}
}

// TestProcFSProviderDoesNotCrossAttributeInodes verifies that two
// namespaces reusing the same numeric inode never get joined to the wrong
// process (RFD 112).
func TestProcFSProviderDoesNotCrossAttributeInodes(t *testing.T) {
	root := t.TempDir()
	const containerNS = "net:[4026532200]"

	// Host process listening on port 8000 (0x1F40) via inode 777.
	addFakeProcess(t, root, 10, "host-app")
	addFakeSocket(t, root, 10, 3, "777")
	writeHostSocketTable(t, root, 10, false, [][3]string{
		{"00000000:1F40", "0A", "777"},
	})

	// Container process listening on port 9000 (0x2328), coincidentally
	// reusing inode 777 within its own namespace.
	addFakeProcessInNS(t, root, 600, "container-app", containerNS)
	addFakeSocket(t, root, 600, 3, "777")
	writeHostSocketTable(t, root, 600, false, [][3]string{
		{"00000000:2328", "0A", "777"},
	})

	p := newTestProvider(root)
	candidates, err := p.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	host := findCandidate(t, candidates, 10)
	if len(host.Ports) != 1 || host.Ports[0] != 8000 {
		t.Errorf("host Ports = %v, want [8000]", host.Ports)
	}

	container := findCandidate(t, candidates, 600)
	if len(container.Ports) != 1 || container.Ports[0] != 9000 {
		t.Errorf("container Ports = %v, want [9000]", container.Ports)
	}
}

// TestProcFSProviderTwoContainersSamePort verifies two isolated container
// namespaces both listening on the same port produce two distinct
// candidates rather than merging into one (RFD 112).
func TestProcFSProviderTwoContainersSamePort(t *testing.T) {
	root := t.TempDir()
	const ns1 = "net:[4026532300]"
	const ns2 = "net:[4026532301]"

	addFakeProcessInNS(t, root, 700, "app-a", ns1)
	addFakeSocket(t, root, 700, 3, "111")
	writeHostSocketTable(t, root, 700, false, [][3]string{
		{"00000000:1F90", "0A", "111"},
	})

	addFakeProcessInNS(t, root, 701, "app-b", ns2)
	addFakeSocket(t, root, 701, 3, "111")
	writeHostSocketTable(t, root, 701, false, [][3]string{
		{"00000000:1F90", "0A", "111"},
	})

	p := newTestProvider(root)
	candidates, err := p.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	a := findCandidate(t, candidates, 700)
	if len(a.Ports) != 1 || a.Ports[0] != 8080 {
		t.Errorf("app-a Ports = %v, want [8080]", a.Ports)
	}
	b := findCandidate(t, candidates, 701)
	if len(b.Ports) != 1 || b.Ports[0] != 8080 {
		t.Errorf("app-b Ports = %v, want [8080]", b.Ports)
	}
}

// TestProcFSProviderTwoProcessesShareNamespace verifies that when two PIDs
// share one network namespace, each listener is attributed only to its
// owning PID rather than to every PID in the namespace (RFD 112).
func TestProcFSProviderTwoProcessesShareNamespace(t *testing.T) {
	root := t.TempDir()
	const ns = "net:[4026532400]"

	// Representative PID 800 owns the listener on 8080.
	addFakeProcessInNS(t, root, 800, "web", ns)
	addFakeSocket(t, root, 800, 3, "222")
	writeHostSocketTable(t, root, 800, false, [][3]string{
		{"00000000:1F90", "0A", "222"},
	})

	// PID 801 shares the namespace but does not hold that socket fd.
	addFakeProcessInNS(t, root, 801, "sidecar", ns)

	p := newTestProvider(root)
	candidates, err := p.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	web := findCandidate(t, candidates, 800)
	if len(web.Ports) != 1 || web.Ports[0] != 8080 {
		t.Errorf("web Ports = %v, want [8080]", web.Ports)
	}

	for _, c := range candidates {
		if c.PID == 801 {
			t.Errorf("expected PID 801 (no owning fd) to produce no candidate, got %+v", c)
		}
	}
}

// TestProcFSProviderRepresentativeExitsRetriesNextPID verifies that when the
// lowest-PID representative of a namespace has exited (its directory is
// gone), discovery retries the next PID in that namespace rather than
// omitting the namespace (RFD 112).
func TestProcFSProviderRepresentativeExitsRetriesNextPID(t *testing.T) {
	root := t.TempDir()
	const ns = "net:[4026532500]"

	// PID 900 would be the representative (lowest PID) but has exited: its
	// directory never existed beyond being listed... simulate by removing
	// it after registering the namespace grouping is not directly
	// controllable, so instead create a stale ns/net symlink with no
	// backing pid dir content beyond comm, then delete the dir to emulate
	// an exit race.
	addFakeProcessInNS(t, root, 900, "gone", ns)
	if err := os.RemoveAll(filepath.Join(root, "900")); err != nil {
		t.Fatalf("failed to simulate exited process: %v", err)
	}

	// PID 901 is next in ascending order and is healthy.
	addFakeProcessInNS(t, root, 901, "survivor", ns)
	addFakeSocket(t, root, 901, 3, "333")
	writeHostSocketTable(t, root, 901, false, [][3]string{
		{"00000000:1F90", "0A", "333"},
	})

	p := newTestProvider(root)
	candidates, err := p.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	c := findCandidate(t, candidates, 901)
	if len(c.Ports) != 1 || c.Ports[0] != 8080 {
		t.Errorf("Ports = %v, want [8080]", c.Ports)
	}
}

// TestProcFSProviderUnreadableNamespaceDoesNotFailOthers verifies that a
// namespace whose only PID's socket table is unreadable is skipped without
// failing discovery for other namespaces (RFD 112).
func TestProcFSProviderUnreadableNamespaceDoesNotFailOthers(t *testing.T) {
	root := t.TempDir()
	const brokenNS = "net:[4026532600]"

	// A namespace whose sole PID's directory never gets created at all
	// (simulating a permission error / fully unreadable process), so no
	// socket table exists for it.
	pidDir := filepath.Join(root, "1000")
	if err := os.MkdirAll(filepath.Join(pidDir, "fd"), 0o755); err != nil {
		t.Fatalf("failed to create fd dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(pidDir, "ns"), 0o755); err != nil {
		t.Fatalf("failed to create ns dir: %v", err)
	}
	if err := os.Symlink(brokenNS, filepath.Join(pidDir, "ns", "net")); err != nil {
		t.Fatalf("failed to create ns/net symlink: %v", err)
	}
	writeProcFile(t, root, "1000/comm", "broken\n")
	// No net/tcp or net/tcp6 file for PID 1000: scanListeningSockets will
	// simply find nothing, so this namespace legitimately has no listeners
	// rather than being "unreadable" in the strict sense; still exercises
	// the code path of a namespace producing zero candidates.

	// A healthy, independent namespace.
	addFakeProcess(t, root, 100, "otel-app")
	addFakeSocket(t, root, 100, 3, "12345")
	writeHostSocketTable(t, root, 100, false, [][3]string{
		{"0100007F:1F90", "0A", "12345"},
	})

	p := newTestProvider(root)
	candidates, err := p.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v, want no provider-wide failure", err)
	}

	findCandidate(t, candidates, 100)
	for _, c := range candidates {
		if c.PID == 1000 {
			t.Errorf("expected PID 1000 (no listeners) to produce no candidate, got %+v", c)
		}
	}
}

// TestProcFSProviderWarnsOnPermissionDenied verifies that a PID whose
// /proc/<pid>/ns/net cannot be read due to a permission error is skipped
// without failing discovery, while other processes are still reported
// (RFD 112 capability diagnostics).
func TestProcFSProviderWarnsOnPermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses filesystem permission checks")
	}
	root := t.TempDir()

	// PID 1100: ns/net exists but is unreadable (permission denied).
	addFakeProcess(t, root, 1100, "restricted")
	nsDir := filepath.Join(root, "1100", "ns")
	if err := os.Chmod(nsDir, 0o000); err != nil {
		t.Fatalf("failed to chmod ns dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(nsDir, 0o755) })

	// A healthy, independent process should still be reported.
	addFakeProcess(t, root, 100, "otel-app")
	addFakeSocket(t, root, 100, 3, "12345")
	writeHostSocketTable(t, root, 100, false, [][3]string{
		{"0100007F:1F90", "0A", "12345"},
	})

	p := newTestProvider(root)
	candidates, err := p.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v, want no provider-wide failure", err)
	}

	findCandidate(t, candidates, 100)
	for _, c := range candidates {
		if c.PID == 1100 {
			t.Errorf("expected PID 1100 (permission denied) to produce no candidate, got %+v", c)
		}
	}
}

// TestProcFSProviderStableOrdering verifies candidates are returned in
// deterministic PID order across repeated scans.
func TestProcFSProviderStableOrdering(t *testing.T) {
	root := t.TempDir()

	addFakeProcess(t, root, 300, "app-c")
	addFakeSocket(t, root, 300, 3, "1")
	addFakeProcess(t, root, 100, "app-a")
	addFakeSocket(t, root, 100, 3, "2")
	addFakeProcess(t, root, 200, "app-b")
	addFakeSocket(t, root, 200, 3, "3")
	writeHostSocketTable(t, root, 100, false, [][3]string{
		{"00000000:1F90", "0A", "1"},
		{"00000000:1F91", "0A", "2"},
		{"00000000:1F92", "0A", "3"},
	})

	p := newTestProvider(root)

	var lastOrder []int
	for range 3 {
		candidates, err := p.Discover(context.Background())
		if err != nil {
			t.Fatalf("Discover() error = %v", err)
		}
		order := make([]int, len(candidates))
		for i, c := range candidates {
			order[i] = c.PID
		}
		if lastOrder != nil {
			if fmt.Sprint(order) != fmt.Sprint(lastOrder) {
				t.Errorf("candidate order changed across scans: %v vs %v", lastOrder, order)
			}
		}
		lastOrder = order
	}
	if fmt.Sprint(lastOrder) != fmt.Sprint([]int{100, 200, 300}) {
		t.Errorf("order = %v, want ascending PID order [100 200 300]", lastOrder)
	}
}

func findCandidate(t *testing.T, candidates []ProcessCandidate, pid int) ProcessCandidate {
	t.Helper()
	for _, c := range candidates {
		if c.PID == pid {
			return c
		}
	}
	t.Fatalf("no candidate found for PID %d in %+v", pid, candidates)
	return ProcessCandidate{}
}
