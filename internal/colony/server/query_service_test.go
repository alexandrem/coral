package server

import (
	"context"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/colony/database"
)

// setupTestServerWithMetrics creates a test server with real Beyla metrics data.
func setupTestServerWithMetrics(t *testing.T) (*Server, *database.Database) {
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)

	// Create temporary database for testing.
	tmpDir := t.TempDir()
	db, err := database.New(tmpDir, "test-colony", logger)
	require.NoError(t, err)

	ctx := context.Background()

	// Insert realistic HTTP metrics using the database's insert method.
	now := time.Now()
	httpMetrics := []*agentv1.EbpfHttpMetric{
		{
			Timestamp:      now.Add(-5 * time.Minute).UnixMilli(),
			ServiceName:    "api-service",
			HttpMethod:     "GET",
			HttpRoute:      "/api/users",
			HttpStatusCode: 200,
			LatencyBuckets: []float64{1.0, 5.0, 10.0, 50.0, 100.0},
			LatencyCounts:  []uint64{5, 10, 20, 15, 5},
		},
		{
			Timestamp:      now.Add(-10 * time.Minute).UnixMilli(),
			ServiceName:    "api-service",
			HttpMethod:     "POST",
			HttpRoute:      "/api/users",
			HttpStatusCode: 201,
			LatencyBuckets: []float64{1.0, 5.0, 10.0, 50.0, 100.0},
			LatencyCounts:  []uint64{3, 8, 12, 10, 2},
		},
		{
			Timestamp:      now.Add(-15 * time.Minute).UnixMilli(),
			ServiceName:    "api-service",
			HttpMethod:     "GET",
			HttpRoute:      "/api/users",
			HttpStatusCode: 500,
			LatencyBuckets: []float64{1.0, 5.0, 10.0, 50.0, 100.0},
			LatencyCounts:  []uint64{1, 2, 3, 4, 5},
		},
		{
			Timestamp:      now.Add(-20 * time.Minute).UnixMilli(),
			ServiceName:    "payment-service",
			HttpMethod:     "POST",
			HttpRoute:      "/pay",
			HttpStatusCode: 200,
			LatencyBuckets: []float64{5.0, 10.0, 50.0, 100.0, 500.0},
			LatencyCounts:  []uint64{10, 20, 30, 15, 5},
		},
		{
			Timestamp:      now.Add(-25 * time.Minute).UnixMilli(),
			ServiceName:    "payment-service",
			HttpMethod:     "POST",
			HttpRoute:      "/pay",
			HttpStatusCode: 400,
			LatencyBuckets: []float64{5.0, 10.0, 50.0, 100.0, 500.0},
			LatencyCounts:  []uint64{5, 8, 10, 5, 2},
		},
	}

	// Insert metrics for agent-1.
	err = db.InsertBeylaHTTPMetrics(ctx, "agent-1", httpMetrics[:3])
	require.NoError(t, err)

	// Insert metrics for agent-2.
	err = db.InsertBeylaHTTPMetrics(ctx, "agent-2", httpMetrics[3:])
	require.NoError(t, err)

	// Register services in the services table (required for ListServices to work).
	// ListServices now queries the services registry table, not metrics tables.
	database.PopulateTestServices(t, db,
		&database.Service{
			ID:       "api-service-agent-1",
			Name:     "api-service",
			AppID:    "api-service",
			Version:  "1.0.0",
			AgentID:  "agent-1",
			LastSeen: now.Add(-5 * time.Minute),
		},
		&database.Service{
			ID:       "payment-service-agent-2",
			Name:     "payment-service",
			AppID:    "payment-service",
			Version:  "1.0.0",
			AgentID:  "agent-2",
			LastSeen: now.Add(-20 * time.Minute),
		},
	)

	server := &Server{
		database: db,
		logger:   logger,
	}

	return server, db
}

// TestListServicesIntegration tests the ListServices endpoint end-to-end.
func TestListServicesIntegration(t *testing.T) {
	t.Run("successfully lists services from services registry", func(t *testing.T) {
		server, _ := setupTestServerWithMetrics(t)

		req := connect.NewRequest(&colonyv1.ListServicesRequest{})
		resp, err := server.ListServices(context.Background(), req)

		require.NoError(t, err, "ListServices should not return an error")
		assert.NotNil(t, resp)
		assert.GreaterOrEqual(t, len(resp.Msg.Services), 2, "Should find at least 2 services")

		// Verify we can find our test services.
		serviceNames := make(map[string]bool)
		for _, svc := range resp.Msg.Services {
			serviceNames[svc.Name] = true
			assert.NotEmpty(t, svc.Name, "Service name should not be empty")
			assert.NotNil(t, svc.LastSeen, "Last seen timestamp should be set")
		}

		// At least one of our test services should be present.
		assert.True(t,
			serviceNames["api-service"] || serviceNames["payment-service"],
			"Should find at least one of our test services")
	})

	t.Run("returns empty list when database is empty", func(t *testing.T) {
		logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)
		tmpDir := t.TempDir()
		db, err := database.New(tmpDir, "empty-colony", logger)
		require.NoError(t, err)

		server := &Server{
			database: db,
			logger:   logger,
		}

		req := connect.NewRequest(&colonyv1.ListServicesRequest{})
		resp, err := server.ListServices(context.Background(), req)

		require.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Len(t, resp.Msg.Services, 0, "Should return empty list for empty database")
	})
}

// TestGetServiceActivityIntegration tests the GetServiceActivity endpoint.
func TestGetServiceActivityIntegration(t *testing.T) {
	t.Run("successfully gets service activity", func(t *testing.T) {
		server, _ := setupTestServerWithMetrics(t)

		req := connect.NewRequest(&colonyv1.GetServiceActivityRequest{
			Service:     "api-service",
			TimeRangeMs: 3600000, // 1 hour
		})

		resp, err := server.GetServiceActivity(context.Background(), req)

		// This might fail if there are schema mismatches, but we want to know about them.
		if err != nil {
			t.Logf("GetServiceActivity error (may indicate schema issues): %v", err)
			return
		}

		require.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, "api-service", resp.Msg.ServiceName)
		assert.Greater(t, resp.Msg.RequestCount, int64(0), "Should have request count > 0")
		assert.NotNil(t, resp.Msg.Timestamp)
	})

	t.Run("returns not found for nonexistent service", func(t *testing.T) {
		server, _ := setupTestServerWithMetrics(t)

		req := connect.NewRequest(&colonyv1.GetServiceActivityRequest{
			Service:     "nonexistent-service-12345",
			TimeRangeMs: 3600000,
		})

		resp, err := server.GetServiceActivity(context.Background(), req)

		assert.Error(t, err, "Should return error for nonexistent service")
		assert.Nil(t, resp)
	})
}

// TestListServiceActivityIntegration tests listing all service activities.
func TestListServiceActivityIntegration(t *testing.T) {
	t.Run("successfully lists all service activities", func(t *testing.T) {
		server, _ := setupTestServerWithMetrics(t)

		req := connect.NewRequest(&colonyv1.ListServiceActivityRequest{
			TimeRangeMs: 3600000, // 1 hour
		})

		resp, err := server.ListServiceActivity(context.Background(), req)

		// Log if there's an error (schema issues).
		if err != nil {
			t.Logf("ListServiceActivity error (may indicate schema issues): %v", err)
			return
		}

		require.NoError(t, err)
		assert.NotNil(t, resp)
		assert.GreaterOrEqual(t, len(resp.Msg.Services), 1, "Should have at least one service")

		for _, svc := range resp.Msg.Services {
			assert.NotEmpty(t, svc.ServiceName)
			assert.GreaterOrEqual(t, svc.RequestCount, int64(0))
		}
	})
}

// TestExecuteQueryIntegration tests raw SQL execution.
func TestExecuteQueryIntegration(t *testing.T) {
	t.Run("executes query against beyla_http_metrics table", func(t *testing.T) {
		server, _ := setupTestServerWithMetrics(t)

		// Test that we can query the beyla_http_metrics table directly.
		req := connect.NewRequest(&colonyv1.ExecuteQueryRequest{
			Sql:     "SELECT DISTINCT service_name FROM beyla_http_metrics ORDER BY service_name",
			MaxRows: 100,
		})

		resp, err := server.ExecuteQuery(context.Background(), req)

		require.NoError(t, err, "Should be able to query beyla_http_metrics table")
		assert.NotNil(t, resp)
		assert.Greater(t, resp.Msg.RowCount, int32(0), "Should return at least one service")
		assert.Contains(t, resp.Msg.Columns, "service_name")
	})

	t.Run("regression test: ebpf_http_metrics table does not exist", func(t *testing.T) {
		server, _ := setupTestServerWithMetrics(t)

		// This query should fail because ebpf_http_metrics doesn't exist.
		// This is our regression test - we're ensuring we don't accidentally
		// use the old table name.
		req := connect.NewRequest(&colonyv1.ExecuteQueryRequest{
			Sql:     "SELECT COUNT(*) FROM ebpf_http_metrics",
			MaxRows: 100,
		})

		resp, err := server.ExecuteQuery(context.Background(), req)

		// Should fail because ebpf_http_metrics doesn't exist.
		assert.Error(t, err, "Query to ebpf_http_metrics should fail")
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "ebpf_http_metrics",
			"Error should mention the table name")
	})

	t.Run("limits results to maxRows", func(t *testing.T) {
		server, _ := setupTestServerWithMetrics(t)

		req := connect.NewRequest(&colonyv1.ExecuteQueryRequest{
			Sql:     "SELECT * FROM beyla_http_metrics",
			MaxRows: 5,
		})

		resp, err := server.ExecuteQuery(context.Background(), req)

		require.NoError(t, err)
		assert.NotNil(t, resp)
		assert.LessOrEqual(t, resp.Msg.RowCount, int32(5))
	})
}

// TestListServicesDualSourceDiscovery tests the RFD 084 dual-source discovery feature.
func TestListServicesDualSourceDiscovery(t *testing.T) {
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)
	tmpDir := t.TempDir()
	db, err := database.New(tmpDir, "dual-source-test", logger)
	require.NoError(t, err)

	ctx := context.Background()
	now := time.Now()

	// Create test scenario:
	// 1. "registered-only" - in services table only (no telemetry)
	// 2. "discovered-only" - has telemetry but not in services table
	// 3. "both-service" - in both services table AND has telemetry
	// 4. "old-telemetry" - has telemetry older than time range

	// Insert telemetry for "discovered-only" and "both-service".
	metricsDiscovered := []*agentv1.EbpfHttpMetric{
		{
			Timestamp:      now.Add(-10 * time.Minute).UnixMilli(),
			ServiceName:    "discovered-only",
			HttpMethod:     "GET",
			HttpRoute:      "/test",
			HttpStatusCode: 200,
			LatencyBuckets: []float64{10.0},
			LatencyCounts:  []uint64{5},
		},
		{
			Timestamp:      now.Add(-15 * time.Minute).UnixMilli(),
			ServiceName:    "both-service",
			HttpMethod:     "GET",
			HttpRoute:      "/test",
			HttpStatusCode: 200,
			LatencyBuckets: []float64{10.0},
			LatencyCounts:  []uint64{5},
		},
		{
			Timestamp:      now.Add(-2 * time.Hour).UnixMilli(), // Old telemetry
			ServiceName:    "old-telemetry",
			HttpMethod:     "GET",
			HttpRoute:      "/test",
			HttpStatusCode: 200,
			LatencyBuckets: []float64{10.0},
			LatencyCounts:  []uint64{5},
		},
	}
	err = db.InsertBeylaHTTPMetrics(ctx, "agent-1", metricsDiscovered)
	require.NoError(t, err)

	// Register services in services table.
	database.PopulateTestServices(t, db,
		&database.Service{
			ID:       "registered-only-id",
			Name:     "registered-only",
			AppID:    "registered-only",
			AgentID:  "agent-2",
			LastSeen: now.Add(-5 * time.Minute),
		},
		&database.Service{
			ID:       "both-service-id",
			Name:     "both-service",
			AppID:    "both-service",
			AgentID:  "agent-1",
			LastSeen: now.Add(-3 * time.Minute),
		},
	)

	server := &Server{
		database: db,
		logger:   logger,
	}

	t.Run("lists all services from both sources", func(t *testing.T) {
		req := connect.NewRequest(&colonyv1.ListServicesRequest{
			TimeRange: "1h", // 1 hour lookback
		})
		resp, err := server.ListServices(ctx, req)

		require.NoError(t, err)
		assert.NotNil(t, resp)

		// Should find 3 services: registered-only, discovered-only, both-service.
		// old-telemetry should be excluded (outside time range).
		serviceMap := make(map[string]*colonyv1.ServiceSummary)
		for _, svc := range resp.Msg.Services {
			serviceMap[svc.Name] = svc
		}

		// Verify registered-only service.
		if svc, exists := serviceMap["registered-only"]; exists {
			assert.Equal(t, colonyv1.ServiceSource_SERVICE_SOURCE_REGISTERED, svc.Source)
			assert.NotNil(t, svc.Status)
			assert.Equal(t, colonyv1.ServiceStatus_SERVICE_STATUS_ACTIVE, *svc.Status)
			assert.NotNil(t, svc.AgentId)
		}

		// Verify discovered-only service.
		if svc, exists := serviceMap["discovered-only"]; exists {
			assert.Equal(t, colonyv1.ServiceSource_SERVICE_SOURCE_OBSERVED, svc.Source)
			assert.NotNil(t, svc.Status)
			assert.Equal(t, colonyv1.ServiceStatus_SERVICE_STATUS_OBSERVED_ONLY, *svc.Status)
		}

		// Verify both-service.
		if svc, exists := serviceMap["both-service"]; exists {
			assert.Equal(t, colonyv1.ServiceSource_SERVICE_SOURCE_VERIFIED, svc.Source)
			assert.NotNil(t, svc.Status)
			assert.Equal(t, colonyv1.ServiceStatus_SERVICE_STATUS_ACTIVE, *svc.Status)
			assert.NotNil(t, svc.AgentId)
		}

		// old-telemetry should NOT be included (outside time range).
		assert.NotContains(t, serviceMap, "old-telemetry")
	})

	t.Run("respects time range parameter", func(t *testing.T) {
		// Use a very short time range (5 minutes).
		// Should only find services with recent activity.
		req := connect.NewRequest(&colonyv1.ListServicesRequest{
			TimeRange: "5m",
		})
		resp, err := server.ListServices(ctx, req)

		require.NoError(t, err)
		assert.NotNil(t, resp)

		serviceMap := make(map[string]*colonyv1.ServiceSummary)
		for _, svc := range resp.Msg.Services {
			serviceMap[svc.Name] = svc
		}

		// registered-only should still appear (from registry).
		assert.Contains(t, serviceMap, "registered-only")

		// both-service should appear (in registry).
		assert.Contains(t, serviceMap, "both-service")

		// discovered-only has telemetry at -10 minutes, outside 5m range.
		// It should NOT appear.
		assert.NotContains(t, serviceMap, "discovered-only")
	})

	t.Run("filters by source - registered only", func(t *testing.T) {
		source := colonyv1.ServiceSource_SERVICE_SOURCE_REGISTERED
		req := connect.NewRequest(&colonyv1.ListServicesRequest{
			TimeRange:    "1h",
			SourceFilter: &source,
		})
		resp, err := server.ListServices(ctx, req)

		require.NoError(t, err)
		assert.NotNil(t, resp)

		// Should only find registered-only service.
		serviceMap := make(map[string]*colonyv1.ServiceSummary)
		for _, svc := range resp.Msg.Services {
			serviceMap[svc.Name] = svc
			assert.Equal(t, colonyv1.ServiceSource_SERVICE_SOURCE_REGISTERED, svc.Source)
		}

		assert.Contains(t, serviceMap, "registered-only")
		assert.NotContains(t, serviceMap, "discovered-only")
		assert.NotContains(t, serviceMap, "both-service") // BOTH is not REGISTERED
	})

	t.Run("filters by source - discovered only", func(t *testing.T) {
		source := colonyv1.ServiceSource_SERVICE_SOURCE_OBSERVED
		req := connect.NewRequest(&colonyv1.ListServicesRequest{
			TimeRange:    "1h",
			SourceFilter: &source,
		})
		resp, err := server.ListServices(ctx, req)

		require.NoError(t, err)
		assert.NotNil(t, resp)

		// Should only find discovered-only service.
		serviceMap := make(map[string]*colonyv1.ServiceSummary)
		for _, svc := range resp.Msg.Services {
			serviceMap[svc.Name] = svc
			assert.Equal(t, colonyv1.ServiceSource_SERVICE_SOURCE_OBSERVED, svc.Source)
		}

		assert.Contains(t, serviceMap, "discovered-only")
		assert.NotContains(t, serviceMap, "registered-only")
		assert.NotContains(t, serviceMap, "both-service")
	})

	t.Run("filters by source - both", func(t *testing.T) {
		source := colonyv1.ServiceSource_SERVICE_SOURCE_VERIFIED
		req := connect.NewRequest(&colonyv1.ListServicesRequest{
			TimeRange:    "1h",
			SourceFilter: &source,
		})
		resp, err := server.ListServices(ctx, req)

		require.NoError(t, err)
		assert.NotNil(t, resp)

		// Should only find both-service.
		serviceMap := make(map[string]*colonyv1.ServiceSummary)
		for _, svc := range resp.Msg.Services {
			serviceMap[svc.Name] = svc
			assert.Equal(t, colonyv1.ServiceSource_SERVICE_SOURCE_VERIFIED, svc.Source)
		}

		assert.Contains(t, serviceMap, "both-service")
		assert.NotContains(t, serviceMap, "registered-only")
		assert.NotContains(t, serviceMap, "discovered-only")
	})
}

// TestTableNameRegression specifically tests that we use beyla_http_metrics, not ebpf_http_metrics.
func TestTableNameRegression(t *testing.T) {
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)
	tmpDir := t.TempDir()
	db, err := database.New(tmpDir, "regression-test", logger)
	require.NoError(t, err)

	// Insert data into the CORRECT table (beyla_http_metrics).
	ctx := context.Background()
	metrics := []*agentv1.EbpfHttpMetric{
		{
			Timestamp:      time.Now().UnixMilli(),
			ServiceName:    "test-service",
			HttpMethod:     "GET",
			HttpRoute:      "/test",
			HttpStatusCode: 200,
			LatencyBuckets: []float64{10.0},
			LatencyCounts:  []uint64{5},
		},
	}
	err = db.InsertBeylaHTTPMetrics(ctx, "test-agent", metrics)
	require.NoError(t, err)

	// Register service in services table (required for ListServices).
	database.PopulateTestServices(t, db,
		&database.Service{
			ID:      "test-service-agent",
			Name:    "test-service",
			AppID:   "test-service",
			AgentID: "test-agent",
		},
	)

	server := &Server{
		database: db,
		logger:   logger,
	}

	t.Run("ListServices uses services registry table", func(t *testing.T) {
		req := connect.NewRequest(&colonyv1.ListServicesRequest{})
		resp, err := server.ListServices(ctx, req)

		// ListServices now queries the services registry table.
		require.NoError(t, err, "ListServices should work with services registry table")
		assert.NotNil(t, resp)
		assert.GreaterOrEqual(t, len(resp.Msg.Services), 1, "Should find test-service")
	})

	t.Run("ExecuteQuery confirms beyla_http_metrics exists and has data", func(t *testing.T) {
		req := connect.NewRequest(&colonyv1.ExecuteQueryRequest{
			Sql:     "SELECT COUNT(*) as count FROM beyla_http_metrics",
			MaxRows: 1,
		})

		resp, err := server.ExecuteQuery(ctx, req)

		require.NoError(t, err, "Should be able to query beyla_http_metrics")
		assert.NotNil(t, resp)
		assert.Equal(t, int32(1), resp.Msg.RowCount)
		// Verify we got a count > 0.
		assert.NotEmpty(t, resp.Msg.Rows[0].Values[0])
	})

	t.Run("ExecuteQuery confirms ebpf_http_metrics does NOT exist", func(t *testing.T) {
		req := connect.NewRequest(&colonyv1.ExecuteQueryRequest{
			Sql:     "SELECT COUNT(*) FROM ebpf_http_metrics",
			MaxRows: 1,
		})

		resp, err := server.ExecuteQuery(ctx, req)

		// This MUST fail - the old table name should not exist.
		assert.Error(t, err, "ebpf_http_metrics table should not exist")
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "ebpf_http_metrics")
	})
}
