package retry_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/coral-mesh/coral/internal/retry"
)

var ErrTransient = errors.New("transient error")

// Example demonstrates basic retry usage with exponential backoff.
func Example() {
	cfg := retry.Config{
		MaxRetries:     3,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     1 * time.Second,
		Jitter:         0.1,
	}

	attempt := 0
	err := retry.Do(context.Background(), cfg, func() error {
		attempt++
		if attempt < 3 {
			return ErrTransient
		}
		return nil
	}, func(err error) bool {
		return errors.Is(err, ErrTransient)
	})

	if err != nil {
		fmt.Printf("Failed: %v\n", err)
	} else {
		fmt.Printf("Succeeded after %d attempts\n", attempt)
	}
	// Output: Succeeded after 3 attempts
}

// Example_databaseConflict demonstrates retrying database transaction conflicts.
func Example_databaseConflict() {
	cfg := retry.Config{
		MaxRetries:     10,
		InitialBackoff: 2 * time.Millisecond,
		Jitter:         0.5,
	}

	err := retry.Do(context.Background(), cfg, func() error {
		// Simulate database operation that might conflict.
		return nil
	}, func(err error) bool {
		// Only retry on transaction conflicts.
		return err.Error() == "transaction conflict"
	})

	if err != nil {
		fmt.Printf("Database operation failed: %v\n", err)
	} else {
		fmt.Println("Database operation succeeded")
	}
	// Output: Database operation succeeded
}

// Example_withTimeout demonstrates using a context with timeout.
func Example_withTimeout() {
	cfg := retry.Config{
		MaxRetries:     5,
		InitialBackoff: 100 * time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := retry.Do(ctx, cfg, func() error {
		return errors.New("always fails")
	}, nil)

	if errors.Is(err, context.DeadlineExceeded) {
		fmt.Println("Operation timed out")
	} else {
		fmt.Printf("Failed: %v\n", err)
	}
	// Output: Operation timed out
}
