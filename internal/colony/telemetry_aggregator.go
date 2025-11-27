package colony

import (
	"sort"
	"sync"
	"time"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/colony/database"
)

// TelemetryAggregator aggregates spans queried from agents into 1-minute buckets.
// This runs at the colony level (pull-based architecture, RFD 025).
type TelemetryAggregator struct {
	buckets map[string]*bucketData // key: "agent_id:bucket_time:service:kind"
	mu      sync.RWMutex
}

// bucketData holds raw data for aggregation.
type bucketData struct {
	agentID     string
	bucketTime  time.Time
	serviceName string
	spanKind    string
	durations   []float64
	traces      []string
	errors      int32
	total       int32
}

// NewTelemetryAggregator creates a new telemetry aggregator.
func NewTelemetryAggregator() *TelemetryAggregator {
	return &TelemetryAggregator{
		buckets: make(map[string]*bucketData),
	}
}

// AddSpans adds spans from an agent query to the aggregator.
func (a *TelemetryAggregator) AddSpans(agentID string, spans []*agentv1.TelemetrySpan) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, span := range spans {
		// Convert timestamp from Unix milliseconds to time.Time.
		timestamp := time.UnixMilli(span.Timestamp)

		// Align timestamp to 1-minute bucket.
		bucketTime := timestamp.Truncate(time.Minute)
		key := getBucketKey(agentID, bucketTime, span.ServiceName, span.SpanKind)

		// Get or create bucket data.
		bucket, exists := a.buckets[key]
		if !exists {
			bucket = &bucketData{
				agentID:     agentID,
				bucketTime:  bucketTime,
				serviceName: span.ServiceName,
				spanKind:    span.SpanKind,
				durations:   make([]float64, 0, 100),
				traces:      make([]string, 0, 5),
			}
			a.buckets[key] = bucket
		}

		// Add duration for percentile calculation.
		bucket.durations = append(bucket.durations, span.DurationMs)
		bucket.total++

		// Track errors.
		if span.IsError {
			bucket.errors++
		}

		// Keep sample traces (max 5).
		if len(bucket.traces) < 5 {
			bucket.traces = append(bucket.traces, span.TraceId)
		}
	}
}

// GetSummaries returns aggregated summaries for all buckets.
func (a *TelemetryAggregator) GetSummaries() []database.TelemetrySummary {
	a.mu.RLock()
	defer a.mu.RUnlock()

	summaries := make([]database.TelemetrySummary, 0, len(a.buckets))

	for _, data := range a.buckets {
		// Calculate percentiles.
		p50, p95, p99 := calculatePercentiles(data.durations)

		summary := database.TelemetrySummary{
			BucketTime:   data.bucketTime,
			AgentID:      data.agentID,
			ServiceName:  data.serviceName,
			SpanKind:     data.spanKind,
			P50Ms:        p50,
			P95Ms:        p95,
			P99Ms:        p99,
			ErrorCount:   data.errors,
			TotalSpans:   data.total,
			SampleTraces: data.traces,
		}

		summaries = append(summaries, summary)
	}

	return summaries
}

// Clear clears all buckets after they've been stored.
func (a *TelemetryAggregator) Clear() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.buckets = make(map[string]*bucketData)
}

// getBucketKey creates a unique key for a bucket.
func getBucketKey(agentID string, bucketTime time.Time, service, kind string) string {
	return agentID + "|" + bucketTime.Format(time.RFC3339) + "|" + service + "|" + kind
}

// calculatePercentiles calculates p50, p95, and p99 from a slice of durations.
func calculatePercentiles(durations []float64) (p50, p95, p99 float64) {
	if len(durations) == 0 {
		return 0, 0, 0
	}

	// Sort durations.
	sorted := make([]float64, len(durations))
	copy(sorted, durations)
	sort.Float64s(sorted)

	n := len(sorted)
	p50 = sorted[int(float64(n)*0.50)]
	p95 = sorted[min(int(float64(n)*0.95), n-1)]
	p99 = sorted[min(int(float64(n)*0.99), n-1)]

	return p50, p95, p99
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
