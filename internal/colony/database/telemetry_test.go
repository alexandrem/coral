package database

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestInsertTelemetryBuckets(t *testing.T) {
	// Create temporary database.
	tmpDir := t.TempDir()
	db, err := New(tmpDir, "test-colony", zerolog.Nop())
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Create test buckets.
	now := time.Now().Truncate(time.Minute)
	buckets := []TelemetryBucket{
		{
			BucketTime:   now,
			AgentID:      "agent-1",
			ServiceName:  "checkout",
			SpanKind:     "SERVER",
			P50Ms:        100.0,
			P95Ms:        250.0,
			P99Ms:        500.0,
			ErrorCount:   5,
			TotalSpans:   100,
			SampleTraces: []string{"trace-1", "trace-2", "trace-3"},
		},
		{
			BucketTime:   now,
			AgentID:      "agent-1",
			ServiceName:  "payment",
			SpanKind:     "CLIENT",
			P50Ms:        50.0,
			P95Ms:        120.0,
			P99Ms:        200.0,
			ErrorCount:   2,
			TotalSpans:   50,
			SampleTraces: []string{"trace-4", "trace-5"},
		},
	}

	// Insert buckets.
	err = db.InsertTelemetryBuckets(ctx, buckets)
	if err != nil {
		t.Fatalf("Failed to insert buckets: %v", err)
	}

	// Query buckets back.
	retrieved, err := db.QueryTelemetryBuckets(ctx, "agent-1", now.Add(-1*time.Minute), now.Add(1*time.Minute))
	if err != nil {
		t.Fatalf("Failed to query buckets: %v", err)
	}

	if len(retrieved) != 2 {
		t.Errorf("Expected 2 buckets, got %d", len(retrieved))
	}

	// Verify first bucket.
	if len(retrieved) > 0 {
		bucket := retrieved[0]
		if bucket.ServiceName != "payment" && bucket.ServiceName != "checkout" {
			t.Errorf("Unexpected service name: %s", bucket.ServiceName)
		}
		if bucket.AgentID != "agent-1" {
			t.Errorf("Expected agent_id='agent-1', got '%s'", bucket.AgentID)
		}
	}
}

func TestInsertTelemetryBuckets_Upsert(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := New(tmpDir, "test-colony", zerolog.Nop())
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Minute)

	// Insert initial bucket.
	initial := []TelemetryBucket{
		{
			BucketTime:   now,
			AgentID:      "agent-1",
			ServiceName:  "checkout",
			SpanKind:     "SERVER",
			P50Ms:        100.0,
			P95Ms:        200.0,
			P99Ms:        300.0,
			ErrorCount:   5,
			TotalSpans:   100,
			SampleTraces: []string{"trace-1"},
		},
	}

	err = db.InsertTelemetryBuckets(ctx, initial)
	if err != nil {
		t.Fatalf("Failed to insert initial bucket: %v", err)
	}

	// Update with new values (same key: bucket_time, agent_id, service_name, span_kind).
	updated := []TelemetryBucket{
		{
			BucketTime:   now,
			AgentID:      "agent-1",
			ServiceName:  "checkout",
			SpanKind:     "SERVER",
			P50Ms:        150.0, // Updated.
			P95Ms:        250.0, // Updated.
			P99Ms:        400.0, // Updated.
			ErrorCount:   10,    // Updated.
			TotalSpans:   200,   // Updated.
			SampleTraces: []string{"trace-1", "trace-2"}, // Updated.
		},
	}

	err = db.InsertTelemetryBuckets(ctx, updated)
	if err != nil {
		t.Fatalf("Failed to upsert bucket: %v", err)
	}

	// Query and verify update.
	retrieved, err := db.QueryTelemetryBuckets(ctx, "agent-1", now.Add(-1*time.Minute), now.Add(1*time.Minute))
	if err != nil {
		t.Fatalf("Failed to query buckets: %v", err)
	}

	if len(retrieved) != 1 {
		t.Errorf("Expected 1 bucket after upsert, got %d", len(retrieved))
	}

	if len(retrieved) > 0 {
		bucket := retrieved[0]
		if bucket.P50Ms != 150.0 {
			t.Errorf("Expected p50=150.0 after upsert, got %f", bucket.P50Ms)
		}
		if bucket.ErrorCount != 10 {
			t.Errorf("Expected error_count=10 after upsert, got %d", bucket.ErrorCount)
		}
		if bucket.TotalSpans != 200 {
			t.Errorf("Expected total_spans=200 after upsert, got %d", bucket.TotalSpans)
		}
	}
}

func TestCleanupOldTelemetry(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := New(tmpDir, "test-colony", zerolog.Nop())
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Insert buckets with different ages.
	now := time.Now().Truncate(time.Minute)
	buckets := []TelemetryBucket{
		{
			BucketTime:   now.Add(-25 * time.Hour), // Old (>24 hours).
			AgentID:      "agent-1",
			ServiceName:  "old-service",
			SpanKind:     "SERVER",
			P50Ms:        100.0,
			P95Ms:        200.0,
			P99Ms:        300.0,
			ErrorCount:   1,
			TotalSpans:   10,
			SampleTraces: []string{"trace-old"},
		},
		{
			BucketTime:   now.Add(-23 * time.Hour), // Recent (<24 hours).
			AgentID:      "agent-1",
			ServiceName:  "recent-service",
			SpanKind:     "SERVER",
			P50Ms:        100.0,
			P95Ms:        200.0,
			P99Ms:        300.0,
			ErrorCount:   1,
			TotalSpans:   10,
			SampleTraces: []string{"trace-recent"},
		},
		{
			BucketTime:   now, // Current.
			AgentID:      "agent-1",
			ServiceName:  "current-service",
			SpanKind:     "SERVER",
			P50Ms:        100.0,
			P95Ms:        200.0,
			P99Ms:        300.0,
			ErrorCount:   1,
			TotalSpans:   10,
			SampleTraces: []string{"trace-current"},
		},
	}

	err = db.InsertTelemetryBuckets(ctx, buckets)
	if err != nil {
		t.Fatalf("Failed to insert buckets: %v", err)
	}

	// Run cleanup with 24-hour retention.
	deleted, err := db.CleanupOldTelemetry(ctx, 24)
	if err != nil {
		t.Fatalf("Failed to cleanup: %v", err)
	}

	if deleted != 1 {
		t.Errorf("Expected 1 deleted row, got %d", deleted)
	}

	// Verify only recent and current buckets remain.
	retrieved, err := db.QueryTelemetryBuckets(ctx, "agent-1", now.Add(-30*time.Hour), now.Add(1*time.Hour))
	if err != nil {
		t.Fatalf("Failed to query buckets: %v", err)
	}

	if len(retrieved) != 2 {
		t.Errorf("Expected 2 remaining buckets, got %d", len(retrieved))
	}

	// Verify old bucket is gone.
	for _, bucket := range retrieved {
		if bucket.ServiceName == "old-service" {
			t.Error("Old bucket should have been deleted")
		}
	}
}

func TestQueryTelemetryBuckets_TimeRange(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := New(tmpDir, "test-colony", zerolog.Nop())
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Minute)

	// Insert buckets at different times.
	buckets := []TelemetryBucket{
		{
			BucketTime:   now.Add(-60 * time.Minute),
			AgentID:      "agent-1",
			ServiceName:  "service-1",
			SpanKind:     "SERVER",
			P50Ms:        100.0,
			P95Ms:        200.0,
			P99Ms:        300.0,
			ErrorCount:   1,
			TotalSpans:   10,
			SampleTraces: []string{"trace-1"},
		},
		{
			BucketTime:   now.Add(-30 * time.Minute),
			AgentID:      "agent-1",
			ServiceName:  "service-2",
			SpanKind:     "SERVER",
			P50Ms:        100.0,
			P95Ms:        200.0,
			P99Ms:        300.0,
			ErrorCount:   1,
			TotalSpans:   10,
			SampleTraces: []string{"trace-2"},
		},
		{
			BucketTime:   now,
			AgentID:      "agent-1",
			ServiceName:  "service-3",
			SpanKind:     "SERVER",
			P50Ms:        100.0,
			P95Ms:        200.0,
			P99Ms:        300.0,
			ErrorCount:   1,
			TotalSpans:   10,
			SampleTraces: []string{"trace-3"},
		},
	}

	err = db.InsertTelemetryBuckets(ctx, buckets)
	if err != nil {
		t.Fatalf("Failed to insert buckets: %v", err)
	}

	// Query only the middle bucket.
	retrieved, err := db.QueryTelemetryBuckets(ctx, "agent-1", now.Add(-45*time.Minute), now.Add(-15*time.Minute))
	if err != nil {
		t.Fatalf("Failed to query buckets: %v", err)
	}

	if len(retrieved) != 1 {
		t.Errorf("Expected 1 bucket in time range, got %d", len(retrieved))
	}

	if len(retrieved) > 0 && retrieved[0].ServiceName != "service-2" {
		t.Errorf("Expected service-2, got %s", retrieved[0].ServiceName)
	}
}

func TestQueryTelemetryBuckets_DifferentAgents(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := New(tmpDir, "test-colony", zerolog.Nop())
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Minute)

	// Insert buckets for different agents.
	buckets := []TelemetryBucket{
		{
			BucketTime:   now,
			AgentID:      "agent-1",
			ServiceName:  "service-1",
			SpanKind:     "SERVER",
			P50Ms:        100.0,
			P95Ms:        200.0,
			P99Ms:        300.0,
			ErrorCount:   1,
			TotalSpans:   10,
			SampleTraces: []string{"trace-1"},
		},
		{
			BucketTime:   now,
			AgentID:      "agent-2",
			ServiceName:  "service-2",
			SpanKind:     "SERVER",
			P50Ms:        100.0,
			P95Ms:        200.0,
			P99Ms:        300.0,
			ErrorCount:   1,
			TotalSpans:   10,
			SampleTraces: []string{"trace-2"},
		},
	}

	err = db.InsertTelemetryBuckets(ctx, buckets)
	if err != nil {
		t.Fatalf("Failed to insert buckets: %v", err)
	}

	// Query agent-1 only.
	retrieved, err := db.QueryTelemetryBuckets(ctx, "agent-1", now.Add(-1*time.Minute), now.Add(1*time.Minute))
	if err != nil {
		t.Fatalf("Failed to query buckets: %v", err)
	}

	if len(retrieved) != 1 {
		t.Errorf("Expected 1 bucket for agent-1, got %d", len(retrieved))
	}

	if len(retrieved) > 0 && retrieved[0].AgentID != "agent-1" {
		t.Errorf("Expected agent-1, got %s", retrieved[0].AgentID)
	}
}
