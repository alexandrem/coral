package bootstrap

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildAgentSPIFFEID_ForRenewal tests that BuildAgentSPIFFEID produces correct URIs.
func TestBuildAgentSPIFFEID_ForRenewal(t *testing.T) {
	tests := []struct {
		name       string
		colonyID   string
		agentID    string
		expectedID string
	}{
		{
			name:       "simple IDs",
			colonyID:   "test-colony",
			agentID:    "web-1",
			expectedID: "spiffe://coral/colony/test-colony/agent/web-1",
		},
		{
			name:       "complex IDs",
			colonyID:   "my-app-prod-a3f2e1",
			agentID:    "worker-node-1-xyz789",
			expectedID: "spiffe://coral/colony/my-app-prod-a3f2e1/agent/worker-node-1-xyz789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uri, err := BuildAgentSPIFFEID(tt.colonyID, tt.agentID)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedID, uri.String())
		})
	}
}

// TestRenewalClient_createCSR tests CSR creation for the renewal client.
func TestRenewalClient_createCSR(t *testing.T) {
	tests := []struct {
		name     string
		agentID  string
		colonyID string
	}{
		{
			name:     "basic agent",
			agentID:  "web-1",
			colonyID: "my-colony",
		},
		{
			name:     "complex IDs",
			agentID:  "web-prod-server-1-abc123",
			colonyID: "my-app-production-a3f2e1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &RenewalClient{
				config: RenewalConfig{
					AgentID:  tt.agentID,
					ColonyID: tt.colonyID,
				},
				logger: zerolog.Nop(),
			}

			// Generate Ed25519 keys.
			publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
			require.NoError(t, err)

			// Create CSR.
			csrPEM, err := client.createCSR(publicKey, privateKey)
			require.NoError(t, err)
			assert.NotEmpty(t, csrPEM)

			// Parse and validate CSR.
			block, _ := pem.Decode(csrPEM)
			require.NotNil(t, block)
			assert.Equal(t, "CERTIFICATE REQUEST", block.Type)

			csr, err := x509.ParseCertificateRequest(block.Bytes)
			require.NoError(t, err)

			// Verify CSR fields.
			expectedCN := "agent." + tt.agentID + "." + tt.colonyID
			assert.Equal(t, expectedCN, csr.Subject.CommonName)
			assert.Contains(t, csr.Subject.Organization, tt.colonyID)

			// Verify SPIFFE ID in SAN.
			require.Len(t, csr.URIs, 1)
			expectedSPIFFE := "spiffe://coral/colony/" + tt.colonyID + "/agent/" + tt.agentID
			assert.Equal(t, expectedSPIFFE, csr.URIs[0].String())
		})
	}
}

// TestRenewalClient_validateReceivedCertificate tests certificate validation for renewal.
func TestRenewalClient_validateReceivedCertificate(t *testing.T) {
	agentID := "test-agent"
	colonyID := "test-colony"

	// Generate Ed25519 keys for the agent.
	agentPubKey, agentPrivKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	// Generate a CA for signing.
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	caCert := generateTestCACertForRenewal(t, caKey)

	client := &RenewalClient{
		config: RenewalConfig{
			AgentID:  agentID,
			ColonyID: colonyID,
		},
		logger: zerolog.Nop(),
	}

	t.Run("valid certificate", func(t *testing.T) {
		certPEM := generateTestAgentCertWithEd25519(t, caCert, caKey, agentID, colonyID, agentPubKey, agentPrivKey)

		cert, err := client.validateReceivedCertificate(certPEM, agentPubKey)
		require.NoError(t, err)
		assert.NotNil(t, cert)
		assert.Equal(t, "agent."+agentID+"."+colonyID, cert.Subject.CommonName)
	})

	t.Run("wrong public key", func(t *testing.T) {
		certPEM := generateTestAgentCertWithEd25519(t, caCert, caKey, agentID, colonyID, agentPubKey, agentPrivKey)

		// Generate different Ed25519 key.
		wrongPubKey, _, err := ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)

		_, err = client.validateReceivedCertificate(certPEM, wrongPubKey)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "public key")
	})

	t.Run("wrong agent ID", func(t *testing.T) {
		certPEM := generateTestAgentCertWithEd25519(t, caCert, caKey, agentID, colonyID, agentPubKey, agentPrivKey)

		wrongClient := &RenewalClient{
			config: RenewalConfig{
				AgentID:  "wrong-agent",
				ColonyID: colonyID,
			},
			logger: zerolog.Nop(),
		}

		_, err := wrongClient.validateReceivedCertificate(certPEM, agentPubKey)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "SPIFFE ID")
	})

	t.Run("wrong colony ID", func(t *testing.T) {
		certPEM := generateTestAgentCertWithEd25519(t, caCert, caKey, agentID, colonyID, agentPubKey, agentPrivKey)

		wrongClient := &RenewalClient{
			config: RenewalConfig{
				AgentID:  agentID,
				ColonyID: "wrong-colony",
			},
			logger: zerolog.Nop(),
		}

		_, err := wrongClient.validateReceivedCertificate(certPEM, agentPubKey)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "SPIFFE ID")
	})

	t.Run("invalid PEM", func(t *testing.T) {
		_, err := client.validateReceivedCertificate([]byte("not valid pem"), agentPubKey)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "decode")
	})
}

// TestNewRenewalClient tests renewal client creation.
func TestNewRenewalClient(t *testing.T) {
	cfg := RenewalConfig{
		AgentID:          "test-agent",
		ColonyID:         "test-colony",
		CAFingerprint:    "sha256:abc123",
		ColonyEndpoint:   "https://colony.example.com:9000",
		ExistingCertPath: "/path/to/cert.crt",
		ExistingKeyPath:  "/path/to/key.key",
		RootCAPath:       "/path/to/root-ca.crt",
		Logger:           zerolog.Nop(),
	}

	client := NewRenewalClient(cfg)

	assert.NotNil(t, client)
	assert.Equal(t, cfg.AgentID, client.config.AgentID)
	assert.Equal(t, cfg.ColonyID, client.config.ColonyID)
	assert.NotNil(t, client.validator)
}

// Helper to generate a test CA certificate for renewal tests.
func generateTestCACertForRenewal(t *testing.T, key *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test"},
			CommonName:   "Test CA",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	cert, err := x509.ParseCertificate(certDER)
	require.NoError(t, err)

	return cert
}

// Helper to generate a test agent certificate with Ed25519 keys.
func generateTestAgentCertWithEd25519(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, agentID, colonyID string, agentPubKey ed25519.PublicKey, _ ed25519.PrivateKey) []byte {
	t.Helper()

	spiffeID, err := url.Parse("spiffe://coral/colony/" + colonyID + "/agent/" + agentID)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			CommonName:   "agent." + agentID + "." + colonyID,
			Organization: []string{colonyID},
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:        []*url.URL{spiffeID},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, ca, agentPubKey, caKey)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	return certPEM
}
