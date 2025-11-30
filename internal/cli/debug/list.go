package debug

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"text/tabwriter"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
)

func NewListCmd() *cobra.Command {
	var (
		colonyAddr  string
		serviceName string
		status      string
	)

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"sessions"},
		Short:   "List active debug sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

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

			if len(resp.Msg.Sessions) == 0 {
				fmt.Println("No active debug sessions found.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "SESSION ID\tSERVICE\tFUNCTION\tSTATUS\tEXPIRES")

			for _, session := range resp.Msg.Sessions {
				expiresIn := time.Until(session.ExpiresAt.AsTime()).Round(time.Second)
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					session.SessionId,
					session.ServiceName,
					session.FunctionName,
					session.Status,
					expiresIn,
				)
			}
			w.Flush()

			return nil
		},
	}

	cmd.Flags().StringVar(&colonyAddr, "colony-addr", "http://localhost:8081", "Colony address")
	cmd.Flags().StringVarP(&serviceName, "service", "s", "", "Filter by service name")
	cmd.Flags().StringVar(&status, "status", "", "Filter by status")

	return cmd
}
