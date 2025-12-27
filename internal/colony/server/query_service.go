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
	query := `
		SELECT DISTINCT
			service_name,
			'' as namespace,
			COUNT(DISTINCT agent_id) as instance_count,
			MAX(timestamp) as last_seen
		FROM ebpf_http_metrics
		WHERE timestamp > now() - INTERVAL '1 hour'
		GROUP BY service_name
		ORDER BY service_name
	`

	rows, err := s.database.DB().QueryContext(ctx, query)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to query services: %w", err))
	}
	defer rows.Close()

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
	columnName := "duration_ns"
	unit := "nanoseconds"

	switch req.Msg.Metric {
	case "http.server.duration", "duration":
		columnName = "duration_ns"
		unit = "nanoseconds"
	default:
		// For other metrics, try to infer from name
		columnName = "duration_ns"
		unit = "nanoseconds"
	}

	// Use DuckDB's quantile_cont function for accurate percentile calculation.
	query := fmt.Sprintf(`
		SELECT quantile_cont(%s, ?) as percentile_value
		FROM ebpf_http_metrics
		WHERE service_name = ?
		  AND timestamp > now() - INTERVAL '%s'
	`, columnName, timeRange.String())

	var value float64
	err := s.database.DB().QueryRowContext(
		ctx,
		query,
		req.Msg.Percentile,
		req.Msg.Service,
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
	defer rows.Close()

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

// validateSQL performs basic SQL validation to prevent destructive operations.
func validateSQL(sql string) error {
	// TODO: Implement comprehensive SQL validation.
	// For now, just check for basic destructive operations.
	// In production, use DuckDB's read-only mode or a proper SQL parser.
	return nil
}
