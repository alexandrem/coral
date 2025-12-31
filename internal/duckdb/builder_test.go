package duckdb

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuilder_SimpleSelect(t *testing.T) {
	q, args, err := NewQueryBuilder("test_table").Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM test_table", q)
	assert.Empty(t, args)
}

func TestBuilder_SelectColumns(t *testing.T) {
	q, args, err := NewQueryBuilder("test_table").
		Select("id", "name", "created_at").
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT id, name, created_at FROM test_table", q)
	assert.Empty(t, args)
}

func TestBuilder_SelectAggregations(t *testing.T) {
	q, args, err := NewQueryBuilder("metrics").
		Select("service_name", "SUM(count) as total_count", "MIN(timestamp) as first_seen").
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT service_name, SUM(count) as total_count, MIN(timestamp) as first_seen FROM metrics", q)
	assert.Empty(t, args)
}

func TestBuilder_TimeRange(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	q, args, err := NewQueryBuilder("test_table").
		TimeRange(start, end).
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM test_table WHERE timestamp >= ? AND timestamp <= ?", q)
	assert.Equal(t, []interface{}{start, end}, args)
}

func TestBuilder_CustomTimeColumn(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	q, args, err := NewQueryBuilder("test_table").
		TimeColumn("bucket_time").
		TimeRange(start, end).
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM test_table WHERE bucket_time >= ? AND bucket_time <= ?", q)
	assert.Equal(t, []interface{}{start, end}, args)
}

func TestBuilder_StartTimeColumn(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	q, args, err := NewQueryBuilder("beyla_traces").
		TimeColumn("start_time").
		TimeRange(start, end).
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM beyla_traces WHERE start_time >= ? AND start_time <= ?", q)
	assert.Equal(t, []interface{}{start, end}, args)
}

func TestBuilder_Eq(t *testing.T) {
	q, args, err := NewQueryBuilder("test_table").
		Eq("service_name", "my-service").
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM test_table WHERE service_name = ?", q)
	assert.Equal(t, []interface{}{"my-service"}, args)
}

func TestBuilder_EqWithEmptyString(t *testing.T) {
	q, args, err := NewQueryBuilder("test_table").
		Eq("agent_id", "").
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM test_table", q)
	assert.Empty(t, args)
}

func TestBuilder_MultipleEq(t *testing.T) {
	q, args, err := NewQueryBuilder("test_table").
		Eq("service_name", "my-service").
		Eq("http_method", "GET").
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM test_table WHERE service_name = ? AND http_method = ?", q)
	assert.Equal(t, []interface{}{"my-service", "GET"}, args)
}

func TestBuilder_In(t *testing.T) {
	q, args, err := NewQueryBuilder("test_table").
		In("service_name", "svc1", "svc2", "svc3").
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM test_table WHERE service_name IN (?, ?, ?)", q)
	assert.Equal(t, []interface{}{"svc1", "svc2", "svc3"}, args)
}

func TestBuilder_InWithEmptyValues(t *testing.T) {
	q, args, err := NewQueryBuilder("test_table").
		In("service_name").
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM test_table", q)
	assert.Empty(t, args)
}

func TestBuilder_Between(t *testing.T) {
	q, args, err := NewQueryBuilder("test_table").
		Between("http_status_code", 200, 299).
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM test_table WHERE http_status_code BETWEEN ? AND ?", q)
	assert.Equal(t, []interface{}{200, 299}, args)
}

func TestBuilder_Gte(t *testing.T) {
	q, args, err := NewQueryBuilder("test_table").
		Gte("duration_us", 1000).
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM test_table WHERE duration_us >= ?", q)
	assert.Equal(t, []interface{}{1000}, args)
}

func TestBuilder_Gt(t *testing.T) {
	q, args, err := NewQueryBuilder("test_table").
		Gt("age", 18).
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM test_table WHERE age > ?", q)
	assert.Equal(t, []interface{}{18}, args)
}

func TestBuilder_Lte(t *testing.T) {
	q, args, err := NewQueryBuilder("test_table").
		Lte("price", 100).
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM test_table WHERE price <= ?", q)
	assert.Equal(t, []interface{}{100}, args)
}

func TestBuilder_Lt(t *testing.T) {
	cutoff := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	q, args, err := NewQueryBuilder("test_table").
		Lt("timestamp", cutoff).
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM test_table WHERE timestamp < ?", q)
	assert.Equal(t, []interface{}{cutoff}, args)
}

func TestBuilder_Where(t *testing.T) {
	q, args, err := NewQueryBuilder("test_table").
		Where("name IS NOT NULL").
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM test_table WHERE name IS NOT NULL", q)
	assert.Empty(t, args)
}

func TestBuilder_WhereWithArgs(t *testing.T) {
	q, args, err := NewQueryBuilder("test_table").
		Where("http_status_code BETWEEN ? AND ?", 200, 299).
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM test_table WHERE http_status_code BETWEEN ? AND ?", q)
	assert.Equal(t, []interface{}{200, 299}, args)
}

func TestBuilder_MultipleWhere(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	q, args, err := NewQueryBuilder("test_table").
		TimeRange(start, end).
		Eq("service_name", "my-service").
		Where("duration_us >= ?", 1000).
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM test_table WHERE timestamp >= ? AND timestamp <= ? AND service_name = ? AND duration_us >= ?", q)
	assert.Equal(t, []interface{}{start, end, "my-service", 1000}, args)
}

func TestBuilder_GroupBy(t *testing.T) {
	q, args, err := NewQueryBuilder("metrics").
		Select("service_name", "SUM(count) as total").
		GroupBy("service_name").
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT service_name, SUM(count) as total FROM metrics GROUP BY service_name", q)
	assert.Empty(t, args)
}

func TestBuilder_GroupByMultiple(t *testing.T) {
	q, _, err := NewQueryBuilder("metrics").
		Select("service_name", "http_method", "SUM(count) as total").
		GroupBy("service_name", "http_method").
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT service_name, http_method, SUM(count) as total FROM metrics GROUP BY service_name, http_method", q)
}

func TestBuilder_OrderBy(t *testing.T) {
	q, args, err := NewQueryBuilder("test_table").
		OrderBy("created_at").
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM test_table ORDER BY created_at", q)
	assert.Empty(t, args)
}

func TestBuilder_OrderByDesc(t *testing.T) {
	q, args, err := NewQueryBuilder("test_table").
		OrderBy("-timestamp").
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM test_table ORDER BY timestamp DESC", q)
	assert.Empty(t, args)
}

func TestBuilder_OrderByMultiple(t *testing.T) {
	q, _, err := NewQueryBuilder("test_table").
		OrderBy("http_route", "latency_bucket_ms").
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM test_table ORDER BY http_route, latency_bucket_ms", q)
}

func TestBuilder_OrderByMixed(t *testing.T) {
	q, _, err := NewQueryBuilder("test_table").
		OrderBy("name", "-created_at").
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM test_table ORDER BY name, created_at DESC", q)
}

func TestBuilder_Limit(t *testing.T) {
	q, args, err := NewQueryBuilder("test_table").
		Limit(100).
		Build()

	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM test_table LIMIT ?", q)
	assert.Equal(t, []interface{}{100}, args)
}

func TestBuilder_ComplexBeylaHTTPQuery(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	q, args, err := NewQueryBuilder("beyla_http_metrics").
		Select(
			"service_name",
			"http_method",
			"http_route",
			"http_status_code",
			"latency_bucket_ms",
			"SUM(count) as total_count",
			"MIN(timestamp) as first_seen",
			"MAX(timestamp) as last_seen",
		).
		TimeRange(start, end).
		Eq("service_name", "my-service").
		Eq("http_method", "GET").
		Between("http_status_code", 200, 299).
		GroupBy("service_name", "http_method", "http_route", "http_status_code", "latency_bucket_ms").
		OrderBy("http_route", "latency_bucket_ms").
		Build()

	require.NoError(t, err)
	assert.Contains(t, q, "SELECT service_name, http_method, http_route, http_status_code, latency_bucket_ms, SUM(count) as total_count, MIN(timestamp) as first_seen, MAX(timestamp) as last_seen")
	assert.Contains(t, q, "FROM beyla_http_metrics")
	assert.Contains(t, q, "WHERE timestamp >= ? AND timestamp <= ?")
	assert.Contains(t, q, "AND service_name = ?")
	assert.Contains(t, q, "AND http_method = ?")
	assert.Contains(t, q, "AND http_status_code BETWEEN ? AND ?")
	assert.Contains(t, q, "GROUP BY service_name, http_method, http_route, http_status_code, latency_bucket_ms")
	assert.Contains(t, q, "ORDER BY http_route, latency_bucket_ms")
	assert.Len(t, args, 6) // start, end, service, method, 200, 299
}

func TestBuilder_BeylaTracesQuery(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	b := NewQueryBuilder("beyla_traces").
		Select(
			"trace_id",
			"span_id",
			"parent_span_id",
			"service_name",
			"span_name",
			"span_kind",
			"start_time",
			"duration_us",
			"status_code",
		).
		TimeColumn("start_time").
		TimeRange(start, end).
		Eq("trace_id", "abc123").
		Eq("service_name", "my-service").
		OrderBy("-start_time")

	// Add optional duration filter.
	b.Gte("duration_us", 1000)
	b.Limit(50)

	q, args, err := b.Build()

	require.NoError(t, err)
	assert.Contains(t, q, "start_time >= ? AND start_time <= ?")
	assert.Contains(t, q, "AND trace_id = ?")
	assert.Contains(t, q, "AND service_name = ?")
	assert.Contains(t, q, "AND duration_us >= ?")
	assert.Contains(t, q, "ORDER BY start_time DESC")
	assert.Contains(t, q, "LIMIT ?")
	assert.Len(t, args, 6) // start, end, trace_id, service, duration, limit
}

func TestBuilder_TelemetrySummariesQuery(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	q, args, err := NewQueryBuilder("otel_summaries").
		Select("bucket_time", "agent_id", "service_name", "span_kind",
			"p50_ms", "p95_ms", "p99_ms", "error_count", "total_spans", "sample_traces").
		TimeColumn("bucket_time").
		TimeRange(start, end).
		Eq("agent_id", "agent-123").
		OrderBy("-bucket_time").
		Build()

	require.NoError(t, err)
	assert.Contains(t, q, "bucket_time >= ? AND bucket_time <= ?")
	assert.Contains(t, q, "AND agent_id = ?")
	assert.Contains(t, q, "ORDER BY bucket_time DESC")
	assert.Len(t, args, 3) // start, end, agent_id
}

func TestBuilder_WildcardAgentID(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	q, args, err := NewQueryBuilder("otel_summaries").
		Select("bucket_time", "agent_id", "service_name").
		TimeColumn("bucket_time").
		TimeRange(start, end).
		Eq("agent_id", ""). // Empty string should be skipped.
		OrderBy("-bucket_time").
		Build()

	require.NoError(t, err)
	assert.NotContains(t, q, "agent_id = ?") // Should not have WHERE clause for agent_id
	assert.Len(t, args, 2)                   // start, end only
}

func TestBuilder_ErrorNoTable(t *testing.T) {
	b := &Builder{}
	_, _, err := b.Build()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "table name is required")
}

func TestBuilder_MustBuild(t *testing.T) {
	q, args := NewQueryBuilder("test_table").
		Select("id", "name").
		MustBuild()

	assert.Equal(t, "SELECT id, name FROM test_table", q)
	assert.Empty(t, args)
}

func TestBuilder_MustBuildPanic(t *testing.T) {
	defer func() {
		r := recover()
		assert.NotNil(t, r)
	}()

	// Should panic because table is required.
	_, _ = (&Builder{}).MustBuild()
}

func TestBuilder_EmptyFiltersSkipped(t *testing.T) {
	filters := map[string]string{
		"http_method": "GET",
		"http_route":  "", // empty, should be skipped
	}

	q, args, err := NewQueryBuilder("beyla_http_metrics").
		Eq("http_method", filters["http_method"]).
		Eq("http_route", filters["http_route"]).
		Build()

	require.NoError(t, err)
	assert.Contains(t, q, "http_method = ?")
	assert.NotContains(t, q, "http_route")
	assert.Len(t, args, 1) // Only http_method
}
