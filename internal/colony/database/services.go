package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Service represents a service registered in the colony.
type Service struct {
	ID       string
	Name     string
	AppID    string
	Version  string
	AgentID  string
	Labels   string
	LastSeen time.Time
	Status   string
}

// GetServiceByName retrieves a service by its name.
// It returns only the most recently seen instance if multiple exist with the same name.
func (d *Database) GetServiceByName(ctx context.Context, serviceName string) (*Service, error) {
	query := `
		SELECT id, name, app_id, version, agent_id, labels, last_seen, status
		FROM services
		WHERE name = ?
		ORDER BY last_seen DESC
		LIMIT 1
	`

	var s Service
	err := d.db.QueryRowContext(ctx, query, serviceName).Scan(
		&s.ID,
		&s.Name,
		&s.AppID,
		&s.Version,
		&s.AgentID,
		&s.Labels,
		&s.LastSeen,
		&s.Status,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // Not found
		}
		return nil, fmt.Errorf("failed to get service by name: %w", err)
	}

	return &s, nil
}

// UpsertService creates or updates a service in the database.
// Note: DuckDB has limitations with ON CONFLICT DO UPDATE when updating indexed columns.
// We use a manual DELETE + INSERT pattern instead.
func (d *Database) UpsertService(ctx context.Context, service *Service) error {
	// First, try to delete any existing entry
	deleteQuery := `DELETE FROM services WHERE id = ?`
	_, err := d.db.ExecContext(ctx, deleteQuery, service.ID)
	if err != nil {
		return fmt.Errorf("failed to delete existing service: %w", err)
	}

	// Then insert the new/updated entry
	insertQuery := `
		INSERT INTO services (id, name, app_id, version, agent_id, labels, last_seen, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err = d.db.ExecContext(ctx, insertQuery,
		service.ID,
		service.Name,
		service.AppID,
		service.Version,
		service.AgentID,
		service.Labels,
		service.LastSeen,
		service.Status,
	)

	if err != nil {
		return fmt.Errorf("failed to insert service: %w", err)
	}

	return nil
}

// UpdateServiceLastSeen updates the last_seen timestamp for all services belonging to an agent.
func (d *Database) UpdateServiceLastSeen(ctx context.Context, agentID string, lastSeen time.Time) error {
	query := `
		UPDATE services
		SET last_seen = ?
		WHERE agent_id = ?
	`

	_, err := d.db.ExecContext(ctx, query, lastSeen, agentID)
	if err != nil {
		return fmt.Errorf("failed to update service last_seen: %w", err)
	}

	return nil
}

// ListAllServices retrieves all services from the database.
func (d *Database) ListAllServices(ctx context.Context) ([]*Service, error) {
	query := `
		SELECT id, name, app_id, version, agent_id, labels, last_seen, status
		FROM services
		ORDER BY agent_id, name
	`

	rows, err := d.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query services: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var services []*Service
	for rows.Next() {
		var s Service
		err := rows.Scan(
			&s.ID,
			&s.Name,
			&s.AppID,
			&s.Version,
			&s.AgentID,
			&s.Labels,
			&s.LastSeen,
			&s.Status,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan service: %w", err)
		}
		services = append(services, &s)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return services, nil
}
