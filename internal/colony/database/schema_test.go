package database

import (
	"os"
	"testing"

	"github.com/rs/zerolog"
)

func TestInitSchema_TablesCreated(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()

	// Create logger for test.
	logger := zerolog.New(os.Stdout)

	// Initialize database.
	db, err := New(tempDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Verify all six tables exist.
	expectedTables := []string{
		"services",
		"metric_summaries",
		"events",
		"insights",
		"service_connections",
		"baselines",
	}

	for _, table := range expectedTables {
		t.Run(table, func(t *testing.T) {
			// Query the table to verify it exists (will error if table doesn't exist).
			query := "SELECT COUNT(*) FROM " + table
			var count int
			if err := db.DB().QueryRow(query).Scan(&count); err != nil {
				t.Errorf("Table %s does not exist or query failed: %v", table, err)
			}
		})
	}
}

func TestInitSchema_Idempotency(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()

	// Create logger for test.
	logger := zerolog.New(os.Stdout)

	// Initialize database first time.
	db1, err := New(tempDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database first time: %v", err)
	}
	_ = db1.Close()

	// Initialize database second time (should reuse existing database).
	db2, err := New(tempDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database second time: %v", err)
	}
	defer func() { _ = db2.Close() }()

	// Verify tables still exist.
	var count int
	if err := db2.DB().QueryRow("SELECT COUNT(*) FROM services").Scan(&count); err != nil {
		t.Errorf("Failed to query services table after re-initialization: %v", err)
	}
}

func TestInitSchema_Indexes(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()

	// Create logger for test.
	logger := zerolog.New(os.Stdout)

	// Initialize database.
	db, err := New(tempDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Verify indexes exist by querying DuckDB's system tables.
	// DuckDB stores index information in duckdb_indexes() table function.
	rows, err := db.DB().Query("SELECT index_name FROM duckdb_indexes()")
	if err != nil {
		t.Fatalf("Failed to query indexes: %v", err)
	}
	defer func() { _ = rows.Close() }()

	indexes := make(map[string]bool)
	for rows.Next() {
		var indexName string
		if err := rows.Scan(&indexName); err != nil {
			t.Fatalf("Failed to scan index name: %v", err)
		}
		indexes[indexName] = true
	}

	// Verify some expected indexes exist.
	// Note: services table indexes removed due to DuckDB limitations.
	expectedIndexes := []string{
		"idx_metric_summaries_service_id",
		"idx_events_service_id",
		"idx_insights_status",
		"idx_baselines_service_id",
	}

	for _, idx := range expectedIndexes {
		if !indexes[idx] {
			t.Errorf("Expected index %s not found", idx)
		}
	}
}

func TestInitSchema_ColumnTypes(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()

	// Create logger for test.
	logger := zerolog.New(os.Stdout)

	// Initialize database.
	db, err := New(tempDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Test services table columns.
	t.Run("services", func(t *testing.T) {
		_, err := db.DB().Exec(`
			INSERT INTO services (id, name, app_id, agent_id, status, registered_at)
			VALUES ('svc-1', 'test-service', 'app-1', 'agent-1', 'running', CURRENT_TIMESTAMP)
		`)
		if err != nil {
			t.Errorf("Failed to insert into services: %v", err)
		}

		var count int
		if err := db.DB().QueryRow("SELECT COUNT(*) FROM services WHERE id = 'svc-1'").Scan(&count); err != nil {
			t.Errorf("Failed to query services: %v", err)
		}
		if count != 1 {
			t.Errorf("Expected 1 row, got %d", count)
		}
	})

	// Test service_heartbeats table columns.
	t.Run("service_heartbeats", func(t *testing.T) {
		_, err := db.DB().Exec(`
			INSERT INTO service_heartbeats (service_id, last_seen)
			VALUES ('svc-1', CURRENT_TIMESTAMP)
		`)
		if err != nil {
			t.Errorf("Failed to insert into service_heartbeats: %v", err)
		}

		var count int
		if err := db.DB().QueryRow("SELECT COUNT(*) FROM service_heartbeats WHERE service_id = 'svc-1'").Scan(&count); err != nil {
			t.Errorf("Failed to query service_heartbeats: %v", err)
		}
		if count != 1 {
			t.Errorf("Expected 1 row, got %d", count)
		}
	})

	// Test metric_summaries table columns.
	t.Run("metric_summaries", func(t *testing.T) {
		_, err := db.DB().Exec(`
			INSERT INTO metric_summaries (timestamp, service_id, metric_name, interval, p50, p95, mean, count)
			VALUES (CURRENT_TIMESTAMP, 'svc-1', 'response_time', '5m', 10.5, 50.2, 15.3, 100)
		`)
		if err != nil {
			t.Errorf("Failed to insert into metric_summaries: %v", err)
		}
	})

	// Test events table columns.
	t.Run("events", func(t *testing.T) {
		_, err := db.DB().Exec(`
			INSERT INTO events (id, timestamp, service_id, event_type)
			VALUES (1, CURRENT_TIMESTAMP, 'svc-1', 'deploy')
		`)
		if err != nil {
			t.Errorf("Failed to insert into events: %v", err)
		}
	})

	// Test insights table columns.
	t.Run("insights", func(t *testing.T) {
		_, err := db.DB().Exec(`
			INSERT INTO insights (id, created_at, insight_type, priority, title, summary, status)
			VALUES (1, CURRENT_TIMESTAMP, 'anomaly', 'high', 'Test Insight', 'Test summary', 'active')
		`)
		if err != nil {
			t.Errorf("Failed to insert into insights: %v", err)
		}
	})

	// Test service_connections table columns.
	t.Run("service_connections", func(t *testing.T) {
		_, err := db.DB().Exec(`
			INSERT INTO service_connections (from_service, to_service, protocol, first_observed, last_observed, connection_count)
			VALUES ('svc-1', 'svc-2', 'http', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 10)
		`)
		if err != nil {
			t.Errorf("Failed to insert into service_connections: %v", err)
		}
	})

	// Test baselines table columns.
	t.Run("baselines", func(t *testing.T) {
		_, err := db.DB().Exec(`
			INSERT INTO baselines (service_id, metric_name, time_window, mean, p50, last_updated)
			VALUES ('svc-1', 'response_time', '1h', 15.5, 10.2, CURRENT_TIMESTAMP)
		`)
		if err != nil {
			t.Errorf("Failed to insert into baselines: %v", err)
		}
	})
}

func TestInitSchema_PrimaryKeys(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()

	// Create logger for test.
	logger := zerolog.New(os.Stdout)

	// Initialize database.
	db, err := New(tempDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Test primary key constraint on services table.
	t.Run("services_pk", func(t *testing.T) {
		_, err := db.DB().Exec(`
			INSERT INTO services (id, name, app_id, agent_id, status, registered_at)
			VALUES ('svc-1', 'test-service', 'app-1', 'agent-1', 'running', CURRENT_TIMESTAMP)
		`)
		if err != nil {
			t.Fatalf("Failed to insert first row: %v", err)
		}

		// Attempt to insert duplicate primary key (should fail).
		_, err = db.DB().Exec(`
			INSERT INTO services (id, name, app_id, agent_id, status, registered_at)
			VALUES ('svc-1', 'test-service-2', 'app-2', 'agent-2', 'running', CURRENT_TIMESTAMP)
		`)
		if err == nil {
			t.Error("Expected primary key constraint violation, but insert succeeded")
		}
	})

	// Test composite primary key on metric_summaries table.
	t.Run("metric_summaries_pk", func(t *testing.T) {
		ts := "2025-01-01 00:00:00"
		_, err := db.DB().Exec(`
			INSERT INTO metric_summaries (timestamp, service_id, metric_name, interval, mean, count)
			VALUES (?, 'svc-1', 'response_time', '5m', 15.0, 100)
		`, ts)
		if err != nil {
			t.Fatalf("Failed to insert first row: %v", err)
		}

		// Attempt to insert duplicate composite primary key (should fail).
		_, err = db.DB().Exec(`
			INSERT INTO metric_summaries (timestamp, service_id, metric_name, interval, mean, count)
			VALUES (?, 'svc-1', 'response_time', '5m', 20.0, 200)
		`, ts)
		if err == nil {
			t.Error("Expected primary key constraint violation, but insert succeeded")
		}
	})
}
