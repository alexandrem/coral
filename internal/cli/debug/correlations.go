package debug

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
)

// NewCorrelationsCmd creates the `coral debug correlations` command (RFD 091).
func NewCorrelationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "correlations",
		Short: "Manage active correlation descriptors",
		Long: `List and remove active correlation descriptors deployed to agents.

Correlations are created by the colony LLM through the MCP interface. The CLI
provides read and remove operations for operator visibility and incident cleanup.

Examples:
  coral debug correlations
  coral debug correlations --service payments
  coral debug correlations remove corr-abc123`,
	}

	// Allow `coral debug correlations` (no subcommand) to also list.
	listCmd := newCorrelationsListCmd()
	cmd.Flags().AddFlagSet(listCmd.Flags())
	cmd.RunE = listCmd.RunE

	cmd.AddCommand(listCmd)
	cmd.AddCommand(newCorrelationsRemoveCmd())

	return cmd
}

func newCorrelationsListCmd() *cobra.Command {
	var (
		serviceName string
		format      string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List active correlation descriptors",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			client, err := getColonyDebugClient()
			if err != nil {
				return fmt.Errorf("failed to create debug client: %w", err)
			}

			resp, err := client.ListCorrelations(ctx, connect.NewRequest(&colonypb.ColonyListCorrelationsRequest{
				ServiceName: serviceName,
			}))
			if err != nil {
				return fmt.Errorf("failed to list correlations: %w", err)
			}

			if format == "json" {
				return json.NewEncoder(os.Stdout).Encode(resp.Msg)
			}

			if len(resp.Msg.Descriptors) == 0 {
				fmt.Println("No active correlations.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			defer func() { _ = w.Flush() }()
			if _, err := fmt.Fprintln(w, "ID\tService\tStrategy\tWindow\tThreshold\tAction"); err != nil {
				return err
			}
			for _, d := range resp.Msg.Descriptors {
				window := "-"
				if d.Window != nil {
					window = d.Window.AsDuration().String()
				}
				threshold := "-"
				if d.Threshold != 0 {
					threshold = fmt.Sprintf("%.0f", d.Threshold)
				}
				action := "emit_event"
				if d.Action != nil {
					action = d.Action.Kind.String()
				}
				if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					d.Id,
					d.ServiceName,
					d.Strategy.String(),
					window,
					threshold,
					action,
				); err != nil {
					return err
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&serviceName, "service", "s", "", "Filter by service name")
	cmd.Flags().StringVar(&format, "format", "table", "Output format: table or json")

	return cmd
}

func newCorrelationsRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <correlation-id>",
		Short: "Remove an active correlation descriptor",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			client, err := getColonyDebugClient()
			if err != nil {
				return fmt.Errorf("failed to create debug client: %w", err)
			}

			correlationID := args[0]
			_, err = client.RemoveCorrelation(ctx, connect.NewRequest(&colonypb.ColonyRemoveCorrelationRequest{
				CorrelationId: correlationID,
			}))
			if err != nil {
				return fmt.Errorf("failed to remove correlation: %w", err)
			}

			fmt.Printf("Correlation %s removed.\n", correlationID)
			return nil
		},
	}
	return cmd
}
