package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/mark3labs/mcp-go/mcp"
	"google.golang.org/protobuf/types/known/durationpb"

	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
)

// registerShellExecTool registers the coral_shell_exec tool (RFD 045).
func (s *Server) registerShellExecTool() {
	if !s.isToolEnabled("coral_shell_exec") {
		return
	}

	inputSchema, err := generateInputSchema(ShellExecInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_shell_exec")
		return
	}

	// Marshal schema to JSON bytes for MCP tool.
	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_shell_exec")
		return
	}

	// Create MCP tool with raw schema.
	tool := mcp.NewToolWithRawSchema(
		"coral_shell_exec",
		"Execute a one-off command in the agent's host environment. Returns stdout, stderr, and exit code. Command runs with 30s timeout (max 300s). Use for diagnostic commands like 'ps aux', 'ss -tlnp', 'tcpdump -c 10'.",
		schemaBytes,
	)

	// Register tool handler with MCP server.
	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Parse arguments from MCP request.
		var input ShellExecInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		// Convert to JSON and call execute method.
		argumentsJSON, err := json.Marshal(input)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal input: %v", err)), nil
		}

		result, err := s.executeShellExecTool(ctx, string(argumentsJSON))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(result), nil
	})
}

// registerAttachUprobeTool registers the coral_attach_uprobe tool.
func (s *Server) registerAttachUprobeTool() {
	if !s.isToolEnabled("coral_attach_uprobe") {
		return
	}

	inputSchema, err := generateInputSchema(AttachUprobeInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_attach_uprobe")
		return
	}

	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_attach_uprobe")
		return
	}

	tool := mcp.NewToolWithRawSchema(
		"coral_attach_uprobe",
		"Attach eBPF uprobe to application function for live debugging. Captures entry/exit events, measures duration. Time-limited and production-safe.",
		schemaBytes,
	)

	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var input AttachUprobeInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_attach_uprobe", input)

		// Parse duration
		duration := 60 * time.Second
		if input.Duration != nil {
			d, err := time.ParseDuration(*input.Duration)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid duration format: %v", err)), nil
			}
			duration = d
		}

		// Handle optional fields
		var agentID, sdkAddr string
		if input.AgentID != nil {
			agentID = *input.AgentID
		}
		if input.SDKAddr != nil {
			sdkAddr = *input.SDKAddr
		}

		// Call DebugService.AttachUprobe
		req := connect.NewRequest(&debugpb.AttachUprobeRequest{
			ServiceName:  input.Service,
			FunctionName: input.Function,
			AgentId:      agentID,
			SdkAddr:      sdkAddr,
			Duration:     durationpb.New(duration),
		})

		resp, err := s.debugService.AttachUprobe(ctx, req)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to attach uprobe: %v", err)), nil
		}

		if !resp.Msg.Success {
			return mcp.NewToolResultError(fmt.Sprintf("failed to attach uprobe: %s", resp.Msg.Error)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Debug session started for %s/%s\nSession ID: %s\nExpires At: %s",
			input.Service, input.Function, resp.Msg.SessionId, resp.Msg.ExpiresAt.AsTime().Format(time.RFC3339))), nil
	})
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

// registerDetachUprobeTool registers the coral_detach_uprobe tool.
func (s *Server) registerDetachUprobeTool() {
	if !s.isToolEnabled("coral_detach_uprobe") {
		return
	}

	inputSchema, err := generateInputSchema(DetachUprobeInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_detach_uprobe")
		return
	}

	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_detach_uprobe")
		return
	}

	tool := mcp.NewToolWithRawSchema(
		"coral_detach_uprobe",
		"Stop debug session early and detach eBPF probes. Returns collected data summary.",
		schemaBytes,
	)

	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var input DetachUprobeInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_detach_uprobe", input)

		// Call DebugService.DetachUprobe
		req := connect.NewRequest(&debugpb.DetachUprobeRequest{
			SessionId: input.SessionID,
		})

		resp, err := s.debugService.DetachUprobe(ctx, req)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to detach uprobe: %v", err)), nil
		}

		if !resp.Msg.Success {
			return mcp.NewToolResultError(fmt.Sprintf("failed to detach uprobe: %s", resp.Msg.Error)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Session %s detached successfully.", input.SessionID)), nil
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

// registerSearchFunctionsTool registers the coral_search_functions tool.
func (s *Server) registerSearchFunctionsTool() {
	if !s.isToolEnabled("coral_search_functions") {
		return
	}

	inputSchema, err := generateInputSchema(SearchFunctionsInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_search_functions")
		return
	}

	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_search_functions")
		return
	}

	tool := mcp.NewToolWithRawSchema(
		"coral_search_functions",
		"Semantic search for functions by keywords. Searches function names, file paths, and comments. Returns ranked results. Prefer this over list_probeable_functions for discovery.",
		schemaBytes,
	)

	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var input SearchFunctionsInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_search_functions", input)

		// TODO: Implement semantic search (RFD 063)
		return mcp.NewToolResultError("coral_search_functions is not yet implemented (RFD 063 - Intelligent Function Discovery)"), nil
	})
}

// registerGetFunctionContextTool registers the coral_get_function_context tool.
func (s *Server) registerGetFunctionContextTool() {
	if !s.isToolEnabled("coral_get_function_context") {
		return
	}

	inputSchema, err := generateInputSchema(GetFunctionContextInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_get_function_context")
		return
	}

	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_get_function_context")
		return
	}

	tool := mcp.NewToolWithRawSchema(
		"coral_get_function_context",
		"Get context about a function: what calls it, what it calls, recent performance metrics. Use this to navigate the call graph after discovering an entry point.",
		schemaBytes,
	)

	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var input GetFunctionContextInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_get_function_context", input)

		// TODO: Implement function context (RFD 063)
		return mcp.NewToolResultError("coral_get_function_context is not yet implemented (RFD 063 - Intelligent Function Discovery)"), nil
	})
}

// registerListProbeableFunctionsTool registers the coral_list_probeable_functions tool.
func (s *Server) registerListProbeableFunctionsTool() {
	if !s.isToolEnabled("coral_list_probeable_functions") {
		return
	}

	inputSchema, err := generateInputSchema(ListProbeableFunctionsInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_list_probeable_functions")
		return
	}

	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_list_probeable_functions")
		return
	}

	tool := mcp.NewToolWithRawSchema(
		"coral_list_probeable_functions",
		"List functions available for uprobe attachment using regex pattern. Use coral_search_functions instead for semantic search. This is a fallback for regex-based filtering.",
		schemaBytes,
	)

	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var input ListProbeableFunctionsInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_list_probeable_functions", input)

		// TODO: Implement list probeable functions (RFD 063)
		return mcp.NewToolResultError("coral_list_probeable_functions is not yet implemented (RFD 063 - Intelligent Function Discovery)"), nil
	})
}

// registerDiscoverFunctionsTool registers the coral_discover_functions tool (RFD 069).
func (s *Server) registerDiscoverFunctionsTool() {
	if !s.isToolEnabled("coral_discover_functions") {
		return
	}

	inputSchema, err := generateInputSchema(DiscoverFunctionsInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_discover_functions")
		return
	}

	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_discover_functions")
		return
	}

	tool := mcp.NewToolWithRawSchema(
		"coral_discover_functions",
		"Unified function discovery with semantic search. Replaces coral_search_functions, coral_list_probeable_functions, and coral_get_function_context. Returns functions with embedded metrics, instrumentation info, and actionable suggestions. Use this for all function discovery needs (RFD 069).",
		schemaBytes,
	)

	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var input DiscoverFunctionsInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_discover_functions", input)

		// Set defaults
		maxResults := int32(20)
		if input.MaxResults != nil {
			maxResults = *input.MaxResults
		}
		includeMetrics := true
		if input.IncludeMetrics != nil {
			includeMetrics = *input.IncludeMetrics
		}
		prioritizeSlow := false
		if input.PrioritizeSlow != nil {
			prioritizeSlow = *input.PrioritizeSlow
		}
		serviceName := ""
		if input.Service != nil {
			serviceName = *input.Service
		}

		// Call DebugService.QueryFunctions
		req := connect.NewRequest(&debugpb.QueryFunctionsRequest{
			ServiceName:    serviceName,
			Query:          input.Query,
			MaxResults:     maxResults,
			IncludeMetrics: includeMetrics,
			PrioritizeSlow: prioritizeSlow,
		})

		resp, err := s.debugService.QueryFunctions(ctx, req)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to query functions: %v", err)), nil
		}

		// Format response
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Found %d function(s) matching '%s'", len(resp.Msg.Results), input.Query))
		if serviceName != "" {
			sb.WriteString(fmt.Sprintf(" in service '%s'", serviceName))
		}
		sb.WriteString(fmt.Sprintf("\nData coverage: %d%%\n\n", resp.Msg.DataCoveragePct))

		for i, result := range resp.Msg.Results {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, result.Function.Name))
			if result.Function.Package != "" {
				sb.WriteString(fmt.Sprintf("   Package: %s\n", result.Function.Package))
			}
			if result.Function.File != "" {
				sb.WriteString(fmt.Sprintf("   Location: %s:%d\n", result.Function.File, result.Function.Line))
			}
			if result.Search != nil {
				sb.WriteString(fmt.Sprintf("   Relevance: %.2f - %s\n", result.Search.Score, result.Search.Reason))
			}
			if result.Instrumentation != nil {
				sb.WriteString(fmt.Sprintf("   Probeable: %v, Has DWARF: %v\n",
					result.Instrumentation.IsProbeable, result.Instrumentation.HasDwarf))
			}
			if result.Metrics != nil {
				sb.WriteString(fmt.Sprintf("   Metrics [%s]:\n", result.Metrics.Source))
				if result.Metrics.P95 != nil {
					sb.WriteString(fmt.Sprintf("     P95: %s\n", result.Metrics.P95.AsDuration().String()))
				}
				if result.Metrics.CallsPerMin > 0 {
					sb.WriteString(fmt.Sprintf("     Calls/min: %.1f\n", result.Metrics.CallsPerMin))
				}
			}
			if result.Suggestion != "" {
				sb.WriteString(fmt.Sprintf("   → %s\n", result.Suggestion))
			}
			sb.WriteString("\n")
		}

		if resp.Msg.Suggestion != "" {
			sb.WriteString(fmt.Sprintf("💡 %s\n", resp.Msg.Suggestion))
		}

		return mcp.NewToolResultText(sb.String()), nil
	})
}

// registerProfileFunctionsTool registers the coral_profile_functions tool (RFD 069).
func (s *Server) registerProfileFunctionsTool() {
	if !s.isToolEnabled("coral_profile_functions") {
		return
	}

	inputSchema, err := generateInputSchema(ProfileFunctionsInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_profile_functions")
		return
	}

	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_profile_functions")
		return
	}

	tool := mcp.NewToolWithRawSchema(
		"coral_profile_functions",
		"Intelligent batch profiling with automatic analysis. Discovers functions via semantic search, applies selection strategy, attaches probes to multiple functions simultaneously, waits and collects data, analyzes bottlenecks automatically, and returns actionable recommendations. Reduces 7+ tool calls to 1. Use this for performance investigation (RFD 069).",
		schemaBytes,
	)

	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var input ProfileFunctionsInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_profile_functions", input)

		// Set defaults
		strategy := "critical_path"
		if input.Strategy != nil {
			strategy = *input.Strategy
		}
		maxFunctions := int32(20)
		if input.MaxFunctions != nil {
			maxFunctions = *input.MaxFunctions
		}
		async := false
		if input.Async != nil {
			async = *input.Async
		}
		sampleRate := 1.0
		if input.SampleRate != nil {
			sampleRate = *input.SampleRate
		}

		// Parse duration
		duration := time.Duration(60 * time.Second)
		if input.Duration != nil {
			d, err := time.ParseDuration(*input.Duration)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid duration format: %v", err)), nil
			}
			duration = d
		}

		// Call DebugService.ProfileFunctions
		req := connect.NewRequest(&debugpb.ProfileFunctionsRequest{
			ServiceName:  input.Service,
			Query:        input.Query,
			Strategy:     strategy,
			MaxFunctions: maxFunctions,
			Duration:     durationpb.New(duration),
			Async:        async,
			SampleRate:   sampleRate,
		})

		resp, err := s.debugService.ProfileFunctions(ctx, req)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to profile functions: %v", err)), nil
		}

		// Format response
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Profiling Session: %s\n", resp.Msg.SessionId))
		sb.WriteString(fmt.Sprintf("Status: %s\n\n", resp.Msg.Status))

		if resp.Msg.Summary != nil {
			sb.WriteString("Summary:\n")
			sb.WriteString(fmt.Sprintf("  Functions Selected: %d\n", resp.Msg.Summary.FunctionsSelected))
			sb.WriteString(fmt.Sprintf("  Functions Probed:   %d\n", resp.Msg.Summary.FunctionsProbed))
			if resp.Msg.Summary.ProbesFailed > 0 {
				sb.WriteString(fmt.Sprintf("  Probes Failed:      %d\n", resp.Msg.Summary.ProbesFailed))
			}
			if resp.Msg.Summary.Duration != nil {
				sb.WriteString(fmt.Sprintf("  Duration:           %s\n", resp.Msg.Summary.Duration.AsDuration().String()))
			}
			sb.WriteString("\n")
		}

		if len(resp.Msg.Bottlenecks) > 0 {
			sb.WriteString("🔥 Bottlenecks Identified:\n\n")
			for i, b := range resp.Msg.Bottlenecks {
				sb.WriteString(fmt.Sprintf("%d. %s [%s]\n", i+1, b.Function, b.Severity))
				sb.WriteString(fmt.Sprintf("   P95: %s (%d%% contribution)\n",
					b.P95.AsDuration().String(), b.ContributionPct))
				sb.WriteString(fmt.Sprintf("   Impact: %s\n", b.Impact))
				sb.WriteString(fmt.Sprintf("   → %s\n\n", b.Recommendation))
			}
		}

		if resp.Msg.Recommendation != "" {
			sb.WriteString(fmt.Sprintf("💡 Recommendation: %s\n\n", resp.Msg.Recommendation))
		}

		if len(resp.Msg.NextSteps) > 0 {
			sb.WriteString("Next Steps:\n")
			for _, step := range resp.Msg.NextSteps {
				sb.WriteString(fmt.Sprintf("  • %s\n", step))
			}
		}

		return mcp.NewToolResultText(sb.String()), nil
	})
}
