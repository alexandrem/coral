package debug

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/spf13/cobra"
)

func NewQueryCmd() *cobra.Command {
	var (
		functionName string
		since        time.Duration
		colonyAddr   string
	)

	cmd := &cobra.Command{
		Use:   "query <service>",
		Short: "Query historical debug data",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			serviceName := args[0]
			ctx := context.Background()

			client := colonyv1connect.NewDebugServiceClient(
				http.DefaultClient,
				colonyAddr,
			)

			fmt.Printf("Querying debug data for %s/%s (last %s)...\n", serviceName, functionName, since)

			// First, list sessions to find relevant ones (optional, but good for UX)
			// Or just call GetDebugResults if we had a session ID.
			// But the CLI command is `coral debug query <service> --function <name>`.
			// The RFD example output shows "Debug Sessions for handleCheckout (last 1 hour):"
			// This implies we need to find sessions first.
			// But `GetDebugResults` takes a session_id.
			// So we probably need to list sessions first, then get results for each?
			// Or maybe `GetDebugResults` should support filtering by function?
			// The proto I added only has `session_id`.
			// Let's use `ListDebugSessions` to find sessions, then iterate and print details.

			listReq := &colonypb.ListDebugSessionsRequest{
				ServiceName: serviceName,
				// Status: "all", // We want history
			}
			listResp, err := client.ListDebugSessions(ctx, connect.NewRequest(listReq))
			if err != nil {
				return fmt.Errorf("failed to list sessions: %w", err)
			}

			fmt.Printf("\nDebug Sessions for %s (last %s):\n\n", functionName, since)

			found := false
			for _, session := range listResp.Msg.Sessions {
				if session.FunctionName != functionName {
					continue
				}
				// Check time range (started_at > now - since)
				if time.Since(session.StartedAt.AsTime()) > since {
					continue
				}

				found = true

				// Get details for this session
				resReq := &colonypb.GetDebugResultsRequest{
					SessionId: session.SessionId,
					Format:    "summary",
				}
				resResp, err := client.GetDebugResults(ctx, connect.NewRequest(resReq))
				if err != nil {
					fmt.Printf("Session: %s (Error fetching results: %v)\n", session.SessionId, err)
					continue
				}

				fmt.Printf("Session: %s (%s, %s ago)\n",
					session.SessionId,
					resResp.Msg.Duration.AsDuration(),
					time.Since(session.StartedAt.AsTime()).Round(time.Minute),
				)

				if stats := resResp.Msg.Statistics; stats != nil {
					fmt.Printf("  Calls:        %d\n", stats.TotalCalls)
					fmt.Printf("  P50 duration: %s\n", stats.DurationP50.AsDuration())
					fmt.Printf("  P95 duration: %s\n", stats.DurationP95.AsDuration())
					fmt.Printf("  Max duration: %s\n", stats.DurationMax.AsDuration())
				}
				fmt.Println()
			}

			if !found {
				fmt.Println("No sessions found matching criteria.")
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&functionName, "function", "f", "", "Function name to query (required)")
	cmd.Flags().DurationVar(&since, "since", 1*time.Hour, "Time range to query")
	cmd.Flags().StringVar(&colonyAddr, "colony-addr", "http://localhost:8081", "Colony address")

	cmd.MarkFlagRequired("function")

	return cmd
}
