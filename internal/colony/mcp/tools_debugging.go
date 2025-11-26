package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// Phase 3: Live Debugging Tools
// These tools enable on-demand debugging actions like eBPF profiling,
// container exec, and interactive shells.

// registerStartEBPFCollectorTool registers the coral_start_ebpf_collector tool.
func (s *Server) registerStartEBPFCollectorTool() {
	if !s.isToolEnabled("coral_start_ebpf_collector") {
		return
	}

	inputSchema, err := generateInputSchema(StartEBPFCollectorInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_start_ebpf_collector")
		return
	}

	// Marshal schema to JSON bytes for MCP tool.
	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_start_ebpf_collector")
		return
	}

	// Create MCP tool with raw schema.
	tool := mcp.NewToolWithRawSchema(
		"coral_start_ebpf_collector",
		"Start an on-demand eBPF collector for live debugging (CPU profiling, syscall tracing, network analysis). Collector runs for specified duration.",
		schemaBytes,
	)

	// Register tool handler with MCP server.
	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Parse arguments from MCP request.
		var input StartEBPFCollectorInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_start_ebpf_collector", input)

		// TODO: Implement eBPF collector startup (RFD 013).
		// This requires:
		// - Agent eBPF manager implementation (RFD 013)
		// - eBPF program catalog (cpu_profile, syscall_stats, http_latency, tcp_metrics)
		// - Colony-to-agent RPC for collector lifecycle management
		// - Data streaming from agent to colony

		text := fmt.Sprintf("eBPF Collector: %s\n\n", input.CollectorType)
		text += "Status: Not yet implemented\n\n"
		text += fmt.Sprintf("Target Service: %s\n", input.Service)

		if input.DurationSeconds != nil {
			text += fmt.Sprintf("Duration: %d seconds\n", *input.DurationSeconds)
		} else {
			text += "Duration: 30 seconds (default)\n"
		}

		text += "\n"
		text += "Implementation Status:\n"
		text += "  - RFD 013 (eBPF framework) is in partial implementation\n"
		text += "  - Agent eBPF manager has capability detection but no real collectors yet\n"
		text += "  - Colony integration and data storage are pending\n"
		text += "\n"
		text += "Once implemented, this tool will:\n"
		text += "  1. Start eBPF collector on target agent\n"
		text += "  2. Collect profiling data for specified duration\n"
		text += "  3. Stream results to colony for analysis\n"
		text += "  4. Return collector ID for querying results\n"

		return mcp.NewToolResultText(text), nil
	})
}

// registerStopEBPFCollectorTool registers the coral_stop_ebpf_collector tool.
func (s *Server) registerStopEBPFCollectorTool() {
	if !s.isToolEnabled("coral_stop_ebpf_collector") {
		return
	}

	inputSchema, err := generateInputSchema(StopEBPFCollectorInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_stop_ebpf_collector")
		return
	}

	// Marshal schema to JSON bytes for MCP tool.
	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_stop_ebpf_collector")
		return
	}

	// Create MCP tool with raw schema.
	tool := mcp.NewToolWithRawSchema(
		"coral_stop_ebpf_collector",
		"Stop a running eBPF collector before its duration expires.",
		schemaBytes,
	)

	// Register tool handler with MCP server.
	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Parse arguments from MCP request.
		var input StopEBPFCollectorInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_stop_ebpf_collector", input)

		// TODO: Implement eBPF collector stop (RFD 013).

		text := fmt.Sprintf("eBPF Collector Stop: %s\n\n", input.CollectorID)
		text += "Status: Not yet implemented\n\n"
		text += "Implementation Status:\n"
		text += "  - RFD 013 (eBPF framework) is in partial implementation\n"
		text += "  - Collector lifecycle management is pending\n"
		text += "\n"
		text += "Once implemented, this tool will:\n"
		text += "  1. Send stop signal to running eBPF collector\n"
		text += "  2. Flush remaining data to colony\n"
		text += "  3. Clean up eBPF programs and maps\n"
		text += "  4. Return final collector status and data summary\n"

		return mcp.NewToolResultText(text), nil
	})
}

// registerListEBPFCollectorsTool registers the coral_list_ebpf_collectors tool.
func (s *Server) registerListEBPFCollectorsTool() {
	if !s.isToolEnabled("coral_list_ebpf_collectors") {
		return
	}

	inputSchema, err := generateInputSchema(ListEBPFCollectorsInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_list_ebpf_collectors")
		return
	}

	// Marshal schema to JSON bytes for MCP tool.
	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_list_ebpf_collectors")
		return
	}

	// Create MCP tool with raw schema.
	tool := mcp.NewToolWithRawSchema(
		"coral_list_ebpf_collectors",
		"List currently active eBPF collectors with their status and remaining duration.",
		schemaBytes,
	)

	// Register tool handler with MCP server.
	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Parse arguments from MCP request.
		var input ListEBPFCollectorsInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_list_ebpf_collectors", input)

		// TODO: Implement eBPF collector listing (RFD 013).

		text := "Active eBPF Collectors:\n\n"
		text += "No active collectors.\n\n"
		text += "Implementation Status:\n"
		text += "  - RFD 013 (eBPF framework) is in partial implementation\n"
		text += "  - Collector registry and status tracking are pending\n"
		text += "\n"
		text += "Once implemented, this tool will show:\n"
		text += "  - Collector ID and type (cpu_profile, syscall_stats, etc.)\n"
		text += "  - Target service name\n"
		text += "  - Start time and remaining duration\n"
		text += "  - Data collection status (active, stopping, completed)\n"
		text += "  - Samples collected so far\n"

		return mcp.NewToolResultText(text), nil
	})
}

// registerExecCommandTool registers the coral_exec_command tool.
func (s *Server) registerExecCommandTool() {
	if !s.isToolEnabled("coral_exec_command") {
		return
	}

	inputSchema, err := generateInputSchema(ExecCommandInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_exec_command")
		return
	}

	// Marshal schema to JSON bytes for MCP tool.
	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_exec_command")
		return
	}

	// Create MCP tool with raw schema.
	tool := mcp.NewToolWithRawSchema(
		"coral_exec_command",
		"Execute a command in an application container (kubectl/docker exec semantics). Useful for checking configuration, running diagnostic commands, or inspecting container state.",
		schemaBytes,
	)

	// Register tool handler with MCP server.
	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Parse arguments from MCP request.
		var input ExecCommandInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		s.auditToolCall("coral_exec_command", input)

		// TODO: Implement container exec (RFD 017).
		// This requires:
		// - CRI integration on agents (containerd, CRI-O, Docker)
		// - Colony-to-agent RPC for exec requests
		// - Output streaming and exit code handling
		// - Security checks and audit logging

		text := fmt.Sprintf("Container Exec: %s\n\n", input.Service)
		text += "Status: Not yet implemented\n\n"
		text += fmt.Sprintf("Command: %v\n", input.Command)

		if input.TimeoutSeconds != nil {
			text += fmt.Sprintf("Timeout: %d seconds\n", *input.TimeoutSeconds)
		} else {
			text += "Timeout: 30 seconds (default)\n"
		}

		if input.WorkingDir != nil {
			text += fmt.Sprintf("Working Directory: %s\n", *input.WorkingDir)
		}

		text += "\n"
		text += "Implementation Status:\n"
		text += "  - RFD 017 (exec command) is in draft status\n"
		text += "  - CRI integration (containerd, CRI-O, Docker) is not yet implemented\n"
		text += "  - Colony-to-agent RPC for exec is pending\n"
		text += "\n"
		text += "Once implemented, this tool will:\n"
		text += "  1. Locate target container via agent registry\n"
		text += "  2. Use CRI to execute command in application container\n"
		text += "  3. Stream stdout/stderr back to colony\n"
		text += "  4. Return exit code and full output\n"
		text += "  5. Support both one-off commands and interactive sessions\n"

		return mcp.NewToolResultText(text), nil
	})
}

// registerShellStartTool registers the coral_shell_start tool.
func (s *Server) registerShellStartTool() {
	if !s.isToolEnabled("coral_shell_start") {
		return
	}

	inputSchema, err := generateInputSchema(ShellStartInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_shell_start")
		return
	}

	// Marshal schema to JSON bytes for MCP tool.
	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_shell_start")
		return
	}

	// Create MCP tool with raw schema.
	tool := mcp.NewToolWithRawSchema(
		"coral_shell_start",
		"Discover agent shell access information and get CLI command to connect. Returns connection details, agent status, and the coral shell command to execute for interactive access.",
		schemaBytes,
	)

	// Register tool handler with MCP server.
	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Parse arguments from MCP request.
		var input ShellStartInput
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

		result, err := s.executeShellStartTool(ctx, string(argumentsJSON))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(result), nil
	})
}
