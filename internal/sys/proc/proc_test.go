package proc

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetKernelVersion(t *testing.T) {
	version := GetKernelVersion()
	if version == "" {
		t.Error("GetKernelVersion returned empty string")
	}
	// On non-Linux (like macOS), it might return "unknown", which is fine for this test logic
	// as we just want to ensure it runs.
}

func TestListPids(t *testing.T) {
	pids, err := FS("").ListPids()
	if err != nil {
		// If /proc doesn't exist (macOS), it returns error.
		if os.Getenv("GOOS") == "linux" {
			t.Errorf("ListPids returned error on Linux: %v", err)
		}
		return
	}

	if len(pids) == 0 {
		// It's possible but unlikely on a live system to have 0 PIDs visible if /proc exists.
		t.Log("ListPids returned 0 pids")
	}
}

func TestReadKallsyms(t *testing.T) {
	symbols, zeroAddresses, err := ReadKallsyms()
	if err != nil {
		// On non-Linux or without permissions, this might fail or return errors.
		// If /proc/kallsyms doesn't exist (macOS), it returns error.
		if os.Getenv("GOOS") == "linux" {
			// If we are root, we should get symbols. If not, we might get zeroAddresses > 0.
			if os.Geteuid() == 0 {
				if len(symbols) == 0 {
					t.Error("ReadKallsyms returned 0 symbols as root")
				}
			} else {
				// Non-root might see zero addresses
				if len(symbols) == 0 && zeroAddresses == 0 {
					// This could be strange on Linux unless kptr_restrict is very strict?
					t.Logf("ReadKallsyms returned error/empty on Linux: %v", err)
				}
			}
		}
		return
	}

	if len(symbols) > 0 {
		t.Logf("ReadKallsyms returned %d symbols", len(symbols))
	} else if zeroAddresses > 0 {
		t.Logf("ReadKallsyms found %d zero addresses (permissions)", zeroAddresses)
	}
}

// TestFindPidByExePattern_MatchesSelf matches the running test binary via
// its cmdline (the compiled test binary's basename won't fit in the
// 15-byte /proc/<pid>/comm field, so this exercises the cmdline fallback).
func TestFindPidByExePattern_MatchesSelf(t *testing.T) {
	pattern := regexp.QuoteMeta(filepath.Base(os.Args[0]))

	pid, err := FindPidByExePattern(pattern)
	if err != nil {
		if runtime.GOOS == "linux" {
			t.Fatalf("FindPidByExePattern returned error on Linux: %v", err)
		}
		return // No /proc on this platform.
	}

	if runtime.GOOS == "linux" && pid != int32(os.Getpid()) {
		t.Errorf("FindPidByExePattern matched pid %d, want own pid %d", pid, os.Getpid())
	}
}

func TestFindPidByExePattern_NoMatch(t *testing.T) {
	pid, err := FindPidByExePattern("definitely-not-a-real-process-name-xyz123")
	if err != nil {
		if runtime.GOOS == "linux" {
			t.Fatalf("FindPidByExePattern returned error on Linux: %v", err)
		}
		return
	}
	if pid != 0 {
		t.Errorf("FindPidByExePattern returned pid %d for a pattern that should match nothing", pid)
	}
}

func TestFindPidByExePattern_InvalidRegex(t *testing.T) {
	_, err := FindPidByExePattern("(unclosed[")
	if err == nil {
		t.Error("FindPidByExePattern should return an error for an invalid regex")
	}
}

// fakeProcess creates <root>/<pid> with a comm file and a network namespace
// link, plus one fd symlink per socket inode it holds.
func fakeProcess(t *testing.T, root string, pid int, ns string, inodes ...string) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "fd"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "ns"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "comm"), []byte("app\n"), 0o644))
	require.NoError(t, os.Symlink(ns, filepath.Join(dir, "ns", "net")))
	for i, inode := range inodes {
		holdSocket(t, root, pid, 3+i, inode)
	}
}

// holdSocket gives pid an fd symlink for the socket with the given inode.
func holdSocket(t *testing.T, root string, pid, fd int, inode string) {
	t.Helper()
	path := filepath.Join(root, strconv.Itoa(pid), "fd", strconv.Itoa(fd))
	require.NoError(t, os.Symlink("socket:["+inode+"]", path))
}

// writeListeners writes pid's net/tcp table with one LISTEN entry per
// inode -> port pair.
func writeListeners(t *testing.T, root string, pid int, sockets map[string]int) {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n")
	for inode, port := range sockets {
		fmt.Fprintf(&sb, "   0: 00000000:%04X 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 %s 1 0000000000000000 100 0 0 10 0\n", port, inode)
	}
	path := filepath.Join(root, strconv.Itoa(pid), "net", "tcp")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(sb.String()), 0o644))
}

const testNS = "net:[4026531993]"

func TestLookupEnv(t *testing.T) {
	environ := []byte("malformed\x00OTEL_SERVICE_NAME=a\x00SERVICE_NAME=b\x00EMPTY=\x00SERVICE_NAME=c\x00")
	tests := []struct {
		key       string
		want      string
		wantFound bool
	}{
		{"OTEL_SERVICE_NAME", "a", true},
		{"SERVICE_NAME", "c", true}, // Last occurrence wins; not matched inside OTEL_SERVICE_NAME.
		{"EMPTY", "", true},
		{"MISSING", "", false},
		{"malformed", "", false},
	}
	for _, tt := range tests {
		got, found := LookupEnv(environ, tt.key)
		require.Equal(t, tt.want, got, tt.key)
		require.Equal(t, tt.wantFound, found, tt.key)
	}
}

func TestScanListeners_LiveSocket(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux procfs")
	}
	for _, network := range []string{"tcp4", "tcp6"} {
		t.Run(network, func(t *testing.T) {
			address := "127.0.0.1:0"
			if network == "tcp6" {
				address = "[::1]:0"
			}
			listener, err := net.Listen(network, address)
			if err != nil && network == "tcp6" {
				t.Skipf("IPv6 unavailable: %v", err)
			}
			require.NoError(t, err)
			t.Cleanup(func() { _ = listener.Close() })

			listeners, err := FS("").ScanListeners()
			require.NoError(t, err)
			require.Contains(t, listeners.Ports[os.Getpid()], listener.Addr().(*net.TCPAddr).Port)
		})
	}
}

// TestListenerScanner verifies that a scan skips the descriptor walk of
// processes that held no listener on the previous scan only while their
// namespace's socket table is unchanged, and never trusts that for
// processes that do listen or were not seen before.
func TestListenerScanner(t *testing.T) {
	// setup creates PID 100 listening on 8080 (inode 111) and an idle PID
	// 200 in the same namespace, and runs a first, full scan.
	setup := func(t *testing.T) (string, *ListenerScanner) {
		t.Helper()
		root := t.TempDir()
		fakeProcess(t, root, 100, testNS, "111")
		fakeProcess(t, root, 200, testNS)
		writeListeners(t, root, 100, map[string]int{"111": 8080})
		s := NewListenerScanner(FS(root))
		require.Equal(t, map[int][]int{100: {8080}}, scanPorts(t, s))
		return root, s
	}

	t.Run("idle process skipped until full scan", func(t *testing.T) {
		root, s := setup(t)
		// PID 200 receives the existing listener (e.g. via SCM_RIGHTS),
		// leaving the socket table unchanged.
		holdSocket(t, root, 200, 3, "111")

		for scan := 2; scan <= listenerFullScanEvery; scan++ {
			require.NotContains(t, scanPorts(t, s), 200, "scan %d", scan)
		}
		require.Equal(t, []int{8080}, scanPorts(t, s)[200])
	})

	t.Run("socket table change invalidates idle processes", func(t *testing.T) {
		root, s := setup(t)
		holdSocket(t, root, 200, 3, "222")
		writeListeners(t, root, 100, map[string]int{"111": 8080, "222": 9090})
		require.Equal(t, []int{9090}, scanPorts(t, s)[200])
	})

	t.Run("new process is walked", func(t *testing.T) {
		root, s := setup(t)
		// PID 300 is forked by PID 100 and inherits its listener.
		fakeProcess(t, root, 300, testNS, "111")
		require.Equal(t, []int{8080}, scanPorts(t, s)[300])
	})

	t.Run("listening process is re-verified", func(t *testing.T) {
		root, s := setup(t)
		// PID 200 picks up the listener, then a full scan sees it.
		holdSocket(t, root, 200, 3, "111")
		for range listenerFullScanEvery {
			scanPorts(t, s)
		}
		// PID 100 closes its copy; PID 200 keeps the socket open, so the
		// table is unchanged but PID 100 must no longer be reported.
		require.NoError(t, os.Remove(filepath.Join(root, "100", "fd", "3")))
		require.Equal(t, map[int][]int{200: {8080}}, scanPorts(t, s))
	})
}

// scanPorts runs one scan and returns the ports reported per PID.
func scanPorts(t *testing.T, s *ListenerScanner) map[int][]int {
	t.Helper()
	listeners, err := s.Scan()
	require.NoError(t, err)
	return listeners.Ports
}
