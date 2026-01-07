package debug

import (
	"fmt"
	"sort"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
)

// EventPair represents a matched entry/exit pair of uprobe events.
type EventPair struct {
	Entry    *agentv1.UprobeEvent
	Exit     *agentv1.UprobeEvent
	Duration time.Duration
}

// CallStackFrame represents a function call in the call stack.
type CallStackFrame struct {
	FunctionName string
	EntryTime    time.Time
	Children     []*CallStackFrame
	TotalTime    time.Duration
	SelfTime     time.Duration
	CallCount    int64
}

// BuildCallTreeFromEvents constructs a call tree from uprobe entry/exit events.
func BuildCallTreeFromEvents(events []*agentv1.UprobeEvent, p95Duration time.Duration) *debugpb.CallTree {
	if len(events) == 0 {
		return nil
	}

	// Group events by thread ID
	eventsByThread := groupEventsByThread(events)

	// Build call stacks for each thread
	var allRoots []*CallStackFrame
	totalInvocations := int64(0)

	for _, threadEvents := range eventsByThread {
		roots := buildCallStacksForThread(threadEvents)
		allRoots = append(allRoots, roots...)
		totalInvocations += int64(len(roots))
	}

	if len(allRoots) == 0 {
		return nil
	}

	// Aggregate all roots into a single tree
	aggregatedRoot := aggregateCallStacks(allRoots)

	// Convert to protobuf format
	pbRoot := convertToProtoNode(aggregatedRoot, p95Duration)

	return &debugpb.CallTree{
		Root:             pbRoot,
		TotalInvocations: totalInvocations,
	}
}

// groupEventsByThread groups events by their thread ID.
func groupEventsByThread(events []*agentv1.UprobeEvent) map[int32][]*agentv1.UprobeEvent {
	grouped := make(map[int32][]*agentv1.UprobeEvent)

	for _, event := range events {
		tid := event.Tid
		grouped[tid] = append(grouped[tid], event)
	}

	// Sort events within each thread by timestamp
	for tid := range grouped {
		sort.Slice(grouped[tid], func(i, j int) bool {
			return grouped[tid][i].Timestamp.AsTime().Before(grouped[tid][j].Timestamp.AsTime())
		})
	}

	return grouped
}

// buildCallStacksForThread builds call stacks from a thread's events.
func buildCallStacksForThread(events []*agentv1.UprobeEvent) []*CallStackFrame {
	var roots []*CallStackFrame
	var stack []*CallStackFrame

	for _, event := range events {
		if event.EventType == "entry" {
			// Push new frame onto stack
			frame := &CallStackFrame{
				FunctionName: event.FunctionName,
				EntryTime:    event.Timestamp.AsTime(),
				Children:     make([]*CallStackFrame, 0),
				CallCount:    1,
			}

			if len(stack) > 0 {
				// Add as child of current stack top
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, frame)
			} else {
				// This is a root call
				roots = append(roots, frame)
			}

			stack = append(stack, frame)

		} else if event.EventType == "return" {
			// Pop frame from stack
			if len(stack) == 0 {
				// Unmatched return event, skip
				continue
			}

			frame := stack[len(stack)-1]
			stack = stack[:len(stack)-1]

			// Calculate duration
			exitTime := event.Timestamp.AsTime()
			frame.TotalTime = exitTime.Sub(frame.EntryTime)

			// Calculate self time (total time minus children time)
			childrenTime := time.Duration(0)
			for _, child := range frame.Children {
				childrenTime += child.TotalTime
			}
			frame.SelfTime = frame.TotalTime - childrenTime
		}
	}

	return roots
}

// aggregateCallStacks merges multiple call stacks into a single aggregated tree.
func aggregateCallStacks(roots []*CallStackFrame) *CallStackFrame {
	if len(roots) == 0 {
		return nil
	}

	if len(roots) == 1 {
		return roots[0]
	}

	// Create a map to aggregate by function name
	aggregated := make(map[string]*CallStackFrame)

	for _, root := range roots {
		key := root.FunctionName
		if existing, ok := aggregated[key]; ok {
			// Merge with existing
			existing.CallCount += root.CallCount
			existing.TotalTime += root.TotalTime
			existing.SelfTime += root.SelfTime
			existing.Children = mergeChildren(existing.Children, root.Children)
		} else {
			// Add new
			aggregated[key] = root
		}
	}

	// If there's only one unique root function, return it
	if len(aggregated) == 1 {
		for _, frame := range aggregated {
			return frame
		}
	}

	// If there are multiple root functions, create a synthetic root
	syntheticRoot := &CallStackFrame{
		FunctionName: "(multiple entry points)",
		Children:     make([]*CallStackFrame, 0, len(aggregated)),
		CallCount:    0,
		TotalTime:    0,
		SelfTime:     0,
	}

	for _, frame := range aggregated {
		syntheticRoot.Children = append(syntheticRoot.Children, frame)
		syntheticRoot.CallCount += frame.CallCount
		syntheticRoot.TotalTime += frame.TotalTime
	}

	return syntheticRoot
}

// mergeChildren merges two lists of children, aggregating by function name.
func mergeChildren(children1, children2 []*CallStackFrame) []*CallStackFrame {
	merged := make(map[string]*CallStackFrame)

	// Add all children from first list
	for _, child := range children1 {
		merged[child.FunctionName] = child
	}

	// Merge or add children from second list
	for _, child := range children2 {
		if existing, ok := merged[child.FunctionName]; ok {
			existing.CallCount += child.CallCount
			existing.TotalTime += child.TotalTime
			existing.SelfTime += child.SelfTime
			existing.Children = mergeChildren(existing.Children, child.Children)
		} else {
			merged[child.FunctionName] = child
		}
	}

	// Convert map back to slice
	result := make([]*CallStackFrame, 0, len(merged))
	for _, frame := range merged {
		result = append(result, frame)
	}

	// Sort by total time descending
	sort.Slice(result, func(i, j int) bool {
		return result[i].TotalTime > result[j].TotalTime
	})

	return result
}

// convertToProtoNode converts a CallStackFrame to a protobuf CallTreeNode.
func convertToProtoNode(frame *CallStackFrame, p95Duration time.Duration) *debugpb.CallTreeNode {
	if frame == nil {
		return nil
	}

	node := &debugpb.CallTreeNode{
		FunctionName:  frame.FunctionName,
		TotalDuration: durationpb.New(frame.TotalTime),
		SelfDuration:  durationpb.New(frame.SelfTime),
		CallCount:     frame.CallCount,
		IsSlow:        frame.TotalTime > p95Duration,
		Children:      make([]*debugpb.CallTreeNode, 0, len(frame.Children)),
	}

	for _, child := range frame.Children {
		node.Children = append(node.Children, convertToProtoNode(child, p95Duration))
	}

	return node
}

// AggregateStatistics computes statistics from uprobe events.
func AggregateStatistics(events []*agentv1.UprobeEvent) *debugpb.DebugStatistics {
	if len(events) == 0 {
		return &debugpb.DebugStatistics{}
	}

	// Collect all durations from return events
	var durations []time.Duration
	for _, event := range events {
		if event.EventType == "return" && event.DurationNs > 0 {
			//nolint:gosec // G115: Duration conversion is safe
			durations = append(durations, time.Duration(event.DurationNs))
		}
	}

	if len(durations) == 0 {
		return &debugpb.DebugStatistics{
			TotalCalls: int64(len(events) / 2), // Approximate: entry + exit
		}
	}

	// Sort durations
	sort.Slice(durations, func(i, j int) bool {
		return durations[i] < durations[j]
	})

	// Calculate percentiles
	p50 := percentile(durations, 0.50)
	p95 := percentile(durations, 0.95)
	p99 := percentile(durations, 0.99)
	max := durations[len(durations)-1]

	return &debugpb.DebugStatistics{
		TotalCalls:  int64(len(durations)),
		DurationP50: durationpb.New(p50),
		DurationP95: durationpb.New(p95),
		DurationP99: durationpb.New(p99),
		DurationMax: durationpb.New(max),
	}
}

// percentile calculates the percentile value from a sorted slice of durations.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}

	index := int(float64(len(sorted)) * p)
	if index >= len(sorted) {
		index = len(sorted) - 1
	}

	return sorted[index]
}

// FindSlowOutliers identifies events that exceed the P95 threshold.
func FindSlowOutliers(events []*agentv1.UprobeEvent, p95Duration time.Duration) []*debugpb.SlowOutlier {
	var outliers []*debugpb.SlowOutlier

	for _, event := range events {
		if event.EventType == "return" && event.DurationNs > 0 {
			//nolint:gosec // G115: Duration conversion is safe
			duration := time.Duration(event.DurationNs)
			if duration > p95Duration {
				outlier := &debugpb.SlowOutlier{
					Duration:  durationpb.New(duration),
					Timestamp: event.Timestamp,
					Labels: map[string]string{
						"function": event.FunctionName,
						"pid":      fmt.Sprintf("%d", event.Pid),
						"tid":      fmt.Sprintf("%d", event.Tid),
					},
				}
				outliers = append(outliers, outlier)
			}
		}
	}

	// Sort by duration descending
	sort.Slice(outliers, func(i, j int) bool {
		return outliers[i].Duration.AsDuration() > outliers[j].Duration.AsDuration()
	})

	// Limit to top 10 outliers
	if len(outliers) > 10 {
		outliers = outliers[:10]
	}

	return outliers
}
