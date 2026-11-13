package colony

import (
	"context"
	"fmt"
	"io"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/cli/helpers"
	"github.com/coral-mesh/coral/internal/config"
)

func newAgentsCmd() *cobra.Command {
	var (
		format   string
		verbose  bool
		colonyID string
		options  agentListOptions
	)

	cmd := &cobra.Command{
		Use:   "agents",
		Short: "List registered agents",
		Args:  cobra.NoArgs,
		Long: `Display all agents currently connected to the colony.

This command queries the running colony to retrieve real-time agent information including:
- Agent ID and component name
- Mesh IP addresses (IPv4 and IPv6)
- Connection status (healthy, degraded, unhealthy)
- Last seen timestamp
- Runtime context (with --verbose)

Note: The colony must be running for this command to work.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := options.validate(); err != nil {
				return err
			}
			if err := helpers.ValidateFormat(format, []helpers.OutputFormat{helpers.FormatTable, helpers.FormatJSON, helpers.FormatYAML}); err != nil {
				return err
			}
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

			// Create RPC client using shared helper (prioritizes env var > local > mesh)
			client, endpoint, err := helpers.GetColonyClientWithFallback(cmd.Context(), colonyID)
			if err != nil {
				return err
			}
			if verbose && format == string(helpers.FormatTable) {
				fmt.Fprintf(cmd.OutOrStdout(), "Connected to colony at: %s\n\n", endpoint)
			}

			// Call ListAgents RPC
			// Note: timeout is handled inside GetColonyClientWithFallback for connection,
			// but we want a timeout for the actual request too.
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			req := connect.NewRequest(&colonyv1.ListAgentsRequest{})
			resp, err := client.ListAgents(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to list agents: %w", err)
			}

			agents := options.selectAgents(resp.Msg.Agents)

			// Use formatter for non-table output.
			if format != string(helpers.FormatTable) {
				formatter, err := helpers.NewFormatter(helpers.OutputFormat(format))
				if err != nil {
					return err
				}
				return formatter.Format(agents, cmd.OutOrStdout())
			}

			outputAgentSummary(cmd.OutOrStdout(), resp.Msg.Agents, len(agents))
			if len(agents) == 0 {
				return nil
			}
			if verbose {
				return outputAgentsVerbose(cmd.OutOrStdout(), agents)
			}
			return outputAgentsTable(cmd.OutOrStdout(), agents, options.wide)

		},
	}

	helpers.AddFormatFlag(cmd, &format, helpers.FormatTable, []helpers.OutputFormat{
		helpers.FormatTable,
		helpers.FormatJSON,
		helpers.FormatYAML,
	})
	helpers.AddVerboseFlag(cmd, &verbose)
	helpers.AddColonyFlag(cmd, &colonyID)
	options.addFlags(cmd)

	return cmd
}

// outputAgentsVerbose outputs agents in verbose format with full runtime context.
func outputAgentsVerbose(out io.Writer, agents []*colonyv1.Agent) error {
	fmt.Fprintf(out, "Connected Agents (%d):\n\n", len(agents))

	for i, agent := range agents {
		if i > 0 {
			fmt.Fprintln(out)
		}

		fmt.Fprintf(out, "┌─ %s ", agent.AgentId)
		for j := 0; j < 50-len(agent.AgentId); j++ {
			fmt.Fprint(out, "─")
		}
		fmt.Fprintln(out, "┐")

		//nolint:staticcheck // ComponentName is deprecated but kept for backward compatibility
		fmt.Fprintf(out, "│ Component:  %-45s│\n", agent.ComponentName)
		fmt.Fprintf(out, "│ Status:     %-45s│\n", formatAgentStatus(agent))
		fmt.Fprintf(out, "│ Mesh IP:    %-45s│\n", agent.MeshIpv4)
		fmt.Fprintln(out, "│                                                                │")

		if agent.RuntimeContext != nil {
			rc := agent.RuntimeContext
			fmt.Fprintf(out, "│ Runtime:    %-45s│\n", formatRuntimeTypeShort(rc.RuntimeType))
			if rc.SidecarMode != agentv1.SidecarMode_SIDECAR_MODE_UNKNOWN {
				fmt.Fprintf(out, "│ Mode:       %-45s│\n", formatSidecarModeShort(rc.SidecarMode))
			}
			if rc.Platform != nil {
				fmt.Fprintf(out, "│ Platform:   %s (%s) %-27s│\n", rc.Platform.Os, rc.Platform.Arch, "")
			}
			if rc.CriSocket != nil {
				fmt.Fprintf(out, "│ CRI:        %-45s│\n", rc.CriSocket.Type)
			}
			fmt.Fprintln(out, "│                                                                │")

			// Capabilities
			fmt.Fprintln(out, "│ Capabilities:                                                  │")
			fmt.Fprintf(out, "│   %s connect  %s exec  %s shell  %s run                    │\n",
				formatCapabilitySymbol(rc.GetCapabilities().GetCanConnect()),
				formatCapabilitySymbol(rc.GetCapabilities().GetCanExec()),
				formatCapabilitySymbol(rc.GetCapabilities().GetCanShell()),
				formatCapabilitySymbol(rc.GetCapabilities().GetCanRun()))

			// Linux Capabilities (if available)
			if rc.Capabilities != nil && rc.Capabilities.LinuxCapabilities != nil {
				fmt.Fprintln(out, "│                                                                │")
				fmt.Fprintln(out, "│ Linux Capabilities:                                            │")
				linuxCaps := rc.Capabilities.LinuxCapabilities

				// Show BPF/eBPF capabilities (most important for eBPF operations)
				fmt.Fprintf(out, "│   %s CAP_BPF        %s CAP_PERFMON      %s CAP_SYSLOG      │\n",
					formatCapabilitySymbol(linuxCaps.CapBpf),
					formatCapabilitySymbol(linuxCaps.CapPerfmon),
					formatCapabilitySymbol(linuxCaps.CapSyslog))

				// Show core required capabilities
				fmt.Fprintf(out, "│   %s CAP_NET_ADMIN  %s CAP_SYS_PTRACE  %s CAP_SYS_RESOURCE │\n",
					formatCapabilitySymbol(linuxCaps.CapNetAdmin),
					formatCapabilitySymbol(linuxCaps.CapSysPtrace),
					formatCapabilitySymbol(linuxCaps.CapSysResource))

				// Show CAP_SYS_ADMIN separately (optional - needed for nsenter)
				fmt.Fprintf(out, "│   %s CAP_SYS_ADMIN (nsenter exec + older kernel fallback)   │\n",
					formatCapabilitySymbol(linuxCaps.CapSysAdmin))
			}
			fmt.Fprintln(out, "│                                                                │")

			// Visibility
			if rc.Visibility != nil {
				fmt.Fprintf(out, "│ Visibility: %-45s│\n", formatVisibilityShort(rc.Visibility))
			}
		} else {
			fmt.Fprintln(out, "│ Runtime:    Unknown (legacy agent)                            │")
		}

		fmt.Fprintln(out, "└────────────────────────────────────────────────────────────────┘")
	}

	fmt.Fprintln(out)
	return nil
}
