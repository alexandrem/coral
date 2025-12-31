// Package query provides a fluent SQL query builder for DuckDB SELECT queries.
//
// This package eliminates duplicate query construction patterns across database
// files by providing a chainable API for building SQL queries with common
// patterns like time range filtering, equality filters, aggregation, ordering,
// and pagination.
//
// The builder focuses on SQL generation only and does not execute queries.
// Use the generated SQL and arguments with the Database struct's QueryContext
// or ExecContext methods.
//
// Example usage:
//
//	// Simple query with time range and service filter
//	q, args, err := query.New("beyla_http_metrics").
//		Select("*").
//		TimeRange(startTime, endTime).
//		Eq("service_name", "my-service").
//		OrderBy("-timestamp").
//		Limit(100).
//		Build()
//
//	rows, err := db.QueryContext(ctx, q, args...)
//
// The builder supports flexible time column names (timestamp, bucket_time,
// start_time) via the TimeColumn() method, and automatically skips empty
// string filters for wildcard behavior.
package query
