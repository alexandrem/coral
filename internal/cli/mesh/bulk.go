package mesh

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/cli/fleet"
	"github.com/coral-mesh/coral/internal/cli/helpers"
)

func newPingCmd() *cobra.Command {
	var options fleet.Options
	var colony string
	var count int
	var timeout time.Duration
	cmd := &cobra.Command{
		Use: "ping [agent-id]", Short: "Ping selected agents through the Colony mesh",
		Long: `Ping agents through the Colony, using the same fleet filters as coral colony agents.
Without a selector, all registered agents are targeted. Each agent has a separate result.

Examples:
  coral mesh ping --status degraded,unhealthy --dry-run
  coral mesh ping --service payments --concurrency 4 --report ping.json
  coral mesh ping --retry-failed ping.json --format json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if options.RetryFailed != "" {
				report, err := fleet.LoadRetryReport(options.RetryFailed, "mesh ping")
				if err != nil {
					return err
				}
				var saved struct {
					Count   int    `json:"count"`
					Timeout string `json:"timeout"`
				}
				if err := json.Unmarshal(report.Parameters, &saved); err != nil {
					return fmt.Errorf("invalid ping parameters in retry report: %w", err)
				}
				if !cmd.Flags().Changed("count") {
					count = saved.Count
				}
				if !cmd.Flags().Changed("timeout") {
					timeout, err = time.ParseDuration(saved.Timeout)
					if err != nil {
						return fmt.Errorf("invalid ping timeout in retry report: %w", err)
					}
				}
			}
			if count < 1 || count > 100 {
				return fmt.Errorf("--count must be between 1 and 100")
			}
			if timeout < time.Millisecond || timeout > time.Minute {
				return fmt.Errorf("--timeout must be between 1ms and 1m")
			}
			if err := positionalTarget(&options, args); err != nil {
				return err
			}
			parameters := struct {
				Count   int    `json:"count"`
				Timeout string `json:"timeout"`
			}{count, timeout.String()}
			plan, err := options.Prepare(cmd.Context(), colony, "mesh ping", parameters)
			if err != nil {
				return err
			}
			report := plan.Run(cmd.Context(), options, func(ctx context.Context, target fleet.Target, _ json.RawMessage) (string, json.RawMessage, error) {
				resp, err := plan.Client.MeshPing(ctx, connect.NewRequest(&colonyv1.MeshPingRequest{AgentId: target.ID, Count: int32(count), TimeoutMs: int32(timeout.Milliseconds())}))
				if err != nil {
					return "", nil, err
				}
				if len(resp.Msg.Results) != 1 || resp.Msg.Results[0].AgentId != target.ID {
					return "", nil, connect.NewError(connect.CodeDataLoss, fmt.Errorf("colony returned a ping result for the wrong target"))
				}
				result := resp.Msg.Results[0]
				data, err := json.Marshal(result)
				if err != nil {
					return "", nil, err
				}
				message := fmt.Sprintf("%s loss=%.1f%% avg=%.3fms", result.MeshIp, result.PacketLossPercentage, result.AvgRttMs)
				if result.Error != "" {
					return message, data, connect.NewError(connect.CodeUnavailable, fmt.Errorf("%s", result.Error))
				}
				if result.Received == 0 || result.PacketLossPercentage > 50 {
					return message, data, connect.NewError(connect.CodeUnavailable, fmt.Errorf("%s", message))
				}
				return message, data, nil
			})
			return options.FinishWithTable(cmd, report, printMeshReport)
		},
	}
	options.AddFlags(cmd)
	helpers.AddColonyFlag(cmd, &colony)
	cmd.Flags().IntVarP(&count, "count", "n", 4, "Number of pings per agent (1-100)")
	cmd.Flags().DurationVarP(&timeout, "timeout", "W", 2*time.Second, "Timeout for each ping (1ms-1m); --request-timeout bounds the whole attempt")
	return cmd
}

func newAuditCmd() *cobra.Command {
	var options fleet.Options
	var colony string
	cmd := &cobra.Command{
		Use: "audit [agent-id]", Short: "Audit selected agents for NAT and mesh issues",
		Long: `Compare Colony WireGuard observations with agent-announced endpoints.
Use fleet filters to select targets, --dry-run to preview, and --report to save retryable results.
NAT classifications are findings; RPC and per-agent assessment errors fail the target.

Examples:
  coral mesh audit --service payments --format json
  coral mesh audit --agents agent-1,agent-2 --report audit.json
  coral mesh audit --retry-failed audit.json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := positionalTarget(&options, args); err != nil {
				return err
			}
			plan, err := options.Prepare(cmd.Context(), colony, "mesh audit", struct{}{})
			if err != nil {
				return err
			}
			report := plan.Run(cmd.Context(), options, func(ctx context.Context, target fleet.Target, _ json.RawMessage) (string, json.RawMessage, error) {
				resp, err := plan.Client.MeshAudit(ctx, connect.NewRequest(&colonyv1.MeshAuditRequest{AgentId: target.ID}))
				if err != nil {
					return "", nil, err
				}
				if len(resp.Msg.Results) != 1 || resp.Msg.Results[0].AgentId != target.ID {
					return "", nil, connect.NewError(connect.CodeDataLoss, fmt.Errorf("colony returned an audit result for the wrong target"))
				}
				result := resp.Msg.Results[0]
				data, err := json.Marshal(result)
				if err != nil {
					return "", nil, err
				}
				message := fmt.Sprintf("%s NAT=%s handshake=%s observed=%s registered=%s", result.MeshIp, result.NatType, formatAge(result.HandshakeAgeSeconds), result.ColonyObservedEndpoint, result.AgentRegisteredEndpoint)
				if result.HandshakeAgeSeconds < 0 {
					message = fmt.Sprintf("%s NAT=%s handshake=never", result.MeshIp, result.NatType)
				}
				if result.Error != "" {
					return message, data, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("%s", result.Error))
				}
				if result.NatType == "error" {
					return message, data, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("could not assess registered or observed mesh endpoints"))
				}
				return message, data, nil
			})
			return options.FinishWithTable(cmd, report, printMeshReport)
		},
	}
	options.AddFlags(cmd)
	helpers.AddColonyFlag(cmd, &colony)
	return cmd
}

func positionalTarget(options *fleet.Options, args []string) error {
	if len(args) == 0 {
		return nil
	}
	if options.Selector.HasFilters() || options.All || options.RetryFailed != "" {
		return fmt.Errorf("an agent argument cannot be combined with fleet selectors, --all, or --retry-failed")
	}
	options.Selector.IDs = []string{args[0]}
	return nil
}
