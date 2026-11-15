package startup

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/agent/bootstrap"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

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
			info:     &certs.CertificateInfo{AgentID: "agent-1", ColonyID: "colony-1", SPIFFEID: "spiffe://coral/colony/colony-1/agent/agent-1"},
			agentID:  "agent-1",
			colonyID: "colony-1",
			want:     true,
		},
		{
			name:     "SPIFFE mismatch",
			info:     &certs.CertificateInfo{AgentID: "agent-1", ColonyID: "colony-1", SPIFFEID: "spiffe://other/colony/colony-1/agent/agent-1"},
			agentID:  "agent-1",
			colonyID: "colony-1",
			want:     false,
		},
		{
			name:     "agent mismatch",
			info:     &certs.CertificateInfo{AgentID: "agent-2", ColonyID: "colony-1", SPIFFEID: "spiffe://coral/colony/colony-1/agent/agent-1"},
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

// enrollmentCertificate creates real certificate material for startup restart tests.
func enrollmentCertificate(t *testing.T, agentID, colonyID, spiffe string) *bootstrap.Result {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	uri, err := url.Parse(spiffe)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(123),
		Subject:      pkix.Name{CommonName: "agent." + agentID + "." + colonyID},
		NotBefore:    time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour),
		URIs: []*url.URL{uri}, IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	private, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return &bootstrap.Result{
		Certificate: cert, PrivateKey: pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private}),
		RootCA: cert, CAChain: cert,
		Registration: &meshv1.RegisterResponse{Accepted: true, AssignedIp: "100.64.0.5", MeshSubnet: "100.64.0.0/10"},
	}
}

func TestEnrollmentRestartBoundaries(t *testing.T) {
	for _, boundary := range []string{"pending identity", "server response", "certificate file only", "certificate and key only", "certificate writes", "checkpoint commit", "mesh configured", "legacy certificate", "missing certificate"} {
		t.Run(boundary, func(t *testing.T) {
			dir := t.TempDir()
			logger := zerolog.Nop()
			cfg := &config.AgentConfig{}
			cfg.Agent.Bootstrap.Enabled = true
			cfg.Agent.Bootstrap.CertsDir = dir
			network := NewNetworkInitializer(logger, &config.ResolvedConfig{ColonyID: "colony-1"}, cfg, nil, "agent-1")
			keys, err := network.loadOrGenerateAgentKeys()
			require.NoError(t, err)
			store := enrollmentstate.NewStore(dir, logger)
			manager := certs.NewManager(certs.Config{CertsDir: dir, Logger: logger})
			phase := NewBootstrapPhase(logger, cfg, "colony-1", "agent-1", keys, nil, nil, "test", nil)
			issued := enrollmentCertificate(t, "agent-1", "colony-1", "spiffe://coral/colony/colony-1/agent/agent-1")
			committed := boundary == "checkpoint commit" || boundary == "mesh configured"
			switch boundary {
			case "certificate file only", "certificate and key only":
				require.NoError(t, os.WriteFile(manager.GetCertPath(), issued.Certificate, 0644))
				if boundary == "certificate and key only" {
					require.NoError(t, os.WriteFile(manager.GetKeyPath(), issued.PrivateKey, 0600))
				}
			case "certificate writes", "legacy certificate":
				require.NoError(t, manager.Save(issued))
				if boundary == "legacy certificate" {
					require.NoError(t, os.Remove(filepath.Join(dir, enrollmentstate.CheckpointFileName)))
				}
			case "checkpoint commit", "mesh configured", "missing certificate":
				_, err := phase.saveBootstrapResult(manager, store, issued)
				require.NoError(t, err)
				if boundary == "missing certificate" {
					require.NoError(t, os.Remove(manager.GetCertPath()))
				}
			}
			// A new initializer and bootstrap phase simulate the next process.
			restartedKeys, err := network.loadOrGenerateAgentKeys()
			require.NoError(t, err)
			if boundary != "legacy certificate" {
				require.Equal(t, keys, restartedKeys)
			}
			phase.agentKeys = restartedKeys
			result, err := phase.Execute(context.Background())
			if committed {
				require.NoError(t, err)
				require.False(t, result.Bootstrapped)
				require.True(t, result.MeshEnrollmentRestored)
				require.Equal(t, issued.Registration.AssignedIp, result.Registration.AssignedIp)
				cert, err := os.ReadFile(manager.GetCertPath())
				require.NoError(t, err)
				require.Equal(t, issued.Certificate, cert)
			} else {
				// Fresh enrollment is required. Stop before network calls, then
				// deliver a simulated compound response through the real writer.
				require.ErrorContains(t, err, "no CA fingerprint")
				cp, err := store.Load()
				require.NoError(t, err)
				require.Equal(t, enrollmentstate.StatePending, cp.State)
				require.Equal(t, restartedKeys.PublicKey, cp.WireGuardPublicKey)
				_, err = phase.saveBootstrapResult(manager, store, issued)
				require.NoError(t, err)
				result, err = phase.Execute(context.Background())
				require.NoError(t, err)
				require.True(t, result.MeshEnrollmentRestored)
			}
		})
	}
}

func TestBootstrapRejectsInvalidCompoundResult(t *testing.T) {
	for _, mutation := range []string{"agent", "colony", "spiffe", "rejected", "missing assignment", "outside subnet"} {
		t.Run(mutation, func(t *testing.T) {
			agent, colony, spiffe := "agent-1", "colony-1", "spiffe://coral/colony/colony-1/agent/agent-1"
			switch mutation {
			case "agent":
				agent = "agent-2"
			case "colony":
				colony = "colony-2"
			case "spiffe":
				spiffe = "spiffe://other/colony/colony-1/agent/agent-1"
			}
			issued := enrollmentCertificate(t, agent, colony, spiffe)
			switch mutation {
			case "rejected":
				issued.Registration.Accepted = false
			case "missing assignment":
				issued.Registration.AssignedIp = ""
			case "outside subnet":
				issued.Registration.AssignedIp = "192.0.2.1"
			}
			phase := &BootstrapPhase{agentID: "agent-1", colonyID: "colony-1"}
			require.Error(t, phase.validateBootstrapResult(issued))
		})
	}
}

func TestRestoredEnrollmentUsesLiveDiscovery(t *testing.T) {
	cm := &ConnectionManager{logger: zerolog.Nop(), colonyInfo: &discovery.LookupColonyResponse{
		MeshIPv4: "100.64.0.1", Endpoints: []string{"198.51.100.1:9000"},
	}}
	result := &BootstrapResult{MeshEnrollmentRestored: true}
	require.Equal(t, "198.51.100.1:51820", result.colonyPeerEndpoint(cm))
	// Discovery changes independently of the persisted enrollment.
	cm.colonyInfo.Endpoints = []string{"198.51.100.2:9000"}
	require.Equal(t, "198.51.100.2:51820", result.colonyPeerEndpoint(cm))
	for _, endpoint := range []string{"127.0.0.1:9000", "100.64.0.1:9000"} {
		cm.colonyInfo.Endpoints = []string{endpoint}
		require.Empty(t, result.colonyPeerEndpoint(cm))
	}
	result.MeshEnrollmentRestored = false
	cm.colonyInfo.Endpoints = []string{"198.51.100.2:9000"}
	require.Empty(t, result.colonyPeerEndpoint(cm), "initial rendezvous uses Colony-initiated handshake")
}
