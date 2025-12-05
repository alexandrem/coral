package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	"github.com/coral-mesh/coral/internal/colony/registry"
)

// Tool execution wrappers for direct RPC calls.
// These methods parse JSON arguments and execute tool logic,
// enabling the test-tool CLI command and direct RPC access.

// executeShellExecTool executes coral_shell_exec (RFD 045).
func (s *Server) executeShellExecTool(ctx context.Context, argumentsJSON string) (string, error) {
	var input ShellExecInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_shell_exec", input)

	// Validate command.
	if len(input.Command) == 0 {
		return "", fmt.Errorf("command cannot be empty")
	}

	// Resolve target agent (RFD 044: agent ID or service name with disambiguation).
	agent, err := s.resolveAgent(input.AgentID, input.Service)
	if err != nil {
		return "", err
	}

	// Validate agent status.
	status := registry.DetermineStatus(agent.LastSeen, time.Now())
	if status == registry.StatusUnhealthy {
		return "", fmt.Errorf("agent %s is unhealthy (last seen %s ago) - command execution may fail",
			agent.AgentID, formatDuration(time.Since(agent.LastSeen)))
	}

	// Create gRPC client to agent.
	agentURL := fmt.Sprintf("http://%s:9001", agent.MeshIPv4)
	client := agentv1connect.NewAgentServiceClient(http.DefaultClient, agentURL)

	// Prepare request.
	timeout := uint32(30)
	if input.TimeoutSeconds != nil {
		timeout = *input.TimeoutSeconds
		if timeout > 300 {
			timeout = 300
		}
	}

	req := &agentv1.ShellExecRequest{
		Command:        input.Command,
		UserId:         "mcp-server", // TODO: Get from MCP context
		TimeoutSeconds: timeout,
	}

	if input.WorkingDir != nil {
		req.WorkingDir = *input.WorkingDir
	}

	if input.Env != nil {
		req.Env = input.Env
	}

	// Execute command with timeout.
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout+5)*time.Second)
	defer cancel()

	resp, err := client.ShellExec(execCtx, connect.NewRequest(req))
	if err != nil {
		return "", fmt.Errorf("failed to execute command on agent %s: %w", agent.AgentID, err)
	}

	// Format response.
	return formatShellExecResponse(agent, resp.Msg), nil
}

// executeContainerExecTool executes coral_container_exec (RFD 056).
func (s *Server) executeContainerExecTool(ctx context.Context, argumentsJSON string) (string, error) {
	var input ContainerExecInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	s.auditToolCall("coral_container_exec", input)

	// Validate command.
	if len(input.Command) == 0 {
		return "", fmt.Errorf("command cannot be empty")
	}

	// Resolve target agent (RFD 044: agent ID or service name with disambiguation).
	agent, err := s.resolveAgent(input.AgentID, input.Service)
	if err != nil {
		return "", err
	}

	// Validate agent status.
	status := registry.DetermineStatus(agent.LastSeen, time.Now())
	if status == registry.StatusUnhealthy {
		return "", fmt.Errorf("agent %s is unhealthy (last seen %s ago) - command execution may fail",
			agent.AgentID, formatDuration(time.Since(agent.LastSeen)))
	}

	// Create gRPC client to agent.
	agentURL := fmt.Sprintf("http://%s:9001", agent.MeshIPv4)
	client := agentv1connect.NewAgentServiceClient(http.DefaultClient, agentURL)

	// Prepare request.
	timeout := uint32(30)
	if input.TimeoutSeconds != nil {
		timeout = *input.TimeoutSeconds
		if timeout > 300 {
			timeout = 300
		}
	}

	req := &agentv1.ContainerExecRequest{
		Command:        input.Command,
		UserId:         "mcp-server", // TODO: Get from MCP context
		TimeoutSeconds: timeout,
	}

	if input.ContainerName != nil {
		req.ContainerName = *input.ContainerName
	}

	if input.WorkingDir != nil {
		req.WorkingDir = *input.WorkingDir
	}

	if input.Env != nil {
		req.Env = input.Env
	}

	if len(input.Namespaces) > 0 {
		req.Namespaces = input.Namespaces
	}

	// Execute command with timeout.
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout+5)*time.Second)
	defer cancel()

	resp, err := client.ContainerExec(execCtx, connect.NewRequest(req))
	if err != nil {
		return "", fmt.Errorf("failed to execute command in container on agent %s: %w", agent.AgentID, err)
	}

	// Format response.
	return formatContainerExecResponse(agent, resp.Msg), nil
}

// Helper functions for agent resolution and disambiguation (RFD 044).

// resolveAgent resolves an agent by either agent ID or service name.
// If agent_id is specified, it takes precedence and must match exactly one agent.
// If only service is specified, it filters by service name and requires unique match.
// Returns error with agent ID list if multiple agents match the service.
func (s *Server) resolveAgent(agentID *string, serviceName string) (*registry.Entry, error) {
	// Agent ID lookup takes precedence (unambiguous).
	if agentID != nil && *agentID != "" {
		agent, err := s.registry.Get(*agentID)
		if err != nil {
			return nil, fmt.Errorf("agent not found: %s", *agentID)
		}
		return agent, nil
	}

	// Fallback: Service-based lookup (with disambiguation).
	agents := s.registry.ListAll()
	var matchedAgents []*registry.Entry

	for _, agent := range agents {
		// Check Services[] array, not ComponentName (RFD 044).
		for _, svc := range agent.Services {
			if matchesPattern(svc.Name, serviceName) {
				matchedAgents = append(matchedAgents, agent)
				break
			}
		}
	}

	if len(matchedAgents) == 0 {
		return nil, fmt.Errorf("no agents found for service '%s'", serviceName)
	}

	// Disambiguation requirement (RFD 044).
	if len(matchedAgents) > 1 {
		var agentIDs []string
		for _, a := range matchedAgents {
			agentIDs = append(agentIDs, a.AgentID)
		}
		return nil, fmt.Errorf(
			"multiple agents found for service '%s': %s\nPlease specify agent_id parameter to disambiguate",
			serviceName,
			strings.Join(agentIDs, ", "),
		)
	}

	return matchedAgents[0], nil
}

// formatShellExecResponse formats the response for coral_shell_exec tool (RFD 045).
// Returns formatted command output with exit code and execution details.
func formatShellExecResponse(agent *registry.Entry, resp *agentv1.ShellExecResponse) string {
	// Build service names list.
	serviceNames := make([]string, 0, len(agent.Services))
	for _, svc := range agent.Services {
		serviceNames = append(serviceNames, svc.Name)
	}
	servicesStr := strings.Join(serviceNames, ", ")
	if servicesStr == "" {
		servicesStr = agent.Name
	}

	var text string

	// Header with execution details.
	text += fmt.Sprintf("Command executed on agent %s (%s)\n", agent.AgentID, servicesStr)
	text += fmt.Sprintf("Duration: %dms | Exit Code: %d | Session: %s\n\n",
		resp.DurationMs, resp.ExitCode, resp.SessionId)

	// Show error if present.
	if resp.Error != "" {
		text += fmt.Sprintf("❌ Error: %s\n\n", resp.Error)
	}

	// Show stdout.
	if len(resp.Stdout) > 0 {
		text += "STDOUT:\n"
		text += "```\n"
		text += string(resp.Stdout)
		text += "\n```\n\n"
	} else {
		text += "STDOUT: (empty)\n\n"
	}

	// Show stderr if present.
	if len(resp.Stderr) > 0 {
		text += "STDERR:\n"
		text += "```\n"
		text += string(resp.Stderr)
		text += "\n```\n\n"
	}

	// Add status summary.
	if resp.ExitCode == 0 && resp.Error == "" {
		text += "✅ Command completed successfully\n"
	} else if resp.ExitCode != 0 {
		text += fmt.Sprintf("⚠️  Command exited with non-zero code: %d\n", resp.ExitCode)
	}

	return text
}

// formatContainerExecResponse formats the response for coral_container_exec tool (RFD 056).
// Returns formatted command output with exit code, container PID, and execution details.
func formatContainerExecResponse(agent *registry.Entry, resp *agentv1.ContainerExecResponse) string {
	// Build service names list.
	serviceNames := make([]string, 0, len(agent.Services))
	for _, svc := range agent.Services {
		serviceNames = append(serviceNames, svc.Name)
	}
	servicesStr := strings.Join(serviceNames, ", ")
	if servicesStr == "" {
		servicesStr = agent.Name
	}

	var text string

	// Header with execution details.
	text += fmt.Sprintf("Command executed in container namespace on agent %s (%s)\n", agent.AgentID, servicesStr)
	text += fmt.Sprintf("Container PID: %d | Namespaces: %s\n",
		resp.ContainerPid, strings.Join(resp.NamespacesEntered, ", "))
	text += fmt.Sprintf("Duration: %dms | Exit Code: %d | Session: %s\n\n",
		resp.DurationMs, resp.ExitCode, resp.SessionId)

	// Show error if present.
	if resp.Error != "" {
		text += fmt.Sprintf("❌ Error: %s\n\n", resp.Error)
	}

	// Show stdout.
	if len(resp.Stdout) > 0 {
		text += "STDOUT:\n"
		text += "```\n"
		text += string(resp.Stdout)
		text += "\n```\n\n"
	} else {
		text += "STDOUT: (empty)\n\n"
	}

	// Show stderr if present.
	if len(resp.Stderr) > 0 {
		text += "STDERR:\n"
		text += "```\n"
		text += string(resp.Stderr)
		text += "\n```\n\n"
	}

	// Add status summary.
	if resp.ExitCode == 0 && resp.Error == "" {
		text += "✅ Command completed successfully\n"
	} else if resp.ExitCode != 0 {
		text += fmt.Sprintf("⚠️  Command exited with non-zero code: %d\n", resp.ExitCode)
	}

	return text
}
