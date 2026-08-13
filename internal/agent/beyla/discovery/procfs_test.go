package discovery

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// addFakeProcess creates <root>/<pid>/comm with the given name.
func addFakeProcess(t *testing.T, root string, pid int, name string) {
	t.Helper()
	writeProcFile(t, root, fmt.Sprintf("%d/comm", pid), name+"\n")
	// Ensure the fd directory exists (even if empty) so listeningPortsByPID
	// doesn't treat this as an exited process.
	fdDir := filepath.Join(root, fmt.Sprintf("%d", pid), "fd")
	if err := os.MkdirAll(fdDir, 0o755); err != nil {
		t.Fatalf("failed to create fd dir: %v", err)
	}
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

// writeSocketTable writes a /proc/net/tcp[6]-formatted file. rows are
// (hexLocalAddr, state, inode) triples; a header line is added automatically.
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

func TestProcFSProviderServerProcess(t *testing.T) {
	root := t.TempDir()

	// PID 100: otel-app, listening on port 8080 (0x1F90) via inode 12345.
	addFakeProcess(t, root, 100, "otel-app")
	addFakeSocket(t, root, 100, 3, "12345")
	writeSocketTable(t, root, "net/tcp", [][3]string{
		{"0100007F:1F90", "0A", "12345"},
	})

	p := &ProcFSProvider{procRoot: root}
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

	p := &ProcFSProvider{procRoot: root}
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
	writeSocketTable(t, root, "net/tcp", [][3]string{
		{"0100007F:1F90", "0A", "12345"},
	})

	p := &ProcFSProvider{procRoot: root}
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
	writeSocketTable(t, root, "net/tcp6", [][3]string{
		{"00000000000000000000000000000000:2382", "0A", "99999"},
	})

	p := &ProcFSProvider{procRoot: root}
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
	p := NewProcFSProvider()
	if !p.Probe() {
		t.Error("Probe() = false, want true")
	}
	if p.Name() != "procfs" {
		t.Errorf("Name() = %q, want %q", p.Name(), "procfs")
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
