package poller

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/internal/colony/registry"
	"github.com/coral-mesh/coral/internal/constants"
)

func registerPollingAgent(t *testing.T, reg *registry.Registry, id string, lastSeen time.Time) {
	t.Helper()
	entry, err := reg.Register(id, "", "", "", nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	entry.LastSeen = lastSeen
}

func TestForEachHealthyAgent_BoundedConcurrency(t *testing.T) {
	reg := registry.New(nil)
	const agentCount = 25
	for i := range agentCount {
		registerPollingAgent(t, reg, fmt.Sprint(i), time.Now())
	}

	started := make(chan struct{}, agentCount)
	release := make(chan struct{})
	releaseAll := sync.OnceFunc(func() { close(release) })
	defer releaseAll()
	var mu sync.Mutex
	active, peak := 0, 0
	visited := make(map[string]int)
	done := make(chan struct{})
	var success, failures int
	go func() {
		success, failures = ForEachHealthyAgent(context.Background(), reg, zerolog.Nop(), func(agent *registry.Entry) error {
			mu.Lock()
			active++
			peak = max(peak, active)
			visited[agent.AgentID]++
			mu.Unlock()
			started <- struct{}{}
			<-release
			mu.Lock()
			active--
			mu.Unlock()
			return nil
		})
		close(done)
	}()

	// All workers must start even though earlier agents have not finished.
	for range maxConcurrentAgentPolls {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("agents were not polled concurrently")
		}
	}
	releaseAll()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("polling did not finish")
	}
	if success != agentCount || failures != 0 {
		t.Fatalf("got (%d, %d), want (%d, 0)", success, failures, agentCount)
	}
	if peak != maxConcurrentAgentPolls {
		t.Errorf("peak concurrency = %d, want %d", peak, maxConcurrentAgentPolls)
	}
	for id, count := range visited {
		if count != 1 {
			t.Errorf("agent %s visited %d times", id, count)
		}
	}
	if len(visited) != agentCount {
		t.Errorf("visited %d agents, want %d", len(visited), agentCount)
	}
}

func TestForEachHealthyAgent_HealthAndFailures(t *testing.T) {
	reg := registry.New(nil)
	registerPollingAgent(t, reg, "healthy", time.Now())
	registerPollingAgent(t, reg, "degraded", time.Now().Add(-constants.DefaultAgentHealthyThreshold-time.Second))
	registerPollingAgent(t, reg, "failed", time.Now())
	registerPollingAgent(t, reg, "panicked", time.Now())
	registerPollingAgent(t, reg, "unhealthy", time.Now().Add(-constants.DefaultAgentDegradedThreshold-time.Second))
	success, failures := ForEachHealthyAgent(context.Background(), reg, zerolog.Nop(), func(agent *registry.Entry) error {
		switch agent.AgentID {
		case "failed":
			return fmt.Errorf("agent unavailable")
		case "panicked":
			panic("bad response")
		case "unhealthy":
			t.Error("unhealthy agent was polled")
		}
		return nil
	})
	if success != 2 || failures != 2 {
		t.Errorf("got (%d, %d), want (2, 2)", success, failures)
	}
}

func TestForEachHealthyAgent_Cancellation(t *testing.T) {
	reg := registry.New(nil)
	for i := range 25 {
		registerPollingAgent(t, reg, fmt.Sprint(i), time.Now())
	}
	t.Run("before polling", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		success, failures := ForEachHealthyAgent(ctx, reg, zerolog.Nop(), func(*registry.Entry) error {
			t.Error("visitor called after cancellation")
			return nil
		})
		if success != 0 || failures != 0 {
			t.Errorf("got (%d, %d), want (0, 0)", success, failures)
		}
	})
	t.Run("during polling", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		started := make(chan struct{}, 25)
		done := make(chan struct{})
		var success, failures int
		go func() {
			success, failures = ForEachHealthyAgent(ctx, reg, zerolog.Nop(), func(*registry.Entry) error {
				started <- struct{}{}
				<-ctx.Done()
				return ctx.Err()
			})
			close(done)
		}()
		for range maxConcurrentAgentPolls {
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("workers did not start")
			}
		}
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("polling did not stop after cancellation")
		}
		if success != 0 || failures != maxConcurrentAgentPolls || len(started) != 0 {
			t.Errorf("got (%d, %d), %d extra visits; want (0, %d), no extra visits",
				success, failures, len(started), maxConcurrentAgentPolls)
		}
	})
}

// mockPoller is a test implementation of the Poller interface.
type mockPoller struct {
	pollCount    int
	cleanupCount int
	mu           sync.Mutex
	pollErr      error
	cleanupErr   error
	pollChan     chan struct{} // Optional channel to signal polls
}

func (m *mockPoller) PollOnce(ctx context.Context) error {
	m.mu.Lock()
	m.pollCount++
	m.mu.Unlock()

	// Signal on channel if provided
	if m.pollChan != nil {
		select {
		case m.pollChan <- struct{}{}:
		default:
		}
	}

	return m.pollErr
}

func (m *mockPoller) RunCleanup(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupCount++
	return m.cleanupErr
}

func (m *mockPoller) getPollCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pollCount
}

func (m *mockPoller) getCleanupCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cleanupCount
}

func TestBasePoller_StartStop(t *testing.T) {
	mock := &mockPoller{}
	logger := zerolog.Nop()

	config := Config{
		Name:         "test_poller",
		PollInterval: 10 * time.Millisecond,
		Logger:       logger,
	}

	base := NewBasePoller(context.Background(), config)

	// Should not be running initially.
	if base.IsRunning() {
		t.Error("Poller should not be running initially")
	}

	// Start the poller.
	if err := base.Start(mock); err != nil {
		t.Fatalf("Failed to start poller: %v", err)
	}

	if !base.IsRunning() {
		t.Error("Poller should be running after Start")
	}

	// Starting again should be idempotent.
	if err := base.Start(mock); err != nil {
		t.Fatalf("Second Start should not fail: %v", err)
	}

	// Wait for a few polls.
	time.Sleep(50 * time.Millisecond)

	// Stop the poller.
	if err := base.Stop(); err != nil {
		t.Fatalf("Failed to stop poller: %v", err)
	}

	if base.IsRunning() {
		t.Error("Poller should not be running after Stop")
	}

	// Stopping again should be idempotent.
	if err := base.Stop(); err != nil {
		t.Fatalf("Second Stop should not fail: %v", err)
	}

	// Should have polled at least once (initial poll).
	if mock.getPollCount() < 1 {
		t.Errorf("Expected at least 1 poll, got %d", mock.getPollCount())
	}
}

func TestBasePoller_PollingInterval(t *testing.T) {
	mock := &mockPoller{}
	logger := zerolog.Nop()

	config := Config{
		Name:         "test_poller",
		PollInterval: 20 * time.Millisecond,
		Logger:       logger,
	}

	base := NewBasePoller(context.Background(), config)

	if err := base.Start(mock); err != nil {
		t.Fatalf("Failed to start poller: %v", err)
	}
	defer base.Stop()

	// Wait for multiple poll intervals.
	time.Sleep(100 * time.Millisecond)

	pollCount := mock.getPollCount()

	// Should have polled multiple times.
	// Initial poll + ~5 interval polls (100ms / 20ms).
	if pollCount < 4 {
		t.Errorf("Expected at least 4 polls, got %d", pollCount)
	}
}

func TestBasePoller_CleanupInterval(t *testing.T) {
	mock := &mockPoller{}
	logger := zerolog.Nop()

	config := Config{
		Name:            "test_poller",
		PollInterval:    10 * time.Millisecond,
		CleanupInterval: 30 * time.Millisecond,
		Logger:          logger,
	}

	base := NewBasePoller(context.Background(), config)

	if err := base.Start(mock); err != nil {
		t.Fatalf("Failed to start poller: %v", err)
	}
	defer base.Stop()

	// Wait for cleanup to run.
	time.Sleep(95 * time.Millisecond)

	cleanupCount := mock.getCleanupCount()

	// Should have cleaned up at least twice (95ms / 30ms accounting for startup overhead).
	if cleanupCount < 2 {
		t.Errorf("Expected at least 2 cleanups, got %d", cleanupCount)
	}
}

func TestBasePoller_DefaultCleanupInterval(t *testing.T) {
	logger := zerolog.Nop()

	config := Config{
		Name:         "test_poller",
		PollInterval: 10 * time.Millisecond,
		// CleanupInterval not set, should default to 1 hour.
		Logger: logger,
	}

	base := NewBasePoller(context.Background(), config)

	if base.cleanupInterval != 1*time.Hour {
		t.Errorf("Expected default cleanup interval of 1 hour, got %v", base.cleanupInterval)
	}
}

func TestDetectGaps_NoGaps(t *testing.T) {
	// Continuous seq IDs from checkpoint 0.
	seqIDs := []uint64{1, 2, 3, 4, 5}
	oldTimestamp := time.Now().Add(-30 * time.Second).UnixMilli()
	timestamps := []int64{oldTimestamp, oldTimestamp, oldTimestamp, oldTimestamp, oldTimestamp}

	gaps := DetectGaps(0, seqIDs, timestamps)
	if len(gaps) != 0 {
		t.Errorf("Expected no gaps, got %d: %+v", len(gaps), gaps)
	}
}

func TestDetectGaps_GapInMiddle(t *testing.T) {
	// Missing seq_id 4.
	seqIDs := []uint64{1, 2, 3, 5, 6}
	oldTimestamp := time.Now().Add(-30 * time.Second).UnixMilli()
	timestamps := []int64{oldTimestamp, oldTimestamp, oldTimestamp, oldTimestamp, oldTimestamp}

	gaps := DetectGaps(0, seqIDs, timestamps)
	if len(gaps) != 1 {
		t.Fatalf("Expected 1 gap, got %d: %+v", len(gaps), gaps)
	}
	if gaps[0].StartSeqID != 4 || gaps[0].EndSeqID != 4 {
		t.Errorf("Expected gap [4,4], got [%d,%d]", gaps[0].StartSeqID, gaps[0].EndSeqID)
	}
}

func TestDetectGaps_GapFromCheckpoint(t *testing.T) {
	// Checkpoint at 5, next batch starts at 8 (missing 6, 7).
	seqIDs := []uint64{8, 9, 10}
	oldTimestamp := time.Now().Add(-30 * time.Second).UnixMilli()
	timestamps := []int64{oldTimestamp, oldTimestamp, oldTimestamp}

	gaps := DetectGaps(5, seqIDs, timestamps)
	if len(gaps) != 1 {
		t.Fatalf("Expected 1 gap, got %d: %+v", len(gaps), gaps)
	}
	if gaps[0].StartSeqID != 6 || gaps[0].EndSeqID != 7 {
		t.Errorf("Expected gap [6,7], got [%d,%d]", gaps[0].StartSeqID, gaps[0].EndSeqID)
	}
}

func TestDetectGaps_MultipleGaps(t *testing.T) {
	// Missing 2, 5-6.
	seqIDs := []uint64{1, 3, 4, 7, 8}
	oldTimestamp := time.Now().Add(-30 * time.Second).UnixMilli()
	timestamps := []int64{oldTimestamp, oldTimestamp, oldTimestamp, oldTimestamp, oldTimestamp}

	gaps := DetectGaps(0, seqIDs, timestamps)
	if len(gaps) != 2 {
		t.Fatalf("Expected 2 gaps, got %d: %+v", len(gaps), gaps)
	}
	if gaps[0].StartSeqID != 2 || gaps[0].EndSeqID != 2 {
		t.Errorf("Expected first gap [2,2], got [%d,%d]", gaps[0].StartSeqID, gaps[0].EndSeqID)
	}
	if gaps[1].StartSeqID != 5 || gaps[1].EndSeqID != 6 {
		t.Errorf("Expected second gap [5,6], got [%d,%d]", gaps[1].StartSeqID, gaps[1].EndSeqID)
	}
}

func TestDetectGaps_GracePeriod(t *testing.T) {
	// Records are very recent (< 10s), so gaps should NOT be reported.
	seqIDs := []uint64{1, 2, 3, 5, 6}
	recentTimestamp := time.Now().UnixMilli() // Now, within grace period.
	timestamps := []int64{recentTimestamp, recentTimestamp, recentTimestamp, recentTimestamp, recentTimestamp}

	gaps := DetectGaps(0, seqIDs, timestamps)
	if len(gaps) != 0 {
		t.Errorf("Expected no gaps within grace period, got %d: %+v", len(gaps), gaps)
	}
}

func TestDetectGaps_EmptyInput(t *testing.T) {
	gaps := DetectGaps(0, nil, nil)
	if len(gaps) != 0 {
		t.Errorf("Expected no gaps for empty input, got %d", len(gaps))
	}
}

func TestDetectGaps_MismatchedLengths(t *testing.T) {
	gaps := DetectGaps(0, []uint64{1, 2}, []int64{100})
	if len(gaps) != 0 {
		t.Errorf("Expected no gaps for mismatched lengths, got %d", len(gaps))
	}
}

func TestDetectGaps_FirstPoll(t *testing.T) {
	// First poll from checkpoint 0, data starts at 1. No gap.
	seqIDs := []uint64{1, 2, 3}
	oldTimestamp := time.Now().Add(-30 * time.Second).UnixMilli()
	timestamps := []int64{oldTimestamp, oldTimestamp, oldTimestamp}

	gaps := DetectGaps(0, seqIDs, timestamps)
	if len(gaps) != 0 {
		t.Errorf("Expected no gaps on first poll, got %d: %+v", len(gaps), gaps)
	}
}

func TestBasePoller_ContextCancellation(t *testing.T) {
	// Create a mock that signals on a channel when polled
	pollChan := make(chan struct{}, 10)
	mock := &mockPoller{
		pollChan: pollChan,
	}

	logger := zerolog.Nop()
	ctx, cancel := context.WithCancel(context.Background())

	config := Config{
		Name:         "test_poller",
		PollInterval: 50 * time.Millisecond,
		Logger:       logger,
	}

	base := NewBasePoller(ctx, config)

	if err := base.Start(mock); err != nil {
		t.Fatalf("Failed to start poller: %v", err)
	}

	// Wait for at least one poll to confirm poller is running
	select {
	case <-pollChan:
		// Good, poller is running
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Poller did not start polling")
	}

	// Cancel the parent context
	cancel()

	// Drain any polls that were in-flight
	deadline := time.After(200 * time.Millisecond)
drainLoop:
	for {
		select {
		case <-pollChan:
			// Drain in-flight polls
		case <-deadline:
			break drainLoop
		}
	}

	// Now verify no new polls occur for at least 2 poll intervals
	select {
	case <-pollChan:
		t.Error("Poller should have stopped polling after context cancellation")
	case <-time.After(150 * time.Millisecond): // 3x poll interval
		// Good, no more polls
	}

	// Stop should still work gracefully
	if err := base.Stop(); err != nil {
		t.Fatalf("Stop should work after context cancellation: %v", err)
	}

	if base.IsRunning() {
		t.Error("Poller should not be running after Stop")
	}
}
