// Package telemetry provides telemetry data collection and aggregation.
package telemetry

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// Span represents a filtered OpenTelemetry span for local storage.
type Span struct {
	Timestamp   time.Time
	TraceID     string
	SpanID      string
	ServiceName string
	SpanKind    string
	DurationMs  float64
	IsError     bool
	HTTPStatus  int
	HTTPMethod  string
	HTTPRoute   string
	Attributes  map[string]string
}

// Bucket represents a 1-minute aggregated telemetry bucket.
type Bucket struct {
	BucketTime   time.Time
	AgentID      string
	ServiceName  string
	SpanKind     string
	P50Ms        float64
	P95Ms        float64
	P99Ms        float64
	ErrorCount   int32
	TotalSpans   int32
	SampleTraces []string
}

// Aggregator aggregates spans into 1-minute buckets.
type Aggregator struct {
	agentID string
	buckets map[string]*bucketData // key: "bucket_time:service:kind"
	mu      sync.RWMutex
}

// bucketData holds raw data for aggregation.
type bucketData struct {
	durations []float64
	traces    []string
	errors    int32
	total     int32
}

// NewAggregator creates a new span aggregator.
func NewAggregator(agentID string) *Aggregator {
	return &Aggregator{
		agentID: agentID,
		buckets: make(map[string]*bucketData),
	}
}

// AddSpan adds a span to the aggregator.
func (a *Aggregator) AddSpan(span Span) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Align timestamp to 1-minute bucket.
	bucketTime := span.Timestamp.Truncate(time.Minute)
	key := getBucketKey(bucketTime, span.ServiceName, span.SpanKind)

	// Get or create bucket data.
	bucket, exists := a.buckets[key]
	if !exists {
		bucket = &bucketData{
			durations: make([]float64, 0, 100),
			traces:    make([]string, 0, 5),
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
		bucket.traces = append(bucket.traces, span.TraceID)
	}
}

// FlushBuckets returns all completed buckets and clears them.
// A bucket is considered complete if it's older than the current minute.
func (a *Aggregator) FlushBuckets() []Bucket {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	currentBucket := now.Truncate(time.Minute)

	buckets := make([]Bucket, 0)

	for key, data := range a.buckets {
		bucketTime, service, kind := parseBucketKey(key)

		// Only flush buckets from previous minutes.
		if !bucketTime.Before(currentBucket) {
			continue
		}

		// Calculate percentiles.
		p50, p95, p99 := calculatePercentiles(data.durations)

		bucket := Bucket{
			BucketTime:   bucketTime,
			AgentID:      a.agentID,
			ServiceName:  service,
			SpanKind:     kind,
			P50Ms:        p50,
			P95Ms:        p95,
			P99Ms:        p99,
			ErrorCount:   data.errors,
			TotalSpans:   data.total,
			SampleTraces: data.traces,
		}

		buckets = append(buckets, bucket)

		// Remove flushed bucket.
		delete(a.buckets, key)
	}

	return buckets
}

// getBucketKey creates a unique key for a bucket.
func getBucketKey(bucketTime time.Time, service, kind string) string {
	return bucketTime.Format(time.RFC3339) + "|" + service + "|" + kind
}

// parseBucketKey parses a bucket key back into components.
func parseBucketKey(key string) (time.Time, string, string) {
	// Key format: "bucketTime|service|kind"
	parts := strings.SplitN(key, "|", 3)
	if len(parts) != 3 {
		return time.Time{}, "", ""
	}

	// Parse the bucket time.
	bucketTime, err := time.Parse(time.RFC3339, parts[0])
	if err != nil {
		return time.Time{}, "", ""
	}

	return bucketTime, parts[1], parts[2]
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
