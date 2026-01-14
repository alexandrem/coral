package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// MockInteraction represents a single expected interaction in the mock script.
// It defines the expected input messages and the canned response to return.
type MockInteraction struct {
	// ExpectedMessages is the list of messages expected from the agent.
	// If nil/empty, matches any input (useful for simple single-turn tests).
	ExpectedMessages []Message `json:"expected_messages,omitempty"`

	// Response is the canned response to return.
	Response MockResponse `json:"response"`
}

// MockResponse represents the canned response content.
type MockResponse struct {
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// MockScript represents the full test script (list of interactions).
type MockScript struct {
	Interactions []MockInteraction `json:"interactions"`
}

// MockProvider implements the Provider interface for testing.
type MockProvider struct {
	scriptPath   string
	interactions []MockInteraction
	currentStep  int
}

// NewMockProvider creates a new mock provider that replays the given script.
func NewMockProvider(ctx context.Context, scriptPath string) (*MockProvider, error) {
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read mock script: %w", err)
	}

	var script MockScript
	if err := json.Unmarshal(data, &script); err != nil {
		return nil, fmt.Errorf("failed to parse mock script: %w", err)
	}

	return &MockProvider{
		scriptPath:   scriptPath,
		interactions: script.Interactions,
		currentStep:  0,
	}, nil
}

// Name returns the provider name.
func (p *MockProvider) Name() string {
	return "mock"
}

// Generate implements the Provider interface.
func (p *MockProvider) Generate(ctx context.Context, req GenerateRequest, streamCallback StreamCallback) (*GenerateResponse, error) {
	if p.currentStep >= len(p.interactions) {
		return nil, fmt.Errorf("mock provider exhausted: no more interactions defined (step %d)", p.currentStep)
	}

	interaction := p.interactions[p.currentStep]
	p.currentStep++

	// Verify expected messages if defined
	if len(interaction.ExpectedMessages) > 0 {
		if err := p.verifyMessages(req.Messages, interaction.ExpectedMessages); err != nil {
			return nil, fmt.Errorf("mock expectation failed at step %d: %w", p.currentStep-1, err)
		}
	}

	// Simulate streaming if callback provided
	if streamCallback != nil && req.Stream {
		// Split content into chunks to simulate streaming
		words := strings.Split(interaction.Response.Content, " ")
		for i, word := range words {
			suffix := " "
			if i == len(words)-1 {
				suffix = ""
			}
			if err := streamCallback(word + suffix); err != nil {
				return nil, err
			}
			// Tiny sleep to make it feel like streaming in tests?
			// time.Sleep(10 * time.Millisecond)
		}
	}

	return &GenerateResponse{
		Content:      interaction.Response.Content,
		ToolCalls:    interaction.Response.ToolCalls,
		FinishReason: "stop",
	}, nil
}

// verifyMessages checks if the actual messages match the expected messages.
// It ignores system messages for simplicity, focusing on conversation flow.
func (p *MockProvider) verifyMessages(actual []Message, expected []Message) error {
	// Filter out system messages from actual for comparison
	var filteredActual []Message
	for _, msg := range actual {
		if msg.Role != "system" {
			filteredActual = append(filteredActual, msg)
		}
	}

	if len(filteredActual) != len(expected) {
		// Detailed error message
		msg := fmt.Sprintf("message count mismatch: got %d, want %d\n", len(filteredActual), len(expected))
		msg += "Actual:\n"
		for i, m := range filteredActual {
			msg += fmt.Sprintf("  [%d] %s: %s\n", i, m.Role, abbreviate(m.Content))
		}
		msg += "Expected:\n"
		for i, m := range expected {
			msg += fmt.Sprintf("  [%d] %s: %s\n", i, m.Role, abbreviate(m.Content))
		}
		return fmt.Errorf("%s", msg)
	}

	for i, exp := range expected {
		act := filteredActual[i]
		if act.Role != exp.Role {
			return fmt.Errorf("role mismatch at index %d: got %s, want %s", i, act.Role, exp.Role)
		}
		// Simple content check - contains expected
		// This is looser than exact match to allow for slight variations (e.g. timestamps)
		if !strings.Contains(act.Content, exp.Content) {
			return fmt.Errorf("content mismatch at index %d:\nGot: %q\nWant (contains): %q", i, act.Content, exp.Content)
		}

		// Check tool responses if present
		if len(exp.ToolResponses) > 0 {
			if len(act.ToolResponses) != len(exp.ToolResponses) {
				return fmt.Errorf("tool response count mismatch at index %d: got %d, want %d", i, len(act.ToolResponses), len(exp.ToolResponses))
			}
			// Deep equal for tool responses for now, or check names
			for j, tr := range exp.ToolResponses {
				if act.ToolResponses[j].Name != tr.Name {
					return fmt.Errorf("tool response name mismatch at index %d/%d: got %s, want %s", i, j, act.ToolResponses[j].Name, tr.Name)
				}
				// We omit deep content check for tool outputs as they can be large and variable (JSON)
				// But we could add it if needed.
			}
		}
	}

	return nil
}

func abbreviate(s string) string {
	if len(s) > 50 {
		return s[:47] + "..."
	}
	return s
}
