package proc

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

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
	pids, err := ListPids()
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

func addTestDescriptor(t testing.TB, root string, pid, fd int, target string) string {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid), "fd")
	require.NoError(t, os.MkdirAll(dir, 0755))
	path := filepath.Join(dir, strconv.Itoa(fd))
	require.NoError(t, os.Symlink(target, path))
	return path
}

func TestSocketOwnerCache_SharedSnapshot(t *testing.T) {
	root := t.TempDir()
	addTestDescriptor(t, root, 20, 3, "socket:[100]")
	addTestDescriptor(t, root, 10, 4, "socket:[200]")
	addTestDescriptor(t, root, 10, 5, "socket:[100]") // Shared socket: prefer lowest PID.
	addTestDescriptor(t, root, 10, 6, "pipe:[300]")
	addTestDescriptor(t, root, 10, 7, "/missing/file")
	require.NoError(t, os.WriteFile(filepath.Join(root, "10", "fd", "8"), nil, 0600))
	require.NoError(t, os.Mkdir(filepath.Join(root, "30"), 0755)) // Process with no accessible fd directory.
	require.NoError(t, os.Mkdir(filepath.Join(root, "self"), 0755))
	now := time.Now()
	cache := socketOwnerCache{procRoot: root, now: func() time.Time { return now }}
	for _, inode := range []string{"100", "200"} {
		pid, err := cache.lookup(inode)
		require.NoError(t, err)
		require.Equal(t, int32(10), pid)
	}
	pid, err := cache.lookup("300")
	require.NoError(t, err)
	require.Zero(t, pid, "pipes are not sockets")

	// A new process is invisible within the snapshot, even on repeated misses.
	// This verifies nearby lookups do not traverse procfs again.
	addTestDescriptor(t, root, 40, 3, "socket:[400]")
	for range 3 {
		pid, err = cache.lookup("400")
		require.NoError(t, err)
		require.Zero(t, pid)
	}
	now = now.Add(time.Second)
	pid, err = cache.lookup("400")
	require.NoError(t, err)
	require.Equal(t, int32(40), pid, "new sockets must be discovered when the snapshot expires")
}

func TestSocketOwnerCache_RejectsStaleOwners(t *testing.T) {
	for _, change := range []string{"closed fd", "reused fd", "exited process"} {
		t.Run(change, func(t *testing.T) {
			root := t.TempDir()
			fd := addTestDescriptor(t, root, 10, 3, "socket:[100]")
			now := time.Now()
			cache := socketOwnerCache{procRoot: root, now: func() time.Time { return now }}
			pid, err := cache.lookup("100")
			require.NoError(t, err)
			require.Equal(t, int32(10), pid)

			switch change {
			case "closed fd":
				require.NoError(t, os.Remove(fd))
			case "reused fd":
				require.NoError(t, os.Remove(fd))
				addTestDescriptor(t, root, 10, 3, "socket:[200]")
			case "exited process":
				require.NoError(t, os.RemoveAll(filepath.Join(root, "10")))
			}
			pid, err = cache.lookup("100")
			require.NoError(t, err)
			require.Zero(t, pid, "never return stale ownership during the cache TTL")

			// Reusing the inode in another process is picked up on refresh.
			addTestDescriptor(t, root, 20, 3, "socket:[100]")
			now = now.Add(time.Second)
			pid, err = cache.lookup("100")
			require.NoError(t, err)
			require.Equal(t, int32(20), pid)
		})
	}
}

func TestSocketOwnerCache_ConcurrentLookups(t *testing.T) {
	root := t.TempDir()
	for pid := 1; pid <= 20; pid++ {
		addTestDescriptor(t, root, pid, 3, fmt.Sprintf("socket:[%d]", pid))
	}
	cache := socketOwnerCache{procRoot: root, now: time.Now}
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() {
			want := int32(i%20 + 1)
			pid, err := cache.lookup(strconv.Itoa(int(want)))
			if err != nil || pid != want {
				t.Errorf("lookup returned (%d, %v), want (%d, nil)", pid, err, want)
			}
		})
	}
	wg.Wait()
}

func TestSocketOwnerCache_RefreshError(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	cache := socketOwnerCache{procRoot: filepath.Join(root, "proc"), now: func() time.Time { return now }}
	_, err := cache.lookup("100")
	require.Error(t, err)
	addTestDescriptor(t, cache.procRoot, 10, 3, "socket:[100]")
	pid, err := cache.lookup("100")
	require.NoError(t, err, "a failed refresh must not cache the error")
	require.Equal(t, int32(10), pid)
	now = now.Add(time.Second)
	require.NoError(t, os.Rename(cache.procRoot, filepath.Join(root, "gone")))
	pid, err = cache.lookup("100")
	require.Error(t, err)
	require.Zero(t, pid, "a failed refresh must not serve an expired snapshot")
}

func TestFindPidByPort_ListeningSocket(t *testing.T) {
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
			portSocketOwners.mu.Lock()
			portSocketOwners.expiresAt = time.Time{}
			portSocketOwners.mu.Unlock()
			pid, err := FindPidByPort(listener.Addr().(*net.TCPAddr).Port)
			require.NoError(t, err)
			require.Equal(t, int32(os.Getpid()), pid)
		})
	}
}

func BenchmarkSocketOwnerCache(b *testing.B) {
	root := b.TempDir()
	for pid := 1; pid <= 100; pid++ {
		for fd := 1; fd <= 20; fd++ {
			addTestDescriptor(b, root, pid, fd, fmt.Sprintf("socket:[%d]", pid*100+fd))
		}
	}
	now := time.Now()
	for _, cached := range []bool{false, true} {
		b.Run(fmt.Sprintf("cached_%t", cached), func(b *testing.B) {
			cache := socketOwnerCache{procRoot: root, now: func() time.Time { return now }}
			_, err := cache.lookup("101")
			require.NoError(b, err)
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if !cached {
					cache.expiresAt = time.Time{}
				}
				pid, err := cache.lookup(strconv.Itoa((i%100+1)*100 + 1))
				if err != nil || pid == 0 {
					b.Fatalf("lookup returned (%d, %v)", pid, err)
				}
			}
		})
	}
}
