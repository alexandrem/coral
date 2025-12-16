// Package retry provides exponential backoff retry mechanisms for handling transient failures.
//
// This package implements a configurable retry strategy with exponential backoff,
// optional jitter, and context cancellation support. It's designed to handle
// transient errors in distributed systems, such as database conflicts, network
// timeouts, and service unavailability.
//
// # Basic Usage
//
//	cfg := retry.Config{
//	    MaxRetries:     5,
//	    InitialBackoff: 100 * time.Millisecond,
//	    MaxBackoff:     5 * time.Second,
//	    Jitter:         0.1,
//	}
//
//	err := retry.Do(ctx, cfg, func() error {
//	    return doSomething()
//	}, func(err error) bool {
//	    return isTransientError(err)
//	})
//
// # Backoff Strategy
//
// The backoff duration follows an exponential pattern: InitialBackoff * 2^(attempt-1).
// For example, with InitialBackoff of 100ms:
//   - Attempt 1: 100ms
//   - Attempt 2: 200ms
//   - Attempt 3: 400ms
//   - Attempt 4: 800ms
//
// Optional jitter can be added to prevent thundering herd problems when multiple
// clients retry simultaneously. Jitter increases linearly with attempt number.
//
// # Context Cancellation
//
// All retry operations respect context cancellation. If the context is canceled
// during a backoff period, the retry loop exits immediately with the context error.
package retry

import (
	"context"
	"fmt"
	"math"
	"time"
)

// Config defines the retry behavior for exponential backoff operations.
//
// The zero value is not usable; MaxRetries and InitialBackoff must be set.
type Config struct {
	// MaxRetries is the maximum number of retry attempts.
	// The function will be called at most MaxRetries times.
	// Must be greater than 0.
	MaxRetries int

	// InitialBackoff is the base backoff duration.
	// Each retry multiplies this by 2^(attempt-1).
	// For example, with InitialBackoff of 100ms:
	//   - Attempt 1: 100ms
	//   - Attempt 2: 200ms
	//   - Attempt 3: 400ms
	// Must be greater than 0.
	InitialBackoff time.Duration

	// MaxBackoff caps the backoff duration.
	// If the calculated exponential backoff exceeds this value,
	// it will be capped to MaxBackoff.
	// Zero means no cap (backoff grows unbounded).
	MaxBackoff time.Duration

	// Jitter adds randomness to backoff to prevent thundering herd (0.0 to 1.0).
	// The jitter amount increases linearly with attempt number:
	//   jitter_amount = backoff * Jitter * attempt / MaxRetries
	// For example, with Jitter of 0.5 (50%):
	//   - Early attempts have less jitter
	//   - Later attempts have more jitter (up to 50% of backoff)
	// Zero means no jitter.
	Jitter float64
}

// ShouldRetryFunc is a function that determines if an error should trigger a retry.
//
// Return true to retry the operation, or false to fail immediately with the error.
// If this function is nil when passed to Do, all errors will be retried.
//
// Example:
//
//	shouldRetry := func(err error) bool {
//	    return errors.Is(err, ErrTransient) || errors.Is(err, ErrTimeout)
//	}
type ShouldRetryFunc func(error) bool

// Do executes fn with exponential backoff retry.
//
// The function fn is called up to cfg.MaxRetries times. If fn returns nil,
// Do returns immediately with nil. If fn returns an error, the shouldRetry
// function is called to determine if the error is retryable.
//
// If shouldRetry is nil, all errors are considered retryable.
// If shouldRetry returns false, Do returns immediately with the error.
// If shouldRetry returns true, Do waits for a backoff period and retries.
//
// The backoff period increases exponentially with each retry attempt,
// following the formula: InitialBackoff * 2^(attempt-1).
//
// If all retries are exhausted, Do returns an error that wraps the last
// error from fn with a message indicating the number of retries attempted.
//
// If the context is canceled during execution or backoff, Do returns
// the context error immediately.
//
// Example:
//
//	cfg := retry.Config{
//	    MaxRetries:     3,
//	    InitialBackoff: 100 * time.Millisecond,
//	}
//
//	err := retry.Do(ctx, cfg, func() error {
//	    return client.Call()
//	}, func(err error) bool {
//	    return errors.Is(err, ErrTimeout)
//	})
func Do(ctx context.Context, cfg Config, fn func() error, shouldRetry ShouldRetryFunc) error {
	var lastErr error

	for attempt := 0; attempt < cfg.MaxRetries; attempt++ {
		// Apply backoff before retry (but not on first attempt).
		if attempt > 0 {
			backoff := calculateBackoff(cfg, attempt)

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		// Execute the function.
		err := fn()
		if err == nil {
			return nil
		}

		// Check if error is retryable.
		if shouldRetry != nil && !shouldRetry(err) {
			return err
		}

		lastErr = err
	}

	return fmt.Errorf("failed after %d retries: %w", cfg.MaxRetries, lastErr)
}

// calculateBackoff computes the backoff duration for a given attempt.
//
// The backoff calculation follows these steps:
//  1. Calculate exponential backoff: InitialBackoff * 2^(attempt-1)
//  2. Apply MaxBackoff cap if configured (cfg.MaxBackoff > 0)
//  3. Add jitter if configured (cfg.Jitter > 0)
//
// The jitter amount increases linearly with the attempt number to spread out
// retries over time, preventing thundering herd when multiple clients retry
// simultaneously.
//
// For example, with InitialBackoff=100ms, MaxBackoff=1s, Jitter=0.5, MaxRetries=5:
//   - Attempt 1: 100ms base + 10ms jitter = 110ms
//   - Attempt 2: 200ms base + 40ms jitter = 240ms
//   - Attempt 3: 400ms base + 120ms jitter = 520ms
//   - Attempt 4: 800ms base + 320ms jitter = 1s (capped)
func calculateBackoff(cfg Config, attempt int) time.Duration {
	// Exponential backoff: 2^(attempt-1) * InitialBackoff.
	multiplier := math.Pow(2, float64(attempt-1))
	backoff := time.Duration(multiplier * float64(cfg.InitialBackoff))

	// Apply max backoff cap.
	if cfg.MaxBackoff > 0 && backoff > cfg.MaxBackoff {
		backoff = cfg.MaxBackoff
	}

	// Add jitter (increases linearly with attempt).
	if cfg.Jitter > 0 {
		jitterAmount := float64(backoff) * cfg.Jitter * float64(attempt) / float64(cfg.MaxRetries)
		backoff += time.Duration(jitterAmount)
	}

	return backoff
}
