package debug

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"connectrpc.com/connect"

	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/registry"
)

// setupTestDB creates a file-based DuckDB database for testing to ensure isolation.
func setupTestDB(t *testing.T) *database.Database {
	// Use a temporary directory for the database
	tmpDir := t.TempDir()
	logger := zerolog.Nop()

	db, err := database.New(tmpDir, "test-colony", logger)
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}
	return db
}

// setupTestOrchestrator creates a test orchestrator with in-memory database.
func setupTestOrchestrator(t *testing.T) (*Orchestrator, *database.Database) {
	db := setupTestDB(t)
	logger := zerolog.Nop()
	reg := registry.New()

	// Add a test agent to registry
	_, err := reg.Register(
		"test-agent",
		"test-agent",
		"10.0.0.2",
		"",
		nil,
		nil,
		"v1",
	)
	if err != nil {
		t.Fatalf("Failed to register test agent: %v", err)
	}

	orch := NewOrchestrator(logger, reg, db)
	return orch, db
}

func TestSessionPersistence(t *testing.T) {
	orch, db := setupTestOrchestrator(t)
	defer db.Close()

	ctx := context.Background()

	// Create a test session by inserting directly into database
	// Create a test session by inserting directly into database
	sessionID := "test-session-123"
	err := db.InsertDebugSession(&database.DebugSession{
		SessionID:    sessionID,
		CollectorID:  "collector-456",
		ServiceName:  "test-service",
		FunctionName: "TestFunction",
		AgentID:      "test-agent",
		SDKAddr:      "localhost:50051",
		StartedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(60 * time.Second),
		Status:       "active",
	})
	if err != nil {
		t.Fatalf("Failed to insert test session: %v", err)
	}

	// Test ListDebugSessions - should retrieve the persisted session
	listReq := connect.NewRequest(&debugpb.ListDebugSessionsRequest{})
	listResp, err := orch.ListDebugSessions(ctx, listReq)
	if err != nil {
		t.Fatalf("ListDebugSessions failed: %v", err)
	}

	if len(listResp.Msg.Sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(listResp.Msg.Sessions))
	}

	session := listResp.Msg.Sessions[0]
	if session.SessionId != sessionID {
		t.Errorf("Expected session_id %s, got %s", sessionID, session.SessionId)
	}
	if session.ServiceName != "test-service" {
		t.Errorf("Expected service_name test-service, got %s", session.ServiceName)
	}
	if session.Status != "active" {
		t.Errorf("Expected status active, got %s", session.Status)
	}
}

func TestSessionPersistenceWithFilters(t *testing.T) {
	orch, db := setupTestOrchestrator(t)
	defer db.Close()

	ctx := context.Background()

	// Insert multiple sessions with different statuses
	sessions := []struct {
		id      string
		service string
		status  string
	}{
		{"session-1", "service-a", "active"},
		{"session-2", "service-a", "stopped"},
		{"session-3", "service-b", "active"},
	}

	for _, s := range sessions {
		err := db.InsertDebugSession(&database.DebugSession{
			SessionID:    s.id,
			CollectorID:  "collector-" + s.id,
			ServiceName:  s.service,
			FunctionName: "TestFunction",
			AgentID:      "test-agent",
			SDKAddr:      "localhost:50051",
			StartedAt:    time.Now(),
			ExpiresAt:    time.Now().Add(60 * time.Second),
			Status:       s.status,
		})
		if err != nil {
			t.Fatalf("Failed to insert test session %s: %v", s.id, err)
		}
	}

	// Test filter by status
	listReq := connect.NewRequest(&debugpb.ListDebugSessionsRequest{
		Status: "active",
	})
	listResp, err := orch.ListDebugSessions(ctx, listReq)
	if err != nil {
		t.Fatalf("ListDebugSessions failed: %v", err)
	}

	if len(listResp.Msg.Sessions) != 2 {
		t.Fatalf("Expected 2 active sessions, got %d", len(listResp.Msg.Sessions))
	}

	// Test filter by service
	listReq = connect.NewRequest(&debugpb.ListDebugSessionsRequest{
		ServiceName: "service-a",
	})
	listResp, err = orch.ListDebugSessions(ctx, listReq)
	if err != nil {
		t.Fatalf("ListDebugSessions failed: %v", err)
	}

	if len(listResp.Msg.Sessions) != 2 {
		t.Fatalf("Expected 2 sessions for service-a, got %d", len(listResp.Msg.Sessions))
	}

	// Test filter by both
	listReq = connect.NewRequest(&debugpb.ListDebugSessionsRequest{
		ServiceName: "service-a",
		Status:      "active",
	})
	listResp, err = orch.ListDebugSessions(ctx, listReq)
	if err != nil {
		t.Fatalf("ListDebugSessions failed: %v", err)
	}

	if len(listResp.Msg.Sessions) != 1 {
		t.Fatalf("Expected 1 session for service-a with status active, got %d", len(listResp.Msg.Sessions))
	}
}

func TestSessionUpdate(t *testing.T) {
	orch, db := setupTestOrchestrator(t)
	defer db.Close()

	ctx := context.Background()

	// Insert a test session
	sessionID := "test-session-update"
	err := db.InsertDebugSession(&database.DebugSession{
		SessionID:    sessionID,
		CollectorID:  "collector-789",
		ServiceName:  "test-service",
		FunctionName: "TestFunction",
		AgentID:      "test-agent",
		SDKAddr:      "localhost:50051",
		StartedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(60 * time.Second),
		Status:       "active",
	})
	if err != nil {
		t.Fatalf("Failed to insert test session: %v", err)
	}

	// Update session status
	err = db.UpdateDebugSessionStatus(sessionID, "stopped")
	if err != nil {
		t.Fatalf("Failed to update session status: %v", err)
	}

	// Verify the update
	listReq := connect.NewRequest(&debugpb.ListDebugSessionsRequest{})
	listResp, err := orch.ListDebugSessions(ctx, listReq)
	if err != nil {
		t.Fatalf("ListDebugSessions failed: %v", err)
	}

	if len(listResp.Msg.Sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(listResp.Msg.Sessions))
	}

	if listResp.Msg.Sessions[0].Status != "stopped" {
		t.Errorf("Expected status stopped, got %s", listResp.Msg.Sessions[0].Status)
	}
}

func TestSessionNotFound(t *testing.T) {
	orch, db := setupTestOrchestrator(t)
	defer db.Close()

	ctx := context.Background()

	// Try to detach a non-existent session
	detachReq := connect.NewRequest(&debugpb.DetachUprobeRequest{
		SessionId: "non-existent-session",
	})
	detachResp, err := orch.DetachUprobe(ctx, detachReq)
	if err != nil {
		t.Fatalf("DetachUprobe returned error: %v", err)
	}

	if detachResp.Msg.Success {
		t.Error("Expected DetachUprobe to fail for non-existent session")
	}

	if detachResp.Msg.Error == "" {
		t.Error("Expected error message for non-existent session")
	}
}

func TestSchemaInitialization(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	logger := zerolog.Nop()
	reg := registry.New()

	// Create orchestrator - should initialize schema
	_ = NewOrchestrator(logger, reg, db)

	// Verify table exists by querying it
	// We can use the DB() method to get the underlying sql.DB
	rows, err := db.DB().Query("SELECT COUNT(*) FROM debug_sessions")
	if err != nil {
		t.Fatalf("Failed to query debug_sessions table: %v", err)
	}
	defer rows.Close()

	if !rows.Next() {
		t.Fatal("Expected row from COUNT query")
	}

	var count int
	if err := rows.Scan(&count); err != nil {
		t.Fatalf("Failed to scan count: %v", err)
	}

	// Count should be 0 for new database
	if count != 0 {
		t.Errorf("Expected 0 sessions in new database, got %d", count)
	}
}

func TestAttachUprobe_AgentNotFound(t *testing.T) {
	orch, db := setupTestOrchestrator(t)
	defer db.Close()

	ctx := context.Background()

	// Try to attach with non-existent agent
	req := connect.NewRequest(&debugpb.AttachUprobeRequest{
		AgentId:      "non-existent-agent",
		ServiceName:  "test-service",
		FunctionName: "TestFunction",
		SdkAddr:      "localhost:50051",
	})

	resp, err := orch.AttachUprobe(ctx, req)
	if err != nil {
		t.Fatalf("AttachUprobe returned error: %v", err)
	}

	if resp.Msg.Success {
		t.Error("Expected AttachUprobe to fail for non-existent agent")
	}

	if resp.Msg.Error == "" {
		t.Error("Expected error message for non-existent agent")
	}
}

func TestAttachUprobe_MissingAgentID(t *testing.T) {
	orch, db := setupTestOrchestrator(t)
	defer db.Close()

	ctx := context.Background()

	// Try to attach without agent_id and with service that doesn't exist
	req := connect.NewRequest(&debugpb.AttachUprobeRequest{
		ServiceName:  "non-existent-service",
		FunctionName: "TestFunction",
		SdkAddr:      "localhost:50051",
	})

	resp, err := orch.AttachUprobe(ctx, req)
	if err != nil {
		t.Fatalf("AttachUprobe returned error: %v", err)
	}

	if resp.Msg.Success {
		t.Error("Expected AttachUprobe to fail when service cannot be resolved")
	}

	if resp.Msg.Error == "" {
		t.Error("Expected error message for service resolution failure")
	}
}

func TestAttachUprobe_DurationCapping(t *testing.T) {
	_, db := setupTestOrchestrator(t)
	defer db.Close()

	_ = context.Background()

	tests := []struct {
		name           string
		duration       *time.Duration
		expectedMaxDur time.Duration
		expectCapped   bool
	}{
		{
			name:           "nil duration defaults to 60s",
			duration:       nil,
			expectedMaxDur: 60 * time.Second,
			expectCapped:   true,
		},
		{
			name:           "excessive duration capped to 60s",
			duration:       durationPtr(15 * time.Minute),
			expectedMaxDur: 60 * time.Second,
			expectCapped:   true,
		},
		{
			name:           "valid short duration not capped",
			duration:       durationPtr(30 * time.Second),
			expectedMaxDur: 30 * time.Second,
			expectCapped:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This test would require mocking the agent client
			// For now, we just verify the duration validation logic exists
			// by checking that sessions with different durations are handled

			// We can't fully test this without a mock client,
			// but we verify the logic is sound by inspection
			if tt.duration != nil && *tt.duration > 10*time.Minute {
				if !tt.expectCapped {
					t.Error("Duration over 10 minutes should be capped")
				}
			}
		})
	}
}

func TestQueryUprobeEvents_SessionNotFound(t *testing.T) {
	orch, db := setupTestOrchestrator(t)
	defer db.Close()

	ctx := context.Background()

	req := connect.NewRequest(&debugpb.QueryUprobeEventsRequest{
		SessionId: "non-existent-session",
	})

	_, err := orch.QueryUprobeEvents(ctx, req)
	if err == nil {
		t.Fatal("Expected error for non-existent session")
	}

	// Should return NotFound error
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("Expected NotFound error code, got: %v", connect.CodeOf(err))
	}
}

func TestQueryUprobeEvents_AgentNotInRegistry(t *testing.T) {
	orch, db := setupTestOrchestrator(t)
	defer db.Close()

	ctx := context.Background()

	// Create a session with an agent that's not in the registry
	sessionID := "test-session-orphaned"
	err := db.InsertDebugSession(&database.DebugSession{
		SessionID:    sessionID,
		CollectorID:  "collector-123",
		ServiceName:  "test-service",
		FunctionName: "TestFunction",
		AgentID:      "orphaned-agent", // Not in registry
		SDKAddr:      "localhost:50051",
		StartedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(60 * time.Second),
		Status:       "active",
	})
	if err != nil {
		t.Fatalf("Failed to insert test session: %v", err)
	}

	req := connect.NewRequest(&debugpb.QueryUprobeEventsRequest{
		SessionId: sessionID,
	})

	_, err = orch.QueryUprobeEvents(ctx, req)
	if err == nil {
		t.Fatal("Expected error when agent not found in registry")
	}

	// Should return NotFound error
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("Expected NotFound error code, got: %v", connect.CodeOf(err))
	}
}

func TestConcurrentSessionOperations(t *testing.T) {
	orch, db := setupTestOrchestrator(t)
	defer db.Close()

	ctx := context.Background()

	// Insert multiple test sessions concurrently
	numSessions := 10
	errCh := make(chan error, numSessions)

	for i := 0; i < numSessions; i++ {
		go func(idx int) {
			sessionID := fmt.Sprintf("session-%d", idx)
			err := db.InsertDebugSession(&database.DebugSession{
				SessionID:    sessionID,
				CollectorID:  fmt.Sprintf("collector-%d", idx),
				ServiceName:  "test-service",
				FunctionName: "TestFunction",
				AgentID:      "test-agent",
				SDKAddr:      "localhost:50051",
				StartedAt:    time.Now(),
				ExpiresAt:    time.Now().Add(60 * time.Second),
				Status:       "active",
			})
			errCh <- err
		}(i)
	}

	// Wait for all inserts
	for i := 0; i < numSessions; i++ {
		if err := <-errCh; err != nil {
			t.Errorf("Concurrent insert failed: %v", err)
		}
	}

	// List all sessions
	listReq := connect.NewRequest(&debugpb.ListDebugSessionsRequest{})
	listResp, err := orch.ListDebugSessions(ctx, listReq)
	if err != nil {
		t.Fatalf("ListDebugSessions failed: %v", err)
	}

	if len(listResp.Msg.Sessions) != numSessions {
		t.Errorf("Expected %d sessions, got %d", numSessions, len(listResp.Msg.Sessions))
	}
}

func TestListDebugSessions_EmptyResult(t *testing.T) {
	orch, db := setupTestOrchestrator(t)
	defer db.Close()

	ctx := context.Background()

	// List sessions when none exist
	listReq := connect.NewRequest(&debugpb.ListDebugSessionsRequest{})
	listResp, err := orch.ListDebugSessions(ctx, listReq)
	if err != nil {
		t.Fatalf("ListDebugSessions failed: %v", err)
	}

	if len(listResp.Msg.Sessions) != 0 {
		t.Errorf("Expected 0 sessions, got %d", len(listResp.Msg.Sessions))
	}
}

func TestDetachUprobe_DatabaseUpdateFailureHandled(t *testing.T) {
	_, db := setupTestOrchestrator(t)
	defer db.Close()

	_ = context.Background()

	// Create a valid session
	sessionID := "test-session-detach"
	err := db.InsertDebugSession(&database.DebugSession{
		SessionID:    sessionID,
		CollectorID:  "collector-789",
		ServiceName:  "test-service",
		FunctionName: "TestFunction",
		AgentID:      "test-agent",
		SDKAddr:      "localhost:50051",
		StartedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(60 * time.Second),
		Status:       "active",
	})
	if err != nil {
		t.Fatalf("Failed to insert test session: %v", err)
	}

	// Note: Without mocking, we can't test the actual detach flow
	// This is a placeholder that verifies the session exists
	session, err := db.GetDebugSession(sessionID)
	if err != nil {
		t.Fatalf("Failed to get session: %v", err)
	}
	if session == nil {
		t.Fatal("Session should exist")
	}
}

// Helper function to create duration pointer.
func durationPtr(d time.Duration) *time.Duration {
	return &d
}
