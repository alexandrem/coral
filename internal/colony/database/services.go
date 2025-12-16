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
	ID           string
	Name         string
	AppID        string
	Version      string
	AgentID      string
	Labels       string
	LastSeen     time.Time // Populated from service_heartbeats table.
	Status       string
	RegisteredAt time.Time // From services table.
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
// This design allows efficient updates to last_seen without rewriting service metadata.
// Uses INSERT OR REPLACE with retry logic to handle DuckDB's optimistic concurrency conflicts.
func (d *Database) UpsertService(ctx context.Context, service *Service) error {
	cfg := retry.Config{
		MaxRetries:     10,
		InitialBackoff: 2 * time.Millisecond,
		Jitter:         0.5, // 50% jitter
	}

	return retry.Do(ctx, cfg, func() error {
		return d.upsertServiceOnce(ctx, service)
	}, isTransactionConflict)
}

// upsertServiceOnce performs a single upsert attempt.
func (d *Database) upsertServiceOnce(ctx context.Context, service *Service) error {
	// Determine registered_at timestamp.
	// If not provided, use current time for new services.
	registeredAt := service.RegisteredAt
	if registeredAt.IsZero() {
		registeredAt = time.Now()
	}

	// Use INSERT OR REPLACE for service metadata.
	// This is atomic and avoids concurrency issues with UPDATE/DELETE patterns.
	insertServiceQuery := `
		INSERT OR REPLACE INTO services (id, name, app_id, version, agent_id, labels, status, registered_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := d.db.ExecContext(ctx, insertServiceQuery,
		service.ID,
		service.Name,
		service.AppID,
		service.Version,
		service.AgentID,
		service.Labels,
		service.Status,
		registeredAt,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert service: %w", err)
	}

	// Use INSERT OR REPLACE for heartbeat.
	insertHeartbeatQuery := `
		INSERT OR REPLACE INTO service_heartbeats (service_id, last_seen)
		VALUES (?, ?)
	`
	_, err = d.db.ExecContext(ctx, insertHeartbeatQuery, service.ID, service.LastSeen)
	if err != nil {
		return fmt.Errorf("failed to upsert heartbeat: %w", err)
	}

	return nil
}

// isTransactionConflict checks if an error is a DuckDB transaction conflict that can be retried.
func isTransactionConflict(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "Conflict on update") ||
		strings.Contains(errStr, "PRIMARY KEY or UNIQUE constraint violated")
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
