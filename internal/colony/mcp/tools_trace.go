package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// executeTraceRequestPathTool executes the coral_trace_request_path tool.
func (s *Server) executeTraceRequestPathTool(ctx context.Context, argumentsJSON string) (string, error) {
	var input TraceRequestPathInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_trace_request_path", input)

	// TODO: Call DebugService.TraceRequestPath
	// Note: TraceRequestPath is not yet implemented in the orchestrator
	return "", fmt.Errorf("coral_trace_request_path is not yet implemented")
}

// registerTraceRequestPathTool registers the coral_trace_request_path tool.
func (s *Server) registerTraceRequestPathTool() {
	if !s.isToolEnabled("coral_trace_request_path") {
		return
	}

	inputSchema, err := generateInputSchema(TraceRequestPathInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_trace_request_path")
		return
	}

	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_trace_request_path")
		return
	}

	tool := mcp.NewToolWithRawSchema(
		"coral_trace_request_path",
		"Trace all functions called during HTTP request execution. Auto-discovers call chain and builds execution tree.",
		schemaBytes,
	)

	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var input TraceRequestPathInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_trace_request_path", input)

		// TODO: Call DebugService.TraceRequestPath
		// Note: TraceRequestPath is not yet implemented in the orchestrator
		return mcp.NewToolResultError("coral_trace_request_path is not yet implemented"), nil
	})
}
