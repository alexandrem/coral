package debug

import (
	"fmt"
	"strings"
	"time"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/cli/helpers"
)

// callTreeNodeAdapter adapts colonypb.CallTreeNode to helpers.TreeNode interface.
type callTreeNodeAdapter struct {
	node *colonypb.CallTreeNode
}

func (a *callTreeNodeAdapter) GetName() string {
	return a.node.FunctionName
}

func (a *callTreeNodeAdapter) GetDuration() time.Duration {
	return a.node.TotalDuration.AsDuration()
}

func (a *callTreeNodeAdapter) GetCallCount() int64 {
	return int64(a.node.CallCount)
}

func (a *callTreeNodeAdapter) GetChildren() []helpers.TreeNode {
	children := make([]helpers.TreeNode, len(a.node.Children))
	for i, child := range a.node.Children {
		children[i] = &callTreeNodeAdapter{node: child}
	}
	return children
}

func (a *callTreeNodeAdapter) IsSlow() bool {
	return a.node.IsSlow
}

// RenderCallTree renders a call tree in ASCII art format.
func RenderCallTree(results *colonypb.GetDebugResultsResponse) string {
	if results == nil {
		return "No call tree data available.\n"
	}

	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("\n📊 Call Tree for %s\n", results.Function))
	buf.WriteString(strings.Repeat("=", 60) + "\n\n")

	// Process Info (RFD 064)
	if results.ProcessId != 0 {
		buf.WriteString(fmt.Sprintf("Process ID:   %d\n", results.ProcessId))
	}
	if results.BinaryPath != "" {
		buf.WriteString(fmt.Sprintf("Binary Path:  %s\n", results.BinaryPath))
	}
	if results.ProcessId != 0 || results.BinaryPath != "" {
		buf.WriteString("\n")
	}

	// Summary statistics
	if results.Statistics != nil {
		buf.WriteString(fmt.Sprintf("Total invocations: %d\n",
			results.CallTree.GetTotalInvocations()))
		buf.WriteString(fmt.Sprintf("P50: %s | P95: %s | P99: %s | Max: %s\n\n",
			helpers.FormatDuration(results.Statistics.DurationP50.AsDuration()),
			helpers.FormatDuration(results.Statistics.DurationP95.AsDuration()),
			helpers.FormatDuration(results.Statistics.DurationP99.AsDuration()),
			helpers.FormatDuration(results.Statistics.DurationMax.AsDuration()),
		))
	}

	// Render call tree using helpers
	if results.CallTree != nil && results.CallTree.Root != nil {
		totalDuration := results.CallTree.Root.TotalDuration.AsDuration()
		adapter := &callTreeNodeAdapter{node: results.CallTree.Root}
		buf.WriteString(helpers.RenderTree(adapter, totalDuration))
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
				helpers.FormatDuration(outlier.Duration.AsDuration()),
				outlier.Timestamp.AsTime().Format(time.RFC3339),
			))
		}
	}

	return buf.String()
}
