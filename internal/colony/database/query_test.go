package database

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueryTelemetrySummaries_WildcardAgent(t *testing.T) {
	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr})
	// Setup temporary database.
	tmpDir := t.TempDir()
	db, err := New(tmpDir, "test-colony", logger)
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()
	now := time.Now()

	// Insert test data for two different agents.
	summaries := []TelemetrySummary{
		{
			BucketTime:  now,
			AgentID:     "agent-1",
			ServiceName: "service-a",
			SpanKind:    "server",
			TotalSpans:  100,
		},
		{
			BucketTime:  now,
			AgentID:     "agent-2",
			ServiceName: "service-b",
			SpanKind:    "server",
			TotalSpans:  200,
		},
	}
	err = db.InsertTelemetrySummaries(ctx, summaries)
	require.NoError(t, err)

	// Test 1: Query with specific agent ID.
	results, err := db.QueryTelemetrySummaries(ctx, "agent-1", now.Add(-1*time.Hour), now.Add(1*time.Hour))
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "agent-1", results[0].AgentID)

	// Test 2: Query with empty agent ID (wildcard).
	results, err = db.QueryTelemetrySummaries(ctx, "", now.Add(-1*time.Hour), now.Add(1*time.Hour))
	require.NoError(t, err)
	assert.Len(t, results, 2) // Should return both agents.

	// Verify we got both agents.
	agents := make(map[string]bool)
	for _, r := range results {
		agents[r.AgentID] = true
	}
	assert.True(t, agents["agent-1"])
	assert.True(t, agents["agent-2"])
}

func TestQuerySystemMetricsSummaries_WildcardAgent(t *testing.T) {
	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr})
	// Setup temporary database.
	tmpDir := t.TempDir()
	db, err := New(tmpDir, "test-colony", logger)
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()
	now := time.Now()

	// Insert test data for two different agents.
	summaries := []SystemMetricsSummary{
		{
			BucketTime: now,
			AgentID:    "agent-1",
			MetricName: "cpu.usage",
			AvgValue:   50.0,
		},
		{
			BucketTime: now,
			AgentID:    "agent-2",
			MetricName: "cpu.usage",
			AvgValue:   60.0,
		},
	}
	err = db.InsertSystemMetricsSummaries(ctx, summaries)
	require.NoError(t, err)

	// Test 1: Query with specific agent ID.
	results, err := db.QuerySystemMetricsSummaries(ctx, "agent-1", now.Add(-1*time.Hour), now.Add(1*time.Hour))
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "agent-1", results[0].AgentID)

	// Test 2: Query with empty agent ID (wildcard).
	results, err = db.QuerySystemMetricsSummaries(ctx, "", now.Add(-1*time.Hour), now.Add(1*time.Hour))
	require.NoError(t, err)
	assert.Len(t, results, 2) // Should return both agents.

	// Verify we got both agents.
	agents := make(map[string]bool)
	for _, r := range results {
		agents[r.AgentID] = true
	}
	assert.True(t, agents["agent-1"])
	assert.True(t, agents["agent-2"])
}
