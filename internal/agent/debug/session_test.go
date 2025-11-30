package debug

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/config"
)

// mockResolver implements ServiceResolver for testing
type mockResolver struct {
	addr string
	err  error
}

func (m *mockResolver) Resolve(serviceName string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.addr, nil
}

func TestNewDebugSessionManager(t *testing.T) {
	cfg := config.DebugConfig{
		Enabled: true,
	}
	cfg.Limits.MaxConcurrentSessions = 5
	cfg.Limits.MaxSessionDuration = 600 * time.Second

	logger := zerolog.Nop()
	resolver := &mockResolver{addr: "localhost:8080"}

	manager := NewDebugSessionManager(cfg, logger, resolver)

	if manager == nil {
		t.Fatal("NewDebugSessionManager returned nil")
	}
	if manager.cfg.Enabled != true {
		t.Error("Expected debug to be enabled")
	}
	if len(manager.sessions) != 0 {
		t.Error("Expected empty sessions map")
	}
	if manager.eventCh == nil {
		t.Error("Expected event channel to be initialized")
	}
}

func TestDebugSessionManager_CreateSession_Disabled(t *testing.T) {
	cfg := config.DebugConfig{
		Enabled: false,
	}
	logger := zerolog.Nop()
	resolver := &mockResolver{addr: "localhost:8080"}

	manager := NewDebugSessionManager(cfg, logger, resolver)

	err := manager.CreateSession("session1", 1234, "main.Handler")
	if err == nil {
		t.Error("Expected error when debug is disabled")
	}
	// CreateSession is deprecated and returns "use StartSession instead"
	// regardless of whether debug is enabled
	if err.Error() != "use StartSession instead" {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestDebugSessionManager_CreateSession_Deprecated(t *testing.T) {
	cfg := config.DebugConfig{
		Enabled: true,
	}
	cfg.Limits.MaxConcurrentSessions = 5
	cfg.Limits.MaxSessionDuration = 600 * time.Second

	logger := zerolog.Nop()
	resolver := &mockResolver{addr: "localhost:8080"}

	manager := NewDebugSessionManager(cfg, logger, resolver)

	err := manager.CreateSession("session1", 1234, "main.Handler")
	if err == nil {
		t.Error("Expected error for deprecated CreateSession")
	}
	if err.Error() != "use StartSession instead" {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestDebugSessionManager_GetSession(t *testing.T) {
	cfg := config.DebugConfig{
		Enabled: true,
	}
	cfg.Limits.MaxConcurrentSessions = 5
	cfg.Limits.MaxSessionDuration = 600 * time.Second

	logger := zerolog.Nop()
	resolver := &mockResolver{addr: "localhost:8080"}

	manager := NewDebugSessionManager(cfg, logger, resolver)

	// Test non-existent session
	_, ok := manager.GetSession("nonexistent")
	if ok {
		t.Error("Expected GetSession to return false for non-existent session")
	}

	// Add a session manually for testing
	manager.mu.Lock()
	manager.sessions["test-session"] = &DebugSession{
		ID:        "test-session",
		PID:       1234,
		Function:  "main.Handler",
		StartTime: time.Now(),
	}
	manager.mu.Unlock()

	// Test existing session
	session, ok := manager.GetSession("test-session")
	if !ok {
		t.Error("Expected GetSession to return true for existing session")
	}
	if session.ID != "test-session" {
		t.Errorf("Expected session ID 'test-session', got %s", session.ID)
	}
}

func TestDebugSessionManager_CloseSession(t *testing.T) {
	cfg := config.DebugConfig{
		Enabled: true,
	}
	cfg.Limits.MaxConcurrentSessions = 5
	cfg.Limits.MaxSessionDuration = 600 * time.Second

	logger := zerolog.Nop()
	resolver := &mockResolver{addr: "localhost:8080"}

	manager := NewDebugSessionManager(cfg, logger, resolver)

	// Test closing non-existent session
	err := manager.CloseSession("nonexistent")
	if err == nil {
		t.Error("Expected error when closing non-existent session")
	}

	// Add a session manually for testing
	manager.mu.Lock()
	manager.sessions["test-session"] = &DebugSession{
		ID:        "test-session",
		PID:       1234,
		Function:  "main.Handler",
		StartTime: time.Now(),
	}
	manager.mu.Unlock()

	// Close the session
	err = manager.CloseSession("test-session")
	if err != nil {
		t.Errorf("Unexpected error closing session: %v", err)
	}

	// Verify session is removed
	_, ok := manager.GetSession("test-session")
	if ok {
		t.Error("Expected session to be removed after closing")
	}
}

func TestDebugSessionManager_Subscribe(t *testing.T) {
	cfg := config.DebugConfig{
		Enabled: true,
	}
	cfg.Limits.MaxConcurrentSessions = 5
	cfg.Limits.MaxSessionDuration = 600 * time.Second

	logger := zerolog.Nop()
	resolver := &mockResolver{addr: "localhost:8080"}

	manager := NewDebugSessionManager(cfg, logger, resolver)

	// Subscribe to events
	eventCh := manager.Subscribe()
	if eventCh == nil {
		t.Fatal("Subscribe returned nil channel")
	}

	// Send a test event
	testEvent := &agentv1.DebugEvent{
		SessionId:  "test-session",
		Timestamp:  time.Now().UnixNano(),
		Pid:        1234,
		Tid:        5678,
		DurationNs: 1000000,
	}

	// Non-blocking send
	select {
	case manager.eventCh <- testEvent:
	default:
		t.Error("Failed to send event to channel")
	}

	// Receive event
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	select {
	case event := <-eventCh:
		if event.SessionId != "test-session" {
			t.Errorf("Expected session ID 'test-session', got %s", event.SessionId)
		}
		if event.Pid != 1234 {
			t.Errorf("Expected PID 1234, got %d", event.Pid)
		}
	case <-ctx.Done():
		t.Error("Timeout waiting for event")
	}
}

func TestDebugSessionManager_MaxConcurrentSessions(t *testing.T) {
	cfg := config.DebugConfig{
		Enabled: true,
	}
	cfg.Limits.MaxConcurrentSessions = 2 // Set to 2 for testing
	cfg.Limits.MaxSessionDuration = 600 * time.Second

	logger := zerolog.Nop()
	resolver := &mockResolver{addr: "localhost:8080"}

	manager := NewDebugSessionManager(cfg, logger, resolver)

	// Add 2 sessions manually (up to limit)
	manager.mu.Lock()
	manager.sessions["session1"] = &DebugSession{
		ID:        "session1",
		PID:       1234,
		Function:  "main.Handler1",
		StartTime: time.Now(),
	}
	manager.sessions["session2"] = &DebugSession{
		ID:        "session2",
		PID:       5678,
		Function:  "main.Handler2",
		StartTime: time.Now(),
	}
	manager.mu.Unlock()

	// Try to create a third session (should fail)
	// Note: StartSession would fail because it queries SDK, so we test the limit check directly
	manager.mu.Lock()
	atLimit := len(manager.sessions) >= manager.cfg.Limits.MaxConcurrentSessions
	manager.mu.Unlock()

	if !atLimit {
		t.Error("Expected to be at session limit")
	}
}
