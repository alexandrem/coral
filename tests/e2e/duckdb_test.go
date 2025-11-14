package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/coral-io/coral/tests/helpers"
	"github.com/stretchr/testify/suite"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	colonyv1 "github.com/coral-io/coral/proto/gen/colony/v1"
)

// DuckDBE2ESuite tests DuckDB data collection and storage end-to-end.
type DuckDBE2ESuite struct {
	helpers.E2ETestSuite
	procMgr       *helpers.ProcessManager
	configBuilder *helpers.ConfigBuilder
	dbHelper      *helpers.DatabaseHelper
}

// TestDuckDBE2E is the entry point for the DuckDB E2E test suite.
func TestDuckDBE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E tests in short mode")
	}
	suite.Run(t, new(DuckDBE2ESuite))
}

// SetupSuite runs once before all tests in the suite.
func (s *DuckDBE2ESuite) SetupSuite() {
	s.E2ETestSuite.SetupSuite()
	s.procMgr = helpers.NewProcessManager(s.T())
	s.configBuilder = helpers.NewConfigBuilder(s.T(), s.TempDir)
	s.dbHelper = helpers.NewDatabaseHelper(s.T(), s.TempDir)
}

// TearDownSuite runs once after all tests in the suite.
func (s *DuckDBE2ESuite) TearDownSuite() {
	s.procMgr.StopAll(10 * time.Second)
	s.configBuilder.Cleanup()
	s.dbHelper.CloseAll()
	s.E2ETestSuite.TearDownSuite()
}

// TestDuckDBInitialization tests that DuckDB initializes correctly.
func (s *DuckDBE2ESuite) TestDuckDBInitialization() {
	// Create a test database
	db := s.dbHelper.CreateDB("test-init")
	defer db.Close()

	// Create a test table
	s.dbHelper.Exec(db, `
		CREATE TABLE test_table (
			id INTEGER PRIMARY KEY,
			name VARCHAR,
			created_at TIMESTAMP
		)
	`)

	// Verify table exists
	exists := s.dbHelper.TableExists(db, "test_table")
	s.Require().True(exists, "Test table should exist")

	s.T().Log("DuckDB initialization successful")
}

// TestColonyDatabaseCreation tests that colony creates its database.
func (s *DuckDBE2ESuite) TestColonyDatabaseCreation() {
	apiPort := s.GetFreePort()
	grpcPort := s.GetFreePort()

	configPath := s.configBuilder.WriteColonyConfig("colony-db", apiPort, grpcPort)

	// Start colony
	s.procMgr.Start(
		s.Ctx,
		"colony",
		"./bin/coral",
		"colony", "start",
		"--config", configPath,
	)

	s.Require().True(
		s.WaitForPort("127.0.0.1", grpcPort, 30*time.Second),
		"Colony did not start",
	)

	// Give colony time to initialize database
	time.Sleep(2 * time.Second)

	// TODO: Verify colony database schema
	// When colony DB initialization is implemented, verify:
	// 1. Database file exists
	// 2. Required tables are created
	// 3. Indexes are created

	s.T().Log("Colony database creation test completed")
}

// TestDataIngestion tests ingesting data into DuckDB.
func (s *DuckDBE2ESuite) TestDataIngestion() {
	db := s.dbHelper.CreateDB("test-ingestion")
	defer db.Close()

	// Create metrics table
	s.dbHelper.Exec(db, `
		CREATE TABLE metrics (
			id INTEGER PRIMARY KEY,
			agent_id VARCHAR,
			metric_name VARCHAR,
			metric_value DOUBLE,
			timestamp TIMESTAMP,
			labels JSON
		)
	`)

	// Insert test data
	testData := []struct {
		agentID     string
		metricName  string
		metricValue float64
	}{
		{"agent-1", "cpu_usage", 45.5},
		{"agent-1", "memory_usage", 2048.0},
		{"agent-2", "cpu_usage", 78.2},
		{"agent-2", "memory_usage", 4096.0},
		{"agent-3", "cpu_usage", 23.1},
	}

	for i, td := range testData {
		s.dbHelper.Exec(db, `
			INSERT INTO metrics (id, agent_id, metric_name, metric_value, timestamp, labels)
			VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, '{}')
		`, i+1, td.agentID, td.metricName, td.metricValue)
	}

	// Verify data was inserted
	count := s.dbHelper.CountRows(db, "metrics")
	s.Require().Equal(len(testData), count, "Expected %d rows", len(testData))

	// Query aggregated data
	var avgCPU float64
	err := db.QueryRow(`
		SELECT AVG(metric_value)
		FROM metrics
		WHERE metric_name = 'cpu_usage'
	`).Scan(&avgCPU)
	s.Require().NoError(err)

	expectedAvg := (45.5 + 78.2 + 23.1) / 3
	s.Require().InDelta(expectedAvg, avgCPU, 0.1, "Average CPU should match")

	s.T().Logf("Data ingestion successful, avg CPU: %.2f", avgCPU)
}

// TestTimeSeriesData tests time-series data storage and queries.
func (s *DuckDBE2ESuite) TestTimeSeriesData() {
	db := s.dbHelper.CreateDB("test-timeseries")
	defer db.Close()

	// Create time-series table
	s.dbHelper.Exec(db, `
		CREATE TABLE timeseries_metrics (
			timestamp TIMESTAMP,
			agent_id VARCHAR,
			cpu_percent DOUBLE,
			memory_bytes BIGINT,
			network_rx_bytes BIGINT,
			network_tx_bytes BIGINT
		)
	`)

	// Create index on timestamp for better query performance
	s.dbHelper.Exec(db, `
		CREATE INDEX idx_ts ON timeseries_metrics(timestamp)
	`)

	// Insert time-series data
	baseTime := time.Now().Add(-1 * time.Hour)
	for i := 0; i < 100; i++ {
		timestamp := baseTime.Add(time.Duration(i) * time.Minute)
		s.dbHelper.Exec(db, `
			INSERT INTO timeseries_metrics
			VALUES (?, 'agent-1', ?, ?, ?, ?)
		`,
			timestamp,
			float64(20+i%50),            // cpu_percent
			int64(1000000000+i*1000000), // memory_bytes
			int64(i*100000),             // network_rx_bytes
			int64(i*50000),              // network_tx_bytes
		)
	}

	// Verify data count
	count := s.dbHelper.CountRows(db, "timeseries_metrics")
	s.Require().Equal(100, count)

	// Test time-range query
	var rangeCount int
	err := db.QueryRow(`
		SELECT COUNT(*)
		FROM timeseries_metrics
		WHERE timestamp >= ? AND timestamp <= ?
	`, baseTime, baseTime.Add(30*time.Minute)).Scan(&rangeCount)
	s.Require().NoError(err)
	s.Require().Equal(31, rangeCount, "Expected 31 data points in 30-minute range")

	// Test aggregation query
	var avgCPU, maxMemory float64
	err = db.QueryRow(`
		SELECT
			AVG(cpu_percent),
			MAX(memory_bytes)
		FROM timeseries_metrics
		WHERE timestamp >= ?
	`, baseTime).Scan(&avgCPU, &maxMemory)
	s.Require().NoError(err)

	s.T().Logf("Time-series query successful - Avg CPU: %.2f, Max Memory: %.0f", avgCPU, maxMemory)
}

// TestDataRetention tests data retention policies.
func (s *DuckDBE2ESuite) TestDataRetention() {
	db := s.dbHelper.CreateDB("test-retention")
	defer db.Close()

	// Create table with retention policy simulation
	s.dbHelper.Exec(db, `
		CREATE TABLE retention_test (
			id INTEGER PRIMARY KEY,
			timestamp TIMESTAMP,
			data VARCHAR
		)
	`)

	// Insert old and new data
	oldTime := time.Now().Add(-2 * time.Hour)
	newTime := time.Now().Add(-10 * time.Minute)

	for i := 0; i < 50; i++ {
		s.dbHelper.Exec(db, `INSERT INTO retention_test VALUES (?, ?, ?)`,
			i, oldTime, fmt.Sprintf("old-data-%d", i))
	}

	for i := 50; i < 100; i++ {
		s.dbHelper.Exec(db, `INSERT INTO retention_test VALUES (?, ?, ?)`,
			i, newTime, fmt.Sprintf("new-data-%d", i))
	}

	// Verify initial count
	totalCount := s.dbHelper.CountRows(db, "retention_test")
	s.Require().Equal(100, totalCount)

	// Simulate retention cleanup (delete data older than 1 hour)
	retentionThreshold := time.Now().Add(-1 * time.Hour)
	s.dbHelper.Exec(db, `
		DELETE FROM retention_test
		WHERE timestamp < ?
	`, retentionThreshold)

	// Verify only new data remains
	remainingCount := s.dbHelper.CountRows(db, "retention_test")
	s.Require().Equal(50, remainingCount, "Only recent data should remain")

	s.T().Log("Data retention policy test successful")
}

// TestJSONDataStorage tests storing and querying JSON data.
func (s *DuckDBE2ESuite) TestJSONDataStorage() {
	db := s.dbHelper.CreateDB("test-json")
	defer db.Close()

	// Create table with JSON column
	s.dbHelper.Exec(db, `
		CREATE TABLE json_data (
			id INTEGER PRIMARY KEY,
			agent_id VARCHAR,
			metadata JSON,
			created_at TIMESTAMP
		)
	`)

	// Insert JSON data
	s.dbHelper.Exec(db, `
		INSERT INTO json_data (id, agent_id, metadata, created_at)
		VALUES
			(1, 'agent-1', '{"env": "prod", "region": "us-east-1", "version": "1.0"}', CURRENT_TIMESTAMP),
			(2, 'agent-2', '{"env": "staging", "region": "us-west-2", "version": "1.1"}', CURRENT_TIMESTAMP),
			(3, 'agent-3', '{"env": "prod", "region": "eu-west-1", "version": "1.0"}', CURRENT_TIMESTAMP)
	`)

	// Query using JSON extraction
	var count int
	err := db.QueryRow(`
		SELECT COUNT(*)
		FROM json_data
		WHERE json_extract_string(metadata, '$.env') = 'prod'
	`).Scan(&count)
	s.Require().NoError(err)
	s.Require().Equal(2, count, "Expected 2 prod environments")

	s.T().Log("JSON data storage and querying successful")
}

// TestConcurrentWrites tests concurrent writes to DuckDB.
func (s *DuckDBE2ESuite) TestConcurrentWrites() {
	s.T().Skip("Concurrent writes - DuckDB concurrency behavior needs verification")

	// TODO: Test concurrent write patterns
	// DuckDB has specific concurrency characteristics that need testing:
	// 1. Multiple readers
	// 2. Single writer (typically)
	// 3. WAL mode behavior
}

// TestOTELDataIngestion tests OpenTelemetry data ingestion.
func (s *DuckDBE2ESuite) TestOTELDataIngestion() {
	s.T().Skip("OTEL data ingestion - implementation pending")

	// TODO: Implement when OTEL integration is complete
	// This test should:
	// 1. Start colony with OTEL receiver
	// 2. Send OTEL traces/metrics/logs
	// 3. Verify data is stored in DuckDB
	// 4. Query and validate stored OTEL data
	// 5. Test different OTEL data types (traces, metrics, logs)
}

// TestDataExport tests exporting data from DuckDB.
func (s *DuckDBE2ESuite) TestDataExport() {
	db := s.dbHelper.CreateDB("test-export")
	defer db.Close()

	// Create and populate table
	s.dbHelper.Exec(db, `
		CREATE TABLE export_test (
			id INTEGER,
			name VARCHAR,
			value DOUBLE
		)
	`)

	s.dbHelper.Exec(db, `
		INSERT INTO export_test VALUES
			(1, 'metric-1', 100.5),
			(2, 'metric-2', 200.7),
			(3, 'metric-3', 300.9)
	`)

	// Export to CSV
	csvPath := s.GetTestDataDir("export") + "/export.csv"
	s.dbHelper.Exec(db, fmt.Sprintf(`
		COPY export_test TO '%s' (FORMAT CSV, HEADER)
	`, csvPath))

	// Verify CSV was created
	s.FileExists(csvPath, "CSV export file should exist")

	// Export to Parquet
	parquetPath := s.GetTestDataDir("export") + "/export.parquet"
	s.dbHelper.Exec(db, fmt.Sprintf(`
		COPY export_test TO '%s' (FORMAT PARQUET)
	`, parquetPath))

	// Verify Parquet was created
	s.FileExists(parquetPath, "Parquet export file should exist")

	s.T().Log("Data export test successful")
}
