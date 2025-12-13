package duckdb

import (
	"strings"
	"testing"
	"time"
)

func TestInterpolateQuery_Strings(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		args     []interface{}
		expected string
	}{
		{
			name:     "simple string",
			query:    "SELECT * FROM table WHERE name = ?",
			args:     []interface{}{"Alice"},
			expected: "SELECT * FROM table WHERE name = 'Alice'",
		},
		{
			name:     "string with single quote",
			query:    "SELECT * FROM table WHERE name = ?",
			args:     []interface{}{"O'Brien"},
			expected: "SELECT * FROM table WHERE name = 'O''Brien'",
		},
		{
			name:     "multiple strings",
			query:    "SELECT * FROM table WHERE first = ? AND last = ?",
			args:     []interface{}{"John", "Doe"},
			expected: "SELECT * FROM table WHERE first = 'John' AND last = 'Doe'",
		},
		{
			name:     "empty string",
			query:    "SELECT * FROM table WHERE name = ?",
			args:     []interface{}{""},
			expected: "SELECT * FROM table WHERE name = ''",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := InterpolateQuery(tt.query, tt.args)
			if result != tt.expected {
				t.Errorf("InterpolateQuery() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestInterpolateQuery_Numbers(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		args     []interface{}
		expected string
	}{
		{
			name:     "integer",
			query:    "SELECT * FROM table WHERE id = ?",
			args:     []interface{}{42},
			expected: "SELECT * FROM table WHERE id = 42",
		},
		{
			name:     "int64",
			query:    "SELECT * FROM table WHERE id = ?",
			args:     []interface{}{int64(123456789)},
			expected: "SELECT * FROM table WHERE id = 123456789",
		},
		{
			name:     "float64",
			query:    "SELECT * FROM table WHERE price = ?",
			args:     []interface{}{99.99},
			expected: "SELECT * FROM table WHERE price = 99.99",
		},
		{
			name:     "negative number",
			query:    "SELECT * FROM table WHERE balance = ?",
			args:     []interface{}{-100},
			expected: "SELECT * FROM table WHERE balance = -100",
		},
		{
			name:     "zero",
			query:    "SELECT * FROM table WHERE count = ?",
			args:     []interface{}{0},
			expected: "SELECT * FROM table WHERE count = 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := InterpolateQuery(tt.query, tt.args)
			if result != tt.expected {
				t.Errorf("InterpolateQuery() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestInterpolateQuery_Time(t *testing.T) {
	// Use a fixed timestamp for predictable testing.
	fixedTime := time.Date(2025, 12, 13, 15, 30, 45, 123456789, time.UTC)

	tests := []struct {
		name     string
		query    string
		args     []interface{}
		expected string
	}{
		{
			name:     "timestamp",
			query:    "SELECT * FROM table WHERE created_at = ?",
			args:     []interface{}{fixedTime},
			expected: "SELECT * FROM table WHERE created_at = '2025-12-13T15:30:45.123456789Z'",
		},
		{
			name:     "multiple timestamps",
			query:    "SELECT * FROM table WHERE start = ? AND end = ?",
			args:     []interface{}{fixedTime, fixedTime.Add(time.Hour)},
			expected: "SELECT * FROM table WHERE start = '2025-12-13T15:30:45.123456789Z' AND end = '2025-12-13T16:30:45.123456789Z'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := InterpolateQuery(tt.query, tt.args)
			if result != tt.expected {
				t.Errorf("InterpolateQuery() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestInterpolateQuery_NoMonotonicClock(t *testing.T) {
	// This test ensures that time.Time values don't include the monotonic clock
	// component (m=...) in the output.
	now := time.Now()
	query := "SELECT * FROM table WHERE timestamp = ?"
	result := InterpolateQuery(query, []interface{}{now})

	// The result should NOT contain the monotonic clock indicator.
	if strings.Contains(result, " m=") {
		t.Errorf("Expected no monotonic clock in output, got: %q", result)
	}

	// The result should contain a quoted timestamp.
	if !strings.Contains(result, "'") {
		t.Error("Expected timestamp to be quoted")
	}
}

func TestInterpolateQuery_Boolean(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		args     []interface{}
		expected string
	}{
		{
			name:     "true",
			query:    "SELECT * FROM table WHERE active = ?",
			args:     []interface{}{true},
			expected: "SELECT * FROM table WHERE active = true",
		},
		{
			name:     "false",
			query:    "SELECT * FROM table WHERE active = ?",
			args:     []interface{}{false},
			expected: "SELECT * FROM table WHERE active = false",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := InterpolateQuery(tt.query, tt.args)
			if result != tt.expected {
				t.Errorf("InterpolateQuery() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestInterpolateQuery_Null(t *testing.T) {
	query := "SELECT * FROM table WHERE optional = ?"
	result := InterpolateQuery(query, []interface{}{nil})
	expected := "SELECT * FROM table WHERE optional = NULL"

	if result != expected {
		t.Errorf("InterpolateQuery() = %q, expected %q", result, expected)
	}
}

func TestInterpolateQuery_Mixed(t *testing.T) {
	fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	query := "INSERT INTO users (name, age, balance, created_at, active) VALUES (?, ?, ?, ?, ?)"
	args := []interface{}{"Alice", 30, 1234.56, fixedTime, true}
	result := InterpolateQuery(query, args)

	expected := "INSERT INTO users (name, age, balance, created_at, active) VALUES ('Alice', 30, 1234.56, '2025-01-01T00:00:00Z', true)"

	if result != expected {
		t.Errorf("InterpolateQuery() = %q, expected %q", result, expected)
	}
}

func TestInterpolateQuery_WhitespaceHandling(t *testing.T) {
	query := "SELECT *\nFROM table\t\nWHERE id = ?"
	args := []interface{}{42}
	result := InterpolateQuery(query, args)

	// Newlines and tabs should be removed/replaced.
	if strings.Contains(result, "\n") {
		t.Error("Expected no newlines in result")
	}
	if strings.Contains(result, "\t") {
		t.Error("Expected no tabs in result")
	}

	// Should still have the interpolated value.
	if !strings.Contains(result, "42") {
		t.Error("Expected interpolated value '42' in result")
	}
}

func TestInterpolateQuery_NoArgs(t *testing.T) {
	query := "SELECT * FROM table"
	result := InterpolateQuery(query, []interface{}{})
	expected := "SELECT * FROM table"

	if result != expected {
		t.Errorf("InterpolateQuery() = %q, expected %q", result, expected)
	}
}

func TestInterpolateQuery_ValidSQL(t *testing.T) {
	// Test that output is valid SQL that could be copy-pasted.
	fixedTime := time.Date(2025, 12, 13, 10, 0, 0, 0, time.UTC)

	query := `SELECT bucket_time, agent_id, metric_name
		FROM system_metrics_summaries
		WHERE bucket_time >= ? AND bucket_time <= ?
		  AND agent_id = ?
		ORDER BY bucket_time DESC`

	args := []interface{}{
		fixedTime,
		fixedTime.Add(time.Hour),
		"agent-123",
	}

	result := InterpolateQuery(query, args)

	// Should not contain placeholders.
	if strings.Contains(result, "?") {
		t.Error("Expected no placeholders in result")
	}

	// Should contain properly formatted timestamps.
	if !strings.Contains(result, "2025-12-13T10:00:00Z") {
		t.Error("Expected properly formatted timestamp")
	}
	if !strings.Contains(result, "2025-12-13T11:00:00Z") {
		t.Error("Expected properly formatted timestamp for end time")
	}

	// Should contain quoted agent ID.
	if !strings.Contains(result, "'agent-123'") {
		t.Error("Expected quoted agent ID")
	}

	// Should not contain monotonic clock.
	if strings.Contains(result, " m=") {
		t.Error("Expected no monotonic clock in result")
	}
}
