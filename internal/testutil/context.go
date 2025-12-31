// Package testutil provides testing utilities for the Coral project.
package testutil

import (
	"context"
	"time"
)

// NewTestContext creates a test context with a 30-second timeout.
func NewTestContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}
