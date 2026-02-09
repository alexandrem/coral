package beyla

import (
	"context"
	"database/sql"
	"testing"
	"time"

	ebpfpb "github.com/coral-mesh/coral/coral/mesh/v1"
	_ "github.com/marcboeker/go-duckdb"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func setupTestStorage(t *testing.T) (*BeylaStorage, func()) {
	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}

	logger := zerolog.Nop()
	storage, err := NewBeylaStorage(db, ":memory:", logger)
	if err != nil {
		db.Close()
		t.Fatalf("Failed to create storage: %v", err)
	}

	cleanup := func() {
		db.Close()
	}

	return storage, cleanup
}

func TestStoreTrace(t *testing.T) {
	storage, cleanup := setupTestStorage(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	tests := []struct {
		name    string
		event   *ebpfpb.EbpfEvent
		wantErr bool
	}{
		{
			name: "valid trace span",
			event: &ebpfpb.EbpfEvent{
				Timestamp:   timestamppb.New(now),
				CollectorId: "test-collector",
				AgentId:     "test-agent",
				ServiceName: "payments-api",
				Payload: &ebpfpb.EbpfEvent_BeylaTrace{
					BeylaTrace: &ebpfpb.BeylaTraceSpan{
						TraceId:      "abc123def456789012345678901234ab",
						SpanId:       "1234567890abcdef",
						ParentSpanId: "fedcba0987654321",
						ServiceName:  "payments-api",
						SpanName:     "POST /api/v1/payments",
						SpanKind:     "server",
						StartTime:    timestamppb.New(now),
						Duration:     durationpb.New(450 * time.Millisecond),
						StatusCode:   200,
						Attributes: map[string]string{
							"http.method": "POST",
							"http.route":  "/api/v1/payments",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "root span (no parent)",
			event: &ebpfpb.EbpfEvent{
				Timestamp:   timestamppb.New(now),
				CollectorId: "test-collector",
				AgentId:     "test-agent",
				ServiceName: "frontend-api",
				Payload: &ebpfpb.EbpfEvent_BeylaTrace{
					BeylaTrace: &ebpfpb.BeylaTraceSpan{
						TraceId:      "abc123def456789012345678901234ab",
						SpanId:       "fedcba0987654321",
						ParentSpanId: "",
						ServiceName:  "frontend-api",
						SpanName:     "GET /checkout",
						SpanKind:     "server",
						StartTime:    timestamppb.New(now),
						Duration:     durationpb.New(1200 * time.Millisecond),
						StatusCode:   200,
						Attributes:   map[string]string{},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "nil trace payload",
			event: &ebpfpb.EbpfEvent{
				Timestamp:   timestamppb.New(now),
				CollectorId: "test-collector",
				AgentId:     "test-agent",
				ServiceName: "test-service",
				Payload:     &ebpfpb.EbpfEvent_BeylaTrace{},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := storage.StoreTrace(ctx, tt.event)
			if (err != nil) != tt.wantErr {
				t.Errorf("StoreTrace() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestQueryTraceByID(t *testing.T) {
	storage, cleanup := setupTestStorage(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	traceID := "trace1abc000000000000000000000001"

	// Insert test trace with multiple spans.
	spans := []*ebpfpb.EbpfEvent{
		{
			Timestamp:   timestamppb.New(now),
			CollectorId: "test-collector",
			AgentId:     "test-agent",
			ServiceName: "frontend-api",
			Payload: &ebpfpb.EbpfEvent_BeylaTrace{
				BeylaTrace: &ebpfpb.BeylaTraceSpan{
					TraceId:      traceID,
					SpanId:       "span00000000001",
					ParentSpanId: "",
					ServiceName:  "frontend-api",
					SpanName:     "GET /checkout",
					SpanKind:     "server",
					StartTime:    timestamppb.New(now),
					Duration:     durationpb.New(1200 * time.Millisecond),
					StatusCode:   200,
					Attributes:   map[string]string{},
				},
			},
		},
		{
			Timestamp:   timestamppb.New(now),
			CollectorId: "test-collector",
			AgentId:     "test-agent",
			ServiceName: "payments-api",
			Payload: &ebpfpb.EbpfEvent_BeylaTrace{
				BeylaTrace: &ebpfpb.BeylaTraceSpan{
					TraceId:      traceID,
					SpanId:       "span00000000002",
					ParentSpanId: "span00000000001",
					ServiceName:  "payments-api",
					SpanName:     "POST /api/v1/payments",
					SpanKind:     "client",
					StartTime:    timestamppb.New(now),
					Duration:     durationpb.New(450 * time.Millisecond),
					StatusCode:   200,
					Attributes:   map[string]string{},
				},
			},
		},
	}

	for _, span := range spans {
		if err := storage.StoreTrace(ctx, span); err != nil {
			t.Fatalf("Failed to store test trace: %v", err)
		}
	}

	// Query by trace ID.
	result, err := storage.QueryTraceByID(ctx, traceID)
	if err != nil {
		t.Fatalf("QueryTraceByID() error = %v", err)
	}

	if len(result) != 2 {
		t.Errorf("QueryTraceByID() returned %d spans, want 2", len(result))
	}

	// Verify all spans have the correct trace ID.
	for _, span := range result {
		if span.TraceId != traceID {
			t.Errorf("Span has TraceId %s, want %s", span.TraceId, traceID)
		}
	}

	// Test non-existent trace.
	result, err = storage.QueryTraceByID(ctx, "nonexistent0000000000000000000")
	if err != nil {
		t.Fatalf("QueryTraceByID() error = %v", err)
	}

	if len(result) != 0 {
		t.Errorf("QueryTraceByID() returned %d spans for non-existent trace, want 0", len(result))
	}
}

func TestQueryHTTPMetricsBySeqID(t *testing.T) {
	storage, cleanup := setupTestStorage(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	// Insert HTTP metrics via EbpfEvents.
	for i := 0; i < 5; i++ {
		event := &ebpfpb.EbpfEvent{
			Timestamp:   timestamppb.New(now.Add(-time.Duration(5-i) * time.Minute)),
			CollectorId: "test-collector",
			AgentId:     "test-agent",
			ServiceName: "api-service",
			Payload: &ebpfpb.EbpfEvent_BeylaHttp{
				BeylaHttp: &ebpfpb.BeylaHttpMetrics{
					Timestamp:      timestamppb.New(now.Add(-time.Duration(5-i) * time.Minute)),
					ServiceName:    "api-service",
					HttpMethod:     "GET",
					HttpRoute:      "/api/v1/users",
					HttpStatusCode: 200,
					LatencyBuckets: []float64{10.0},
					LatencyCounts:  []uint64{1},
					Attributes:     map[string]string{},
				},
			},
		}
		if err := storage.StoreHTTPMetric(ctx, event); err != nil {
			t.Fatalf("StoreHTTPMetric() error: %v", err)
		}
	}

	// Query all (seq_id > 0).
	metrics, maxSeqID, err := storage.QueryHTTPMetricsBySeqID(ctx, 0, 100, nil)
	if err != nil {
		t.Fatalf("QueryHTTPMetricsBySeqID() error: %v", err)
	}
	if len(metrics) != 5 {
		t.Errorf("Expected 5 metrics, got %d", len(metrics))
	}
	if maxSeqID == 0 {
		t.Error("Expected maxSeqID > 0")
	}

	// Query after max should return empty.
	metrics2, _, err := storage.QueryHTTPMetricsBySeqID(ctx, maxSeqID, 100, nil)
	if err != nil {
		t.Fatalf("QueryHTTPMetricsBySeqID() error: %v", err)
	}
	if len(metrics2) != 0 {
		t.Errorf("Expected 0 metrics after max seq_id, got %d", len(metrics2))
	}
}

func TestQueryTracesBySeqID(t *testing.T) {
	storage, cleanup := setupTestStorage(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	// Insert 3 trace spans.
	for i := 0; i < 3; i++ {
		event := &ebpfpb.EbpfEvent{
			Timestamp:   timestamppb.New(now.Add(-time.Duration(3-i) * time.Minute)),
			CollectorId: "test-collector",
			AgentId:     "test-agent",
			ServiceName: "api-service",
			Payload: &ebpfpb.EbpfEvent_BeylaTrace{
				BeylaTrace: &ebpfpb.BeylaTraceSpan{
					TraceId:      "traceabc000000000000000000000001",
					SpanId:       "span0000000000" + string(rune('1'+i)),
					ParentSpanId: "",
					ServiceName:  "api-service",
					SpanName:     "GET /test",
					SpanKind:     "server",
					StartTime:    timestamppb.New(now.Add(-time.Duration(3-i) * time.Minute)),
					Duration:     durationpb.New(100 * time.Millisecond),
					StatusCode:   200,
					Attributes:   map[string]string{},
				},
			},
		}
		if err := storage.StoreTrace(ctx, event); err != nil {
			t.Fatalf("StoreTrace() error: %v", err)
		}
	}

	// Query all (seq_id > 0).
	spans, maxSeqID, err := storage.QueryTracesBySeqID(ctx, 0, 100, nil)
	if err != nil {
		t.Fatalf("QueryTracesBySeqID() error: %v", err)
	}
	if len(spans) != 3 {
		t.Errorf("Expected 3 spans, got %d", len(spans))
	}
	if maxSeqID == 0 {
		t.Error("Expected maxSeqID > 0")
	}

	// Query after max should return empty.
	spans2, _, err := storage.QueryTracesBySeqID(ctx, maxSeqID, 100, nil)
	if err != nil {
		t.Fatalf("QueryTracesBySeqID() error: %v", err)
	}
	if len(spans2) != 0 {
		t.Errorf("Expected 0 spans after max seq_id, got %d", len(spans2))
	}
}

func TestTraceCleanup(t *testing.T) {
	storage, cleanup := setupTestStorage(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	// Insert old trace (beyond retention).
	oldTrace := &ebpfpb.EbpfEvent{
		Timestamp:   timestamppb.New(now.Add(-2 * time.Hour)),
		CollectorId: "test-collector",
		AgentId:     "test-agent",
		ServiceName: "old-service",
		Payload: &ebpfpb.EbpfEvent_BeylaTrace{
			BeylaTrace: &ebpfpb.BeylaTraceSpan{
				TraceId:      "oldtrace0000000000000000000000001",
				SpanId:       "span00000000001",
				ParentSpanId: "",
				ServiceName:  "old-service",
				SpanName:     "GET /old",
				SpanKind:     "server",
				StartTime:    timestamppb.New(now.Add(-2 * time.Hour)),
				Duration:     durationpb.New(100 * time.Millisecond),
				StatusCode:   200,
				Attributes:   map[string]string{},
			},
		},
	}

	// Insert recent trace (within retention).
	recentTrace := &ebpfpb.EbpfEvent{
		Timestamp:   timestamppb.New(now.Add(-30 * time.Minute)),
		CollectorId: "test-collector",
		AgentId:     "test-agent",
		ServiceName: "recent-service",
		Payload: &ebpfpb.EbpfEvent_BeylaTrace{
			BeylaTrace: &ebpfpb.BeylaTraceSpan{
				TraceId:      "recent00000000000000000000000001",
				SpanId:       "span00000000002",
				ParentSpanId: "",
				ServiceName:  "recent-service",
				SpanName:     "GET /recent",
				SpanKind:     "server",
				StartTime:    timestamppb.New(now.Add(-30 * time.Minute)),
				Duration:     durationpb.New(100 * time.Millisecond),
				StatusCode:   200,
				Attributes:   map[string]string{},
			},
		},
	}

	if err := storage.StoreTrace(ctx, oldTrace); err != nil {
		t.Fatalf("Failed to store old trace: %v", err)
	}
	if err := storage.StoreTrace(ctx, recentTrace); err != nil {
		t.Fatalf("Failed to store recent trace: %v", err)
	}

	// Run cleanup with 1 hour retention.
	cutoff := now.Add(-1 * time.Hour)
	_, err := storage.db.ExecContext(ctx, "DELETE FROM beyla_traces_local WHERE start_time < ?", cutoff)
	if err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}

	// Verify old trace was deleted.
	oldSpans, err := storage.QueryTraceByID(ctx, "oldtrace0000000000000000000000001")
	if err != nil {
		t.Fatalf("QueryTraceByID() error = %v", err)
	}
	if len(oldSpans) != 0 {
		t.Errorf("Old trace should be deleted, found %d spans", len(oldSpans))
	}

	// Verify recent trace still exists.
	recentSpans, err := storage.QueryTraceByID(ctx, "recent00000000000000000000000001")
	if err != nil {
		t.Fatalf("QueryTraceByID() error = %v", err)
	}
	if len(recentSpans) != 1 {
		t.Errorf("Recent trace should exist, found %d spans", len(recentSpans))
	}
}
