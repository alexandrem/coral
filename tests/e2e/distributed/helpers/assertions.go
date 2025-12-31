package helpers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
