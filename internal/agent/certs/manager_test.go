package certs

import (
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

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coral-mesh/coral/internal/agent/bootstrap"
)

// Test helper to generate test certificates.
func generateTestCertificate(t *testing.T, agentID, colonyID string, notAfter time.Time) (certPEM, keyPEM, caPEM []byte) {
	t.Helper()

	// Generate CA.
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test"},
			CommonName:   "Test CA",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	require.NoError(t, err)

	caPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caCertDER,
	})

	// Generate agent certificate.
	agentKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	spiffeID, err := url.Parse("spiffe://coral/colony/" + colonyID + "/agent/" + agentID)
	require.NoError(t, err)

	caCert, err := x509.ParseCertificate(caCertDER)
	require.NoError(t, err)

	agentTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			CommonName:   "agent." + agentID + "." + colonyID,
			Organization: []string{colonyID},
		},
		NotBefore:   time.Now(),
		NotAfter:    notAfter,
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:        []*url.URL{spiffeID},
	}

	agentCertDER, err := x509.CreateCertificate(rand.Reader, agentTemplate, caCert, &agentKey.PublicKey, caKey)
	require.NoError(t, err)

	certPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: agentCertDER,
	})

	agentKeyDER, err := x509.MarshalECPrivateKey(agentKey)
	require.NoError(t, err)

	keyPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: agentKeyDER,
	})

	return certPEM, keyPEM, caPEM
}

func TestNewManager(t *testing.T) {
	t.Run("default certs dir", func(t *testing.T) {
		logger := zerolog.Nop()
		m := NewManager(Config{Logger: logger})

		homeDir, _ := os.UserHomeDir()
		expected := filepath.Join(homeDir, ".coral", "certs")
		assert.Equal(t, expected, m.certsDir)
	})

	t.Run("custom certs dir", func(t *testing.T) {
		logger := zerolog.Nop()
		m := NewManager(Config{
			CertsDir: "/custom/path",
			Logger:   logger,
		})

		assert.Equal(t, "/custom/path", m.certsDir)
	})
}

func TestCertificateExists(t *testing.T) {
	tmpDir := t.TempDir()
	logger := zerolog.Nop()
	m := NewManager(Config{CertsDir: tmpDir, Logger: logger})

	t.Run("no certificate", func(t *testing.T) {
		assert.False(t, m.CertificateExists())
	})

	t.Run("with certificate and key", func(t *testing.T) {
		certPath := filepath.Join(tmpDir, CertFileName)
		keyPath := filepath.Join(tmpDir, KeyFileName)

		require.NoError(t, os.WriteFile(certPath, []byte("cert"), 0644))
		require.NoError(t, os.WriteFile(keyPath, []byte("key"), 0600))

		assert.True(t, m.CertificateExists())
	})
}

func TestSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	logger := zerolog.Nop()
	m := NewManager(Config{CertsDir: tmpDir, Logger: logger})

	agentID := "test-agent"
	colonyID := "test-colony"
	notAfter := time.Now().Add(90 * 24 * time.Hour)

	certPEM, keyPEM, caPEM := generateTestCertificate(t, agentID, colonyID, notAfter)

	spiffeID, _ := url.Parse("spiffe://coral/colony/" + colonyID + "/agent/" + agentID)

	result := &bootstrap.Result{
		Certificate:   certPEM,
		PrivateKey:    keyPEM,
		CAChain:       caPEM,
		RootCA:        caPEM,
		ExpiresAt:     notAfter,
		AgentSPIFFEID: spiffeID.String(),
	}

	// Save.
	err := m.Save(result)
	require.NoError(t, err)

	// Verify files exist with correct permissions.
	certInfo, err := os.Stat(filepath.Join(tmpDir, CertFileName))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0644), certInfo.Mode().Perm())

	keyInfo, err := os.Stat(filepath.Join(tmpDir, KeyFileName))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), keyInfo.Mode().Perm())

	// Load.
	err = m.Load()
	require.NoError(t, err)

	info := m.GetCertificateInfo()
	require.NotNil(t, info)

	assert.Equal(t, agentID, info.AgentID)
	assert.Equal(t, colonyID, info.ColonyID)
	assert.Equal(t, spiffeID.String(), info.SPIFFEID)
	assert.Equal(t, CertStatusValid, info.Status)
}

func TestCertificateStatus(t *testing.T) {
	tests := []struct {
		name           string
		expiryOffset   time.Duration
		expectedStatus CertStatus
	}{
		{
			name:           "valid certificate",
			expiryOffset:   60 * 24 * time.Hour, // 60 days
			expectedStatus: CertStatusValid,
		},
		{
			name:           "renewal needed",
			expiryOffset:   25 * 24 * time.Hour, // 25 days
			expectedStatus: CertStatusRenewalNeeded,
		},
		{
			name:           "expiring soon",
			expiryOffset:   5 * 24 * time.Hour, // 5 days
			expectedStatus: CertStatusExpiringSoon,
		},
		{
			name:           "expired",
			expiryOffset:   -1 * time.Hour, // Expired
			expectedStatus: CertStatusExpired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			logger := zerolog.Nop()
			m := NewManager(Config{CertsDir: tmpDir, Logger: logger})

			notAfter := time.Now().Add(tt.expiryOffset)
			certPEM, keyPEM, caPEM := generateTestCertificate(t, "agent", "colony", notAfter)

			result := &bootstrap.Result{
				Certificate:   certPEM,
				PrivateKey:    keyPEM,
				CAChain:       caPEM,
				RootCA:        caPEM,
				ExpiresAt:     notAfter,
				AgentSPIFFEID: "spiffe://coral/colony/colony/agent/agent",
			}

			require.NoError(t, m.Save(result))
			require.NoError(t, m.Load())

			info := m.GetCertificateInfo()
			assert.Equal(t, tt.expectedStatus, info.Status)
		})
	}
}

func TestSaveAgentID(t *testing.T) {
	tmpDir := t.TempDir()
	logger := zerolog.Nop()
	m := NewManager(Config{CertsDir: tmpDir, Logger: logger})

	agentID := "my-test-agent"

	err := m.SaveAgentID(agentID)
	require.NoError(t, err)

	loaded, err := m.LoadAgentID()
	require.NoError(t, err)
	assert.Equal(t, agentID, loaded)
}

func TestGetTLSConfig(t *testing.T) {
	t.Run("no certificate loaded", func(t *testing.T) {
		tmpDir := t.TempDir()
		logger := zerolog.Nop()
		m := NewManager(Config{CertsDir: tmpDir, Logger: logger})

		_, err := m.GetTLSConfig()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no certificate loaded")
	})

	t.Run("with certificate loaded", func(t *testing.T) {
		tmpDir := t.TempDir()
		logger := zerolog.Nop()
		m := NewManager(Config{CertsDir: tmpDir, Logger: logger})

		notAfter := time.Now().Add(90 * 24 * time.Hour)
		certPEM, keyPEM, caPEM := generateTestCertificate(t, "agent", "colony", notAfter)

		result := &bootstrap.Result{
			Certificate:   certPEM,
			PrivateKey:    keyPEM,
			CAChain:       caPEM,
			RootCA:        caPEM,
			ExpiresAt:     notAfter,
			AgentSPIFFEID: "spiffe://coral/colony/colony/agent/agent",
		}

		require.NoError(t, m.Save(result))
		require.NoError(t, m.Load())

		tlsConfig, err := m.GetTLSConfig()
		require.NoError(t, err)
		assert.NotNil(t, tlsConfig)
		assert.Len(t, tlsConfig.Certificates, 1)
		assert.NotNil(t, tlsConfig.RootCAs)
	})
}

func TestNeedsRenewal(t *testing.T) {
	tests := []struct {
		name         string
		expiryOffset time.Duration
		expected     bool
	}{
		{
			name:         "valid - no renewal",
			expiryOffset: 60 * 24 * time.Hour,
			expected:     false,
		},
		{
			name:         "renewal needed",
			expiryOffset: 25 * 24 * time.Hour,
			expected:     true,
		},
		{
			name:         "expiring soon",
			expiryOffset: 5 * 24 * time.Hour,
			expected:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			logger := zerolog.Nop()
			m := NewManager(Config{CertsDir: tmpDir, Logger: logger})

			notAfter := time.Now().Add(tt.expiryOffset)
			certPEM, keyPEM, caPEM := generateTestCertificate(t, "agent", "colony", notAfter)

			result := &bootstrap.Result{
				Certificate:   certPEM,
				PrivateKey:    keyPEM,
				CAChain:       caPEM,
				RootCA:        caPEM,
				ExpiresAt:     notAfter,
				AgentSPIFFEID: "spiffe://coral/colony/colony/agent/agent",
			}

			require.NoError(t, m.Save(result))
			require.NoError(t, m.Load())

			assert.Equal(t, tt.expected, m.NeedsRenewal())
		})
	}
}

func TestNeedsBootstrap(t *testing.T) {
	t.Run("no certificate", func(t *testing.T) {
		tmpDir := t.TempDir()
		logger := zerolog.Nop()
		m := NewManager(Config{CertsDir: tmpDir, Logger: logger})

		assert.True(t, m.NeedsBootstrap())
	})

	t.Run("valid certificate", func(t *testing.T) {
		tmpDir := t.TempDir()
		logger := zerolog.Nop()
		m := NewManager(Config{CertsDir: tmpDir, Logger: logger})

		notAfter := time.Now().Add(60 * 24 * time.Hour)
		certPEM, keyPEM, caPEM := generateTestCertificate(t, "agent", "colony", notAfter)

		result := &bootstrap.Result{
			Certificate:   certPEM,
			PrivateKey:    keyPEM,
			CAChain:       caPEM,
			RootCA:        caPEM,
			ExpiresAt:     notAfter,
			AgentSPIFFEID: "spiffe://coral/colony/colony/agent/agent",
		}

		require.NoError(t, m.Save(result))

		assert.False(t, m.NeedsBootstrap())
	})

	t.Run("expired certificate", func(t *testing.T) {
		tmpDir := t.TempDir()
		logger := zerolog.Nop()
		m := NewManager(Config{CertsDir: tmpDir, Logger: logger})

		notAfter := time.Now().Add(-1 * time.Hour) // Expired
		certPEM, keyPEM, caPEM := generateTestCertificate(t, "agent", "colony", notAfter)

		result := &bootstrap.Result{
			Certificate:   certPEM,
			PrivateKey:    keyPEM,
			CAChain:       caPEM,
			RootCA:        caPEM,
			ExpiresAt:     notAfter,
			AgentSPIFFEID: "spiffe://coral/colony/colony/agent/agent",
		}

		require.NoError(t, m.Save(result))

		assert.True(t, m.NeedsBootstrap())
	})
}

func TestPathMethods(t *testing.T) {
	tmpDir := t.TempDir()
	logger := zerolog.Nop()
	m := NewManager(Config{CertsDir: tmpDir, Logger: logger})

	assert.Equal(t, tmpDir, m.GetCertsDir())
	assert.Equal(t, filepath.Join(tmpDir, CertFileName), m.GetCertPath())
	assert.Equal(t, filepath.Join(tmpDir, KeyFileName), m.GetKeyPath())
	assert.Equal(t, filepath.Join(tmpDir, RootCAFileName), m.GetRootCAPath())
	assert.Equal(t, filepath.Join(tmpDir, CAChainFileName), m.GetCAChainPath())
}
