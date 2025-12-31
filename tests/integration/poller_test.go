// Package integration provides integration tests for Coral components.
package integration

import (
	"context"
	"testing"
	"time"

	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/poller"
	"github.com/coral-mesh/coral/internal/testutil"
)

// mockPoller implements the Poller interface for testing.
type mockPoller struct {
	pollCount    int
	cleanupCount int
	shouldFail   bool
}

func (m *mockPoller) PollOnce(ctx context.Context) error {
	m.pollCount++
	if m.shouldFail {
		return context.DeadlineExceeded
	}
	return nil
}

func (m *mockPoller) RunCleanup(ctx context.Context) error {
	m.cleanupCount++
	return nil
}

// TestBasePollerLifecycle tests the base poller lifecycle and polling loop.
func TestBasePollerLifecycle(t *testing.T) {
	ctx, cancel := testutil.NewTestContext()
	defer cancel()

	logger := testutil.NewTestLogger(t)

	// Create a mock poller.
	mock := &mockPoller{}

	// Create base poller with short intervals.
	base := poller.NewBasePoller(ctx, poller.Config{
		Name:            "test-poller",
		PollInterval:    100 * time.Millisecond,
		CleanupInterval: 200 * time.Millisecond,
		Logger:          logger,
	})

	// Start the poller.
	if err := base.Start(mock); err != nil {
		t.Fatalf("Failed to start poller: %v", err)
	}

	// Verify it's running.
	if !base.IsRunning() {
		t.Error("Poller should be running after Start()")
	}

	// Wait for a few polling cycles.
	time.Sleep(350 * time.Millisecond)

	// Stop the poller.
	if err := base.Stop(); err != nil {
		t.Fatalf("Failed to stop poller: %v", err)
	}

	// Verify it's stopped.
	if base.IsRunning() {
		t.Error("Poller should not be running after Stop()")
	}

	// Verify that polling occurred multiple times.
	if mock.pollCount < 3 {
		t.Errorf("Expected at least 3 poll calls, got %d", mock.pollCount)
	}

	// Verify that cleanup occurred at least once.
	if mock.cleanupCount < 1 {
		t.Errorf("Expected at least 1 cleanup call, got %d", mock.cleanupCount)
	}

	t.Logf("Poller lifecycle test completed: %d polls, %d cleanups", mock.pollCount, mock.cleanupCount)
}

// TestBasePollerContextCancellation tests that poller stops cleanly when context is cancelled.
func TestBasePollerContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	logger := testutil.NewTestLogger(t)
	mock := &mockPoller{}

	// Create base poller with long interval.
	base := poller.NewBasePoller(ctx, poller.Config{
		Name:            "test-poller",
		PollInterval:    1 * time.Second,
		CleanupInterval: 1 * time.Hour,
		Logger:          logger,
	})

	// Start poller.
	if err := base.Start(mock); err != nil {
		t.Fatalf("Failed to start poller: %v", err)
	}

	// Cancel the context.
	cancel()

	// Wait a bit for the poller to react to cancellation.
	time.Sleep(100 * time.Millisecond)

	// Stop should succeed even though context is cancelled.
	if err := base.Stop(); err != nil {
		t.Errorf("Stop() failed: %v", err)
	}

	if base.IsRunning() {
		t.Error("Poller should not be running after context cancellation and Stop()")
	}
}

// TestDatabaseIntegration tests database operations with system metrics.
func TestDatabaseIntegration(t *testing.T) {
	db := testutil.NewTestDatabase(t)
	ctx, cancel := testutil.NewTestContext()
	defer cancel()

	// Insert some test data.
	summaries := []database.SystemMetricsSummary{
		{
			BucketTime:  time.Now().Truncate(time.Minute),
			AgentID:     "test-agent-1",
			MetricName:  "cpu.usage",
			MinValue:    10.0,
			MaxValue:    90.0,
			AvgValue:    50.0,
			P95Value:    85.0,
			DeltaValue:  0.0,
			SampleCount: 60,
			Unit:        "percent",
			MetricType:  "gauge",
			Attributes:  "{}",
		},
		{
			BucketTime:  time.Now().Truncate(time.Minute),
			AgentID:     "test-agent-1",
			MetricName:  "memory.used",
			MinValue:    100000000,
			MaxValue:    200000000,
			AvgValue:    150000000,
			P95Value:    190000000,
			DeltaValue:  0.0,
			SampleCount: 60,
			Unit:        "bytes",
			MetricType:  "gauge",
			Attributes:  "{}",
		},
	}

	// Insert the summaries.
	if err := db.InsertSystemMetricsSummaries(ctx, summaries); err != nil {
		t.Fatalf("Failed to insert system metrics summaries: %v", err)
	}

	// Query back the data.
	startTime := time.Now().Add(-1 * time.Hour)
	endTime := time.Now().Add(1 * time.Hour)

	results, err := db.QuerySystemMetricsSummaries(ctx, "test-agent-1", startTime, endTime)
	if err != nil {
		t.Fatalf("Failed to query system metrics summaries: %v", err)
	}

	// Verify we got our data back.
	if len(results) != 2 {
		t.Errorf("Expected 2 results, got %d", len(results))
	}

	t.Logf("Database integration test completed: inserted %d summaries, queried %d results", len(summaries), len(results))
}
