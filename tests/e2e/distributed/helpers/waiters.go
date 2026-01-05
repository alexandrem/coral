package helpers

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// WaitForHTTPEndpoint waits for an HTTP endpoint to respond with 200 OK.
func WaitForHTTPEndpoint(ctx context.Context, url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	client := &http.Client{Timeout: 2 * time.Second}

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for HTTP endpoint %s: %w", url, ctx.Err())
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
			if err != nil {
				continue
			}

			resp, err := client.Do(req)
			if err != nil {
				continue
			}
			resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
	}
}

// WaitForCondition polls a condition function until it returns true or timeout.
func WaitForCondition(ctx context.Context, cn func() bool, timeout time.Duration, interval time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Check immediately first.
	if cn() {
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for condition: %w", ctx.Err())
		case <-ticker.C:
			if cn() {
				return nil
			}
		}
	}
}
