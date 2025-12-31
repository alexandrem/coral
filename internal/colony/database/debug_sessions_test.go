package database

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDebugSessionAttachUprobeScenario simulates the exact flow from AttachUprobe.
// This validates the specific end-to-end scenario that was causing the original error.
// This is the integration test that validates the real-world use case.
func TestDebugSessionAttachUprobeScenario(t *testing.T) {
	tempDir := t.TempDir()
	logger := zerolog.Nop()
	db, err := New(tempDir, "test-colony", logger)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// Simulate AttachUprobe flow
	sessionID := "session-uprobe-test"
	session := &DebugSession{
		SessionID:    sessionID,
		CollectorID:  "collector-123",
		ServiceName:  "demo/main",
		FunctionName: "ValidateCard",
		AgentID:      "agent-demo",
		SDKAddr:      "localhost:9092",
		StartedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(60 * time.Second),
		Status:       "active",
	}

	// This is where the original error occurred
	err = db.InsertDebugSession(context.Background(), session)
	require.NoError(t, err, "Session storage should succeed (was failing with PRIMARY KEY error)")

	// Verify the session was inserted
	retrieved, err := db.GetDebugSession(context.Background(), sessionID)
	require.NoError(t, err)
	assert.Equal(t, sessionID, retrieved.SessionID)
	assert.Equal(t, "demo/main", retrieved.ServiceName)
	assert.Equal(t, "ValidateCard", retrieved.FunctionName)

	// Simulate DetachUprobe
	err = db.UpdateDebugSessionStatus(context.Background(), sessionID, "stopped")
	require.NoError(t, err)

	retrieved, err = db.GetDebugSession(context.Background(), sessionID)
	require.NoError(t, err)
	assert.Equal(t, "stopped", retrieved.Status)
}
