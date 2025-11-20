package telemetry

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// Storage handles local storage of filtered telemetry spans.
// Spans are stored for ~1 hour and can be queried by colony on-demand (RFD 025).
type Storage struct {
	db     *sql.DB
	logger zerolog.Logger
	mu     sync.RWMutex
}

// NewStorage creates a new telemetry storage.
func NewStorage(db *sql.DB, logger zerolog.Logger) (*Storage, error) {
	s := &Storage{
		db:     db,
		logger: logger.With().Str("component", "telemetry_storage").Logger(),
	}

	// Initialize schema.
	if err := s.initSchema(); err != nil {
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return s, nil
}

// initSchema creates the local telemetry spans table.
func (s *Storage) initSchema() error {
	schema := `
		CREATE TABLE IF NOT EXISTS otel_spans_local (
			timestamp     TIMESTAMP NOT NULL,
			trace_id      TEXT NOT NULL,
			span_id       TEXT NOT NULL,
			service_name  TEXT NOT NULL,
			span_kind     TEXT,
			duration_ms   DOUBLE PRECISION,
			is_error      BOOLEAN DEFAULT false,
			http_status   INTEGER,
			http_method   TEXT,
			http_route    TEXT,
			attributes    JSON,
			created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (trace_id, span_id)
		);

		-- Index for time-range queries.
		CREATE INDEX IF NOT EXISTS idx_otel_spans_local_timestamp
		ON otel_spans_local(timestamp DESC);

		-- Index for service filtering.
		CREATE INDEX IF NOT EXISTS idx_otel_spans_local_service
		ON otel_spans_local(service_name, timestamp DESC);
	`

	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	s.logger.Info().Msg("Telemetry storage schema initialized")
	return nil
}

// StoreSpan stores a filtered span in local storage.
func (s *Storage) StoreSpan(ctx context.Context, span Span) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `
		INSERT INTO otel_spans_local (
			timestamp, trace_id, span_id, service_name, span_kind,
			duration_ms, is_error, http_status, http_method, http_route,
			attributes
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (trace_id, span_id) DO NOTHING
	`

	// Convert attributes to JSON.
	attributesJSON, err := json.Marshal(span.Attributes)
	if err != nil {
		return fmt.Errorf("failed to marshal attributes: %w", err)
	}
	if len(span.Attributes) == 0 {
		attributesJSON = []byte("{}")
	}

	_, err = s.db.ExecContext(
		ctx,
		query,
		span.Timestamp,
		span.TraceID,
		span.SpanID,
		span.ServiceName,
		span.SpanKind,
		span.DurationMs,
		span.IsError,
		span.HTTPStatus,
		span.HTTPMethod,
		span.HTTPRoute,
		attributesJSON,
	)

	if err != nil {
		return fmt.Errorf("failed to store span: %w", err)
	}

	return nil
}

// QuerySpans queries spans within a time range, optionally filtered by service names.
func (s *Storage) QuerySpans(ctx context.Context, startTime, endTime time.Time, serviceNames []string) ([]Span, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
		SELECT timestamp, trace_id, span_id, service_name, span_kind,
		       duration_ms, is_error, http_status, http_method, http_route, attributes
		FROM otel_spans_local
		WHERE timestamp >= ? AND timestamp < ?
	`

	args := []interface{}{startTime, endTime}

	// Add service name filter if provided.
	if len(serviceNames) > 0 {
		placeholders := ""
		for i, service := range serviceNames {
			if i > 0 {
				placeholders += ","
			}
			placeholders += "?"
			args = append(args, service)
		}
		query += " AND service_name IN (" + placeholders + ")"
	}

	query += " ORDER BY timestamp DESC LIMIT 10000"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query spans: %w", err)
	}
	defer func() { _ = rows.Close() }() // TODO: errcheck

	spans := make([]Span, 0)

	for rows.Next() {
		var span Span
		var httpStatus sql.NullInt32
		var httpMethod sql.NullString
		var httpRoute sql.NullString
		var attributesJSON []byte

		err := rows.Scan(
			&span.Timestamp,
			&span.TraceID,
			&span.SpanID,
			&span.ServiceName,
			&span.SpanKind,
			&span.DurationMs,
			&span.IsError,
			&httpStatus,
			&httpMethod,
			&httpRoute,
			&attributesJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan span: %w", err)
		}

		if httpStatus.Valid {
			span.HTTPStatus = int(httpStatus.Int32)
		}
		if httpMethod.Valid {
			span.HTTPMethod = httpMethod.String
		}
		if httpRoute.Valid {
			span.HTTPRoute = httpRoute.String
		}

		// Unmarshal attributes.
		if len(attributesJSON) > 0 {
			if err := json.Unmarshal(attributesJSON, &span.Attributes); err != nil {
				return nil, fmt.Errorf("failed to unmarshal attributes: %w", err)
			}
		}

		spans = append(spans, span)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating spans: %w", err)
	}

	s.logger.Debug().
		Int("span_count", len(spans)).
		Time("start_time", startTime).
		Time("end_time", endTime).
		Msg("Queried telemetry spans")

	return spans, nil
}

// CleanupOldSpans removes spans older than the retention period (~1 hour).
func (s *Storage) CleanupOldSpans(ctx context.Context, retentionPeriod time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-retentionPeriod)

	query := `DELETE FROM otel_spans_local WHERE timestamp < ?`

	result, err := s.db.ExecContext(ctx, query, cutoff)
	if err != nil {
		return fmt.Errorf("failed to cleanup old spans: %w", err)
	}

	rowsDeleted, _ := result.RowsAffected()

	if rowsDeleted > 0 {
		s.logger.Debug().
			Int64("rows_deleted", rowsDeleted).
			Time("cutoff", cutoff).
			Msg("Cleaned up old telemetry spans")
	}

	return nil
}

// RunCleanupLoop runs a periodic cleanup goroutine.
func (s *Storage) RunCleanupLoop(ctx context.Context, retentionPeriod time.Duration) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	s.logger.Info().
		Dur("retention_period", retentionPeriod).
		Msg("Starting telemetry cleanup loop")

	for {
		select {
		case <-ticker.C:
			if err := s.CleanupOldSpans(ctx, retentionPeriod); err != nil {
				s.logger.Error().
					Err(err).
					Msg("Failed to cleanup old telemetry spans")
			}

		case <-ctx.Done():
			s.logger.Info().Msg("Stopping telemetry cleanup loop")
			return
		}
	}
}

// GetSpanCount returns the current number of spans in local storage (for monitoring).
func (s *Storage) GetSpanCount(ctx context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int
	query := `SELECT COUNT(*) FROM otel_spans_local`

	err := s.db.QueryRowContext(ctx, query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to get span count: %w", err)
	}

	return count, nil
}
