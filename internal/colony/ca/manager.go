package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Manager handles certificate authority operations for agent mTLS.
// Implements RFD 022 - Embedded step-ca for Agent mTLS Bootstrap.
type Manager struct {
	db               *sql.DB
	rootCert         *x509.Certificate
	intermediateCert *x509.Certificate
	intermediateKey  interface{}
	jwtSigningKey    []byte
	colonyID         string
}

// Config contains CA manager configuration.
type Config struct {
	ColonyID      string
	JWTSigningKey []byte
	KMSKeyID      string // Optional KMS key for envelope encryption.
}

// NewManager creates a new CA manager instance.
func NewManager(db *sql.DB, cfg Config) (*Manager, error) {
	m := &Manager{
		db:            db,
		jwtSigningKey: cfg.JWTSigningKey,
		colonyID:      cfg.ColonyID,
	}

	// Initialize or load CA state.
	if err := m.initializeCA(cfg.KMSKeyID); err != nil {
		return nil, fmt.Errorf("failed to initialize CA: %w", err)
	}

	return m, nil
}

// initializeCA initializes the CA or loads existing CA state from the database.
func (m *Manager) initializeCA(kmsKeyID string) error {
	// Check if CA already exists.
	var exists bool
	err := m.db.QueryRow("SELECT EXISTS(SELECT 1 FROM ca_metadata WHERE id = 1)").Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check CA existence: %w", err)
	}

	if exists {
		return m.loadCA()
	}

	// Generate new CA.
	return m.generateCA(kmsKeyID)
}

// generateCA generates a new root and intermediate CA.
func (m *Manager) generateCA(kmsKeyID string) error {
	// Generate root CA key (ECDSA P-256).
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate root key: %w", err)
	}

	rootTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Coral"},
			CommonName:   fmt.Sprintf("Coral Root CA - %s", m.colonyID),
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(10, 0, 0), // 10 years.
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}

	rootCertDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		return fmt.Errorf("failed to create root certificate: %w", err)
	}

	rootCert, err := x509.ParseCertificate(rootCertDER)
	if err != nil {
		return fmt.Errorf("failed to parse root certificate: %w", err)
	}

	// Generate intermediate CA key (ECDSA P-256).
	intermediateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate intermediate key: %w", err)
	}

	intermediateTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			Organization: []string{"Coral"},
			CommonName:   fmt.Sprintf("Coral Intermediate CA - %s", m.colonyID),
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(5, 0, 0), // 5 years.
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}

	intermediateCertDER, err := x509.CreateCertificate(rand.Reader, intermediateTemplate, rootCert, &intermediateKey.PublicKey, rootKey)
	if err != nil {
		return fmt.Errorf("failed to create intermediate certificate: %w", err)
	}

	intermediateCert, err := x509.ParseCertificate(intermediateCertDER)
	if err != nil {
		return fmt.Errorf("failed to parse intermediate certificate: %w", err)
	}

	// Encode certificates to PEM.
	rootCertPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: rootCertDER,
	})

	intermediateCertPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: intermediateCertDER,
	})

	// Encode keys to PEM.
	// TODO: Implement KMS envelope encryption for keys.
	// For now, we'll store them directly (this should be encrypted in production).
	rootKeyBytes, err := x509.MarshalECPrivateKey(rootKey)
	if err != nil {
		return fmt.Errorf("failed to marshal root key: %w", err)
	}
	rootKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: rootKeyBytes,
	})

	intermediateKeyBytes, err := x509.MarshalECPrivateKey(intermediateKey)
	if err != nil {
		return fmt.Errorf("failed to marshal intermediate key: %w", err)
	}
	intermediateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: intermediateKeyBytes,
	})

	// Compute fingerprints.
	rootFingerprint := fmt.Sprintf("%x", sha256.Sum256(rootCertDER))
	intermediateFingerprint := fmt.Sprintf("%x", sha256.Sum256(intermediateCertDER))

	// Store in database.
	_, err = m.db.Exec(`
		INSERT INTO ca_metadata (
			id, root_fingerprint, intermediate_fingerprint,
			root_cert_pem, intermediate_cert_pem,
			encrypted_root_key, encrypted_intermediate_key,
			kms_key_id, created_at, next_rotation_at
		) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		rootFingerprint, intermediateFingerprint,
		string(rootCertPEM), string(intermediateCertPEM),
		rootKeyPEM, intermediateKeyPEM,
		kmsKeyID, time.Now(), time.Now().AddDate(4, 0, 0), // Rotate intermediate in 4 years.
	)
	if err != nil {
		return fmt.Errorf("failed to store CA metadata: %w", err)
	}

	// Load into memory.
	m.rootCert = rootCert
	m.intermediateCert = intermediateCert
	m.intermediateKey = intermediateKey

	return nil
}

// loadCA loads existing CA state from the database.
func (m *Manager) loadCA() error {
	var rootCertPEM, intermediateCertPEM string
	var rootKeyPEM, intermediateKeyPEM []byte

	err := m.db.QueryRow(`
		SELECT root_cert_pem, intermediate_cert_pem,
		       encrypted_root_key, encrypted_intermediate_key
		FROM ca_metadata WHERE id = 1
	`).Scan(&rootCertPEM, &intermediateCertPEM, &rootKeyPEM, &intermediateKeyPEM)
	if err != nil {
		return fmt.Errorf("failed to load CA metadata: %w", err)
	}

	// Parse root certificate.
	rootBlock, _ := pem.Decode([]byte(rootCertPEM))
	if rootBlock == nil {
		return fmt.Errorf("failed to decode root certificate PEM")
	}

	rootCert, err := x509.ParseCertificate(rootBlock.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse root certificate: %w", err)
	}

	// Parse intermediate certificate.
	intermediateBlock, _ := pem.Decode([]byte(intermediateCertPEM))
	if intermediateBlock == nil {
		return fmt.Errorf("failed to decode intermediate certificate PEM")
	}

	intermediateCert, err := x509.ParseCertificate(intermediateBlock.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse intermediate certificate: %w", err)
	}

	// Parse intermediate key.
	// TODO: Implement KMS envelope decryption.
	intermediateKeyBlock, _ := pem.Decode(intermediateKeyPEM)
	if intermediateKeyBlock == nil {
		return fmt.Errorf("failed to decode intermediate key PEM")
	}

	intermediateKey, err := x509.ParseECPrivateKey(intermediateKeyBlock.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse intermediate key: %w", err)
	}

	m.rootCert = rootCert
	m.intermediateCert = intermediateCert
	m.intermediateKey = intermediateKey

	return nil
}

// ValidateToken validates a bootstrap JWT token and returns the claims.
func (m *Manager) ValidateToken(tokenString string) (*BootstrapClaims, error) {
	// Parse and validate JWT.
	token, err := jwt.ParseWithClaims(tokenString, &BootstrapClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.jwtSigningKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	claims, ok := token.Claims.(*BootstrapClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	// Verify token hasn't been consumed.
	tokenHash := sha256.Sum256([]byte(tokenString))
	tokenHashHex := hex.EncodeToString(tokenHash[:])

	var status string
	var consumedAt sql.NullTime
	err = m.db.QueryRow(`
		SELECT status, consumed_at FROM bootstrap_tokens
		WHERE jwt_hash = ?
	`, tokenHashHex).Scan(&status, &consumedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("token not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to check token status: %w", err)
	}

	if status != "active" || consumedAt.Valid {
		return nil, fmt.Errorf("token already consumed")
	}

	return claims, nil
}

// IssueCertificate issues a new client certificate for an agent.
func (m *Manager) IssueCertificate(claims *BootstrapClaims, csrPEM []byte, tokenString string) ([]byte, []byte, time.Time, error) {
	// Parse CSR.
	csrBlock, _ := pem.Decode(csrPEM)
	if csrBlock == nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to decode CSR PEM")
	}

	csr, err := x509.ParseCertificateRequest(csrBlock.Bytes)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to parse CSR: %w", err)
	}

	// Verify CSR signature.
	if err := csr.CheckSignature(); err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("invalid CSR signature: %w", err)
	}

	// Enforce policy: CN must match agent_id.
	expectedCN := fmt.Sprintf("agent.%s.%s", claims.AgentID, claims.ColonyID)
	if csr.Subject.CommonName != expectedCN {
		return nil, nil, time.Time{}, fmt.Errorf("CSR CN mismatch: expected %s, got %s", expectedCN, csr.Subject.CommonName)
	}

	// Generate serial number.
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to generate serial number: %w", err)
	}

	// Create certificate.
	notBefore := time.Now()
	notAfter := notBefore.AddDate(0, 0, 90) // 90-day validity.

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      csr.Subject,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, m.intermediateCert, csr.PublicKey, m.intermediateKey)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to create certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	// Build CA chain.
	caChain := append(pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: m.intermediateCert.Raw,
	}), pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: m.rootCert.Raw,
	})...)

	// Mark token as consumed and store certificate.
	tx, err := m.db.Begin()
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	tokenHash := sha256.Sum256([]byte(tokenString))
	tokenHashHex := hex.EncodeToString(tokenHash[:])

	_, err = tx.Exec(`
		UPDATE bootstrap_tokens
		SET status = 'consumed', consumed_at = ?, consumed_by = ?
		WHERE jwt_hash = ?
	`, time.Now(), claims.AgentID, tokenHashHex)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to mark token as consumed: %w", err)
	}

	_, err = tx.Exec(`
		INSERT INTO issued_certificates (
			serial_number, agent_id, colony_id,
			certificate_pem, issued_at, expires_at, status
		) VALUES (?, ?, ?, ?, ?, ?, 'active')
	`, serialNumber.String(), claims.AgentID, claims.ColonyID, string(certPEM), notBefore, notAfter)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to store certificate: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return certPEM, caChain, notAfter, nil
}

// RevokeCertificate revokes an issued certificate.
func (m *Manager) RevokeCertificate(serialNumber, reason, revokedBy string) error {
	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		UPDATE issued_certificates
		SET status = 'revoked', revoked_at = ?, revocation_reason = ?
		WHERE serial_number = ?
	`, time.Now(), reason, serialNumber)
	if err != nil {
		return fmt.Errorf("failed to update certificate status: %w", err)
	}

	_, err = tx.Exec(`
		INSERT INTO certificate_revocations (serial_number, reason, revoked_by)
		VALUES (?, ?, ?)
	`, serialNumber, reason, revokedBy)
	if err != nil {
		return fmt.Errorf("failed to record revocation: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// GetCAFingerprint returns the root CA fingerprint.
func (m *Manager) GetCAFingerprint() string {
	hash := sha256.Sum256(m.rootCert.Raw)
	return hex.EncodeToString(hash[:])
}

// GetRootCertPEM returns the root CA certificate in PEM format.
func (m *Manager) GetRootCertPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: m.rootCert.Raw,
	})
}

// BootstrapClaims contains JWT claims for bootstrap tokens.
type BootstrapClaims struct {
	ReefID   string `json:"reef_id"`
	ColonyID string `json:"colony_id"`
	AgentID  string `json:"agent_id"`
	Intent   string `json:"intent"`
	jwt.RegisteredClaims
}
