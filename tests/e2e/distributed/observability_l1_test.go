package distributed

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/coral-mesh/coral/tests/e2e/distributed/fixtures"
	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// ObservabilityL1Suite tests Level 1 - OTLP Telemetry observability.
type ObservabilityL1Suite struct {
	E2EDistributedSuite
}

// TestObservabilityL1Suite runs the Level 1 observability test suite.
func TestObservabilityL1Suite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping observability L1 tests in short mode")
	}

	suite.Run(t, new(ObservabilityL1Suite))
}

// TestLevel1_OTLPIngestion verifies that the agent receives and stores OTLP telemetry.
//
// Test flow:
// 1. Start agent container with OTLP receiver enabled
// 2. Start OTLP test app container configured to send traces to agent
// 3. Generate HTTP traffic to test app to create telemetry data
// 4. Verify the test app is responsive and generating traces
//
// Note: Full verification of trace storage in agent DuckDB will be added
// once agent telemetry query APIs are exposed for testing.
func (s *ObservabilityL1Suite) TestLevel1_OTLPIngestion() {
	s.T().Log("Testing OTLP ingestion flow...")

	// Create fixture with agent and OTLP app.
	// The OTLP app is configured to send traces to agent-0 via env var.
	fixture, err := fixtures.NewContainerFixture(s.ctx, fixtures.FixtureOptions{
		NumAgents:   1,
		WithOTELApp: true,
	})
	s.Require().NoError(err, "Failed to create container fixture")
	defer func() {
		if fixture != nil {
			_ = fixture.Cleanup(s.ctx)
		}
	}()

	// Get OTLP app endpoint.
	otlpAppEndpoint, err := fixture.GetOTELAppEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get OTLP app endpoint")

	s.T().Logf("OTLP app listening at: %s", otlpAppEndpoint)

	// Verify OTLP app is responsive.
	s.T().Log("Verifying OTLP app is responsive...")
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(fmt.Sprintf("http://%s/health", otlpAppEndpoint))
	s.Require().NoError(err, "OTLP app health check should succeed")
	defer resp.Body.Close()
	s.Require().Equal(http.StatusOK, resp.StatusCode, "OTLP app should be healthy")

	s.T().Log("OTLP app is healthy and ready")

	// Generate HTTP traffic to different endpoints to create telemetry data.
	// The OTLP app instruments these endpoints with OpenTelemetry.
	endpoints := []string{
		"/api/users",
		"/api/products",
		"/api/checkout",
	}

	s.T().Log("Generating HTTP traffic to create telemetry data...")
	requestCount := 0

	for i := 0; i < 10; i++ {
		for _, endpoint := range endpoints {
			url := fmt.Sprintf("http://%s%s", otlpAppEndpoint, endpoint)

			resp, err := client.Get(url)
			if err != nil {
				s.T().Logf("Request to %s failed: %v (may be expected for error simulation)", url, err)
				continue
			}

			requestCount++
			s.T().Logf("Request %d: %s → status %d", requestCount, endpoint, resp.StatusCode)
			_ = resp.Body.Close()
		}

		// Small delay between request batches.
		time.Sleep(100 * time.Millisecond)
	}

	s.T().Logf("Generated %d requests across %d endpoints", requestCount, len(endpoints))

	// Wait for OTLP spans to be sent to agent and processed.
	s.T().Log("Waiting for OTLP spans to be processed by agent...")
	time.Sleep(3 * time.Second)

	// Query agent's telemetry storage to verify spans were ingested.
	s.T().Log("Querying agent for ingested telemetry spans...")

	agentEndpoint, err := fixture.GetAgentGRPCEndpoint(s.ctx, 0)
	s.Require().NoError(err, "Failed to get agent gRPC endpoint")

	agentClient := helpers.NewAgentClient(agentEndpoint)

	// Query spans from the last 5 minutes.
	now := time.Now()
	telemetryResp, err := helpers.QueryAgentTelemetry(
		s.ctx,
		agentClient,
		now.Add(-5*time.Minute).Unix(),
		now.Unix(),
		[]string{"otel-app"}, // Filter by our test app service name
	)
	s.Require().NoError(err, "Failed to query telemetry from agent")

	// Verify spans were ingested.
	s.Require().Greater(int(telemetryResp.TotalSpans), 0,
		"Expected telemetry spans to be ingested, got 0")

	s.T().Logf("✓ Verified %d spans ingested by agent", telemetryResp.TotalSpans)

	// Verify span properties.
	if len(telemetryResp.Spans) > 0 {
		span := telemetryResp.Spans[0]
		s.Require().Equal("otel-app", span.ServiceName,
			"Span should be from otel-app service")
		s.Require().NotEmpty(span.SpanId, "Span should have span ID")
		s.Require().NotEmpty(span.TraceId, "Span should have trace ID")

		s.T().Logf("  Sample span: service=%s method=%s route=%s duration=%.2fms",
			span.ServiceName, span.HttpMethod, span.HttpRoute, span.DurationMs)
	}

	s.T().Log("✓ OTLP ingestion verified end-to-end")
	s.T().Log("  - OTLP app sends telemetry to agent")
	s.T().Log("  - Agent OTLP receiver processes spans")
	s.T().Log("  - Spans stored in agent's local DuckDB")
	s.T().Log("  - Spans queryable via gRPC API")
}

// TestLevel1_OTELAppEndpoints verifies the OTEL test app endpoints work correctly.
func (s *ObservabilityL1Suite) TestLevel1_OTELAppEndpoints() {
	s.T().Log("Testing OTEL app endpoint functionality...")

	fixture, err := fixtures.NewContainerFixture(s.ctx, fixtures.FixtureOptions{
		NumAgents:   1,
		WithOTELApp: true,
	})
	s.Require().NoError(err, "Failed to create container fixture")
	defer func() {
		if fixture != nil {
			_ = fixture.Cleanup(s.ctx)
		}
	}()

	otlpAppEndpoint, err := fixture.GetOTELAppEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get OTLP app endpoint")

	client := &http.Client{Timeout: 5 * time.Second}

	// Test each endpoint.
	testCases := []struct {
		endpoint       string
		expectedStatus int
	}{
		{"/health", http.StatusOK},
		{"/api/users", http.StatusOK},
		{"/api/products", http.StatusOK},
		{"/api/checkout", http.StatusOK}, // May sometimes return 500 for error simulation
	}

	for _, tc := range testCases {
		url := fmt.Sprintf("http://%s%s", otlpAppEndpoint, tc.endpoint)

		resp, err := client.Get(url)
		s.Require().NoError(err, "Request to %s should succeed", tc.endpoint)
		defer resp.Body.Close()

		// For /api/checkout, we expect either 200 (success) or 500 (simulated error).
		if tc.endpoint == "/api/checkout" {
			s.Require().Contains([]int{http.StatusOK, http.StatusInternalServerError},
				resp.StatusCode,
				"Checkout endpoint should return 200 or 500")
		} else {
			s.Require().Equal(tc.expectedStatus, resp.StatusCode,
				"Endpoint %s should return %d", tc.endpoint, tc.expectedStatus)
		}

		s.T().Logf("✓ %s → status %d", tc.endpoint, resp.StatusCode)
	}

	s.T().Log("All OTEL app endpoints validated")
}
