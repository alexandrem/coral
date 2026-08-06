package database

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// insertBeylaTraceSQL inserts a beyla_traces row using raw SQL for test setup.
func insertBeylaTraceSQL(t *testing.T, db *Database, traceID, spanID, agentID, serviceName, spanName string, startTime time.Time, durationUs int64, processPID int32) {
	t.Helper()
	_, err := db.db.Exec(`
		INSERT INTO beyla_traces (trace_id, span_id, agent_id, service_name, span_name, span_kind, start_time, duration_us, status_code, process_pid)
		VALUES (?, ?, ?, ?, ?, 'SERVER', ?, ?, 0, ?)
	`, traceID, spanID, agentID, serviceName, spanName, startTime, durationUs, processPID)
	require.NoError(t, err)
}

// insertProfileSummarySQLWithTGID inserts a CPU profile summary with an explicit tgid,
// used to simulate OS PID reuse across services/agents.
func insertProfileSummarySQLWithTGID(t *testing.T, db *Database, ts time.Time, agentID, serviceName, buildID string, frameIDs []int64, sampleCount uint32, tgid int32) {
	t.Helper()
	stackHash := ComputeStackHash(frameIDs)
	arrayLiteral := "["
	for i, id := range frameIDs {
		if i > 0 {
			arrayLiteral += ", "
		}
		arrayLiteral += fmt.Sprintf("%d", id)
	}
	arrayLiteral += "]"

	_, err := db.db.Exec(fmt.Sprintf(`
		INSERT INTO cpu_profile_summaries (timestamp, agent_id, service_name, build_id, tgid, stack_hash, stack_frame_ids, sample_count)
		VALUES (?, ?, ?, ?, ?, ?, %s::BIGINT[], ?)
	`, arrayLiteral), ts, agentID, serviceName, buildID, tgid, stackHash, sampleCount)
	require.NoError(t, err)
}

func TestQueryTraceProfileCPU_NoMatch(t *testing.T) {
	db := setupTestDBForProfiling(t)
	ctx := context.Background()

	results, metadata, err := db.QueryTraceProfileCPU(ctx, "nonexistent-trace", "")
	require.NoError(t, err)
	assert.Nil(t, metadata)
	assert.Empty(t, results)
}

func TestQueryTraceProfileCPU_JoinsMatchingProfile(t *testing.T) {
	db := setupTestDBForProfiling(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Minute)

	insertBeylaTraceSQL(t, db, "trace-1", "span-1", "agent-1", "checkout-svc", "POST /checkout", now, 100000, 1234)

	frames, err := db.EncodeStackFrames(ctx, []string{"main", "processOrder"})
	require.NoError(t, err)
	insertProfileSummarySQLWithTGID(t, db, now, "agent-1", "checkout-svc", "build-1", frames, 50, 1234)

	results, metadata, err := db.QueryTraceProfileCPU(ctx, "trace-1", "")
	require.NoError(t, err)
	require.NotNil(t, metadata)
	require.Len(t, results, 1)

	assert.Equal(t, "checkout-svc", results[0].ServiceName)
	assert.Equal(t, uint32(1234), results[0].TGID)
	assert.Equal(t, int64(50), results[0].TotalSamples)
}

// TestQueryTraceProfileCPU_DoesNotConflateAcrossServices guards against joining profile
// samples from a different service that happens to reuse the same OS PID (tgid), which
// can occur when independent hosts/containers assign overlapping PIDs.
func TestQueryTraceProfileCPU_DoesNotConflateAcrossServices(t *testing.T) {
	db := setupTestDBForProfiling(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Minute)

	// Trace belongs to "checkout-svc" with pid 1234.
	insertBeylaTraceSQL(t, db, "trace-1", "span-1", "agent-1", "checkout-svc", "POST /checkout", now, 100000, 1234)

	// A different service, "billing-svc" on a different agent, happens to reuse pid 1234
	// within the same time window.
	framesOther, err := db.EncodeStackFrames(ctx, []string{"main", "billingHandler"})
	require.NoError(t, err)
	insertProfileSummarySQLWithTGID(t, db, now, "agent-2", "billing-svc", "build-2", framesOther, 999, 1234)

	results, metadata, err := db.QueryTraceProfileCPU(ctx, "trace-1", "")
	require.NoError(t, err)
	require.NotNil(t, metadata)

	// The billing-svc profile must not be joined into checkout-svc's trace.
	assert.Empty(t, results, "profile samples from an unrelated service sharing the same PID must not be joined")
}
