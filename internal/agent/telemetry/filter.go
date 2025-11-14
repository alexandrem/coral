package telemetry

import (
	"math/rand"
)

// Filter applies static filtering rules to determine if a span should be captured.
type Filter struct {
	config FilterConfig
	rng    *rand.Rand
}

// NewFilter creates a new span filter.
func NewFilter(config FilterConfig) *Filter {
	return &Filter{
		config: config,
		rng:    rand.New(rand.NewSource(rand.Int63())),
	}
}

// ShouldCapture determines if a span should be captured based on filtering rules.
func (f *Filter) ShouldCapture(span Span) bool {
	// Rule 1: Always capture error spans.
	if f.config.AlwaysCaptureErrors && span.IsError {
		return true
	}

	// Rule 2: Always capture high-latency spans.
	if span.DurationMs > f.config.HighLatencyThresholdMs {
		return true
	}

	// Rule 3: Sample normal spans based on sample rate.
	return f.rng.Float64() < f.config.SampleRate
}
