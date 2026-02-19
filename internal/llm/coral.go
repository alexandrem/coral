package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/safe"
)

// CoralProvider implements the Provider interface for the Coral AI Ops
// diagnostic endpoint. The model is fixed server-side and not user-selectable.
type CoralProvider struct {
	endpoint  string // Base URL of the Coral AI Ops Workers endpoint.
	apiToken  string // Bearer token for authentication.
	sessionID string // Session ID maintained across Generate() calls.
	client    *http.Client
	logger    zerolog.Logger
}

// diagnosticRequest is the JSON body sent to POST /v1/diagnose.
type diagnosticRequest struct {
	MeshID     string `json:"meshId"`
	SignalType string `json:"signalType"`
	Payload    string `json:"payload"`
	Question   string `json:"question,omitempty"`
	SessionID  string `json:"sessionId,omitempty"`
	NoCache    bool   `json:"noCache,omitempty"`
}

// diagnosticResponse is the JSON body returned from POST /v1/diagnose.
type diagnosticResponse struct {
	DiagnosticID string   `json:"diagnosticId"`
	Summary      string   `json:"summary"`
	Analysis     string   `json:"analysis"`
	Severity     string   `json:"severity"`
	Remediations []string `json:"remediations"`
	Cached       bool     `json:"cached"`
	Model        string   `json:"model"`
	SessionID    string   `json:"sessionId"`
	Usage        *struct {
		PromptTokens     int `json:"promptTokens"`
		CompletionTokens int `json:"completionTokens"`
	} `json:"usage,omitempty"`
}

// diagnosticErrorResponse is the JSON error body from the endpoint.
type diagnosticErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// NewCoralProvider creates a new Coral AI Ops provider.
func NewCoralProvider(_ context.Context, endpoint string, apiToken string) (*CoralProvider, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("Coral AI endpoint URL is required") //nolint:staticcheck
	}

	// Strip trailing slash.
	endpoint = strings.TrimRight(endpoint, "/")

	return &CoralProvider{
		endpoint: endpoint,
		apiToken: apiToken,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
		logger: zerolog.Nop(),
	}, nil
}

// Name returns the provider name.
func (p *CoralProvider) Name() string {
	return "coral"
}

// Generate sends a diagnostic request to the Coral AI Ops endpoint
// and returns the formatted analysis as a GenerateResponse.
func (p *CoralProvider) Generate(ctx context.Context, req GenerateRequest, streamCallback StreamCallback) (*GenerateResponse, error) {
	// Extract the last user message as the question.
	var question string
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" && req.Messages[i].Content != "" {
			question = req.Messages[i].Content
			break
		}
	}

	// Serialize all messages into a payload string.
	payload := serializeMessages(req.Messages)

	// Infer signal type from message content.
	signalType := inferSignalType(payload)

	// Build the diagnostic request.
	diagReq := diagnosticRequest{
		MeshID:     "coral",
		SignalType: signalType,
		Payload:    payload,
		Question:   question,
		SessionID:  p.sessionID,
	}

	body, err := json.Marshal(diagReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal diagnostic request: %w", err)
	}

	// Build HTTP request.
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/v1/diagnose", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiToken)
	}

	// Execute the request.
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("diagnostic request failed: %w", err)
	}
	defer safe.Close(resp.Body, p.logger, "failed to close response body")

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Handle error responses.
	if resp.StatusCode != http.StatusOK {
		return nil, mapHTTPError(resp.StatusCode, respBody)
	}

	// Parse the diagnostic response.
	var diagResp diagnosticResponse
	if err := json.Unmarshal(respBody, &diagResp); err != nil {
		return nil, fmt.Errorf("failed to parse diagnostic response: %w (body: %s)", err, truncateBody(respBody))
	}

	// Store session ID for next call.
	if diagResp.SessionID != "" {
		p.sessionID = diagResp.SessionID
	}

	// Format the response as readable markdown.
	content := formatDiagnosticReport(diagResp)

	// Simulate streaming if callback is provided.
	if req.Stream && streamCallback != nil {
		if err := simulateStream(content, streamCallback); err != nil {
			return nil, fmt.Errorf("stream callback error: %w", err)
		}
	}

	return &GenerateResponse{
		Content:      content,
		ToolCalls:    nil,
		FinishReason: "stop",
	}, nil
}

// serializeMessages converts chat messages into a labeled payload string.
func serializeMessages(messages []Message) string {
	var sb strings.Builder
	for _, msg := range messages {
		if msg.Content == "" {
			continue
		}
		// Skip system messages — they're internal to the agent.
		if msg.Role == "system" {
			continue
		}
		sb.WriteString(fmt.Sprintf("[%s] %s\n", msg.Role, msg.Content))
	}
	return sb.String()
}

// inferSignalType guesses the telemetry signal type from content keywords.
func inferSignalType(content string) string {
	lower := strings.ToLower(content)

	switch {
	case strings.Contains(lower, "trace") || strings.Contains(lower, "span"):
		return "traces"
	case strings.Contains(lower, "log") || strings.Contains(lower, "error:") || strings.Contains(lower, "exception"):
		return "logs"
	case strings.Contains(lower, "latency") || strings.Contains(lower, "p99") || strings.Contains(lower, "throughput") || strings.Contains(lower, "metric"):
		return "metrics"
	case strings.Contains(lower, "profile") || strings.Contains(lower, "flamegraph") || strings.Contains(lower, "cpu"):
		return "profile"
	default:
		return "mixed"
	}
}

// formatDiagnosticReport formats a diagnostic response as readable markdown.
func formatDiagnosticReport(resp diagnosticResponse) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("## %s\n", resp.Summary))
	sb.WriteString(fmt.Sprintf("**Severity:** %s\n\n", resp.Severity))
	sb.WriteString(resp.Analysis)

	if len(resp.Remediations) > 0 {
		sb.WriteString("\n\n### Remediations\n")
		for i, r := range resp.Remediations {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r))
		}
	}

	return sb.String()
}

// simulateStream emits the content in paragraph-sized chunks via the callback.
func simulateStream(content string, callback StreamCallback) error {
	paragraphs := strings.Split(content, "\n\n")
	for i, p := range paragraphs {
		chunk := p
		if i < len(paragraphs)-1 {
			chunk += "\n\n"
		}
		if err := callback(chunk); err != nil {
			return err
		}
	}
	return nil
}

// mapHTTPError converts HTTP error status codes to descriptive errors.
func mapHTTPError(statusCode int, body []byte) error {
	// Try to parse a structured error response.
	var errResp diagnosticErrorResponse
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Message != "" {
		switch statusCode {
		case http.StatusUnauthorized:
			return fmt.Errorf("Coral AI API token is invalid or missing: %s", errResp.Message) //nolint:staticcheck
		case http.StatusTooManyRequests:
			return fmt.Errorf("rate limit exceeded on Coral AI endpoint: %s", errResp.Message)
		default:
			return fmt.Errorf("Coral AI endpoint error (HTTP %d): %s", statusCode, errResp.Message) //nolint:staticcheck
		}
	}

	switch statusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("Coral AI API token is invalid or missing") //nolint:staticcheck
	case http.StatusTooManyRequests:
		return fmt.Errorf("rate limit exceeded on Coral AI endpoint")
	default:
		return fmt.Errorf("Coral AI endpoint unavailable (HTTP %d): %s", statusCode, truncateBody(body)) //nolint:staticcheck
	}
}

// truncateBody returns up to 200 bytes of the response body for error messages.
func truncateBody(body []byte) string {
	if len(body) > 200 {
		return string(body[:200]) + "..."
	}
	return string(body)
}

func init() {
	Register(ProviderMetadata{
		Name:            "coral",
		DisplayName:     "Coral AI",
		Description:     "Coral hosted AI diagnostics service",
		DefaultEnvVar:   "CORAL_AI_TOKEN",
		RequiresAPIKey:  false,             // Free tier allows anonymous access
		SupportedModels: []ModelMetadata{}, // Model is server-side managed
	}, func(ctx context.Context, modelID string, cfg *config.AskConfig, debug bool) (Provider, error) {
		endpoint := cfg.APIKeys["coral_endpoint"]
		if endpoint == "" {
			endpoint = os.Getenv("CORAL_AI_ENDPOINT")
		}
		if endpoint == "" {
			return nil, fmt.Errorf("Coral AI endpoint not configured (set coral_endpoint in api_keys or CORAL_AI_ENDPOINT)") //nolint:staticcheck
		}
		apiToken := cfg.APIKeys["coral"]
		if apiToken == "" {
			// Coral's free tier allows anonymous access, so token is optional.
			apiToken = os.Getenv("CORAL_AI_TOKEN")
		}
		return NewCoralProvider(ctx, endpoint, apiToken)
	})
}
