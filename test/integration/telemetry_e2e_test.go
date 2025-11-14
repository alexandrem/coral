package integration

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"

	colonyv1 "github.com/coral-io/coral/coral/colony/v1"
	"github.com/coral-io/coral/internal/agent/telemetry"
	"github.com/coral-io/coral/internal/colony/database"
	"github.com/coral-io/coral/internal/colony/registry"
	"github.com/coral-io/coral/internal/colony/server"
)

// TestTelemetryEndToEnd tests the complete telemetry pipeline:
// Agent -> Filter -> Aggregator -> Colony Server -> Database.
func TestTelemetryEndToEnd(t *testing.T) {
	// Setup colony database and server.
	tmpDir := t.TempDir()
	db, err := database.New(tmpDir, "test-colony", zerolog.Nop())
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	reg := registry.New(zerolog.Nop())
	colonyServer := server.New(reg, db, server.Config{
		ColonyID:        "test-colony",
		ApplicationName: "test-app",
		Environment:     "test",
	}, zerolog.Nop())

	// Setup agent telemetry components.
	agentID := "agent-1"
	filterConfig := telemetry.FilterConfig{
		AlwaysCaptureErrors:  true,
		LatencyThresholdMs:   500.0,
		SampleRate:           0.10, // 10% sampling.
	}
	filter := telemetry.NewFilter(filterConfig)
	aggregator := telemetry.NewAggregator(agentID)

	// Simulate incoming spans over the past minute.
	pastTime := time.Now().Add(-2 * time.Minute).Truncate(time.Minute)

	spans := []telemetry.Span{
		// Checkout service spans.
		{
			ServiceName: "checkout",
			SpanKind:    "SERVER",
			DurationMs:  100.0,
			IsError:     false,
			TraceID:     "checkout-trace-1",
			Timestamp:   pastTime,
		},
		{
			ServiceName: "checkout",
			SpanKind:    "SERVER",
			DurationMs:  600.0, // High latency - should be captured.
			IsError:     false,
			TraceID:     "checkout-trace-2",
			Timestamp:   pastTime,
		},
		{
			ServiceName: "checkout",
			SpanKind:    "SERVER",
			DurationMs:  150.0,
			IsError:     true, // Error - should be captured.
			TraceID:     "checkout-trace-3",
			Timestamp:   pastTime,
		},
		{
			ServiceName: "checkout",
			SpanKind:    "SERVER",
			DurationMs:  120.0,
			IsError:     false,
			TraceID:     "checkout-trace-4",
			Timestamp:   pastTime,
		},
		{
			ServiceName: "checkout",
			SpanKind:    "SERVER",
			DurationMs:  180.0,
			IsError:     false,
			TraceID:     "checkout-trace-5",
			Timestamp:   pastTime,
		},
		// Payment service spans.
		{
			ServiceName: "payment",
			SpanKind:    "CLIENT",
			DurationMs:  50.0,
			IsError:     false,
			TraceID:     "payment-trace-1",
			Timestamp:   pastTime,
		},
		{
			ServiceName: "payment",
			SpanKind:    "CLIENT",
			DurationMs:  75.0,
			IsError:     true, // Error - should be captured.
			TraceID:     "payment-trace-2",
			Timestamp:   pastTime,
		},
	}

	// Process spans through filter and aggregator (simulating agent processing).
	for _, span := range spans {
		if filter.ShouldCapture(span) {
			aggregator.AddSpan(span)
		}
	}

	// Flush aggregated buckets (simulating periodic agent flush).
	agentBuckets := aggregator.FlushBuckets()

	if len(agentBuckets) == 0 {
		t.Fatal("Expected aggregated buckets, got none")
	}

	t.Logf("Agent aggregated %d buckets", len(agentBuckets))

	// Convert agent buckets to protobuf format.
	pbBuckets := make([]*colonyv1.TelemetryBucket, 0, len(agentBuckets))
	for _, bucket := range agentBuckets {
		pbBuckets = append(pbBuckets, &colonyv1.TelemetryBucket{
			AgentId:      bucket.AgentID,
			BucketTime:   bucket.BucketTime.Unix(),
			ServiceName:  bucket.ServiceName,
			SpanKind:     bucket.SpanKind,
			P50Ms:        bucket.P50Ms,
			P95Ms:        bucket.P95Ms,
			P99Ms:        bucket.P99Ms,
			ErrorCount:   bucket.ErrorCount,
			TotalSpans:   bucket.TotalSpans,
			SampleTraces: bucket.SampleTraces,
		})
	}

	// Send to colony (simulating agent -> colony RPC).
	ctx := context.Background()
	req := connect.NewRequest(&colonyv1.IngestTelemetryRequest{
		Buckets: pbBuckets,
	})

	resp, err := colonyServer.IngestTelemetry(ctx, req)
	if err != nil {
		t.Fatalf("Colony ingestion failed: %v", err)
	}

	if resp.Msg.Rejected > 0 {
		t.Errorf("Expected 0 rejected buckets, got %d: %s", resp.Msg.Rejected, resp.Msg.Message)
	}

	t.Logf("Colony accepted %d buckets", resp.Msg.Accepted)

	// Query database to verify end-to-end storage.
	storedBuckets, err := db.QueryTelemetryBuckets(ctx, agentID, pastTime.Add(-1*time.Minute), pastTime.Add(1*time.Minute))
	if err != nil {
		t.Fatalf("Failed to query stored buckets: %v", err)
	}

	if len(storedBuckets) == 0 {
		t.Fatal("Expected stored buckets, got none")
	}

	t.Logf("Database contains %d buckets", len(storedBuckets))

	// Verify checkout bucket.
	var checkoutBucket *database.TelemetryBucket
	for i := range storedBuckets {
		if storedBuckets[i].ServiceName == "checkout" {
			checkoutBucket = &storedBuckets[i]
			break
		}
	}

	if checkoutBucket == nil {
		t.Fatal("Checkout bucket not found in database")
	}

	// Verify bucket data makes sense.
	if checkoutBucket.AgentID != agentID {
		t.Errorf("Expected agent_id=%s, got %s", agentID, checkoutBucket.AgentID)
	}

	if checkoutBucket.BucketTime != pastTime {
		t.Errorf("Expected bucket_time=%v, got %v", pastTime, checkoutBucket.BucketTime)
	}

	if checkoutBucket.TotalSpans == 0 {
		t.Error("Expected total_spans > 0")
	}

	if checkoutBucket.P50Ms == 0 || checkoutBucket.P95Ms == 0 || checkoutBucket.P99Ms == 0 {
		t.Error("Expected percentiles to be calculated")
	}

	// Should have captured at least the error span and high-latency span.
	if checkoutBucket.TotalSpans < 2 {
		t.Errorf("Expected at least 2 checkout spans (error + high-latency), got %d", checkoutBucket.TotalSpans)
	}

	// Should have 1 error (checkout-trace-3).
	if checkoutBucket.ErrorCount < 1 {
		t.Errorf("Expected at least 1 error, got %d", checkoutBucket.ErrorCount)
	}

	t.Logf("Checkout bucket: p50=%fms, p95=%fms, p99=%fms, errors=%d, total=%d",
		checkoutBucket.P50Ms, checkoutBucket.P95Ms, checkoutBucket.P99Ms,
		checkoutBucket.ErrorCount, checkoutBucket.TotalSpans)

	// Verify payment bucket.
	var paymentBucket *database.TelemetryBucket
	for i := range storedBuckets {
		if storedBuckets[i].ServiceName == "payment" {
			paymentBucket = &storedBuckets[i]
			break
		}
	}

	if paymentBucket == nil {
		t.Fatal("Payment bucket not found in database")
	}

	// Payment should have captured at least the error span.
	if paymentBucket.ErrorCount < 1 {
		t.Errorf("Expected at least 1 error in payment, got %d", paymentBucket.ErrorCount)
	}

	t.Logf("Payment bucket: p50=%fms, p95=%fms, p99=%fms, errors=%d, total=%d",
		paymentBucket.P50Ms, paymentBucket.P95Ms, paymentBucket.P99Ms,
		paymentBucket.ErrorCount, paymentBucket.TotalSpans)
}

// TestTelemetryEndToEnd_Cleanup tests the TTL cleanup flow.
func TestTelemetryEndToEnd_Cleanup(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := database.New(tmpDir, "test-colony", zerolog.Nop())
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Minute)

	// Insert old and recent buckets.
	buckets := []database.TelemetryBucket{
		{
			BucketTime:   now.Add(-25 * time.Hour), // Old.
			AgentID:      "agent-1",
			ServiceName:  "old-service",
			SpanKind:     "SERVER",
			P50Ms:        100.0,
			P95Ms:        200.0,
			P99Ms:        300.0,
			ErrorCount:   1,
			TotalSpans:   10,
			SampleTraces: []string{"old-trace"},
		},
		{
			BucketTime:   now.Add(-1 * time.Hour), // Recent.
			AgentID:      "agent-1",
			ServiceName:  "recent-service",
			SpanKind:     "SERVER",
			P50Ms:        100.0,
			P95Ms:        200.0,
			P99Ms:        300.0,
			ErrorCount:   1,
			TotalSpans:   10,
			SampleTraces: []string{"recent-trace"},
		},
	}

	err = db.InsertTelemetryBuckets(ctx, buckets)
	if err != nil {
		t.Fatalf("Failed to insert buckets: %v", err)
	}

	// Verify both exist.
	allBuckets, err := db.QueryTelemetryBuckets(ctx, "agent-1", now.Add(-30*time.Hour), now.Add(1*time.Hour))
	if err != nil {
		t.Fatalf("Failed to query all buckets: %v", err)
	}

	if len(allBuckets) != 2 {
		t.Errorf("Expected 2 buckets before cleanup, got %d", len(allBuckets))
	}

	// Run cleanup (24-hour TTL).
	deleted, err := db.CleanupOldTelemetry(ctx, 24)
	if err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}

	t.Logf("Cleanup deleted %d buckets", deleted)

	if deleted != 1 {
		t.Errorf("Expected 1 deleted bucket, got %d", deleted)
	}

	// Verify only recent bucket remains.
	remaining, err := db.QueryTelemetryBuckets(ctx, "agent-1", now.Add(-30*time.Hour), now.Add(1*time.Hour))
	if err != nil {
		t.Fatalf("Failed to query remaining buckets: %v", err)
	}

	if len(remaining) != 1 {
		t.Errorf("Expected 1 remaining bucket, got %d", len(remaining))
	}

	if len(remaining) > 0 && remaining[0].ServiceName != "recent-service" {
		t.Error("Expected recent-service to remain after cleanup")
	}
}

// TestTelemetryEndToEnd_MultipleFlushes tests multiple flush cycles.
func TestTelemetryEndToEnd_MultipleFlushes(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := database.New(tmpDir, "test-colony", zerolog.Nop())
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	reg := registry.New(zerolog.Nop())
	colonyServer := server.New(reg, db, server.Config{
		ColonyID: "test-colony",
	}, zerolog.Nop())

	agentID := "agent-1"
	aggregator := telemetry.NewAggregator(agentID)
	ctx := context.Background()

	// Simulate 3 flush cycles (3 minutes of data).
	for minute := 0; minute < 3; minute++ {
		bucketTime := time.Now().Add(time.Duration(-3+minute) * time.Minute).Truncate(time.Minute)

		// Add spans for this minute.
		for i := 0; i < 10; i++ {
			span := telemetry.Span{
				ServiceName: "api",
				SpanKind:    "SERVER",
				DurationMs:  float64(100 + i*10),
				IsError:     i%3 == 0,
				TraceID:     "trace-" + string(rune('0'+minute)) + "-" + string(rune('0'+i)),
				Timestamp:   bucketTime,
			}
			aggregator.AddSpan(span)
		}

		// Flush and send to colony.
		buckets := aggregator.FlushBuckets()
		if len(buckets) == 0 {
			continue
		}

		pbBuckets := make([]*colonyv1.TelemetryBucket, 0, len(buckets))
		for _, bucket := range buckets {
			pbBuckets = append(pbBuckets, &colonyv1.TelemetryBucket{
				AgentId:      bucket.AgentID,
				BucketTime:   bucket.BucketTime.Unix(),
				ServiceName:  bucket.ServiceName,
				SpanKind:     bucket.SpanKind,
				P50Ms:        bucket.P50Ms,
				P95Ms:        bucket.P95Ms,
				P99Ms:        bucket.P99Ms,
				ErrorCount:   bucket.ErrorCount,
				TotalSpans:   bucket.TotalSpans,
				SampleTraces: bucket.SampleTraces,
			})
		}

		req := connect.NewRequest(&colonyv1.IngestTelemetryRequest{
			Buckets: pbBuckets,
		})

		_, err := colonyServer.IngestTelemetry(ctx, req)
		if err != nil {
			t.Fatalf("Ingestion failed for minute %d: %v", minute, err)
		}
	}

	// Query all buckets.
	startTime := time.Now().Add(-10 * time.Minute)
	endTime := time.Now()
	allBuckets, err := db.QueryTelemetryBuckets(ctx, agentID, startTime, endTime)
	if err != nil {
		t.Fatalf("Failed to query buckets: %v", err)
	}

	// Should have 3 buckets (one per minute).
	if len(allBuckets) != 3 {
		t.Errorf("Expected 3 buckets (one per flush), got %d", len(allBuckets))
	}

	// Verify each bucket has data.
	for i, bucket := range allBuckets {
		if bucket.TotalSpans != 10 {
			t.Errorf("Bucket %d: expected 10 spans, got %d", i, bucket.TotalSpans)
		}
		if bucket.ErrorCount == 0 {
			t.Errorf("Bucket %d: expected some errors", i)
		}
	}

	t.Logf("Successfully processed and stored %d buckets across 3 flush cycles", len(allBuckets))
}
