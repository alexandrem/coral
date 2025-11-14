package telemetry

import (
	"testing"
	"time"
)

func TestAggregator_AddSpan(t *testing.T) {
	agg := NewAggregator("agent-1")

	now := time.Now()
	span := Span{
		ServiceName: "checkout",
		SpanKind:    "SERVER",
		DurationMs:  150.0,
		IsError:     false,
		TraceID:     "trace-123",
		Timestamp:   now,
	}

	agg.AddSpan(span)

	// Check that the span was added to a bucket.
	if len(agg.buckets) != 1 {
		t.Errorf("Expected 1 bucket, got %d", len(agg.buckets))
	}
}

func TestAggregator_FlushBuckets(t *testing.T) {
	agg := NewAggregator("agent-1")

	// Add spans from a previous minute.
	pastTime := time.Now().Add(-2 * time.Minute)

	for i := 0; i < 10; i++ {
		span := Span{
			ServiceName: "checkout",
			SpanKind:    "SERVER",
			DurationMs:  float64(100 + i*10),
			IsError:     i%3 == 0, // Every 3rd span is an error.
			TraceID:     "trace-" + string(rune('0'+i)),
			Timestamp:   pastTime,
		}
		agg.AddSpan(span)
	}

	// Add a span in the current minute (should not be flushed).
	currentSpan := Span{
		ServiceName: "payment",
		SpanKind:    "CLIENT",
		DurationMs:  50.0,
		IsError:     false,
		TraceID:     "trace-current",
		Timestamp:   time.Now(),
	}
	agg.AddSpan(currentSpan)

	// Flush buckets.
	buckets := agg.FlushBuckets()

	// Should only flush the past bucket, not the current one.
	if len(buckets) != 1 {
		t.Errorf("Expected 1 flushed bucket, got %d", len(buckets))
	}

	if len(buckets) > 0 {
		bucket := buckets[0]

		if bucket.AgentID != "agent-1" {
			t.Errorf("Expected agent_id='agent-1', got '%s'", bucket.AgentID)
		}

		if bucket.ServiceName != "checkout" {
			t.Errorf("Expected service_name='checkout', got '%s'", bucket.ServiceName)
		}

		if bucket.TotalSpans != 10 {
			t.Errorf("Expected total_spans=10, got %d", bucket.TotalSpans)
		}

		if bucket.ErrorCount != 4 { // Indices 0, 3, 6, 9.
			t.Errorf("Expected error_count=4, got %d", bucket.ErrorCount)
		}

		// Check that percentiles are calculated.
		if bucket.P50Ms == 0 || bucket.P95Ms == 0 || bucket.P99Ms == 0 {
			t.Error("Expected percentiles to be calculated")
		}
	}

	// Verify that flushed bucket was removed.
	if len(agg.buckets) != 1 {
		t.Errorf("Expected 1 remaining bucket (current minute), got %d", len(agg.buckets))
	}
}

func TestCalculatePercentiles(t *testing.T) {
	durations := []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}

	p50, p95, p99 := calculatePercentiles(durations)

	// p50 should be around 50.
	if p50 < 40 || p50 > 60 {
		t.Errorf("Expected p50 around 50, got %f", p50)
	}

	// p95 should be around 95.
	if p95 < 85 || p95 > 100 {
		t.Errorf("Expected p95 around 95, got %f", p95)
	}

	// p99 should be around 99.
	if p99 < 90 || p99 > 100 {
		t.Errorf("Expected p99 around 99, got %f", p99)
	}
}

func TestCalculatePercentiles_EmptySlice(t *testing.T) {
	durations := []float64{}

	p50, p95, p99 := calculatePercentiles(durations)

	if p50 != 0 || p95 != 0 || p99 != 0 {
		t.Error("Expected all percentiles to be 0 for empty slice")
	}
}

func TestCalculatePercentiles_SingleValue(t *testing.T) {
	durations := []float64{42.0}

	p50, p95, p99 := calculatePercentiles(durations)

	if p50 != 42.0 || p95 != 42.0 || p99 != 42.0 {
		t.Error("Expected all percentiles to be 42.0 for single-value slice")
	}
}
