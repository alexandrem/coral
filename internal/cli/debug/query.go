package debug

import (
	"context"
	"fmt"
	"net/http"
	"os"
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
		format       string
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

			if format == "text" {
				fmt.Printf("🔍 Querying debug data for %s/%s (last %s)...\n", serviceName, functionName, since)
			}

			// List sessions to find relevant ones
			listReq := &colonypb.ListDebugSessionsRequest{
				ServiceName: serviceName,
			}
			listResp, err := client.ListDebugSessions(ctx, connect.NewRequest(listReq))
			if err != nil {
				return fmt.Errorf("failed to list sessions: %w", err)
			}

			if format == "text" {
				fmt.Printf("\nDebug Sessions for %s (last %s):\n\n", functionName, since)
			}

			var matchingSessions []*colonypb.DebugSession
			for _, session := range listResp.Msg.Sessions {
				if session.FunctionName != functionName {
					continue
				}
				// Check time range (started_at > now - since)
				if time.Since(session.StartedAt.AsTime()) > since {
					continue
				}
				matchingSessions = append(matchingSessions, session)
			}

			if len(matchingSessions) == 0 {
				if format == "text" {
					fmt.Println("No sessions found matching criteria.")
				}
				return nil
			}

			// For text format, show detailed results for each session
			if format == "text" {
				for _, session := range matchingSessions {
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
			} else {
				// For JSON/CSV, just output the session list
				formatter := NewFormatter(OutputFormat(format))
				output, err := formatter.FormatSessions(matchingSessions)
				if err != nil {
					return fmt.Errorf("failed to format output: %w", err)
				}
				if err := WriteOutput(os.Stdout, output); err != nil {
					return fmt.Errorf("failed to write output: %w", err)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&functionName, "function", "f", "", "Function name to query (required)")
	cmd.Flags().DurationVar(&since, "since", 1*time.Hour, "Time range to query")
	cmd.Flags().StringVar(&colonyAddr, "colony-addr", "http://localhost:8081", "Colony address")
	cmd.Flags().StringVar(&format, "format", "text", "Output format (text, json, csv)")

	cmd.MarkFlagRequired("function")

	return cmd
}
