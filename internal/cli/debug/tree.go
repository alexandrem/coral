package debug

import (
	"fmt"
	"strings"
	"time"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
)

// RenderCallTree renders a call tree in ASCII art format.
func RenderCallTree(results *colonypb.GetDebugResultsResponse) string {
	if results == nil {
		return "No call tree data available.\n"
	}

	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("\n📊 Call Tree for %s\n", results.Function))
	buf.WriteString(strings.Repeat("=", 60) + "\n\n")

	// Summary statistics
	if results.Statistics != nil {
		buf.WriteString(fmt.Sprintf("Total invocations: %d\n",
			results.CallTree.GetTotalInvocations()))
		buf.WriteString(fmt.Sprintf("P50: %s | P95: %s | P99: %s | Max: %s\n\n",
			formatDuration(results.Statistics.DurationP50.AsDuration()),
			formatDuration(results.Statistics.DurationP95.AsDuration()),
			formatDuration(results.Statistics.DurationP99.AsDuration()),
			formatDuration(results.Statistics.DurationMax.AsDuration()),
		))
	}

	// Render call tree
	if results.CallTree != nil && results.CallTree.Root != nil {
		totalDuration := results.CallTree.Root.TotalDuration.AsDuration()
		buf.WriteString(renderCallTreeNode(results.CallTree.Root, "", true, totalDuration))
	} else {
		buf.WriteString("No call tree data captured.\n")
	}

	// Show slow outliers if present
	if len(results.SlowOutliers) > 0 {
		buf.WriteString("\n🐌 Slow Outliers:\n")
		for i, outlier := range results.SlowOutliers {
			if i >= 5 {
				break
			}
			buf.WriteString(fmt.Sprintf("  %d. %s at %s\n",
				i+1,
				formatDuration(outlier.Duration.AsDuration()),
				outlier.Timestamp.AsTime().Format(time.RFC3339),
			))
		}
	}

	// Legend
	buf.WriteString("\n" + renderLegend())

	return buf.String()
}

// renderCallTreeNode renders a single call tree node with proper indentation.
func renderCallTreeNode(node *colonypb.CallTreeNode, prefix string, isLast bool, totalDuration time.Duration) string {
	var buf strings.Builder

	// Determine the connector
	connector := "├─"
	if isLast {
		connector = "└─"
	}

	// Calculate percentage
	percentage := 0.0
	if totalDuration > 0 {
		percentage = (float64(node.TotalDuration.AsDuration()) / float64(totalDuration)) * 100
	}

	// Slow marker
	slowMarker := ""
	if node.IsSlow {
		slowMarker = " ← SLOW"
	}

	// Render current node
	buf.WriteString(fmt.Sprintf("%s%s %s (%s, %d calls, %.1f%%)%s\n",
		prefix,
		connector,
		node.FunctionName,
		formatDuration(node.TotalDuration.AsDuration()),
		node.CallCount,
		percentage,
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
		buf.WriteString(renderCallTreeNode(child, childPrefix, isLastChild, totalDuration))
	}

	return buf.String()
}

// formatDuration formats a duration in a human-readable way.
func formatDuration(d time.Duration) string {
	if d < time.Microsecond {
		return fmt.Sprintf("%dns", d.Nanoseconds())
	} else if d < time.Millisecond {
		return fmt.Sprintf("%.1fµs", float64(d.Nanoseconds())/1000)
	} else if d < time.Second {
		return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}

// renderLegend renders the legend for the call tree.
func renderLegend() string {
	return `Legend:
  ├─ = intermediate node    │  = continuation
  └─ = last child           ← SLOW = exceeds P95 threshold
`
}
