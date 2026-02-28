package mesh

import (
	"context"
	"fmt"
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
