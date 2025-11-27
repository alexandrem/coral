package agent

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"connectrpc.com/connect"
	_ "github.com/marcboeker/go-duckdb"
	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/agent/telemetry"
)

// TestQueryTelemetry_Success tests the QueryTelemetry RPC handler.
func TestQueryTelemetry_Success(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	// Create agent storage with test data.
	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }() // TODO: errcheck

	storage, err := telemetry.NewStorage(db, logger)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	// Create receiver with storage.
	config := telemetry.Config{
		Disabled:     false,
		AgentID:      "test-agent",
		GRPCEndpoint: "127.0.0.1:4317",
		Filters: telemetry.FilterConfig{
			HighLatencyThresholdMs: 500.0,
			SampleRate:             0.1,
		},
	}

	receiver, err := telemetry.NewReceiver(config, storage, logger)
	if err != nil {
		t.Fatalf("Failed to create receiver: %v", err)
	}

	// Store test spans.
	now := time.Now()
	testSpans := []telemetry.Span{
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
			Attributes:  map[string]string{"region": "us-east"},
		},
		{
			Timestamp:   now.Add(-20 * time.Second),
			TraceID:     "trace-2",
			SpanID:      "span-2",
			ServiceName: "payment",
			SpanKind:    "CLIENT",
			DurationMs:  75.0,
			IsError:     false,
			HTTPStatus:  200,
			HTTPMethod:  "POST",
			HTTPRoute:   "/charge",
			Attributes:  map[string]string{"region": "us-east"},
		},
	}

	for _, span := range testSpans {
		if err := storage.StoreSpan(ctx, span); err != nil {
			t.Fatalf("Failed to store span: %v", err)
		}
	}

	// Create handler.
	handler := NewTelemetryHandler(receiver)

	// Create query request.
	startTime := now.Add(-1 * time.Minute).Unix()
	endTime := now.Add(1 * time.Minute).Unix()

	req := connect.NewRequest(&agentv1.QueryTelemetryRequest{
		StartTime:    startTime,
		EndTime:      endTime,
		ServiceNames: nil, // Query all services.
	})

	// Call QueryTelemetry.
	resp, err := handler.QueryTelemetry(ctx, req)
	if err != nil {
		t.Fatalf("QueryTelemetry failed: %v", err)
	}

	// Verify response.
	if resp.Msg.TotalSpans != 2 {
		t.Errorf("Expected 2 total spans, got %d", resp.Msg.TotalSpans)
	}

	if len(resp.Msg.Spans) != 2 {
		t.Fatalf("Expected 2 spans in response, got %d", len(resp.Msg.Spans))
	}

	// Verify checkout span.
	var checkoutSpan *agentv1.TelemetrySpan
	for _, span := range resp.Msg.Spans {
		if span.ServiceName == "checkout" {
			checkoutSpan = span
			break
		}
	}

	if checkoutSpan == nil {
		t.Fatal("Checkout span not found in response")
	}

	if checkoutSpan.TraceId != "trace-1" {
		t.Errorf("Expected trace-1, got %s", checkoutSpan.TraceId)
	}
	if checkoutSpan.IsError != true {
		t.Error("Expected error span")
	}
	if checkoutSpan.HttpStatus != 500 {
		t.Errorf("Expected HTTP 500, got %d", checkoutSpan.HttpStatus)
	}
	if checkoutSpan.Attributes["region"] != "us-east" {
		t.Errorf("Expected region=us-east, got %s", checkoutSpan.Attributes["region"])
	}

	// Verify payment span.
	var paymentSpan *agentv1.TelemetrySpan
	for _, span := range resp.Msg.Spans {
		if span.ServiceName == "payment" {
			paymentSpan = span
			break
		}
	}

	if paymentSpan == nil {
		t.Fatal("Payment span not found in response")
	}

	if paymentSpan.TraceId != "trace-2" {
		t.Errorf("Expected trace-2, got %s", paymentSpan.TraceId)
	}
	if paymentSpan.IsError != false {
		t.Error("Expected non-error span")
	}
}

// TestQueryTelemetry_ServiceFilter tests service name filtering.
func TestQueryTelemetry_ServiceFilter(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	// Create agent storage.
	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }() // TODO: errcheck

	storage, err := telemetry.NewStorage(db, logger)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	// Create receiver.
	config := telemetry.Config{
		Disabled:     false,
		AgentID:      "test-agent",
		GRPCEndpoint: "127.0.0.1:4317",
	}

	receiver, err := telemetry.NewReceiver(config, storage, logger)
	if err != nil {
		t.Fatalf("Failed to create receiver: %v", err)
	}

	// Store spans for multiple services.
	now := time.Now()
	testSpans := []telemetry.Span{
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

	for _, span := range testSpans {
		if err := storage.StoreSpan(ctx, span); err != nil {
			t.Fatalf("Failed to store span: %v", err)
		}
	}

	// Create handler.
	handler := NewTelemetryHandler(receiver)

	// Query only checkout service.
	req := connect.NewRequest(&agentv1.QueryTelemetryRequest{
		StartTime:    now.Add(-1 * time.Minute).Unix(),
		EndTime:      now.Add(1 * time.Minute).Unix(),
		ServiceNames: []string{"checkout"},
	})

	resp, err := handler.QueryTelemetry(ctx, req)
	if err != nil {
		t.Fatalf("QueryTelemetry failed: %v", err)
	}

	// Should only get checkout span.
	if resp.Msg.TotalSpans != 1 {
		t.Errorf("Expected 1 span, got %d", resp.Msg.TotalSpans)
	}

	if len(resp.Msg.Spans) > 0 && resp.Msg.Spans[0].ServiceName != "checkout" {
		t.Errorf("Expected checkout service, got %s", resp.Msg.Spans[0].ServiceName)
	}
}

// TestQueryTelemetry_TimeRange tests time range filtering.
func TestQueryTelemetry_TimeRange(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	// Create agent storage.
	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }() // TODO: errcheck

	storage, err := telemetry.NewStorage(db, logger)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	// Create receiver.
	config := telemetry.Config{
		Disabled:     false,
		AgentID:      "test-agent",
		GRPCEndpoint: "127.0.0.1:4317",
	}

	receiver, err := telemetry.NewReceiver(config, storage, logger)
	if err != nil {
		t.Fatalf("Failed to create receiver: %v", err)
	}

	// Store spans at different times.
	now := time.Now()
	testSpans := []telemetry.Span{
		// Old span (outside query range).
		{
			Timestamp:   now.Add(-2 * time.Hour),
			TraceID:     "old-trace",
			SpanID:      "old-span",
			ServiceName: "api",
			SpanKind:    "SERVER",
			DurationMs:  100.0,
		},
		// Recent span (inside query range).
		{
			Timestamp:   now.Add(-30 * time.Second),
			TraceID:     "recent-trace",
			SpanID:      "recent-span",
			ServiceName: "api",
			SpanKind:    "SERVER",
			DurationMs:  100.0,
		},
	}

	for _, span := range testSpans {
		if err := storage.StoreSpan(ctx, span); err != nil {
			t.Fatalf("Failed to store span: %v", err)
		}
	}

	// Create handler.
	handler := NewTelemetryHandler(receiver)

	// Query only last 1 minute.
	req := connect.NewRequest(&agentv1.QueryTelemetryRequest{
		StartTime:    now.Add(-1 * time.Minute).Unix(),
		EndTime:      now.Add(1 * time.Minute).Unix(),
		ServiceNames: nil,
	})

	resp, err := handler.QueryTelemetry(ctx, req)
	if err != nil {
		t.Fatalf("QueryTelemetry failed: %v", err)
	}

	// Should only get recent span.
	if resp.Msg.TotalSpans != 1 {
		t.Errorf("Expected 1 span in time range, got %d", resp.Msg.TotalSpans)
	}

	if len(resp.Msg.Spans) > 0 && resp.Msg.Spans[0].TraceId != "recent-trace" {
		t.Errorf("Expected recent-trace, got %s", resp.Msg.Spans[0].TraceId)
	}
}

// TestQueryTelemetry_EmptyResult tests querying when no spans match.
func TestQueryTelemetry_EmptyResult(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	// Create agent storage.
	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }() // TODO: errcheck

	storage, err := telemetry.NewStorage(db, logger)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	// Create receiver.
	config := telemetry.Config{
		Disabled:     false,
		AgentID:      "test-agent",
		GRPCEndpoint: "127.0.0.1:4317",
	}

	receiver, err := telemetry.NewReceiver(config, storage, logger)
	if err != nil {
		t.Fatalf("Failed to create receiver: %v", err)
	}

	// Don't store any spans.

	// Create handler.
	handler := NewTelemetryHandler(receiver)

	// Query empty storage.
	now := time.Now()
	req := connect.NewRequest(&agentv1.QueryTelemetryRequest{
		StartTime:    now.Add(-1 * time.Minute).Unix(),
		EndTime:      now.Add(1 * time.Minute).Unix(),
		ServiceNames: nil,
	})

	resp, err := handler.QueryTelemetry(ctx, req)
	if err != nil {
		t.Fatalf("QueryTelemetry failed: %v", err)
	}

	// Should get empty result.
	if resp.Msg.TotalSpans != 0 {
		t.Errorf("Expected 0 spans, got %d", resp.Msg.TotalSpans)
	}

	if len(resp.Msg.Spans) != 0 {
		t.Errorf("Expected empty spans list, got %d spans", len(resp.Msg.Spans))
	}
}
