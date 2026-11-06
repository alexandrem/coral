package helpers

import (
	"context"
	"net/http"
	"testing"
	"testing/synctest"
	"time"
)

func TestWaitForHTTPEndpointSlowHealthyResponse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Keep this test serial: the waiter's client uses http.DefaultTransport.
		originalTransport := http.DefaultTransport
		defer func() { http.DefaultTransport = originalTransport }()
		http.DefaultTransport = waiterRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			// Simulate a response slower than the old two-second client timeout.
			// Both this timer and the client's timeout use synctest's virtual clock.
			timer := time.NewTimer(3 * time.Second)
			defer timer.Stop()
			select {
			case <-timer.C:
				return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		})

		if err := WaitForHTTPEndpoint(context.Background(), "http://example.test/status", 4*time.Second); err != nil {
			t.Fatal(err)
		}
	})
}

type waiterRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f waiterRoundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
