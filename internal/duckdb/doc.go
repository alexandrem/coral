// Package duckdb provides DuckDB database utilities including ORM and query builder.
//
// # ORM
//
// The Table type provides an ORM for DuckDB tables with automatic schema
// generation and batch upsert operations:
//
//	type User struct {
//	    ID   string `duckdb:"id,pk"`
//	    Name string `duckdb:"name"`
//	}
//
//	table := duckdb.NewTable[User](db, "users")
//	err := table.BatchUpsert(ctx, []*User{...})
//
// # Query Builder
//
// The query builder provides a fluent API for constructing SELECT queries with
// common patterns like time range filtering, equality filters, aggregation,
// ordering, and pagination:
//
//	sql, args, err := duckdb.NewQueryBuilder("users").
//	    Select("id", "name", "created_at").
//	    TimeRange(startTime, endTime).
//	    Eq("status", "active").
//	    OrderBy("-created_at").
//	    Limit(100).
//	    Build()
//
//	rows, err := db.QueryContext(ctx, sql, args...)
//
// The builder focuses on SQL generation only and does not execute queries.
// It supports flexible time column names (timestamp, bucket_time, start_time)
// via the TimeColumn() method, and automatically skips empty string filters
// for wildcard behavior.
package duckdb
