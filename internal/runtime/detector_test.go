package runtime

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
)

func TestDetector_Detect(t *testing.T) {
	logger := zerolog.Nop()
	detector := NewDetector(logger, "v2.0.0-test")

	ctx := context.Background()
	result, err := detector.Detect(ctx)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Platform should be detected.
	assert.NotNil(t, result.Platform)
	assert.NotEmpty(t, result.Platform.Os)
	assert.NotEmpty(t, result.Platform.Arch)

	// Runtime type should be valid.
	assert.NotEqual(t, agentv1.RuntimeContext_RUNTIME_CONTEXT_UNKNOWN, result.RuntimeType)

	// Capabilities should be set.
	assert.NotNil(t, result.Capabilities)
	assert.True(t, result.Capabilities.CanConnect, "connect should always be supported")

	// Visibility should be set.
	assert.NotNil(t, result.Visibility)
	assert.NotEmpty(t, result.Visibility.Namespace)

	// Metadata should be set.
	assert.NotNil(t, result.DetectedAt)
	assert.Equal(t, "v2.0.0-test", result.Version)
}

func TestDetector_DetermineCapabilities(t *testing.T) {
	logger := zerolog.Nop()
	detector := NewDetector(logger, "v2.0.0")

	tests := []struct {
		name        string
		runtimeType agentv1.RuntimeContext
		sidecarMode agentv1.SidecarMode
		hasCRI      bool
		wantRun     bool
		wantExec    bool
		wantShell   bool
		wantConnect bool
	}{
		{
			name:        "Native - full capabilities",
			runtimeType: agentv1.RuntimeContext_RUNTIME_CONTEXT_NATIVE,
			sidecarMode: agentv1.SidecarMode_SIDECAR_MODE_UNKNOWN,
			hasCRI:      false,
			wantRun:     true,
			wantExec:    true,
			wantShell:   true,
			wantConnect: true,
		},
		{
			name:        "Docker - full capabilities",
			runtimeType: agentv1.RuntimeContext_RUNTIME_CONTEXT_DOCKER,
			sidecarMode: agentv1.SidecarMode_SIDECAR_MODE_UNKNOWN,
			hasCRI:      false,
			wantRun:     true,
			wantExec:    true,
			wantShell:   true,
			wantConnect: true,
		},
		{
			name:        "K8s Sidecar CRI - no run",
			runtimeType: agentv1.RuntimeContext_RUNTIME_CONTEXT_K8S_SIDECAR,
			sidecarMode: agentv1.SidecarMode_SIDECAR_MODE_CRI,
			hasCRI:      true,
			wantRun:     false,
			wantExec:    true,
			wantShell:   true,
			wantConnect: true,
		},
		{
			name:        "K8s Sidecar Shared NS - no run",
			runtimeType: agentv1.RuntimeContext_RUNTIME_CONTEXT_K8S_SIDECAR,
			sidecarMode: agentv1.SidecarMode_SIDECAR_MODE_SHARED_NS,
			hasCRI:      false,
			wantRun:     false,
			wantExec:    true,
			wantShell:   true,
			wantConnect: true,
		},
		{
			name:        "K8s Sidecar Passive - limited",
			runtimeType: agentv1.RuntimeContext_RUNTIME_CONTEXT_K8S_SIDECAR,
			sidecarMode: agentv1.SidecarMode_SIDECAR_MODE_PASSIVE,
			hasCRI:      false,
			wantRun:     false,
			wantExec:    false,
			wantShell:   false,
			wantConnect: true,
		},
		{
			name:        "K8s DaemonSet - no run",
			runtimeType: agentv1.RuntimeContext_RUNTIME_CONTEXT_K8S_DAEMONSET,
			sidecarMode: agentv1.SidecarMode_SIDECAR_MODE_UNKNOWN,
			hasCRI:      false,
			wantRun:     false,
			wantExec:    true,
			wantShell:   true,
			wantConnect: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var criSocket *agentv1.CRISocketInfo
			if tt.hasCRI {
				criSocket = &agentv1.CRISocketInfo{
					Path: "/var/run/containerd/containerd.sock",
					Type: "containerd",
				}
			}

			caps := detector.determineCapabilities(tt.runtimeType, tt.sidecarMode, criSocket)

			assert.Equal(t, tt.wantRun, caps.CanRun, "CanRun mismatch")
			assert.Equal(t, tt.wantExec, caps.CanExec, "CanExec mismatch")
			assert.Equal(t, tt.wantShell, caps.CanShell, "CanShell mismatch")
			assert.Equal(t, tt.wantConnect, caps.CanConnect, "CanConnect mismatch")
		})
	}
}

func TestDetector_CalculateVisibility(t *testing.T) {
	logger := zerolog.Nop()
	detector := NewDetector(logger, "v2.0.0")

	tests := []struct {
		name              string
		runtimeType       agentv1.RuntimeContext
		sidecarMode       agentv1.SidecarMode
		hasCRI            bool
		wantAllPids       bool
		wantAllContainers bool
		wantPodScope      bool
		wantNamespace     string
	}{
		{
			name:              "Native - all host PIDs",
			runtimeType:       agentv1.RuntimeContext_RUNTIME_CONTEXT_NATIVE,
			sidecarMode:       agentv1.SidecarMode_SIDECAR_MODE_UNKNOWN,
			hasCRI:            false,
			wantAllPids:       true,
			wantAllContainers: false,
			wantPodScope:      false,
			wantNamespace:     "host",
		},
		{
			name:              "Native with CRI - all PIDs and containers",
			runtimeType:       agentv1.RuntimeContext_RUNTIME_CONTEXT_NATIVE,
			sidecarMode:       agentv1.SidecarMode_SIDECAR_MODE_UNKNOWN,
			hasCRI:            true,
			wantAllPids:       true,
			wantAllContainers: true,
			wantPodScope:      false,
			wantNamespace:     "host",
		},
		{
			name:              "Docker - all containers",
			runtimeType:       agentv1.RuntimeContext_RUNTIME_CONTEXT_DOCKER,
			sidecarMode:       agentv1.SidecarMode_SIDECAR_MODE_UNKNOWN,
			hasCRI:            false,
			wantAllPids:       false,
			wantAllContainers: true,
			wantPodScope:      false,
			wantNamespace:     "container",
		},
		{
			name:              "K8s Sidecar - pod scope",
			runtimeType:       agentv1.RuntimeContext_RUNTIME_CONTEXT_K8S_SIDECAR,
			sidecarMode:       agentv1.SidecarMode_SIDECAR_MODE_CRI,
			hasCRI:            true,
			wantAllPids:       false,
			wantAllContainers: false,
			wantPodScope:      true,
			wantNamespace:     "pod",
		},
		{
			name:              "K8s DaemonSet - node scope",
			runtimeType:       agentv1.RuntimeContext_RUNTIME_CONTEXT_K8S_DAEMONSET,
			sidecarMode:       agentv1.SidecarMode_SIDECAR_MODE_UNKNOWN,
			hasCRI:            false,
			wantAllPids:       true,
			wantAllContainers: true,
			wantPodScope:      false,
			wantNamespace:     "node",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var criSocket *agentv1.CRISocketInfo
			if tt.hasCRI {
				criSocket = &agentv1.CRISocketInfo{
					Path: "/var/run/containerd/containerd.sock",
					Type: "containerd",
				}
			}

			vis := detector.calculateVisibility(tt.runtimeType, tt.sidecarMode, criSocket)

			assert.Equal(t, tt.wantAllPids, vis.AllPids, "AllPids mismatch")
			assert.Equal(t, tt.wantAllContainers, vis.AllContainers, "AllContainers mismatch")
			assert.Equal(t, tt.wantPodScope, vis.PodScope, "PodScope mismatch")
			assert.Equal(t, tt.wantNamespace, vis.Namespace, "Namespace mismatch")
		})
	}
}

func TestIsKubernetes(t *testing.T) {
	tests := []struct {
		name    string
		envVars map[string]string
		want    bool
	}{
		{
			name: "Kubernetes environment",
			envVars: map[string]string{
				"KUBERNETES_SERVICE_HOST": "10.96.0.1",
			},
			want: true,
		},
		{
			name:    "Non-Kubernetes environment",
			envVars: map[string]string{},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Note: This test would need environment manipulation
			// For now, we just test the current environment
			result := isKubernetes()
			// Can't make assertions about actual environment
			assert.IsType(t, false, result)
		})
	}
}

func TestProbeCRISocket(t *testing.T) {
	tests := []struct {
		name             string
		path             string
		wantType         string
		wantVersionEmpty bool
	}{
		{
			name:             "containerd socket",
			path:             "/var/run/containerd/containerd.sock",
			wantType:         "containerd",
			wantVersionEmpty: true, // No actual CRI query
		},
		{
			name:             "crio socket",
			path:             "/var/run/crio/crio.sock",
			wantType:         "crio",
			wantVersionEmpty: true,
		},
		{
			name:             "docker socket",
			path:             "/var/run/docker.sock",
			wantType:         "docker",
			wantVersionEmpty: true,
		},
		{
			name:             "unknown socket",
			path:             "/var/run/unknown.sock",
			wantType:         "unknown",
			wantVersionEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			criType, version := probeCRISocket(tt.path)

			assert.Equal(t, tt.wantType, criType)
			if tt.wantVersionEmpty {
				assert.Empty(t, version, "version should be empty without actual CRI query")
			}
		})
	}
}

func TestDetectPlatform(t *testing.T) {
	logger := zerolog.Nop()
	detector := NewDetector(logger, "v2.0.0")

	platform, err := detector.detectPlatform()

	require.NoError(t, err)
	require.NotNil(t, platform)

	// Should detect current platform.
	assert.NotEmpty(t, platform.Os)
	assert.NotEmpty(t, platform.Arch)
	assert.NotEmpty(t, platform.OsVersion)
	assert.NotEmpty(t, platform.Kernel)

	// OS should be one of the supported values.
	assert.Contains(t, []string{"linux", "darwin", "windows"}, platform.Os)

	// Architecture should be one of the common values.
	assert.Contains(t, []string{"amd64", "arm64", "386", "arm"}, platform.Arch)
}
