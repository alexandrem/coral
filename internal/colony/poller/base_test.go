package poller

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

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
	time.Sleep(80 * time.Millisecond)

	cleanupCount := mock.getCleanupCount()

	// Should have cleaned up at least twice (80ms / 30ms).
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
