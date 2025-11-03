package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"

	agentv1 "github.com/coral-io/coral/coral/agent/v1"
)

func TestFormatRuntimeType(t *testing.T) {
	tests := []struct {
		name string
		rt   agentv1.RuntimeContext
		want string
	}{
		{
			name: "Native",
			rt:   agentv1.RuntimeContext_RUNTIME_CONTEXT_NATIVE,
			want: "Native",
		},
		{
			name: "Docker",
			rt:   agentv1.RuntimeContext_RUNTIME_CONTEXT_DOCKER,
			want: "Docker Container",
		},
		{
			name: "K8s Sidecar",
			rt:   agentv1.RuntimeContext_RUNTIME_CONTEXT_K8S_SIDECAR,
			want: "Kubernetes Sidecar",
		},
		{
			name: "K8s DaemonSet",
			rt:   agentv1.RuntimeContext_RUNTIME_CONTEXT_K8S_DAEMONSET,
			want: "Kubernetes DaemonSet",
		},
		{
			name: "Unknown",
			rt:   agentv1.RuntimeContext_RUNTIME_CONTEXT_UNKNOWN,
			want: "Unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatRuntimeType(tt.rt)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestFormatSidecarMode(t *testing.T) {
	tests := []struct {
		name string
		sm   agentv1.SidecarMode
		want string
	}{
		{
			name: "CRI",
			sm:   agentv1.SidecarMode_SIDECAR_MODE_CRI,
			want: "CRI (recommended)",
		},
		{
			name: "Shared NS",
			sm:   agentv1.SidecarMode_SIDECAR_MODE_SHARED_NS,
			want: "Shared Process Namespace",
		},
		{
			name: "Passive",
			sm:   agentv1.SidecarMode_SIDECAR_MODE_PASSIVE,
			want: "Passive (limited)",
		},
		{
			name: "Unknown",
			sm:   agentv1.SidecarMode_SIDECAR_MODE_UNKNOWN,
			want: "Unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatSidecarMode(tt.sm)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestFormatCapability(t *testing.T) {
	tests := []struct {
		name      string
		supported bool
		want      string
	}{
		{
			name:      "Supported",
			supported: true,
			want:      "✅",
		},
		{
			name:      "Not supported",
			supported: false,
			want:      "❌",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatCapability(tt.supported)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestFormatVisibilityScope(t *testing.T) {
	tests := []struct {
		name string
		ctx  *agentv1.RuntimeContextResponse
		want string
	}{
		{
			name: "All PIDs",
			ctx: &agentv1.RuntimeContextResponse{
				Visibility: &agentv1.VisibilityScope{
					AllPids: true,
				},
			},
			want: "All host processes",
		},
		{
			name: "All containers",
			ctx: &agentv1.RuntimeContextResponse{
				Visibility: &agentv1.VisibilityScope{
					AllContainers: true,
				},
			},
			want: "All containers",
		},
		{
			name: "Pod scope",
			ctx: &agentv1.RuntimeContextResponse{
				Visibility: &agentv1.VisibilityScope{
					PodScope: true,
				},
			},
			want: "Pod only",
		},
		{
			name: "Limited",
			ctx: &agentv1.RuntimeContextResponse{
				Visibility: &agentv1.VisibilityScope{},
			},
			want: "Limited",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatVisibilityScope(tt.ctx)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestTruncateContainerID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want string
	}{
		{
			name: "Short ID",
			id:   "abc123",
			want: "abc123",
		},
		{
			name: "Exactly 12 chars",
			id:   "abc123def456",
			want: "abc123def456",
		},
		{
			name: "Long ID - truncate",
			id:   "abc123def456ghi789jkl012",
			want: "abc123def456",
		},
		{
			name: "Empty ID",
			id:   "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateContainerID(tt.id)
			assert.Equal(t, tt.want, result)
		})
	}
}
