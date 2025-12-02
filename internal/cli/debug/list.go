package debug

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
)

func NewListCmd() *cobra.Command {
	var (
		serviceName string
		status      string
		format      string
	)

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"sessions"},
		Short:   "List active debug sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			// Resolve colony URL from config
			colonyAddr, err := getColonyURL()
			if err != nil {
				return fmt.Errorf("failed to resolve colony address: %w", err)
			}

			client := colonyv1connect.NewDebugServiceClient(
				http.DefaultClient,
				colonyAddr,
			)

			req := &colonypb.ListDebugSessionsRequest{
				ServiceName: serviceName,
				Status:      status,
			}

			resp, err := client.ListDebugSessions(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to list sessions: %w", err)
			}

			// Format and print output
			formatter := NewFormatter(OutputFormat(format))
			output, err := formatter.FormatSessions(resp.Msg.Sessions)
			if err != nil {
				return fmt.Errorf("failed to format output: %w", err)
			}

			if err := WriteOutput(os.Stdout, output); err != nil {
				return fmt.Errorf("failed to write output: %w", err)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&serviceName, "service", "s", "", "Filter by service name")
	cmd.Flags().StringVar(&status, "status", "", "Filter by status")
	cmd.Flags().StringVar(&format, "format", "text", "Output format (text, json, csv)")

	return cmd
}
