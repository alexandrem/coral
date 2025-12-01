package debug

import (
	"fmt"
	"strings"
	"time"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
)

// CallTreeNode represents a node in the call tree.
type CallTreeNode struct {
	Function   string
	Duration   time.Duration
	Calls      int64
	Children   []*CallTreeNode
	IsSlow     bool
	Percentage float64
}

// RenderCallTree renders a call tree in ASCII art format.
func RenderCallTree(results *colonypb.GetDebugResultsResponse) string {
	if results == nil || results.Statistics == nil {
		return "No call tree data available.\n"
	}

	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("\n📊 Call Tree for %s\n", results.Function))
	buf.WriteString(strings.Repeat("=", 60) + "\n\n")

	// For now, we'll render a simple tree structure
	// In a full implementation, this would parse actual call graph data
	buf.WriteString(fmt.Sprintf("%s (entry)\n", results.Function))
	buf.WriteString(fmt.Sprintf("  Total calls: %d\n", results.Statistics.TotalCalls))
	buf.WriteString(fmt.Sprintf("  P95 duration: %s\n", results.Statistics.DurationP95.AsDuration()))
	buf.WriteString(fmt.Sprintf("  Max duration: %s\n", results.Statistics.DurationMax.AsDuration()))

	if len(results.SlowOutliers) > 0 {
		buf.WriteString("\n🐌 Slow Outliers:\n")
		for i, outlier := range results.SlowOutliers {
			if i >= 5 {
				break
			}
			buf.WriteString(fmt.Sprintf("  %d. %s at %s\n",
				i+1,
				outlier.Duration.AsDuration(),
				outlier.Timestamp.AsTime().Format(time.RFC3339),
			))
		}
	}

	return buf.String()
}

// renderNode renders a single node in the tree with proper indentation and connectors.
func renderNode(node *CallTreeNode, prefix string, isLast bool) string {
	var buf strings.Builder

	// Determine the connector
	connector := "├─"
	if isLast {
		connector = "└─"
	}

	// Render current node
	slowMarker := ""
	if node.IsSlow {
		slowMarker = " ← SLOW"
	}

	buf.WriteString(fmt.Sprintf("%s%s %s (%.1fms, %d calls)%s\n",
		prefix,
		connector,
		node.Function,
		node.Duration.Seconds()*1000,
		node.Calls,
		slowMarker,
	))

	// Prepare prefix for children
	childPrefix := prefix
	if isLast {
		childPrefix += "  "
	} else {
		childPrefix += "│ "
	}

	// Render children
	for i, child := range node.Children {
		isLastChild := i == len(node.Children)-1
		buf.WriteString(renderNode(child, childPrefix, isLastChild))
	}

	return buf.String()
}

// RenderSimpleTree renders a simple ASCII tree (used when full call graph isn't available).
func RenderSimpleTree(functionName string, stats *colonypb.DebugStatistics) string {
	var buf strings.Builder

	buf.WriteString("\n📊 Function Analysis\n")
	buf.WriteString(strings.Repeat("=", 60) + "\n\n")
	buf.WriteString(fmt.Sprintf("%s (entry point)\n", functionName))
	buf.WriteString(fmt.Sprintf("  ├─ Total calls: %d\n", stats.TotalCalls))
	buf.WriteString(fmt.Sprintf("  ├─ P50: %s\n", stats.DurationP50.AsDuration()))
	buf.WriteString(fmt.Sprintf("  ├─ P95: %s\n", stats.DurationP95.AsDuration()))
	buf.WriteString(fmt.Sprintf("  ├─ P99: %s\n", stats.DurationP99.AsDuration()))
	buf.WriteString(fmt.Sprintf("  └─ Max: %s\n", stats.DurationMax.AsDuration()))

	return buf.String()
}
