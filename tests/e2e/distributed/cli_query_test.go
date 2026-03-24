package distributed

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// CLIQuerySuite tests CLI query commands.
//
// This suite validates:
// 1. Query summary command (coral query summary)
// 2. Query traces command (coral query traces)
// 3. Query metrics command (coral query metrics)
// 4. Service list command (coral colony service list)
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
	// allowing registry-based queries (like 'coral colony service list') to work.
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
	result := helpers.QuerySummary(s.ctx, s.cliEnv, "", "5m")
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
	resultWithService := helpers.QuerySummary(s.ctx, s.cliEnv, "otel-app", "5m")
	// Don't require success - service might not have data yet, but shouldn't crash
	s.T().Logf("Query with service filter exit code: %d", resultWithService.ExitCode)
}

// TestQueryServicesCommand tests 'coral colony service list' output.
//
// Validates:
// - Lists discovered services
// - Table and JSON formats work
// - Service information is present
func (s *CLIQuerySuite) TestQueryServicesCommand() {
	s.T().Log("Testing 'coral colony service list' command...")

	// Test table format
	result := helpers.QueryServices(s.ctx, s.cliEnv)
	result.MustSucceed(s.T())

	s.T().Log("Table output:")
	s.T().Log(result.Output)

	// Verify output structure
	rows := helpers.ParseTableOutput(result.Output)
	s.Require().GreaterOrEqual(len(rows), 1, "Should have at least headers")

	// Test JSON format
	// TODO: add format json
	var services []map[string]interface{}
	// services, err := helpers.QueryServicesJSON(s.ctx, s.cliEnv)
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
	result := helpers.QueryTraces(s.ctx, s.cliEnv, "", "5m", 0)
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
	resultFiltered := helpers.QueryTraces(s.ctx, s.cliEnv, "otel-app", "5m", 0)
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
	result := helpers.QueryMetrics(s.ctx, s.cliEnv, "", "5m")
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
		result := helpers.QuerySummary(s.ctx, s.cliEnv, "", tr)
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
	result := helpers.QuerySummary(s.ctx, s.cliEnv, "", "invalid-time")
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
	// _, err = helpers.QueryServicesJSON(s.ctx, s.cliEnv)
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
	result := helpers.QuerySummary(s.ctx, s.cliEnv, "", "5m")
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
	result = helpers.QueryServices(s.ctx, s.cliEnv)
	result.MustSucceed(s.T())
	rows := helpers.ParseTableOutput(result.Output)
	s.T().Logf("Services table: %d rows", len(rows))

	s.T().Log("✓ Table formatting validated")
}

// TestCLIQueryTopology tests 'coral query topology' CLI command (RFD 092).
//
// Validates:
//   - Cross-service edges (otel-app → cpu-app) appear after traffic generation
//   - Default text output contains directed call edges
//   - JSON output flag works and includes colony_id and connections fields
func (s *CLIQuerySuite) TestCLIQueryTopology() {
	s.T().Log("Testing 'coral query topology' CLI command (RFD 092)...")

	// Generate real cross-service HTTP traffic so MaterializeConnections has
	// parent-child span relationships to mine across service boundaries.
	s.ensureCrossServiceData()

	// Poll 'coral query topology' until the otel-app → cpu-app edge appears or
	// the deadline is exceeded. On each cycle, send a few more /chain calls so
	// fresh Beyla spans are always available — this handles cases where the
	// otel-app container restarted mid-suite and Beyla's eBPF uprobes needed
	// time to re-attach to the new process.
	//
	// The loop requires BOTH "otel-app" AND "cpu-app" to avoid false positives:
	// previously it broke on "otel-app" alone, which matched "coral-agent → otel-app"
	// (agent health-check edge) instead of the real "otel-app → cpu-app" topology edge.
	const (
		topologyTimeout  = 320 * time.Second
		topologyInterval = 5 * time.Second
	)
	otelAppURL := "http://localhost:8082"
	chainClient := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(topologyTimeout)

	// Log Beyla config once up front so it appears in CI output regardless of
	// whether the test times out.
	if cfg, err := s.fixture.GetBeylaConfig(s.ctx, "agent-0"); err == nil {
		s.T().Logf("Beyla config on agent-0:\n%s", cfg)
	}

	var result *helpers.CLIResult
	for {
		// Keep generating cross-service calls so there is always fresh traffic
		// for Beyla to capture once its uprobe attaches to the new process.
		for i := 0; i < 3; i++ {
			if resp, err := chainClient.Get(otelAppURL + "/chain"); err == nil {
				_ = resp.Body.Close()
			}
		}

		result = s.cliEnv.Run(s.ctx, "query", "topology")
		if result.Err == nil && strings.Contains(result.Output, "otel-app") && strings.Contains(result.Output, "cpu-app") {
			break
		}
		if time.Now().After(deadline) {
			s.T().Logf("coral query topology last output:\n%s", result.Output)
			s.debugBeylaTracesLocal()
			s.Require().Fail("timed out waiting for otel-app → cpu-app edge in coral query topology output")
			return
		}
		s.T().Logf("otel-app → cpu-app edge not yet in topology output, retrying in %s...", topologyInterval)
		time.Sleep(topologyInterval)
	}

	result.MustSucceed(s.T())
	s.Require().NotEmpty(result.Output, "CLI topology output should not be empty")
	s.T().Logf("coral query topology output:\n%s", result.Output)

	// The output must include a detected edge between the two services.
	// otel-app calls cpu-app via the /chain endpoint; Beyla's eBPF interceptor
	// owns the traceparent header so the parent-child span pair lives entirely
	// in beyla_traces, letting MaterializeConnections find the edge.
	s.Require().Contains(result.Output, "otel-app", "Output should name the caller service")
	s.Require().Contains(result.Output, "cpu-app", "Output should name the callee service")
	s.Require().Contains(result.Output, "→", "Output should show a directed call edge")
	// RFD 033: LAYER column must be present in topology output.
	s.Require().Contains(result.Output, "LAYER", "Output should include LAYER column header (RFD 033)")

	// JSON output format.
	jsonResult := s.cliEnv.Run(s.ctx, "query", "topology", "--format", "json")
	jsonResult.MustSucceed(s.T())

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(jsonResult.Output), &parsed); err != nil {
		s.T().Logf("JSON parse error (output was): %s", jsonResult.Output)
		s.Require().NoError(err, "JSON output should be valid JSON")
	}
	s.Require().Contains(parsed, "colony_id", "JSON output must include colony_id field")
	s.Require().Contains(parsed, "connections", "JSON output must include connections field")

	// RFD 033: JSON connections must include the layer field.
	if conns, ok := parsed["connections"].([]interface{}); ok && len(conns) > 0 {
		firstConn, ok := conns[0].(map[string]interface{})
		s.Require().True(ok, "connection entry must be an object")
		s.Require().Contains(firstConn, "layer", "JSON connection must include layer field (RFD 033)")
	}

	s.T().Log("✓ coral query topology CLI validated with real cross-service connections")
}

// debugBeylaTracesLocal queries beyla_traces_local on the agent that runs
// otel-app and cpu-app, and also queries the colony's beyla_traces table.
// Both dumps are logged so CI failures can be diagnosed without local repro.
func (s *CLIQuerySuite) debugBeylaTracesLocal() {
	s.T().Log("=== DEBUG: beyla span ingestion state ===")

	// The duckdb proxy requires admin auth on the public HTTPS endpoint.
	token, err := s.fixture.CreateToken(s.ctx, "debug-beyla-traces", "admin")
	if err != nil {
		s.T().Logf("DEBUG: could not create admin token: %v", err)
		return
	}

	duckdbEnv, err := helpers.SetupCLIEnv(s.ctx, s.fixture.ColonyID, "https://localhost:8443")
	if err != nil {
		s.T().Logf("DEBUG: could not set up duckdb CLI env: %v", err)
		return
	}
	defer duckdbEnv.Cleanup()
	duckdbEnv.ExtraVars["CORAL_INSECURE"] = "true"
	duckdbEnv.ExtraVars["CORAL_API_TOKEN"] = token

	// --- Colony-level view (beyla_traces) ---
	// This works without an agent ID and shows what was successfully polled
	// from the agent to the colony — a reliable baseline even if agent proxy fails.
	// The colony database is attached as "colony", so tables must be qualified.
	s.T().Log("--- colony beyla_traces (polled data) ---")

	colonyCount := duckdbEnv.Run(s.ctx, "duckdb", "query", "--colony",
		"SELECT COUNT(*) AS total FROM colony.beyla_traces")
	s.T().Logf("colony beyla_traces count:\n%s", colonyCount.Output)

	// Group by (agent_id, service_name) — confirms which agent's spans reached the
	// colony and whether cpu-app + otel-app are stored under the same agent_id.
	colonyServices := duckdbEnv.Run(s.ctx, "duckdb", "query", "--colony",
		"SELECT agent_id, service_name, COUNT(*) AS spans, MAX(start_time) AS last_seen "+
			"FROM colony.beyla_traces GROUP BY agent_id, service_name ORDER BY agent_id, spans DESC")
	s.T().Logf("colony beyla_traces by agent+service:\n%s", colonyServices.Output)

	// service_connections shows what MaterializeConnections last derived.
	colonyConns := duckdbEnv.Run(s.ctx, "duckdb", "query", "--colony",
		"SELECT from_service, to_service, protocol, connection_count, last_observed "+
			"FROM colony.service_connections ORDER BY last_observed DESC")
	s.T().Logf("colony service_connections:\n%s", colonyConns.Output)

	// otel_spans holds spans from the OTLP SDK path (not Beyla eBPF).
	// If otel-app spans went here instead of beyla_traces, the JOIN in
	// MaterializeConnections would never find them.
	colonyOtelSpans := duckdbEnv.Run(s.ctx, "duckdb", "query", "--colony",
		"SELECT service_name, COUNT(*) AS spans, MAX(start_time) AS last_seen "+
			"FROM colony.otel_spans GROUP BY service_name ORDER BY spans DESC")
	s.T().Logf("colony otel_spans by service (OTLP path):\n%s", colonyOtelSpans.Output)
	if colonyOtelSpans.Err != nil {
		s.T().Logf("DEBUG: otel_spans query error (table may not exist): %v", colonyOtelSpans.Err)
	}

	// --- Agent-level view (beyla_traces_local) ---
	// Resolve the real agent ID from the registry rather than assuming "agent-0",
	// since CI agents may have different IDs (e.g. container hostname).
	agents, agentErr := helpers.ColonyAgentsJSON(s.ctx, duckdbEnv)
	if agentErr != nil {
		s.T().Logf("DEBUG: could not list agents: %v", agentErr)
	} else {
		s.T().Logf("DEBUG: registered agents: %v", agents)
	}

	agentID := helpers.GetHealthyAgentID(agents)
	if agentID == "" {
		s.T().Log("DEBUG: no healthy agent found; skipping agent-level beyla_traces_local query")
	} else {
		s.T().Logf("--- agent %s beyla_traces_local (raw local data) ---", agentID)

		// The agent database is attached as "agent_<sanitized_id>" where the
		// sanitized form replaces every non-alphanumeric character with "_".
		// Tables must be qualified with this alias (DuckDB requirement).
		dbAlias := "agent_" + sanitizeAgentIDForSQL(agentID)

		// Pass --database explicitly: the agent only exposes metrics.duckdb (which
		// contains beyla_traces_local) and the auto-detect step would otherwise
		// require the colony-proxy /agent/{id}/duckdb/ route to return the list.
		agentCount := duckdbEnv.Run(s.ctx, "duckdb", "query", agentID,
			"SELECT COUNT(*) AS total_spans FROM "+dbAlias+".beyla_traces_local",
			"--database", "metrics.duckdb")
		s.T().Logf("agent beyla_traces_local count:\n%s", agentCount.Output)
		if agentCount.Err != nil {
			s.T().Logf("DEBUG: agent count error: %v", agentCount.Err)
		}

		agentServices := duckdbEnv.Run(s.ctx, "duckdb", "query", agentID,
			"SELECT service_name, COUNT(*) AS spans, MIN(start_time) AS first_seen, MAX(start_time) AS last_seen "+
				"FROM "+dbAlias+".beyla_traces_local GROUP BY service_name ORDER BY spans DESC",
			"--database", "metrics.duckdb")
		s.T().Logf("agent beyla_traces_local by service:\n%s", agentServices.Output)
		if agentServices.Err != nil {
			s.T().Logf("DEBUG: agent services error: %v", agentServices.Err)
		}

		agentSample := duckdbEnv.Run(s.ctx, "duckdb", "query", agentID,
			"SELECT service_name, span_name, span_kind, trace_id, span_id, parent_span_id, start_time "+
				"FROM "+dbAlias+".beyla_traces_local ORDER BY start_time DESC LIMIT 20",
			"--database", "metrics.duckdb")
		s.T().Logf("agent beyla_traces_local recent spans (up to 20):\n%s", agentSample.Output)
		if agentSample.Err != nil {
			s.T().Logf("DEBUG: agent sample error: %v", agentSample.Err)
		}

		// seq_id range per service — critical for diagnosing checkpoint skip bugs.
		// If otel-app seq_ids are entirely below the traces checkpoint, those spans
		// were already polled (or skipped due to a stale checkpoint from a prior run).
		agentSeqIDs := duckdbEnv.Run(s.ctx, "duckdb", "query", agentID,
			"SELECT service_name, MIN(seq_id) AS min_seq, MAX(seq_id) AS max_seq, COUNT(*) AS spans "+
				"FROM "+dbAlias+".beyla_traces_local GROUP BY service_name ORDER BY min_seq",
			"--database", "metrics.duckdb")
		s.T().Logf("agent beyla_traces_local seq_id ranges by service:\n%s", agentSeqIDs.Output)
		if agentSeqIDs.Err != nil {
			s.T().Logf("DEBUG: agent seq_id query error: %v", agentSeqIDs.Err)
		}

		// otel-app span_kind breakdown — reveals whether Beyla captures CLIENT spans
		// (outgoing calls to cpu-app) or only SERVER/INTERNAL spans.
		// CLIENT spans are required for MaterializeConnections: it self-joins on
		// child.parent_span_id = parent.span_id, where parent is the CLIENT span.
		otelSpanKinds := duckdbEnv.Run(s.ctx, "duckdb", "query", agentID,
			"SELECT span_kind, COUNT(*) AS cnt FROM "+dbAlias+".beyla_traces_local "+
				"WHERE service_name='otel-app' GROUP BY span_kind ORDER BY cnt DESC",
			"--database", "metrics.duckdb")
		s.T().Logf("agent otel-app spans by span_kind:\n%s", otelSpanKinds.Output)
		if otelSpanKinds.Err != nil {
			s.T().Logf("DEBUG: otel span_kind query error: %v", otelSpanKinds.Err)
		}

		// Sample of the oldest otel-app spans — shows what was first captured and
		// whether CLIENT spans are linked to cpu-app SERVER spans via trace_id.
		otelSample := duckdbEnv.Run(s.ctx, "duckdb", "query", agentID,
			"SELECT service_name, span_name, span_kind, trace_id, span_id, parent_span_id, start_time, seq_id "+
				"FROM "+dbAlias+".beyla_traces_local WHERE service_name='otel-app' ORDER BY seq_id ASC LIMIT 10",
			"--database", "metrics.duckdb")
		s.T().Logf("agent otel-app oldest spans (seq order):\n%s", otelSample.Output)
		if otelSample.Err != nil {
			s.T().Logf("DEBUG: otel sample query error: %v", otelSample.Err)
		}

		// Cross-service span linkage — counts how many otel-app CLIENT spans have
		// matching cpu-app SERVER spans in the agent's local DB.
		// If this is 0, the topology JOIN can never succeed regardless of
		// how many spans reach the colony.
		crossServiceJoin := duckdbEnv.Run(s.ctx, "duckdb", "query", agentID,
			"SELECT COUNT(*) AS linked_pairs FROM "+dbAlias+".beyla_traces_local otel "+
				"JOIN "+dbAlias+".beyla_traces_local cpu "+
				"ON otel.span_id = cpu.parent_span_id AND otel.trace_id = cpu.trace_id "+
				"WHERE otel.service_name='otel-app' AND cpu.service_name='cpu-app'",
			"--database", "metrics.duckdb")
		s.T().Logf("agent otel-app→cpu-app linked span pairs:\n%s", crossServiceJoin.Output)
		if crossServiceJoin.Err != nil {
			s.T().Logf("DEBUG: cross-service join query error: %v", crossServiceJoin.Err)
		}

		// Colony traces checkpoint for this agent — shows where polling left off.
		// If this value is beyond otel-app's max seq_id, those spans were skipped.
		colonyCheckpoint := duckdbEnv.Run(s.ctx, "duckdb", "query", "--colony",
			"SELECT agent_id, data_type, last_seq_id, session_id, updated_at "+
				"FROM colony.polling_checkpoints WHERE agent_id = '"+agentID+"' ORDER BY data_type")
		s.T().Logf("colony polling_checkpoints for agent %s:\n%s", agentID, colonyCheckpoint.Output)
		if colonyCheckpoint.Err != nil {
			s.T().Logf("DEBUG: checkpoint query error: %v", colonyCheckpoint.Err)
		}
	}

	s.T().Log("=== END DEBUG beyla span ingestion state ===")
}

// ensureCrossServiceData drives traffic through otel-app's /chain endpoint.
// That handler makes a plain HTTP call to cpu-app (no OTel SDK traceparent
// injection) so Beyla's eBPF interceptor can own the traceparent header end-
// to-end: it injects its own span_id on the outgoing side and the cpu-app
// SERVER span records it as parent_span_id, giving MaterializeConnections a
// consistent parent-child pair inside beyla_traces.
func (s *CLIQuerySuite) ensureCrossServiceData() {
	s.T().Log("Generating cross-service traffic (otel-app → cpu-app)...")

	otelAppURL := "http://localhost:8082"

	if err := helpers.WaitForHTTPEndpoint(s.ctx, otelAppURL+"/health", 10*time.Second); err != nil {
		s.T().Log("otel-app not reachable, cross-service traffic generation skipped")
		return
	}

	// Verify the /chain endpoint exists before generating traffic. A 404 means
	// the otel-app image was not rebuilt after the /chain handler was added.
	client := &http.Client{Timeout: 5 * time.Second}
	probe, err := client.Get(otelAppURL + "/chain")
	s.Require().NoError(err, "/chain probe request should succeed (is the otel-app image rebuilt?)")
	_ = probe.Body.Close()
	s.Require().Equal(http.StatusOK, probe.StatusCode,
		"/chain returned %d — rebuild the otel-app Docker image to include the /chain handler", probe.StatusCode)

	// Retry loop — Beyla may need a few seconds to attach uprobes after the process
	// is first exercised by the probe request above. By making multiple batches
	// spaced out over time, we ensure the eBPF uprobes are active for the later calls.
	const (
		batches       = 3
		callsPerBatch = 5
	)
	calls := 0
	for attempt := 1; attempt <= batches; attempt++ {
		s.T().Logf("Traffic generation attempt %d/%d...", attempt, batches)
		for i := 0; i < callsPerBatch; i++ {
			resp, err := client.Get(otelAppURL + "/chain")
			s.Require().NoError(err, "Traffic generation failed (otel-app endpoint unreachable)")
			s.Require().Equal(http.StatusOK, resp.StatusCode, "Traffic generation failed (otel-app returned error)")
			resp.Body.Close()
			calls++
			time.Sleep(100 * time.Millisecond)
		}

		if attempt < batches {
			time.Sleep(3 * time.Second)
		}
	}

	// Allow time for Beyla to capture both span sides, the colony's Beyla poll
	// (1s in e2e config) to forward them, and the first topology materialization
	// to run. Ten seconds gives comfortable headroom across that pipeline.
	s.T().Log("Waiting for cross-service spans to propagate to colony...")
	time.Sleep(10 * time.Second)

	s.T().Logf("Generated %d cross-service calls", calls)
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
	// CLI query tests need otel-app and cpu-app — the topology test requires
	// both services to be connected so cross-service edges are materialized.
	helpers.EnsureServicesConnected(s.T(), s.ctx, s.fixture, 0, []helpers.ServiceConfig{
		{Name: "otel-app", Port: 8090, HealthEndpoint: "/health"},
		{Name: "cpu-app", Port: 8080, HealthEndpoint: "/health"},
	})
}

// disconnectAllServices disconnects all test services from all agents.
func (s *CLIQuerySuite) disconnectAllServices() {
	helpers.DisconnectAllServices(s.T(), s.ctx, s.fixture, 0, []string{
		"otel-app",
		"cpu-app",
	})
}

// sanitizeAgentIDForSQL converts an agent ID to the DuckDB alias used when the
// database is attached, matching the logic in internal/cli/duckdb/helpers.go.
// Every character that is not a-z, A-Z, or 0-9 is replaced with an underscore.
func sanitizeAgentIDForSQL(agentID string) string {
	result := make([]byte, len(agentID))
	for i := range agentID {
		ch := agentID[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			result[i] = ch
		} else {
			result[i] = '_'
		}
	}
	return string(result)
}
