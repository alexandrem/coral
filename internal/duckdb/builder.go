package duckdb

import (
	"fmt"
	"strings"
	"time"
)

// Builder constructs SELECT queries with a fluent API.
type Builder struct {
	table      string
	columns    []string
	where      []whereClause
	groupBy    []string
	orderBy    []orderClause
	limit      int
	args       []interface{}
	timeColumn string // Configurable: timestamp, bucket_time, start_time
}

// whereClause represents a WHERE condition.
type whereClause struct {
	expr string
	args []interface{}
}

// orderClause represents an ORDER BY clause.
type orderClause struct {
	column string
	desc   bool
}

// NewQueryBuilder creates a new query builder for the specified table.
func NewQueryBuilder(table string) *Builder {
	return &Builder{
		table:      table,
		timeColumn: "timestamp", // default
		args:       make([]interface{}, 0),
	}
}

// Select specifies the columns to retrieve.
// Supports column names, aggregates, and aliases.
// Examples:
//
//	Select("name", "age")
//	Select("SUM(count) as total_count", "MIN(timestamp) as first_seen")
func (b *Builder) Select(columns ...string) *Builder {
	b.columns = append(b.columns, columns...)
	return b
}

// TimeColumn sets the name of the time column for time range filtering.
// Default is "timestamp". Use this before calling TimeRange().
func (b *Builder) TimeColumn(name string) *Builder {
	b.timeColumn = name
	return b
}

// TimeRange adds a time range filter using the configured time column.
// Generates: WHERE <timeColumn> >= ? AND <timeColumn> <= ?
func (b *Builder) TimeRange(start, end time.Time) *Builder {
	b.where = append(b.where, whereClause{
		expr: fmt.Sprintf("%s >= ? AND %s <= ?", b.timeColumn, b.timeColumn),
		args: []interface{}{start, end},
	})
	return b
}

// Where adds a custom WHERE clause with optional arguments.
// Multiple Where() calls are combined with AND.
// Examples:
//
//	Where("service_name = ?", "my-service")
//	Where("http_status_code BETWEEN ? AND ?", 200, 299)
//	Where("name IS NOT NULL")
func (b *Builder) Where(expr string, args ...interface{}) *Builder {
	b.where = append(b.where, whereClause{
		expr: expr,
		args: args,
	})
	return b
}

// Eq adds an equality filter.
// Generates: WHERE column = ?
// If value is empty string, the filter is skipped (wildcard behavior).
func (b *Builder) Eq(column string, value interface{}) *Builder {
	// Skip empty strings for wildcard behavior.
	if str, ok := value.(string); ok && str == "" {
		return b
	}
	return b.Where(fmt.Sprintf("%s = ?", column), value)
}

// In adds an IN clause.
// Generates: WHERE column IN (?, ?, ...)
// If values is empty, the filter is skipped.
func (b *Builder) In(column string, values ...interface{}) *Builder {
	if len(values) == 0 {
		return b
	}
	placeholders := make([]string, len(values))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	expr := fmt.Sprintf("%s IN (%s)", column, strings.Join(placeholders, ", "))
	return b.Where(expr, values...)
}

// Between adds a BETWEEN clause.
// Generates: WHERE column BETWEEN ? AND ?
func (b *Builder) Between(column string, min, max interface{}) *Builder {
	return b.Where(fmt.Sprintf("%s BETWEEN ? AND ?", column), min, max)
}

// Gte adds a >= comparison.
// Generates: WHERE column >= ?
func (b *Builder) Gte(column string, value interface{}) *Builder {
	return b.Where(fmt.Sprintf("%s >= ?", column), value)
}

// Gt adds a > comparison.
// Generates: WHERE column > ?
func (b *Builder) Gt(column string, value interface{}) *Builder {
	return b.Where(fmt.Sprintf("%s > ?", column), value)
}

// Lte adds a <= comparison.
// Generates: WHERE column <= ?
func (b *Builder) Lte(column string, value interface{}) *Builder {
	return b.Where(fmt.Sprintf("%s <= ?", column), value)
}

// Lt adds a < comparison.
// Generates: WHERE column < ?
func (b *Builder) Lt(column string, value interface{}) *Builder {
	return b.Where(fmt.Sprintf("%s < ?", column), value)
}

// GroupBy adds GROUP BY columns.
func (b *Builder) GroupBy(columns ...string) *Builder {
	b.groupBy = append(b.groupBy, columns...)
	return b
}

// OrderBy adds ORDER BY clauses.
// Use "-" prefix for DESC order.
// Examples:
//
//	OrderBy("created_at")        // ASC
//	OrderBy("-created_at")       // DESC
//	OrderBy("name", "-created_at") // name ASC, created_at DESC
func (b *Builder) OrderBy(columns ...string) *Builder {
	for _, col := range columns {
		desc := false
		if strings.HasPrefix(col, "-") {
			desc = true
			col = col[1:]
		}
		b.orderBy = append(b.orderBy, orderClause{
			column: col,
			desc:   desc,
		})
	}
	return b
}

// Limit sets the maximum number of rows to return.
func (b *Builder) Limit(n int) *Builder {
	b.limit = n
	return b
}

// Build constructs the SQL query and returns the query string and arguments.
// Returns (query, args, error).
func (b *Builder) Build() (string, []interface{}, error) {
	if b.table == "" {
		return "", nil, fmt.Errorf("table name is required")
	}

	var query strings.Builder

	// SELECT clause.
	query.WriteString("SELECT ")
	if len(b.columns) == 0 {
		query.WriteString("*")
	} else {
		query.WriteString(strings.Join(b.columns, ", "))
	}

	// FROM clause.
	query.WriteString(" FROM ")
	query.WriteString(b.table)

	// WHERE clause.
	if len(b.where) > 0 {
		query.WriteString(" WHERE ")
		exprs := make([]string, len(b.where))
		for i, w := range b.where {
			exprs[i] = w.expr
			b.args = append(b.args, w.args...)
		}
		query.WriteString(strings.Join(exprs, " AND "))
	}

	// GROUP BY clause.
	if len(b.groupBy) > 0 {
		query.WriteString(" GROUP BY ")
		query.WriteString(strings.Join(b.groupBy, ", "))
	}

	// ORDER BY clause.
	if len(b.orderBy) > 0 {
		query.WriteString(" ORDER BY ")
		orderParts := make([]string, len(b.orderBy))
		for i, o := range b.orderBy {
			if o.desc {
				orderParts[i] = o.column + " DESC"
			} else {
				orderParts[i] = o.column
			}
		}
		query.WriteString(strings.Join(orderParts, ", "))
	}

	// LIMIT clause.
	if b.limit > 0 {
		query.WriteString(" LIMIT ?")
		b.args = append(b.args, b.limit)
	}

	return query.String(), b.args, nil
}

// MustBuild builds the query and panics on error.
// Useful for tests and cases where query construction should never fail.
func (b *Builder) MustBuild() (string, []interface{}) {
	q, args, err := b.Build()
	if err != nil {
		panic(err)
	}
	return q, args
}
