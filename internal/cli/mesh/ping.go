package mesh

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/cli/helpers"
	"github.com/coral-mesh/coral/internal/config"
)

func newPingCmd() *cobra.Command {
	var (
		count    int
		timeout  time.Duration
		colonyID string
	)

	cmd := &cobra.Command{
		Use:   "ping [agent-id]",
		Short: "Ping agent(s) through the mesh via Colony",
		Long: `Send encrypted UDP pings to an agent (or all agents) via the control mesh.
Pings are performed by the Colony and results are reported back to the CLI.
This verifies the complete user-space cryptography routing path perfectly.

Example:
  coral mesh ping prod-agent-01
  coral mesh ping (pings all connected agents)`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var agentID string
			if len(args) > 0 {
				agentID = args[0]
			}
			ctx := cmd.Context()

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
			client, _, err := helpers.GetColonyClientWithFallback(ctx, colonyID)
			if err != nil {
				return fmt.Errorf("failed to connect to colony: %w", err)
			}

			// Call MeshPing RPC on Colony
			pingCtx, cancel := context.WithTimeout(ctx, timeout*time.Duration(count+1))
			defer cancel()

			pingReq := connect.NewRequest(&colonyv1.MeshPingRequest{
				AgentId:   agentID,
				Count:     int32(count),
				TimeoutMs: int32(timeout.Milliseconds()),
			})

			resp, err := client.MeshPing(pingCtx, pingReq)
			if err != nil {
				return fmt.Errorf("failed to perform mesh ping: %w", err)
			}

			if len(resp.Msg.Results) == 0 {
				fmt.Println("No agents connected to the colony.")
				return nil
			}

			fmt.Printf("MESH PING results via Colony (%d agents, %d pings each):\n\n", len(resp.Msg.Results), count)
			fmt.Printf("%-25s %-15s %-10s %-10s %s\n", "AGENT ID", "MESH IP", "LOSS", "AVG RTT", "STATUS")
			fmt.Println("--------------------------------------------------------------------------------")

			for _, result := range resp.Msg.Results {
				status := "OK"
				if result.Error != "" {
					status = result.Error
				} else if result.PacketLossPercentage > 50 {
					status = "DEGRADED"
				}

				avgRTT := "n/a"
				if result.Received > 0 {
					avgRTT = fmt.Sprintf("%.3fms", result.AvgRttMs)
				}

				fmt.Printf("%-25s %-15s %-10.1f%% %-10s %s\n",
					truncate(result.AgentId, 25),
					result.MeshIp,
					result.PacketLossPercentage,
					avgRTT,
					status,
				)
			}

			return nil
		},
	}

	cmd.Flags().IntVarP(&count, "count", "n", 4, "Number of pings to send")
	cmd.Flags().DurationVarP(&timeout, "timeout", "W", 2*time.Second, "Time to wait for a response")
	helpers.AddColonyFlag(cmd, &colonyID)

	return cmd
}

func truncate(s string, l int) string {
	if len(s) > l {
		return s[:l-3] + "..."
	}
	return s
}

func newAuditCmd() *cobra.Command {
	var (
		colonyID string
		format   string
	)

	cmd := &cobra.Command{
		Use:   "audit [agent-id]",
		Short: "Audit the mesh topology to detect NAT issues",
		Long: `Compare Colony's live WireGuard observations against agent-announced STUN endpoints.

Detects:
  - SYMMETRIC NAT: port seen by Colony differs from agent's STUN-reported port
  - ROAMING mode: agent registered without STUN endpoint discovery
  - Stale connections: no handshake for an extended period

Example:
  coral mesh audit
  coral mesh audit prod-agent-01
  coral mesh audit --format json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var agentID string
			if len(args) > 0 {
				agentID = args[0]
			}
			ctx := cmd.Context()

			resolver, err := config.NewResolver()
			if err != nil {
				return fmt.Errorf("failed to create config resolver: %w", err)
			}
			if colonyID == "" {
				colonyID, err = resolver.ResolveColonyID()
				if err != nil {
					return fmt.Errorf("failed to resolve colony: %w\n\nRun 'coral init <app-name>' to create a colony", err)
				}
			}

			client, _, err := helpers.GetColonyClientWithFallback(ctx, colonyID)
			if err != nil {
				return fmt.Errorf("failed to connect to colony: %w", err)
			}

			resp, err := client.MeshAudit(ctx, connect.NewRequest(&colonyv1.MeshAuditRequest{
				AgentId: agentID,
			}))
			if err != nil {
				return fmt.Errorf("mesh audit failed: %w", err)
			}

			if format != string(helpers.FormatTable) {
				formatter, err := helpers.NewFormatter(helpers.OutputFormat(format))
				if err != nil {
					return err
				}
				return formatter.Format(resp.Msg.Results, os.Stdout)
			}

			printAuditResults(resp.Msg.Results)
			return nil
		},
	}

	helpers.AddColonyFlag(cmd, &colonyID)
	helpers.AddFormatFlag(cmd, &format, helpers.FormatTable, []helpers.OutputFormat{
		helpers.FormatTable,
		helpers.FormatJSON,
	})
	return cmd
}

func printAuditResults(results []*colonyv1.MeshAuditAgentResult) {
	if len(results) == 0 {
		fmt.Println("No agents connected to the colony.")
		return
	}

	fmt.Printf("MESH AUDIT (%d agents)\n\n", len(results))
	fmt.Printf("%-24s %-14s %-23s %-23s %-12s %-12s %s\n",
		"AGENT", "MESH IP", "COLONY OBSERVES", "AGENT REGISTERED", "NAT TYPE", "HANDSHAKE", "LINK")
	fmt.Println(strings.Repeat("-", 115))

	var warnings []string
	for _, r := range results {
		observed := r.ColonyObservedEndpoint
		if observed == "" {
			observed = "(no packets)"
		}
		registered := r.AgentRegisteredEndpoint
		if registered == "" {
			registered = "(roaming)"
		}
		hsAge := "never"
		if r.HandshakeAgeSeconds >= 0 {
			hsAge = formatAge(r.HandshakeAgeSeconds)
		}
		natDisplay := r.NatType
		if r.NatType == "symmetric" {
			natDisplay = "SYMMETRIC !"
		}
		link := formatBytes(r.TxBytes) + "up " + formatBytes(r.RxBytes) + "dn"
		if r.Error != "" {
			link = r.Error
		}

		fmt.Printf("%-24s %-14s %-23s %-23s %-12s %-12s %s\n",
			truncate(r.AgentId, 24),
			r.MeshIp,
			truncate(observed, 23),
			truncate(registered, 23),
			natDisplay,
			hsAge,
			link,
		)

		if r.NatType == "symmetric" {
			warnings = append(warnings, fmt.Sprintf(
				"! %s: SYMMETRIC NAT -- Colony sees port %s but agent's STUN reported %s.\n  WireGuard roaming handles this while traffic flows, but silence >180s may require agent restart.",
				r.AgentId, portOf(r.ColonyObservedEndpoint), portOf(r.AgentRegisteredEndpoint),
			))
		}
	}

	if len(warnings) > 0 {
		fmt.Println()
		for _, w := range warnings {
			fmt.Println(w)
		}
	}
}

func formatAge(seconds int64) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds ago", seconds)
	}
	return fmt.Sprintf("%dm%ds ago", seconds/60, seconds%60)
}

func formatBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1fGB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%dB", b)
	}
}

func portOf(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return port
}
