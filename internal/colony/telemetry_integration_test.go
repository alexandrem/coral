package colony

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/agent/telemetry"
	"github.com/coral-mesh/coral/internal/colony/database"
)

// TestPullBasedTelemetry_EndToEnd tests the full pull-based telemetry flow:
// 1. Agent receives and stores spans locally
// 2. Colony queries agent for spans
// 3. Colony aggregates spans into summaries
// 4. Colony stores summaries in database
func TestPullBasedTelemetry_EndToEnd(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	// === Setup Agent ===
	// Create agent local storage (in-memory SQLite for testing).
	agentDB, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("Failed to create agent database: %v", err)
	}
	defer func() { _ = agentDB.Close() }() // TODO: errcheck

	agentStorage, err := telemetry.NewStorage(agentDB, logger)
	if err != nil {
		t.Fatalf("Failed to create agent storage: %v", err)
	}

	// Store test spans in agent local storage.
	now := time.Now()
	testSpans := []telemetry.Span{
		// Checkout service spans (errors + high latency).
		{
			Timestamp:   now.Add(-30 * time.Second),
			TraceID:     "trace-1",
			SpanID:      "span-1",
			ServiceName: "checkout",
			SpanKind:    "SERVER",
			DurationMs:  150.0,
			IsError:     true,
			HTTPStatus:  500,
			HTTPMethod:  "POST",
			HTTPRoute:   "/checkout",
			Attributes:  map[string]string{"env": "test"},
		},
		{
			Timestamp:   now.Add(-25 * time.Second),
			TraceID:     "trace-2",
			SpanID:      "span-2",
			ServiceName: "checkout",
			SpanKind:    "SERVER",
			DurationMs:  600.0, // High latency.
			IsError:     false,
			HTTPStatus:  200,
			HTTPMethod:  "POST",
			HTTPRoute:   "/checkout",
			Attributes:  map[string]string{"env": "test"},
		},
		{
			Timestamp:   now.Add(-20 * time.Second),
			TraceID:     "trace-3",
			SpanID:      "span-3",
			ServiceName: "checkout",
			SpanKind:    "SERVER",
			DurationMs:  100.0,
			IsError:     false,
			HTTPStatus:  200,
			HTTPMethod:  "POST",
			HTTPRoute:   "/checkout",
			Attributes:  map[string]string{"env": "test"},
		},
		// Payment service spans.
		{
			Timestamp:   now.Add(-28 * time.Second),
			TraceID:     "trace-4",
			SpanID:      "span-4",
			ServiceName: "payment",
			SpanKind:    "CLIENT",
			DurationMs:  50.0,
			IsError:     false,
			HTTPStatus:  200,
			HTTPMethod:  "POST",
			HTTPRoute:   "/charge",
			Attributes:  map[string]string{"env": "test"},
		},
		{
			Timestamp:   now.Add(-22 * time.Second),
			TraceID:     "trace-5",
			SpanID:      "span-5",
			ServiceName: "payment",
			SpanKind:    "CLIENT",
			DurationMs:  75.0,
			IsError:     false,
			HTTPStatus:  200,
			HTTPMethod:  "POST",
			HTTPRoute:   "/charge",
			Attributes:  map[string]string{"env": "test"},
		},
	}

	for _, span := range testSpans {
		if err := agentStorage.StoreSpan(ctx, span); err != nil {
			t.Fatalf("Failed to store span: %v", err)
		}
	}

	// === Simulate Colony Querying Agent ===
	startTime := now.Add(-1 * time.Minute)
	endTime := now.Add(1 * time.Minute)

	// Query spans from agent (this simulates colony calling agent's QueryTelemetry RPC).
	queriedSpans, err := agentStorage.QuerySpans(ctx, startTime, endTime, nil)
	if err != nil {
		t.Fatalf("Failed to query spans from agent: %v", err)
	}

	if len(queriedSpans) != 5 {
		t.Errorf("Expected 5 spans from agent, got %d", len(queriedSpans))
	}

	// === Setup Colony ===
	// Create colony database.
	tmpDir := t.TempDir()
	colonyDB, err := database.New(tmpDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create colony database: %v", err)
	}
	defer func() { _ = colonyDB.Close() }() // TODO: errcheck

	// === Colony Aggregates Spans ===
	aggregator := NewTelemetryAggregator()

	// Convert internal spans to protobuf spans (simulating RPC response).
	pbSpans := make([]*agentv1.TelemetrySpan, 0, len(queriedSpans))
	for _, span := range queriedSpans {
		pbSpan := &agentv1.TelemetrySpan{
			Timestamp:   span.Timestamp.UnixMilli(),
			TraceId:     span.TraceID,
			SpanId:      span.SpanID,
			ServiceName: span.ServiceName,
			SpanKind:    span.SpanKind,
			DurationMs:  span.DurationMs,
			IsError:     span.IsError,
			HttpStatus:  int32(span.HTTPStatus),
			HttpMethod:  span.HTTPMethod,
			HttpRoute:   span.HTTPRoute,
			Attributes:  span.Attributes,
		}
		pbSpans = append(pbSpans, pbSpan)
	}

	// Add spans to aggregator.
	aggregator.AddSpans("agent-1", pbSpans)

	// Get aggregated summaries.
	summaries := aggregator.GetSummaries()

	if len(summaries) == 0 {
		t.Fatal("Expected aggregated summaries, got none")
	}

	// Should have 2 summaries (checkout:SERVER and payment:CLIENT).
	if len(summaries) != 2 {
		t.Errorf("Expected 2 summaries, got %d", len(summaries))
	}

	// === Colony Stores Summaries ===
	if err := colonyDB.InsertTelemetrySummaries(ctx, summaries); err != nil {
		t.Fatalf("Failed to insert summaries: %v", err)
	}

	// === Verify Stored Summaries ===
	// Query summaries from colony database.
	// Calculate the actual bucket time from the first span to handle edge cases.
	firstSpanTime := now.Add(-30 * time.Second)
	bucketTime := firstSpanTime.Truncate(time.Minute)
	storedSummaries, err := colonyDB.QueryTelemetrySummaries(ctx, "agent-1", bucketTime, bucketTime.Add(1*time.Minute))
	if err != nil {
		t.Fatalf("Failed to query summaries: %v", err)
	}

	if len(storedSummaries) != 2 {
		t.Errorf("Expected 2 stored summaries, got %d", len(storedSummaries))
	}

	// Verify checkout summary.
	var checkoutSummary *database.TelemetrySummary
	for i := range storedSummaries {
		if storedSummaries[i].ServiceName == "checkout" {
			checkoutSummary = &storedSummaries[i]
			break
		}
	}

	if checkoutSummary == nil {
		t.Fatal("Checkout summary not found")
	}

	// Verify checkout metrics.
	if checkoutSummary.AgentID != "agent-1" {
		t.Errorf("Expected agent-1, got %s", checkoutSummary.AgentID)
	}
	if checkoutSummary.SpanKind != "SERVER" {
		t.Errorf("Expected SERVER span kind, got %s", checkoutSummary.SpanKind)
	}
	if checkoutSummary.TotalSpans != 3 {
		t.Errorf("Expected 3 total spans, got %d", checkoutSummary.TotalSpans)
	}
	if checkoutSummary.ErrorCount != 1 {
		t.Errorf("Expected 1 error, got %d", checkoutSummary.ErrorCount)
	}
	// Verify percentiles exist (values will depend on sorting).
	if checkoutSummary.P50Ms == 0 {
		t.Error("Expected non-zero p50")
	}
	if checkoutSummary.P95Ms == 0 {
		t.Error("Expected non-zero p95")
	}
	if checkoutSummary.P99Ms == 0 {
		t.Error("Expected non-zero p99")
	}

	// Verify payment summary.
	var paymentSummary *database.TelemetrySummary
	for i := range storedSummaries {
		if storedSummaries[i].ServiceName == "payment" {
			paymentSummary = &storedSummaries[i]
			break
		}
	}

	if paymentSummary == nil {
		t.Fatal("Payment summary not found")
	}

	// Verify payment metrics.
	if paymentSummary.AgentID != "agent-1" {
		t.Errorf("Expected agent-1, got %s", paymentSummary.AgentID)
	}
	if paymentSummary.SpanKind != "CLIENT" {
		t.Errorf("Expected CLIENT span kind, got %s", paymentSummary.SpanKind)
	}
	if paymentSummary.TotalSpans != 2 {
		t.Errorf("Expected 2 total spans, got %d", paymentSummary.TotalSpans)
	}
	if paymentSummary.ErrorCount != 0 {
		t.Errorf("Expected 0 errors, got %d", paymentSummary.ErrorCount)
	}

	t.Logf("✅ Pull-based telemetry flow completed successfully")
	t.Logf("   - Agent stored: %d spans", len(testSpans))
	t.Logf("   - Colony queried: %d spans", len(queriedSpans))
	t.Logf("   - Colony created: %d summaries", len(summaries))
	t.Logf("   - Colony stored: %d summaries", len(storedSummaries))
}

// TestPullBasedTelemetry_TimeRangeFiltering tests that colony can query specific time ranges.
func TestPullBasedTelemetry_TimeRangeFiltering(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	// Create agent storage.
	agentDB, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("Failed to create agent database: %v", err)
	}
	defer func() { _ = agentDB.Close() }() // TODO: errcheck

	agentStorage, err := telemetry.NewStorage(agentDB, logger)
	if err != nil {
		t.Fatalf("Failed to create agent storage: %v", err)
	}

	// Store spans across different time periods.
	now := time.Now()
	spans := []telemetry.Span{
		// Old span (2 hours ago).
		{
			Timestamp:   now.Add(-2 * time.Hour),
			TraceID:     "old-trace",
			SpanID:      "old-span",
			ServiceName: "api",
			SpanKind:    "SERVER",
			DurationMs:  100.0,
			IsError:     false,
		},
		// Recent span (30 seconds ago).
		{
			Timestamp:   now.Add(-30 * time.Second),
			TraceID:     "recent-trace",
			SpanID:      "recent-span",
			ServiceName: "api",
			SpanKind:    "SERVER",
			DurationMs:  200.0,
			IsError:     false,
		},
	}

	for _, span := range spans {
		if err := agentStorage.StoreSpan(ctx, span); err != nil {
			t.Fatalf("Failed to store span: %v", err)
		}
	}

	// Query only recent spans (last 1 minute).
	startTime := now.Add(-1 * time.Minute)
	endTime := now.Add(1 * time.Minute)

	queriedSpans, err := agentStorage.QuerySpans(ctx, startTime, endTime, nil)
	if err != nil {
		t.Fatalf("Failed to query spans: %v", err)
	}

	// Should only get the recent span.
	if len(queriedSpans) != 1 {
		t.Errorf("Expected 1 span in time range, got %d", len(queriedSpans))
	}

	if len(queriedSpans) > 0 && queriedSpans[0].TraceID != "recent-trace" {
		t.Errorf("Expected recent-trace, got %s", queriedSpans[0].TraceID)
	}
}

// TestPullBasedTelemetry_ServiceFiltering tests that colony can query specific services.
func TestPullBasedTelemetry_ServiceFiltering(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	// Create agent storage.
	agentDB, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("Failed to create agent database: %v", err)
	}
	defer func() { _ = agentDB.Close() }() // TODO: errcheck

	agentStorage, err := telemetry.NewStorage(agentDB, logger)
	if err != nil {
		t.Fatalf("Failed to create agent storage: %v", err)
	}

	// Store spans for different services.
	now := time.Now()
	spans := []telemetry.Span{
		{
			Timestamp:   now,
			TraceID:     "trace-1",
			SpanID:      "span-1",
			ServiceName: "checkout",
			SpanKind:    "SERVER",
			DurationMs:  100.0,
		},
		{
			Timestamp:   now,
			TraceID:     "trace-2",
			SpanID:      "span-2",
			ServiceName: "payment",
			SpanKind:    "CLIENT",
			DurationMs:  50.0,
		},
		{
			Timestamp:   now,
			TraceID:     "trace-3",
			SpanID:      "span-3",
			ServiceName: "inventory",
			SpanKind:    "SERVER",
			DurationMs:  75.0,
		},
	}

	for _, span := range spans {
		if err := agentStorage.StoreSpan(ctx, span); err != nil {
			t.Fatalf("Failed to store span: %v", err)
		}
	}

	// Query only checkout and payment services.
	startTime := now.Add(-1 * time.Minute)
	endTime := now.Add(1 * time.Minute)
	serviceNames := []string{"checkout", "payment"}

	queriedSpans, err := agentStorage.QuerySpans(ctx, startTime, endTime, serviceNames)
	if err != nil {
		t.Fatalf("Failed to query spans: %v", err)
	}

	// Should get 2 spans (checkout + payment, not inventory).
	if len(queriedSpans) != 2 {
		t.Errorf("Expected 2 spans for filtered services, got %d", len(queriedSpans))
	}

	// Verify we didn't get inventory.
	for _, span := range queriedSpans {
		if span.ServiceName == "inventory" {
			t.Error("Should not have received inventory span")
		}
	}
}

// TestPullBasedTelemetry_Aggregation tests percentile calculation.
func TestPullBasedTelemetry_Aggregation(t *testing.T) {
	aggregator := NewTelemetryAggregator()

	now := time.Now()
	spans := []*agentv1.TelemetrySpan{
		// Create spans with known durations for predictable percentiles.
		{Timestamp: now.UnixMilli(), ServiceName: "api", SpanKind: "SERVER", DurationMs: 10.0},
		{Timestamp: now.UnixMilli(), ServiceName: "api", SpanKind: "SERVER", DurationMs: 20.0},
		{Timestamp: now.UnixMilli(), ServiceName: "api", SpanKind: "SERVER", DurationMs: 30.0},
		{Timestamp: now.UnixMilli(), ServiceName: "api", SpanKind: "SERVER", DurationMs: 40.0},
		{Timestamp: now.UnixMilli(), ServiceName: "api", SpanKind: "SERVER", DurationMs: 50.0},
		{Timestamp: now.UnixMilli(), ServiceName: "api", SpanKind: "SERVER", DurationMs: 60.0},
		{Timestamp: now.UnixMilli(), ServiceName: "api", SpanKind: "SERVER", DurationMs: 70.0},
		{Timestamp: now.UnixMilli(), ServiceName: "api", SpanKind: "SERVER", DurationMs: 80.0},
		{Timestamp: now.UnixMilli(), ServiceName: "api", SpanKind: "SERVER", DurationMs: 90.0},
		{Timestamp: now.UnixMilli(), ServiceName: "api", SpanKind: "SERVER", DurationMs: 100.0},
	}

	aggregator.AddSpans("agent-1", spans)
	summaries := aggregator.GetSummaries()

	if len(summaries) != 1 {
		t.Fatalf("Expected 1 summary, got %d", len(summaries))
	}

	summary := summaries[0]

	// Verify total spans.
	if summary.TotalSpans != 10 {
		t.Errorf("Expected 10 total spans, got %d", summary.TotalSpans)
	}

	// Verify percentiles (p50 should be around 50, p95 around 95, p99 around 99).
	if summary.P50Ms < 40 || summary.P50Ms > 60 {
		t.Errorf("Expected p50 ~50ms, got %.2f", summary.P50Ms)
	}
	if summary.P95Ms < 85 || summary.P95Ms > 100 {
		t.Errorf("Expected p95 ~95ms, got %.2f", summary.P95Ms)
	}
	if summary.P99Ms < 95 || summary.P99Ms > 100 {
		t.Errorf("Expected p99 ~99ms, got %.2f", summary.P99Ms)
	}
}
