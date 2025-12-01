package database

import (
	"database/sql"
	"fmt"
	"time"
)

// DebugSession represents a persistent debug session record.
type DebugSession struct {
	SessionID    string
	CollectorID  string
	ServiceName  string
	FunctionName string
	AgentID      string
	SDKAddr      string
	StartedAt    time.Time
	ExpiresAt    time.Time
	Status       string
	RequestedBy  string
	EventCount   int
}

// DebugSessionFilters contains filters for listing debug sessions.
type DebugSessionFilters struct {
	ServiceName string
	Status      string
}

// InsertDebugSession persists a new debug session to the database.
func (d *Database) InsertDebugSession(session *DebugSession) error {
	query := `
		INSERT INTO debug_sessions (
			session_id, collector_id, service_name, function_name,
			agent_id, sdk_addr, started_at, expires_at, status, requested_by, event_count
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := d.db.Exec(query,
		session.SessionID,
		session.CollectorID,
		session.ServiceName,
		session.FunctionName,
		session.AgentID,
		session.SDKAddr,
		session.StartedAt,
		session.ExpiresAt,
		session.Status,
		session.RequestedBy,
		session.EventCount,
	)
	if err != nil {
		return fmt.Errorf("failed to insert debug session: %w", err)
	}

	return nil
}

// UpdateDebugSessionStatus updates the status of a debug session.
func (d *Database) UpdateDebugSessionStatus(sessionID, status string) error {
	query := `
		UPDATE debug_sessions
		SET status = ?
		WHERE session_id = ?
	`

	_, err := d.db.Exec(query, status, sessionID)
	if err != nil {
		return fmt.Errorf("failed to update debug session status: %w", err)
	}

	return nil
}

// GetDebugSession retrieves a debug session by ID.
func (d *Database) GetDebugSession(sessionID string) (*DebugSession, error) {
	query := `
		SELECT session_id, collector_id, service_name, function_name,
		       agent_id, sdk_addr, started_at, expires_at, status, requested_by, event_count
		FROM debug_sessions
		WHERE session_id = ?
	`

	var session DebugSession
	var requestedBy sql.NullString // Handle nullable field

	err := d.db.QueryRow(query, sessionID).Scan(
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
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // Not found
		}
		return nil, fmt.Errorf("failed to get debug session: %w", err)
	}

	if requestedBy.Valid {
		session.RequestedBy = requestedBy.String
	}

	return &session, nil
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
