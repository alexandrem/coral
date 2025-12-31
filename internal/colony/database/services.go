package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/coral-mesh/coral/internal/retry"
)

// Service represents a service registered in the colony.
type Service struct {
	ID           string    `duckdb:"id,pk,immutable"`         // Immutable: PRIMARY KEY, cannot be updated.
	Name         string    `duckdb:"name,immutable"`          // Immutable: service name shouldn't change for an ID.
	AppID        string    `duckdb:"app_id,immutable"`        // Immutable: app ID shouldn't change.
	Version      string    `duckdb:"version"`                 // Mutable: version can be updated.
	AgentID      string    `duckdb:"agent_id,immutable"`      // Immutable: agent ID shouldn't change for a service.
	Labels       string    `duckdb:"labels"`                  // Mutable: labels can be updated.
	Status       string    `duckdb:"status"`                  // Mutable: status changes (active/inactive).
	RegisteredAt time.Time `duckdb:"registered_at,immutable"` // Immutable: registration time is fixed.

	// LastSeen is populated from service_heartbeats table via join or separate query.
	// It is not part of the services table definition.
	LastSeen time.Time `duckdb:"-"`
}

// ServiceHeartbeat represents the last seen time of a service.
type ServiceHeartbeat struct {
	ServiceID string    `duckdb:"service_id,pk,immutable"` // Immutable: PRIMARY KEY, cannot be updated.
	LastSeen  time.Time `duckdb:"last_seen"`               // Mutable: updated on each heartbeat.
}

// GetServiceByName retrieves a service by its name.
// It returns only the most recently seen instance if multiple exist with the same name.
func (d *Database) GetServiceByName(ctx context.Context, serviceName string) (*Service, error) {
	query := `
		SELECT s.id, s.name, s.app_id, s.version, s.agent_id, s.labels, s.status, s.registered_at, h.last_seen
		FROM services s
		LEFT JOIN service_heartbeats h ON s.id = h.service_id
		WHERE s.name = ?
		ORDER BY h.last_seen DESC
		LIMIT 1
	`

	var s Service
	var lastSeen sql.NullTime
	err := d.db.QueryRowContext(ctx, query, serviceName).Scan(
		&s.ID,
		&s.Name,
		&s.AppID,
		&s.Version,
		&s.AgentID,
		&s.Labels,
		&s.Status,
		&s.RegisteredAt,
		&lastSeen,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // Not found.
		}
		return nil, fmt.Errorf("failed to get service by name: %w", err)
	}

	if lastSeen.Valid {
		s.LastSeen = lastSeen.Time
	}

	return &s, nil
}

// UpsertService creates or updates a service in the database.
// Uses separate tables for metadata (services) and heartbeats (service_heartbeats).
// Wraps both operations in a retry block to handle concurrent upserts gracefully.
func (d *Database) UpsertService(ctx context.Context, service *Service) error {
	if service.RegisteredAt.IsZero() {
		service.RegisteredAt = time.Now()
	}

	// Retry the entire multi-operation sequence to handle concurrent conflicts.
	cfg := retry.Config{
		MaxRetries:     10,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     500 * time.Millisecond,
		Jitter:         0.1,
	}

	return retry.Do(ctx, cfg, func() error {
		// 1. Upsert Service Metadata
		if err := d.servicesTable.Upsert(ctx, service); err != nil {
			return fmt.Errorf("failed to upsert service: %w", err)
		}

		// 2. Upsert Heartbeat
		heartbeat := &ServiceHeartbeat{
			ServiceID: service.ID,
			LastSeen:  service.LastSeen,
		}
		if err := d.heartbeatsTable.Upsert(ctx, heartbeat); err != nil {
			return fmt.Errorf("failed to upsert heartbeat: %w", err)
		}

		return nil
	}, isTransactionConflict)
}

// isTransactionConflict checks if an error is a DuckDB transaction conflict.
func isTransactionConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// Detect various DuckDB transaction conflict patterns
	return strings.Contains(msg, "Conflict on update") ||
		strings.Contains(msg, "conflict") ||
		strings.Contains(msg, "transaction") ||
		strings.Contains(msg, "serialization") ||
		strings.Contains(msg, "TransactionContext Error") ||
		(strings.Contains(msg, "PRIMARY KEY") && strings.Contains(msg, "constraint violated"))
}

// UpdateServiceLastSeen updates the last_seen timestamp for all services belonging to an agent.
func (d *Database) UpdateServiceLastSeen(ctx context.Context, agentID string, lastSeen time.Time) error {
	// Get all service IDs for this agent.
	rows, err := d.db.QueryContext(ctx, "SELECT id FROM services WHERE agent_id = ?", agentID)
	if err != nil {
		return fmt.Errorf("failed to query services: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var serviceIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("failed to scan service ID: %w", err)
		}
		serviceIDs = append(serviceIDs, id)
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating service rows: %w", err)
	}

	// Update or insert heartbeat for each service.
	for _, serviceID := range serviceIDs {
		// Try update first.
		updateQuery := `UPDATE service_heartbeats SET last_seen = ? WHERE service_id = ?`
		result, err := d.db.ExecContext(ctx, updateQuery, lastSeen, serviceID)
		if err != nil {
			return fmt.Errorf("failed to update heartbeat for service %s: %w", serviceID, err)
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("failed to check rows affected: %w", err)
		}

		// If no rows updated, insert new heartbeat.
		if rowsAffected == 0 {
			insertQuery := `INSERT INTO service_heartbeats (service_id, last_seen) VALUES (?, ?)`
			_, err = d.db.ExecContext(ctx, insertQuery, serviceID, lastSeen)
			if err != nil {
				return fmt.Errorf("failed to insert heartbeat for service %s: %w", serviceID, err)
			}
		}
	}

	return nil
}

// ListAllServices retrieves all services from the database.
func (d *Database) ListAllServices(ctx context.Context) ([]*Service, error) {
	query := `
		SELECT s.id, s.name, s.app_id, s.version, s.agent_id, s.labels, s.status, s.registered_at, h.last_seen
		FROM services s
		LEFT JOIN service_heartbeats h ON s.id = h.service_id
		ORDER BY s.agent_id, s.name
	`

	rows, err := d.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query services: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var services []*Service
	for rows.Next() {
		var s Service
		var lastSeen sql.NullTime
		err := rows.Scan(
			&s.ID,
			&s.Name,
			&s.AppID,
			&s.Version,
			&s.AgentID,
			&s.Labels,
			&s.Status,
			&s.RegisteredAt,
			&lastSeen,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan service: %w", err)
		}

		if lastSeen.Valid {
			s.LastSeen = lastSeen.Time
		}

		services = append(services, &s)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return services, nil
}
