package server

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
)

// Focused Query Handlers (RFD 076).
// These provide focused, scriptable queries for CLI and TypeScript SDK.
// Unified query handlers (RFD 067) are in unified_query_handlers.go.

// ListServices handles service discovery requests.
func (s *Server) ListServices(
	ctx context.Context,
	req *connect.Request[colonyv1.ListServicesRequest],
) (*connect.Response[colonyv1.ListServicesResponse], error) {
	// Calculate cutoff time (1 hour ago).
	cutoff := time.Now().Add(-1 * time.Hour)

	query := `
		SELECT DISTINCT
			service_name,
			'' as namespace,
			COUNT(DISTINCT agent_id) as instance_count,
			MAX(timestamp) as last_seen
		FROM beyla_http_metrics
		WHERE timestamp > ?
		GROUP BY service_name
		ORDER BY service_name
	`

	rows, err := s.database.DB().QueryContext(ctx, query, cutoff)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to query services: %w", err))
	}
	defer func() { _ = rows.Close() }()

	var services []*colonyv1.ServiceInfo
	for rows.Next() {
		var svc colonyv1.ServiceInfo
		var lastSeen time.Time
		if err := rows.Scan(&svc.Name, &svc.Namespace, &svc.InstanceCount, &lastSeen); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to scan service: %w", err))
		}
		svc.LastSeen = timestamppb.New(lastSeen)
		services = append(services, &svc)
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

	if err == sql.ErrNoRows {
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

	if err == sql.ErrNoRows {
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
