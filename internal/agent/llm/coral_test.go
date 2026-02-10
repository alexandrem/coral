package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCoralProvider_Name(t *testing.T) {
	p, err := NewCoralProvider(context.Background(), "https://example.com", "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := p.Name(); got != "coral" {
		t.Errorf("Name() = %q, want %q", got, "coral")
	}
}

func TestCoralProvider_NewRequiresEndpoint(t *testing.T) {
	_, err := NewCoralProvider(context.Background(), "", "token")
	if err == nil {
		t.Fatal("expected error for empty endpoint")
	}
}

func TestCoralProvider_Generate_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request.
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/diagnose" {
			t.Errorf("expected /v1/diagnose, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("expected Bearer test-token, got %s", r.Header.Get("Authorization"))
		}

		// Verify request body.
		var req diagnosticRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		if req.MeshID != "coral" {
			t.Errorf("expected meshId coral, got %s", req.MeshID)
		}
		if req.Question == "" {
			t.Error("expected non-empty question")
		}

		// Return diagnostic response.
		resp := diagnosticResponse{
			DiagnosticID: "diag-123",
			Summary:      "High error rate on checkout service",
			Analysis:     "The checkout service is returning 503 errors at a rate of 15% over the last 10 minutes.",
			Severity:     "critical",
			Remediations: []string{"Scale up checkout pods", "Check database connection pool"},
			Cached:       false,
			Model:        "@cf/qwen/qwen3-coder-next",
			SessionID:    "session-abc",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p, err := NewCoralProvider(context.Background(), server.URL, "test-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp, err := p.Generate(context.Background(), GenerateRequest{
		Messages: []Message{
			{Role: "user", Content: "why is checkout returning errors?"},
		},
	}, nil)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "stop")
	}
	if !strings.Contains(resp.Content, "High error rate") {
		t.Errorf("Content missing summary, got: %s", resp.Content)
	}
	if !strings.Contains(resp.Content, "503 errors") {
		t.Errorf("Content missing analysis, got: %s", resp.Content)
	}
	if !strings.Contains(resp.Content, "Scale up checkout pods") {
		t.Errorf("Content missing remediations, got: %s", resp.Content)
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(resp.ToolCalls))
	}
}

func TestCoralProvider_Generate_SessionContinuity(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var req diagnosticRequest
		json.NewDecoder(r.Body).Decode(&req)

		switch callCount {
		case 1:
			// First call: no session ID expected.
			if req.SessionID != "" {
				t.Errorf("first call should have no sessionId, got %q", req.SessionID)
			}
		case 2:
			// Second call: should include the session ID from first response.
			if req.SessionID != "session-xyz" {
				t.Errorf("second call sessionId = %q, want %q", req.SessionID, "session-xyz")
			}
		}

		resp := diagnosticResponse{
			DiagnosticID: "diag-" + string(rune('0'+callCount)),
			Summary:      "Test summary",
			Analysis:     "Test analysis",
			Severity:     "info",
			SessionID:    "session-xyz",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p, err := NewCoralProvider(context.Background(), server.URL, "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// First call.
	_, err = p.Generate(context.Background(), GenerateRequest{
		Messages: []Message{{Role: "user", Content: "first question"}},
	}, nil)
	if err != nil {
		t.Fatalf("first Generate() error: %v", err)
	}

	// Second call should include session ID.
	_, err = p.Generate(context.Background(), GenerateRequest{
		Messages: []Message{
			{Role: "user", Content: "first question"},
			{Role: "assistant", Content: "first answer"},
			{Role: "user", Content: "follow-up question"},
		},
	}, nil)
	if err != nil {
		t.Fatalf("second Generate() error: %v", err)
	}

	if callCount != 2 {
		t.Errorf("expected 2 calls, got %d", callCount)
	}
}

func TestCoralProvider_Generate_Streaming(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := diagnosticResponse{
			DiagnosticID: "diag-1",
			Summary:      "Test",
			Analysis:     "Paragraph one.\n\nParagraph two.",
			Severity:     "ok",
			SessionID:    "s1",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p, err := NewCoralProvider(context.Background(), server.URL, "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var chunks []string
	callback := func(chunk string) error {
		chunks = append(chunks, chunk)
		return nil
	}

	_, err = p.Generate(context.Background(), GenerateRequest{
		Messages: []Message{{Role: "user", Content: "test"}},
		Stream:   true,
	}, callback)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	if len(chunks) < 2 {
		t.Errorf("expected at least 2 chunks, got %d", len(chunks))
	}
}

func TestCoralProvider_Generate_AuthError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(diagnosticErrorResponse{
			Error:   "Unauthorized",
			Message: "Invalid or missing API token",
		})
	}))
	defer server.Close()

	p, err := NewCoralProvider(context.Background(), server.URL, "bad-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = p.Generate(context.Background(), GenerateRequest{
		Messages: []Message{{Role: "user", Content: "test"}},
	}, nil)
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !strings.Contains(err.Error(), "invalid or missing") {
		t.Errorf("error should mention auth, got: %v", err)
	}
}

func TestCoralProvider_Generate_RateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(diagnosticErrorResponse{
			Error:   "Rate limit exceeded",
			Message: "Maximum 60 requests per minute",
		})
	}))
	defer server.Close()

	p, err := NewCoralProvider(context.Background(), server.URL, "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = p.Generate(context.Background(), GenerateRequest{
		Messages: []Message{{Role: "user", Content: "test"}},
	}, nil)
	if err == nil {
		t.Fatal("expected error for 429")
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("error should mention rate limit, got: %v", err)
	}
}

func TestCoralProvider_PayloadSerialization(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are an assistant"},
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "second question"},
	}

	payload := serializeMessages(messages)

	// System messages should be excluded.
	if strings.Contains(payload, "[system]") {
		t.Error("system messages should be excluded from payload")
	}

	if !strings.Contains(payload, "[user] first question") {
		t.Error("missing first user message")
	}
	if !strings.Contains(payload, "[assistant] first answer") {
		t.Error("missing assistant message")
	}
	if !strings.Contains(payload, "[user] second question") {
		t.Error("missing second user message")
	}
}

func TestInferSignalType(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"show me the traces for checkout", "traces"},
		{"what span is slowest?", "traces"},
		{"check the error logs", "logs"},
		{"exception in payment service", "logs"},
		{"p99 latency is high", "metrics"},
		{"what does the CPU profile show?", "profile"},
		{"what's going on with checkout?", "mixed"},
	}

	for _, tt := range tests {
		got := inferSignalType(tt.input)
		if got != tt.expected {
			t.Errorf("inferSignalType(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
