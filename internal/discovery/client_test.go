package discovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPollBootstrapRendezvousUsesContextDeadlineInsteadOfClientTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/coral.discovery.v1.DiscoveryService/PollBootstrapRendezvous" {
			t.Errorf("unexpected request path: %s", r.URL.Path)
		}
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"records":[],"timedOut":true}`))
	}))
	defer server.Close()

	// The ordinary client timeout is deliberately shorter than the simulated
	// long poll. PollBootstrapRendezvous must instead honor its caller's
	// context deadline.
	client := NewClient(server.URL, WithTimeout(20*time.Millisecond))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	response, err := client.PollBootstrapRendezvous(ctx, "mesh-1", 25)
	if err != nil {
		t.Fatalf("PollBootstrapRendezvous() error = %v", err)
	}
	if !response.TimedOut {
		t.Fatal("PollBootstrapRendezvous() TimedOut = false, want true")
	}
}
