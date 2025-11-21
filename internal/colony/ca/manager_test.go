package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestInitialize(t *testing.T) {
	// Create temp directory.
	tmpDir, err := os.MkdirTemp("", "ca-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	colonyID := "test-colony"
	caDir := filepath.Join(tmpDir, "ca")

	// Test Initialization.
	result, err := Initialize(caDir, colonyID)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Verify files exist.
	files := []string{
		"root-ca.crt", "root-ca.key",
		"server-intermediate.crt", "server-intermediate.key",
		"agent-intermediate.crt", "agent-intermediate.key",
		"policy-signing.crt", "policy-signing.key",
	}

	for _, f := range files {
		path := filepath.Join(caDir, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("file %s not created", f)
		}
	}

	// Verify Root CA.
	rootPEM, err := os.ReadFile(filepath.Join(caDir, "root-ca.crt"))
	if err != nil {
		t.Fatalf("failed to read root CA: %v", err)
	}
	block, _ := pem.Decode(rootPEM)
	rootCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("failed to parse root CA: %v", err)
	}

	if rootCert.Subject.CommonName != "Coral Root CA - test-colony" {
		t.Errorf("unexpected root CA CN: %s", rootCert.Subject.CommonName)
	}

	// Verify result fingerprint matches.
	if result.RootFingerprint == "" {
		t.Error("empty root fingerprint")
	}
}

func TestValidateReferralTicket(t *testing.T) {
	// Setup
	jwtKey := []byte("test-secret")
	manager := &Manager{
		jwtSigningKey: jwtKey,
	}

	t.Run("valid ticket", func(t *testing.T) {
		// Create valid token
		claims := &ReferralClaims{
			ReefID:   "reef-1",
			ColonyID: "colony-1",
			AgentID:  "agent-1",
			Intent:   "bootstrap",
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    "coral-discovery",
				Audience:  jwt.ClaimStrings{"coral-colony"},
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		tokenString, err := token.SignedString(jwtKey)
		if err != nil {
			t.Fatalf("failed to sign token: %v", err)
		}

		// Validate
		gotClaims, err := manager.ValidateReferralTicket(tokenString)
		if err != nil {
			t.Errorf("ValidateReferralTicket failed: %v", err)
		}
		if gotClaims.AgentID != "agent-1" {
			t.Errorf("expected agent-1, got %s", gotClaims.AgentID)
		}
	})

	t.Run("invalid signature", func(t *testing.T) {
		claims := &ReferralClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    "coral-discovery",
				Audience:  jwt.ClaimStrings{"coral-colony"},
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		tokenString, err := token.SignedString([]byte("wrong-key"))
		if err != nil {
			t.Fatalf("failed to sign token: %v", err)
		}

		_, err = manager.ValidateReferralTicket(tokenString)
		if err == nil {
			t.Error("expected error for invalid signature")
		}
	})

	t.Run("expired token", func(t *testing.T) {
		claims := &ReferralClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    "coral-discovery",
				Audience:  jwt.ClaimStrings{"coral-colony"},
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		tokenString, err := token.SignedString(jwtKey)
		if err != nil {
			t.Fatalf("failed to sign token: %v", err)
		}

		_, err = manager.ValidateReferralTicket(tokenString)
		if err == nil {
			t.Error("expected error for expired token")
		}
	})

	t.Run("invalid issuer", func(t *testing.T) {
		claims := &ReferralClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    "bad-issuer",
				Audience:  jwt.ClaimStrings{"coral-colony"},
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		tokenString, err := token.SignedString(jwtKey)
		if err != nil {
			t.Fatalf("failed to sign token: %v", err)
		}

		_, err = manager.ValidateReferralTicket(tokenString)
		if err == nil {
			t.Error("expected error for invalid issuer")
		}
	})

	t.Run("invalid audience", func(t *testing.T) {
		claims := &ReferralClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    "coral-discovery",
				Audience:  jwt.ClaimStrings{"bad-audience"},
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		tokenString, err := token.SignedString(jwtKey)
		if err != nil {
			t.Fatalf("failed to sign token: %v", err)
		}

		_, err = manager.ValidateReferralTicket(tokenString)
		if err == nil {
			t.Error("expected error for invalid audience")
		}
	})
}

func TestInitialize_CAHierarchy(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ca-hierarchy-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	colonyID := "test-colony"
	caDir := filepath.Join(tmpDir, "ca")

	result, err := Initialize(caDir, colonyID)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Load all certificates for validation.
	rootCert := loadTestCert(t, filepath.Join(caDir, "root-ca.crt"))
	serverIntCert := loadTestCert(t, filepath.Join(caDir, "server-intermediate.crt"))
	agentIntCert := loadTestCert(t, filepath.Join(caDir, "agent-intermediate.crt"))
	policySignCert := loadTestCert(t, filepath.Join(caDir, "policy-signing.crt"))

	t.Run("root CA properties", func(t *testing.T) {
		if !rootCert.IsCA {
			t.Error("root cert should be CA")
		}
		if rootCert.MaxPathLen != 2 {
			t.Errorf("root MaxPathLen should be 2, got %d", rootCert.MaxPathLen)
		}
		// 10-year validity.
		expectedExpiry := time.Now().AddDate(10, 0, 0)
		if rootCert.NotAfter.Before(expectedExpiry.Add(-24*time.Hour)) ||
			rootCert.NotAfter.After(expectedExpiry.Add(24*time.Hour)) {
			t.Errorf("root CA validity not ~10 years: %v", rootCert.NotAfter)
		}
		if rootCert.KeyUsage&x509.KeyUsageCertSign == 0 {
			t.Error("root CA should have KeyUsageCertSign")
		}
	})

	t.Run("server intermediate properties", func(t *testing.T) {
		if !serverIntCert.IsCA {
			t.Error("server intermediate should be CA")
		}
		// MaxPathLen -1 means unset; 0 with MaxPathLenZero means constrained to 0.
		// Current impl doesn't set MaxPathLenZero, so we just verify it's a CA.
		// 1-year validity.
		expectedExpiry := time.Now().AddDate(1, 0, 0)
		if serverIntCert.NotAfter.Before(expectedExpiry.Add(-24*time.Hour)) ||
			serverIntCert.NotAfter.After(expectedExpiry.Add(24*time.Hour)) {
			t.Errorf("server intermediate validity not ~1 year: %v", serverIntCert.NotAfter)
		}
		// Verify signed by root.
		if err := serverIntCert.CheckSignatureFrom(rootCert); err != nil {
			t.Errorf("server intermediate not signed by root: %v", err)
		}
	})

	t.Run("agent intermediate properties", func(t *testing.T) {
		if !agentIntCert.IsCA {
			t.Error("agent intermediate should be CA")
		}
		// MaxPathLen -1 means unset; 0 with MaxPathLenZero means constrained to 0.
		// Current impl doesn't set MaxPathLenZero, so we just verify it's a CA.
		// 1-year validity.
		expectedExpiry := time.Now().AddDate(1, 0, 0)
		if agentIntCert.NotAfter.Before(expectedExpiry.Add(-24*time.Hour)) ||
			agentIntCert.NotAfter.After(expectedExpiry.Add(24*time.Hour)) {
			t.Errorf("agent intermediate validity not ~1 year: %v", agentIntCert.NotAfter)
		}
		// Verify signed by root.
		if err := agentIntCert.CheckSignatureFrom(rootCert); err != nil {
			t.Errorf("agent intermediate not signed by root: %v", err)
		}
	})

	t.Run("policy signing cert properties", func(t *testing.T) {
		if policySignCert.IsCA {
			t.Error("policy signing cert should not be CA")
		}
		// 10-year validity.
		expectedExpiry := time.Now().AddDate(10, 0, 0)
		if policySignCert.NotAfter.Before(expectedExpiry.Add(-24*time.Hour)) ||
			policySignCert.NotAfter.After(expectedExpiry.Add(24*time.Hour)) {
			t.Errorf("policy signing validity not ~10 years: %v", policySignCert.NotAfter)
		}
		if policySignCert.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
			t.Error("policy signing cert should have KeyUsageDigitalSignature")
		}
		// Verify signed by root.
		if err := policySignCert.CheckSignatureFrom(rootCert); err != nil {
			t.Errorf("policy signing cert not signed by root: %v", err)
		}
	})

	t.Run("result fields", func(t *testing.T) {
		if result.CADir != caDir {
			t.Errorf("CADir mismatch: %s vs %s", result.CADir, caDir)
		}
		if !strings.HasPrefix(result.ColonySPIFFEID, "spiffe://coral/colony/") {
			t.Errorf("invalid SPIFFE ID: %s", result.ColonySPIFFEID)
		}
		if len(result.RootFingerprint) != 64 { // SHA256 hex = 64 chars.
			t.Errorf("invalid fingerprint length: %d", len(result.RootFingerprint))
		}
	})
}

func TestInitialize_Idempotent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ca-idempotent-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	colonyID := "test-colony"
	caDir := filepath.Join(tmpDir, "ca")

	// First initialization.
	result1, err := Initialize(caDir, colonyID)
	if err != nil {
		t.Fatalf("first Initialize failed: %v", err)
	}

	// Second initialization should return same fingerprint.
	result2, err := Initialize(caDir, colonyID)
	if err != nil {
		t.Fatalf("second Initialize failed: %v", err)
	}

	if result1.RootFingerprint != result2.RootFingerprint {
		t.Errorf("fingerprint changed: %s vs %s", result1.RootFingerprint, result2.RootFingerprint)
	}
}

func TestInitialize_FilePermissions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ca-perms-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	caDir := filepath.Join(tmpDir, "ca")
	_, err = Initialize(caDir, "test-colony")
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Verify directory permissions.
	dirInfo, err := os.Stat(caDir)
	if err != nil {
		t.Fatalf("failed to stat CA dir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0700 {
		t.Errorf("CA dir permissions should be 0700, got %o", dirInfo.Mode().Perm())
	}

	// Verify key file permissions (0600).
	keyFiles := []string{"root-ca.key", "server-intermediate.key", "agent-intermediate.key", "policy-signing.key"}
	for _, f := range keyFiles {
		info, err := os.Stat(filepath.Join(caDir, f))
		if err != nil {
			t.Errorf("failed to stat %s: %v", f, err)
			continue
		}
		if info.Mode().Perm() != 0600 {
			t.Errorf("%s permissions should be 0600, got %o", f, info.Mode().Perm())
		}
	}

	// Verify cert file permissions (0644).
	certFiles := []string{"root-ca.crt", "server-intermediate.crt", "agent-intermediate.crt", "policy-signing.crt"}
	for _, f := range certFiles {
		info, err := os.Stat(filepath.Join(caDir, f))
		if err != nil {
			t.Errorf("failed to stat %s: %v", f, err)
			continue
		}
		if info.Mode().Perm() != 0644 {
			t.Errorf("%s permissions should be 0644, got %o", f, info.Mode().Perm())
		}
	}
}

func TestIssueServerCertificate(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ca-server-cert-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	caDir := filepath.Join(tmpDir, "ca")
	colonyID := "test-colony"
	_, err = Initialize(caDir, colonyID)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Create manager (without DB for this test).
	manager := &Manager{
		colonyID: colonyID,
		caDir:    caDir,
	}
	if err := manager.loadCA(); err != nil {
		t.Fatalf("loadCA failed: %v", err)
	}

	dnsNames := []string{"localhost", "colony.example.com"}
	certPEM, keyPEM, err := manager.IssueServerCertificate(dnsNames)
	if err != nil {
		t.Fatalf("IssueServerCertificate failed: %v", err)
	}

	// Parse and validate certificate.
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("failed to decode cert PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}

	t.Run("DNS names", func(t *testing.T) {
		for _, dns := range dnsNames {
			found := false
			for _, certDNS := range cert.DNSNames {
				if certDNS == dns {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("DNS name %s not in certificate", dns)
			}
		}
	})

	t.Run("SPIFFE ID in SAN", func(t *testing.T) {
		if len(cert.URIs) == 0 {
			t.Fatal("no URIs in certificate")
		}
		expectedSPIFFE := fmt.Sprintf("spiffe://coral/colony/%s", colonyID)
		if cert.URIs[0].String() != expectedSPIFFE {
			t.Errorf("SPIFFE ID mismatch: %s vs %s", cert.URIs[0].String(), expectedSPIFFE)
		}
	})

	t.Run("server auth EKU", func(t *testing.T) {
		hasServerAuth := false
		for _, eku := range cert.ExtKeyUsage {
			if eku == x509.ExtKeyUsageServerAuth {
				hasServerAuth = true
				break
			}
		}
		if !hasServerAuth {
			t.Error("certificate should have ServerAuth EKU")
		}
	})

	t.Run("signed by server intermediate", func(t *testing.T) {
		if err := cert.CheckSignatureFrom(manager.serverIntermediateCert); err != nil {
			t.Errorf("certificate not signed by server intermediate: %v", err)
		}
	})

	t.Run("key is valid", func(t *testing.T) {
		keyBlock, _ := pem.Decode(keyPEM)
		if keyBlock == nil {
			t.Fatal("failed to decode key PEM")
		}
		_, err := x509.ParseECPrivateKey(keyBlock.Bytes)
		if err != nil {
			t.Errorf("failed to parse private key: %v", err)
		}
	})
}

func TestGetStatus(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ca-status-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	caDir := filepath.Join(tmpDir, "ca")
	colonyID := "test-colony"
	initResult, err := Initialize(caDir, colonyID)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	manager := &Manager{
		colonyID: colonyID,
		caDir:    caDir,
	}
	if err := manager.loadCA(); err != nil {
		t.Fatalf("loadCA failed: %v", err)
	}

	status := manager.GetStatus()

	if status.RootCA.Fingerprint != initResult.RootFingerprint {
		t.Errorf("fingerprint mismatch: %s vs %s", status.RootCA.Fingerprint, initResult.RootFingerprint)
	}

	if status.ColonySPIFFEID != fmt.Sprintf("spiffe://coral/colony/%s", colonyID) {
		t.Errorf("unexpected SPIFFE ID: %s", status.ColonySPIFFEID)
	}

	// Verify paths.
	if status.RootCA.Path != filepath.Join(caDir, "root-ca.crt") {
		t.Errorf("unexpected root CA path: %s", status.RootCA.Path)
	}

	// Verify expiry times are in the future.
	now := time.Now()
	if status.RootCA.ExpiresAt.Before(now) {
		t.Error("root CA already expired")
	}
	if status.ServerIntermediate.ExpiresAt.Before(now) {
		t.Error("server intermediate already expired")
	}
	if status.AgentIntermediate.ExpiresAt.Before(now) {
		t.Error("agent intermediate already expired")
	}
	if status.PolicySigning.ExpiresAt.Before(now) {
		t.Error("policy signing cert already expired")
	}
}

func TestGetCAFingerprint(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ca-fingerprint-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	caDir := filepath.Join(tmpDir, "ca")
	initResult, err := Initialize(caDir, "test-colony")
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	manager := &Manager{
		colonyID: "test-colony",
		caDir:    caDir,
	}
	if err := manager.loadCA(); err != nil {
		t.Fatalf("loadCA failed: %v", err)
	}

	fingerprint := manager.GetCAFingerprint()

	// Should be 64 hex characters (SHA256).
	if len(fingerprint) != 64 {
		t.Errorf("fingerprint should be 64 chars, got %d", len(fingerprint))
	}

	// Should match init result.
	if fingerprint != initResult.RootFingerprint {
		t.Errorf("fingerprint mismatch: %s vs %s", fingerprint, initResult.RootFingerprint)
	}
}

func TestRotateIntermediate(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ca-rotate-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	caDir := filepath.Join(tmpDir, "ca")
	colonyID := "test-colony"
	_, err = Initialize(caDir, colonyID)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	manager := &Manager{
		colonyID: colonyID,
		caDir:    caDir,
	}
	if err := manager.loadCA(); err != nil {
		t.Fatalf("loadCA failed: %v", err)
	}

	// Get original cert serial.
	origSerial := manager.serverIntermediateCert.SerialNumber

	t.Run("rotate server intermediate", func(t *testing.T) {
		err := manager.RotateIntermediate("server")
		if err != nil {
			t.Fatalf("RotateIntermediate failed: %v", err)
		}

		// Serial should change.
		if manager.serverIntermediateCert.SerialNumber.Cmp(origSerial) == 0 {
			t.Error("serial number should change after rotation")
		}

		// Should still be signed by root.
		if err := manager.serverIntermediateCert.CheckSignatureFrom(manager.rootCert); err != nil {
			t.Errorf("rotated cert not signed by root: %v", err)
		}

		// Old cert should be archived.
		entries, err := os.ReadDir(caDir)
		if err != nil {
			t.Fatalf("failed to read CA dir: %v", err)
		}
		hasArchive := false
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "server-intermediate.old.") {
				hasArchive = true
				break
			}
		}
		if !hasArchive {
			t.Error("old certificate not archived")
		}
	})

	t.Run("rotate agent intermediate", func(t *testing.T) {
		origAgentSerial := manager.agentIntermediateCert.SerialNumber

		err := manager.RotateIntermediate("agent")
		if err != nil {
			t.Fatalf("RotateIntermediate failed: %v", err)
		}

		if manager.agentIntermediateCert.SerialNumber.Cmp(origAgentSerial) == 0 {
			t.Error("serial number should change after rotation")
		}

		if err := manager.agentIntermediateCert.CheckSignatureFrom(manager.rootCert); err != nil {
			t.Errorf("rotated cert not signed by root: %v", err)
		}
	})

	t.Run("invalid type", func(t *testing.T) {
		err := manager.RotateIntermediate("invalid")
		if err == nil {
			t.Error("expected error for invalid type")
		}
	})
}

func TestGetCertPEMMethods(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ca-pem-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	caDir := filepath.Join(tmpDir, "ca")
	_, err = Initialize(caDir, "test-colony")
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	manager := &Manager{
		colonyID: "test-colony",
		caDir:    caDir,
	}
	if err := manager.loadCA(); err != nil {
		t.Fatalf("loadCA failed: %v", err)
	}

	t.Run("GetRootCertPEM", func(t *testing.T) {
		pem := manager.GetRootCertPEM()
		if len(pem) == 0 {
			t.Error("empty root cert PEM")
		}
		if !strings.Contains(string(pem), "BEGIN CERTIFICATE") {
			t.Error("invalid PEM format")
		}
	})

	t.Run("GetServerIntermediateCertPEM", func(t *testing.T) {
		pem := manager.GetServerIntermediateCertPEM()
		if len(pem) == 0 {
			t.Error("empty server intermediate cert PEM")
		}
	})

	t.Run("GetAgentIntermediateCertPEM", func(t *testing.T) {
		pem := manager.GetAgentIntermediateCertPEM()
		if len(pem) == 0 {
			t.Error("empty agent intermediate cert PEM")
		}
	})

	t.Run("GetServerCertChainPEM", func(t *testing.T) {
		chainPEM := manager.GetServerCertChainPEM()
		// Should contain 2 certificates.
		count := strings.Count(string(chainPEM), "BEGIN CERTIFICATE")
		if count != 2 {
			t.Errorf("chain should have 2 certs, got %d", count)
		}
	})
}

func TestIssueCertificate(t *testing.T) {
	// Skip if no test database available.
	// This test requires a database connection.
	t.Skip("IssueCertificate requires database; covered by integration tests")
}

// loadTestCert is a helper to load a certificate from a file.
func loadTestCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read cert %s: %v", path, err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatalf("failed to decode PEM for %s", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("failed to parse cert %s: %v", path, err)
	}
	return cert
}

// generateTestCSR creates a CSR for testing.
func generateTestCSR(t *testing.T, agentID, colonyID string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	template := &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName: fmt.Sprintf("agent.%s.%s", agentID, colonyID),
		},
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		t.Fatalf("failed to create CSR: %v", err)
	}

	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: csrDER,
	})
}
