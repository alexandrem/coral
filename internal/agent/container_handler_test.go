package agent

import (
	"errors"
	"runtime"
	"testing"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCgroupMatchesName(t *testing.T) {
	tests := []struct {
		name    string
		content string
		target  string
		want    bool
	}{
		{
			name:    "cgroup v1 docker match",
			content: "12:memory:/docker/a1b2c3d4e5f6\n11:cpu:/docker/a1b2c3d4e5f6\n",
			target:  "a1b2c3d4",
			want:    true,
		},
		{
			name:    "cgroup v2 containerd match",
			content: "0::/kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podUID.slice/cri-containerd-abc123.scope\n",
			target:  "cri-containerd-abc123",
			want:    true,
		},
		{
			name:    "docker-compose match by service name",
			content: "0::/system.slice/docker-coral-e2e-otel-app-1.scope\n",
			target:  "otel-app",
			want:    true,
		},
		{
			name:    "no match",
			content: "0::/system.slice/docker-coral-e2e-otel-app-1.scope\n",
			target:  "nginx",
			want:    false,
		},
		{
			name:    "case sensitive no match",
			content: "0::/system.slice/docker-coral-e2e-otel-app-1.scope\n",
			target:  "OTEL-APP",
			want:    false,
		},
		{
			name:    "empty content no match",
			content: "",
			target:  "any",
			want:    false,
		},
		{
			name:    "empty name matches any non-empty line",
			content: "0::/system.slice/docker-foo.scope\n",
			target:  "",
			want:    true,
		},
		// Standard Docker uses an opaque container ID in the cgroup path.
		// These cases confirm the cgroup match returns false so the caller
		// can fall back to comm matching instead.
		{
			name:    "opaque docker container ID does not match service name",
			content: "0::/docker/a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6\n",
			target:  "otel-app",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cgroupMatchesName(tt.content, tt.target)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFindPidByCgroupName_NoMatch(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("cgroup lookup requires Linux /proc")
	}

	h := NewContainerHandler(zerolog.Nop())
	_, err := h.findPidByCgroupName("definitely-not-a-real-container-xyzzy-12345")
	require.Error(t, err)

	var connectErr *connect.Error
	require.True(t, errors.As(err, &connectErr), "expected a connect.Error")
	assert.Equal(t, connect.CodeNotFound, connectErr.Code())
}

func TestDetectContainerPID_Fallback(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("PID detection requires Linux /proc")
	}

	h := NewContainerHandler(zerolog.Nop())
	pid, err := h.detectContainerPID("")
	require.NoError(t, err)
	assert.Greater(t, pid, 0)
}
