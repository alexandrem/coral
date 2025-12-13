package collector

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// Storage handles local storage of system metrics.
// Metrics are stored for ~1 hour with 15s precision and can be queried by colony on-demand (RFD 071).
type Storage struct {
	db     *sql.DB
	logger zerolog.Logger
	mu     sync.RWMutex
}

// Metric represents a single system metric data point.
type Metric struct {
	Timestamp  time.Time
	Name       string
	Value      float64
	Unit       string
	MetricType string // gauge, counter, delta
	Attributes map[string]string
}

// NewStorage creates a new system metrics storage.
func NewStorage(db *sql.DB, logger zerolog.Logger) (*Storage, error) {
	s := &Storage{
		db:     db,
		logger: logger.With().Str("component", "system_metrics_storage").Logger(),
	}

	// Initialize schema.
	if err := s.initSchema(); err != nil {
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return s, nil
}

// initSchema creates the local system metrics table.
func (s *Storage) initSchema() error {
	schema := `
		CREATE TABLE IF NOT EXISTS system_metrics_local (
			timestamp        TIMESTAMP NOT NULL,
			metric_name      VARCHAR(50) NOT NULL,
			value            DOUBLE PRECISION NOT NULL,
			unit             VARCHAR(20),
			metric_type      VARCHAR(10),
			attributes       JSON,
			created_at       TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		-- Index for time-range queries.
		CREATE INDEX IF NOT EXISTS idx_system_metrics_timestamp
		ON system_metrics_local(timestamp DESC);

		-- Index for metric name and time filtering.
		CREATE INDEX IF NOT EXISTS idx_system_metrics_name_time
		ON system_metrics_local(metric_name, timestamp DESC);
	`

	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	// Force WAL checkpoint so remote HTTP clients can see the schema.
	if _, err := s.db.Exec("CHECKPOINT"); err != nil {
		s.logger.Warn().Err(err).Msg("Failed to checkpoint database")
	}

	s.logger.Info().Msg("System metrics storage schema initialized")

	// Set WAL auto-checkpoint limit for frequent flushing.
	if _, err := s.db.Exec("PRAGMA wal_autocheckpoint='4MB'"); err != nil {
		s.logger.Warn().Err(err).Msg("Failed to set WAL auto-checkpoint limit")
	}

	// Initial checkpoint.
	if _, err := s.db.Exec("CHECKPOINT"); err != nil {
		s.logger.Warn().Err(err).Msg("Initial checkpoint failed")
	}

	return nil
}

// StoreMetric stores a single system metric in local storage.
func (s *Storage) StoreMetric(ctx context.Context, metric Metric) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	attributesJSON, err := json.Marshal(metric.Attributes)
	if err != nil {
		return fmt.Errorf("failed to marshal attributes: %w", err)
	}

	query := `
		INSERT INTO system_metrics_local (
			timestamp, metric_name, value, unit, metric_type, attributes
		) VALUES (?, ?, ?, ?, ?, ?)
	`

	_, err = s.db.ExecContext(ctx, query,
		metric.Timestamp,
		metric.Name,
		metric.Value,
		metric.Unit,
		metric.MetricType,
		string(attributesJSON),
	)

	if err != nil {
		return fmt.Errorf("failed to store metric: %w", err)
	}

	return nil
}

// StoreMetrics stores multiple system metrics in a single transaction.
func (s *Storage) StoreMetrics(ctx context.Context, metrics []Metric) error {
	if len(metrics) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO system_metrics_local (
			timestamp, metric_name, value, unit, metric_type, attributes
		) VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, metric := range metrics {
		attributesJSON, err := json.Marshal(metric.Attributes)
		if err != nil {
			return fmt.Errorf("failed to marshal attributes: %w", err)
		}

		_, err = stmt.ExecContext(ctx,
			metric.Timestamp,
			metric.Name,
			metric.Value,
			metric.Unit,
			metric.MetricType,
			string(attributesJSON),
		)
		if err != nil {
			return fmt.Errorf("failed to execute statement: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// QueryMetrics retrieves metrics for a given time range and optional metric name filters.
func (s *Storage) QueryMetrics(ctx context.Context, startTime, endTime time.Time, metricNames []string) ([]Metric, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var rows *sql.Rows
	var err error

	if len(metricNames) > 0 {
		// Query specific metrics using IN clause.
		placeholders := make([]string, len(metricNames))
		args := make([]interface{}, 0, len(metricNames)+2)

		for i := range metricNames {
			placeholders[i] = "?"
			args = append(args, metricNames[i])
		}
		args = append(args, startTime, endTime)

		query := `
			SELECT timestamp, metric_name, value, unit, metric_type, CAST(attributes AS TEXT)
			FROM system_metrics_local
			WHERE metric_name IN (` + strings.Join(placeholders, ", ") + `)
			  AND timestamp >= ? AND timestamp <= ?
			ORDER BY timestamp ASC, metric_name
		`
		rows, err = s.db.QueryContext(ctx, query, args...)
	} else {
		// Query all metrics.
		query := `
			SELECT timestamp, metric_name, value, unit, metric_type, CAST(attributes AS TEXT)
			FROM system_metrics_local
			WHERE timestamp >= ? AND timestamp <= ?
			ORDER BY timestamp ASC, metric_name
		`
		rows, err = s.db.QueryContext(ctx, query, startTime, endTime)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to query metrics: %w", err)
	}
	defer rows.Close()

	var metrics []Metric
	for rows.Next() {
		var m Metric
		var attributesJSON string

		err := rows.Scan(
			&m.Timestamp,
			&m.Name,
			&m.Value,
			&m.Unit,
			&m.MetricType,
			&attributesJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		if attributesJSON != "" {
			if err := json.Unmarshal([]byte(attributesJSON), &m.Attributes); err != nil {
				s.logger.Warn().Err(err).Str("metric", m.Name).Msg("Failed to unmarshal attributes")
				m.Attributes = make(map[string]string)
			}
		} else {
			m.Attributes = make(map[string]string)
		}

		metrics = append(metrics, m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return metrics, nil
}

// CleanupOldMetrics removes metrics older than the retention period.
// This should be called periodically (e.g., every 10 minutes) with a retention of ~1 hour.
func (s *Storage) CleanupOldMetrics(ctx context.Context, retention time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-retention)
	query := `DELETE FROM system_metrics_local WHERE timestamp < ?`

	result, err := s.db.ExecContext(ctx, query, cutoff)
	if err != nil {
		return fmt.Errorf("failed to cleanup old metrics: %w", err)
	}

	rowsDeleted, _ := result.RowsAffected()
	if rowsDeleted > 0 {
		s.logger.Debug().
			Int64("rows_deleted", rowsDeleted).
			Time("cutoff", cutoff).
			Msg("Cleaned up old system metrics")
	}

	return nil
}

// RunCleanupLoop runs a periodic cleanup goroutine.
// It removes metrics older than the retention period every cleanupInterval.
func (s *Storage) RunCleanupLoop(ctx context.Context, retention time.Duration, cleanupInterval time.Duration) {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	s.logger.Info().
		Dur("retention", retention).
		Dur("cleanup_interval", cleanupInterval).
		Msg("Starting system metrics cleanup loop")

	for {
		select {
		case <-ctx.Done():
			s.logger.Info().Msg("Stopping system metrics cleanup loop")
			return
		case <-ticker.C:
			if err := s.CleanupOldMetrics(ctx, retention); err != nil {
				s.logger.Error().Err(err).Msg("Failed to cleanup old system metrics")
			}
		}
	}
}
