package database

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestCheckpointDB(t *testing.T) *Database {
	t.Helper()
	tempDir := t.TempDir()
	logger := zerolog.Nop()
	db, err := New(tempDir, "test-checkpoints", logger)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// --- Polling Checkpoint Tests ---

func TestGetPollingCheckpoint_NotFound(t *testing.T) {
	db := setupTestCheckpointDB(t)
	ctx := context.Background()

	cp, err := db.GetPollingCheckpoint(ctx, "agent-1", "telemetry")
	require.NoError(t, err)
	assert.Nil(t, cp, "Expected nil checkpoint for non-existent entry")
}

func TestGetPollingCheckpoint_Exists(t *testing.T) {
	db := setupTestCheckpointDB(t)
	ctx := context.Background()

	err := db.UpdatePollingCheckpoint(ctx, "agent-1", "telemetry", "session-abc", 42)
	require.NoError(t, err)

	cp, err := db.GetPollingCheckpoint(ctx, "agent-1", "telemetry")
	require.NoError(t, err)
	require.NotNil(t, cp)

	assert.Equal(t, "agent-1", cp.AgentID)
	assert.Equal(t, "telemetry", cp.DataType)
	assert.Equal(t, "session-abc", cp.SessionID)
	assert.Equal(t, uint64(42), cp.LastSeqID)
	assert.False(t, cp.CreatedAt.IsZero())
	assert.False(t, cp.UpdatedAt.IsZero())
}

func TestUpdatePollingCheckpoint_Create(t *testing.T) {
	db := setupTestCheckpointDB(t)
	ctx := context.Background()

	err := db.UpdatePollingCheckpoint(ctx, "agent-1", "telemetry", "session-1", 100)
	require.NoError(t, err)

	cp, err := db.GetPollingCheckpoint(ctx, "agent-1", "telemetry")
	require.NoError(t, err)
	require.NotNil(t, cp)
	assert.Equal(t, uint64(100), cp.LastSeqID)
	assert.Equal(t, "session-1", cp.SessionID)
}

func TestUpdatePollingCheckpoint_Update(t *testing.T) {
	db := setupTestCheckpointDB(t)
	ctx := context.Background()

	// Create initial checkpoint.
	err := db.UpdatePollingCheckpoint(ctx, "agent-1", "telemetry", "session-1", 100)
	require.NoError(t, err)

	// Update to higher seq_id.
	err = db.UpdatePollingCheckpoint(ctx, "agent-1", "telemetry", "session-1", 200)
	require.NoError(t, err)

	cp, err := db.GetPollingCheckpoint(ctx, "agent-1", "telemetry")
	require.NoError(t, err)
	require.NotNil(t, cp)
	assert.Equal(t, uint64(200), cp.LastSeqID)
}

func TestUpdatePollingCheckpoint_SessionChange(t *testing.T) {
	db := setupTestCheckpointDB(t)
	ctx := context.Background()

	// Create checkpoint with session-1.
	err := db.UpdatePollingCheckpoint(ctx, "agent-1", "telemetry", "session-1", 500)
	require.NoError(t, err)

	// Update with different session (simulates agent DB recreation).
	err = db.UpdatePollingCheckpoint(ctx, "agent-1", "telemetry", "session-2", 10)
	require.NoError(t, err)

	cp, err := db.GetPollingCheckpoint(ctx, "agent-1", "telemetry")
	require.NoError(t, err)
	require.NotNil(t, cp)
	assert.Equal(t, "session-2", cp.SessionID)
	assert.Equal(t, uint64(10), cp.LastSeqID)
}

func TestUpdatePollingCheckpointTx(t *testing.T) {
	db := setupTestCheckpointDB(t)
	ctx := context.Background()

	tx, err := db.BeginTx(ctx)
	require.NoError(t, err)

	err = db.UpdatePollingCheckpointTx(ctx, tx, "agent-1", "telemetry", "session-1", 50)
	require.NoError(t, err)

	err = tx.Commit()
	require.NoError(t, err)

	cp, err := db.GetPollingCheckpoint(ctx, "agent-1", "telemetry")
	require.NoError(t, err)
	require.NotNil(t, cp)
	assert.Equal(t, uint64(50), cp.LastSeqID)
}

func TestResetPollingCheckpoint(t *testing.T) {
	db := setupTestCheckpointDB(t)
	ctx := context.Background()

	// Create checkpoints for same agent, different data types.
	err := db.UpdatePollingCheckpoint(ctx, "agent-1", "telemetry", "s1", 100)
	require.NoError(t, err)
	err = db.UpdatePollingCheckpoint(ctx, "agent-1", "system_metrics", "s1", 200)
	require.NoError(t, err)

	// Reset only telemetry checkpoint.
	err = db.ResetPollingCheckpoint(ctx, "agent-1", "telemetry")
	require.NoError(t, err)

	// Telemetry should be gone.
	cp, err := db.GetPollingCheckpoint(ctx, "agent-1", "telemetry")
	require.NoError(t, err)
	assert.Nil(t, cp)

	// System metrics should still exist.
	cp, err = db.GetPollingCheckpoint(ctx, "agent-1", "system_metrics")
	require.NoError(t, err)
	require.NotNil(t, cp)
	assert.Equal(t, uint64(200), cp.LastSeqID)
}

func TestResetAllPollingCheckpoints(t *testing.T) {
	db := setupTestCheckpointDB(t)
	ctx := context.Background()

	// Create checkpoints for multiple data types.
	for _, dt := range []string{"telemetry", "system_metrics", "beyla_http"} {
		err := db.UpdatePollingCheckpoint(ctx, "agent-1", dt, "s1", 100)
		require.NoError(t, err)
	}

	// Also create checkpoint for a different agent.
	err := db.UpdatePollingCheckpoint(ctx, "agent-2", "telemetry", "s2", 50)
	require.NoError(t, err)

	// Reset all for agent-1.
	err = db.ResetAllPollingCheckpoints(ctx, "agent-1")
	require.NoError(t, err)

	// All agent-1 checkpoints gone.
	for _, dt := range []string{"telemetry", "system_metrics", "beyla_http"} {
		cp, err := db.GetPollingCheckpoint(ctx, "agent-1", dt)
		require.NoError(t, err)
		assert.Nil(t, cp, "Expected nil for agent-1 %s", dt)
	}

	// Agent-2 checkpoint should still exist.
	cp, err := db.GetPollingCheckpoint(ctx, "agent-2", "telemetry")
	require.NoError(t, err)
	require.NotNil(t, cp)
	assert.Equal(t, uint64(50), cp.LastSeqID)
}

// --- Sequence Gap Tests ---

func TestRecordSequenceGap(t *testing.T) {
	db := setupTestCheckpointDB(t)
	ctx := context.Background()

	err := db.RecordSequenceGap(ctx, "agent-1", "telemetry", 5, 10)
	require.NoError(t, err)

	gaps, err := db.GetPendingSequenceGaps(ctx, 3)
	require.NoError(t, err)
	require.Len(t, gaps, 1)

	assert.Equal(t, "agent-1", gaps[0].AgentID)
	assert.Equal(t, "telemetry", gaps[0].DataType)
	assert.Equal(t, uint64(5), gaps[0].StartSeqID)
	assert.Equal(t, uint64(10), gaps[0].EndSeqID)
	assert.Equal(t, "detected", gaps[0].Status)
	assert.Equal(t, 0, gaps[0].RecoveryAttempts)
}

func TestGetPendingSequenceGaps(t *testing.T) {
	db := setupTestCheckpointDB(t)
	ctx := context.Background()

	// Record multiple gaps.
	require.NoError(t, db.RecordSequenceGap(ctx, "agent-1", "telemetry", 5, 10))
	require.NoError(t, db.RecordSequenceGap(ctx, "agent-1", "system_metrics", 20, 25))
	require.NoError(t, db.RecordSequenceGap(ctx, "agent-2", "telemetry", 1, 3))

	gaps, err := db.GetPendingSequenceGaps(ctx, 3)
	require.NoError(t, err)
	assert.Len(t, gaps, 3)
}

func TestGetPendingSequenceGaps_Filters(t *testing.T) {
	db := setupTestCheckpointDB(t)
	ctx := context.Background()

	// Record gaps with different statuses.
	require.NoError(t, db.RecordSequenceGap(ctx, "agent-1", "telemetry", 1, 5))      // detected
	require.NoError(t, db.RecordSequenceGap(ctx, "agent-1", "system_metrics", 1, 5)) // will be recovered
	require.NoError(t, db.RecordSequenceGap(ctx, "agent-1", "beyla_http", 1, 5))     // will be permanent
	require.NoError(t, db.RecordSequenceGap(ctx, "agent-1", "beyla_grpc", 1, 5))     // will exceed attempts

	// Mark one as recovered.
	gaps, _ := db.GetPendingSequenceGaps(ctx, 10)
	for _, g := range gaps {
		if g.DataType == "system_metrics" {
			require.NoError(t, db.MarkGapRecovered(ctx, g.ID))
		}
		if g.DataType == "beyla_http" {
			require.NoError(t, db.MarkGapPermanent(ctx, g.ID))
		}
		if g.DataType == "beyla_grpc" {
			// Increment attempts to exceed maxAttempts=2.
			require.NoError(t, db.IncrementGapRecoveryAttempt(ctx, g.ID))
			require.NoError(t, db.IncrementGapRecoveryAttempt(ctx, g.ID))
			require.NoError(t, db.IncrementGapRecoveryAttempt(ctx, g.ID))
		}
	}

	// Query with maxAttempts=2: should return only the first gap (detected, 0 attempts).
	pending, err := db.GetPendingSequenceGaps(ctx, 2)
	require.NoError(t, err)
	assert.Len(t, pending, 1)
	assert.Equal(t, "telemetry", pending[0].DataType)
}

func TestMarkGapRecovered(t *testing.T) {
	db := setupTestCheckpointDB(t)
	ctx := context.Background()

	require.NoError(t, db.RecordSequenceGap(ctx, "agent-1", "telemetry", 5, 10))

	gaps, _ := db.GetPendingSequenceGaps(ctx, 3)
	require.Len(t, gaps, 1)

	require.NoError(t, db.MarkGapRecovered(ctx, gaps[0].ID))

	// Should no longer appear in pending.
	pending, err := db.GetPendingSequenceGaps(ctx, 3)
	require.NoError(t, err)
	assert.Len(t, pending, 0)
}

func TestMarkGapPermanent(t *testing.T) {
	db := setupTestCheckpointDB(t)
	ctx := context.Background()

	require.NoError(t, db.RecordSequenceGap(ctx, "agent-1", "telemetry", 5, 10))

	gaps, _ := db.GetPendingSequenceGaps(ctx, 3)
	require.Len(t, gaps, 1)

	require.NoError(t, db.MarkGapPermanent(ctx, gaps[0].ID))

	// Should no longer appear in pending.
	pending, err := db.GetPendingSequenceGaps(ctx, 3)
	require.NoError(t, err)
	assert.Len(t, pending, 0)
}

func TestIncrementGapRecoveryAttempt(t *testing.T) {
	db := setupTestCheckpointDB(t)
	ctx := context.Background()

	require.NoError(t, db.RecordSequenceGap(ctx, "agent-1", "telemetry", 5, 10))

	gaps, _ := db.GetPendingSequenceGaps(ctx, 10)
	require.Len(t, gaps, 1)
	assert.Equal(t, 0, gaps[0].RecoveryAttempts)
	assert.Equal(t, "detected", gaps[0].Status)

	// Increment attempt.
	require.NoError(t, db.IncrementGapRecoveryAttempt(ctx, gaps[0].ID))

	gaps, _ = db.GetPendingSequenceGaps(ctx, 10)
	require.Len(t, gaps, 1)
	assert.Equal(t, 1, gaps[0].RecoveryAttempts)
	assert.Equal(t, "recovering", gaps[0].Status)
}

func TestCleanupOldSequenceGaps(t *testing.T) {
	db := setupTestCheckpointDB(t)
	ctx := context.Background()

	// Record gaps and resolve them.
	require.NoError(t, db.RecordSequenceGap(ctx, "agent-1", "telemetry", 1, 5))
	require.NoError(t, db.RecordSequenceGap(ctx, "agent-1", "system_metrics", 1, 5))
	require.NoError(t, db.RecordSequenceGap(ctx, "agent-1", "beyla_http", 1, 5)) // stays pending

	gaps, _ := db.GetPendingSequenceGaps(ctx, 10)
	for _, g := range gaps {
		if g.DataType == "telemetry" {
			require.NoError(t, db.MarkGapRecovered(ctx, g.ID))
		}
		if g.DataType == "system_metrics" {
			require.NoError(t, db.MarkGapPermanent(ctx, g.ID))
		}
	}

	// Cleanup with 0 retention (removes all resolved gaps).
	err := db.CleanupOldSequenceGaps(ctx, 0)
	require.NoError(t, err)

	// Only the pending gap should remain.
	pending, err := db.GetPendingSequenceGaps(ctx, 10)
	require.NoError(t, err)
	assert.Len(t, pending, 1)
	assert.Equal(t, "beyla_http", pending[0].DataType)
}

func TestCleanupOldSequenceGaps_KeepsRecent(t *testing.T) {
	db := setupTestCheckpointDB(t)
	ctx := context.Background()

	// Record and recover a gap.
	require.NoError(t, db.RecordSequenceGap(ctx, "agent-1", "telemetry", 1, 5))
	gaps, _ := db.GetPendingSequenceGaps(ctx, 10)
	require.Len(t, gaps, 1)
	require.NoError(t, db.MarkGapRecovered(ctx, gaps[0].ID))

	// Cleanup with 24h retention — gap was just created, should be kept.
	err := db.CleanupOldSequenceGaps(ctx, 24*time.Hour)
	require.NoError(t, err)

	// Verify the gap still exists by counting rows directly.
	var count int
	err = db.db.QueryRow("SELECT COUNT(*) FROM sequence_gaps").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "Recently recovered gap should be kept")
}
