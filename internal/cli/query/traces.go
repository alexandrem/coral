package query

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/cli/helpers"
)

// traceSpanJSON is the JSON representation of a single span.
type traceSpanJSON struct {
	TraceID    string            `json:"trace_id"`
	SpanName   string            `json:"span_name"`
	Service    string            `json:"service"`
	DurationMs float64           `json:"duration_ms"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// tracesResponseJSON is the JSON output for coral query traces.
type tracesResponseJSON struct {
	TotalTraces int32           `json:"total_traces"`
	TotalSpans  int             `json:"total_spans"`
	Spans       []traceSpanJSON `json:"spans"`
}

func NewTracesCmd() *cobra.Command {
	var (
		since     string
		traceID   string
		source    string
		minDurMs  int
		maxTraces int
		format    string
	)

	cmd := &cobra.Command{
		Use:   "traces [service]",
		Short: "Query distributed traces",
		Long: `Query distributed traces from all sources (eBPF + OTLP).

Returns unified trace data with source annotations.

Examples:
  coral query traces api                           # All traces for api service
  coral query traces --trace-id abc123             # Specific trace
  coral query traces api --source ebpf             # Only eBPF traces
  coral query traces api --min-duration-ms 500     # Only slow traces
  coral query traces api --format json             # JSON output
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
			req := &colonypb.QueryUnifiedTracesRequest{
				Service:       service,
				TimeRange:     since,
				Source:        source,
				TraceId:       traceID,
				MinDurationMs: int32(minDurMs),
				MaxTraces:     int32(maxTraces),
			}

			resp, err := client.QueryUnifiedTraces(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to query traces: %w", err)
			}

			if format == "json" {
				return printTracesJSON(resp.Msg.Spans, resp.Msg.TotalTraces)
			}

			// Print result.
			if len(resp.Msg.Spans) == 0 {
				fmt.Println("No traces found for the specified criteria")
				return nil
			}

			// Group spans by trace ID.
			traceGroups := make(map[string][]*agentv1.EbpfTraceSpan)
			for _, span := range resp.Msg.Spans {
				traceGroups[span.TraceId] = append(traceGroups[span.TraceId], span)
			}

			fmt.Printf("Found %d spans across %d traces:\n\n", len(resp.Msg.Spans), resp.Msg.TotalTraces)

			for traceID, traceSpans := range traceGroups {
				fmt.Printf("Trace: %s (%d spans)\n", traceID, len(traceSpans))
				for _, span := range traceSpans {
					durationMs := float64(span.DurationUs) / 1000.0
					sourceIcon := "📍" // Default eBPF
					if span.ServiceName != "" && len(span.ServiceName) > 6 {
						if span.ServiceName[len(span.ServiceName)-6:] == "[OTLP]" {
							sourceIcon = "📊" // OTLP data
						}
					}

					fmt.Printf("  %s %s: %s (%.2fms)\n",
						sourceIcon, span.ServiceName, span.SpanName, durationMs)

					// Show OTLP attributes if present.
					if source, ok := span.Attributes["source"]; ok && source == "OTLP" {
						fmt.Printf("     Aggregated: %s spans, %s errors\n",
							span.Attributes["total_spans"], span.Attributes["error_count"])
					}
				}
				fmt.Println()
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&since, "since", "1h", "Time range (e.g., 1h, 30m, 24h)")
	cmd.Flags().StringVar(&traceID, "trace-id", "", "Specific trace ID")
	cmd.Flags().StringVar(&source, "source", "all", "Data source: ebpf, telemetry, or all")
	cmd.Flags().IntVar(&minDurMs, "min-duration-ms", 0, "Minimum trace duration in milliseconds")
	cmd.Flags().IntVar(&maxTraces, "max-traces", 10, "Maximum number of traces to return")
	cmd.Flags().StringVar(&format, "format", "text", "Output format (text, json)")

	return cmd
}

// printTracesJSON outputs trace spans as JSON.
func printTracesJSON(spans []*agentv1.EbpfTraceSpan, totalTraces int32) error {
	if len(spans) == 0 {
		fmt.Println(`{"total_traces":0,"total_spans":0,"spans":[]}`)
		return nil
	}

	out := tracesResponseJSON{
		TotalTraces: totalTraces,
		TotalSpans:  len(spans),
		Spans:       make([]traceSpanJSON, 0, len(spans)),
	}
	for _, s := range spans {
		out.Spans = append(out.Spans, traceSpanJSON{
			TraceID:    s.TraceId,
			SpanName:   s.SpanName,
			Service:    s.ServiceName,
			DurationMs: float64(s.DurationUs) / 1000.0,
			Attributes: s.Attributes,
		})
	}

	return json.NewEncoder(os.Stdout).Encode(out)
}
