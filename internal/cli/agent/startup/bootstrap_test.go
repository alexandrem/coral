package startup

import (
	"testing"

	"github.com/coral-mesh/coral/internal/agent/certs"
	"github.com/coral-mesh/coral/internal/agent/enrollmentstate"
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

func TestIdentityMatches(t *testing.T) {
	tests := []struct {
		name     string
		info     *certs.CertificateInfo
		agentID  string
		colonyID string
		want     bool
	}{
		{
			name:     "match",
			info:     &certs.CertificateInfo{AgentID: "agent-1", ColonyID: "colony-1"},
			agentID:  "agent-1",
			colonyID: "colony-1",
			want:     true,
		},
		{
			name:     "agent mismatch",
			info:     &certs.CertificateInfo{AgentID: "agent-2", ColonyID: "colony-1"},
			agentID:  "agent-1",
			colonyID: "colony-1",
			want:     false,
		},
		{
			name:     "colony mismatch",
			info:     &certs.CertificateInfo{AgentID: "agent-1", ColonyID: "colony-2"},
			agentID:  "agent-1",
			colonyID: "colony-1",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := identityMatches(tt.info, tt.agentID, tt.colonyID); got != tt.want {
				t.Fatalf("identityMatches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateCheckpoint(t *testing.T) {
	valid := func() *enrollmentstate.Checkpoint {
		return &enrollmentstate.Checkpoint{
			State:              enrollmentstate.StateEnrolled,
			AgentID:            "agent-1",
			ColonyID:           "colony-1",
			WireGuardPublicKey: "pubkey",
			AssignedIP:         "100.64.0.5",
			MeshSubnet:         "100.64.0.0/10",
			CertificateSHA256:  "sha",
			CertificateSerial:  "serial",
		}
	}

	t.Run("valid checkpoint passes", func(t *testing.T) {
		if err := validateCheckpoint(valid(), "agent-1", "colony-1", "pubkey", "sha", "serial"); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})

	t.Run("pending state is rejected", func(t *testing.T) {
		cp := valid()
		cp.State = enrollmentstate.StatePending
		if err := validateCheckpoint(cp, "agent-1", "colony-1", "pubkey", "sha", "serial"); err == nil {
			t.Fatal("expected error for pending state")
		}
	})

	t.Run("identity mismatch is rejected", func(t *testing.T) {
		cp := valid()
		if err := validateCheckpoint(cp, "agent-2", "colony-1", "pubkey", "sha", "serial"); err == nil {
			t.Fatal("expected error for identity mismatch")
		}
	})

	t.Run("wireguard key mismatch is rejected", func(t *testing.T) {
		cp := valid()
		if err := validateCheckpoint(cp, "agent-1", "colony-1", "other-pubkey", "sha", "serial"); err == nil {
			t.Fatal("expected error for wireguard key mismatch")
		}
	})

	t.Run("certificate hash mismatch is rejected", func(t *testing.T) {
		cp := valid()
		if err := validateCheckpoint(cp, "agent-1", "colony-1", "pubkey", "different-sha", "serial"); err == nil {
			t.Fatal("expected error for certificate hash mismatch")
		}
	})

	t.Run("certificate serial mismatch is rejected", func(t *testing.T) {
		cp := valid()
		if err := validateCheckpoint(cp, "agent-1", "colony-1", "pubkey", "sha", "different-serial"); err == nil {
			t.Fatal("expected error for certificate serial mismatch")
		}
	})

	t.Run("invalid assigned IP is rejected", func(t *testing.T) {
		cp := valid()
		cp.AssignedIP = "not-an-ip"
		if err := validateCheckpoint(cp, "agent-1", "colony-1", "pubkey", "sha", "serial"); err == nil {
			t.Fatal("expected error for invalid assigned IP")
		}
	})

	t.Run("invalid mesh subnet is rejected", func(t *testing.T) {
		cp := valid()
		cp.MeshSubnet = "not-a-cidr"
		if err := validateCheckpoint(cp, "agent-1", "colony-1", "pubkey", "sha", "serial"); err == nil {
			t.Fatal("expected error for invalid mesh subnet")
		}
	})
}
