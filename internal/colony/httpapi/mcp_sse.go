// Package httpapi provides MCP SSE transport for the HTTP API endpoint (RFD 031).
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/internal/auth"
)

// MCPSSEHandler implements MCP over Server-Sent Events.
type MCPSSEHandler struct {
	mcpServer  MCPServerInterface
	tokenStore *auth.TokenStore
	logger     zerolog.Logger
}

// NewMCPSSEHandler creates a new MCP SSE handler.
func NewMCPSSEHandler(mcpServer MCPServerInterface, tokenStore *auth.TokenStore,
	logger zerolog.Logger) *MCPSSEHandler {
	return &MCPSSEHandler{
		mcpServer:  mcpServer,
		tokenStore: tokenStore,
		logger:     logger.With().Str("handler", "mcp-sse").Logger(),
	}
}

// MCPRequest represents an MCP tool call request.
type MCPRequest struct {
	// JSONRPC version (should be "2.0").
	JSONRPC string `json:"jsonrpc"`

	// ID is the request ID.
	ID interface{} `json:"id"`

	// Method is the MCP method (e.g., "tools/call", "tools/list").
	Method string `json:"method"`

	// Params contains the method parameters.
	Params json.RawMessage `json:"params,omitempty"`
}

// MCPResponse represents an MCP response.
type MCPResponse struct {
	// JSONRPC version.
	JSONRPC string `json:"jsonrpc"`

	// ID is the request ID.
	ID interface{} `json:"id"`

	// Result contains the method result (success case).
	Result interface{} `json:"result,omitempty"`

	// Error contains the error (failure case).
	Error *MCPError `json:"error,omitempty"`
}

// MCPError represents an MCP error.
type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ToolCallParams represents parameters for tools/call.
type ToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ServeHTTP handles MCP SSE requests.
func (h *MCPSSEHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Verify authentication.
	token := GetAuthenticatedToken(r.Context())
	if token == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	h.logger.Info().
		Str("token_id", token.TokenID).
		Str("method", r.Method).
		Msg("MCP SSE request received")

	switch r.Method {
	case http.MethodGet:
		h.handleSSEConnection(w, r, token)
	case http.MethodPost:
		h.handleToolCall(w, r, token)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSSEConnection handles SSE GET requests for streaming.
func (h *MCPSSEHandler) handleSSEConnection(
	w http.ResponseWriter,
	r *http.Request,
	token *auth.APIToken,
) {
	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering.

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	h.logger.Info().
		Str("token_id", token.TokenID).
		Msg("MCP SSE connection established")

	ctx := r.Context()

	// Send initial capabilities event.
	tools := h.mcpServer.ListToolNames()
	h.sendEvent(w, flusher, "capabilities", map[string]interface{}{
		"protocol_version": "2024-11-05",
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{
				"listChanged": false,
			},
		},
		"tools": tools,
	})

	// Keep connection alive with periodic pings until client disconnects.
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			h.logger.Debug().
				Str("token_id", token.TokenID).
				Msg("MCP SSE connection closed")
			return
		case <-ticker.C:
			h.sendEvent(w, flusher, "ping", map[string]interface{}{
				"timestamp": time.Now().Unix(),
			})
		}
	}
}

// handleToolCall handles POST requests for tool calls.
func (h *MCPSSEHandler) handleToolCall(
	w http.ResponseWriter,
	r *http.Request,
	token *auth.APIToken,
) {
	var request MCPRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		h.sendJSONResponse(w, MCPResponse{
			JSONRPC: "2.0",
			ID:      nil,
			Error: &MCPError{
				Code:    -32700,
				Message: "Parse error: " + err.Error(),
			},
		})
		return
	}

	h.logger.Debug().
		Str("token_id", token.TokenID).
		Str("method", request.Method).
		Interface("id", request.ID).
		Msg("MCP request received")

	switch request.Method {
	case "tools/list":
		h.handleToolsList(w, request, token)
	case "tools/call":
		h.handleToolsCall(w, r.Context(), request, token)
	default:
		h.sendJSONResponse(w, MCPResponse{
			JSONRPC: "2.0",
			ID:      request.ID,
			Error: &MCPError{
				Code:    -32601,
				Message: "Method not found: " + request.Method,
			},
		})
	}
}

// handleToolsList handles tools/list requests.
func (h *MCPSSEHandler) handleToolsList(
	w http.ResponseWriter,
	request MCPRequest,
	token *auth.APIToken,
) {
	tools := h.mcpServer.ListToolNames()

	h.sendJSONResponse(w, MCPResponse{
		JSONRPC: "2.0",
		ID:      request.ID,
		Result: map[string]interface{}{
			"tools": tools,
		},
	})
}

// handleToolsCall handles tools/call requests.
func (h *MCPSSEHandler) handleToolsCall(
	w http.ResponseWriter,
	ctx context.Context,
	request MCPRequest,
	token *auth.APIToken,
) {
	var params ToolCallParams
	if err := json.Unmarshal(request.Params, &params); err != nil {
		h.sendJSONResponse(w, MCPResponse{
			JSONRPC: "2.0",
			ID:      request.ID,
			Error: &MCPError{
				Code:    -32602,
				Message: "Invalid params: " + err.Error(),
			},
		})
		return
	}

	// Check permission for this tool.
	requiredPerm := GetMCPToolPermission(params.Name)
	if !auth.HasPermission(token, requiredPerm) {
		h.logger.Warn().
			Str("token_id", token.TokenID).
			Str("tool", params.Name).
			Str("required_permission", string(requiredPerm)).
			Msg("Permission denied for MCP tool")

		h.sendJSONResponse(w, MCPResponse{
			JSONRPC: "2.0",
			ID:      request.ID,
			Error: &MCPError{
				Code:    -32600,
				Message: fmt.Sprintf("Permission denied: tool %q requires %q permission", params.Name, requiredPerm),
			},
		})
		return
	}

	// Execute tool.
	result, err := h.mcpServer.ExecuteTool(ctx, params.Name, string(params.Arguments))
	if err != nil {
		h.logger.Error().
			Str("token_id", token.TokenID).
			Str("tool", params.Name).
			Err(err).
			Msg("MCP tool execution failed")

		h.sendJSONResponse(w, MCPResponse{
			JSONRPC: "2.0",
			ID:      request.ID,
			Error: &MCPError{
				Code:    -32000,
				Message: "Tool execution failed: " + err.Error(),
			},
		})
		return
	}

	h.logger.Debug().
		Str("token_id", token.TokenID).
		Str("tool", params.Name).
		Msg("MCP tool executed successfully")

	h.sendJSONResponse(w, MCPResponse{
		JSONRPC: "2.0",
		ID:      request.ID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": result,
				},
			},
		},
	})
}

// sendEvent sends an SSE event.
func (h *MCPSSEHandler) sendEvent(w http.ResponseWriter, flusher http.Flusher, event string, data interface{}) {
	var payload []byte
	if data != nil {
		payload, _ = json.Marshal(data)
	}

	fmt.Fprintf(w, "event: %s\n", event)
	if len(payload) > 0 {
		fmt.Fprintf(w, "data: %s\n", payload)
	}
	fmt.Fprint(w, "\n")
	flusher.Flush()
}

// sendJSONResponse sends a JSON response.
func (h *MCPSSEHandler) sendJSONResponse(w http.ResponseWriter, response MCPResponse) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error().Err(err).Msg("Failed to encode MCP response")
	}
}
