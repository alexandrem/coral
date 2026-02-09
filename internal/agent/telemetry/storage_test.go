package telemetry

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/rs/zerolog"
)

func setupTestTelemetryStorage(t *testing.T) (*Storage, func()) {
	t.Helper()

	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

	logger := zerolog.Nop()
	storage, err := NewStorage(db, logger)
	if err != nil {
		_ = db.Close()
		t.Fatalf("failed to create storage: %v", err)
	}

	cleanup := func() {
		_ = db.Close()
	}

	return storage, cleanup
}

func TestNewStorage(t *testing.T) {
	storage, cleanup := setupTestTelemetryStorage(t)
	defer cleanup()

	if storage == nil {
		t.Fatal("NewStorage() returned nil")
	}

	if storage.db == nil {
		t.Error("Storage.db is nil")
	}

	if storage.spansTable == nil {
		t.Error("Storage.spansTable is nil")
	}
}

func TestStorage_StoreSpan(t *testing.T) {
	storage, cleanup := setupTestTelemetryStorage(t)
	defer cleanup()

	ctx := context.Background()

	tests := []struct {
		name    string
		span    Span
		wantErr bool
	}{
		{
			name: "store basic span",
			span: Span{
				Timestamp:   time.Now(),
				TraceID:     "trace-123",
				SpanID:      "span-456",
				ServiceName: "test-service",
				SpanKind:    "SERVER",
				DurationMs:  100.5,
				IsError:     false,
				Attributes:  map[string]string{},
			},
			wantErr: false,
		},
		{
			name: "store error span",
			span: Span{
				Timestamp:   time.Now(),
				TraceID:     "trace-456",
				SpanID:      "span-789",
				ServiceName: "error-service",
				SpanKind:    "SERVER",
				DurationMs:  200.0,
				IsError:     true,
				HTTPStatus:  500,
				Attributes:  map[string]string{"error": "true"},
			},
			wantErr: false,
		},
		{
			name: "store HTTP span",
			span: Span{
				Timestamp:   time.Now(),
				TraceID:     "trace-http",
				SpanID:      "span-http",
				ServiceName: "http-service",
				SpanKind:    "SERVER",
				DurationMs:  150.0,
				IsError:     false,
				HTTPStatus:  200,
				HTTPMethod:  "GET",
				HTTPRoute:   "/api/users",
				Attributes:  map[string]string{"http.method": "GET"},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := storage.StoreSpan(ctx, tt.span)
			if (err != nil) != tt.wantErr {
				t.Errorf("StoreSpan() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestStorage_CleanupOldSpans(t *testing.T) {
	storage, cleanup := setupTestTelemetryStorage(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	// Insert spans at different ages.
	spans := []Span{
		{
			Timestamp:   now.Add(-2 * time.Hour),
			TraceID:     "old-trace",
			SpanID:      "old-span",
			ServiceName: "service-a",
			SpanKind:    "SERVER",
			DurationMs:  100.0,
			Attributes:  map[string]string{},
		},
		{
			Timestamp:   now.Add(-30 * time.Minute),
			TraceID:     "recent-trace",
			SpanID:      "recent-span",
			ServiceName: "service-b",
			SpanKind:    "SERVER",
			DurationMs:  200.0,
			Attributes:  map[string]string{},
		},
		{
			Timestamp:   now,
			TraceID:     "current-trace",
			SpanID:      "current-span",
			ServiceName: "service-c",
			SpanKind:    "SERVER",
			DurationMs:  150.0,
			Attributes:  map[string]string{},
		},
	}

	for _, span := range spans {
		if err := storage.StoreSpan(ctx, span); err != nil {
			t.Fatalf("Failed to store test span: %v", err)
		}
	}

	// Cleanup spans older than 1 hour.
	err := storage.CleanupOldSpans(ctx, 1*time.Hour)
	if err != nil {
		t.Errorf("CleanupOldSpans() error = %v", err)
		return
	}

	// Query all remaining spans using seq_id-based query.
	results, _, err := storage.QuerySpansBySeqID(ctx, 0, 10000, nil)
	if err != nil {
		t.Errorf("QuerySpansBySeqID() after cleanup failed: %v", err)
		return
	}

	// Should have 2 spans remaining (recent and current).
	if len(results) > 2 {
		t.Errorf("After cleanup, found %d spans, want at most 2", len(results))
	}
}

func TestStorage_GetSpanCount(t *testing.T) {
	storage, cleanup := setupTestTelemetryStorage(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	// Insert test spans.
	for i := 0; i < 5; i++ {
		span := Span{
			Timestamp:   now.Add(-time.Duration(i) * time.Minute),
			TraceID:     "trace-" + string(rune('0'+i)),
			SpanID:      "span-" + string(rune('0'+i)),
			ServiceName: "test-service",
			SpanKind:    "SERVER",
			DurationMs:  100.0,
			Attributes:  map[string]string{},
		}

		if err := storage.StoreSpan(ctx, span); err != nil {
			t.Fatalf("Failed to store test span: %v", err)
		}
	}

	// Count spans.
	count, err := storage.GetSpanCount(ctx)
	if err != nil {
		t.Errorf("GetSpanCount() error = %v", err)
		return
	}

	if count != 5 {
		t.Errorf("GetSpanCount() = %d, want 5", count)
	}
}

func TestStorage_QuerySpansBySeqID(t *testing.T) {
	storage, cleanup := setupTestTelemetryStorage(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	// Insert 5 spans.
	for i := 0; i < 5; i++ {
		span := Span{
			Timestamp:   now.Add(-time.Duration(5-i) * time.Minute),
			TraceID:     fmt.Sprintf("trace-%d", i),
			SpanID:      fmt.Sprintf("span-%d", i),
			ServiceName: "test-service",
			SpanKind:    "SERVER",
			DurationMs:  100.0,
			Attributes:  map[string]string{},
		}
		if err := storage.StoreSpan(ctx, span); err != nil {
			t.Fatalf("Failed to store span: %v", err)
		}
	}

	// Query all spans (seq_id > 0).
	spans, maxSeqID, err := storage.QuerySpansBySeqID(ctx, 0, 100, nil)
	if err != nil {
		t.Fatalf("QuerySpansBySeqID() error: %v", err)
	}
	if len(spans) != 5 {
		t.Errorf("Expected 5 spans, got %d", len(spans))
	}
	if maxSeqID == 0 {
		t.Error("Expected maxSeqID > 0")
	}

	// Query with seq_id > maxSeqID should return nothing.
	spans2, maxSeqID2, err := storage.QuerySpansBySeqID(ctx, maxSeqID, 100, nil)
	if err != nil {
		t.Fatalf("QuerySpansBySeqID() error: %v", err)
	}
	if len(spans2) != 0 {
		t.Errorf("Expected 0 spans after last seq_id, got %d", len(spans2))
	}
	if maxSeqID2 != 0 {
		t.Errorf("Expected maxSeqID 0 for empty result, got %d", maxSeqID2)
	}

	// Insert 2 more spans and query from the previous max.
	for i := 5; i < 7; i++ {
		span := Span{
			Timestamp:   now,
			TraceID:     fmt.Sprintf("trace-%d", i),
			SpanID:      fmt.Sprintf("span-%d", i),
			ServiceName: "test-service",
			SpanKind:    "SERVER",
			DurationMs:  50.0,
			Attributes:  map[string]string{},
		}
		if err := storage.StoreSpan(ctx, span); err != nil {
			t.Fatalf("Failed to store span: %v", err)
		}
	}

	spans3, maxSeqID3, err := storage.QuerySpansBySeqID(ctx, maxSeqID, 100, nil)
	if err != nil {
		t.Fatalf("QuerySpansBySeqID() error: %v", err)
	}
	if len(spans3) != 2 {
		t.Errorf("Expected 2 new spans, got %d", len(spans3))
	}
	if maxSeqID3 <= maxSeqID {
		t.Errorf("Expected maxSeqID3 > %d, got %d", maxSeqID, maxSeqID3)
	}
}

func TestStorage_SeqIDMonotonicallyIncreasing(t *testing.T) {
	storage, cleanup := setupTestTelemetryStorage(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	// Insert 10 spans.
	for i := 0; i < 10; i++ {
		span := Span{
			Timestamp:   now.Add(-time.Duration(10-i) * time.Minute),
			TraceID:     fmt.Sprintf("trace-%d", i),
			SpanID:      fmt.Sprintf("span-%d", i),
			ServiceName: "test-service",
			SpanKind:    "SERVER",
			DurationMs:  100.0,
			Attributes:  map[string]string{},
		}
		if err := storage.StoreSpan(ctx, span); err != nil {
			t.Fatalf("Failed to store span: %v", err)
		}
	}

	// Query all and verify seq_ids are monotonically increasing.
	spans, _, err := storage.QuerySpansBySeqID(ctx, 0, 100, nil)
	if err != nil {
		t.Fatalf("QuerySpansBySeqID() error: %v", err)
	}

	var prevSeqID uint64
	for _, span := range spans {
		if span.SeqID <= prevSeqID {
			t.Errorf("SeqID %d is not greater than previous %d", span.SeqID, prevSeqID)
		}
		prevSeqID = span.SeqID
	}
}

func TestNewAggregator(t *testing.T) {
	agentID := "test-agent"
	agg := NewAggregator(agentID)

	if agg == nil {
		t.Fatal("NewAggregator() returned nil")
	}

	if agg.agentID != agentID {
		t.Errorf("Aggregator.agentID = %q, want %q", agg.agentID, agentID)
	}

	if agg.buckets == nil {
		t.Error("Aggregator.buckets is nil")
	}
}
