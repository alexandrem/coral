package distributed

import (
	"fmt"
	"net/http"
	"time"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// triageDegradedService is driven into a real critical/degraded state via
// genuine 5xx traffic (see generateCheckoutErrors). It is the only fixture
// app with a controllable error path.
const triageDegradedService = "otel-app"

// triageHealthyService is exercised only through its always-successful
// /trigger endpoint, so it stays healthy and gives TestTriageAllServicesSelectsWorst
// a deterministic non-degraded control service.
const triageHealthyService = "sdk-app"

// CLITriageSuite tests the 'coral triage' command end-to-end (RFD 114).
//
// RFD 114's own Testing Strategy calls for integration coverage of a
// degraded-service diagnosis (with and without --attach), deterministic
// all-services selection, and structured partial results on attachment
// denial — but the original implementation (commit 85ba5fb) only added unit
// tests against a fake client (internal/cli/triage/triage_test.go). This
// suite fills that gap against a real colony/agent/service stack.
//
// Candidate resolution scope: otel-app's /api/checkout error injection drives
// a real critical/degraded QueryUnifiedSummary status without ever touching
// summary.Issues or summary.Regressions (those are only populated by the
// host CPU/memory and profiling-regression paths — see
// internal/colony/ebpf_service.go's "High CPU"/"High Memory" append sites),
// and without any profiling data collected. Per triage.go's resolveCandidate,
// both the hot-path and semantic-query branches therefore deterministically
// find nothing, so candidate_status is always "not_found" here — not a
// relaxed assumption, but the actual code path for an error-rate-only
// degradation. Exercising the "found" branch would need a fixture service
// that is simultaneously CPU-hot (for a real profiling hot path) and
// error-prone, which none of the current fixture apps are; that combination
// remains covered only at the unit level.
type CLITriageSuite struct {
	E2EDistributedSuite

	cliEnv *helpers.CLITestEnv
}

// SetupSuite connects the fixture services and drives otel-app into a real
// critical/degraded state before any test runs.
func (s *CLITriageSuite) SetupSuite() {
	s.E2EDistributedSuite.SetupSuite()

	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")

	s.cliEnv, err = helpers.SetupCLIEnv(s.ctx, "test-colony-e2e", colonyEndpoint)
	s.Require().NoError(err, "Failed to setup CLI environment")

	helpers.EnsureServicesConnected(s.T(), s.ctx, s.fixture, 0, []helpers.ServiceConfig{
		{Name: triageDegradedService, Port: 8090, HealthEndpoint: "/health"},
	})
	helpers.EnsureServicesConnected(s.T(), s.ctx, s.fixture, 1, []helpers.ServiceConfig{
		{Name: triageHealthyService, Port: 3001, HealthEndpoint: "/health"},
	})

	s.T().Log("Generating checkout error traffic to push otel-app into a critical state...")
	s.generateCheckoutErrors(80)
	s.T().Log("Generating clean traffic to keep sdk-app healthy...")
	s.generateHealthyTraffic(20)

	summary := s.waitForSummary(triageDegradedService, func(r *colonyv1.UnifiedSummaryResult) bool {
		return r.Status == "critical" || r.Status == "degraded"
	}, 90*time.Second)
	s.T().Logf("otel-app summary before tests: status=%s error_rate=%.1f%% avg_latency=%.0fms",
		summary.Status, summary.ErrorRate, summary.AvgLatencyMs)
}

// TearDownSuite disconnects services and cleans up the CLI environment.
func (s *CLITriageSuite) TearDownSuite() {
	helpers.DisconnectAllServices(s.T(), s.ctx, s.fixture, 0, []string{triageDegradedService})
	helpers.DisconnectAllServices(s.T(), s.ctx, s.fixture, 1, []string{triageHealthyService})

	if s.cliEnv != nil {
		_ = s.cliEnv.Cleanup()
	}
	s.E2EDistributedSuite.TearDownSuite()
}

// generateCheckoutErrors drives real POST /api/checkout traffic against
// otel-app. Each call has roughly a 34% cumulative chance of a real 5xx
// (main.go's per-step errorPct across 5 steps), which reliably clears the 5%
// "critical" error-rate threshold (internal/colony/ebpf_service.go) at n=80.
func (s *CLITriageSuite) generateCheckoutErrors(n int) {
	endpoint, err := s.fixture.GetOTELAppEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get otel-app endpoint")

	client := &http.Client{Timeout: 5 * time.Second}
	url := fmt.Sprintf("http://%s/api/checkout", endpoint)
	for i := 0; i < n; i++ {
		resp, err := client.Post(url, "application/json", nil)
		if err != nil {
			s.T().Logf("checkout request %d failed: %v", i, err)
			continue
		}
		_ = resp.Body.Close()
	}
}

// generateHealthyTraffic drives real GET /trigger traffic against sdk-app,
// whose handler always succeeds, so the service's error rate stays at 0%.
func (s *CLITriageSuite) generateHealthyTraffic(n int) {
	endpoint, err := s.fixture.GetSDKAppEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get sdk-app endpoint")

	client := &http.Client{Timeout: 5 * time.Second}
	url := fmt.Sprintf("http://%s/trigger", endpoint)
	for i := 0; i < n; i++ {
		resp, err := client.Get(url)
		if err != nil {
			s.T().Logf("trigger request %d failed: %v", i, err)
			continue
		}
		_ = resp.Body.Close()
	}
}

// waitForSummary polls QueryUnifiedSummary directly (bypassing the CLI) until
// match returns true for the named service's summary, or fails the test.
func (s *CLITriageSuite) waitForSummary(serviceName string, match func(*colonyv1.UnifiedSummaryResult) bool, timeout time.Duration) *colonyv1.UnifiedSummaryResult {
	colonyEndpoint, err := s.fixture.GetColonyEndpoint(s.ctx)
	s.Require().NoError(err, "Failed to get colony endpoint")
	colonyClient := helpers.NewColonyClient(colonyEndpoint)

	var found *colonyv1.UnifiedSummaryResult
	err = helpers.WaitForCondition(s.ctx, func() bool {
		resp, queryErr := helpers.QueryColonySummary(s.ctx, colonyClient, serviceName, "5m")
		if queryErr != nil {
			return false
		}
		for _, r := range resp.Summaries {
			if r.ServiceName == serviceName && match(r) {
				found = r
				return true
			}
		}
		return false
	}, timeout, 2*time.Second)
	s.Require().NoError(err, "timed out waiting for %s summary condition", serviceName)
	return found
}

// TestTriageAttachDurationRequiresAttach validates the --attach-duration/--attach
// argument dependency (RFD 114 Solution: "Supplying --attach-duration without
// --attach is an argument error"). Flag validation runs before any RPC, so
// this needs no connected services.
func (s *CLITriageSuite) TestTriageAttachDurationRequiresAttach() {
	result := s.cliEnv.Run(s.ctx, "triage", "--attach-duration", "15s")

	s.Require().True(result.HasError(), "expected non-zero exit, got: %s", result.Output)
	helpers.AssertContains(s.T(), result.Output, "--attach-duration requires --attach")
}

// TestTriageInvalidFormatRejected validates the --format flag's argument error.
func (s *CLITriageSuite) TestTriageInvalidFormatRejected() {
	result := s.cliEnv.Run(s.ctx, "triage", "--format", "xml")

	s.Require().True(result.HasError(), "expected non-zero exit, got: %s", result.Output)
	helpers.AssertContains(s.T(), result.Output, "unsupported format")
}

// TestTriageUnknownServiceReturnsNoData validates the no_data outcome (RFD
// 114 Solution stage 2): a named service with no QueryUnifiedSummary rows
// returns outcome no_data without querying functions or attaching.
func (s *CLITriageSuite) TestTriageUnknownServiceReturnsNoData() {
	result := s.cliEnv.Run(s.ctx, "triage", "definitely-not-a-registered-service-xyz", "--format", "json")
	result.MustSucceed(s.T())

	out := helpers.ValidateJSONObject(s.T(), result.Output, "service", "outcome", "attach")
	s.Require().Equal("no_data", out["outcome"])

	attach, ok := out["attach"].(map[string]interface{})
	s.Require().True(ok, "attach field should be an object")
	s.Require().Equal("not_requested", attach["status"])
}

// TestTriageHealthyServiceSkipsCandidateAndAttach validates that a named,
// genuinely healthy service returns outcome healthy and never reaches
// candidate resolution or attachment (RFD 114 Solution: "A healthy named
// service returns its summary with outcome healthy and does not attach").
func (s *CLITriageSuite) TestTriageHealthyServiceSkipsCandidateAndAttach() {
	summary := s.waitForSummary(triageHealthyService, func(r *colonyv1.UnifiedSummaryResult) bool {
		return r.Status == "healthy"
	}, 60*time.Second)
	s.T().Logf("sdk-app summary: status=%s error_rate=%.1f%% avg_latency=%.0fms",
		summary.Status, summary.ErrorRate, summary.AvgLatencyMs)

	result := s.cliEnv.Run(s.ctx, "triage", triageHealthyService, "--format", "json")
	result.MustSucceed(s.T())

	out := helpers.ValidateJSONObject(s.T(), result.Output, "service", "outcome", "attach")
	s.Require().Equal(triageHealthyService, out["service"])
	s.Require().Equal("healthy", out["outcome"])
	_, hasCandidateStatus := out["candidate_status"]
	s.Require().False(hasCandidateStatus, "healthy outcome must not include a candidate_status field")

	attach, ok := out["attach"].(map[string]interface{})
	s.Require().True(ok, "attach field should be an object")
	s.Require().Equal("not_requested", attach["status"])
}

// TestTriageDegradedServiceReadOnly validates the read-only default path
// against a real critical/degraded summary: outcome degraded, a well-formed
// (deterministically not_found, see suite doc) candidate result, and no
// attachment ever attempted without --attach (RFD 114 Security
// Considerations: "Triage is read-only unless --attach is explicitly
// supplied").
func (s *CLITriageSuite) TestTriageDegradedServiceReadOnly() {
	result := s.cliEnv.Run(s.ctx, "triage", triageDegradedService, "--format", "json")
	result.MustSucceed(s.T())

	out := helpers.ValidateJSONObject(s.T(), result.Output, "service", "outcome", "summary", "attach")
	s.Require().Equal(triageDegradedService, out["service"])
	s.Require().Equal("degraded", out["outcome"])

	summary, ok := out["summary"].(map[string]interface{})
	s.Require().True(ok, "summary field should be an object")
	status := summary["status"]
	s.Require().Contains([]interface{}{"critical", "degraded"}, status)

	s.Require().Equal("not_found", out["candidate_status"],
		"error-rate-only degradation touches neither Issues/Regressions nor profiling data, "+
			"so candidate resolution has nothing to match (see suite doc)")
	_, hasCandidateFunction := out["candidate_function"]
	s.Require().False(hasCandidateFunction, "not_found must not include a candidate_function")

	attach, ok := out["attach"].(map[string]interface{})
	s.Require().True(ok, "attach field should be an object")
	s.Require().Equal("not_requested", attach["status"])
}

// TestTriageDegradedServiceAttachSkipsWithoutCandidate validates the
// documented partial result for --attach when no candidate was resolved (RFD
// 114 Solution: "Otherwise leave the system unchanged" / attach status
// "skipped").
func (s *CLITriageSuite) TestTriageDegradedServiceAttachSkipsWithoutCandidate() {
	result := s.cliEnv.Run(s.ctx, "triage", triageDegradedService, "--attach", "--attach-duration", "5s", "--format", "json")
	result.MustSucceed(s.T())

	out := helpers.ValidateJSONObject(s.T(), result.Output, "service", "outcome", "attach")
	s.Require().Equal("degraded", out["outcome"])
	s.Require().Equal("not_found", out["candidate_status"])

	attach, ok := out["attach"].(map[string]interface{})
	s.Require().True(ok, "attach field should be an object")
	s.Require().Equal("skipped", attach["status"],
		"no candidate was resolved, so --attach must be a no-op, never a claimed attachment")
	_, hasSessionID := attach["session_id"]
	s.Require().False(hasSessionID, "a skipped attach must not carry a session_id")
}

// TestTriageAllServicesSelectsWorst validates deterministic worst-service
// selection across the fleet (RFD 114 Solution stage 3): with otel-app
// critical/degraded and sdk-app healthy, an unscoped `coral triage` must
// select otel-app.
func (s *CLITriageSuite) TestTriageAllServicesSelectsWorst() {
	result := s.cliEnv.Run(s.ctx, "triage", "--format", "json")
	result.MustSucceed(s.T())

	out := helpers.ValidateJSONObject(s.T(), result.Output, "service", "outcome")
	s.Require().Equal(triageDegradedService, out["service"],
		"unscoped triage should select the critical/degraded service over the healthy one")
	s.Require().Equal("degraded", out["outcome"])
}

// TestTriageTextOutputShowsFailureProminently validates that default text
// output never describes a non-attachment as a success (RFD 114 Solution:
// "Failure fields must be prominent in text output and must not be described
// as a successful attachment").
func (s *CLITriageSuite) TestTriageTextOutputShowsFailureProminently() {
	result := s.cliEnv.Run(s.ctx, "triage", triageDegradedService, "--attach", "--attach-duration", "5s")
	result.MustSucceed(s.T())

	helpers.AssertContains(s.T(), result.Output, "Triage: "+triageDegradedService)
	helpers.AssertContains(s.T(), result.Output, "Outcome: degraded")
	helpers.AssertContains(s.T(), result.Output, "Candidate: not found")
	helpers.AssertContains(s.T(), result.Output, "Attach: skipped (no candidate)")
	helpers.AssertNotContains(s.T(), result.Output, "Attach: attached")
}
