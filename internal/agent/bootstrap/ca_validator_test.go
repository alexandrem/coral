package bootstrap

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"math/big"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test helpers for generating test certificates.
func generateTestCA(t *testing.T, cn string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test"},
			CommonName:   cn,
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	cert, err := x509.ParseCertificate(certDER)
	require.NoError(t, err)

	return cert, key
}

func generateTestServerCert(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, colonyID string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	spiffeID, err := url.Parse("spiffe://coral/colony/" + colonyID)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			Organization: []string{"Coral"},
			CommonName:   "colony." + colonyID,
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		URIs:        []*url.URL{spiffeID},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	require.NoError(t, err)

	cert, err := x509.ParseCertificate(certDER)
	require.NoError(t, err)

	return cert, key
}

func TestNewCAValidator(t *testing.T) {
	tests := []struct {
		name                string
		fingerprint         string
		colonyID            string
		expectedFingerprint string
	}{
		{
			name:                "plain hex fingerprint",
			fingerprint:         "ABCD1234",
			colonyID:            "test-colony",
			expectedFingerprint: "abcd1234",
		},
		{
			name:                "sha256 prefixed fingerprint",
			fingerprint:         "sha256:ABCD1234",
			colonyID:            "test-colony",
			expectedFingerprint: "abcd1234",
		},
		{
			name:                "lowercase fingerprint",
			fingerprint:         "abcd1234",
			colonyID:            "test-colony",
			expectedFingerprint: "abcd1234",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := NewCAValidator(tt.fingerprint, tt.colonyID)
			assert.Equal(t, tt.expectedFingerprint, v.expectedFingerprint)
			assert.Equal(t, tt.colonyID, v.expectedColonyID)
		})
	}
}

func TestComputeFingerprint(t *testing.T) {
	ca, _ := generateTestCA(t, "Test CA")

	fp := computeFingerprint(ca)

	// Verify format.
	assert.Len(t, fp, 64) // SHA256 is 32 bytes = 64 hex chars

	// Verify it's consistent.
	fp2 := computeFingerprint(ca)
	assert.Equal(t, fp, fp2)

	// Verify it matches manual calculation.
	hash := sha256.Sum256(ca.Raw)
	expected := hex.EncodeToString(hash[:])
	assert.Equal(t, expected, fp)
}

func TestExtractColonyFromSAN(t *testing.T) {
	tests := []struct {
		name        string
		colonyID    string
		expectError bool
		expectedID  string
		expectedSAN string
	}{
		{
			name:        "valid colony ID",
			colonyID:    "my-app-prod",
			expectError: false,
			expectedID:  "my-app-prod",
			expectedSAN: "spiffe://coral/colony/my-app-prod",
		},
		{
			name:        "colony ID with suffix",
			colonyID:    "my-app-prod-a3f2e1",
			expectError: false,
			expectedID:  "my-app-prod-a3f2e1",
			expectedSAN: "spiffe://coral/colony/my-app-prod-a3f2e1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ca, caKey := generateTestCA(t, "Test CA")
			serverCert, _ := generateTestServerCert(t, ca, caKey, tt.colonyID)

			spiffeID, colonyID, err := extractColonyFromSAN(serverCert)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedID, colonyID)
				assert.Equal(t, tt.expectedSAN, spiffeID)
			}
		})
	}
}

func TestBuildAgentSPIFFEID(t *testing.T) {
	tests := []struct {
		name       string
		colonyID   string
		agentID    string
		expectedID string
	}{
		{
			name:       "basic IDs",
			colonyID:   "my-colony",
			agentID:    "web-1",
			expectedID: "spiffe://coral/colony/my-colony/agent/web-1",
		},
		{
			name:       "complex IDs",
			colonyID:   "my-app-prod-a3f2e1",
			agentID:    "web-prod-1-abc123",
			expectedID: "spiffe://coral/colony/my-app-prod-a3f2e1/agent/web-prod-1-abc123",
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

func TestValidateConnectionState(t *testing.T) {
	// Create test CA and server certificate.
	ca, caKey := generateTestCA(t, "Test CA")
	colonyID := "test-colony"
	serverCert, _ := generateTestServerCert(t, ca, caKey, colonyID)

	// Compute expected fingerprint.
	expectedFP := computeFingerprint(ca)

	t.Run("valid connection state", func(t *testing.T) {
		v := NewCAValidator(expectedFP, colonyID)

		state := &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{serverCert, ca},
		}

		result, err := v.ValidateConnectionState(state)
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, expectedFP, result.ComputedFingerprint)
		assert.Equal(t, colonyID, result.ExtractedColonyID)
		assert.Contains(t, result.ServerSPIFFEID, "spiffe://coral/colony/"+colonyID)
	})

	t.Run("fingerprint mismatch", func(t *testing.T) {
		v := NewCAValidator("wrongfingerprint", colonyID)

		state := &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{serverCert, ca},
		}

		result, err := v.ValidateConnectionState(state)
		require.Error(t, err)
		assert.NotNil(t, result) // Result still returned for debugging.

		var fpErr *FingerprintMismatchError
		assert.ErrorAs(t, err, &fpErr)
		assert.Equal(t, "wrongfingerprint", fpErr.Expected)
		assert.Equal(t, expectedFP, fpErr.Received)
	})

	t.Run("colony ID mismatch", func(t *testing.T) {
		v := NewCAValidator(expectedFP, "wrong-colony")

		state := &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{serverCert, ca},
		}

		result, err := v.ValidateConnectionState(state)
		require.Error(t, err)
		assert.NotNil(t, result)

		var colErr *ColonyMismatchError
		assert.ErrorAs(t, err, &colErr)
		assert.Equal(t, "wrong-colony", colErr.Expected)
		assert.Equal(t, colonyID, colErr.Received)
	})

	t.Run("nil connection state", func(t *testing.T) {
		v := NewCAValidator(expectedFP, colonyID)

		_, err := v.ValidateConnectionState(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil")
	})

	t.Run("empty peer certificates", func(t *testing.T) {
		v := NewCAValidator(expectedFP, colonyID)

		state := &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{},
		}

		_, err := v.ValidateConnectionState(state)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no peer certificates")
	})
}

func TestGetTLSConfig(t *testing.T) {
	v := NewCAValidator("somefingerprint", "test-colony")
	tlsConfig := v.GetTLSConfig()

	assert.NotNil(t, tlsConfig)
	// Should use InsecureSkipVerify for manual fingerprint validation.
	assert.True(t, tlsConfig.InsecureSkipVerify)
}

func TestFingerprintMismatchError(t *testing.T) {
	err := &FingerprintMismatchError{
		Expected: "expected123",
		Received: "received456",
	}

	msg := err.Error()
	assert.Contains(t, msg, "expected123")
	assert.Contains(t, msg, "received456")
	assert.Contains(t, msg, "MITM")
}

func TestColonyMismatchError(t *testing.T) {
	err := &ColonyMismatchError{
		Expected: "colony-a",
		Received: "colony-b",
	}

	msg := err.Error()
	assert.Contains(t, msg, "colony-a")
	assert.Contains(t, msg, "colony-b")
	assert.Contains(t, msg, "impersonation")
}
