package telemetry

import (
	"context"
	"database/sql"
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

func TestStorage_QuerySpans(t *testing.T) {
	storage, cleanup := setupTestTelemetryStorage(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	// Insert test data.
	testSpans := []Span{
		{
			Timestamp:   now.Add(-10 * time.Minute),
			TraceID:     "trace-1",
			SpanID:      "span-1",
			ServiceName: "service-a",
			SpanKind:    "SERVER",
			DurationMs:  100.0,
			IsError:     false,
			Attributes:  map[string]string{},
		},
		{
			Timestamp:   now.Add(-5 * time.Minute),
			TraceID:     "trace-2",
			SpanID:      "span-2",
			ServiceName: "service-a",
			SpanKind:    "SERVER",
			DurationMs:  200.0,
			IsError:     true,
			Attributes:  map[string]string{},
		},
		{
			Timestamp:   now.Add(-5 * time.Minute),
			TraceID:     "trace-3",
			SpanID:      "span-3",
			ServiceName: "service-b",
			SpanKind:    "CLIENT",
			DurationMs:  50.0,
			IsError:     false,
			Attributes:  map[string]string{},
		},
	}

	for _, span := range testSpans {
		if err := storage.StoreSpan(ctx, span); err != nil {
			t.Fatalf("Failed to store test span: %v", err)
		}
	}

	tests := []struct {
		name         string
		startTime    time.Time
		endTime      time.Time
		serviceNames []string
		wantCount    int
		wantErr      bool
	}{
		{
			name:         "query all spans",
			startTime:    now.Add(-15 * time.Minute),
			endTime:      now,
			serviceNames: nil,
			wantCount:    3,
			wantErr:      false,
		},
		{
			name:         "query specific service",
			startTime:    now.Add(-15 * time.Minute),
			endTime:      now,
			serviceNames: []string{"service-a"},
			wantCount:    2,
			wantErr:      false,
		},
		{
			name:         "query with narrow time range",
			startTime:    now.Add(-6 * time.Minute),
			endTime:      now,
			serviceNames: nil,
			wantCount:    2,
			wantErr:      false,
		},
		{
			name:         "query non-existent service",
			startTime:    now.Add(-15 * time.Minute),
			endTime:      now,
			serviceNames: []string{"non-existent"},
			wantCount:    0,
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := storage.QuerySpans(ctx, tt.startTime, tt.endTime, tt.serviceNames)
			if (err != nil) != tt.wantErr {
				t.Errorf("QuerySpans() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if len(results) != tt.wantCount {
				t.Errorf("QuerySpans() returned %d spans, want %d", len(results), tt.wantCount)
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

	// Query all remaining spans.
	results, err := storage.QuerySpans(ctx, now.Add(-3*time.Hour), now.Add(1*time.Minute), nil)
	if err != nil {
		t.Errorf("QuerySpans() after cleanup failed: %v", err)
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
