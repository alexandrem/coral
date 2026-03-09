package helpers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
)

// AssertEventually asserts that a condition becomes true within a timeout.
// This is a helper that wraps testify's require.Eventually.
func AssertEventually(t *testing.T, condition func() bool, timeout time.Duration, interval time.Duration, msgAndArgs ...interface{}) {
	t.Helper()
	require.Eventually(t, condition, timeout, interval, msgAndArgs...)
}

// AssertNever asserts that a condition never becomes true within a duration.
// This is a helper that wraps testify's require.Never.
func AssertNever(t *testing.T, condition func() bool, duration time.Duration, interval time.Duration, msgAndArgs ...interface{}) {
	t.Helper()
	require.Never(t, condition, duration, interval, msgAndArgs...)
}

// AssertReturnEventDuration verifies that a return event has a duration within
// the expected range. tolerancePercent is relative (e.g. 0.50 = ±50%).
func AssertReturnEventDuration(t *testing.T, event *agentv1.UprobeEvent, expectedMs float64, tolerancePercent float64) {
	t.Helper()
	require.Equal(t, "return", event.EventType, "Event must be a return event")
	require.Greater(t, event.DurationNs, uint64(0), "Return event must have non-zero duration")

	durationMs := float64(event.DurationNs) / 1e6
	minMs := expectedMs * (1 - tolerancePercent)
	maxMs := expectedMs * (1 + tolerancePercent)
	assert.GreaterOrEqual(t, durationMs, minMs,
		"Duration %.2fms below expected range [%.2f, %.2f]ms", durationMs, minMs, maxMs)
	assert.LessOrEqual(t, durationMs, maxMs,
		"Duration %.2fms above expected range [%.2f, %.2f]ms", durationMs, minMs, maxMs)
}

// CountEventsByType counts entry and return events in a slice of uprobe events.
func CountEventsByType(events []*agentv1.UprobeEvent) (entries, returns int) {
	for _, e := range events {
		switch e.EventType {
		case "entry":
			entries++
		case "return":
			returns++
		}
	}
	return
}
