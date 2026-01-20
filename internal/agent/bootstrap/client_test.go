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

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
)

func TestClient_createCSR(t *testing.T) {
	agentID := "web-1"
	colonyID := "my-colony"

	client := NewClient(Config{
		AgentID:  agentID,
		ColonyID: colonyID,
		Logger:   zerolog.Nop(),
	})

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	// Updated call to use the unexported helper (if in same package)
	// or the refactored logic.
	csrPEM, err := client.createCSR(pub, priv)
	require.NoError(t, err)

	block, _ := pem.Decode(csrPEM)
	require.NotNil(t, block)
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	require.NoError(t, err)

	assert.Equal(t, "agent."+agentID+"."+colonyID, csr.Subject.CommonName)
	require.Len(t, csr.URIs, 1)
	assert.Contains(t, csr.URIs[0].String(), agentID)
}

func TestClient_parseAndVerifyResult(t *testing.T) {
	agentID := "test-agent"
	colonyID := "test-colony"

	agentPubKey, agentPrivKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caCert := generateTestCACert(t, caKey)

	client := NewClient(Config{
		AgentID:  agentID,
		ColonyID: colonyID,
		Logger:   zerolog.Nop(),
	})

	t.Run("valid cert result", func(t *testing.T) {
		certPEM := generateTestAgentCert(t, caCert, caKey, agentID, colonyID, agentPubKey)

		// Mock a response object as received from the server
		caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCert.Raw})
		res := &colonyv1.RequestCertificateResponse{
			Certificate: certPEM,
			CaChain:     caPEM,
			ExpiresAt:   time.Now().Add(time.Hour).Unix(),
		}

		result, err := client.parseAndVerifyResult(res, agentPubKey, agentPrivKey)
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, agentID, client.cfg.AgentID)
	})

	t.Run("mismatched public key", func(t *testing.T) {
		certPEM := generateTestAgentCert(t, caCert, caKey, agentID, colonyID, agentPubKey)

		// New key that doesn't match the cert
		wrongPub, wrongPriv, _ := ed25519.GenerateKey(rand.Reader)

		caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCert.Raw})
		res := &colonyv1.RequestCertificateResponse{
			Certificate: certPEM,
			CaChain:     caPEM,
		}
		_, err := client.parseAndVerifyResult(res, wrongPub, wrongPriv)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "mismatch")
	})
}

func generateTestCACert(t *testing.T, key *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "Test CA"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	cert, _ := x509.ParseCertificate(der)
	return cert
}

func generateTestAgentCert(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, agentID, colonyID string, pub ed25519.PublicKey) []byte {
	t.Helper()
	spiffeID, _ := url.Parse("spiffe://coral/colony/" + colonyID + "/agent/" + agentID)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "agent." + agentID + "." + colonyID},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:         []*url.URL{spiffeID},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, pub, caKey)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
