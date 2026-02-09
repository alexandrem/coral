package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// PollingCheckpoint represents a checkpoint for sequence-based polling (RFD 089).
type PollingCheckpoint struct {
	AgentID      string
	DataType     string
	SessionID    string
	LastSeqID    uint64
	LastPollTime time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// SequenceGap represents a detected gap in polling sequences (RFD 089).
type SequenceGap struct {
	ID                  int
	AgentID             string
	DataType            string
	StartSeqID          uint64
	EndSeqID            uint64
	DetectedAt          time.Time
	RecoveredAt         *time.Time
	Status              string // "detected", "recovering", "recovered", "permanent".
	RecoveryAttempts    int
	LastRecoveryAttempt *time.Time
}

// GetPollingCheckpoint retrieves the last checkpoint for an agent/data_type pair.
// Returns nil if no checkpoint exists (first poll scenario).
func (d *Database) GetPollingCheckpoint(ctx context.Context, agentID, dataType string) (*PollingCheckpoint, error) {
	var cp PollingCheckpoint
	err := d.db.QueryRowContext(ctx,
		"SELECT agent_id, data_type, session_id, last_seq_id, last_poll_time, created_at, updated_at FROM polling_checkpoints WHERE agent_id = ? AND data_type = ?",
		agentID, dataType,
	).Scan(&cp.AgentID, &cp.DataType, &cp.SessionID, &cp.LastSeqID, &cp.LastPollTime, &cp.CreatedAt, &cp.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get polling checkpoint: %w", err)
	}

	return &cp, nil
}

// UpdatePollingCheckpoint creates or updates a checkpoint for an agent/data_type pair.
// If the session_id has changed (database reset), the checkpoint is reset.
// This should be called within the same transaction as the data storage
// to ensure atomicity (RFD 089).
func (d *Database) UpdatePollingCheckpoint(ctx context.Context, agentID, dataType, sessionID string, lastSeqID uint64) error {
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO polling_checkpoints (agent_id, data_type, session_id, last_seq_id, last_poll_time, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT (agent_id, data_type)
		DO UPDATE SET
			session_id = EXCLUDED.session_id,
			last_seq_id = EXCLUDED.last_seq_id,
			last_poll_time = now(),
			updated_at = now()
	`, agentID, dataType, sessionID, lastSeqID)

	if err != nil {
		return fmt.Errorf("failed to update polling checkpoint: %w", err)
	}
	return nil
}

// UpdatePollingCheckpointTx creates or updates a checkpoint within an existing transaction.
// Use this to ensure atomicity between data storage and checkpoint updates (RFD 089).
func (d *Database) UpdatePollingCheckpointTx(ctx context.Context, tx *sql.Tx, agentID, dataType, sessionID string, lastSeqID uint64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO polling_checkpoints (agent_id, data_type, session_id, last_seq_id, last_poll_time, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT (agent_id, data_type)
		DO UPDATE SET
			session_id = EXCLUDED.session_id,
			last_seq_id = EXCLUDED.last_seq_id,
			last_poll_time = now(),
			updated_at = now()
	`, agentID, dataType, sessionID, lastSeqID)

	if err != nil {
		return fmt.Errorf("failed to update polling checkpoint in tx: %w", err)
	}
	return nil
}

// ResetPollingCheckpoint deletes the checkpoint for an agent/data_type pair.
// Used when a session_id mismatch is detected (database reset).
func (d *Database) ResetPollingCheckpoint(ctx context.Context, agentID, dataType string) error {
	_, err := d.db.ExecContext(ctx,
		"DELETE FROM polling_checkpoints WHERE agent_id = ? AND data_type = ?",
		agentID, dataType,
	)
	if err != nil {
		return fmt.Errorf("failed to reset polling checkpoint: %w", err)
	}
	return nil
}

// ResetAllPollingCheckpoints deletes all checkpoints for an agent.
// Used when the agent is removed from the registry.
func (d *Database) ResetAllPollingCheckpoints(ctx context.Context, agentID string) error {
	_, err := d.db.ExecContext(ctx,
		"DELETE FROM polling_checkpoints WHERE agent_id = ?",
		agentID,
	)
	if err != nil {
		return fmt.Errorf("failed to reset all polling checkpoints: %w", err)
	}
	return nil
}

// RecordSequenceGap stores a detected sequence gap for tracking and recovery (RFD 089).
func (d *Database) RecordSequenceGap(ctx context.Context, agentID, dataType string, startSeqID, endSeqID uint64) error {
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO sequence_gaps (agent_id, data_type, start_seq_id, end_seq_id)
		VALUES (?, ?, ?, ?)
	`, agentID, dataType, startSeqID, endSeqID)

	if err != nil {
		return fmt.Errorf("failed to record sequence gap: %w", err)
	}
	return nil
}

// GetPendingSequenceGaps retrieves gaps that need recovery attempts.
func (d *Database) GetPendingSequenceGaps(ctx context.Context, maxAttempts int) ([]SequenceGap, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, agent_id, data_type, start_seq_id, end_seq_id, detected_at,
		       recovered_at, status, recovery_attempts, last_recovery_attempt
		FROM sequence_gaps
		WHERE status IN ('detected', 'recovering')
		  AND recovery_attempts < ?
		ORDER BY detected_at ASC
		LIMIT 100
	`, maxAttempts)
	if err != nil {
		return nil, fmt.Errorf("failed to get pending sequence gaps: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var gaps []SequenceGap
	for rows.Next() {
		var g SequenceGap
		err := rows.Scan(
			&g.ID, &g.AgentID, &g.DataType, &g.StartSeqID, &g.EndSeqID,
			&g.DetectedAt, &g.RecoveredAt, &g.Status, &g.RecoveryAttempts,
			&g.LastRecoveryAttempt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan sequence gap: %w", err)
		}
		gaps = append(gaps, g)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating sequence gaps: %w", err)
	}
	return gaps, nil
}

// MarkGapRecovered marks a sequence gap as recovered.
func (d *Database) MarkGapRecovered(ctx context.Context, gapID int) error {
	_, err := d.db.ExecContext(ctx, `
		UPDATE sequence_gaps
		SET status = 'recovered',
		    recovered_at = now(),
		    last_recovery_attempt = now()
		WHERE id = ?
	`, gapID)
	if err != nil {
		return fmt.Errorf("failed to mark gap recovered: %w", err)
	}
	return nil
}

// MarkGapPermanent marks a sequence gap as permanent (data lost, unrecoverable).
func (d *Database) MarkGapPermanent(ctx context.Context, gapID int) error {
	_, err := d.db.ExecContext(ctx, `
		UPDATE sequence_gaps
		SET status = 'permanent',
		    last_recovery_attempt = now()
		WHERE id = ?
	`, gapID)
	if err != nil {
		return fmt.Errorf("failed to mark gap permanent: %w", err)
	}
	return nil
}

// IncrementGapRecoveryAttempt increments the recovery attempt counter for a gap.
func (d *Database) IncrementGapRecoveryAttempt(ctx context.Context, gapID int) error {
	_, err := d.db.ExecContext(ctx, `
		UPDATE sequence_gaps
		SET recovery_attempts = recovery_attempts + 1,
		    status = 'recovering',
		    last_recovery_attempt = now()
		WHERE id = ?
	`, gapID)
	if err != nil {
		return fmt.Errorf("failed to increment gap recovery attempt: %w", err)
	}
	return nil
}

// CleanupOldSequenceGaps removes recovered or permanent gaps older than the retention period.
func (d *Database) CleanupOldSequenceGaps(ctx context.Context, retention time.Duration) error {
	cutoff := time.Now().Add(-retention)
	_, err := d.db.ExecContext(ctx, `
		DELETE FROM sequence_gaps
		WHERE status IN ('recovered', 'permanent')
		  AND detected_at < ?
	`, cutoff)
	if err != nil {
		return fmt.Errorf("failed to cleanup old sequence gaps: %w", err)
	}
	return nil
}

// BeginTx starts a new transaction on the colony database.
// Use this for atomic checkpoint-commit operations (RFD 089).
func (d *Database) BeginTx(ctx context.Context) (*sql.Tx, error) {
	return d.db.BeginTx(ctx, nil)
}
