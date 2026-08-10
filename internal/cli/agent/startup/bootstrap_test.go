package startup

import (
	"testing"

	"github.com/coral-mesh/coral/internal/discovery"
)

func TestResolveBootstrapPublicEndpoint(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		observed   *discovery.Endpoint
		listenPort int
		want       string
	}{
		{
			name:       "explicit endpoint wins",
			configured: "agent.example.com:9444",
			observed:   &discovery.Endpoint{IP: "203.0.113.10", Port: 51820},
			want:       "agent.example.com:9444",
		},
		{
			name:     "derived IPv4 default port",
			observed: &discovery.Endpoint{IP: "203.0.113.10", Port: 51820},
			want:     "203.0.113.10:8444",
		},
		{
			name:       "derived IPv6 custom port",
			observed:   &discovery.Endpoint{IP: "2001:db8::10", Port: 51820},
			listenPort: 9444,
			want:       "[2001:db8::10]:9444",
		},
		{name: "missing observation"},
		{name: "invalid observed IP", observed: &discovery.Endpoint{IP: "not-an-ip", Port: 51820}},
		{name: "invalid listener", observed: &discovery.Endpoint{IP: "203.0.113.10"}, listenPort: 70000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveBootstrapPublicEndpoint(tt.configured, tt.observed, tt.listenPort)
			if got != tt.want {
				t.Fatalf("resolveBootstrapPublicEndpoint() = %q, want %q", got, tt.want)
			}
		})
	}
}
