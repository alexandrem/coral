package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	agentv1 "github.com/coral-io/coral/coral/agent/v1"
	"github.com/coral-io/coral/coral/agent/v1/agentv1connect"
)

// NewStatusCmd creates the agent status command.
func NewStatusCmd() *cobra.Command {
	var (
		jsonOutput bool
		agentURL   string
		agent      string
		colony     string
	)

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show agent status and runtime context",
		Long: `Display detailed information about a Coral agent.

This command shows:
- Agent platform and version information
- Runtime context (Native, Docker, K8s, etc.)
- Available capabilities (run, exec, shell, connect)
- Visibility scope and container access
- Colony connection status

Examples:
  # Show status of local agent
  coral agent status

  # Show status of specific agent by ID
  coral agent status --agent hostname-api-1

  # Show status of agent by mesh IP
  coral agent status --agent-url http://10.42.0.15:9001

  # Output in JSON format
  coral agent status --agent hostname-api-1 --json

The agent must be running and accessible.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			// RFD 044: Agent ID resolution via colony registry.
			// If --agent is specified, query colony to resolve mesh IP.
			if agent != "" {
				if agentURL != "" {
					return fmt.Errorf("cannot specify both --agent and --agent-url")
				}

				// Resolve agent ID to mesh IP via colony registry.
				resolvedAddr, err := resolveAgentID(ctx, agent, colony)
				if err != nil {
					return fmt.Errorf("failed to resolve agent ID: %w", err)
				}
				agentURL = fmt.Sprintf("http://%s", resolvedAddr)
			}

			// Default agent URL (typically on localhost)
			if agentURL == "" {
				agentURL = "http://localhost:9001"
			}

			// Create agent client
			client := agentv1connect.NewAgentServiceClient(http.DefaultClient, agentURL)

			// Query runtime context
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			req := connect.NewRequest(&agentv1.GetRuntimeContextRequest{})
			resp, err := client.GetRuntimeContext(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to get agent status: %w\n\nIs the agent running?", err)
			}

			runtimeCtx := resp.Msg

			// Query connected services (ListServices RPC).
			var services []*agentv1.ServiceStatus
			servicesResp, err := client.ListServices(ctx, connect.NewRequest(&agentv1.ListServicesRequest{}))
			if err == nil && servicesResp != nil {
				services = servicesResp.Msg.Services
			}

			// Output in requested format
			if jsonOutput {
				return outputAgentStatusJSON(runtimeCtx, services)
			}

			return outputAgentStatusTable(runtimeCtx, services)
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	cmd.Flags().StringVar(&agentURL, "agent-url", "", "Agent URL (default: http://localhost:9001)")
	cmd.Flags().StringVar(&agent, "agent", "", "Agent ID (resolves via colony registry)")
	cmd.Flags().StringVar(&colony, "colony", "", "Colony ID (default: auto-detect)")

	return cmd
}

// outputAgentStatusJSON outputs agent status in JSON format.
func outputAgentStatusJSON(ctx *agentv1.RuntimeContextResponse, services []*agentv1.ServiceStatus) error {
	output := map[string]interface{}{
		"runtime_context": ctx,
	}

	if len(services) > 0 {
		output["services"] = services
	}

	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	fmt.Println(string(data))
	return nil
}

// outputAgentStatusTable outputs agent status in human-readable format.
func outputAgentStatusTable(ctx *agentv1.RuntimeContextResponse, services []*agentv1.ServiceStatus) error {
	fmt.Println()
	fmt.Println("Agent Status")
	fmt.Println("============")
	fmt.Println()

	fmt.Printf("Agent ID:     %s\n", ctx.AgentId)
	fmt.Println()

	// Connected Services section.
	printServices(services)

	// Platform section
	fmt.Println("Platform:")
	fmt.Printf("  OS:           %s (%s)\n", ctx.Platform.Os, ctx.Platform.OsVersion)
	fmt.Printf("  Architecture: %s\n", ctx.Platform.Arch)
	fmt.Printf("  Kernel:       %s\n", ctx.Platform.Kernel)
	fmt.Printf("  Agent:        %s\n", ctx.Version)
	fmt.Println()

	// Runtime section
	fmt.Println("Runtime:")
	fmt.Printf("  Type:         %s\n", formatRuntimeType(ctx.RuntimeType))

	if ctx.SidecarMode != agentv1.SidecarMode_SIDECAR_MODE_UNKNOWN {
		fmt.Printf("  Mode:         %s\n", formatSidecarMode(ctx.SidecarMode))
	}

	if ctx.CriSocket != nil {
		fmt.Printf("  CRI Socket:   %s\n", ctx.CriSocket.Path)
		fmt.Printf("  CRI Runtime:  %s", ctx.CriSocket.Type)
		if ctx.CriSocket.Version != "" {
			fmt.Printf(" v%s", ctx.CriSocket.Version)
		}
		fmt.Println()
	}

	if ctx.DetectedAt != nil {
		fmt.Printf("  Detected:     %s\n", ctx.DetectedAt.AsTime().Format(time.RFC3339))
	}
	fmt.Println()

	// Capabilities section
	fmt.Println("Capabilities:")
	fmt.Printf("  %s coral connect       %s\n", formatCapability(ctx.Capabilities.CanConnect), "Monitor and observe")

	// RFD 057: Enhanced exec capability display
	if ctx.Capabilities.ExecCapabilities != nil {
		execCaps := ctx.Capabilities.ExecCapabilities
		execIcon := formatCapability(ctx.Capabilities.CanExec)
		execDesc := "Execute in containers"

		switch execCaps.Mode {
		case agentv1.ExecMode_EXEC_MODE_NSENTER:
			execDesc = "Execute in containers (nsenter mode - full access)"
		case agentv1.ExecMode_EXEC_MODE_CRI:
			execDesc = "Execute in containers (CRI mode - limited)"
			execIcon = "⚠️"
		case agentv1.ExecMode_EXEC_MODE_NONE:
			execDesc = "Execute in containers (not available)"
			execIcon = "❌"
		}

		fmt.Printf("  %s coral exec          %s\n", execIcon, execDesc)
		if execCaps.Mode != agentv1.ExecMode_EXEC_MODE_UNKNOWN {
			fmt.Printf("     Mode:               %s\n", formatExecMode(execCaps.Mode))
			if execCaps.Mode == agentv1.ExecMode_EXEC_MODE_NSENTER || !execCaps.MountNamespaceAccess {
				fmt.Printf("     Mount Namespace:    %s", formatCapability(execCaps.MountNamespaceAccess))
				if !execCaps.MountNamespaceAccess {
					fmt.Printf(" (requires CAP_SYS_ADMIN + CAP_SYS_PTRACE)")
				}
				fmt.Println()
			}
		}
	} else {
		fmt.Printf("  %s coral exec          %s\n", formatCapability(ctx.Capabilities.CanExec), "Execute in containers")
	}

	fmt.Printf("  %s coral shell         %s\n", formatCapability(ctx.Capabilities.CanShell), "Interactive shell")
	fmt.Printf("  %s coral run           %s\n", formatCapability(ctx.Capabilities.CanRun), "Launch new containers")
	fmt.Println()

	// RFD 057: Linux Capabilities section
	if ctx.Capabilities.LinuxCapabilities != nil {
		linuxCaps := ctx.Capabilities.LinuxCapabilities
		fmt.Println("Linux Capabilities:")
		fmt.Printf("  %s CAP_NET_ADMIN       WireGuard mesh networking\n", formatCapability(linuxCaps.CapNetAdmin))
		fmt.Printf("  %s CAP_SYS_ADMIN       Container namespace execution (coral exec)\n", formatCapability(linuxCaps.CapSysAdmin))
		fmt.Printf("  %s CAP_SYS_PTRACE      Process inspection (/proc)\n", formatCapability(linuxCaps.CapSysPtrace))
		fmt.Printf("  %s CAP_SYS_RESOURCE    eBPF memory locking\n", formatCapability(linuxCaps.CapSysResource))
		fmt.Printf("  %s CAP_BPF             Modern eBPF (kernel 5.8+)\n", formatCapability(linuxCaps.CapBpf))
		fmt.Printf("  %s CAP_PERFMON         Performance monitoring\n", formatCapability(linuxCaps.CapPerfmon))
		fmt.Println()
	}

	// eBPF capabilities section
	if ctx.EbpfCapabilities != nil {
		fmt.Println("eBPF:")
		fmt.Printf("  Supported:        %s\n", formatCapability(ctx.EbpfCapabilities.Supported))
		fmt.Printf("  Kernel:           %s\n", ctx.EbpfCapabilities.KernelVersion)
		if ctx.EbpfCapabilities.Supported {
			fmt.Printf("  BTF Available:    %s\n", formatCapability(ctx.EbpfCapabilities.BtfAvailable))
			fmt.Printf("  CAP_BPF:          %s\n", formatCapability(ctx.EbpfCapabilities.CapBpf))
			fmt.Printf("  Collectors:       %d available\n", len(ctx.EbpfCapabilities.AvailableCollectors))
		}
		fmt.Println()
	}

	// Visibility section
	fmt.Println("Visibility:")
	fmt.Printf("  Scope:            %s\n", formatVisibilityScope(ctx))
	fmt.Printf("  Namespace:        %s\n", ctx.Visibility.Namespace)

	if len(ctx.Visibility.ContainerIds) > 0 {
		fmt.Printf("  Target Containers: %d\n", len(ctx.Visibility.ContainerIds))
		for i, id := range ctx.Visibility.ContainerIds {
			if i < 5 {
				fmt.Printf("    - %s\n", truncateContainerID(id))
			}
		}
		if len(ctx.Visibility.ContainerIds) > 5 {
			fmt.Printf("    ... and %d more\n", len(ctx.Visibility.ContainerIds)-5)
		}
	}
	fmt.Println()

	// Show warnings for limited capabilities
	if ctx.SidecarMode == agentv1.SidecarMode_SIDECAR_MODE_PASSIVE {
		fmt.Println("⚠️  Warning: Agent running in PASSIVE mode")
		fmt.Println("    Limited functionality - no exec/shell support")
		fmt.Println()
		fmt.Println("    To enable full capabilities:")
		fmt.Println("    1. Mount CRI socket (recommended):")
		fmt.Println("       volumes:")
		fmt.Println("         - name: cri-sock")
		fmt.Println("           hostPath:")
		fmt.Println("             path: /var/run/containerd/containerd.sock")
		fmt.Println("       volumeMounts:")
		fmt.Println("         - name: cri-sock")
		fmt.Println("           mountPath: /var/run/containerd/containerd.sock")
		fmt.Println()
		fmt.Println("    2. Or enable shareProcessNamespace:")
		fmt.Println("       spec:")
		fmt.Println("         shareProcessNamespace: true")
		fmt.Println()
	}

	return nil
}

func printServices(services []*agentv1.ServiceStatus) {
	if len(services) > 0 {
		fmt.Println("Connected Services:")
		for _, svc := range services {
			statusIcon := ""
			switch svc.Status {
			case "healthy":
				statusIcon = "✓"
			case "unhealthy":
				statusIcon = "✗"
			case "unknown":
				statusIcon = "⚠"
			default:
				statusIcon = "?"
			}

			fmt.Printf("  %s %-20s port %d", statusIcon, svc.Name, svc.Port)
			if svc.HealthEndpoint != "" {
				fmt.Printf(" (health: %s)", svc.HealthEndpoint)
			}
			if svc.ServiceType != "" {
				fmt.Printf(" [%s]", svc.ServiceType)
			}
			fmt.Println()

			// Show error if unhealthy.
			if svc.Status == "unhealthy" && svc.Error != "" {
				fmt.Printf("    Error: %s\n", svc.Error)
			}

			// Show last check time.
			if svc.LastCheck != nil {
				elapsed := time.Since(svc.LastCheck.AsTime())
				var timingStr string
				if elapsed < time.Minute {
					timingStr = fmt.Sprintf("%ds ago", int(elapsed.Seconds()))
				} else if elapsed < time.Hour {
					timingStr = fmt.Sprintf("%dm ago", int(elapsed.Minutes()))
				} else {
					timingStr = fmt.Sprintf("%dh ago", int(elapsed.Hours()))
				}
				fmt.Printf("    Last check: %s\n", timingStr)
			}
		}
		fmt.Println()
	}
}

// formatRuntimeType formats runtime type for display.
func formatRuntimeType(rt agentv1.RuntimeContext) string {
	switch rt {
	case agentv1.RuntimeContext_RUNTIME_CONTEXT_NATIVE:
		return "Native"
	case agentv1.RuntimeContext_RUNTIME_CONTEXT_DOCKER:
		return "Docker Container"
	case agentv1.RuntimeContext_RUNTIME_CONTEXT_K8S_SIDECAR:
		return "Kubernetes Sidecar"
	case agentv1.RuntimeContext_RUNTIME_CONTEXT_K8S_DAEMONSET:
		return "Kubernetes DaemonSet"
	default:
		return "Unknown"
	}
}

// formatSidecarMode formats sidecar mode for display.
func formatSidecarMode(sm agentv1.SidecarMode) string {
	switch sm {
	case agentv1.SidecarMode_SIDECAR_MODE_CRI:
		return "CRI (recommended)"
	case agentv1.SidecarMode_SIDECAR_MODE_SHARED_NS:
		return "Shared Process Namespace"
	case agentv1.SidecarMode_SIDECAR_MODE_PASSIVE:
		return "Passive (limited)"
	default:
		return "Unknown"
	}
}

// formatCapability formats capability status.
func formatCapability(supported bool) string {
	if supported {
		return "✅"
	}
	return "❌"
}

// formatExecMode formats exec mode for display (RFD 057).
func formatExecMode(mode agentv1.ExecMode) string {
	switch mode {
	case agentv1.ExecMode_EXEC_MODE_NSENTER:
		return "nsenter (full container filesystem access)"
	case agentv1.ExecMode_EXEC_MODE_CRI:
		return "CRI (limited - no mount namespace access)"
	case agentv1.ExecMode_EXEC_MODE_NONE:
		return "none (exec not available)"
	default:
		return "unknown"
	}
}

// formatVisibilityScope formats visibility scope for display.
func formatVisibilityScope(ctx *agentv1.RuntimeContextResponse) string {
	if ctx.Visibility.AllPids {
		return "All host processes"
	}
	if ctx.Visibility.AllContainers {
		return "All containers"
	}
	if ctx.Visibility.PodScope {
		return "Pod only"
	}
	return "Limited"
}

// truncateContainerID truncates container ID for display.
func truncateContainerID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}
