package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	prodAPIID    = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"
	stagingAPIID = "ffeeddccbbaa00112233445566778899ffeeddccbbaa00112233445566778899"
)

// newTestDockerLabelMatcher serves containers' labels from a fake Docker API
// and reads processes from a fake procfs tree. requests counts API calls.
func newTestDockerLabelMatcher(t *testing.T, labels map[string]map[string]string) (*dockerLabelMatcher, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/containers/"), "/json")
		containerLabels, ok := labels[id]
		if !ok {
			http.Error(w, `{"message":"No such container"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"Config": map[string]any{"Labels": containerLabels}}))
	}))
	t.Cleanup(server.Close)

	return &dockerLabelMatcher{
		procRoot: t.TempDir(),
		baseURL:  server.URL,
		client:   server.Client(),
		cache:    make(map[string]map[string]string),
	}, &requests
}

// writeContainerProcFile writes <proc>/<pid>/<name>.
func writeContainerProcFile(t *testing.T, m *dockerLabelMatcher, pid int, name, content string) {
	t.Helper()
	dir := filepath.Join(m.procRoot, strconv.Itoa(pid))
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}

func TestDockerLabelMatcher_ContainerID(t *testing.T) {
	m, _ := newTestDockerLabelMatcher(t, nil)

	// cgroup v1 and v2 paths name the container directly.
	writeContainerProcFile(t, m, 10, "cgroup", "12:pids:/docker/"+prodAPIID+"\n")
	writeContainerProcFile(t, m, 11, "cgroup", "0::/system.slice/docker-"+stagingAPIID+".scope\n")
	// In a private cgroup namespace the path is "/"; Docker's bind mounts
	// still name it.
	writeContainerProcFile(t, m, 12, "cgroup", "0::/\n")
	writeContainerProcFile(t, m, 12, "mountinfo",
		"612 590 254:1 /var/lib/docker/containers/"+prodAPIID+"/hostname /etc/hostname rw,relatime - ext4 /dev/vda1 rw\n")
	// A host process has neither.
	writeContainerProcFile(t, m, 13, "cgroup", "0::/system.slice/ssh.service\n")
	writeContainerProcFile(t, m, 13, "mountinfo", "22 1 254:1 / / rw,relatime - ext4 /dev/vda1 rw\n")

	tests := []struct {
		pid     int
		want    string
		wantErr error
	}{
		{pid: 10, want: prodAPIID},
		{pid: 11, want: stagingAPIID},
		{pid: 12, want: prodAPIID},
		{pid: 13, wantErr: errNotDockerContainer},
	}
	for _, tt := range tests {
		id, err := m.containerID(tt.pid)
		if tt.wantErr != nil {
			assert.ErrorIs(t, err, tt.wantErr, "pid %d", tt.pid)
			continue
		}
		require.NoError(t, err, "pid %d", tt.pid)
		assert.Equal(t, tt.want, id, "pid %d", tt.pid)
	}
}

func TestDockerLabelMatcher_Matches(t *testing.T) {
	m, requests := newTestDockerLabelMatcher(t, map[string]map[string]string{
		prodAPIID:    {"com.docker.swarm.service.name": "showup_api", "com.docker.stack.namespace": "showup"},
		stagingAPIID: {"com.docker.compose.project": "showup-staging", "com.docker.compose.service": "api"},
	})
	writeContainerProcFile(t, m, 10, "cgroup", "0::/system.slice/docker-"+prodAPIID+".scope\n")
	writeContainerProcFile(t, m, 11, "cgroup", "0::/system.slice/docker-"+stagingAPIID+".scope\n")
	// A container run by another runtime, unknown to the Docker daemon.
	writeContainerProcFile(t, m, 12, "cgroup", "0::/kubepods/cri-containerd-"+strings.Repeat("0", 64)+".scope\n")

	swarm := map[string]string{"com.docker.swarm.service.name": "showup_api"}
	compose := map[string]string{"com.docker.compose.project": "showup-staging", "com.docker.compose.service": "api"}

	ok, err := m.Matches(10, swarm)
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = m.Matches(11, swarm)
	require.NoError(t, err)
	assert.False(t, ok)

	ok, err = m.Matches(11, compose)
	require.NoError(t, err)
	assert.True(t, ok)

	// Every label must match.
	ok, err = m.Matches(11, map[string]string{"com.docker.compose.project": "showup-staging", "com.docker.compose.service": "web"})
	require.NoError(t, err)
	assert.False(t, ok)

	ok, err = m.Matches(12, swarm)
	require.NoError(t, err)
	assert.False(t, ok)

	// Each container is inspected once.
	assert.EqualValues(t, 3, requests.Load())
}

func TestDockerLabelMatcher_APIUnavailable(t *testing.T) {
	m, _ := newTestDockerLabelMatcher(t, nil)
	m.baseURL = "http://127.0.0.1:1"
	writeContainerProcFile(t, m, 10, "cgroup", "0::/system.slice/docker-"+prodAPIID+".scope\n")

	ok, err := m.Matches(10, map[string]string{"com.docker.swarm.service.name": "showup_api"})
	assert.False(t, ok)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Docker API")
}

func TestNewDockerLabelMatcher_DockerHost(t *testing.T) {
	tests := []struct {
		host    string
		baseURL string
		wantErr bool
	}{
		{host: "", baseURL: "http://docker"},
		{host: "unix:///run/user/1000/docker.sock", baseURL: "http://docker"},
		{host: "tcp://10.0.0.5:2375", baseURL: "http://10.0.0.5:2375"},
		{host: "ssh://user@host", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			t.Setenv("DOCKER_HOST", tt.host)
			m, err := newDockerLabelMatcher()
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.baseURL, m.baseURL)
		})
	}
}
