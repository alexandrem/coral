package script

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) *sql.DB {
	// Create in-memory DuckDB for testing
	db, err := sql.Open("duckdb", ":memory:")
	require.NoError(t, err)

	// Create test tables
	_, err = db.Exec(`
		CREATE TABLE otel_spans_local (
			trace_id VARCHAR,
			span_id VARCHAR,
			service_name VARCHAR,
			duration_ns BIGINT,
			is_error BOOLEAN,
			http_status INTEGER,
			http_method VARCHAR,
			http_route VARCHAR,
			start_time TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	require.NoError(t, err)

	_, err = db.Exec(`
		CREATE TABLE system_metrics_local (
			timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			name VARCHAR,
			value DOUBLE,
			unit VARCHAR,
			metric_type VARCHAR,
			attributes VARCHAR
		)
	`)
	require.NoError(t, err)

	// Insert test data
	_, err = db.Exec(`
		INSERT INTO otel_spans_local VALUES
			('trace-1', 'span-1', 'payments', 5000000, false, 200, 'GET', '/api/payments', CURRENT_TIMESTAMP),
			('trace-2', 'span-2', 'payments', 600000000, true, 500, 'POST', '/api/payments', CURRENT_TIMESTAMP),
			('trace-3', 'span-3', 'orders', 3000000, false, 200, 'GET', '/api/orders', CURRENT_TIMESTAMP)
	`)
	require.NoError(t, err)

	_, err = db.Exec(`
		INSERT INTO system_metrics_local VALUES
			(CURRENT_TIMESTAMP, 'system.cpu.utilization', 45.5, 'percent', 'gauge', '{}'),
			(CURRENT_TIMESTAMP, 'system.memory.usage', 8589934592, 'bytes', 'gauge', '{}'),
			(CURRENT_TIMESTAMP, 'system.memory.total', 17179869184, 'bytes', 'gauge', '{}')
	`)
	require.NoError(t, err)

	return db
}

func TestSDKServer_HandleHealth(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	server := &SDKServer{
		db:     db,
		logger: zerolog.Nop(),
	}

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	server.handleHealth(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	assert.Equal(t, "ok", response["status"])
	assert.NotNil(t, response["db_stats"])
}

func TestSDKServer_HandleDBQuery(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	server := &SDKServer{
		db:     db,
		logger: zerolog.Nop(),
	}

	tests := []struct {
		name           string
		sql            string
		expectedCount  int
		expectedError  bool
	}{
		{
			name:          "Select all spans",
			sql:           "SELECT * FROM otel_spans_local",
			expectedCount: 3,
		},
		{
			name:          "Filter by service",
			sql:           "SELECT * FROM otel_spans_local WHERE service_name = 'payments'",
			expectedCount: 2,
		},
		{
			name:          "Invalid SQL",
			sql:           "SELECT * FROM nonexistent_table",
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqBody := map[string]string{"sql": tt.sql}
			body, _ := json.Marshal(reqBody)

			req := httptest.NewRequest("POST", "/db/query", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			server.handleDBQuery(w, req)

			if tt.expectedError {
				assert.Equal(t, http.StatusInternalServerError, w.Code)
			} else {
				assert.Equal(t, http.StatusOK, w.Code)

				var response map[string]interface{}
				err := json.NewDecoder(w.Body).Decode(&response)
				require.NoError(t, err)

				rows := response["rows"].([]interface{})
				assert.Len(t, rows, tt.expectedCount)
			}
		})
	}
}

func TestSDKServer_HandleMetricsPercentile(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	server := &SDKServer{
		db:     db,
		logger: zerolog.Nop(),
	}

	req := httptest.NewRequest("GET", "/metrics/percentile?service=payments&metric=http.server.duration&p=0.99", nil)
	w := httptest.NewRecorder()

	server.handleMetricsPercentile(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	assert.NotNil(t, response["value"])
}

func TestSDKServer_HandleMetricsErrorRate(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	server := &SDKServer{
		db:     db,
		logger: zerolog.Nop(),
	}

	req := httptest.NewRequest("GET", "/metrics/error-rate?service=payments&window=5m", nil)
	w := httptest.NewRecorder()

	server.handleMetricsErrorRate(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	errorRate := response["value"].(float64)
	assert.Greater(t, errorRate, 0.0) // Should be > 0 since we have 1 error out of 2 spans
}

func TestSDKServer_HandleTracesQuery(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	server := &SDKServer{
		db:     db,
		logger: zerolog.Nop(),
	}

	req := httptest.NewRequest("GET", "/traces/query?service=payments", nil)
	w := httptest.NewRecorder()

	server.handleTracesQuery(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	traces := response["traces"].([]interface{})
	assert.Len(t, traces, 2) // Should have 2 payment traces
}

func TestSDKServer_HandleSystemCPU(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	server := &SDKServer{
		db:     db,
		logger: zerolog.Nop(),
	}

	req := httptest.NewRequest("GET", "/system/cpu", nil)
	w := httptest.NewRecorder()

	server.handleSystemCPU(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	cpuUsage := response["usage_percent"].(float64)
	assert.Equal(t, 45.5, cpuUsage)
}

func TestSDKServer_HandleSystemMemory(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	server := &SDKServer{
		db:     db,
		logger: zerolog.Nop(),
	}

	req := httptest.NewRequest("GET", "/system/memory", nil)
	w := httptest.NewRecorder()

	server.handleSystemMemory(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	used := response["used"].(float64)
	total := response["total"].(float64)

	assert.Greater(t, used, 0.0)
	assert.Greater(t, total, used)
}

func TestSDKServer_HandleEmit(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	server := &SDKServer{
		db:     db,
		logger: zerolog.Nop(),
	}

	event := map[string]interface{}{
		"name": "alert",
		"data": map[string]interface{}{
			"message":  "High latency detected",
			"service":  "payments",
			"severity": "warning",
		},
		"severity": "warning",
	}

	body, _ := json.Marshal(event)
	req := httptest.NewRequest("POST", "/emit", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleEmit(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]string
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	assert.Equal(t, "ok", response["status"])
}

func TestSDKServer_StartStop(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	server := NewSDKServer(9999, ":memory:", zerolog.Nop())
	server.db = db // Use our test DB

	ctx := context.Background()

	// Start server
	err := server.Start(ctx)
	require.NoError(t, err)

	// Give it time to start
	time.Sleep(100 * time.Millisecond)

	// Test health endpoint
	resp, err := http.Get("http://localhost:9999/health")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Stop server
	err = server.Stop(ctx)
	assert.NoError(t, err)
}

func TestSDKServer_ConcurrencyMiddleware(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	server := &SDKServer{
		db:     db,
		logger: zerolog.Nop(),
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := server.concurrencyMiddleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	// Before request
	assert.Equal(t, int64(0), server.activeReqs)

	// During request (we can't test this easily in unit test)
	middleware.ServeHTTP(w, req)

	// After request
	assert.Equal(t, int64(0), server.activeReqs)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSDKServer_QueryTimeout(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	server := &SDKServer{
		db:     db,
		logger: zerolog.Nop(),
	}

	// This query would hang forever without timeout
	reqBody := map[string]string{
		"sql": "SELECT sleep(60)", // DuckDB sleep function
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/db/query", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	// Set a short timeout context
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()

	// This should timeout
	server.handleDBQuery(w, req)

	// Should get an error due to timeout
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestSDKServer_RowLimit(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Insert many rows
	for i := 0; i < 15000; i++ {
		_, err := db.Exec(`
			INSERT INTO otel_spans_local VALUES
				('trace-' || ?, 'span-' || ?, 'test', 1000000, false, 200, 'GET', '/test', CURRENT_TIMESTAMP)
		`, i, i)
		require.NoError(t, err)
	}

	server := &SDKServer{
		db:     db,
		logger: zerolog.Nop(),
	}

	reqBody := map[string]string{
		"sql": "SELECT * FROM otel_spans_local",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/db/query", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleDBQuery(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	rows := response["rows"].([]interface{})
	// Should be limited to 10,000 rows
	assert.LessOrEqual(t, len(rows), 10000)

	truncated := response["truncated"].(bool)
	assert.True(t, truncated) // Should be truncated
}
