package debug

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/timestamppb"

	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
)

func TestBuildCallTreeFromEvents(t *testing.T) {
	baseTime := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		events      []*meshv1.UprobeEvent
		p95Duration time.Duration
		validate    func(t *testing.T, tree *debugpb.CallTree)
	}{
		{
			name: "Simple linear call",
			events: []*meshv1.UprobeEvent{
				// Entry A
				{
					Timestamp:    timestamppb.New(baseTime),
					EventType:    "entry",
					FunctionName: "FunctionA",
					Tid:          1,
				},
				// Entry B
				{
					Timestamp:    timestamppb.New(baseTime.Add(10 * time.Millisecond)),
					EventType:    "entry",
					FunctionName: "FunctionB",
					Tid:          1,
				},
				// Return B (took 20ms)
				{
					Timestamp:    timestamppb.New(baseTime.Add(30 * time.Millisecond)),
					EventType:    "return",
					FunctionName: "FunctionB",
					Tid:          1,
					DurationNs:   20 * 1000 * 1000,
				},
				// Return A (took 40ms)
				{
					Timestamp:    timestamppb.New(baseTime.Add(40 * time.Millisecond)),
					EventType:    "return",
					FunctionName: "FunctionA",
					Tid:          1,
					DurationNs:   40 * 1000 * 1000,
				},
			},
			p95Duration: 100 * time.Millisecond,
			validate: func(t *testing.T, tree *debugpb.CallTree) {
				assert.NotNil(t, tree)
				assert.Equal(t, int64(1), tree.TotalInvocations)
				assert.NotNil(t, tree.Root)

				// Check Root (FunctionA)
				assert.Equal(t, "FunctionA", tree.Root.FunctionName)
				assert.Equal(t, int64(1), tree.Root.CallCount)
				assert.Equal(t, 40*time.Millisecond, tree.Root.TotalDuration.AsDuration())

				// Self time = Total - Children = 40 - 20 = 20ms
				assert.Equal(t, 20*time.Millisecond, tree.Root.SelfDuration.AsDuration())

				// Check Child (FunctionB)
				assert.Len(t, tree.Root.Children, 1)
				child := tree.Root.Children[0]
				assert.Equal(t, "FunctionB", child.FunctionName)
				assert.Equal(t, int64(1), child.CallCount)
				assert.Equal(t, 20*time.Millisecond, child.TotalDuration.AsDuration())
				assert.Equal(t, 20*time.Millisecond, child.SelfDuration.AsDuration())
			},
		},
		{
			name: "Multiple invocations aggregated",
			events: []*meshv1.UprobeEvent{
				// Call 1: A -> B
				{Timestamp: timestamppb.New(baseTime), EventType: "entry", FunctionName: "A", Tid: 1},
				{Timestamp: timestamppb.New(baseTime.Add(10 * time.Millisecond)), EventType: "entry", FunctionName: "B", Tid: 1},
				{Timestamp: timestamppb.New(baseTime.Add(20 * time.Millisecond)), EventType: "return", FunctionName: "B", Tid: 1, DurationNs: 10 * 1e6},
				{Timestamp: timestamppb.New(baseTime.Add(30 * time.Millisecond)), EventType: "return", FunctionName: "A", Tid: 1, DurationNs: 30 * 1e6},

				// Call 2: A -> B (same thread, later time)
				{Timestamp: timestamppb.New(baseTime.Add(100 * time.Millisecond)), EventType: "entry", FunctionName: "A", Tid: 1},
				{Timestamp: timestamppb.New(baseTime.Add(110 * time.Millisecond)), EventType: "entry", FunctionName: "B", Tid: 1},
				{Timestamp: timestamppb.New(baseTime.Add(120 * time.Millisecond)), EventType: "return", FunctionName: "B", Tid: 1, DurationNs: 10 * 1e6},
				{Timestamp: timestamppb.New(baseTime.Add(130 * time.Millisecond)), EventType: "return", FunctionName: "A", Tid: 1, DurationNs: 30 * 1e6},
			},
			p95Duration: 100 * time.Millisecond,
			validate: func(t *testing.T, tree *debugpb.CallTree) {
				assert.NotNil(t, tree)
				assert.Equal(t, int64(2), tree.TotalInvocations)

				// Root A should have 2 calls
				assert.Equal(t, "A", tree.Root.FunctionName)
				assert.Equal(t, int64(2), tree.Root.CallCount)
				assert.Equal(t, 60*time.Millisecond, tree.Root.TotalDuration.AsDuration()) // 30 + 30

				// Child B should have 2 calls
				assert.Len(t, tree.Root.Children, 1)
				child := tree.Root.Children[0]
				assert.Equal(t, "B", child.FunctionName)
				assert.Equal(t, int64(2), child.CallCount)
				assert.Equal(t, 20*time.Millisecond, child.TotalDuration.AsDuration()) // 10 + 10
			},
		},
		{
			name: "Slow outlier identification",
			events: []*meshv1.UprobeEvent{
				{Timestamp: timestamppb.New(baseTime), EventType: "entry", FunctionName: "Fast", Tid: 1},
				{Timestamp: timestamppb.New(baseTime.Add(10 * time.Millisecond)), EventType: "return", FunctionName: "Fast", Tid: 1, DurationNs: 10 * 1e6},

				{Timestamp: timestamppb.New(baseTime.Add(20 * time.Millisecond)), EventType: "entry", FunctionName: "Slow", Tid: 1},
				{Timestamp: timestamppb.New(baseTime.Add(120 * time.Millisecond)), EventType: "return", FunctionName: "Slow", Tid: 1, DurationNs: 100 * 1e6},
			},
			p95Duration: 50 * time.Millisecond,
			validate: func(t *testing.T, tree *debugpb.CallTree) {
				// We expect a synthetic root because there are two top-level functions (Fast and Slow)
				// or if we treat them as separate invocations, they might be aggregated if they were the same function.
				// Here they are different functions, so we expect a synthetic root.

				assert.Equal(t, "(multiple entry points)", tree.Root.FunctionName)
				assert.Len(t, tree.Root.Children, 2)

				// Find Slow child
				var slowNode *debugpb.CallTreeNode
				for _, child := range tree.Root.Children {
					if child.FunctionName == "Slow" {
						slowNode = child
						break
					}
				}

				assert.NotNil(t, slowNode)
				assert.True(t, slowNode.IsSlow)
				assert.Equal(t, 100*time.Millisecond, slowNode.TotalDuration.AsDuration())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree := BuildCallTreeFromEvents(tt.events, tt.p95Duration)
			tt.validate(t, tree)
		})
	}
}

func TestAggregateStatistics(t *testing.T) {
	events := []*meshv1.UprobeEvent{
		{EventType: "return", DurationNs: 10 * 1e6},
		{EventType: "return", DurationNs: 20 * 1e6},
		{EventType: "return", DurationNs: 30 * 1e6},
		{EventType: "return", DurationNs: 100 * 1e6}, // Outlier
		{EventType: "entry"},                         // Should be ignored
	}

	stats := AggregateStatistics(events)

	assert.Equal(t, int64(4), stats.TotalCalls)
	assert.Equal(t, 30*time.Millisecond, stats.DurationP50.AsDuration()) // Implementation uses index int(N*P), so int(4*0.5)=2 -> 30ms
	// Our percentile implementation uses nearest rank: index = 4 * 0.5 = 2 -> index 2 (0-based) -> 30ms?
	// Let's check the implementation: index = int(4 * 0.5) = 2. sorted[2] is 30ms.
	// Wait, sorted is 10, 20, 30, 100. Index 2 is 30.

	assert.Equal(t, 100*time.Millisecond, stats.DurationMax.AsDuration())
}
