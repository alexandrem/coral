package mcp

import (
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

	genkit.DefineTool(
		s.genkit,
		"coral_start_ebpf_collector",
		"Start an on-demand eBPF collector for live debugging (CPU profiling, syscall tracing, network analysis). Collector runs for specified duration.",
		func(ctx *ai.ToolContext, input StartEBPFCollectorInput) (string, error) {
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

			return text, nil
		},
	)
}

// registerStopEBPFCollectorTool registers the coral_stop_ebpf_collector tool.
func (s *Server) registerStopEBPFCollectorTool() {
	if !s.isToolEnabled("coral_stop_ebpf_collector") {
		return
	}

	genkit.DefineTool(
		s.genkit,
		"coral_stop_ebpf_collector",
		"Stop a running eBPF collector before its duration expires.",
		func(ctx *ai.ToolContext, input StopEBPFCollectorInput) (string, error) {
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

			return text, nil
		},
	)
}

// registerListEBPFCollectorsTool registers the coral_list_ebpf_collectors tool.
func (s *Server) registerListEBPFCollectorsTool() {
	if !s.isToolEnabled("coral_list_ebpf_collectors") {
		return
	}

	genkit.DefineTool(
		s.genkit,
		"coral_list_ebpf_collectors",
		"List currently active eBPF collectors with their status and remaining duration.",
		func(ctx *ai.ToolContext, input ListEBPFCollectorsInput) (string, error) {
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

			return text, nil
		},
	)
}

// registerExecCommandTool registers the coral_exec_command tool.
func (s *Server) registerExecCommandTool() {
	if !s.isToolEnabled("coral_exec_command") {
		return
	}

	genkit.DefineTool(
		s.genkit,
		"coral_exec_command",
		"Execute a command in an application container (kubectl/docker exec semantics). Useful for checking configuration, running diagnostic commands, or inspecting container state.",
		func(ctx *ai.ToolContext, input ExecCommandInput) (string, error) {
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

			return text, nil
		},
	)
}

// registerAgentShellExecTool registers the agent_shell_exec tool.
func (s *Server) registerAgentShellExecTool() {
	if !s.isToolEnabled("agent_shell_exec") {
		return
	}

	genkit.DefineTool(
		s.genkit,
		"agent_shell_exec",
		"Execute a command in the agent's environment (not the application container). Provides access to agent-level debugging tools (tcpdump, DuckDB queries, agent logs). Returns command output (stdout/stderr) and exit code.",
		func(ctx *ai.ToolContext, input AgentShellExecInput) (string, error) {
			s.auditToolCall("agent_shell_exec", input)

			// TODO: Implement agent command execution (RFD 045).
			// This requires:
			// - Agent resolution logic from RFD 044 (agent_id or service)
			// - Agent gRPC API call to execute command
			// - Capture stdout, stderr, exit code
			// - Timeout handling
			// - Security controls (RBAC, command auditing)

			text := fmt.Sprintf("Agent Command Execution: %s\n\n", input.Service)
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
			text += "  - RFD 045 (agent shell exec) is in draft status\n"
			text += "  - Agent gRPC API integration is pending\n"
			text += "  - RFD 044 agent resolution integration is pending\n"
			text += "\n"
			text += "Once implemented, this tool will:\n"
			text += "  1. Resolve agent via RFD 044 (agent_id or service)\n"
			text += "  2. Execute command in agent's environment via gRPC\n"
			text += "  3. Capture stdout, stderr, and exit code\n"
			text += "  4. Return formatted output with execution metadata\n"
			text += "  5. Support timeouts and working directory\n"
			text += "  6. Audit all command executions\n"
			text += "\n"
			text += "Security Note:\n"
			text += "  - Agent commands run with elevated privileges (CRI socket, eBPF, mesh access)\n"
			text += "  - All command executions are audited\n"
			text += "  - RBAC checks will be enforced (RFD 043)\n"
			text += "\n"
			text += "For interactive debugging sessions, use the 'coral shell' CLI command.\n"

			return text, nil
		},
	)
}
