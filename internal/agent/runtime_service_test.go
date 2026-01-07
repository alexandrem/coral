package agent

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
)

func TestNewRuntimeService(t *testing.T) {
	logger := zerolog.Nop()

	tests := []struct {
		name                string
		config              RuntimeServiceConfig
		wantRefreshInterval time.Duration
	}{
		{
			name: "default refresh interval",
			config: RuntimeServiceConfig{
				Context: context.Background(),
				Logger:  logger,
				Version: "v2.0.0",
			},
			wantRefreshInterval: 5 * time.Minute,
		},
		{
			name: "custom refresh interval",
			config: RuntimeServiceConfig{
				Context:         context.Background(),
				Logger:          logger,
				Version:         "v2.0.0",
				RefreshInterval: 10 * time.Minute,
			},
			wantRefreshInterval: 10 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, err := NewRuntimeService(tt.config)

			require.NoError(t, err)
			require.NotNil(t, svc)
			assert.Equal(t, tt.wantRefreshInterval, svc.refreshInterval)
		})
	}
}

func TestRuntimeService_StartAndStop(t *testing.T) {
	logger := zerolog.Nop()
	svc, err := NewRuntimeService(RuntimeServiceConfig{
		Context:         context.Background(),
		Logger:          logger,
		Version:         "v2.0.0-test",
		RefreshInterval: 1 * time.Minute,
	})
	require.NoError(t, err)

	// Start should detect runtime context.
	err = svc.Start()
	require.NoError(t, err)

	// Should have cached context.
	ctx := svc.GetCachedContext()
	require.NotNil(t, ctx)
	assert.Equal(t, "v2.0.0-test", ctx.Version)

	// Stop should not error.
	err = svc.Stop()
	require.NoError(t, err)
}

func TestRuntimeService_GetRuntimeContext(t *testing.T) {
	logger := zerolog.Nop()
	svc, err := NewRuntimeService(RuntimeServiceConfig{
		Context: context.Background(),
		Logger:  logger,
		Version: "v2.0.0",
	})
	require.NoError(t, err)

	// Start to perform initial detection.
	err = svc.Start()
	require.NoError(t, err)
	defer func() { _ = svc.Stop() }()

	// GetRuntimeContext should return cached context.
	ctx := context.Background()
	req := &agentv1.GetRuntimeContextRequest{}
	resp, err := svc.GetRuntimeContext(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotNil(t, resp.Platform)
	assert.NotNil(t, resp.Capabilities)
	assert.NotNil(t, resp.Visibility)
	assert.Equal(t, "v2.0.0", resp.Version)
}

func TestRuntimeService_GetRuntimeContext_NotStarted(t *testing.T) {
	logger := zerolog.Nop()
	svc, err := NewRuntimeService(RuntimeServiceConfig{
		Context: context.Background(),
		Logger:  logger,
		Version: "v2.0.0",
	})
	require.NoError(t, err)

	// GetRuntimeContext before Start should fail.
	ctx := context.Background()
	req := &agentv1.GetRuntimeContextRequest{}
	_, err = svc.GetRuntimeContext(ctx, req)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not yet detected")
}

func TestRuntimeService_RefreshContext(t *testing.T) {
	logger := zerolog.Nop()
	svc, err := NewRuntimeService(RuntimeServiceConfig{
		Context: context.Background(),
		Logger:  logger,
		Version: "v2.0.0",
	})
	require.NoError(t, err)

	// Start to perform initial detection.
	err = svc.Start()
	require.NoError(t, err)
	defer func() { _ = svc.Stop() }()

	initialCtx := svc.GetCachedContext()
	require.NotNil(t, initialCtx)
	initialTime := initialCtx.DetectedAt.AsTime()

	// Wait a bit to ensure timestamp changes.
	time.Sleep(10 * time.Millisecond)

	// Refresh context.
	err = svc.RefreshContext()
	require.NoError(t, err)

	refreshedCtx := svc.GetCachedContext()
	require.NotNil(t, refreshedCtx)
	refreshedTime := refreshedCtx.DetectedAt.AsTime()

	// Timestamp should be updated.
	assert.True(t, refreshedTime.After(initialTime))
}

func TestRuntimeService_HasContextChanged(t *testing.T) {
	logger := zerolog.Nop()
	svc, err := NewRuntimeService(RuntimeServiceConfig{
		Context: context.Background(),
		Logger:  logger,
		Version: "v2.0.0",
	})
	require.NoError(t, err)

	// Create two identical contexts.
	oldCtx := &agentv1.RuntimeContextResponse{
		RuntimeType: agentv1.RuntimeContext_RUNTIME_CONTEXT_NATIVE,
		SidecarMode: agentv1.SidecarMode_SIDECAR_MODE_UNKNOWN,
		CriSocket:   nil,
		Capabilities: &agentv1.Capabilities{
			CanRun:     true,
			CanExec:    true,
			CanShell:   true,
			CanConnect: true,
		},
		Visibility: &agentv1.VisibilityScope{
			ContainerIds: []string{},
		},
	}

	newCtx := &agentv1.RuntimeContextResponse{
		RuntimeType: agentv1.RuntimeContext_RUNTIME_CONTEXT_NATIVE,
		SidecarMode: agentv1.SidecarMode_SIDECAR_MODE_UNKNOWN,
		CriSocket:   nil,
		Capabilities: &agentv1.Capabilities{
			CanRun:     true,
			CanExec:    true,
			CanShell:   true,
			CanConnect: true,
		},
		Visibility: &agentv1.VisibilityScope{
			ContainerIds: []string{},
		},
	}

	// Should not detect change.
	assert.False(t, svc.hasContextChanged(oldCtx, newCtx))

	// Change runtime type.
	newCtx.RuntimeType = agentv1.RuntimeContext_RUNTIME_CONTEXT_DOCKER
	assert.True(t, svc.hasContextChanged(oldCtx, newCtx))
	newCtx.RuntimeType = agentv1.RuntimeContext_RUNTIME_CONTEXT_NATIVE

	// Change sidecar mode.
	newCtx.SidecarMode = agentv1.SidecarMode_SIDECAR_MODE_CRI
	assert.True(t, svc.hasContextChanged(oldCtx, newCtx))
	newCtx.SidecarMode = agentv1.SidecarMode_SIDECAR_MODE_UNKNOWN

	// Add CRI socket.
	newCtx.CriSocket = &agentv1.CRISocketInfo{
		Path: "/var/run/containerd/containerd.sock",
		Type: "containerd",
	}
	assert.True(t, svc.hasContextChanged(oldCtx, newCtx))
	newCtx.CriSocket = nil

	// Change capabilities.
	newCtx.Capabilities.CanRun = false
	assert.True(t, svc.hasContextChanged(oldCtx, newCtx))
	newCtx.Capabilities.CanRun = true

	// Change container IDs.
	newCtx.Visibility.ContainerIds = []string{"container1"}
	assert.True(t, svc.hasContextChanged(oldCtx, newCtx))
}

func TestRuntimeService_GetDetectedAt(t *testing.T) {
	logger := zerolog.Nop()
	svc, err := NewRuntimeService(RuntimeServiceConfig{
		Context: context.Background(),
		Logger:  logger,
		Version: "v2.0.0",
	})
	require.NoError(t, err)

	// Before Start, should return nil.
	assert.Nil(t, svc.GetDetectedAt())

	// After Start, should return timestamp.
	err = svc.Start()
	require.NoError(t, err)
	defer func() { _ = svc.Stop() }()

	detectedAt := svc.GetDetectedAt()
	require.NotNil(t, detectedAt)
	assert.True(t, detectedAt.IsValid())
}

func TestRuntimeService_GetCachedContext(t *testing.T) {
	logger := zerolog.Nop()
	svc, err := NewRuntimeService(RuntimeServiceConfig{
		Context: context.Background(),
		Logger:  logger,
		Version: "v2.0.0",
	})
	require.NoError(t, err)

	// Before Start, should return nil.
	assert.Nil(t, svc.GetCachedContext())

	// After Start, should return context.
	err = svc.Start()
	require.NoError(t, err)
	defer func() { _ = svc.Stop() }()

	ctx := svc.GetCachedContext()
	require.NotNil(t, ctx)
	assert.NotNil(t, ctx.Platform)
	assert.NotNil(t, ctx.Capabilities)
	assert.Equal(t, "v2.0.0", ctx.Version)
}
