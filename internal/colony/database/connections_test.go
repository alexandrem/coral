package database

import (
	"context"
	"testing"
	"time"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/constants"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestDatabase creates an in-memory DuckDB database for connection tests.
func newTestDatabase(t *testing.T) (*Database, func()) {
	t.Helper()
	tempDir := t.TempDir()
	db, err := New(tempDir, "test-colony", constants.DefaultConnectionsCacheTTL, zerolog.Nop())
	require.NoError(t, err, "Failed to create test database")
	return db, func() { _ = db.Close() }
}

// insertCrossServiceSpan inserts a parent span in parentSvc and a child span in childSvc
// linked by the same trace ID. This represents a single cross-service call.
func insertCrossServiceSpan(t *testing.T, db *Database, traceID, parentSpanID, childSpanID, parentSvc, childSvc string, offset time.Duration) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().Add(offset)

	parent := &agentv1.EbpfTraceSpan{
		TraceId:     traceID,
		SpanId:      parentSpanID,
		ServiceName: parentSvc,
		SpanName:    "GET /api",
		SpanKind:    "client",
		StartTime:   now.UnixMilli(),
		DurationUs:  1000,
		StatusCode:  200,
	}
	require.NoError(t, db.InsertBeylaTraces(ctx, "agent-parent", []*agentv1.EbpfTraceSpan{parent}))

	child := &agentv1.EbpfTraceSpan{
		TraceId:      traceID,
		SpanId:       childSpanID,
		ParentSpanId: parentSpanID,
		ServiceName:  childSvc,
		SpanName:     "POST /rpc",
		SpanKind:     "server",
		StartTime:    now.UnixMilli(),
		DurationUs:   500,
		StatusCode:   200,
	}
	require.NoError(t, db.InsertBeylaTraces(ctx, "agent-child", []*agentv1.EbpfTraceSpan{child}))
}

func TestMaterializeConnections_DetectsEdge(t *testing.T) {
	db, cleanup := newTestDatabase(t)
	defer cleanup()

	// Insert a single cross-service span pair: api-gateway → user-service.
	insertCrossServiceSpan(t, db,
		"trace000000000000000000000000001",
		"parentspan000001",
		"childspan0000001",
		"api-gateway", "user-service",
		0,
	)

	since := time.Now().Add(-time.Hour)
	err := db.MaterializeConnections(context.Background(), since)
	require.NoError(t, err)

	// Verify one edge was written.
	var count int
	require.NoError(t, db.db.QueryRow("SELECT COUNT(*) FROM service_connections").Scan(&count))
	assert.Equal(t, 1, count)

	// Verify edge details.
	var from, to, protocol string
	var connCount int
	require.NoError(t, db.db.QueryRow(`
		SELECT from_service, to_service, protocol, connection_count
		FROM service_connections
	`).Scan(&from, &to, &protocol, &connCount))

	assert.Equal(t, "api-gateway", from)
	assert.Equal(t, "user-service", to)
	assert.Equal(t, "http", protocol)
	assert.Equal(t, 1, connCount)
}

func TestMaterializeConnections_SameServiceSpansExcluded(t *testing.T) {
	db, cleanup := newTestDatabase(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	// Insert two spans within the same service — should NOT create a connection edge.
	spans := []*agentv1.EbpfTraceSpan{
		{
			TraceId:     "trace000000000000000000000000002",
			SpanId:      "parentspan000002",
			ServiceName: "api-gateway",
			SpanName:    "GET /health",
			SpanKind:    "server",
			StartTime:   now.UnixMilli(),
			DurationUs:  100,
			StatusCode:  200,
		},
		{
			TraceId:      "trace000000000000000000000000002",
			SpanId:       "childspan0000002",
			ParentSpanId: "parentspan000002",
			ServiceName:  "api-gateway", // Same service — must be excluded.
			SpanName:     "internal call",
			SpanKind:     "internal",
			StartTime:    now.UnixMilli(),
			DurationUs:   50,
			StatusCode:   200,
		},
	}
	require.NoError(t, db.InsertBeylaTraces(ctx, "agent-1", spans))

	since := time.Now().Add(-time.Hour)
	require.NoError(t, db.MaterializeConnections(ctx, since))

	var count int
	require.NoError(t, db.db.QueryRow("SELECT COUNT(*) FROM service_connections").Scan(&count))
	assert.Equal(t, 0, count, "Intra-service spans must not create connection edges")
}

func TestMaterializeConnections_AggregatesMultipleCalls(t *testing.T) {
	db, cleanup := newTestDatabase(t)
	defer cleanup()

	// Insert three separate traces between the same pair of services.
	pairs := [][2]string{
		{"trace000000000000000000000000003", "trace000000000000000000000000003"},
		{"trace000000000000000000000000004", "trace000000000000000000000000004"},
		{"trace000000000000000000000000005", "trace000000000000000000000000005"},
	}
	for i, p := range pairs {
		insertCrossServiceSpan(t, db,
			p[0],
			"parent0000000"+string(rune('1'+i)),
			"child00000000"+string(rune('1'+i)),
			"frontend", "backend",
			time.Duration(i)*time.Minute,
		)
	}

	since := time.Now().Add(-time.Hour)
	require.NoError(t, db.MaterializeConnections(context.Background(), since))

	var connCount int
	require.NoError(t, db.db.QueryRow(
		"SELECT connection_count FROM service_connections WHERE from_service = 'frontend'",
	).Scan(&connCount))
	assert.Equal(t, 3, connCount, "All three calls should be aggregated into one edge")
}

func TestMaterializeConnections_MultipleDistinctEdges(t *testing.T) {
	db, cleanup := newTestDatabase(t)
	defer cleanup()

	// api-gateway → user-service
	insertCrossServiceSpan(t, db,
		"trace000000000000000000000000010",
		"parentspan000010",
		"childspan0000010",
		"api-gateway", "user-service",
		0,
	)
	// user-service → postgres
	insertCrossServiceSpan(t, db,
		"trace000000000000000000000000011",
		"parentspan000011",
		"childspan0000011",
		"user-service", "postgres",
		10*time.Minute,
	)

	since := time.Now().Add(-time.Hour)
	require.NoError(t, db.MaterializeConnections(context.Background(), since))

	var count int
	require.NoError(t, db.db.QueryRow("SELECT COUNT(*) FROM service_connections").Scan(&count))
	assert.Equal(t, 2, count, "Two distinct edges should be stored")
}

func TestMaterializeConnections_SinceFiltersOldData(t *testing.T) {
	db, cleanup := newTestDatabase(t)
	defer cleanup()

	ctx := context.Background()

	// Insert a cross-service span with an old timestamp (older than our window).
	oldTime := time.Now().Add(-2 * time.Hour)
	parent := &agentv1.EbpfTraceSpan{
		TraceId:     "trace000000000000000000000000020",
		SpanId:      "parentspan000020",
		ServiceName: "old-service",
		SpanName:    "GET /old",
		SpanKind:    "server",
		StartTime:   oldTime.UnixMilli(),
		DurationUs:  100,
		StatusCode:  200,
	}
	child := &agentv1.EbpfTraceSpan{
		TraceId:      "trace000000000000000000000000020",
		SpanId:       "childspan0000020",
		ParentSpanId: "parentspan000020",
		ServiceName:  "old-target",
		SpanName:     "POST /old",
		SpanKind:     "client",
		StartTime:    oldTime.UnixMilli(),
		DurationUs:   50,
		StatusCode:   200,
	}
	require.NoError(t, db.InsertBeylaTraces(ctx, "agent-old", []*agentv1.EbpfTraceSpan{parent, child}))

	// Materialize with a 1-hour window — old spans should be excluded.
	since := time.Now().Add(-time.Hour)
	require.NoError(t, db.MaterializeConnections(ctx, since))

	var count int
	require.NoError(t, db.db.QueryRow("SELECT COUNT(*) FROM service_connections").Scan(&count))
	assert.Equal(t, 0, count, "Spans outside the time window must not produce edges")
}

func TestGetServiceConnections_ReturnsConnections(t *testing.T) {
	db, cleanup := newTestDatabase(t)
	defer cleanup()

	insertCrossServiceSpan(t, db,
		"trace000000000000000000000000030",
		"parentspan000030",
		"childspan0000030",
		"svc-a", "svc-b",
		0,
	)

	since := time.Now().Add(-time.Hour)
	conns, err := db.GetServiceConnections(context.Background(), since)
	require.NoError(t, err)

	require.Len(t, conns, 1)
	assert.Equal(t, "svc-a", conns[0].FromService)
	assert.Equal(t, "svc-b", conns[0].ToService)
	assert.Equal(t, "http", conns[0].Protocol)
	assert.Equal(t, 1, conns[0].ConnectionCount)
	assert.False(t, conns[0].FirstObserved.IsZero())
	assert.False(t, conns[0].LastObserved.IsZero())
}

func TestGetServiceConnections_EmptyResult(t *testing.T) {
	db, cleanup := newTestDatabase(t)
	defer cleanup()

	since := time.Now().Add(-time.Hour)
	conns, err := db.GetServiceConnections(context.Background(), since)
	require.NoError(t, err)
	assert.Empty(t, conns)
}

func TestGetServiceConnections_CacheHitSkipsMaterialization(t *testing.T) {
	db, cleanup := newTestDatabase(t)
	defer cleanup()

	ctx := context.Background()
	since := time.Now().Add(-time.Hour)

	// First call: populates cache and materializes.
	insertCrossServiceSpan(t, db,
		"trace000000000000000000000000040",
		"parentspan000040",
		"childspan0000040",
		"cached-a", "cached-b",
		0,
	)
	conns1, err := db.GetServiceConnections(ctx, since)
	require.NoError(t, err)
	require.Len(t, conns1, 1)

	// Insert a new cross-service edge AFTER the first call but within the TTL window.
	// The second call should still return the cached (pre-insert) result.
	insertCrossServiceSpan(t, db,
		"trace000000000000000000000000041",
		"parentspan000041",
		"childspan0000041",
		"new-a", "new-b",
		0,
	)

	// Second call: should hit cache and return only the first result.
	conns2, err := db.GetServiceConnections(ctx, since)
	require.NoError(t, err)
	// Cache is fresh — new edge should not appear yet.
	assert.Equal(t, len(conns1), len(conns2), "Cache hit should return same result count as first call")
}

func TestGetServiceConnections_StaleCache(t *testing.T) {
	db, cleanup := newTestDatabase(t)
	defer cleanup()

	ctx := context.Background()
	since := time.Now().Add(-time.Hour)

	// Seed a connection and warm the cache.
	insertCrossServiceSpan(t, db,
		"trace000000000000000000000000050",
		"parentspan000050",
		"childspan0000050",
		"stale-a", "stale-b",
		0,
	)
	_, err := db.GetServiceConnections(ctx, since)
	require.NoError(t, err)

	// Manually expire the cache timestamp.
	db.connectionsMu.Lock()
	db.connectionsLastMaterialized = time.Time{}
	db.connectionsMu.Unlock()

	// Insert another edge — now that cache is expired, it should be picked up.
	insertCrossServiceSpan(t, db,
		"trace000000000000000000000000051",
		"parentspan000051",
		"childspan0000051",
		"stale-c", "stale-d",
		0,
	)

	conns, err := db.GetServiceConnections(ctx, since)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(conns), 2, "Expired cache should re-materialize and include new edge")
}
