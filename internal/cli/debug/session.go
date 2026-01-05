package debug

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
)

func NewSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Manage debug sessions",
		Long:  "Manage debug sessions, including listing, inspecting, and stopping them.",
	}

	cmd.AddCommand(newSessionListCmd())
	cmd.AddCommand(newSessionGetCmd())
	cmd.AddCommand(newSessionQueryCmd())
	cmd.AddCommand(newSessionEventsCmd())
	cmd.AddCommand(newSessionStopCmd())

	return cmd
}

func newSessionListCmd() *cobra.Command {
	var (
		serviceName string
		status      string
		format      string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List active debug sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			colonyAddr, err := getColonyURL()
			if err != nil {
				return fmt.Errorf("failed to resolve colony address: %w", err)
			}

			client := colonyv1connect.NewColonyDebugServiceClient(http.DefaultClient, colonyAddr)

			req := &colonypb.ListDebugSessionsRequest{
				ServiceName: serviceName,
				Status:      status,
			}

			resp, err := client.ListDebugSessions(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to list sessions: %w", err)
			}

			formatter := NewFormatter(OutputFormat(format))
			output, err := formatter.FormatSessions(resp.Msg.Sessions)
			if err != nil {
				return fmt.Errorf("failed to format output: %w", err)
			}

			return WriteOutput(os.Stdout, output)
		},
	}

	cmd.Flags().StringVarP(&serviceName, "service", "s", "", "Filter by service name")
	cmd.Flags().StringVar(&status, "status", "", "Filter by status")
	cmd.Flags().StringVar(&format, "format", "text", "Output format (text, json, csv)")

	return cmd
}

func newSessionGetCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "get <session-id>",
		Short: "Get session metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			ctx := context.Background()
			colonyAddr, err := getColonyURL()
			if err != nil {
				return fmt.Errorf("failed to resolve colony address: %w", err)
			}

			client := colonyv1connect.NewColonyDebugServiceClient(http.DefaultClient, colonyAddr)

			// We have to list all sessions and filter because there is no GetSession RPC
			req := &colonypb.ListDebugSessionsRequest{}
			resp, err := client.ListDebugSessions(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to list sessions: %w", err)
			}

			var session *colonypb.DebugSession
			for _, s := range resp.Msg.Sessions {
				if s.SessionId == sessionID {
					session = s
					break
				}
			}

			if session == nil {
				return fmt.Errorf("session not found: %s", sessionID)
			}

			formatter := NewFormatter(OutputFormat(format))
			output, err := formatter.FormatSessions([]*colonypb.DebugSession{session})
			if err != nil {
				return fmt.Errorf("failed to format output: %w", err)
			}

			return WriteOutput(os.Stdout, output)
		},
	}

	cmd.Flags().StringVar(&format, "format", "text", "Output format (text, json, csv)")

	return cmd
}

func newSessionEventsCmd() *cobra.Command {
	var (
		maxEvents int32
		follow    bool
		since     time.Duration
	)

	cmd := &cobra.Command{
		Use:   "events <session-id>",
		Short: "Query events from a debug session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			ctx := context.Background()
			colonyAddr, err := getColonyURL()
			if err != nil {
				return fmt.Errorf("failed to resolve colony address: %w", err)
			}

			client := colonyv1connect.NewColonyDebugServiceClient(http.DefaultClient, colonyAddr)

			var startTime *timestamppb.Timestamp
			if since > 0 {
				startTime = timestamppb.New(time.Now().Add(-since))
			}

			for {
				req := &colonypb.QueryUprobeEventsRequest{
					SessionId: sessionID,
					MaxEvents: maxEvents,
					StartTime: startTime,
				}

				resp, err := client.QueryUprobeEvents(ctx, connect.NewRequest(req))
				if err != nil {
					return fmt.Errorf("failed to query events: %w", err)
				}

				for _, event := range resp.Msg.Events {
					if startTime == nil || event.Timestamp.AsTime().After(startTime.AsTime()) {
						startTime = event.Timestamp
					}
					data, _ := json.Marshal(event)
					fmt.Println(string(data))
				}

				if !follow {
					break
				}

				time.Sleep(1 * time.Second)
			}

			return nil
		},
	}

	cmd.Flags().Int32Var(&maxEvents, "max", 100, "Max events to retrieve")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow new events")
	cmd.Flags().DurationVar(&since, "since", 0, "Show events since duration (e.g. 5m)")

	return cmd
}

func newSessionStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop <session-id>",
		Short: "Stop a debug session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			ctx := context.Background()
			colonyAddr, err := getColonyURL()
			if err != nil {
				return fmt.Errorf("failed to resolve colony address: %w", err)
			}

			client := colonyv1connect.NewColonyDebugServiceClient(http.DefaultClient, colonyAddr)

			fmt.Printf("Stopping session %s...\n", sessionID)

			req := &colonypb.DetachUprobeRequest{
				SessionId: sessionID,
			}

			resp, err := client.DetachUprobe(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to stop session: %w", err)
			}

			if !resp.Msg.Success {
				return fmt.Errorf("failed to stop session: %s", resp.Msg.Error)
			}

			fmt.Println("✓ Debug session stopped")
			return nil
		},
	}

	return cmd
}

func newSessionQueryCmd() *cobra.Command {
	var (
		functionName string
		sessionID    string
		since        time.Duration
		format       string
	)

	cmd := &cobra.Command{
		Use:   "query <service>",
		Short: "Query debug session results",
		Long: `Query debug session results by function name or session ID.

This command searches for debug sessions matching the criteria and displays
their aggregated results and statistics (call counts, duration percentiles).

Examples:
  # Query sessions for a specific function
  coral debug session query api --function processOrder --since 1h

  # Query a specific session
  coral debug session query api --session-id abc123`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			serviceName := args[0]
			ctx := context.Background()

			if functionName == "" && sessionID == "" {
				return fmt.Errorf("either --function or --session-id must be provided")
			}

			// Resolve colony URL from config.
			colonyAddr, err := getColonyURL()
			if err != nil {
				return fmt.Errorf("failed to resolve colony address: %w", err)
			}

			client := colonyv1connect.NewColonyDebugServiceClient(
				http.DefaultClient,
				colonyAddr,
			)

			if format == "text" {
				if sessionID != "" {
					fmt.Printf("🔍 Querying debug session %s...\n", sessionID)
				} else {
					fmt.Printf("🔍 Querying debug data for %s/%s (last %s)...\n", serviceName, functionName, since)
				}
			}

			var matchingSessions []*colonypb.DebugSession

			if sessionID != "" {
				// If session ID is provided, we can try to get it directly via ListDebugSessions filtering.
				// (Since GetDebugSession RPC doesn't exist yet, we list and filter)
				listReq := &colonypb.ListDebugSessionsRequest{
					ServiceName: serviceName,
				}
				listResp, err := client.ListDebugSessions(ctx, connect.NewRequest(listReq))
				if err != nil {
					return fmt.Errorf("failed to list sessions: %w", err)
				}

				for _, session := range listResp.Msg.Sessions {
					if session.SessionId == sessionID {
						matchingSessions = append(matchingSessions, session)
						break
					}
				}

				if len(matchingSessions) == 0 {
					return fmt.Errorf("session not found: %s", sessionID)
				}
			} else {
				// List sessions to find relevant ones by function.
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

				for _, session := range listResp.Msg.Sessions {
					if session.FunctionName != functionName {
						continue
					}
					// Check time range (started_at > now - since).
					if time.Since(session.StartedAt.AsTime()) > since {
						continue
					}
					matchingSessions = append(matchingSessions, session)
				}
			}

			if len(matchingSessions) == 0 {
				if format == "text" {
					fmt.Println("No sessions found matching criteria.")
				}
				return nil
			}

			// For text format, show detailed results for each session.
			if format == "text" {
				for _, session := range matchingSessions {
					// Get details for this session.
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
				// For JSON/CSV, just output the session list.
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

	cmd.Flags().StringVarP(&functionName, "function", "f", "", "Function name to query")
	cmd.Flags().StringVar(&sessionID, "session-id", "", "Session ID to query")
	cmd.Flags().DurationVar(&since, "since", 1*time.Hour, "Time range to query")
	cmd.Flags().StringVar(&format, "format", "text", "Output format (text, json, csv)")

	return cmd
}
