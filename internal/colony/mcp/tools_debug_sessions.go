package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/mark3labs/mcp-go/mcp"

	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/safe"
)

// executeListDebugSessionsTool executes the coral_list_debug_sessions tool.
func (s *Server) executeListDebugSessionsTool(ctx context.Context, argumentsJSON string) (string, error) {
	if s.debugService == nil {
		return "", fmt.Errorf("debug service not available")
	}

	var input ListDebugSessionsInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_list_debug_sessions", input)

	status := ""
	if input.Status != nil {
		status = *input.Status
	}

	// Call DebugService.ListDebugSessions
	req := connect.NewRequest(&debugpb.ListDebugSessionsRequest{
		Status: status,
	})

	resp, err := s.debugService.ListDebugSessions(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to list debug sessions: %w", err)
	}

	if len(resp.Msg.Sessions) == 0 {
		return "No active debug sessions found.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d debug sessions:\n\n", len(resp.Msg.Sessions)))
	for _, session := range resp.Msg.Sessions {
		sb.WriteString(fmt.Sprintf("- Session ID: %s\n", session.SessionId))
		sb.WriteString(fmt.Sprintf("  Service:    %s\n", session.ServiceName))
		sb.WriteString(fmt.Sprintf("  Function:   %s\n", session.FunctionName))
		sb.WriteString(fmt.Sprintf("  Agent ID:   %s\n", session.AgentId))
		sb.WriteString(fmt.Sprintf("  Status:     %s\n", session.Status))
		sb.WriteString(fmt.Sprintf("  Expires:    %s\n", session.ExpiresAt.AsTime().Format(time.RFC3339)))
		sb.WriteString("\n")
	}

	return sb.String(), nil
}

// executeGetDebugResultsTool executes the coral_get_debug_results tool.
func (s *Server) executeGetDebugResultsTool(ctx context.Context, argumentsJSON string) (string, error) {
	if s.debugService == nil {
		return "", fmt.Errorf("debug service not available")
	}

	var input GetDebugResultsInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_get_debug_results", input)

	// Call DebugService.QueryUprobeEvents (used for getting results)
	req := connect.NewRequest(&debugpb.QueryUprobeEventsRequest{
		SessionId: input.SessionID,
	})

	resp, err := s.debugService.QueryUprobeEvents(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to get debug results: %w", err)
	}

	// Format results
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Debug Results for Session %s:\n\n", input.SessionID))
	sb.WriteString(fmt.Sprintf("Total Events: %d\n", len(resp.Msg.Events)))

	if len(resp.Msg.Events) > 0 {
		sb.WriteString("\nRecent Events:\n")
		for i, event := range resp.Msg.Events {
			if i >= 10 {
				sb.WriteString(fmt.Sprintf("... and %d more events\n", len(resp.Msg.Events)-10))
				break
			}
			durationNS, clamped := safe.Uint64ToInt64(event.DurationNs)
			if clamped {
				s.logger.Warn().
					Uint64("durationNS", event.DurationNs).
					Msg("Event duration NS exceeds int64 max, clamped to max value")
			}

			duration := time.Duration(durationNS) * time.Nanosecond
			sb.WriteString(fmt.Sprintf("- [%s] Duration: %s\n",
				event.Timestamp.AsTime().Format(time.RFC3339),
				duration.String()))
		}
	}

	return sb.String(), nil
}

// registerListDebugSessionsTool registers the coral_list_debug_sessions tool.
func (s *Server) registerListDebugSessionsTool() {
	if !s.isToolEnabled("coral_list_debug_sessions") {
		return
	}

	inputSchema, err := generateInputSchema(ListDebugSessionsInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_list_debug_sessions")
		return
	}

	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_list_debug_sessions")
		return
	}

	tool := mcp.NewToolWithRawSchema(
		"coral_list_debug_sessions",
		"List active and recent debug sessions across services.",
		schemaBytes,
	)

	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if s.debugService == nil {
			return mcp.NewToolResultError("debug service not available"), nil
		}

		var input ListDebugSessionsInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_list_debug_sessions", input)

		status := ""
		if input.Status != nil {
			status = *input.Status
		}

		// Call DebugService.ListDebugSessions
		req := connect.NewRequest(&debugpb.ListDebugSessionsRequest{
			Status: status,
		})

		resp, err := s.debugService.ListDebugSessions(ctx, req)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list debug sessions: %v", err)), nil
		}

		if len(resp.Msg.Sessions) == 0 {
			return mcp.NewToolResultText("No active debug sessions found."), nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Found %d debug sessions:\n\n", len(resp.Msg.Sessions)))
		for _, session := range resp.Msg.Sessions {
			sb.WriteString(fmt.Sprintf("- Session ID: %s\n", session.SessionId))
			sb.WriteString(fmt.Sprintf("  Service:    %s\n", session.ServiceName))
			sb.WriteString(fmt.Sprintf("  Function:   %s\n", session.FunctionName))
			sb.WriteString(fmt.Sprintf("  Agent ID:   %s\n", session.AgentId))
			sb.WriteString(fmt.Sprintf("  Status:     %s\n", session.Status))
			sb.WriteString(fmt.Sprintf("  Expires:    %s\n", session.ExpiresAt.AsTime().Format(time.RFC3339)))
			sb.WriteString("\n")
		}

		return mcp.NewToolResultText(sb.String()), nil
	})
}

// registerGetDebugResultsTool registers the coral_get_debug_results tool.
func (s *Server) registerGetDebugResultsTool() {
	if !s.isToolEnabled("coral_get_debug_results") {
		return
	}

	inputSchema, err := generateInputSchema(GetDebugResultsInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_get_debug_results")
		return
	}

	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_get_debug_results")
		return
	}

	tool := mcp.NewToolWithRawSchema(
		"coral_get_debug_results",
		"Get aggregated results from debug session: call counts, duration percentiles, slow outliers.",
		schemaBytes,
	)

	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if s.debugService == nil {
			return mcp.NewToolResultError("debug service not available"), nil
		}

		var input GetDebugResultsInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_get_debug_results", input)

		// Call DebugService.QueryUprobeEvents (used for getting results)
		req := connect.NewRequest(&debugpb.QueryUprobeEventsRequest{
			SessionId: input.SessionID,
		})

		resp, err := s.debugService.QueryUprobeEvents(ctx, req)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to get debug results: %v", err)), nil
		}

		// Format results
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Debug Results for Session %s:\n\n", input.SessionID))
		sb.WriteString(fmt.Sprintf("Total Events: %d\n", len(resp.Msg.Events)))

		if len(resp.Msg.Events) > 0 {
			sb.WriteString("\nRecent Events:\n")
			for i, event := range resp.Msg.Events {
				if i >= 10 {
					sb.WriteString(fmt.Sprintf("... and %d more events\n", len(resp.Msg.Events)-10))
					break
				}

				duration := time.Duration(event.DurationNs) * time.Nanosecond
				sb.WriteString(fmt.Sprintf("- [%s] Duration: %s\n",
					event.Timestamp.AsTime().Format(time.RFC3339),
					duration.String()))
			}
		}

		return mcp.NewToolResultText(sb.String()), nil
	})
}
