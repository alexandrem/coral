package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	hostMnt = "mnt:[4026531841]"
	appID   = "3f1c9a7e2b4d6f8091a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708"
	dbID    = "3f1c9a7e2b4d00000000000000000000000000000000000000000000000000ff"
)

// fakeProc describes one process in a fake procfs tree.
type fakeProc struct {
	pid    int
	comm   string
	mnt    string
	cgroup string
}

// newFakeProcHandler builds a procfs tree in a temp dir and returns a handler
// that reads it, with selfPID as the agent's own PID.
func newFakeProcHandler(t *testing.T, selfPID int, procs ...fakeProc) *ContainerHandler {
	t.Helper()
	root := t.TempDir()
	for _, p := range procs {
		dir := filepath.Join(root, strconv.Itoa(p.pid))
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "ns"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "comm"), []byte(p.comm+"\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "cgroup"), []byte(p.cgroup), 0o644))
		require.NoError(t, os.Symlink(p.mnt, filepath.Join(dir, "ns", "mnt")))
	}
	// Non-PID entries must be ignored.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sys"), 0o755))
	return &ContainerHandler{procRoot: root, selfPID: selfPID}
}

// hostPIDProcs is a node seen from a hostPID DaemonSet agent (PID 900).
func hostPIDProcs() []fakeProc {
	return []fakeProc{
		{pid: 1, comm: "systemd", mnt: hostMnt, cgroup: "0::/init.scope\n"},
		{pid: 2, comm: "kthreadd", mnt: hostMnt, cgroup: "0::/\n"},
		{pid: 300, comm: "sshd", mnt: hostMnt, cgroup: "0::/system.slice/ssh.service\n"},
		{pid: 410, comm: "nginx", mnt: "mnt:[1000]", cgroup: "0::/kubepods/burstable/podabc/cri-containerd-" + appID + ".scope\n"},
		{pid: 411, comm: "nginx", mnt: "mnt:[1000]", cgroup: "0::/kubepods/burstable/podabc/cri-containerd-" + appID + ".scope\n"},
		{pid: 520, comm: "postgres", mnt: "mnt:[2000]", cgroup: "12:pids:/docker/" + dbID + "\n"},
		{pid: 900, comm: "coral", mnt: "mnt:[3000]", cgroup: "0::/kubepods/podxyz/agent\n"},
	}
}

func TestDetectContainerPID_HostPIDMode(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		wantPID int
		wantErr string
	}{
		{name: "empty selector never resolves to host init", target: "", wantErr: "container selector required"},
		{name: "container ID prefix", target: "id:" + appID[:14], wantPID: 410},
		{name: "full container ID, upper case", target: "id:" + strings.ToUpper(dbID), wantPID: 520},
		{name: "ambiguous ID prefix", target: "id:" + appID[:12], wantErr: "ambiguous"},
		{name: "unknown ID", target: "id:deadbeef", wantErr: "not found"},
		{name: "non-hex ID", target: "id:nginx", wantErr: "invalid container selector"},
		{name: "empty ID", target: "id:", wantErr: "invalid container selector"},
		{name: "bare hex is a process name, not an ID", target: appID[:14], wantErr: "not found"},
		{name: "process name", target: "postgres", wantPID: 520},
		{name: "process name is case-sensitive", target: "Postgres", wantErr: "not found"},
		{name: "host process name is refused", target: "sshd", wantErr: "not found"},
		{name: "host init name is refused", target: "systemd", wantErr: "not found"},
		{name: "agent itself is skipped", target: "coral", wantErr: "not found"},
		{name: "unknown name", target: "redis", wantErr: "not found"},
		{name: "explicit PID", target: "pid:411", wantPID: 411},
		{name: "host init PID is refused", target: "pid:1", wantErr: "host mount namespace"},
		{name: "host process PID is refused", target: "pid:300", wantErr: "host mount namespace"},
		{name: "agent PID is refused", target: "pid:900", wantErr: "agent itself"},
		{name: "missing PID", target: "pid:12345", wantErr: "not accessible"},
		{name: "non-numeric PID", target: "pid:nginx", wantErr: "invalid container selector"},
		{name: "zero PID", target: "pid:0", wantErr: "invalid container selector"},
	}

	h := newFakeProcHandler(t, 900, hostPIDProcs()...)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pid, err := h.detectContainerPID(tt.target)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Zero(t, pid)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantPID, pid)
		})
	}
}

func TestDetectContainerPID_AmbiguousProcessName(t *testing.T) {
	procs := append(hostPIDProcs(),
		fakeProc{pid: 700, comm: "nginx", mnt: "mnt:[4000]", cgroup: "0::/docker/" + strings.Repeat("a", 64) + "\n"})
	h := newFakeProcHandler(t, 900, procs...)

	_, err := h.detectContainerPID("nginx")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "matches 2 containers")
}

func TestDetectContainerPID_SidecarMode(t *testing.T) {
	// Docker-compose sidecar sharing the app's PID namespace: the app is
	// PID 1 and there are no kernel threads.
	h := newFakeProcHandler(t, 40,
		fakeProc{pid: 1, comm: "node", mnt: "mnt:[1000]", cgroup: "0::/\n"},
		fakeProc{pid: 2, comm: "sh", mnt: "mnt:[1000]", cgroup: "0::/\n"},
		fakeProc{pid: 40, comm: "coral", mnt: "mnt:[2000]", cgroup: "0::/\n"},
	)

	pid, err := h.detectContainerPID("")
	require.NoError(t, err)
	assert.Equal(t, 1, pid)

	pid, err = h.detectContainerPID("node")
	require.NoError(t, err)
	assert.Equal(t, 1, pid)

	// PID 1 is the app here, not host init.
	pid, err = h.detectContainerPID("pid:1")
	require.NoError(t, err)
	assert.Equal(t, 1, pid)

	_, err = h.detectContainerPID("nginx")
	require.Error(t, err)
}

func TestDetectContainerPID_OnlyAgentVisible(t *testing.T) {
	h := newFakeProcHandler(t, 1, fakeProc{pid: 1, comm: "coral", mnt: "mnt:[1]", cgroup: "0::/\n"})

	_, err := h.detectContainerPID("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no container PID found")
}
