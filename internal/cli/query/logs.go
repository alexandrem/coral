package query

import (
	"context"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/cli/helpers"
)

func NewLogsCmd() *cobra.Command {
	var (
		since   string
		level   string
		search  string
		maxLogs int
	)

	cmd := &cobra.Command{
		Use:   "logs [service]",
		Short: "Query logs",
		Long: `Query application logs from OTLP.

Returns logs with filtering and search capabilities.

Examples:
  coral query logs api                        # All logs for api service
  coral query logs api --level error          # Only error logs
  coral query logs --search "timeout"         # Search for specific text
  coral query logs api --since 30m            # Last 30 minutes
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			service := ""
			if len(args) > 0 {
				service = args[0]
			}

			ctx := context.Background()

			// Resolve colony URL
			colonyAddr, err := helpers.GetColonyURL("")
			if err != nil {
				return fmt.Errorf("failed to resolve colony address: %w", err)
			}

			// Create colony client
			client := colonyv1connect.NewColonyServiceClient(
				http.DefaultClient,
				colonyAddr,
			)

			// Execute RPC
			req := &colonypb.QueryUnifiedLogsRequest{
				Service:   service,
				TimeRange: since,
				Level:     level,
				Search:    search,
				MaxLogs:   int32(maxLogs),
			}

			resp, err := client.QueryUnifiedLogs(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to query logs: %w", err)
			}

			// Print result
			if resp.Msg.TotalLogs == 0 {
				fmt.Println("No logs found for the specified criteria")
				return nil
			}

			fmt.Printf("Found %d logs:\n\n", resp.Msg.TotalLogs)
			for _, log := range resp.Msg.Logs {
				levelIcon := "ℹ️"
				switch log.Level {
				case "debug":
					levelIcon = "🔍"
				case "warn":
					levelIcon = "⚠️"
				case "error":
					levelIcon = "❌"
				}

				fmt.Printf("%s [%s] %s: %s\n", levelIcon, log.Level, log.ServiceName, log.Message)
				if log.TraceId != "" {
					fmt.Printf("   Trace ID: %s\n", log.TraceId)
				}
				if len(log.Attributes) > 0 {
					fmt.Println("   Attributes:")
					for k, v := range log.Attributes {
						fmt.Printf("     %s: %s\n", k, v)
					}
				}
				fmt.Println()
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&since, "since", "1h", "Time range (e.g., 1h, 30m, 24h)")
	cmd.Flags().StringVar(&level, "level", "", "Log level filter: debug, info, warn, error")
	cmd.Flags().StringVar(&search, "search", "", "Full-text search query")
	cmd.Flags().IntVar(&maxLogs, "max-logs", 100, "Maximum number of logs to return")

	return cmd
}
