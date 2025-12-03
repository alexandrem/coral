package helpers

import (
	"fmt"
	"strings"
	"time"
)

// TreeNode represents a node in a tree structure for rendering.
type TreeNode interface {
	GetName() string
	GetDuration() time.Duration
	GetCallCount() int64
	GetChildren() []TreeNode
	IsSlow() bool
}

// RenderTree renders a tree structure in ASCII art format.
// totalDuration is used to calculate percentages.
func RenderTree(root TreeNode, totalDuration time.Duration) string {
	if root == nil {
		return "No tree data available.\n"
	}

	var buf strings.Builder
	buf.WriteString(renderTreeNode(root, "", true, totalDuration))
	buf.WriteString("\n" + renderTreeLegend())
	return buf.String()
}

// renderTreeNode renders a single tree node with proper indentation.
func renderTreeNode(node TreeNode, prefix string, isLast bool, totalDuration time.Duration) string {
	var buf strings.Builder

	// Determine the connector
	connector := "├─"
	if isLast {
		connector = "└─"
	}

	// Calculate percentage
	percentage := 0.0
	if totalDuration > 0 {
		percentage = (float64(node.GetDuration()) / float64(totalDuration)) * 100
	}

	// Slow marker
	slowMarker := ""
	if node.IsSlow() {
		slowMarker = " ← SLOW"
	}

	// Render current node
	buf.WriteString(fmt.Sprintf("%s%s %s (%s, %d calls, %.1f%%)%s\n",
		prefix,
		connector,
		node.GetName(),
		FormatDuration(node.GetDuration()),
		node.GetCallCount(),
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
	children := node.GetChildren()
	for i, child := range children {
		isLastChild := i == len(children)-1
		buf.WriteString(renderTreeNode(child, childPrefix, isLastChild, totalDuration))
	}

	return buf.String()
}

// FormatDuration formats a duration in a human-readable way.
func FormatDuration(d time.Duration) string {
	if d < time.Microsecond {
		return fmt.Sprintf("%dns", d.Nanoseconds())
	} else if d < time.Millisecond {
		return fmt.Sprintf("%.1fµs", float64(d.Nanoseconds())/1000)
	} else if d < time.Second {
		return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}

// renderTreeLegend renders the legend for the tree.
func renderTreeLegend() string {
	return `Legend:
  ├─ = intermediate node    │  = continuation
  └─ = last child           ← SLOW = exceeds P95 threshold
`
}
