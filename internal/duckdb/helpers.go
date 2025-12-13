package duckdb

import (
	"fmt"
	"strings"
	"time"
)

// InterpolateQuery returns a formatted query for logging.
// The output is valid SQL that can be copy-pasted into DuckDB.
func InterpolateQuery(query string, args []interface{}) string {
	// A simple, naive substitution for logging.
	for _, arg := range args {
		var replacement string
		switch v := arg.(type) {
		case string:
			// Wrap strings in single quotes, escape internal quotes.
			escaped := strings.ReplaceAll(v, "'", "''")
			replacement = fmt.Sprintf("'%s'", escaped)
		case int, int8, int16, int32, int64:
			// Numbers can be substituted directly.
			replacement = fmt.Sprintf("%d", v)
		case uint, uint8, uint16, uint32, uint64:
			// Unsigned numbers.
			replacement = fmt.Sprintf("%d", v)
		case float32, float64:
			// Floating point numbers.
			replacement = fmt.Sprintf("%v", v)
		case bool:
			// Booleans.
			if v {
				replacement = "true"
			} else {
				replacement = "false"
			}
		case time.Time:
			// Format timestamps without monotonic clock for valid SQL.
			// Use RFC3339Nano for maximum precision.
			replacement = fmt.Sprintf("'%s'", v.Format(time.RFC3339Nano))
		case nil:
			replacement = "NULL"
		default:
			// Fallback for other types - quote as string.
			replacement = fmt.Sprintf("'%v'", v)
		}

		// Replace the first '?'.
		query = strings.Replace(query, "?", replacement, 1)
	}

	query = strings.ReplaceAll(query, "\t", " ")
	query = strings.ReplaceAll(query, "\n", "")

	return query
}
