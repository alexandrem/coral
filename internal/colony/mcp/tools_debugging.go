package mcp

import (
	"encoding/json"
	"fmt"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
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
		s.logger.Error().Err(err).Msg("Failed to generate input schema for tool_name")
		return
	}

	genkit.DefineToolWithInputSchema(
		s.genkit,
		"coral_start_ebpf_collector",
		"Start an on-demand eBPF collector for live debugging (CPU profiling, syscall tracing, network analysis). Collector runs for specified duration.",
		inputSchema,
		func(ctx *ai.ToolContext, input any) (string, error) {
			// Parse input
			inputBytes, err := json.Marshal(input)
			if err != nil {
				return "", fmt.Errorf("failed to marshal input: %w", err)
			}

			var typedInput StartEBPFCollectorInput
			if err := json.Unmarshal(inputBytes, &typedInput); err != nil {
				return "", fmt.Errorf("failed to unmarshal input: %w", err)
			}

			s.auditToolCall("coral_start_ebpf_collector", typedInput)

			// TODO: Implement eBPF collector startup (RFD 013).
			// This requires:
			// - Agent eBPF manager implementation (RFD 013)
			// - eBPF program catalog (cpu_profile, syscall_stats, http_latency, tcp_metrics)
			// - Colony-to-agent RPC for collector lifecycle management
			// - Data streaming from agent to colony

			text := fmt.Sprintf("eBPF Collector: %s\n\n", typedInput.CollectorType)
			text += "Status: Not yet implemented\n\n"
			text += fmt.Sprintf("Target Service: %s\n", typedInput.Service)

			if typedInput.DurationSeconds != nil {
				text += fmt.Sprintf("Duration: %d seconds\n", *typedInput.DurationSeconds)
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

			return text, nil
		},
	)
}

// registerStopEBPFCollectorTool registers the coral_stop_ebpf_collector tool.
func (s *Server) registerStopEBPFCollectorTool() {
	if !s.isToolEnabled("coral_stop_ebpf_collector") {
		return
	}

	inputSchema, err := generateInputSchema(StopEBPFCollectorInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for tool_name")
		return
	}

	genkit.DefineToolWithInputSchema(
		s.genkit,
		"coral_stop_ebpf_collector",
		"Stop a running eBPF collector before its duration expires.",
		inputSchema,
		func(ctx *ai.ToolContext, input any) (string, error) {
			// Parse input
			inputBytes, err := json.Marshal(input)
			if err != nil {
				return "", fmt.Errorf("failed to marshal input: %w", err)
			}

			var typedInput StopEBPFCollectorInput
			if err := json.Unmarshal(inputBytes, &typedInput); err != nil {
				return "", fmt.Errorf("failed to unmarshal input: %w", err)
			}

			s.auditToolCall("coral_stop_ebpf_collector", typedInput)

			// TODO: Implement eBPF collector stop (RFD 013).

			text := fmt.Sprintf("eBPF Collector Stop: %s\n\n", typedInput.CollectorID)
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

			return text, nil
		},
	)
}

// registerListEBPFCollectorsTool registers the coral_list_ebpf_collectors tool.
func (s *Server) registerListEBPFCollectorsTool() {
	if !s.isToolEnabled("coral_list_ebpf_collectors") {
		return
	}

	inputSchema, err := generateInputSchema(ListEBPFCollectorsInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for tool_name")
		return
	}

	genkit.DefineToolWithInputSchema(
		s.genkit,
		"coral_list_ebpf_collectors",
		"List currently active eBPF collectors with their status and remaining duration.",
		inputSchema,
		func(ctx *ai.ToolContext, input any) (string, error) {
			// Parse input
			inputBytes, err := json.Marshal(input)
			if err != nil {
				return "", fmt.Errorf("failed to marshal input: %w", err)
			}

			var typedInput ListEBPFCollectorsInput
			if err := json.Unmarshal(inputBytes, &typedInput); err != nil {
				return "", fmt.Errorf("failed to unmarshal input: %w", err)
			}

			s.auditToolCall("coral_list_ebpf_collectors", typedInput)

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

			return text, nil
		},
	)
}

// registerExecCommandTool registers the coral_exec_command tool.
func (s *Server) registerExecCommandTool() {
	if !s.isToolEnabled("coral_exec_command") {
		return
	}

	inputSchema, err := generateInputSchema(ExecCommandInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for tool_name")
		return
	}

	genkit.DefineToolWithInputSchema(
		s.genkit,
		"coral_exec_command",
		"Execute a command in an application container (kubectl/docker exec semantics). Useful for checking configuration, running diagnostic commands, or inspecting container state.",
		inputSchema,
		func(ctx *ai.ToolContext, input any) (string, error) {
			// Parse input
			inputBytes, err := json.Marshal(input)
			if err != nil {
				return "", fmt.Errorf("failed to marshal input: %w", err)
			}

			var typedInput ExecCommandInput
			if err := json.Unmarshal(inputBytes, &typedInput); err != nil {
				return "", fmt.Errorf("failed to unmarshal input: %w", err)
			}

			s.auditToolCall("coral_exec_command", typedInput)

			// TODO: Implement container exec (RFD 017).
			// This requires:
			// - CRI integration on agents (containerd, CRI-O, Docker)
			// - Colony-to-agent RPC for exec requests
			// - Output streaming and exit code handling
			// - Security checks and audit logging

			text := fmt.Sprintf("Container Exec: %s\n\n", typedInput.Service)
			text += "Status: Not yet implemented\n\n"
			text += fmt.Sprintf("Command: %v\n", typedInput.Command)

			if typedInput.TimeoutSeconds != nil {
				text += fmt.Sprintf("Timeout: %d seconds\n", *typedInput.TimeoutSeconds)
			} else {
				text += "Timeout: 30 seconds (default)\n"
			}

			if typedInput.WorkingDir != nil {
				text += fmt.Sprintf("Working Directory: %s\n", *typedInput.WorkingDir)
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

			return text, nil
		},
	)
}

// registerShellStartTool registers the coral_shell_start tool.
func (s *Server) registerShellStartTool() {
	if !s.isToolEnabled("coral_shell_start") {
		return
	}

	inputSchema, err := generateInputSchema(ShellStartInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for tool_name")
		return
	}

	genkit.DefineToolWithInputSchema(
		s.genkit,
		"coral_shell_start",
		"Start an interactive debug shell in the agent's environment (not the application container). Provides access to debugging tools (tcpdump, netcat, curl) and agent's data. Returns session ID for audit.",
		inputSchema,
		func(ctx *ai.ToolContext, input any) (string, error) {
			// Parse input
			inputBytes, err := json.Marshal(input)
			if err != nil {
				return "", fmt.Errorf("failed to marshal input: %w", err)
			}

			var typedInput ShellStartInput
			if err := json.Unmarshal(inputBytes, &typedInput); err != nil {
				return "", fmt.Errorf("failed to unmarshal input: %w", err)
			}

			s.auditToolCall("coral_shell_start", typedInput)

			// TODO: Implement agent shell access (RFD 026).
			// This requires:
			// - Agent shell server implementation
			// - TTY allocation and terminal handling
			// - Session management and audit logging
			// - Colony-to-agent RPC for shell sessions
			// - Security controls (RBAC, session recording)

			text := fmt.Sprintf("Agent Debug Shell: %s\n\n", typedInput.Service)
			text += "Status: Not yet implemented\n\n"

			shell := "/bin/bash"
			if typedInput.Shell != nil {
				shell = *typedInput.Shell
			}
			text += fmt.Sprintf("Shell: %s\n", shell)

			text += "\n"
			text += "Implementation Status:\n"
			text += "  - RFD 026 (shell command) is in draft status\n"
			text += "  - Agent shell server is not yet implemented\n"
			text += "  - TTY handling and session management are pending\n"
			text += "\n"
			text += "Once implemented, this tool will:\n"
			text += "  1. Locate target agent via registry\n"
			text += "  2. Start interactive shell in agent's container\n"
			text += "  3. Provide access to debugging utilities (tcpdump, netcat, curl, etc.)\n"
			text += "  4. Enable network debugging from agent's perspective\n"
			text += "  5. Allow querying agent's local DuckDB for raw telemetry\n"
			text += "  6. Record full session for audit (elevated privileges)\n"
			text += "\n"
			text += "Security Note:\n"
			text += "  - Agent shells have elevated privileges (CRI socket access, host network)\n"
			text += "  - All sessions will be fully recorded for audit compliance\n"
			text += "  - RBAC checks will be enforced before allowing access\n"

			return text, nil
		},
	)
}
