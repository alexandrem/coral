package script

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	_ "github.com/marcboeker/go-duckdb" // DuckDB driver
	"github.com/rs/zerolog"
)

// SDKServer provides HTTP endpoints for TypeScript scripts to access Coral data.
// It maintains a read-only connection pool to the agent's DuckDB database.
type SDKServer struct {
	port       int
	dbPath     string
	db         *sql.DB
	logger     zerolog.Logger
	server     *http.Server
	mu         sync.RWMutex
	activeReqs int64
}

// NewSDKServer creates a new SDK server.
// The server will open a read-only connection to the DuckDB database at dbPath.
func NewSDKServer(port int, dbPath string, logger zerolog.Logger) *SDKServer {
	return &SDKServer{
		port:   port,
		dbPath: dbPath,
		logger: logger.With().Str("component", "sdk-server").Logger(),
	}
}

// Start starts the SDK HTTP server.
// Opens a read-only DuckDB connection pool for concurrent script access.
func (s *SDKServer) Start(ctx context.Context) error {
	// Open DuckDB in read-only mode to allow concurrent readers.
	// DuckDB supports multiple concurrent readers when opened in read-only mode.
	// Format: file.db?access_mode=read_only&threads=4
	connStr := fmt.Sprintf("%s?access_mode=read_only&threads=4", s.dbPath)

	db, err := sql.Open("duckdb", connStr)
	if err != nil {
		return fmt.Errorf("failed to open DuckDB: %w", err)
	}

	// Configure connection pool for concurrent script access.
	// DuckDB can handle multiple concurrent read-only connections.
	// We set conservative limits to avoid overwhelming the database.
	db.SetMaxOpenConns(20)  // Max concurrent queries from all scripts
	db.SetMaxIdleConns(5)   // Keep some connections warm
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(1 * time.Minute)

	// Verify connection works.
	if err := db.Ping(); err != nil {
		db.Close()
		return fmt.Errorf("failed to ping DuckDB: %w", err)
	}

	s.db = db

	s.logger.Info().
		Str("db_path", s.dbPath).
		Int("max_conns", 20).
		Msg("Opened read-only DuckDB connection pool")

	// Set up HTTP server.
	mux := http.NewServeMux()

	// Register endpoints.
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/db/query", s.handleDBQuery)
	mux.HandleFunc("/metrics/percentile", s.handleMetricsPercentile)
	mux.HandleFunc("/metrics/error-rate", s.handleMetricsErrorRate)
	mux.HandleFunc("/traces/query", s.handleTracesQuery)
	mux.HandleFunc("/system/cpu", s.handleSystemCPU)
	mux.HandleFunc("/system/memory", s.handleSystemMemory)
	mux.HandleFunc("/emit", s.handleEmit)

	s.server = &http.Server{
		Addr:              fmt.Sprintf("localhost:%d", s.port),
		Handler:           s.concurrencyMiddleware(s.loggingMiddleware(mux)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		s.logger.Info().Int("port", s.port).Msg("SDK server listening")
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error().Err(err).Msg("SDK server error")
		}
	}()

	return nil
}

// Stop stops the SDK server and closes the database connection.
func (s *SDKServer) Stop(ctx context.Context) error {
	if s.server == nil {
		return nil
	}

	s.logger.Info().Msg("Stopping SDK server")

	// Shutdown HTTP server.
	if err := s.server.Shutdown(ctx); err != nil {
		s.logger.Error().Err(err).Msg("Error shutting down server")
	}

	// Close database connection pool.
	if s.db != nil {
		if err := s.db.Close(); err != nil {
			s.logger.Error().Err(err).Msg("Error closing database")
			return err
		}
	}

	return nil
}

// concurrencyMiddleware tracks active requests for monitoring.
func (s *SDKServer) concurrencyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.activeReqs++
		current := s.activeReqs
		s.mu.Unlock()

		// Log if we have high concurrency (potential bottleneck).
		if current > 10 {
			s.logger.Warn().
				Int64("active_requests", current).
				Msg("High SDK server concurrency")
		}

		defer func() {
			s.mu.Lock()
			s.activeReqs--
			s.mu.Unlock()
		}()

		next.ServeHTTP(w, r)
	})
}

// loggingMiddleware logs all requests.
func (s *SDKServer) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		s.logger.Debug().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Dur("duration", time.Since(start)).
			Msg("SDK request")
	})
}

// handleHealth handles health check requests.
func (s *SDKServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	// Check database connectivity.
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := s.db.PingContext(ctx); err != nil {
		http.Error(w, fmt.Sprintf("Database unhealthy: %v", err), http.StatusServiceUnavailable)
		return
	}

	s.mu.RLock()
	activeReqs := s.activeReqs
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":         "ok",
		"active_requests": activeReqs,
		"db_stats": map[string]int{
			"open_connections": s.db.Stats().OpenConnections,
			"in_use":           s.db.Stats().InUse,
			"idle":             s.db.Stats().Idle,
		},
	})
}

// handleDBQuery handles raw DuckDB query requests.
func (s *SDKServer) handleDBQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SQL string `json:"sql"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if req.SQL == "" {
		http.Error(w, "SQL query is required", http.StatusBadRequest)
		return
	}

	// Set query timeout to prevent long-running queries.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Execute query with context for timeout.
	rows, err := s.db.QueryContext(ctx, req.SQL)
	if err != nil {
		s.logger.Error().Err(err).Str("sql", req.SQL).Msg("Query failed")
		http.Error(w, fmt.Sprintf("Query failed: %v", err), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// Get column names.
	columns, err := rows.Columns()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get columns: %v", err), http.StatusInternalServerError)
		return
	}

	// Read rows (limit to prevent OOM).
	const maxRows = 10000
	results := make([]map[string]interface{}, 0)

	for rows.Next() && len(results) < maxRows {
		// Create a slice of interface{}'s to represent each column.
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range columns {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			http.Error(w, fmt.Sprintf("Failed to scan row: %v", err), http.StatusInternalServerError)
			return
		}

		// Create a map for this row.
		row := make(map[string]interface{})
		for i, col := range columns {
			row[col] = values[i]
		}

		results = append(results, row)
	}

	if err := rows.Err(); err != nil {
		http.Error(w, fmt.Sprintf("Row iteration error: %v", err), http.StatusInternalServerError)
		return
	}

	truncated := false
	if len(results) == maxRows {
		truncated = true
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"rows":      results,
		"count":     len(results),
		"truncated": truncated,
	})
}

// handleMetricsPercentile handles percentile metric requests.
func (s *SDKServer) handleMetricsPercentile(w http.ResponseWriter, r *http.Request) {
	service := r.URL.Query().Get("service")
	metric := r.URL.Query().Get("metric")
	pStr := r.URL.Query().Get("p")

	if service == "" || metric == "" || pStr == "" {
		http.Error(w, "Missing required parameters: service, metric, p", http.StatusBadRequest)
		return
	}

	p, err := strconv.ParseFloat(pStr, 64)
	if err != nil || p < 0 || p > 1 {
		http.Error(w, "Invalid percentile value (must be 0-1)", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Query percentile from DuckDB.
	// This is a simplified example - actual implementation would query beyla_http_metrics.
	query := fmt.Sprintf(`
		SELECT PERCENTILE_CONT(%f) WITHIN GROUP (ORDER BY duration_ns) as value
		FROM otel_spans_local
		WHERE service_name = '%s'
		  AND start_time > now() - INTERVAL '5 minutes'
	`, p, service)

	var value float64
	err = s.db.QueryRowContext(ctx, query).Scan(&value)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to query percentile")
		http.Error(w, fmt.Sprintf("Query failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"value": value,
	})
}

// handleMetricsErrorRate handles error rate metric requests.
func (s *SDKServer) handleMetricsErrorRate(w http.ResponseWriter, r *http.Request) {
	service := r.URL.Query().Get("service")
	window := r.URL.Query().Get("window")

	if service == "" {
		http.Error(w, "Missing required parameter: service", http.StatusBadRequest)
		return
	}

	if window == "" {
		window = "5m"
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Query error rate from DuckDB.
	query := fmt.Sprintf(`
		SELECT
			COUNT(CASE WHEN is_error THEN 1 END)::FLOAT / COUNT(*)::FLOAT as error_rate
		FROM otel_spans_local
		WHERE service_name = '%s'
		  AND start_time > now() - INTERVAL '%s'
	`, service, window)

	var errorRate float64
	err := s.db.QueryRowContext(ctx, query).Scan(&errorRate)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to query error rate")
		http.Error(w, fmt.Sprintf("Query failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"value": errorRate,
	})
}

// handleTracesQuery handles trace query requests.
func (s *SDKServer) handleTracesQuery(w http.ResponseWriter, r *http.Request) {
	service := r.URL.Query().Get("service")
	minDuration := r.URL.Query().Get("minDuration")
	timeRange := r.URL.Query().Get("timeRange")

	if service == "" {
		http.Error(w, "Missing required parameter: service", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	// Build query.
	query := fmt.Sprintf(`
		SELECT trace_id, span_id, duration_ns, is_error, http_status, http_method, http_route
		FROM otel_spans_local
		WHERE service_name = '%s'
	`, service)

	if timeRange != "" {
		query += fmt.Sprintf(" AND start_time > now() - INTERVAL '%s'", timeRange)
	}

	if minDuration != "" {
		query += fmt.Sprintf(" AND duration_ms > %s", minDuration)
	}

	query += " ORDER BY start_time DESC LIMIT 100"

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to query traces")
		http.Error(w, fmt.Sprintf("Query failed: %v", err), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	traces := make([]map[string]interface{}, 0)
	for rows.Next() {
		var traceID, spanID, httpMethod, httpRoute string
		var durationNs int64
		var isError bool
		var httpStatus int

		if err := rows.Scan(&traceID, &spanID, &durationNs, &isError, &httpStatus, &httpMethod, &httpRoute); err != nil {
			s.logger.Error().Err(err).Msg("Failed to scan trace row")
			continue
		}

		traces = append(traces, map[string]interface{}{
			"trace_id":     traceID,
			"span_id":      spanID,
			"duration_ns":  durationNs,
			"is_error":     isError,
			"http_status":  httpStatus,
			"http_method":  httpMethod,
			"http_route":   httpRoute,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"traces": traces,
		"count":  len(traces),
	})
}

// handleSystemCPU handles system CPU metric requests.
func (s *SDKServer) handleSystemCPU(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Query latest CPU metric from system_metrics_local table.
	query := `
		SELECT value
		FROM system_metrics_local
		WHERE name = 'system.cpu.utilization'
		ORDER BY timestamp DESC
		LIMIT 1
	`

	var cpuUsage float64
	err := s.db.QueryRowContext(ctx, query).Scan(&cpuUsage)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to query CPU metric")
		http.Error(w, fmt.Sprintf("Query failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"usage_percent": cpuUsage,
	})
}

// handleSystemMemory handles system memory metric requests.
func (s *SDKServer) handleSystemMemory(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Query latest memory metrics from system_metrics_local table.
	query := `
		SELECT
			MAX(CASE WHEN name = 'system.memory.usage' THEN value END) as used,
			MAX(CASE WHEN name = 'system.memory.total' THEN value END) as total
		FROM system_metrics_local
		WHERE name IN ('system.memory.usage', 'system.memory.total')
		  AND timestamp > now() - INTERVAL '1 minute'
	`

	var used, total float64
	err := s.db.QueryRowContext(ctx, query).Scan(&used, &total)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to query memory metrics")
		http.Error(w, fmt.Sprintf("Query failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"used":  used,
		"total": total,
	})
}

// handleEmit handles custom event emission from scripts.
func (s *SDKServer) handleEmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var event struct {
		Name     string                 `json:"name"`
		Data     map[string]interface{} `json:"data"`
		Severity string                 `json:"severity"`
	}

	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if event.Name == "" {
		http.Error(w, "Event name is required", http.StatusBadRequest)
		return
	}

	// Log the event (in production, this would be sent to colony).
	s.logger.Info().
		Str("event_name", event.Name).
		Str("severity", event.Severity).
		Interface("data", event.Data).
		Msg("Script event emitted")

	// TODO: Forward event to colony via gRPC.

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
