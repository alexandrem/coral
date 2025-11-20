package beyla

import (
	"context"
	"database/sql"
	"testing"
	"time"

	ebpfpb "github.com/coral-io/coral/coral/mesh/v1"
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
	storage, err := NewBeylaStorage(db, logger)
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

func TestQueryTraces(t *testing.T) {
	storage, cleanup := setupTestStorage(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	// Insert test traces.
	traces := []*ebpfpb.EbpfEvent{
		{
			Timestamp:   timestamppb.New(now.Add(-10 * time.Minute)),
			CollectorId: "test-collector",
			AgentId:     "test-agent",
			ServiceName: "payments-api",
			Payload: &ebpfpb.EbpfEvent_BeylaTrace{
				BeylaTrace: &ebpfpb.BeylaTraceSpan{
					TraceId:      "trace1abc000000000000000000000001",
					SpanId:       "span00000000001",
					ParentSpanId: "",
					ServiceName:  "payments-api",
					SpanName:     "POST /api/v1/payments",
					SpanKind:     "server",
					StartTime:    timestamppb.New(now.Add(-10 * time.Minute)),
					Duration:     durationpb.New(450 * time.Millisecond),
					StatusCode:   200,
					Attributes:   map[string]string{"http.method": "POST"},
				},
			},
		},
		{
			Timestamp:   timestamppb.New(now.Add(-5 * time.Minute)),
			CollectorId: "test-collector",
			AgentId:     "test-agent",
			ServiceName: "frontend-api",
			Payload: &ebpfpb.EbpfEvent_BeylaTrace{
				BeylaTrace: &ebpfpb.BeylaTraceSpan{
					TraceId:      "trace2abc000000000000000000000002",
					SpanId:       "span00000000002",
					ParentSpanId: "",
					ServiceName:  "frontend-api",
					SpanName:     "GET /checkout",
					SpanKind:     "server",
					StartTime:    timestamppb.New(now.Add(-5 * time.Minute)),
					Duration:     durationpb.New(1200 * time.Millisecond),
					StatusCode:   200,
					Attributes:   map[string]string{"http.method": "GET"},
				},
			},
		},
		{
			Timestamp:   timestamppb.New(now.Add(-5 * time.Minute)),
			CollectorId: "test-collector",
			AgentId:     "test-agent",
			ServiceName: "payments-api",
			Payload: &ebpfpb.EbpfEvent_BeylaTrace{
				BeylaTrace: &ebpfpb.BeylaTraceSpan{
					TraceId:      "trace2abc000000000000000000000002",
					SpanId:       "span00000000003",
					ParentSpanId: "span00000000002",
					ServiceName:  "payments-api",
					SpanName:     "POST /api/v1/payments",
					SpanKind:     "client",
					StartTime:    timestamppb.New(now.Add(-5 * time.Minute)),
					Duration:     durationpb.New(450 * time.Millisecond),
					StatusCode:   200,
					Attributes:   map[string]string{"http.method": "POST"},
				},
			},
		},
	}

	for _, trace := range traces {
		if err := storage.StoreTrace(ctx, trace); err != nil {
			t.Fatalf("Failed to store test trace: %v", err)
		}
	}

	tests := []struct {
		name         string
		startTime    time.Time
		endTime      time.Time
		serviceNames []string
		traceID      string
		maxSpans     int32
		wantCount    int
	}{
		{
			name:      "all traces",
			startTime: now.Add(-1 * time.Hour),
			endTime:   now,
			wantCount: 3,
		},
		{
			name:         "filter by service",
			startTime:    now.Add(-1 * time.Hour),
			endTime:      now,
			serviceNames: []string{"payments-api"},
			wantCount:    2,
		},
		{
			name:      "filter by trace ID",
			startTime: now.Add(-1 * time.Hour),
			endTime:   now,
			traceID:   "trace2abc000000000000000000000002",
			wantCount: 2,
		},
		{
			name:      "limit results",
			startTime: now.Add(-1 * time.Hour),
			endTime:   now,
			maxSpans:  1,
			wantCount: 1,
		},
		{
			name:      "time range filter",
			startTime: now.Add(-7 * time.Minute),
			endTime:   now,
			wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spans, err := storage.QueryTraces(ctx, tt.startTime, tt.endTime, tt.serviceNames, tt.traceID, tt.maxSpans)
			if err != nil {
				t.Fatalf("QueryTraces() error = %v", err)
			}

			if len(spans) != tt.wantCount {
				t.Errorf("QueryTraces() returned %d spans, want %d", len(spans), tt.wantCount)
			}

			// Verify spans are valid.
			for _, span := range spans {
				if span.TraceId == "" {
					t.Error("Span has empty TraceId")
				}
				if span.SpanId == "" {
					t.Error("Span has empty SpanId")
				}
				if span.ServiceName == "" {
					t.Error("Span has empty ServiceName")
				}
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
