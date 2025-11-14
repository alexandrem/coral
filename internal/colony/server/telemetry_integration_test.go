package server

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"

	colonyv1 "github.com/coral-io/coral/coral/colony/v1"
	"github.com/coral-io/coral/internal/colony/database"
	"github.com/coral-io/coral/internal/colony/registry"
)

// TestIngestTelemetry_EndToEnd tests the full telemetry ingestion flow.
func TestIngestTelemetry_EndToEnd(t *testing.T) {
	// Create temporary database.
	tmpDir := t.TempDir()
	db, err := database.New(tmpDir, "test-colony", zerolog.Nop())
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	// Create registry.
	reg := registry.New()

	// Create server.
	config := Config{
		ColonyID:        "test-colony",
		ApplicationName: "test-app",
		Environment:     "test",
	}
	server := New(reg, db, config, zerolog.Nop())

	ctx := context.Background()
	now := time.Now().Truncate(time.Minute)

	// Create telemetry request.
	req := connect.NewRequest(&colonyv1.IngestTelemetryRequest{
		Buckets: []*colonyv1.TelemetryBucket{
			{
				AgentId:      "agent-1",
				BucketTime:   now.Unix(),
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
				AgentId:      "agent-1",
				BucketTime:   now.Unix(),
				ServiceName:  "payment",
				SpanKind:     "CLIENT",
				P50Ms:        50.0,
				P95Ms:        120.0,
				P99Ms:        200.0,
				ErrorCount:   2,
				TotalSpans:   50,
				SampleTraces: []string{"trace-4", "trace-5"},
			},
		},
	})

	// Ingest telemetry.
	resp, err := server.IngestTelemetry(ctx, req)
	if err != nil {
		t.Fatalf("IngestTelemetry failed: %v", err)
	}

	// Verify response.
	if resp.Msg.Accepted != 2 {
		t.Errorf("Expected 2 accepted buckets, got %d", resp.Msg.Accepted)
	}
	if resp.Msg.Rejected != 0 {
		t.Errorf("Expected 0 rejected buckets, got %d", resp.Msg.Rejected)
	}
	if resp.Msg.Message != "Success" {
		t.Errorf("Expected 'Success' message, got '%s'", resp.Msg.Message)
	}

	// Query database to verify storage.
	buckets, err := db.QueryTelemetryBuckets(ctx, "agent-1", now.Add(-1*time.Minute), now.Add(1*time.Minute))
	if err != nil {
		t.Fatalf("Failed to query buckets: %v", err)
	}

	if len(buckets) != 2 {
		t.Errorf("Expected 2 stored buckets, got %d", len(buckets))
	}

	// Verify checkout bucket.
	var checkoutBucket *database.TelemetryBucket
	for i := range buckets {
		if buckets[i].ServiceName == "checkout" {
			checkoutBucket = &buckets[i]
			break
		}
	}

	if checkoutBucket == nil {
		t.Fatal("Checkout bucket not found in database")
	}

	if checkoutBucket.P50Ms != 100.0 {
		t.Errorf("Expected p50=100.0, got %f", checkoutBucket.P50Ms)
	}
	if checkoutBucket.P95Ms != 250.0 {
		t.Errorf("Expected p95=250.0, got %f", checkoutBucket.P95Ms)
	}
	if checkoutBucket.P99Ms != 500.0 {
		t.Errorf("Expected p99=500.0, got %f", checkoutBucket.P99Ms)
	}
	if checkoutBucket.ErrorCount != 5 {
		t.Errorf("Expected error_count=5, got %d", checkoutBucket.ErrorCount)
	}
	if checkoutBucket.TotalSpans != 100 {
		t.Errorf("Expected total_spans=100, got %d", checkoutBucket.TotalSpans)
	}
	if len(checkoutBucket.SampleTraces) != 3 {
		t.Errorf("Expected 3 sample traces, got %d", len(checkoutBucket.SampleTraces))
	}
}

// TestIngestTelemetry_EmptyRequest tests handling of empty request.
func TestIngestTelemetry_EmptyRequest(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := database.New(tmpDir, "test-colony", zerolog.Nop())
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	reg := registry.New()
	config := Config{
		ColonyID: "test-colony",
	}
	server := New(reg, db, config, zerolog.Nop())

	ctx := context.Background()

	// Empty request.
	req := connect.NewRequest(&colonyv1.IngestTelemetryRequest{
		Buckets: []*colonyv1.TelemetryBucket{},
	})

	resp, err := server.IngestTelemetry(ctx, req)
	if err != nil {
		t.Fatalf("IngestTelemetry failed: %v", err)
	}

	if resp.Msg.Accepted != 0 {
		t.Errorf("Expected 0 accepted buckets, got %d", resp.Msg.Accepted)
	}
	if resp.Msg.Rejected != 0 {
		t.Errorf("Expected 0 rejected buckets, got %d", resp.Msg.Rejected)
	}
	if resp.Msg.Message != "No buckets provided" {
		t.Errorf("Expected 'No buckets provided' message, got '%s'", resp.Msg.Message)
	}
}

// TestIngestTelemetry_MultipleAgents tests ingestion from multiple agents.
func TestIngestTelemetry_MultipleAgents(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := database.New(tmpDir, "test-colony", zerolog.Nop())
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	reg := registry.New()
	config := Config{
		ColonyID: "test-colony",
	}
	server := New(reg, db, config, zerolog.Nop())

	ctx := context.Background()
	now := time.Now().Truncate(time.Minute)

	// Ingest from agent-1.
	req1 := connect.NewRequest(&colonyv1.IngestTelemetryRequest{
		Buckets: []*colonyv1.TelemetryBucket{
			{
				AgentId:      "agent-1",
				BucketTime:   now.Unix(),
				ServiceName:  "service-a",
				SpanKind:     "SERVER",
				P50Ms:        100.0,
				P95Ms:        200.0,
				P99Ms:        300.0,
				ErrorCount:   1,
				TotalSpans:   10,
				SampleTraces: []string{"trace-a1"},
			},
		},
	})

	_, err = server.IngestTelemetry(ctx, req1)
	if err != nil {
		t.Fatalf("IngestTelemetry for agent-1 failed: %v", err)
	}

	// Ingest from agent-2.
	req2 := connect.NewRequest(&colonyv1.IngestTelemetryRequest{
		Buckets: []*colonyv1.TelemetryBucket{
			{
				AgentId:      "agent-2",
				BucketTime:   now.Unix(),
				ServiceName:  "service-b",
				SpanKind:     "SERVER",
				P50Ms:        150.0,
				P95Ms:        250.0,
				P99Ms:        350.0,
				ErrorCount:   2,
				TotalSpans:   20,
				SampleTraces: []string{"trace-b1"},
			},
		},
	})

	_, err = server.IngestTelemetry(ctx, req2)
	if err != nil {
		t.Fatalf("IngestTelemetry for agent-2 failed: %v", err)
	}

	// Query agent-1 buckets.
	agent1Buckets, err := db.QueryTelemetryBuckets(ctx, "agent-1", now.Add(-1*time.Minute), now.Add(1*time.Minute))
	if err != nil {
		t.Fatalf("Failed to query agent-1 buckets: %v", err)
	}

	if len(agent1Buckets) != 1 {
		t.Errorf("Expected 1 bucket for agent-1, got %d", len(agent1Buckets))
	}

	// Query agent-2 buckets.
	agent2Buckets, err := db.QueryTelemetryBuckets(ctx, "agent-2", now.Add(-1*time.Minute), now.Add(1*time.Minute))
	if err != nil {
		t.Fatalf("Failed to query agent-2 buckets: %v", err)
	}

	if len(agent2Buckets) != 1 {
		t.Errorf("Expected 1 bucket for agent-2, got %d", len(agent2Buckets))
	}

	// Verify isolation.
	if len(agent1Buckets) > 0 && agent1Buckets[0].ServiceName != "service-a" {
		t.Error("Agent-1 bucket should be for service-a")
	}
	if len(agent2Buckets) > 0 && agent2Buckets[0].ServiceName != "service-b" {
		t.Error("Agent-2 bucket should be for service-b")
	}
}

// TestIngestTelemetry_Upsert tests bucket updates.
func TestIngestTelemetry_Upsert(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := database.New(tmpDir, "test-colony", zerolog.Nop())
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	reg := registry.New()
	config := Config{
		ColonyID: "test-colony",
	}
	server := New(reg, db, config, zerolog.Nop())

	ctx := context.Background()
	now := time.Now().Truncate(time.Minute)

	// Initial ingestion.
	req1 := connect.NewRequest(&colonyv1.IngestTelemetryRequest{
		Buckets: []*colonyv1.TelemetryBucket{
			{
				AgentId:      "agent-1",
				BucketTime:   now.Unix(),
				ServiceName:  "checkout",
				SpanKind:     "SERVER",
				P50Ms:        100.0,
				P95Ms:        200.0,
				P99Ms:        300.0,
				ErrorCount:   5,
				TotalSpans:   100,
				SampleTraces: []string{"trace-1"},
			},
		},
	})

	_, err = server.IngestTelemetry(ctx, req1)
	if err != nil {
		t.Fatalf("Initial ingestion failed: %v", err)
	}

	// Update with new values (same key).
	req2 := connect.NewRequest(&colonyv1.IngestTelemetryRequest{
		Buckets: []*colonyv1.TelemetryBucket{
			{
				AgentId:      "agent-1",
				BucketTime:   now.Unix(),
				ServiceName:  "checkout",
				SpanKind:     "SERVER",
				P50Ms:        150.0,                          // Updated.
				P95Ms:        250.0,                          // Updated.
				P99Ms:        400.0,                          // Updated.
				ErrorCount:   10,                             // Updated.
				TotalSpans:   200,                            // Updated.
				SampleTraces: []string{"trace-1", "trace-2"}, // Updated.
			},
		},
	})

	resp, err := server.IngestTelemetry(ctx, req2)
	if err != nil {
		t.Fatalf("Update ingestion failed: %v", err)
	}

	if resp.Msg.Accepted != 1 {
		t.Errorf("Expected 1 accepted bucket, got %d", resp.Msg.Accepted)
	}

	// Query and verify update.
	buckets, err := db.QueryTelemetryBuckets(ctx, "agent-1", now.Add(-1*time.Minute), now.Add(1*time.Minute))
	if err != nil {
		t.Fatalf("Failed to query buckets: %v", err)
	}

	if len(buckets) != 1 {
		t.Errorf("Expected 1 bucket after upsert, got %d", len(buckets))
	}

	if len(buckets) > 0 {
		if buckets[0].P50Ms != 150.0 {
			t.Errorf("Expected updated p50=150.0, got %f", buckets[0].P50Ms)
		}
		if buckets[0].ErrorCount != 10 {
			t.Errorf("Expected updated error_count=10, got %d", buckets[0].ErrorCount)
		}
		if buckets[0].TotalSpans != 200 {
			t.Errorf("Expected updated total_spans=200, got %d", buckets[0].TotalSpans)
		}
	}
}

// TestIngestTelemetry_LargePayload tests handling of large bucket batches.
func TestIngestTelemetry_LargePayload(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := database.New(tmpDir, "test-colony", zerolog.Nop())
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	reg := registry.New()
	config := Config{
		ColonyID: "test-colony",
	}
	server := New(reg, db, config, zerolog.Nop())

	ctx := context.Background()
	now := time.Now().Truncate(time.Minute)

	// Create 100 buckets.
	buckets := make([]*colonyv1.TelemetryBucket, 100)
	for i := 0; i < 100; i++ {
		buckets[i] = &colonyv1.TelemetryBucket{
			AgentId:      "agent-1",
			BucketTime:   now.Unix(),
			ServiceName:  "service-" + string(rune('a'+i%26)),
			SpanKind:     "SERVER",
			P50Ms:        float64(100 + i),
			P95Ms:        float64(200 + i),
			P99Ms:        float64(300 + i),
			ErrorCount:   int32(i % 10),
			TotalSpans:   int32(100 + i),
			SampleTraces: []string{"trace-" + string(rune('0'+i%10))},
		}
	}

	req := connect.NewRequest(&colonyv1.IngestTelemetryRequest{
		Buckets: buckets,
	})

	resp, err := server.IngestTelemetry(ctx, req)
	if err != nil {
		t.Fatalf("IngestTelemetry failed: %v", err)
	}

	if resp.Msg.Accepted != 100 {
		t.Errorf("Expected 100 accepted buckets, got %d", resp.Msg.Accepted)
	}
	if resp.Msg.Rejected != 0 {
		t.Errorf("Expected 0 rejected buckets, got %d", resp.Msg.Rejected)
	}

	// Verify buckets were stored. Due to the primary key (bucket_time, agent_id, service_name, span_kind),
	// only 26 unique buckets are stored (one per service name from "service-a" to "service-z").
	// The remaining 74 buckets upsert existing entries.
	stored, err := db.QueryTelemetryBuckets(ctx, "agent-1", now.Add(-1*time.Minute), now.Add(1*time.Minute))
	if err != nil {
		t.Fatalf("Failed to query buckets: %v", err)
	}

	if len(stored) != 26 {
		t.Errorf("Expected 26 stored buckets (one per unique service), got %d", len(stored))
	}
}
