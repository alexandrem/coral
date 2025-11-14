package telemetry

import (
	"testing"
	"time"
)

func TestFilter_ShouldCapture_Errors(t *testing.T) {
	config := FilterConfig{
		AlwaysCaptureErrors:  true,
		LatencyThresholdMs:   500.0,
		SampleRate:           0.0, // No sampling for normal spans.
	}

	filter := NewFilter(config)

	// Error spans should always be captured.
	errorSpan := Span{
		ServiceName: "test-service",
		SpanKind:    "SERVER",
		DurationMs:  100.0,
		IsError:     true,
		TraceID:     "trace-1",
		Timestamp:   time.Now(),
	}

	if !filter.ShouldCapture(errorSpan) {
		t.Error("Expected error span to be captured")
	}

	// Normal spans should not be captured when sample rate is 0.
	normalSpan := Span{
		ServiceName: "test-service",
		SpanKind:    "SERVER",
		DurationMs:  100.0,
		IsError:     false,
		TraceID:     "trace-2",
		Timestamp:   time.Now(),
	}

	if filter.ShouldCapture(normalSpan) {
		t.Error("Expected normal span to not be captured with 0% sample rate")
	}
}

func TestFilter_ShouldCapture_HighLatency(t *testing.T) {
	config := FilterConfig{
		AlwaysCaptureErrors:  true,
		LatencyThresholdMs:   500.0,
		SampleRate:           0.0,
	}

	filter := NewFilter(config)

	// High-latency spans should always be captured.
	highLatencySpan := Span{
		ServiceName: "test-service",
		SpanKind:    "SERVER",
		DurationMs:  600.0, // > 500ms threshold.
		IsError:     false,
		TraceID:     "trace-1",
		Timestamp:   time.Now(),
	}

	if !filter.ShouldCapture(highLatencySpan) {
		t.Error("Expected high-latency span to be captured")
	}

	// Low-latency spans should not be captured when sample rate is 0.
	lowLatencySpan := Span{
		ServiceName: "test-service",
		SpanKind:    "SERVER",
		DurationMs:  100.0,
		IsError:     false,
		TraceID:     "trace-2",
		Timestamp:   time.Now(),
	}

	if filter.ShouldCapture(lowLatencySpan) {
		t.Error("Expected low-latency span to not be captured with 0% sample rate")
	}
}

func TestFilter_ShouldCapture_SampleRate(t *testing.T) {
	config := FilterConfig{
		AlwaysCaptureErrors:  false,
		LatencyThresholdMs:   1000.0, // High threshold.
		SampleRate:           1.0,    // 100% sampling.
	}

	filter := NewFilter(config)

	// All spans should be captured with 100% sample rate.
	span := Span{
		ServiceName: "test-service",
		SpanKind:    "SERVER",
		DurationMs:  100.0,
		IsError:     false,
		TraceID:     "trace-1",
		Timestamp:   time.Now(),
	}

	if !filter.ShouldCapture(span) {
		t.Error("Expected span to be captured with 100% sample rate")
	}
}
