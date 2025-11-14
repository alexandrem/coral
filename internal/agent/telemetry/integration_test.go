package telemetry

import (
	"testing"
	"time"
)

// TestTelemetryPipeline tests the full telemetry processing pipeline.
func TestTelemetryPipeline(t *testing.T) {
	agentID := "test-agent-1"

	// Create filter with strict rules.
	filterConfig := FilterConfig{
		AlwaysCaptureErrors: true,
		LatencyThresholdMs:  500.0,
		SampleRate:          1.0, // Capture all normal spans for testing.
	}
	filter := NewFilter(filterConfig)

	// Create aggregator.
	aggregator := NewAggregator(agentID)

	// Simulate spans from the last minute.
	pastTime := time.Now().Add(-2 * time.Minute).Truncate(time.Minute)

	// Add various spans.
	spans := []Span{
		// Error span (should always be captured).
		{
			ServiceName: "checkout",
			SpanKind:    "SERVER",
			DurationMs:  100.0,
			IsError:     true,
			TraceID:     "trace-error-1",
			Timestamp:   pastTime,
		},
		// High-latency span (should be captured).
		{
			ServiceName: "checkout",
			SpanKind:    "SERVER",
			DurationMs:  600.0, // > 500ms threshold.
			IsError:     false,
			TraceID:     "trace-slow-1",
			Timestamp:   pastTime,
		},
		// Normal spans.
		{
			ServiceName: "checkout",
			SpanKind:    "SERVER",
			DurationMs:  150.0,
			IsError:     false,
			TraceID:     "trace-normal-1",
			Timestamp:   pastTime,
		},
		{
			ServiceName: "checkout",
			SpanKind:    "SERVER",
			DurationMs:  200.0,
			IsError:     false,
			TraceID:     "trace-normal-2",
			Timestamp:   pastTime,
		},
		{
			ServiceName: "checkout",
			SpanKind:    "SERVER",
			DurationMs:  120.0,
			IsError:     false,
			TraceID:     "trace-normal-3",
			Timestamp:   pastTime,
		},
		// Different service.
		{
			ServiceName: "payment",
			SpanKind:    "CLIENT",
			DurationMs:  50.0,
			IsError:     false,
			TraceID:     "trace-payment-1",
			Timestamp:   pastTime,
		},
	}

	// Process spans through filter and aggregator.
	capturedCount := 0
	for _, span := range spans {
		if filter.ShouldCapture(span) {
			aggregator.AddSpan(span)
			capturedCount++
		}
	}

	// With 100% sample rate, all spans should be captured.
	if capturedCount != len(spans) {
		t.Errorf("Expected %d captured spans, got %d", len(spans), capturedCount)
	}

	// Flush buckets.
	buckets := aggregator.FlushBuckets()

	// Should have 2 buckets (checkout:SERVER and payment:CLIENT).
	if len(buckets) != 2 {
		t.Errorf("Expected 2 buckets, got %d", len(buckets))
	}

	// Verify checkout bucket.
	var checkoutBucket *Bucket
	for i := range buckets {
		if buckets[i].ServiceName == "checkout" && buckets[i].SpanKind == "SERVER" {
			checkoutBucket = &buckets[i]
			break
		}
	}

	if checkoutBucket == nil {
		t.Fatal("Checkout bucket not found")
	}

	if checkoutBucket.AgentID != agentID {
		t.Errorf("Expected agent_id=%s, got %s", agentID, checkoutBucket.AgentID)
	}

	if checkoutBucket.TotalSpans != 5 {
		t.Errorf("Expected 5 total spans, got %d", checkoutBucket.TotalSpans)
	}

	if checkoutBucket.ErrorCount != 1 {
		t.Errorf("Expected 1 error, got %d", checkoutBucket.ErrorCount)
	}

	// Verify percentiles are calculated.
	if checkoutBucket.P50Ms == 0 || checkoutBucket.P95Ms == 0 || checkoutBucket.P99Ms == 0 {
		t.Error("Percentiles should be calculated")
	}

	// p99 should be close to the highest value (600ms).
	if checkoutBucket.P99Ms < 500 {
		t.Errorf("Expected p99 >= 500ms, got %f", checkoutBucket.P99Ms)
	}

	// Verify sample traces are included.
	if len(checkoutBucket.SampleTraces) == 0 {
		t.Error("Expected sample traces to be included")
	}

	// Verify bucket time is aligned to minute.
	if checkoutBucket.BucketTime != pastTime {
		t.Errorf("Expected bucket_time=%v, got %v", pastTime, checkoutBucket.BucketTime)
	}
}

// TestTelemetryPipeline_ErrorCapture tests that errors are always captured.
func TestTelemetryPipeline_ErrorCapture(t *testing.T) {
	filterConfig := FilterConfig{
		AlwaysCaptureErrors: true,
		LatencyThresholdMs:  10000.0, // Very high threshold.
		SampleRate:          0.0,     // No sampling.
	}
	filter := NewFilter(filterConfig)

	aggregator := NewAggregator("test-agent")
	pastTime := time.Now().Add(-2 * time.Minute).Truncate(time.Minute)

	// Add only error spans.
	errorSpan := Span{
		ServiceName: "api",
		SpanKind:    "SERVER",
		DurationMs:  50.0, // Low latency, but error.
		IsError:     true,
		TraceID:     "trace-error",
		Timestamp:   pastTime,
	}

	normalSpan := Span{
		ServiceName: "api",
		SpanKind:    "SERVER",
		DurationMs:  50.0,
		IsError:     false,
		TraceID:     "trace-normal",
		Timestamp:   pastTime,
	}

	// Error span should be captured.
	if filter.ShouldCapture(errorSpan) {
		aggregator.AddSpan(errorSpan)
	}

	// Normal span should NOT be captured (0% sample rate, low latency).
	if filter.ShouldCapture(normalSpan) {
		aggregator.AddSpan(normalSpan)
	}

	buckets := aggregator.FlushBuckets()

	if len(buckets) != 1 {
		t.Errorf("Expected 1 bucket, got %d", len(buckets))
	}

	if len(buckets) > 0 {
		if buckets[0].TotalSpans != 1 {
			t.Errorf("Expected 1 span, got %d", buckets[0].TotalSpans)
		}
		if buckets[0].ErrorCount != 1 {
			t.Errorf("Expected 1 error, got %d", buckets[0].ErrorCount)
		}
	}
}

// TestTelemetryPipeline_HighLatencyCapture tests high-latency span capture.
func TestTelemetryPipeline_HighLatencyCapture(t *testing.T) {
	filterConfig := FilterConfig{
		AlwaysCaptureErrors: false,
		LatencyThresholdMs:  500.0,
		SampleRate:          0.0, // No sampling.
	}
	filter := NewFilter(filterConfig)

	aggregator := NewAggregator("test-agent")
	pastTime := time.Now().Add(-2 * time.Minute).Truncate(time.Minute)

	slowSpan := Span{
		ServiceName: "database",
		SpanKind:    "INTERNAL",
		DurationMs:  750.0, // > 500ms threshold.
		IsError:     false,
		TraceID:     "trace-slow",
		Timestamp:   pastTime,
	}

	fastSpan := Span{
		ServiceName: "database",
		SpanKind:    "INTERNAL",
		DurationMs:  50.0, // < 500ms threshold.
		IsError:     false,
		TraceID:     "trace-fast",
		Timestamp:   pastTime,
	}

	// Slow span should be captured.
	if filter.ShouldCapture(slowSpan) {
		aggregator.AddSpan(slowSpan)
	}

	// Fast span should NOT be captured.
	if filter.ShouldCapture(fastSpan) {
		aggregator.AddSpan(fastSpan)
	}

	buckets := aggregator.FlushBuckets()

	if len(buckets) != 1 {
		t.Errorf("Expected 1 bucket, got %d", len(buckets))
	}

	if len(buckets) > 0 {
		if buckets[0].TotalSpans != 1 {
			t.Errorf("Expected 1 span, got %d", buckets[0].TotalSpans)
		}
		if buckets[0].P99Ms < 700 {
			t.Errorf("Expected p99 >= 700ms, got %f", buckets[0].P99Ms)
		}
	}
}

// TestTelemetryPipeline_MultipleServices tests aggregation across services.
func TestTelemetryPipeline_MultipleServices(t *testing.T) {
	aggregator := NewAggregator("test-agent")
	pastTime := time.Now().Add(-2 * time.Minute).Truncate(time.Minute)

	// Add spans for different services.
	services := []string{"checkout", "payment", "inventory", "shipping"}
	for _, service := range services {
		for i := 0; i < 10; i++ {
			span := Span{
				ServiceName: service,
				SpanKind:    "SERVER",
				DurationMs:  float64(100 + i*10),
				IsError:     i%5 == 0, // 20% error rate.
				TraceID:     service + "-trace-" + string(rune('0'+i)),
				Timestamp:   pastTime,
			}
			aggregator.AddSpan(span)
		}
	}

	buckets := aggregator.FlushBuckets()

	// Should have 4 buckets (one per service).
	if len(buckets) != 4 {
		t.Errorf("Expected 4 buckets, got %d", len(buckets))
	}

	// Verify each bucket.
	for _, bucket := range buckets {
		if bucket.TotalSpans != 10 {
			t.Errorf("Expected 10 spans for %s, got %d", bucket.ServiceName, bucket.TotalSpans)
		}
		if bucket.ErrorCount != 2 {
			t.Errorf("Expected 2 errors for %s, got %d", bucket.ServiceName, bucket.ErrorCount)
		}
	}
}

// TestTelemetryPipeline_BucketAlignment tests time bucket alignment.
func TestTelemetryPipeline_BucketAlignment(t *testing.T) {
	aggregator := NewAggregator("test-agent")

	// Add spans from different seconds within the same minute.
	baseTime := time.Now().Add(-2 * time.Minute).Truncate(time.Minute)

	spans := []Span{
		{
			ServiceName: "api",
			SpanKind:    "SERVER",
			DurationMs:  100.0,
			IsError:     false,
			TraceID:     "trace-1",
			Timestamp:   baseTime.Add(5 * time.Second), // :05.
		},
		{
			ServiceName: "api",
			SpanKind:    "SERVER",
			DurationMs:  200.0,
			IsError:     false,
			TraceID:     "trace-2",
			Timestamp:   baseTime.Add(30 * time.Second), // :30.
		},
		{
			ServiceName: "api",
			SpanKind:    "SERVER",
			DurationMs:  150.0,
			IsError:     false,
			TraceID:     "trace-3",
			Timestamp:   baseTime.Add(55 * time.Second), // :55.
		},
	}

	for _, span := range spans {
		aggregator.AddSpan(span)
	}

	buckets := aggregator.FlushBuckets()

	// All spans should be in the same bucket (same minute).
	if len(buckets) != 1 {
		t.Errorf("Expected 1 bucket (same minute), got %d", len(buckets))
	}

	if len(buckets) > 0 {
		if buckets[0].TotalSpans != 3 {
			t.Errorf("Expected 3 spans in bucket, got %d", buckets[0].TotalSpans)
		}
		// Bucket time should be truncated to minute.
		if buckets[0].BucketTime != baseTime {
			t.Errorf("Expected bucket_time=%v, got %v", baseTime, buckets[0].BucketTime)
		}
	}
}
