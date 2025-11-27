package agent

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	agentv1 "github.com/coral-io/coral/coral/agent/v1"
	"github.com/coral-io/coral/coral/agent/v1/agentv1connect"
	colonyv1 "github.com/coral-io/coral/coral/colony/v1"
	"github.com/coral-io/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-io/coral/internal/config"
)

// NewExecCmd creates the exec command for container namespace execution (RFD 056).
func NewExecCmd() *cobra.Command {
	var (
		agentAddr     string
		agent         string
		colony        string
		userID        string
		containerName string
		timeout       uint32
		workingDir    string
		env           []string
		namespaces    []string
	)

	cmd := &cobra.Command{
		Use:   "exec SERVICE [command...]",
		Short: "Execute command in a service's container",
		Long: `Execute a command in a service's container using nsenter.

Targets a specific SERVICE and uses nsenter to access the service's container
filesystem. This provides access to container-mounted files, configs, and volumes.

Key differences:
  - 'coral shell SERVICE' → Runs on the AGENT HOST (agent's environment)
  - 'coral exec SERVICE'  → Runs in the SERVICE CONTAINER (via nsenter)

Use coral exec to access files that only exist in the container's view:
  - Container configs: /etc/nginx/nginx.conf, /app/config.yaml
  - Container volumes: /data, /logs, /var/lib
  - Container filesystem: /usr/share/nginx/html

All executions are fully audited with session IDs.

Examples:
  # Read nginx config from service container
  coral exec nginx cat /etc/nginx/nginx.conf

  # List files in service's data volume
  coral exec api-server ls -la /data

  # Check processes in service container (with pid namespace)
  coral exec nginx --namespaces mnt,pid ps aux

  # Execute in specific container (multi-container pods)
  coral exec web --container nginx cat /etc/nginx/nginx.conf

  # Target specific agent running the service
  coral exec nginx --agent hostname-api-1 cat /app/config.yaml

  # Set working directory in service container
  coral exec app --working-dir /app ls -la

  # Pass environment variables to service
  coral exec api --env DEBUG=true env

  # Longer timeout for slow commands
  coral exec logs-processor --timeout 60 find /data -name "*.log"

Requirements:
  - Agent must have CAP_SYS_ADMIN and CAP_SYS_PTRACE capabilities
  - Agent must share PID namespace with container (sidecar) or use hostPID (node agent)
  - nsenter binary must be available in agent container`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			// Parse service name and command from args.
			var serviceName string
			var command []string

			if len(args) == 0 {
				return fmt.Errorf("service name required\n\nUsage: coral exec SERVICE <command> [args...]\n\nExamples:\n  coral exec nginx cat /etc/nginx/nginx.conf\n  coral exec api-server ls -la /app")
			}

			serviceName = args[0]
			if len(args) < 2 {
				return fmt.Errorf("command required\n\nUsage: coral exec SERVICE <command> [args...]\n\nExamples:\n  coral exec nginx cat /etc/nginx/nginx.conf\n  coral exec api-server ls -la /app")
			}
			command = args[1:]

			// RFD 044: Agent ID resolution via colony registry.
			// If --agent is specified, use it to target specific agent.
			// Otherwise, resolve service name to agent via colony.
			if agent != "" {
				if agentAddr != "" {
					return fmt.Errorf("cannot specify both --agent and --agent-addr")
				}

				resolvedAddr, err := resolveAgentID(ctx, agent, colony)
				if err != nil {
					return fmt.Errorf("failed to resolve agent ID: %w", err)
				}
				agentAddr = resolvedAddr
			} else if agentAddr == "" {
				// Resolve service name to agent address via colony.
				resolvedAddr, err := resolveServiceToAgent(ctx, serviceName, colony)
				if err != nil {
					return fmt.Errorf("failed to resolve service '%s': %w\n\nTip: Use --agent <agent-id> to target a specific agent", serviceName, err)
				}
				agentAddr = resolvedAddr
			}

			// Execute container command.
			return runContainerExecution(ctx, agentAddr, userID, containerName, command, timeout, workingDir, env, namespaces)
		},
	}

	cmd.Flags().StringVar(&agentAddr, "agent-addr", "", "Agent address (default: auto-discover)")
	cmd.Flags().StringVar(&agent, "agent", "", "Agent ID (resolves via colony registry)")
	cmd.Flags().StringVar(&colony, "colony", "", "Colony ID (default: auto-detect)")
	cmd.Flags().StringVar(&userID, "user-id", "", "User ID for audit (default: $USER)")
	cmd.Flags().StringVar(&containerName, "container", "", "Container name (optional in sidecar mode)")
	cmd.Flags().Uint32Var(&timeout, "timeout", 30, "Timeout in seconds (max 300)")
	cmd.Flags().StringVar(&workingDir, "working-dir", "", "Working directory in container")
	cmd.Flags().StringArrayVar(&env, "env", nil, "Environment variables (KEY=VALUE)")
	cmd.Flags().StringSliceVar(&namespaces, "namespaces", []string{"mnt"}, "Namespaces to enter (mnt,pid,net,ipc,uts,cgroup)")

	return cmd
}

// runContainerExecution executes a command in a container's namespace (RFD 056).
func runContainerExecution(
	ctx context.Context,
	agentAddr, userID, containerName string,
	command []string,
	timeout uint32,
	workingDir string,
	envVars []string,
	namespaces []string,
) error {
	// Get current user if not specified.
	if userID == "" {
		userID = os.Getenv("USER")
		if userID == "" {
			userID = "unknown"
		}
	}

	// Validate timeout.
	if timeout == 0 {
		timeout = 30
	}
	if timeout > 300 {
		timeout = 300
	}

	// Parse environment variables.
	envMap := make(map[string]string)
	for _, e := range envVars {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid environment variable format: %s (expected KEY=VALUE)", e)
		}
		envMap[parts[0]] = parts[1]
	}

	// Normalize agent address.
	normalizedAddr := agentAddr
	if strings.HasPrefix(agentAddr, "http://") {
		normalizedAddr = strings.TrimPrefix(agentAddr, "http://")
	} else if strings.HasPrefix(agentAddr, "https://") {
		normalizedAddr = strings.TrimPrefix(agentAddr, "https://")
	}

	// Create HTTP client.
	httpClient := &http.Client{
		Timeout: time.Duration(timeout+5) * time.Second,
	}
	client := agentv1connect.NewAgentServiceClient(
		httpClient,
		fmt.Sprintf("http://%s", normalizedAddr),
	)

	// Prepare request.
	req := &agentv1.ContainerExecRequest{
		Command:        command,
		UserId:         userID,
		TimeoutSeconds: timeout,
	}

	if containerName != "" {
		req.ContainerName = containerName
	}

	if workingDir != "" {
		req.WorkingDir = workingDir
	}

	if len(envMap) > 0 {
		req.Env = envMap
	}

	if len(namespaces) > 0 {
		req.Namespaces = namespaces
	}

	// Execute command with timeout.
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout+5)*time.Second)
	defer cancel()

	resp, err := client.ContainerExec(execCtx, connect.NewRequest(req))
	if err != nil {
		return fmt.Errorf("failed to execute command in container: %w", err)
	}

	// Show container metadata if verbose.
	if os.Getenv("CORAL_VERBOSE") != "" {
		fmt.Fprintf(os.Stderr, "Container PID: %d\n", resp.Msg.ContainerPid)
		fmt.Fprintf(os.Stderr, "Namespaces: %s\n", strings.Join(resp.Msg.NamespacesEntered, ", "))
		fmt.Fprintf(os.Stderr, "Duration: %dms\n", resp.Msg.DurationMs)
		fmt.Fprintf(os.Stderr, "Session: %s\n\n", resp.Msg.SessionId)
	}

	// Write stdout.
	if len(resp.Msg.Stdout) > 0 {
		if _, err := os.Stdout.Write(resp.Msg.Stdout); err != nil {
			return fmt.Errorf("failed to write stdout: %w", err)
		}
	}

	// Write stderr.
	if len(resp.Msg.Stderr) > 0 {
		if _, err := os.Stderr.Write(resp.Msg.Stderr); err != nil {
			return fmt.Errorf("failed to write stderr: %w", err)
		}
	}

	// Show error if present.
	if resp.Msg.Error != "" {
		fmt.Fprintf(os.Stderr, "\nError: %s\n", resp.Msg.Error)
	}

	// Exit with command's exit code.
	if resp.Msg.ExitCode != 0 {
		os.Exit(int(resp.Msg.ExitCode))
	}

	return nil
}

// resolveServiceToAgent resolves a service name to agent mesh IP:port via colony registry.
// This enables targeting services by name instead of requiring manual agent ID lookup.
func resolveServiceToAgent(ctx context.Context, serviceName, colonyID string) (string, error) {
	// Create config resolver.
	resolver, err := config.NewResolver()
	if err != nil {
		return "", fmt.Errorf("failed to create config resolver: %w", err)
	}

	// Resolve colony ID if not specified.
	if colonyID == "" {
		colonyID, err = resolver.ResolveColonyID()
		if err != nil {
			return "", fmt.Errorf("failed to resolve colony: %w\n\nRun 'coral init <app-name>' to create a colony", err)
		}
	}

	// Load colony configuration.
	loader := resolver.GetLoader()
	colonyConfig, err := loader.LoadColonyConfig(colonyID)
	if err != nil {
		return "", fmt.Errorf("failed to load colony config: %w", err)
	}

	// Get connect port.
	connectPort := colonyConfig.Services.ConnectPort
	if connectPort == 0 {
		connectPort = 9000
	}

	// Create RPC client - try localhost first, then mesh IP.
	baseURL := fmt.Sprintf("http://localhost:%d", connectPort)
	client := colonyv1connect.NewColonyServiceClient(http.DefaultClient, baseURL)

	// Call ListAgents RPC with timeout.
	ctxWithTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req := connect.NewRequest(&colonyv1.ListAgentsRequest{})
	resp, err := client.ListAgents(ctxWithTimeout, req)
	if err != nil {
		// Try mesh IP as fallback.
		meshIP := colonyConfig.WireGuard.MeshIPv4
		if meshIP == "" {
			meshIP = "10.42.0.1"
		}
		baseURL = fmt.Sprintf("http://%s:%d", meshIP, connectPort)
		client = colonyv1connect.NewColonyServiceClient(http.DefaultClient, baseURL)

		ctxWithTimeout2, cancel2 := context.WithTimeout(ctx, 5*time.Second)
		defer cancel2()

		resp, err = client.ListAgents(ctxWithTimeout2, connect.NewRequest(&colonyv1.ListAgentsRequest{}))
		if err != nil {
			return "", fmt.Errorf("failed to query colony (is colony running?): %w", err)
		}
	}

	// Find agent with matching service name.
	// This will fail until the issue in ./issues/resolve-agent-fallback-to-name-field.md is fixed.
	for _, agent := range resp.Msg.Agents {
		// Check if any of the agent's services match the service name.
		for _, svc := range agent.Services {
			if svc.Name == serviceName {
				// Return mesh IP with agent port (default: 9001).
				return fmt.Sprintf("%s:9001", agent.MeshIpv4), nil
			}
		}
		// Fallback: Check deprecated ComponentName field for backward compatibility.
		if agent.ComponentName == serviceName {
			return fmt.Sprintf("%s:9001", agent.MeshIpv4), nil
		}
	}

	return "", fmt.Errorf("service not found: %s\n\nAvailable services:\n%s", serviceName, formatAvailableServices(resp.Msg.Agents))
}

// formatAvailableServices formats the list of available services for error messages.
func formatAvailableServices(agents []*colonyv1.Agent) string {
	if len(agents) == 0 {
		return "  (no services connected)"
	}

	var result strings.Builder
	seen := make(map[string]bool)
	for _, agent := range agents {
		for _, svc := range agent.Services {
			if !seen[svc.Name] {
				result.WriteString(fmt.Sprintf("  - %s (agent: %s, mesh IP: %s)\n", svc.Name, agent.AgentId, agent.MeshIpv4))
				seen[svc.Name] = true
			}
		}
		// Include deprecated ComponentName field.
		if agent.ComponentName != "" && !seen[agent.ComponentName] {
			result.WriteString(fmt.Sprintf("  - %s (agent: %s, mesh IP: %s)\n", agent.ComponentName, agent.AgentId, agent.MeshIpv4))
			seen[agent.ComponentName] = true
		}
	}
	if result.Len() == 0 {
		return "  (no services found)"
	}
	return result.String()
}
