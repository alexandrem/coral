package status

import (
	"testing"
	"time"
)

func TestFormatUptime(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{
			name:     "less than 1 minute",
			duration: 45 * time.Second,
			want:     "45s",
		},
		{
			name:     "1 minute",
			duration: 60 * time.Second,
			want:     "1m 0s",
		},
		{
			name:     "15 minutes 30 seconds",
			duration: 15*time.Minute + 30*time.Second,
			want:     "15m 30s",
		},
		{
			name:     "59 minutes",
			duration: 59 * time.Minute,
			want:     "59m 0s",
		},
		{
			name:     "1 hour",
			duration: 1 * time.Hour,
			want:     "1h 0m",
		},
		{
			name:     "5 hours 20 minutes",
			duration: 5*time.Hour + 20*time.Minute,
			want:     "5h 20m",
		},
		{
			name:     "23 hours 59 minutes",
			duration: 23*time.Hour + 59*time.Minute,
			want:     "23h 59m",
		},
		{
			name:     "1 day",
			duration: 24 * time.Hour,
			want:     "1d 0h",
		},
		{
			name:     "2 days 3 hours",
			duration: 2*24*time.Hour + 3*time.Hour,
			want:     "2d 3h",
		},
		{
			name:     "10 days 5 hours",
			duration: 10*24*time.Hour + 5*time.Hour,
			want:     "10d 5h",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatUptime(tt.duration)
			if got != tt.want {
				t.Errorf("formatUptime(%v) = %q, want %q", tt.duration, got, tt.want)
			}
		})
	}
}

func TestTruncateKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{
			name: "short key",
			key:  "shortkey",
			want: "shortkey",
		},
		{
			name: "exactly 20 characters",
			key:  "12345678901234567890",
			want: "12345678901234567890",
		},
		{
			name: "long key",
			key:  "abcdefghijklmnopqrstuvwxyz0123456789",
			want: "abcdefghijkl...6789",
		},
		{
			name: "wireguard public key",
			key:  "X25519PublicKeyABCDEFGHIJKLMNOPQRSTUVWXYZ1234",
			want: "X25519Public...1234",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateKey(tt.key)
			if got != tt.want {
				t.Errorf("truncateKey(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestStatusOutput(t *testing.T) {
	// Test that the Output struct has expected fields.
	output := Output{
		Colonies: []ColonyStatusInfo{
			{
				ColonyID:    "colony-1",
				Environment: "prod",
				Running:     true,
			},
		},
		Version: "v0.1.0",
	}

	output.Discovery.Endpoint = "https://discovery.coralmesh.dev"
	output.Discovery.Healthy = true
	output.Summary.Total = 1
	output.Summary.Running = 1
	output.Summary.Stopped = 0

	if output.Discovery.Endpoint != "https://discovery.coralmesh.dev" {
		t.Errorf("Discovery.Endpoint = %q, want %q", output.Discovery.Endpoint, "https://discovery.coralmesh.dev")
	}

	if !output.Discovery.Healthy {
		t.Errorf("Discovery.Healthy = %v, want %v", output.Discovery.Healthy, true)
	}

	if output.Summary.Total != 1 {
		t.Errorf("Summary.Total = %d, want %d", output.Summary.Total, 1)
	}

	if len(output.Colonies) != 1 {
		t.Errorf("len(Colonies) = %d, want %d", len(output.Colonies), 1)
	}
}
