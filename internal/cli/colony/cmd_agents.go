package colony

import (
	"context"
	"fmt"
	"io"
	"strings"
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
		Long: `Display registered agents, including agents with stale or missing heartbeats.

This command queries the running colony to retrieve real-time agent information including:
- Agent ID and component name
- Mesh IP addresses (IPv4 and IPv6)
- Heartbeat status (healthy, degraded, unhealthy), independent of service health
- Service check results and live/cached data source
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
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Connected to colony at: %s\n\n", endpoint); err != nil {
					return err
				}
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

			for _, agent := range agents {
				if agent.ServicesError != "" {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s uses cached service registration: %s\n", agent.AgentId, agent.ServicesError)
				}
			}
			if err := outputAgentSummary(cmd.OutOrStdout(), resp.Msg.Agents, len(agents)); err != nil {
				return err
			}
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
	var output strings.Builder
	_, _ = fmt.Fprintf(&output, "Connected Agents (%d):\n\n", len(agents))

	for i, agent := range agents {
		if i > 0 {
			_, _ = fmt.Fprintln(&output)
		}

		_, _ = fmt.Fprintf(&output, "┌─ %s ", agent.AgentId)
		for j := 0; j < 50-len(agent.AgentId); j++ {
			_, _ = fmt.Fprint(&output, "─")
		}
		_, _ = fmt.Fprintln(&output, "┐")

		//nolint:staticcheck // ComponentName is deprecated but kept for backward compatibility
		_, _ = fmt.Fprintf(&output, "│ Component:  %-45s│\n", agent.ComponentName)
		_, _ = fmt.Fprintf(&output, "│ Heartbeat:  %-45s│\n", formatAgentStatus(agent))
		_, _ = fmt.Fprintf(&output, "│ Mesh IP:    %-45s│\n", agent.MeshIpv4)
		source := agent.ServicesSource
		if source == "" {
			source = "unknown"
		}
		checks := helpers.AgentChecks(agent.ServiceStatuses, len(agent.Services), source)
		_, _ = fmt.Fprintf(&output, "│ Checks:     %-45s│\n", checks.Status())
		_, _ = fmt.Fprintf(&output, "│ Last check: %-45s│\n", helpers.FormatAge(checks.LastCheck, time.Now()))
		_, _ = fmt.Fprintf(&output, "│ Data:       %-45s│\n", source)
		_, _ = fmt.Fprintf(&output, "│ Observed:   %-45s│\n", helpers.FormatAge(agent.ServicesObservedAt, time.Now()))
		_, _ = fmt.Fprintln(&output, "│                                                                │")

		if agent.RuntimeContext != nil {
			rc := agent.RuntimeContext
			_, _ = fmt.Fprintf(&output, "│ Runtime:    %-45s│\n", formatRuntimeTypeShort(rc.RuntimeType))
			if rc.SidecarMode != agentv1.SidecarMode_SIDECAR_MODE_UNKNOWN {
				_, _ = fmt.Fprintf(&output, "│ Mode:       %-45s│\n", formatSidecarModeShort(rc.SidecarMode))
			}
			if rc.Platform != nil {
				_, _ = fmt.Fprintf(&output, "│ Platform:   %s (%s) %-27s│\n", rc.Platform.Os, rc.Platform.Arch, "")
			}
			if rc.CriSocket != nil {
				_, _ = fmt.Fprintf(&output, "│ CRI:        %-45s│\n", rc.CriSocket.Type)
			}
			_, _ = fmt.Fprintln(&output, "│                                                                │")

			// Capabilities
			_, _ = fmt.Fprintln(&output, "│ Capabilities:                                                  │")
			_, _ = fmt.Fprintf(&output, "│   %s connect  %s exec  %s shell  %s run                    │\n",
				formatCapabilitySymbol(rc.GetCapabilities().GetCanConnect()),
				formatCapabilitySymbol(rc.GetCapabilities().GetCanExec()),
				formatCapabilitySymbol(rc.GetCapabilities().GetCanShell()),
				formatCapabilitySymbol(rc.GetCapabilities().GetCanRun()))

			// Linux Capabilities (if available)
			if rc.Capabilities != nil && rc.Capabilities.LinuxCapabilities != nil {
				_, _ = fmt.Fprintln(&output, "│                                                                │")
				_, _ = fmt.Fprintln(&output, "│ Linux Capabilities:                                            │")
				linuxCaps := rc.Capabilities.LinuxCapabilities

				// Show BPF/eBPF capabilities (most important for eBPF operations)
				_, _ = fmt.Fprintf(&output, "│   %s CAP_BPF        %s CAP_PERFMON      %s CAP_SYSLOG      │\n",
					formatCapabilitySymbol(linuxCaps.CapBpf),
					formatCapabilitySymbol(linuxCaps.CapPerfmon),
					formatCapabilitySymbol(linuxCaps.CapSyslog))

				// Show core required capabilities
				_, _ = fmt.Fprintf(&output, "│   %s CAP_NET_ADMIN  %s CAP_SYS_PTRACE  %s CAP_SYS_RESOURCE │\n",
					formatCapabilitySymbol(linuxCaps.CapNetAdmin),
					formatCapabilitySymbol(linuxCaps.CapSysPtrace),
					formatCapabilitySymbol(linuxCaps.CapSysResource))

				// Show CAP_SYS_ADMIN separately (optional - needed for nsenter)
				_, _ = fmt.Fprintf(&output, "│   %s CAP_SYS_ADMIN (nsenter exec + older kernel fallback)   │\n",
					formatCapabilitySymbol(linuxCaps.CapSysAdmin))
			}
			_, _ = fmt.Fprintln(&output, "│                                                                │")

			// Visibility
			if rc.Visibility != nil {
				_, _ = fmt.Fprintf(&output, "│ Visibility: %-45s│\n", formatVisibilityShort(rc.Visibility))
			}
		} else {
			_, _ = fmt.Fprintln(&output, "│ Runtime:    Unknown (legacy agent)                            │")
		}

		_, _ = fmt.Fprintln(&output, "└────────────────────────────────────────────────────────────────┘")
	}

	_, _ = fmt.Fprintln(&output)
	_, err := io.WriteString(out, output.String())
	return err
}
