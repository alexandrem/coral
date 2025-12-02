package debug

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/durationpb"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
)

func NewTraceCmd() *cobra.Command {
	var (
		path       string
		duration   time.Duration
		colonyAddr string
		format     string
		wait       bool
	)

	cmd := &cobra.Command{
		Use:   "trace <service>",
		Short: "Trace request path",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			serviceName := args[0]
			ctx := context.Background()

			client := colonyv1connect.NewDebugServiceClient(
				http.DefaultClient,
				colonyAddr,
			)

			if format == "text" {
				fmt.Printf("🔍 Tracing %s on %s for %s...\n", path, serviceName, duration)
			}

			req := &colonypb.TraceRequestPathRequest{
				ServiceName: serviceName,
				Path:        path,
				Duration:    durationpb.New(duration),
			}

			resp, err := client.TraceRequestPath(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to start trace: %w", err)
			}

			if !resp.Msg.Success {
				return fmt.Errorf("failed to start trace: %s", resp.Msg.Error)
			}

			sessionID := resp.Msg.SessionId

			if format == "text" {
				fmt.Printf("✓ Trace session started (id: %s)\n", sessionID)
			}

			// If --wait flag is set, wait for trace to complete and show results
			if wait {
				if format == "text" {
					fmt.Printf("⏳ Waiting for trace to complete (%s)...\n", duration)
				}

				// Wait for the trace duration
				time.Sleep(duration)

				// Fetch results
				resultsReq := &colonypb.GetDebugResultsRequest{
					SessionId:   sessionID,
					Format:      "summary",
					ServiceName: serviceName,
				}

				resultsResp, err := client.GetDebugResults(ctx, connect.NewRequest(resultsReq))
				if err != nil {
					return fmt.Errorf("failed to get trace results: %w", err)
				}

				// Display results
				if format == "text" {
					tree := RenderCallTree(resultsResp.Msg)
					fmt.Print(tree)
				} else {
					formatter := NewFormatter(OutputFormat(format))
					output, err := formatter.FormatResults(resultsResp.Msg)
					if err != nil {
						return fmt.Errorf("failed to format output: %w", err)
					}
					if err := WriteOutput(os.Stdout, output); err != nil {
						return fmt.Errorf("failed to write output: %w", err)
					}
				}
			} else {
				if format == "text" {
					fmt.Println("  Use 'coral debug list' to check status.")
					fmt.Printf("  Use 'coral debug query %s --session-id %s' to view results.\n", serviceName, sessionID)
				} else {
					// For non-text formats, just output the session info
					_, err = fmt.Fprintf(os.Stdout, "{\"session_id\": \"%s\", \"path\": \"%s\", "+
						"\"duration\": \"%s\"}\n",
						sessionID, path, duration)
					if err != nil {
						return fmt.Errorf("failed to write output: %w", err)
					}
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&path, "path", "p", "", "HTTP path to trace (required)")
	cmd.Flags().DurationVarP(&duration, "duration", "d", 60*time.Second, "Duration of the trace session")
	cmd.Flags().StringVar(&colonyAddr, "colony-addr", "http://localhost:8081", "Colony address")
	cmd.Flags().StringVar(&format, "format", "text", "Output format (text, json, csv)")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for trace to complete and display results")

	if err := cmd.MarkFlagRequired("path"); err != nil {
		fmt.Printf("failed to mark flag as required: %v\n", err)
	}

	return cmd
}
