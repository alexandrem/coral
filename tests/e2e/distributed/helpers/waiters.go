package helpers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
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

// WaitForServiceRegistration waits for a service to be fully registered after ConnectService.
// This polls the agent's service list to verify the service is actually registered.
func WaitForServiceRegistration(
	ctx context.Context,
	agentClient agentv1connect.AgentServiceClient,
	serviceName string,
	timeout time.Duration,
) error {
	deadline := time.Now().Add(timeout)
	pollInterval := 500 * time.Millisecond

	for time.Now().Before(deadline) {
		// Query the agent's service list.
		resp, err := agentClient.ListServices(ctx, connect.NewRequest(&agentv1.ListServicesRequest{}))
		if err == nil {
			// Check if our service is in the list.
			for _, svc := range resp.Msg.Services {
				if svc.Name == serviceName {
					// Service found!
					return nil
				}
			}
		}

		// Not found yet, wait and retry.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
			// Continue polling.
		}
	}

	return fmt.Errorf("timeout waiting for service %s to register (checked agent's service list)", serviceName)
}

// WaitForBeylaRestart waits for Beyla to restart and become ready after a service connection.
// Beyla uses a debounced restart mechanism (5s debounce) and needs time for eBPF attachment (4-5s).
// Total wait time: 12 seconds (5s debounce + 4s ready + 3s buffer).
func WaitForBeylaRestart(ctx context.Context, serviceName string) error {
	const beylaRestartTime = 12 * time.Second

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(beylaRestartTime):
		return nil
	}
}

// WaitForServices waits for a specified duration for services to be polled by colony.
// This is a simple sleep-based wait used when we don't need to poll for readiness.
func WaitForServices(ctx context.Context, seconds int) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Duration(seconds) * time.Second):
		return
	}
}
