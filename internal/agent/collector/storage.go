// Package collector implements system metrics collection and storage.
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

	"github.com/coral-mesh/coral/internal/duckdb"
)

// ORM model for system metrics table.

type systemMetricDB struct {
	Timestamp  time.Time `duckdb:"timestamp"`
	MetricName string    `duckdb:"metric_name"`
	Value      float64   `duckdb:"value"`
	Unit       string    `duckdb:"unit"`
	MetricType string    `duckdb:"metric_type"`
	Attributes string    `duckdb:"attributes"`
	CreatedAt  time.Time `duckdb:"created_at,immutable"`
}

// Storage handles local storage of system metrics.
// Metrics are stored for ~1 hour with 15s precision and can be queried by colony on-demand (RFD 071).
type Storage struct {
	db           *sql.DB
	logger       zerolog.Logger
	mu           sync.RWMutex
	metricsTable *duckdb.Table[systemMetricDB]
}

// Metric represents a single system metric data point.
type Metric struct {
	Timestamp  time.Time
	Name       string
	Value      float64
	Unit       string
	MetricType string // gauge, counter, delta
	Attributes map[string]string
	SeqID      uint64 // Sequence ID for checkpoint-based polling (RFD 089).
}

// NewStorage creates a new system metrics storage.
func NewStorage(db *sql.DB, logger zerolog.Logger) (*Storage, error) {
	s := &Storage{
		db:           db,
		logger:       logger.With().Str("component", "system_metrics_storage").Logger(),
		metricsTable: duckdb.NewTable[systemMetricDB](db, "system_metrics_local"),
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
		-- Sequence for checkpoint-based polling (RFD 089).
		CREATE SEQUENCE IF NOT EXISTS seq_system_metrics START 1;

		CREATE TABLE IF NOT EXISTS system_metrics_local (
			seq_id           UBIGINT DEFAULT nextval('seq_system_metrics'),
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

		-- Index for sequence-based polling (RFD 089).
		CREATE INDEX IF NOT EXISTS idx_system_metrics_seq_id
		ON system_metrics_local(seq_id);
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

	// Convert to ORM models.
	items := make([]*systemMetricDB, 0, len(metrics))
	for _, metric := range metrics {
		attributesJSON, err := json.Marshal(metric.Attributes)
		if err != nil {
			return fmt.Errorf("failed to marshal attributes: %w", err)
		}

		items = append(items, &systemMetricDB{
			Timestamp:  metric.Timestamp,
			MetricName: metric.Name,
			Value:      metric.Value,
			Unit:       metric.Unit,
			MetricType: metric.MetricType,
			Attributes: string(attributesJSON),
			CreatedAt:  time.Now(),
		})
	}

	if err := s.metricsTable.BatchUpsert(ctx, items); err != nil {
		return fmt.Errorf("failed to batch upsert metrics: %w", err)
	}

	return nil
}

// QueryMetricsBySeqID queries metrics with seq_id > startSeqID, ordered by seq_id ascending (RFD 089).
// Returns metrics and the maximum seq_id in the result set.
func (s *Storage) QueryMetricsBySeqID(ctx context.Context, startSeqID uint64, maxRecords int32, metricNames []string) ([]Metric, uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if maxRecords <= 0 {
		maxRecords = 10000
	} else if maxRecords > 50000 {
		maxRecords = 50000
	}

	query := `
		SELECT seq_id, timestamp, metric_name, value, unit, metric_type, CAST(attributes AS TEXT)
		FROM system_metrics_local
		WHERE seq_id > ?
	`

	args := []interface{}{startSeqID}

	if len(metricNames) > 0 {
		inPlaceholders := strings.Repeat("?,", len(metricNames))
		inPlaceholders = inPlaceholders[:len(inPlaceholders)-1]
		query += " AND metric_name IN (" + inPlaceholders + ")"
		for _, name := range metricNames {
			args = append(args, name)
		}
	}

	query += " ORDER BY seq_id ASC LIMIT ?"
	args = append(args, maxRecords)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query metrics by seq_id: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var metrics []Metric
	var maxSeqID uint64

	for rows.Next() {
		var m Metric
		var attributesJSON string

		err := rows.Scan(
			&m.SeqID,
			&m.Timestamp,
			&m.Name,
			&m.Value,
			&m.Unit,
			&m.MetricType,
			&attributesJSON,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan row: %w", err)
		}

		if attributesJSON != "" {
			if err := json.Unmarshal([]byte(attributesJSON), &m.Attributes); err != nil {
				s.logger.Warn().Err(err).Str("metric", m.Name).Msg("Failed to unmarshal attributes")
				m.Attributes = make(map[string]string)
			}
		} else {
			m.Attributes = make(map[string]string)
		}

		if m.SeqID > maxSeqID {
			maxSeqID = m.SeqID
		}

		metrics = append(metrics, m)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterating rows: %w", err)
	}

	return metrics, maxSeqID, nil
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
