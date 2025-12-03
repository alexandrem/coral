package ebpf

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/cli/helpers"
)

// NewTracesCmd creates the 'traces' command for querying eBPF trace spans.
func NewTracesCmd() *cobra.Command {
	var (
		timeFlags helpers.TimeFlags
		format    string
		traceID   string
		service   string
	)

	cmd := &cobra.Command{
		Use:   "traces",
		Short: "Query distributed trace spans",
		Long: `Query distributed trace spans collected via eBPF.

Examples:
  # Query by trace ID
  coral query ebpf traces --trace-id abc123def456

  # Query traces for a service
  coral query ebpf traces --service my-service --since 1h
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			// Parse time range
			timeRange, err := timeFlags.Parse()
			if err != nil {
				return err
			}

			// Get client
			client, err := helpers.GetAgentClient("")
			if err != nil {
				return err
			}

			// Prepare request
			req := &agentv1.QueryEbpfMetricsRequest{
				StartTime:     timeRange.Start.Unix(),
				EndTime:       timeRange.End.Unix(),
				TraceId:       traceID,
				IncludeTraces: true,
				MaxTraces:     100,
			}

			if service != "" {
				req.ServiceNames = []string{service}
			}

			// Execute query
			resp, err := client.QueryEbpfMetrics(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to query traces: %w", err)
			}

			// Process traces
			if format == "tree" {
				return renderTraceTree(resp.Msg.TraceSpans, os.Stdout)
			}

			// For table/json/csv, convert to rows
			rows := processTraceSpans(resp.Msg.TraceSpans)

			// Format output
			formatter, err := helpers.NewFormatter(helpers.OutputFormat(format))
			if err != nil {
				return err
			}

			return formatter.Format(rows, os.Stdout)
		},
	}

	timeFlags.AddFlags(cmd.Flags())
	cmd.Flags().StringVarP(&format, "format", "o", "table", "Output format (table, json, csv, tree)")
	cmd.Flags().StringVar(&traceID, "trace-id", "", "Filter by trace ID")
	cmd.Flags().StringVar(&service, "service", "", "Filter by service name")

	return cmd
}

type TraceSpanRow struct {
	TraceID   string `header:"Trace ID"`
	SpanID    string `header:"Span ID"`
	Service   string `header:"Service"`
	Operation string `header:"Operation"`
	Duration  string `header:"Duration"`
	StartTime string `header:"Start Time"`
}

func processTraceSpans(spans []*agentv1.EbpfTraceSpan) []TraceSpanRow {
	var rows []TraceSpanRow
	for _, span := range spans {
		rows = append(rows, TraceSpanRow{
			TraceID:   span.TraceId,
			SpanID:    span.SpanId,
			Service:   span.ServiceName,
			Operation: span.SpanName,
			Duration:  helpers.FormatDuration(time.Duration(span.DurationUs) * time.Microsecond),
			StartTime: time.UnixMilli(span.StartTime).Format(time.RFC3339),
		})
	}
	return rows
}

// traceSpanNode adapts agentv1.EbpfTraceSpan to helpers.TreeNode interface.
type traceSpanNode struct {
	span     *agentv1.EbpfTraceSpan
	children []*traceSpanNode
}

func (n *traceSpanNode) GetName() string {
	return fmt.Sprintf("%s [%s]", n.span.SpanName, n.span.ServiceName)
}

func (n *traceSpanNode) GetDuration() time.Duration {
	return time.Duration(n.span.DurationUs) * time.Microsecond
}

func (n *traceSpanNode) GetCallCount() int64 {
	return 1 // Each span represents one call
}

func (n *traceSpanNode) GetChildren() []helpers.TreeNode {
	children := make([]helpers.TreeNode, len(n.children))
	for i, child := range n.children {
		children[i] = child
	}
	return children
}

func (n *traceSpanNode) IsSlow() bool {
	// Mark spans > 1s as slow
	return time.Duration(n.span.DurationUs)*time.Microsecond > time.Second
}

func renderTraceTree(spans []*agentv1.EbpfTraceSpan, writer *os.File) error {
	if len(spans) == 0 {
		_, _ = fmt.Fprintln(writer, "No trace spans found.")
		return nil
	}

	// Build tree structure from spans
	spanMap := make(map[string]*traceSpanNode)
	var roots []*traceSpanNode

	// First pass: create nodes
	for _, span := range spans {
		spanMap[span.SpanId] = &traceSpanNode{span: span}
	}

	// Second pass: build parent-child relationships
	for _, span := range spans {
		node := spanMap[span.SpanId]
		if span.ParentSpanId == "" {
			roots = append(roots, node)
		} else if parent, ok := spanMap[span.ParentSpanId]; ok {
			parent.children = append(parent.children, node)
		}
	}

	// Render each trace tree
	var buf strings.Builder
	for i, root := range roots {
		if i > 0 {
			buf.WriteString("\n" + strings.Repeat("─", 60) + "\n\n")
		}
		buf.WriteString(fmt.Sprintf("📊 Trace: %s\n\n", root.span.TraceId))
		totalDuration := time.Duration(root.span.DurationUs) * time.Microsecond
		buf.WriteString(helpers.RenderTree(root, totalDuration))
	}

	_, _ = fmt.Fprint(writer, buf.String())
	return nil
}
