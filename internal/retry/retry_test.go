package retry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDo_Success(t *testing.T) {
	cfg := Config{
		MaxRetries:     3,
		InitialBackoff: 10 * time.Millisecond,
	}

	called := 0
	err := Do(context.Background(), cfg, func() error {
		called++
		return nil
	}, nil)

	require.NoError(t, err)
	assert.Equal(t, 1, called, "should succeed on first attempt")
}

func TestDo_SuccessAfterRetries(t *testing.T) {
	cfg := Config{
		MaxRetries:     5,
		InitialBackoff: 1 * time.Millisecond,
	}

	called := 0
	err := Do(context.Background(), cfg, func() error {
		called++
		if called < 3 {
			return errors.New("temporary error")
		}
		return nil
	}, func(err error) bool {
		return true // retry all errors
	})

	require.NoError(t, err)
	assert.Equal(t, 3, called, "should succeed on third attempt")
}

func TestDo_ExhaustedRetries(t *testing.T) {
	cfg := Config{
		MaxRetries:     3,
		InitialBackoff: 1 * time.Millisecond,
	}

	called := 0
	testErr := errors.New("persistent error")
	err := Do(context.Background(), cfg, func() error {
		called++
		return testErr
	}, func(err error) bool {
		return true
	})

	require.Error(t, err)
	assert.Equal(t, 3, called, "should attempt MaxRetries times")
	assert.ErrorIs(t, err, testErr)
	assert.Contains(t, err.Error(), "failed after 3 retries")
}

func TestDo_NonRetryableError(t *testing.T) {
	cfg := Config{
		MaxRetries:     5,
		InitialBackoff: 1 * time.Millisecond,
	}

	nonRetryableErr := errors.New("non-retryable")
	retryableErr := errors.New("retryable")

	called := 0
	err := Do(context.Background(), cfg, func() error {
		called++
		if called == 2 {
			return nonRetryableErr
		}
		return retryableErr
	}, func(err error) bool {
		return !errors.Is(err, nonRetryableErr)
	})

	require.Error(t, err)
	assert.Equal(t, 2, called, "should stop on non-retryable error")
	assert.ErrorIs(t, err, nonRetryableErr)
}

func TestDo_ContextCanceled(t *testing.T) {
	cfg := Config{
		MaxRetries:     10,
		InitialBackoff: 50 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	called := 0
	err := Do(ctx, cfg, func() error {
		called++
		if called == 2 {
			cancel() // Cancel after first retry.
		}
		return errors.New("error")
	}, func(err error) bool {
		return true
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.LessOrEqual(t, called, 3, "should stop soon after context canceled")
}

func TestDo_NilShouldRetry(t *testing.T) {
	cfg := Config{
		MaxRetries:     3,
		InitialBackoff: 1 * time.Millisecond,
	}

	called := 0
	testErr := errors.New("error")
	err := Do(context.Background(), cfg, func() error {
		called++
		return testErr
	}, nil) // nil shouldRetry means retry all errors

	require.Error(t, err)
	assert.Equal(t, 3, called)
	assert.ErrorIs(t, err, testErr)
}

func TestCalculateBackoff_ExponentialGrowth(t *testing.T) {
	cfg := Config{
		InitialBackoff: 10 * time.Millisecond,
		MaxRetries:     5,
	}

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{1, 10 * time.Millisecond},  // 2^0 * 10ms
		{2, 20 * time.Millisecond},  // 2^1 * 10ms
		{3, 40 * time.Millisecond},  // 2^2 * 10ms
		{4, 80 * time.Millisecond},  // 2^3 * 10ms
		{5, 160 * time.Millisecond}, // 2^4 * 10ms
	}

	for _, tt := range tests {
		backoff := calculateBackoff(cfg, tt.attempt)
		assert.Equal(t, tt.expected, backoff, "attempt %d", tt.attempt)
	}
}

func TestCalculateBackoff_MaxBackoffCap(t *testing.T) {
	cfg := Config{
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
		MaxRetries:     5,
	}

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{1, 10 * time.Millisecond}, // 2^0 * 10ms = 10ms (< cap)
		{2, 20 * time.Millisecond}, // 2^1 * 10ms = 20ms (< cap)
		{3, 40 * time.Millisecond}, // 2^2 * 10ms = 40ms (< cap)
		{4, 50 * time.Millisecond}, // 2^3 * 10ms = 80ms (capped to 50ms)
		{5, 50 * time.Millisecond}, // 2^4 * 10ms = 160ms (capped to 50ms)
	}

	for _, tt := range tests {
		backoff := calculateBackoff(cfg, tt.attempt)
		assert.Equal(t, tt.expected, backoff, "attempt %d", tt.attempt)
	}
}

func TestCalculateBackoff_WithJitter(t *testing.T) {
	cfg := Config{
		InitialBackoff: 100 * time.Millisecond,
		MaxRetries:     5,
		Jitter:         0.5, // 50% jitter
	}

	// With jitter, backoff should be base + jitter.
	// Jitter = base * jitter * attempt / maxRetries.
	// For attempt 2: base=200ms, jitter=200*0.5*2/5=40ms, total=240ms.
	backoff := calculateBackoff(cfg, 2)
	expected := 200*time.Millisecond + 40*time.Millisecond
	assert.Equal(t, expected, backoff)
}

func TestCalculateBackoff_NoJitter(t *testing.T) {
	cfg := Config{
		InitialBackoff: 100 * time.Millisecond,
		MaxRetries:     5,
		Jitter:         0, // No jitter
	}

	backoff := calculateBackoff(cfg, 2)
	expected := 200 * time.Millisecond
	assert.Equal(t, expected, backoff)
}
