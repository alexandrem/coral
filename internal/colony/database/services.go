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
