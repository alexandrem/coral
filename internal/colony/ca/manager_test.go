package ca

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
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
