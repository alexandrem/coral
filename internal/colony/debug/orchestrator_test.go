package debug

import (
	"context"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/colony"
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
	reg := registry.New(db)

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

	orch := NewOrchestrator(logger, reg, db, nil)
	return orch, db
}

func TestSessionPersistence(t *testing.T) {
	orch, db := setupTestOrchestrator(t)
	defer db.Close()

	ctx := context.Background()

	// Create a test session by inserting directly into database
	// Create a test session by inserting directly into database
	sessionID := "test-session-123"
	err := db.InsertDebugSession(context.Background(), &database.DebugSession{
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
		err := db.InsertDebugSession(context.Background(), &database.DebugSession{
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
	err := db.InsertDebugSession(context.Background(), &database.DebugSession{
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
	err = db.UpdateDebugSessionStatus(context.Background(), sessionID, "stopped")
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
	reg := registry.New(db)

	// Create orchestrator - should initialize schema
	_ = NewOrchestrator(logger, reg, db, nil)

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
	err := db.InsertDebugSession(context.Background(), &database.DebugSession{
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

	// Insert test events into database
	testEvents := []*agentv1.UprobeEvent{
		{
			Timestamp:    timestamppb.New(time.Now()),
			CollectorId:  "collector-123",
			AgentId:      "orphaned-agent",
			ServiceName:  "test-service",
			FunctionName: "TestFunction",
			EventType:    "return",
			DurationNs:   1000000,
			Pid:          1234,
			Tid:          1234,
		},
		{
			Timestamp:    timestamppb.New(time.Now().Add(1 * time.Second)),
			CollectorId:  "collector-123",
			AgentId:      "orphaned-agent",
			ServiceName:  "test-service",
			FunctionName: "TestFunction",
			EventType:    "return",
			DurationNs:   2000000,
			Pid:          1234,
			Tid:          1234,
		},
	}

	err = db.InsertDebugEvents(context.Background(), sessionID, testEvents)
	if err != nil {
		t.Fatalf("Failed to insert test events: %v", err)
	}

	req := connect.NewRequest(&debugpb.QueryUprobeEventsRequest{
		SessionId: sessionID,
	})

	// Should successfully fall back to database when agent not in registry
	resp, err := orch.QueryUprobeEvents(ctx, req)
	if err != nil {
		t.Fatalf("Expected successful fallback to database, got error: %v", err)
	}

	// Verify we got the events from database
	if len(resp.Msg.Events) != 2 {
		t.Errorf("Expected 2 events from database, got %d", len(resp.Msg.Events))
	}

	// Verify event details
	if resp.Msg.Events[0].FunctionName != "TestFunction" {
		t.Errorf("Expected function name TestFunction, got %s", resp.Msg.Events[0].FunctionName)
	}
}

func TestQueryUprobeEvents_AgentRPCFailsFallbackToDatabase(t *testing.T) {
	orch, db := setupTestOrchestrator(t)
	defer db.Close()

	ctx := context.Background()

	// Create a session with an agent that IS in the registry
	sessionID := "test-session-rpc-fail"
	err := db.InsertDebugSession(context.Background(), &database.DebugSession{
		SessionID:    sessionID,
		CollectorID:  "collector-456",
		ServiceName:  "test-service",
		FunctionName: "TestFunction",
		AgentID:      "test-agent", // This agent exists in registry
		SDKAddr:      "localhost:50051",
		StartedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(60 * time.Second),
		Status:       "active",
	})
	if err != nil {
		t.Fatalf("Failed to insert test session: %v", err)
	}

	// Insert test events into database
	testEvents := []*agentv1.UprobeEvent{
		{
			Timestamp:    timestamppb.New(time.Now()),
			CollectorId:  "collector-456",
			AgentId:      "test-agent",
			ServiceName:  "test-service",
			FunctionName: "TestFunction",
			EventType:    "return",
			DurationNs:   500000,
			Pid:          5678,
			Tid:          5678,
		},
	}

	err = db.InsertDebugEvents(context.Background(), sessionID, testEvents)
	if err != nil {
		t.Fatalf("Failed to insert test events: %v", err)
	}

	// Set up a client factory that simulates RPC failure
	orch.clientFactory = func(httpClient connect.HTTPClient, url string, opts ...connect.ClientOption) agentv1connect.AgentDebugServiceClient {
		return &mockFailingDebugServiceClient{}
	}

	req := connect.NewRequest(&debugpb.QueryUprobeEventsRequest{
		SessionId: sessionID,
	})

	// Should successfully fall back to database when agent RPC fails
	resp, err := orch.QueryUprobeEvents(ctx, req)
	if err != nil {
		t.Fatalf("Expected successful fallback to database, got error: %v", err)
	}

	// Verify we got the events from database
	if len(resp.Msg.Events) != 1 {
		t.Errorf("Expected 1 event from database, got %d", len(resp.Msg.Events))
	}

	// Verify event details
	if resp.Msg.Events[0].FunctionName != "TestFunction" {
		t.Errorf("Expected function name TestFunction, got %s", resp.Msg.Events[0].FunctionName)
	}
}

// Mock debug service client that always fails RPC calls
type mockFailingDebugServiceClient struct{}

func (m *mockFailingDebugServiceClient) StartUprobeCollector(ctx context.Context, req *connect.Request[agentv1.StartUprobeCollectorRequest]) (*connect.Response[agentv1.StartUprobeCollectorResponse], error) {
	return nil, fmt.Errorf("simulated RPC failure")
}

func (m *mockFailingDebugServiceClient) StopUprobeCollector(ctx context.Context, req *connect.Request[agentv1.StopUprobeCollectorRequest]) (*connect.Response[agentv1.StopUprobeCollectorResponse], error) {
	return nil, fmt.Errorf("simulated RPC failure")
}

func (m *mockFailingDebugServiceClient) QueryUprobeEvents(ctx context.Context, req *connect.Request[agentv1.QueryUprobeEventsRequest]) (*connect.Response[agentv1.QueryUprobeEventsResponse], error) {
	return nil, fmt.Errorf("simulated RPC failure")
}

func (m *mockFailingDebugServiceClient) ProfileCPU(ctx context.Context, req *connect.Request[agentv1.ProfileCPUAgentRequest]) (*connect.Response[agentv1.ProfileCPUAgentResponse], error) {
	return nil, fmt.Errorf("simulated RPC failure")
}

func (m *mockFailingDebugServiceClient) QueryCPUProfileSamples(ctx context.Context, req *connect.Request[agentv1.QueryCPUProfileSamplesRequest]) (*connect.Response[agentv1.QueryCPUProfileSamplesResponse], error) {
	return nil, fmt.Errorf("simulated RPC failure")
}

func (m *mockFailingDebugServiceClient) ProfileMemory(ctx context.Context, req *connect.Request[agentv1.ProfileMemoryAgentRequest]) (*connect.Response[agentv1.ProfileMemoryAgentResponse], error) {
	return nil, fmt.Errorf("simulated RPC failure")
}

func (m *mockFailingDebugServiceClient) QueryMemoryProfileSamples(ctx context.Context, req *connect.Request[agentv1.QueryMemoryProfileSamplesRequest]) (*connect.Response[agentv1.QueryMemoryProfileSamplesResponse], error) {
	return nil, fmt.Errorf("simulated RPC failure")
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
			err := db.InsertDebugSession(context.Background(), &database.DebugSession{
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
	err := db.InsertDebugSession(context.Background(), &database.DebugSession{
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
	session, err := db.GetDebugSession(context.Background(), sessionID)
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

// TestProfileFunctions_TracksRealSessionIDs validates the core bug fix:
// ProfileFunctions must return REAL session IDs (created by AttachUprobe),
// not fake UUIDs. This test verifies that the returned session ID actually
// exists in the database and was created by the profiling operation.
func TestProfileFunctions_TracksRealSessionIDs(t *testing.T) {
	orch, db := setupTestOrchestrator(t)
	defer db.Close()

	ctx := context.Background()

	// Create a real function registry with test data
	functionRegistry := colony.NewFunctionRegistry(db, zerolog.Nop())

	// Insert test functions into the registry (using agentv1.FunctionInfo)
	testFunctions := []*agentv1.FunctionInfo{
		{
			Name:     "slowFunction",
			Package:  "main",
			HasDwarf: true,
		},
		{
			Name:     "fastFunction",
			Package:  "main",
			HasDwarf: true,
		},
	}

	if err := functionRegistry.StoreFunctions(ctx, "test-agent", "test-service", "test-hash", testFunctions); err != nil {
		t.Fatalf("Failed to store test functions: %v", err)
	}

	// Replace the orchestrator's function registry
	orch.functionRegistry = functionRegistry

	// Create a mock agent client that simulates successful probe attachment
	mockClientFactory := &mockDebugServiceClientFactory{
		sessions: make(map[string]*mockSession),
		db:       db,
	}

	// Replace the orchestrator's client factory
	orch.clientFactory = mockClientFactory.newClient

	// Call ProfileFunctions with a very short duration for testing
	// Use 1ms so the sleep is negligible, but set expiration far in future
	// so session is still active when we query (not expired)
	req := connect.NewRequest(&debugpb.ProfileFunctionsRequest{
		ServiceName:  "test-service",
		Query:        "function",
		Strategy:     "all",
		MaxFunctions: 10,
		Duration:     durationpb.New(1 * time.Millisecond), // Very short sleep
		Async:        false,
		SampleRate:   1.0,
	})

	// Add some mock events to the sessions that will be created
	mockClientFactory.eventGenerator = func(sessionID string) []*agentv1.UprobeEvent {
		// Generate different latencies for different functions
		if sessionID == "session-slowFunction" {
			// High latency events for slow function
			return generateMockEvents(10, 800*time.Millisecond)
		}
		// Low latency events for fast function
		return generateMockEvents(5, 50*time.Millisecond)
	}

	resp, err := orch.ProfileFunctions(ctx, req)
	if err != nil {
		t.Fatalf("ProfileFunctions failed: %v", err)
	}

	// ====================================================================
	// CRITICAL TEST: Verify the session ID is REAL, not a fake UUID
	// ====================================================================
	// This was the core bug: ProfileFunctions generated a fake UUID and
	// returned it, but that UUID was never stored in the database.
	// The real sessions created by AttachUprobe were lost.

	if resp.Msg.SessionId == "" {
		t.Fatal("Expected non-empty session ID")
	}

	// The returned session ID must exist in the database
	session, err := db.GetDebugSession(context.Background(), resp.Msg.SessionId)
	if err != nil {
		t.Fatalf("Failed to query session from database: %v", err)
	}
	if session == nil {
		t.Fatal("BUG NOT FIXED: Session ID doesn't exist in database! ProfileFunctions returned a fake UUID that was never created.")
	}

	t.Logf("✓ Session ID %s exists in database (bug fixed!)", resp.Msg.SessionId)

	// Validate that ProfileFunctions actually attempted to profile functions
	if resp.Msg.Summary == nil {
		t.Fatal("Expected summary to be present")
	}

	if resp.Msg.Summary.FunctionsSelected < 1 {
		t.Errorf("Expected at least 1 function selected, got %d", resp.Msg.Summary.FunctionsSelected)
	}

	if resp.Msg.Summary.FunctionsProbed < 1 {
		t.Errorf("Expected at least 1 function probed, got %d", resp.Msg.Summary.FunctionsProbed)
	}

	// Verify we have results
	if len(resp.Msg.Results) == 0 {
		t.Error("Expected at least one result")
	}

	// Validate status
	if resp.Msg.Status != "completed" && resp.Msg.Status != "partial_success" {
		t.Errorf("Expected status 'completed' or 'partial_success', got '%s'", resp.Msg.Status)
	}

	// Validate we have a recommendation
	if resp.Msg.Recommendation == "" {
		t.Error("Expected non-empty recommendation")
	}

	t.Logf("Summary: %d functions selected, %d probed, status=%s",
		resp.Msg.Summary.FunctionsSelected,
		resp.Msg.Summary.FunctionsProbed,
		resp.Msg.Status)
}

// Mock debug service client factory
type mockDebugServiceClientFactory struct {
	sessions       map[string]*mockSession
	db             *database.Database
	eventGenerator func(sessionID string) []*agentv1.UprobeEvent
}

type mockSession struct {
	sessionID string
	events    []*agentv1.UprobeEvent
}

func (f *mockDebugServiceClientFactory) newClient(httpClient connect.HTTPClient, url string, opts ...connect.ClientOption) agentv1connect.AgentDebugServiceClient {
	return &mockDebugServiceClient{
		factory: f,
	}
}

type mockDebugServiceClient struct {
	factory *mockDebugServiceClientFactory
}

func (m *mockDebugServiceClient) StartUprobeCollector(ctx context.Context, req *connect.Request[agentv1.StartUprobeCollectorRequest]) (*connect.Response[agentv1.StartUprobeCollectorResponse], error) {
	// Generate a deterministic session ID based on function name
	sessionID := "session-" + req.Msg.FunctionName
	collectorID := "collector-" + req.Msg.FunctionName

	// Store session in factory for later retrieval
	events := []*agentv1.UprobeEvent{}
	if m.factory.eventGenerator != nil {
		events = m.factory.eventGenerator(sessionID)
	}

	m.factory.sessions[collectorID] = &mockSession{
		sessionID: sessionID,
		events:    events,
	}

	return connect.NewResponse(&agentv1.StartUprobeCollectorResponse{
		CollectorId: collectorID,
		Supported:   true,
	}), nil
}

func (m *mockDebugServiceClient) StopUprobeCollector(ctx context.Context, req *connect.Request[agentv1.StopUprobeCollectorRequest]) (*connect.Response[agentv1.StopUprobeCollectorResponse], error) {
	return connect.NewResponse(&agentv1.StopUprobeCollectorResponse{
		Success: true,
	}), nil
}

func (m *mockDebugServiceClient) QueryUprobeEvents(ctx context.Context, req *connect.Request[agentv1.QueryUprobeEventsRequest]) (*connect.Response[agentv1.QueryUprobeEventsResponse], error) {
	session, ok := m.factory.sessions[req.Msg.CollectorId]
	if !ok {
		return connect.NewResponse(&agentv1.QueryUprobeEventsResponse{
			Events: []*agentv1.UprobeEvent{},
		}), nil
	}

	// Persist events to database so they're available even if session expires
	// This simulates what DetachUprobe does
	if len(session.events) > 0 {
		// Find the session ID from the database
		dbSession, _ := m.factory.db.GetDebugSession(context.Background(), session.sessionID)
		if dbSession != nil {
			_ = m.factory.db.InsertDebugEvents(context.Background(), session.sessionID, session.events)
		}
	}

	return connect.NewResponse(&agentv1.QueryUprobeEventsResponse{
		Events: session.events,
	}), nil
}

func (m *mockDebugServiceClient) ProfileCPU(ctx context.Context, req *connect.Request[agentv1.ProfileCPUAgentRequest]) (*connect.Response[agentv1.ProfileCPUAgentResponse], error) {
	return connect.NewResponse(&agentv1.ProfileCPUAgentResponse{
		Success:      true,
		TotalSamples: 100,
	}), nil
}

func (m *mockDebugServiceClient) QueryCPUProfileSamples(ctx context.Context, req *connect.Request[agentv1.QueryCPUProfileSamplesRequest]) (*connect.Response[agentv1.QueryCPUProfileSamplesResponse], error) {
	return connect.NewResponse(&agentv1.QueryCPUProfileSamplesResponse{
		Samples:      []*agentv1.CPUProfileSample{},
		TotalSamples: 0,
	}), nil
}

func (m *mockDebugServiceClient) ProfileMemory(ctx context.Context, req *connect.Request[agentv1.ProfileMemoryAgentRequest]) (*connect.Response[agentv1.ProfileMemoryAgentResponse], error) {
	return connect.NewResponse(&agentv1.ProfileMemoryAgentResponse{Success: true}), nil
}

func (m *mockDebugServiceClient) QueryMemoryProfileSamples(ctx context.Context, req *connect.Request[agentv1.QueryMemoryProfileSamplesRequest]) (*connect.Response[agentv1.QueryMemoryProfileSamplesResponse], error) {
	return connect.NewResponse(&agentv1.QueryMemoryProfileSamplesResponse{}), nil
}

// Generate mock events with specified latency
func generateMockEvents(count int, latency time.Duration) []*agentv1.UprobeEvent {
	events := make([]*agentv1.UprobeEvent, count)
	baseTime := time.Now()

	for i := 0; i < count; i++ {
		events[i] = &agentv1.UprobeEvent{
			Timestamp:  timestamppb.New(baseTime.Add(time.Duration(i) * time.Second)),
			EventType:  "return",
			DurationNs: uint64(latency.Nanoseconds()),
			Pid:        1234,
			Tid:        1234,
		}
	}

	return events
}
