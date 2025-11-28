package database

import (
	"context"
	"testing"
	"time"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/rs/zerolog"
)

func TestInsertBeylaTraces(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()

	// Create logger for test.
	logger := zerolog.Nop()

	// Initialize database.
	db, err := New(tempDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	agentID := "test-agent-001"
	now := time.Now()

	tests := []struct {
		name    string
		spans   []*agentv1.BeylaTraceSpan
		wantErr bool
	}{
		{
			name: "insert single trace span",
			spans: []*agentv1.BeylaTraceSpan{
				{
					TraceId:      "abc123def456789012345678901234ab",
					SpanId:       "1234567890abcdef",
					ParentSpanId: "",
					ServiceName:  "payments-api",
					SpanName:     "POST /api/v1/payments",
					SpanKind:     "server",
					StartTime:    now.UnixMilli(),
					DurationUs:   450000,
					StatusCode:   200,
					Attributes: map[string]string{
						"http.method": "POST",
						"http.route":  "/api/v1/payments",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "insert multiple trace spans",
			spans: []*agentv1.BeylaTraceSpan{
				{
					TraceId:      "trace2abc000000000000000000000002",
					SpanId:       "span00000000001",
					ParentSpanId: "",
					ServiceName:  "frontend-api",
					SpanName:     "GET /checkout",
					SpanKind:     "server",
					StartTime:    now.UnixMilli(),
					DurationUs:   1200000,
					StatusCode:   200,
					Attributes:   map[string]string{"http.method": "GET"},
				},
				{
					TraceId:      "trace2abc000000000000000000000002",
					SpanId:       "span00000000002",
					ParentSpanId: "span00000000001",
					ServiceName:  "payments-api",
					SpanName:     "POST /api/v1/payments",
					SpanKind:     "client",
					StartTime:    now.UnixMilli(),
					DurationUs:   450000,
					StatusCode:   200,
					Attributes:   map[string]string{"http.method": "POST"},
				},
			},
			wantErr: false,
		},
		{
			name:    "insert empty slice",
			spans:   []*agentv1.BeylaTraceSpan{},
			wantErr: false,
		},
		{
			name:    "insert nil slice",
			spans:   nil,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := db.InsertBeylaTraces(ctx, agentID, tt.spans)
			if (err != nil) != tt.wantErr {
				t.Errorf("InsertBeylaTraces() error = %v, wantErr %v", err, tt.wantErr)
			}

			// Verify spans were inserted.
			if !tt.wantErr && len(tt.spans) > 0 {
				var count int
				err := db.db.QueryRow("SELECT COUNT(*) FROM beyla_traces WHERE agent_id = ?", agentID).Scan(&count)
				if err != nil {
					t.Fatalf("Failed to count traces: %v", err)
				}
			}
		})
	}
}

func TestInsertBeylaTraces_Deduplication(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()

	// Create logger for test.
	logger := zerolog.Nop()

	// Initialize database.
	db, err := New(tempDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	agentID := "test-agent-001"
	now := time.Now()

	// Insert same trace span twice.
	span := &agentv1.BeylaTraceSpan{
		TraceId:      "dedup000000000000000000000000001",
		SpanId:       "span00000000001",
		ParentSpanId: "",
		ServiceName:  "test-service",
		SpanName:     "GET /test",
		SpanKind:     "server",
		StartTime:    now.UnixMilli(),
		DurationUs:   100000,
		StatusCode:   200,
		Attributes:   map[string]string{},
	}

	// First insert.
	if err := db.InsertBeylaTraces(ctx, agentID, []*agentv1.BeylaTraceSpan{span}); err != nil {
		t.Fatalf("First insert failed: %v", err)
	}

	// Second insert (should be ignored due to ON CONFLICT DO NOTHING).
	if err := db.InsertBeylaTraces(ctx, agentID, []*agentv1.BeylaTraceSpan{span}); err != nil {
		t.Fatalf("Second insert failed: %v", err)
	}

	// Verify only one row exists.
	var count int
	err = db.db.QueryRow("SELECT COUNT(*) FROM beyla_traces WHERE trace_id = ? AND span_id = ?", span.TraceId, span.SpanId).Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count traces: %v", err)
	}

	if count != 1 {
		t.Errorf("Expected 1 trace span, got %d (deduplication failed)", count)
	}
}

func TestInsertBeylaTraces_AgentID(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()

	// Create logger for test.
	logger := zerolog.Nop()

	// Initialize database.
	db, err := New(tempDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now()

	// Insert spans from different agents.
	span1 := &agentv1.BeylaTraceSpan{
		TraceId:      "multi000000000000000000000000001",
		SpanId:       "span00000000001",
		ParentSpanId: "",
		ServiceName:  "service-a",
		SpanName:     "GET /a",
		SpanKind:     "server",
		StartTime:    now.UnixMilli(),
		DurationUs:   100000,
		StatusCode:   200,
		Attributes:   map[string]string{},
	}

	span2 := &agentv1.BeylaTraceSpan{
		TraceId:      "multi000000000000000000000000001",
		SpanId:       "span00000000002",
		ParentSpanId: "span00000000001",
		ServiceName:  "service-b",
		SpanName:     "GET /b",
		SpanKind:     "client",
		StartTime:    now.UnixMilli(),
		DurationUs:   50000,
		StatusCode:   200,
		Attributes:   map[string]string{},
	}

	if err := db.InsertBeylaTraces(ctx, "agent-001", []*agentv1.BeylaTraceSpan{span1}); err != nil {
		t.Fatalf("Failed to insert span from agent-001: %v", err)
	}

	if err := db.InsertBeylaTraces(ctx, "agent-002", []*agentv1.BeylaTraceSpan{span2}); err != nil {
		t.Fatalf("Failed to insert span from agent-002: %v", err)
	}

	// Verify both spans are stored with correct agent_id.
	var agent1ID, agent2ID string
	err = db.db.QueryRow("SELECT agent_id FROM beyla_traces WHERE span_id = ?", span1.SpanId).Scan(&agent1ID)
	if err != nil {
		t.Fatalf("Failed to query span1: %v", err)
	}
	if agent1ID != "agent-001" {
		t.Errorf("Span1 has agent_id %s, want agent-001", agent1ID)
	}

	err = db.db.QueryRow("SELECT agent_id FROM beyla_traces WHERE span_id = ?", span2.SpanId).Scan(&agent2ID)
	if err != nil {
		t.Fatalf("Failed to query span2: %v", err)
	}
	if agent2ID != "agent-002" {
		t.Errorf("Span2 has agent_id %s, want agent-002", agent2ID)
	}
}

func TestCleanupOldBeylaTraces(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()

	// Create logger for test.
	logger := zerolog.Nop()

	// Initialize database.
	db, err := New(tempDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	agentID := "test-agent-001"
	now := time.Now()

	// Insert old trace (beyond retention).
	oldSpan := &agentv1.BeylaTraceSpan{
		TraceId:      "oldtrace0000000000000000000000001",
		SpanId:       "span00000000001",
		ParentSpanId: "",
		ServiceName:  "old-service",
		SpanName:     "GET /old",
		SpanKind:     "server",
		StartTime:    now.Add(-8 * 24 * time.Hour).UnixMilli(),
		DurationUs:   100000,
		StatusCode:   200,
		Attributes:   map[string]string{},
	}

	// Insert recent trace (within retention).
	recentSpan := &agentv1.BeylaTraceSpan{
		TraceId:      "recent00000000000000000000000001",
		SpanId:       "span00000000002",
		ParentSpanId: "",
		ServiceName:  "recent-service",
		SpanName:     "GET /recent",
		SpanKind:     "server",
		StartTime:    now.Add(-3 * 24 * time.Hour).UnixMilli(),
		DurationUs:   100000,
		StatusCode:   200,
		Attributes:   map[string]string{},
	}

	if err := db.InsertBeylaTraces(ctx, agentID, []*agentv1.BeylaTraceSpan{oldSpan, recentSpan}); err != nil {
		t.Fatalf("Failed to insert test traces: %v", err)
	}

	// Run cleanup with 7 day retention.
	deleted, err := db.CleanupOldBeylaTraces(ctx, 7)
	if err != nil {
		t.Fatalf("CleanupOldBeylaTraces() error = %v", err)
	}

	if deleted != 1 {
		t.Errorf("CleanupOldBeylaTraces() deleted %d rows, want 1", deleted)
	}

	// Verify old trace was deleted.
	var count int
	err = db.db.QueryRow("SELECT COUNT(*) FROM beyla_traces WHERE trace_id = ?", oldSpan.TraceId).Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count old traces: %v", err)
	}
	if count != 0 {
		t.Errorf("Old trace should be deleted, found %d spans", count)
	}

	// Verify recent trace still exists.
	err = db.db.QueryRow("SELECT COUNT(*) FROM beyla_traces WHERE trace_id = ?", recentSpan.TraceId).Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count recent traces: %v", err)
	}
	if count != 1 {
		t.Errorf("Recent trace should exist, found %d spans", count)
	}
}

func TestCleanupOldBeylaTraces_NoOldTraces(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()

	// Create logger for test.
	logger := zerolog.Nop()

	// Initialize database.
	db, err := New(tempDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Run cleanup on empty database.
	deleted, err := db.CleanupOldBeylaTraces(ctx, 7)
	if err != nil {
		t.Fatalf("CleanupOldBeylaTraces() error = %v", err)
	}

	if deleted != 0 {
		t.Errorf("CleanupOldBeylaTraces() deleted %d rows, want 0", deleted)
	}
}

func TestBeylaTracesSchema(t *testing.T) {
	// Create temporary directory for test.
	tempDir := t.TempDir()

	// Create logger for test.
	logger := zerolog.Nop()

	// Initialize database.
	db, err := New(tempDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	// Verify table exists.
	var tableName string
	err = db.db.QueryRow("SELECT table_name FROM information_schema.tables WHERE table_name = 'beyla_traces'").Scan(&tableName)
	if err != nil {
		t.Fatalf("beyla_traces table does not exist: %v", err)
	}

	// Verify indexes exist by querying DuckDB's system tables.
	// DuckDB stores index information in duckdb_indexes() table function.
	rows, err := db.db.Query("SELECT index_name FROM duckdb_indexes()")
	if err != nil {
		t.Fatalf("Failed to query indexes: %v", err)
	}
	defer rows.Close()

	indexMap := make(map[string]bool)
	for rows.Next() {
		var indexName string
		if err := rows.Scan(&indexName); err != nil {
			t.Fatalf("Failed to scan index name: %v", err)
		}
		indexMap[indexName] = true
	}

	// Verify expected indexes exist.
	expectedIndexes := []string{
		"idx_beyla_traces_service_time",
		"idx_beyla_traces_trace_id",
		"idx_beyla_traces_duration",
		"idx_beyla_traces_agent_id",
	}

	for _, index := range expectedIndexes {
		if !indexMap[index] {
			t.Errorf("Index %s does not exist", index)
		}
	}
}
