package colony

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/cli/helpers"
	"github.com/coral-mesh/coral/internal/config"
)

func newAgentsCmd() *cobra.Command {
	var (
		format   string
		verbose  bool
		colonyID string
	)

	cmd := &cobra.Command{
		Use:   "agents",
		Short: "List connected agents",
		Long: `Display all agents currently connected to the colony.

This command queries the running colony to retrieve real-time agent information including:
- Agent ID and component name
- Mesh IP addresses (IPv4 and IPv6)
- Connection status (healthy, degraded, unhealthy)
- Last seen timestamp
- Runtime context (with --verbose)

Note: The colony must be running for this command to work.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create resolver
			resolver, err := config.NewResolver()
			if err != nil {
				return fmt.Errorf("failed to create config resolver: %w", err)
			}

			// Resolve colony ID
			if colonyID == "" {
				colonyID, err = resolver.ResolveColonyID()
				if err != nil {
					return fmt.Errorf("failed to resolve colony: %w\n\nRun 'coral init <app-name>' to create a colony", err)
				}
			}

			// Load colony configuration
			loader := resolver.GetLoader()
			colonyConfig, err := loader.LoadColonyConfig(colonyID)
			if err != nil {
				return fmt.Errorf("failed to load colony config: %w", err)
			}

			// Get connect port
			connectPort := colonyConfig.Services.ConnectPort
			if connectPort == 0 {
				connectPort = 9000
			}

			// Create RPC client - try localhost first, then mesh IP
			baseURL := fmt.Sprintf("http://localhost:%d", connectPort)
			client := colonyv1connect.NewColonyServiceClient(http.DefaultClient, baseURL)

			// Call ListAgents RPC
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			req := connect.NewRequest(&colonyv1.ListAgentsRequest{})
			resp, err := client.ListAgents(ctx, req)
			if err != nil {
				// Try mesh IP as fallback
				meshIP := colonyConfig.WireGuard.MeshIPv4
				if meshIP == "" {
					meshIP = "10.42.0.1"
				}
				baseURL = fmt.Sprintf("http://%s:%d", meshIP, connectPort)
				client = colonyv1connect.NewColonyServiceClient(http.DefaultClient, baseURL)

				ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel2()

				resp, err = client.ListAgents(ctx2, connect.NewRequest(&colonyv1.ListAgentsRequest{}))
				if err != nil {
					return fmt.Errorf("failed to list agents (is colony running?): %w", err)
				}
			}

			agents := resp.Msg.Agents

			// Use formatter for non-table output.
			if format != string(helpers.FormatTable) {
				formatter, err := helpers.NewFormatter(helpers.OutputFormat(format))
				if err != nil {
					return err
				}
				return formatter.Format(agents, os.Stdout)
			}

			// Human-readable output
			if len(agents) == 0 {
				fmt.Println("No agents connected.")
				return nil
			}

			if verbose {
				return outputAgentsVerbose(agents)
			}

			fmt.Printf("Connected Agents (%d):\n\n", len(agents))
			fmt.Printf("%-25s %-20s %-20s %-10s %-10s %s\n", "AGENT ID", "SERVICES", "RUNTIME", "MESH IP", "STATUS", "LAST SEEN")
			fmt.Println("--------------------------------------------------------------------------------------------------------")

			for _, agent := range agents {
				// Format last seen as relative time
				lastSeen := agent.LastSeen.AsTime()
				elapsed := time.Since(lastSeen)
				var lastSeenStr string
				if elapsed < time.Minute {
					lastSeenStr = fmt.Sprintf("%ds ago", int(elapsed.Seconds()))
				} else if elapsed < time.Hour {
					lastSeenStr = fmt.Sprintf("%dm ago", int(elapsed.Minutes()))
				} else {
					lastSeenStr = fmt.Sprintf("%dh ago", int(elapsed.Hours()))
				}

				// Format runtime type
				runtimeStr := "-"
				if agent.RuntimeContext != nil {
					runtimeStr = formatRuntimeTypeShort(agent.RuntimeContext.RuntimeType)
					if agent.RuntimeContext.SidecarMode != 0 && agent.RuntimeContext.SidecarMode != 1 {
						runtimeStr += fmt.Sprintf(" (%s)", formatSidecarModeShort(agent.RuntimeContext.SidecarMode))
					}
				}

				// Format services list (RFD 044: use Services array, not ComponentName).
				servicesStr := formatServicesList(agent.Services)
				if servicesStr == "" {
					//nolint:staticcheck // ComponentName is deprecated but kept for backward compatibility
					servicesStr = agent.ComponentName // Fallback for backward compatibility
				}

				fmt.Printf("%-25s %-20s %-20s %-10s %-10s %s\n",
					truncate(agent.AgentId, 25),
					truncate(servicesStr, 20),
					truncate(runtimeStr, 20),
					agent.MeshIpv4,
					agent.Status,
					lastSeenStr,
				)
			}

			return nil
		},
	}

	helpers.AddFormatFlag(cmd, &format, helpers.FormatTable, []helpers.OutputFormat{
		helpers.FormatTable,
		helpers.FormatJSON,
		helpers.FormatYAML,
	})
	helpers.AddVerboseFlag(cmd, &verbose)
	helpers.AddColonyFlag(cmd, &colonyID)

	return cmd
}

// outputAgentsVerbose outputs agents in verbose format with full runtime context.
func outputAgentsVerbose(agents []*colonyv1.Agent) error {
	fmt.Printf("Connected Agents (%d):\n\n", len(agents))

	for i, agent := range agents {
		if i > 0 {
			fmt.Println()
		}

		fmt.Printf("┌─ %s ", agent.AgentId)
		for j := 0; j < 50-len(agent.AgentId); j++ {
			fmt.Print("─")
		}
		fmt.Println("┐")

		//nolint:staticcheck // ComponentName is deprecated but kept for backward compatibility
		fmt.Printf("│ Component:  %-45s│\n", agent.ComponentName)
		fmt.Printf("│ Status:     %-45s│\n", formatAgentStatus(agent))
		fmt.Printf("│ Mesh IP:    %-45s│\n", agent.MeshIpv4)
		fmt.Println("│                                                                │")

		if agent.RuntimeContext != nil {
			rc := agent.RuntimeContext
			fmt.Printf("│ Runtime:    %-45s│\n", formatRuntimeTypeShort(rc.RuntimeType))
			if rc.SidecarMode != agentv1.SidecarMode_SIDECAR_MODE_UNKNOWN {
				fmt.Printf("│ Mode:       %-45s│\n", formatSidecarModeShort(rc.SidecarMode))
			}
			if rc.Platform != nil {
				fmt.Printf("│ Platform:   %s (%s) %-27s│\n", rc.Platform.Os, rc.Platform.Arch, "")
			}
			if rc.CriSocket != nil {
				fmt.Printf("│ CRI:        %-45s│\n", rc.CriSocket.Type)
			}
			fmt.Println("│                                                                │")

			// Capabilities
			fmt.Println("│ Capabilities:                                                  │")
			fmt.Printf("│   %s connect  %s exec  %s shell  %s run                    │\n",
				formatCapabilitySymbol(rc.Capabilities.CanConnect),
				formatCapabilitySymbol(rc.Capabilities.CanExec),
				formatCapabilitySymbol(rc.Capabilities.CanShell),
				formatCapabilitySymbol(rc.Capabilities.CanRun))

			// Linux Capabilities (if available)
			if rc.Capabilities != nil && rc.Capabilities.LinuxCapabilities != nil {
				fmt.Println("│                                                                │")
				fmt.Println("│ Linux Capabilities:                                            │")
				linuxCaps := rc.Capabilities.LinuxCapabilities

				// Show BPF/eBPF capabilities (most important for eBPF operations)
				fmt.Printf("│   %s CAP_BPF        %s CAP_PERFMON      %s CAP_SYSLOG      │\n",
					formatCapabilitySymbol(linuxCaps.CapBpf),
					formatCapabilitySymbol(linuxCaps.CapPerfmon),
					formatCapabilitySymbol(linuxCaps.CapSyslog))

				// Show core required capabilities
				fmt.Printf("│   %s CAP_NET_ADMIN  %s CAP_SYS_PTRACE  %s CAP_SYS_RESOURCE │\n",
					formatCapabilitySymbol(linuxCaps.CapNetAdmin),
					formatCapabilitySymbol(linuxCaps.CapSysPtrace),
					formatCapabilitySymbol(linuxCaps.CapSysResource))

				// Show CAP_SYS_ADMIN separately (optional - needed for nsenter)
				fmt.Printf("│   %s CAP_SYS_ADMIN (nsenter exec + older kernel fallback)   │\n",
					formatCapabilitySymbol(linuxCaps.CapSysAdmin))
			}
			fmt.Println("│                                                                │")

			// Visibility
			fmt.Printf("│ Visibility: %-45s│\n", formatVisibilityShort(rc.Visibility))
		} else {
			fmt.Println("│ Runtime:    Unknown (legacy agent)                            │")
		}

		fmt.Println("└────────────────────────────────────────────────────────────────┘")
	}

	fmt.Println()
	return nil
}
