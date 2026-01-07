package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// DebugSession represents a persistent debug session record.
type DebugSession struct {
	SessionID    string    `duckdb:"session_id,pk"`
	CollectorID  string    `duckdb:"collector_id,immutable"`
	ServiceName  string    `duckdb:"service_name,immutable"`
	FunctionName string    `duckdb:"function_name,immutable"`
	AgentID      string    `duckdb:"agent_id,immutable"`
	SDKAddr      string    `duckdb:"sdk_addr,immutable"`
	StartedAt    time.Time `duckdb:"started_at,immutable"`
	ExpiresAt    time.Time `duckdb:"expires_at"`
	Status       string    `duckdb:"status"`
	RequestedBy  string    `duckdb:"requested_by,immutable"`
	EventCount   int       `duckdb:"event_count"`
}

// DebugSessionFilters contains filters for listing debug sessions.
type DebugSessionFilters struct {
	ServiceName string
	Status      string
}

// InsertDebugSession persists a new debug session to the database.
func (d *Database) InsertDebugSession(ctx context.Context, session *DebugSession) error {
	return d.debugSessionsTable.Insert(ctx, session)
}

// UpdateDebugSessionStatus updates the status of a debug session.
func (d *Database) UpdateDebugSessionStatus(ctx context.Context, sessionID, status string) error {
	session, err := d.debugSessionsTable.Get(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to get debug session: %w", err)
	}
	session.Status = status
	return d.debugSessionsTable.Update(ctx, session)
}

// GetDebugSession retrieves a debug session by ID.
func (d *Database) GetDebugSession(ctx context.Context, sessionID string) (*DebugSession, error) {
	return d.debugSessionsTable.Get(ctx, sessionID)
}

// ListDebugSessions retrieves debug sessions matching the provided filters.
func (d *Database) ListDebugSessions(filters DebugSessionFilters) ([]*DebugSession, error) {
	query := `
		SELECT session_id, collector_id, service_name, function_name,
		       agent_id, sdk_addr, started_at, expires_at, status, requested_by, event_count
		FROM debug_sessions
		WHERE 1=1
	`
	args := []interface{}{}

	if filters.ServiceName != "" {
		query += " AND service_name = ?"
		args = append(args, filters.ServiceName)
	}

	if filters.Status != "" {
		query += " AND status = ?"
		args = append(args, filters.Status)
	}

	query += " ORDER BY started_at DESC"

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list debug sessions: %w", err)
	}
	defer func() { _ = rows.Close() }() // TODO: errcheck

	var sessions []*DebugSession
	for rows.Next() {
		var session DebugSession
		var requestedBy sql.NullString

		if err := rows.Scan(
			&session.SessionID,
			&session.CollectorID,
			&session.ServiceName,
			&session.FunctionName,
			&session.AgentID,
			&session.SDKAddr,
			&session.StartedAt,
			&session.ExpiresAt,
			&session.Status,
			&requestedBy,
			&session.EventCount,
		); err != nil {
			return nil, fmt.Errorf("failed to scan debug session: %w", err)
		}

		if requestedBy.Valid {
			session.RequestedBy = requestedBy.String
		}

		sessions = append(sessions, &session)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating debug sessions: %w", err)
	}

	return sessions, nil
}
