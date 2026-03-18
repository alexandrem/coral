package query

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/cli/helpers"
)

// logEntryJSON is the JSON representation of a single log entry.
type logEntryJSON struct {
	Service    string            `json:"service"`
	Level      string            `json:"level"`
	Message    string            `json:"message"`
	TraceID    string            `json:"trace_id,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// logsResponseJSON is the JSON output for coral query logs.
type logsResponseJSON struct {
	TotalLogs int32          `json:"total_logs"`
	Logs      []logEntryJSON `json:"logs"`
}

func NewLogsCmd() *cobra.Command {
	var (
		since   string
		level   string
		search  string
		maxLogs int
		format  string
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
  coral query logs api --format json          # JSON output
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			service := ""
			if len(args) > 0 {
				service = args[0]
			}

			ctx := context.Background()

			// Create colony client.
			client, err := helpers.GetColonyClient("")
			if err != nil {
				return fmt.Errorf("failed to create colony client: %w", err)
			}

			// Execute RPC.
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

			if format == "json" {
				return printLogsJSON(resp.Msg)
			}

			// Print result.
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
	cmd.Flags().StringVar(&format, "format", "text", "Output format (text, json)")

	return cmd
}

// printLogsJSON outputs logs as JSON.
func printLogsJSON(msg *colonypb.QueryUnifiedLogsResponse) error {
	if msg.TotalLogs == 0 {
		fmt.Println(`{"total_logs":0,"logs":[]}`)
		return nil
	}

	out := logsResponseJSON{
		TotalLogs: msg.TotalLogs,
		Logs:      make([]logEntryJSON, 0, len(msg.Logs)),
	}
	for _, l := range msg.Logs {
		out.Logs = append(out.Logs, logEntryJSON{
			Service:    l.ServiceName,
			Level:      l.Level,
			Message:    l.Message,
			TraceID:    l.TraceId,
			Attributes: l.Attributes,
		})
	}

	return json.NewEncoder(os.Stdout).Encode(out)
}
