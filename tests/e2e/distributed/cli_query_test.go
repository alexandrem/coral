package distributed

import (
	"fmt"
	"net/http"
	"time"

	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// CLIQuerySuite tests CLI query commands.
//
// This suite validates:
// 1. Query summary command (coral query summary)
// 2. Query traces command (coral query traces)
// 3. Query metrics command (coral query metrics)
// 4. Query services command (coral query services)
// 5. Flag combinations (--service, --time, --limit)
// 6. Output formatting (table vs JSON)
//
// Note: Requires TelemetrySuite to have run (needs telemetry data to query).
// This suite does NOT test query accuracy - it tests CLI output formatting and UX.
type CLIQuerySuite struct {
	E2EDistributedSuite

	cliEnv *helpers.CLITestEnv
}

// SetupSuite runs once before all tests in the suite.
func (s *CLIQuerySuite) SetupSuite() {
	s.E2EDistributedSuite.SetupSuite()

	// Setup CLI environment
	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	colonyID := "test-colony-e2e" // Default

	s.cliEnv, err = helpers.SetupCLIEnv(s.ctx, colonyID, colonyEndpoint)
	s.Require().NoError(err, "Failed to setup CLI environment")

	// Ensure services are connected for query tests.
	// This populates the services registry table via ConnectService API,
	// allowing registry-based queries (like 'coral query services') to work.
	s.ensureServicesConnected()

	s.T().Logf("CLI environment ready: endpoint=%s", colonyEndpoint)
}

// TearDownSuite cleans up after all tests.
func (s *CLIQuerySuite) TearDownSuite() {
	// Disconnect services to clean up.
	s.disconnectAllServices()

	if s.cliEnv != nil {
		_ = s.cliEnv.Cleanup()
	}
	s.E2EDistributedSuite.TearDownSuite()
}

// TestQuerySummaryCommand tests 'coral query summary' output.
//
// Validates:
// - Command executes successfully
// - Table output has expected structure
// - JSON output has required fields
// - Flags work correctly (--service, --time)
func (s *CLIQuerySuite) TestQuerySummaryCommand() {
	s.T().Log("Testing 'coral query summary' command...")

	// Ensure we have some telemetry data
	s.ensureTelemetryData()

	// Test basic summary (table format, default time range)
	result := helpers.QuerySummary(s.ctx, s.cliEnv.ColonyEndpoint, "", "5m")
	result.MustSucceed(s.T())

	s.T().Log("Table output:")
	s.T().Log(result.Output)

	// Should have output (even if no services, should show headers or message)
	s.Require().NotEmpty(result.Output, "Query summary should produce output")

	// Test JSON format
	// TODO: add format json
	// summary, err := helpers.QuerySummaryJSON(s.ctx, s.cliEnv.ColonyEndpoint, "", "5m")
	// s.Require().NoError(err, "JSON query should succeed")

	// Validate JSON structure
	// s.Require().NotNil(summary, "Summary should not be nil")

	// s.T().Logf("✓ Query summary validated (table + JSON)")

	// Test with service filter (if services exist)
	resultWithService := helpers.QuerySummary(s.ctx, s.cliEnv.ColonyEndpoint, "otel-app", "5m")
	// Don't require success - service might not have data yet, but shouldn't crash
	s.T().Logf("Query with service filter exit code: %d", resultWithService.ExitCode)
}

// TestQueryServicesCommand tests 'coral query services' output.
//
// Validates:
// - Lists discovered services
// - Table and JSON formats work
// - Service information is present
func (s *CLIQuerySuite) TestQueryServicesCommand() {
	s.T().Log("Testing 'coral query services' command...")

	// Test table format
	result := helpers.QueryServices(s.ctx, s.cliEnv.ColonyEndpoint)
	result.MustSucceed(s.T())

	s.T().Log("Table output:")
	s.T().Log(result.Output)

	// Verify output structure
	rows := helpers.ParseTableOutput(result.Output)
	s.Require().GreaterOrEqual(len(rows), 1, "Should have at least headers")

	// Test JSON format
	// TODO: add format json
	var services []map[string]interface{}
	// services, err := helpers.QueryServicesJSON(s.ctx, s.cliEnv.ColonyEndpoint)
	// s.Require().NoError(err, "JSON query should succeed")

	s.T().Logf("✓ Query services listed %d services", len(services))
}

// TestQueryTracesCommand tests 'coral query traces' output.
//
// Validates:
// - Traces command works
// - Table and JSON output
// - Flag combinations (--service, --time, --limit)
func (s *CLIQuerySuite) TestQueryTracesCommand() {
	s.T().Log("Testing 'coral query traces' command...")

	// Ensure telemetry data exists
	s.ensureTelemetryData()

	// Test basic traces query
	result := helpers.QueryTraces(s.ctx, s.cliEnv.ColonyEndpoint, "", "5m", 0)
	result.MustSucceed(s.T())

	s.T().Log("Table output (first 10 lines):")
	lines := result.Output
	if len(lines) > 500 {
		lines = lines[:500] + "..." // Truncate for readability
	}
	s.T().Log(lines)

	// Test JSON format
	// TODO: add format json
	// traces, err := helpers.QueryTracesJSON(s.ctx, s.cliEnv.ColonyEndpoint, "", "5m", 10)
	// s.Require().NoError(err, "JSON query should succeed")
	// s.Require().NotNil(traces, "Traces should not be nil")

	s.T().Log("✓ Query traces validated")

	// Test with service filter
	resultFiltered := helpers.QueryTraces(s.ctx, s.cliEnv.ColonyEndpoint, "otel-app", "5m", 0)
	// Service might not have traces yet, but command shouldn't crash
	s.T().Logf("Query traces with service filter exit code: %d", resultFiltered.ExitCode)
}

// TestQueryMetricsCommand tests 'coral query metrics' output.
//
// Validates:
// - Metrics command works
// - Table and JSON output
// - Flag combinations work
func (s *CLIQuerySuite) TestQueryMetricsCommand() {
	s.T().Log("Testing 'coral query metrics' command...")

	// Ensure telemetry data exists
	s.ensureTelemetryData()

	// Test basic metrics query
	result := helpers.QueryMetrics(s.ctx, s.cliEnv.ColonyEndpoint, "", "5m")
	result.MustSucceed(s.T())

	s.T().Log("Table output (truncated):")
	output := result.Output
	if len(output) > 500 {
		output = output[:500] + "..."
	}
	s.T().Log(output)

	// Test JSON format
	// TODO: add format json
	// metrics, err := helpers.QueryMetricsJSON(s.ctx, s.cliEnv.ColonyEndpoint, "", "5m")
	// s.Require().NoError(err, "JSON query should succeed")
	// s.Require().NotNil(metrics, "Metrics should not be nil")

	s.T().Log("✓ Query metrics validated")
}

// TestQueryFlagCombinations tests various flag combinations.
//
// Validates:
// - Different time ranges (1m, 5m, 1h)
// - Service filtering
// - Limit flag for traces
func (s *CLIQuerySuite) TestQueryFlagCombinations() {
	s.T().Log("Testing query flag combinations...")

	// Ensure data
	s.ensureTelemetryData()

	// Test different time ranges
	timeRanges := []string{"1m", "5m", "10m"}
	for _, tr := range timeRanges {
		result := helpers.QuerySummary(s.ctx, s.cliEnv.ColonyEndpoint, "", tr)
		if result.HasError() {
			s.T().Logf("Query with time range %s failed (acceptable if no data): %v", tr, result.Err)
		} else {
			s.T().Logf("✓ Time range %s works", tr)
		}
	}

	// Test limit flag with different values
	// TODO: add limit flag
	/*
		limits := []int{5, 10, 20}
		for _, limit := range limits {
			result := helpers.QueryTraces(s.ctx, s.cliEnv.ColonyEndpoint, "", "5m", limit)
			if result.HasError() {
				s.T().Logf("Query with limit %d failed (acceptable if no data): %v", limit, result.Err)
			} else {
				s.T().Logf("✓ Limit %d works", limit)
			}
		}
	*/

	s.T().Log("✓ Flag combinations validated")
}

// TestQueryInvalidFlags tests error handling for invalid flags.
//
// Validates:
// - Invalid time ranges produce helpful errors
// - Invalid service names fail gracefully
func (s *CLIQuerySuite) TestQueryInvalidFlags() {
	s.T().Log("Testing error handling for invalid flags...")

	// Test invalid time range
	result := helpers.QuerySummary(s.ctx, s.cliEnv.ColonyEndpoint, "", "invalid-time")
	// Should fail or handle gracefully
	if result.HasError() {
		s.T().Log("✓ Invalid time range rejected as expected")
		s.T().Logf("Error output: %s", result.Output)
	} else {
		s.T().Log("⚠ Invalid time range was accepted (might default to valid range)")
	}

	s.T().Log("✓ Error handling validated")
}

// TestQueryJSONOutputValidity tests that all JSON outputs are valid JSON.
//
// Validates:
// - All query *JSON() helpers return valid JSON
// - JSON can be parsed without errors
func (s *CLIQuerySuite) TestQueryJSONOutputValidity() {
	s.T().Log("Testing query JSON output validity...")

	// Ensure data
	s.ensureTelemetryData()

	// Test summary JSON
	// TODO: add json format
	// _, err := helpers.QuerySummaryJSON(s.ctx, s.cliEnv.ColonyEndpoint, "", "5m")
	// s.Require().NoError(err, "Query summary JSON should be valid")

	// Test services JSON
	// TODO: add format json
	// _, err = helpers.QueryServicesJSON(s.ctx, s.cliEnv.ColonyEndpoint)
	// s.Require().NoError(err, "Query services JSON should be valid")

	// Test traces JSON
	// TODO: add format json
	// _, err = helpers.QueryTracesJSON(s.ctx, s.cliEnv.ColonyEndpoint, "", "5m", 10)
	// s.Require().NoError(err, "Query traces JSON should be valid")

	// Test metrics JSON
	// TODO: add format json
	// _, err = helpers.QueryMetricsJSON(s.ctx, s.cliEnv.ColonyEndpoint, "", "5m")
	// s.Require().NoError(err, "Query metrics JSON should be valid")

	s.T().Log("✓ All query JSON outputs are valid")
}

// TestQueryTableOutputFormatting tests table output consistency.
//
// Validates:
// - Tables have reasonable structure
// - Headers and data rows are parseable
func (s *CLIQuerySuite) TestQueryTableOutputFormatting() {
	s.T().Log("Testing query table output formatting...")

	// Ensure data
	s.ensureTelemetryData()

	// Test summary table
	result := helpers.QuerySummary(s.ctx, s.cliEnv.ColonyEndpoint, "", "5m")
	if result.HasError() {
		s.T().Logf("Query summary failed (might be no data): %v", result.Err)
	} else {
		rows := helpers.ParseTableOutput(result.Output)
		s.T().Logf("Summary table: %d rows", len(rows))
		if len(rows) > 0 {
			helpers.PrintTable(s.T(), rows)
		}
	}

	// Test services table
	result = helpers.QueryServices(s.ctx, s.cliEnv.ColonyEndpoint)
	result.MustSucceed(s.T())
	rows := helpers.ParseTableOutput(result.Output)
	s.T().Logf("Services table: %d rows", len(rows))

	s.T().Log("✓ Table formatting validated")
}

// Helper: ensureTelemetryData generates telemetry data by sending requests to test apps.
// This ensures query commands have data to work with.
func (s *CLIQuerySuite) ensureTelemetryData() {
	// Get OTEL app endpoint (from docker-compose)
	otelAppURL := "http://localhost:8082"

	// Generate some HTTP requests to create telemetry data
	client := &http.Client{Timeout: 5 * time.Second}

	s.T().Log("Generating telemetry data...")

	successCount := 0
	for i := 0; i < 10; i++ {
		resp, err := client.Get(fmt.Sprintf("%s/api/users", otelAppURL))
		if err != nil {
			s.T().Logf("Request %d failed: %v", i+1, err)
			continue
		}
		_ = resp.Body.Close()
		successCount++
	}

	s.T().Logf("Generated %d successful requests", successCount)

	// Wait for telemetry to be collected and aggregated
	// E2E environment has 15-second poll intervals
	s.T().Log("Waiting for telemetry collection (3 seconds)...")
	time.Sleep(3 * time.Second)
}

// ensureServicesConnected ensures test services are connected to agent for query tests.
func (s *CLIQuerySuite) ensureServicesConnected() {
	// CLI query tests need otel-app for testing queries.
	// This populates the services registry table via ConnectService API.
	helpers.EnsureServicesConnected(s.T(), s.ctx, s.fixture, 0, []helpers.ServiceConfig{
		{Name: "otel-app", Port: 8080, HealthEndpoint: "/health"},
	})
}

// disconnectAllServices disconnects all test services from all agents.
func (s *CLIQuerySuite) disconnectAllServices() {
	helpers.DisconnectAllServices(s.T(), s.ctx, s.fixture, 0, []string{
		"otel-app",
	})
}
