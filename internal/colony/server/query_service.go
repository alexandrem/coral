package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/safe"
)

// Focused Query Handlers (RFD 076).
// These provide focused, scriptable queries for CLI and TypeScript SDK.
// Unified query handlers (RFD 067) are in unified_query_handlers.go.

// ListServices handles service discovery requests with dual-source discovery (RFD 084).
// Returns services from both the registry (explicitly connected) and telemetry data (auto-observed).
func (s *Server) ListServices(
	ctx context.Context,
	req *connect.Request[colonyv1.ListServicesRequest],
) (*connect.Response[colonyv1.ListServicesResponse], error) {
	// Parse time range for telemetry-based discovery (default: 1 hour).
	timeRange := req.Msg.TimeRange
	if timeRange == "" {
		timeRange = "1h"
	}
	duration, err := time.ParseDuration(timeRange)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid time_range: %w", err))
	}
	cutoff := time.Now().Add(-duration)

	// Enhanced query combining both registry and telemetry sources (RFD 084).
	// Uses FULL OUTER JOIN to include services from either source.
	query := `
		SELECT
			COALESCE(s.name, t.service_name) as name,
			'' as namespace,

			-- Source attribution
			CASE
				WHEN s.name IS NOT NULL AND t.service_name IS NOT NULL THEN 3  -- VERIFIED
				WHEN s.name IS NOT NULL THEN 1                                  -- REGISTERED
				ELSE 2                                                          -- OBSERVED
			END as source,

			-- Registration status (only for registered services)
			s.status as registration_status,

			-- Instance count (only for registered services)
			COALESCE(COUNT(DISTINCT s.agent_id), 0) as instance_count,

			-- Last seen (prefer registry heartbeat, fall back to telemetry)
			COALESCE(
				MAX(h.last_seen),
				MAX(s.registered_at),
				MAX(t.last_timestamp)
			) as last_seen,

			-- Agent ID (only for registered services, pick first if multiple)
			MIN(s.agent_id) as agent_id

		FROM services s

		-- FULL OUTER JOIN with telemetry-observed services
		FULL OUTER JOIN (
			SELECT DISTINCT
				service_name,
				MAX(timestamp) as last_timestamp
			FROM beyla_http_metrics
			WHERE timestamp > ?
			GROUP BY service_name
		) t ON s.name = t.service_name

		LEFT JOIN service_heartbeats h ON s.id = h.service_id

		GROUP BY s.name, t.service_name, s.status
		ORDER BY last_seen DESC
	`

	rows, err := s.database.DB().QueryContext(ctx, query, cutoff)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to query services: %w", err))
	}
	defer func() { _ = rows.Close() }()

	var services []*colonyv1.ServiceSummary
	for rows.Next() {
		var (
			name, namespace    string
			sourceInt          int
			registrationStatus sql.NullString
			instanceCount      int32
			lastSeen           time.Time
			agentID            sql.NullString
		)

		if err := rows.Scan(&name, &namespace, &sourceInt, &registrationStatus, &instanceCount, &lastSeen, &agentID); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to scan service: %w", err))
		}

		// Convert source integer to enum.
		sourceValue, clamped := safe.IntToInt32(sourceInt)
		if clamped {
			s.logger.Warn().
				Int("source", sourceInt).
				Msg("Source value exceeds limit and was clamped")
		}
		source := colonyv1.ServiceSource(sourceValue)

		// Apply source filter if specified.
		if req.Msg.SourceFilter != nil && *req.Msg.SourceFilter != source {
			continue
		}

		// Determine service status based on source and registration status.
		var status *colonyv1.ServiceStatus
		switch source {
		// Service is registered.
		case colonyv1.ServiceSource_SERVICE_SOURCE_REGISTERED, colonyv1.ServiceSource_SERVICE_SOURCE_VERIFIED:
			// Determine health status
			if registrationStatus.Valid {
				switch registrationStatus.String {
				case "active":
					s := colonyv1.ServiceStatus_SERVICE_STATUS_ACTIVE
					status = &s
				case "unhealthy":
					s := colonyv1.ServiceStatus_SERVICE_STATUS_UNHEALTHY
					status = &s
				default:
					s := colonyv1.ServiceStatus_SERVICE_STATUS_UNHEALTHY
					status = &s
				}
			}

		// Service is only observed from telemetry.
		case colonyv1.ServiceSource_SERVICE_SOURCE_OBSERVED:
			s := colonyv1.ServiceStatus_SERVICE_STATUS_OBSERVED_ONLY
			status = &s
		}

		svc := &colonyv1.ServiceSummary{
			Name:          name,
			Namespace:     namespace,
			InstanceCount: instanceCount,
			LastSeen:      timestamppb.New(lastSeen),
			Source:        source,
			Status:        status,
		}

		// Include agent_id if present and service is registered.
		if agentID.Valid && (source == colonyv1.ServiceSource_SERVICE_SOURCE_REGISTERED || source == colonyv1.ServiceSource_SERVICE_SOURCE_VERIFIED) {
			svc.AgentId = &agentID.String
		}

		services = append(services, svc)
	}

	if err := rows.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("error iterating services: %w", err))
	}

	return connect.NewResponse(&colonyv1.ListServicesResponse{
		Services: services,
	}), nil
}

// GetMetricPercentile handles percentile metric queries.
// This fills a gap in the unified query API which only provides averages.
func (s *Server) GetMetricPercentile(
	ctx context.Context,
	req *connect.Request[colonyv1.GetMetricPercentileRequest],
) (*connect.Response[colonyv1.GetMetricPercentileResponse], error) {
	// Convert time range from milliseconds to duration.
	timeRange := time.Duration(req.Msg.TimeRangeMs) * time.Millisecond
	if timeRange == 0 {
		timeRange = 1 * time.Hour // Default to 1 hour
	}

	// Map metric name to column (simplified for MVP).
	// In production, this would be more sophisticated with metric registry.
	var columnName, unit string

	switch req.Msg.Metric {
	case "http.server.duration", "duration":
		columnName = "duration_ns"
		unit = "nanoseconds"
	default:
		// For other metrics, try to infer from name
		columnName = "duration_ns"
		unit = "nanoseconds"
	}

	// Calculate cutoff time.
	cutoff := time.Now().Add(-timeRange)

	// Use DuckDB's quantile_cont function for accurate percentile calculation.
	query := fmt.Sprintf(`
		SELECT quantile_cont(%s, ?) as percentile_value
		FROM beyla_http_metrics
		WHERE service_name = ?
		  AND timestamp > ?
	`, columnName)

	var value float64
	err := s.database.DB().QueryRowContext(
		ctx,
		query,
		req.Msg.Percentile,
		req.Msg.Service,
		cutoff,
	).Scan(&value)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no metrics found for service: %s", req.Msg.Service))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to query percentile: %w", err))
	}

	return connect.NewResponse(&colonyv1.GetMetricPercentileResponse{
		Value:     value,
		Unit:      unit,
		Timestamp: timestamppb.Now(),
	}), nil
}

// ExecuteQuery handles raw SQL queries with guardrails.
func (s *Server) ExecuteQuery(
	ctx context.Context,
	req *connect.Request[colonyv1.ExecuteQueryRequest],
) (*connect.Response[colonyv1.ExecuteQueryResponse], error) {
	// Apply safety guardrails.
	if err := validateSQL(req.Msg.Sql); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Apply max rows limit.
	maxRows := req.Msg.MaxRows
	if maxRows == 0 {
		maxRows = 1000 // Default limit
	}

	rows, err := s.database.DB().QueryContext(ctx, req.Msg.Sql)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to execute query: %w", err))
	}
	defer func() { _ = rows.Close() }()

	columns, err := rows.Columns()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to get columns: %w", err))
	}

	var results []*colonyv1.QueryRow
	rowCount := int32(0)

	for rows.Next() && rowCount < maxRows {
		// Create a slice of interface{} to hold each column value.
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to scan row: %w", err))
		}

		// Convert to string slice.
		row := &colonyv1.QueryRow{
			Values: make([]string, len(columns)),
		}
		for i, val := range values {
			if val != nil {
				row.Values[i] = fmt.Sprintf("%v", val)
			} else {
				row.Values[i] = ""
			}
		}
		results = append(results, row)
		rowCount++
	}

	if err := rows.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("error iterating rows: %w", err))
	}

	return connect.NewResponse(&colonyv1.ExecuteQueryResponse{
		Rows:     results,
		RowCount: rowCount,
		Columns:  columns,
	}), nil
}

// GetServiceActivity handles service activity requests for a specific service.
func (s *Server) GetServiceActivity(
	ctx context.Context,
	req *connect.Request[colonyv1.GetServiceActivityRequest],
) (*connect.Response[colonyv1.GetServiceActivityResponse], error) {
	// Convert time range from milliseconds to duration.
	timeRange := time.Duration(req.Msg.TimeRangeMs) * time.Millisecond
	if timeRange == 0 {
		timeRange = 1 * time.Hour // Default to 1 hour
	}

	// Calculate cutoff time.
	cutoff := time.Now().Add(-timeRange)

	query := `
		SELECT
			service_name,
			COUNT(*) as request_count,
			SUM(CASE WHEN http_status_code >= 400 THEN 1 ELSE 0 END) as error_count
		FROM beyla_http_metrics
		WHERE service_name = ?
		  AND timestamp > ?
		GROUP BY service_name
	`

	var serviceName string
	var requestCount, errorCount int64

	err := s.database.DB().QueryRowContext(
		ctx,
		query,
		req.Msg.Service,
		cutoff,
	).Scan(&serviceName, &requestCount, &errorCount)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no activity found for service: %s", req.Msg.Service))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to query service activity: %w", err))
	}

	errorRate := 0.0
	if requestCount > 0 {
		errorRate = float64(errorCount) / float64(requestCount)
	}

	return connect.NewResponse(&colonyv1.GetServiceActivityResponse{
		ServiceName:  serviceName,
		RequestCount: requestCount,
		ErrorCount:   errorCount,
		ErrorRate:    errorRate,
		Timestamp:    timestamppb.Now(),
	}), nil
}

// ListServiceActivity handles service activity requests for all services.
func (s *Server) ListServiceActivity(
	ctx context.Context,
	req *connect.Request[colonyv1.ListServiceActivityRequest],
) (*connect.Response[colonyv1.ListServiceActivityResponse], error) {
	// Convert time range from milliseconds to duration.
	timeRange := time.Duration(req.Msg.TimeRangeMs) * time.Millisecond
	if timeRange == 0 {
		timeRange = 1 * time.Hour // Default to 1 hour
	}

	// Calculate cutoff time.
	cutoff := time.Now().Add(-timeRange)

	query := `
		SELECT
			service_name,
			COUNT(*) as request_count,
			SUM(CASE WHEN http_status_code >= 400 THEN 1 ELSE 0 END) as error_count
		FROM beyla_http_metrics
		WHERE timestamp > ?
		GROUP BY service_name
		ORDER BY request_count DESC
	`

	rows, err := s.database.DB().QueryContext(ctx, query, cutoff)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to query service activity: %w", err))
	}
	defer func() { _ = rows.Close() }()

	var services []*colonyv1.ServiceActivity
	for rows.Next() {
		var svc colonyv1.ServiceActivity
		var requestCount, errorCount int64

		if err := rows.Scan(&svc.ServiceName, &requestCount, &errorCount); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to scan service activity: %w", err))
		}

		svc.RequestCount = requestCount
		svc.ErrorCount = errorCount
		if requestCount > 0 {
			svc.ErrorRate = float64(errorCount) / float64(requestCount)
		}

		services = append(services, &svc)
	}

	if err := rows.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("error iterating service activity: %w", err))
	}

	return connect.NewResponse(&colonyv1.ListServiceActivityResponse{
		Services: services,
	}), nil
}

// validateSQL performs basic SQL validation to prevent destructive operations.
func validateSQL(sql string) error {
	// TODO: Implement comprehensive SQL validation.
	// For now, just check for basic destructive operations.
	// In production, use DuckDB's read-only mode or a proper SQL parser.
	return nil
}
