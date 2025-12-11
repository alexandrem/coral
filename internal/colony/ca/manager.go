// Package ca provides certificate authority management for mTLS.
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
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/coral-mesh/coral/internal/privilege"
)

// Manager handles certificate authority operations for agent mTLS.
// Implements RFD 047 - Colony CA Infrastructure & Policy Signing.
type Manager struct {
	db                     *sql.DB
	rootCert               *x509.Certificate
	serverIntermediateCert *x509.Certificate
	serverIntermediateKey  *ecdsa.PrivateKey
	agentIntermediateCert  *x509.Certificate
	agentIntermediateKey   *ecdsa.PrivateKey
	policySigningCert      *x509.Certificate
	policySigningKey       *ecdsa.PrivateKey
	jwtSigningKey          []byte
	colonyID               string
	caDir                  string // Filesystem path for CA storage.
}

// Config contains CA manager configuration.
type Config struct {
	ColonyID      string
	CADir         string // Filesystem path for CA storage (~/.coral/colonies/<id>/ca/).
	JWTSigningKey []byte
	KMSKeyID      string // Optional KMS key for envelope encryption.
}

// NewManager creates a new CA manager instance.
func NewManager(db *sql.DB, cfg Config) (*Manager, error) {
	m := &Manager{
		db:            db,
		jwtSigningKey: cfg.JWTSigningKey,
		colonyID:      cfg.ColonyID,
		caDir:         cfg.CADir,
	}

	// Initialize or load CA state from filesystem.
	if err := m.initializeCA(cfg.KMSKeyID); err != nil {
		return nil, fmt.Errorf("failed to initialize CA: %w", err)
	}

	return m, nil
}

// initializeCA initializes the CA or loads existing CA state from the filesystem.
func (m *Manager) initializeCA(kmsKeyID string) error {
	// Check if CA already exists on filesystem.
	rootCertPath := filepath.Join(m.caDir, "root-ca.crt")
	if _, err := os.Stat(rootCertPath); err == nil {
		return m.loadCA()
	}

	// Generate new CA hierarchy.
	return m.generateCA(kmsKeyID)
}

// generateCA generates a new 3-level PKI hierarchy per RFD 047.
// Structure: Root CA -> Server Intermediate + Agent Intermediate + Policy Signing.
func (m *Manager) generateCA(kmsKeyID string) error {
	// Create CA directory with secure permissions.
	if err := os.MkdirAll(m.caDir, 0700); err != nil {
		return fmt.Errorf("failed to create CA directory: %w", err)
	}

	// Generate Root CA (10-year validity).
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
		MaxPathLen:            2, // Allow 2 levels below root.
	}

	rootCertDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		return fmt.Errorf("failed to create root certificate: %w", err)
	}

	rootCert, err := x509.ParseCertificate(rootCertDER)
	if err != nil {
		return fmt.Errorf("failed to parse root certificate: %w", err)
	}

	// Generate Server Intermediate CA (1-year validity).
	serverIntKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate server intermediate key: %w", err)
	}

	serverIntTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			Organization: []string{"Coral"},
			CommonName:   fmt.Sprintf("Coral Server Intermediate CA - %s", m.colonyID),
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(1, 0, 0), // 1 year.
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}

	serverIntCertDER, err := x509.CreateCertificate(rand.Reader, serverIntTemplate, rootCert, &serverIntKey.PublicKey, rootKey)
	if err != nil {
		return fmt.Errorf("failed to create server intermediate certificate: %w", err)
	}

	serverIntCert, err := x509.ParseCertificate(serverIntCertDER)
	if err != nil {
		return fmt.Errorf("failed to parse server intermediate certificate: %w", err)
	}

	// Generate Agent Intermediate CA (1-year validity).
	agentIntKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate agent intermediate key: %w", err)
	}

	agentIntTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject: pkix.Name{
			Organization: []string{"Coral"},
			CommonName:   fmt.Sprintf("Coral Agent Intermediate CA - %s", m.colonyID),
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(1, 0, 0), // 1 year.
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}

	agentIntCertDER, err := x509.CreateCertificate(rand.Reader, agentIntTemplate, rootCert, &agentIntKey.PublicKey, rootKey)
	if err != nil {
		return fmt.Errorf("failed to create agent intermediate certificate: %w", err)
	}

	agentIntCert, err := x509.ParseCertificate(agentIntCertDER)
	if err != nil {
		return fmt.Errorf("failed to parse agent intermediate certificate: %w", err)
	}

	// Generate Policy Signing Certificate (10-year validity, same as Root).
	policySignKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate policy signing key: %w", err)
	}

	policySignTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(4),
		Subject: pkix.Name{
			Organization: []string{"Coral"},
			CommonName:   fmt.Sprintf("Coral Policy Signing - %s", m.colonyID),
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(10, 0, 0), // 10 years.
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  false,
	}

	policySignCertDER, err := x509.CreateCertificate(rand.Reader, policySignTemplate, rootCert, &policySignKey.PublicKey, rootKey)
	if err != nil {
		return fmt.Errorf("failed to create policy signing certificate: %w", err)
	}

	policySignCert, err := x509.ParseCertificate(policySignCertDER)
	if err != nil {
		return fmt.Errorf("failed to parse policy signing certificate: %w", err)
	}

	// Save all certificates and keys to filesystem.
	if err := m.saveCertAndKey("root-ca", rootCertDER, rootKey); err != nil {
		return fmt.Errorf("failed to save root CA: %w", err)
	}
	if err := m.saveCertAndKey("server-intermediate", serverIntCertDER, serverIntKey); err != nil {
		return fmt.Errorf("failed to save server intermediate: %w", err)
	}
	if err := m.saveCertAndKey("agent-intermediate", agentIntCertDER, agentIntKey); err != nil {
		return fmt.Errorf("failed to save agent intermediate: %w", err)
	}
	if err := m.saveCertAndKey("policy-signing", policySignCertDER, policySignKey); err != nil {
		return fmt.Errorf("failed to save policy signing cert: %w", err)
	}

	// Load into memory.
	m.rootCert = rootCert
	m.serverIntermediateCert = serverIntCert
	m.serverIntermediateKey = serverIntKey
	m.agentIntermediateCert = agentIntCert
	m.agentIntermediateKey = agentIntKey
	m.policySigningCert = policySignCert
	m.policySigningKey = policySignKey

	// Fix ownership if running as root (e.g., via sudo).
	if err := fixCAOwnership(m.caDir); err != nil {
		return fmt.Errorf("failed to fix CA ownership: %w", err)
	}

	return nil
}

// saveCertAndKey saves a certificate and private key to the CA directory.
func (m *Manager) saveCertAndKey(name string, certDER []byte, key *ecdsa.PrivateKey) error {
	// Save certificate.
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})
	certPath := filepath.Join(m.caDir, name+".crt")
	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		return fmt.Errorf("failed to write certificate: %w", err)
	}

	// Save private key with restricted permissions.
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: keyBytes,
	})
	keyPath := filepath.Join(m.caDir, name+".key")
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return fmt.Errorf("failed to write private key: %w", err)
	}

	return nil
}

// loadCA loads existing CA state from the filesystem.
func (m *Manager) loadCA() error {
	var err error

	// Load Root CA certificate.
	m.rootCert, err = m.loadCert("root-ca")
	if err != nil {
		return fmt.Errorf("failed to load root CA: %w", err)
	}

	// Load Server Intermediate CA.
	m.serverIntermediateCert, err = m.loadCert("server-intermediate")
	if err != nil {
		return fmt.Errorf("failed to load server intermediate cert: %w", err)
	}
	m.serverIntermediateKey, err = m.loadKey("server-intermediate")
	if err != nil {
		return fmt.Errorf("failed to load server intermediate key: %w", err)
	}

	// Load Agent Intermediate CA.
	m.agentIntermediateCert, err = m.loadCert("agent-intermediate")
	if err != nil {
		return fmt.Errorf("failed to load agent intermediate cert: %w", err)
	}
	m.agentIntermediateKey, err = m.loadKey("agent-intermediate")
	if err != nil {
		return fmt.Errorf("failed to load agent intermediate key: %w", err)
	}

	// Load Policy Signing Certificate.
	m.policySigningCert, err = m.loadCert("policy-signing")
	if err != nil {
		return fmt.Errorf("failed to load policy signing cert: %w", err)
	}
	m.policySigningKey, err = m.loadKey("policy-signing")
	if err != nil {
		return fmt.Errorf("failed to load policy signing key: %w", err)
	}

	return nil
}

// loadCert loads a certificate from the CA directory.
func (m *Manager) loadCert(name string) (*x509.Certificate, error) {
	certPath := filepath.Join(m.caDir, name+".crt")
	//nolint:gosec // G304: Path is constructed from trusted CA directory and validated name.
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read certificate file: %w", err)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode certificate PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	return cert, nil
}

// loadKey loads a private key from the CA directory.
func (m *Manager) loadKey(name string) (*ecdsa.PrivateKey, error) {
	keyPath := filepath.Join(m.caDir, name+".key")
	//nolint:gosec // G304: Path is constructed from trusted CA directory and validated name.
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read key file: %w", err)
	}

	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode key PEM")
	}

	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse key: %w", err)
	}

	return key, nil
}

// ReferralClaims contains JWT claims for referral tickets (RFD 049).
type ReferralClaims struct {
	ReefID   string `json:"reef_id"`
	ColonyID string `json:"colony_id"`
	AgentID  string `json:"agent_id"`
	Intent   string `json:"intent"`
	jwt.RegisteredClaims
}

// ValidateReferralTicket validates a referral ticket JWT.
// This is a stateless validation per RFD 049.
func (m *Manager) ValidateReferralTicket(tokenString string) (*ReferralClaims, error) {
	// Parse and validate JWT.
	token, err := jwt.ParseWithClaims(tokenString, &ReferralClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.jwtSigningKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	claims, ok := token.Claims.(*ReferralClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	// Verify audience.
	// We accept both "colony-step-ca" (legacy) and "coral-colony" (RFD 049).
	validAudience := false
	for _, aud := range claims.Audience {
		if aud == "colony-step-ca" || aud == "coral-colony" {
			validAudience = true
			break
		}
	}
	if !validAudience {
		return nil, fmt.Errorf("invalid audience: %v", claims.Audience)
	}

	// Verify issuer.
	// We accept both "reef-control" (legacy) and "coral-discovery" (RFD 049).
	if claims.Issuer != "reef-control" && claims.Issuer != "coral-discovery" {
		return nil, fmt.Errorf("invalid issuer: %s", claims.Issuer)
	}

	return claims, nil
}

// IssueCertificate issues a new client certificate for an agent.
// Uses the Agent Intermediate CA and includes SPIFFE ID in SAN per RFD 047.
func (m *Manager) IssueCertificate(agentID, colonyID string, csrPEM []byte) ([]byte, []byte, time.Time, error) {
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
	expectedCN := fmt.Sprintf("agent.%s.%s", agentID, colonyID)
	if csr.Subject.CommonName != expectedCN {
		return nil, nil, time.Time{}, fmt.Errorf("CSR CN mismatch: expected %s, got %s", expectedCN, csr.Subject.CommonName)
	}

	// Generate serial number.
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to generate serial number: %w", err)
	}

	// Build SPIFFE ID URI for the agent.
	spiffeID, err := url.Parse(fmt.Sprintf("spiffe://coral/colony/%s/agent/%s", colonyID, agentID))
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to parse SPIFFE ID: %w", err)
	}

	// Create certificate with SPIFFE ID in SAN.
	notBefore := time.Now()
	notAfter := notBefore.AddDate(0, 0, 90) // 90-day validity.

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      csr.Subject,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:         []*url.URL{spiffeID}, // SPIFFE ID in SAN.
	}

	// Sign with Agent Intermediate CA.
	certDER, err := x509.CreateCertificate(rand.Reader, template, m.agentIntermediateCert, csr.PublicKey, m.agentIntermediateKey)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to create certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	// Build CA chain: Agent Intermediate -> Root.
	caChain := append(pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: m.agentIntermediateCert.Raw,
	}), pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: m.rootCert.Raw,
	})...)

	// Store certificate.
	tx, err := m.db.Begin()
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func(tx *sql.Tx) {
		_ = tx.Rollback() // TODO: errcheck
	}(tx)

	_, err = tx.Exec(`
		INSERT INTO issued_certificates (
			serial_number, agent_id, colony_id,
			certificate_pem, issued_at, expires_at, status
		) VALUES (?, ?, ?, ?, ?, ?, 'active')
	`, serialNumber.String(), agentID, colonyID, string(certPEM), notBefore, notAfter)
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
	defer func(tx *sql.Tx) {
		_ = tx.Rollback() // TODO: errcheck
	}(tx)

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

	return nil
}

// RotateIntermediate rotates an intermediate CA certificate.
// typeStr must be "server" or "agent".
func (m *Manager) RotateIntermediate(typeStr string) error {
	var certName string
	var commonNamePrefix string

	switch typeStr {
	case "server":
		certName = "server-intermediate"
		commonNamePrefix = "Coral Server Intermediate CA"
	case "agent":
		certName = "agent-intermediate"
		commonNamePrefix = "Coral Agent Intermediate CA"
	default:
		return fmt.Errorf("invalid certificate type: %s", typeStr)
	}

	// Generate new key.
	newKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate new key: %w", err)
	}

	// Create new certificate template.
	// We use a random serial number to avoid collisions.
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("failed to generate serial number: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Coral"},
			CommonName:   fmt.Sprintf("%s - %s", commonNamePrefix, m.colonyID),
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(1, 0, 0), // 1 year.
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}

	// Sign with Root CA.
	certDER, err := x509.CreateCertificate(rand.Reader, template, m.rootCert, &newKey.PublicKey, m.loadRootKey())
	if err != nil {
		return fmt.Errorf("failed to create new intermediate certificate: %w", err)
	}

	// Archive current certificate and key.
	// We rename them to .old.<timestamp>
	timestamp := time.Now().Format("20060102150405")
	oldCertPath := filepath.Join(m.caDir, certName+".crt")
	oldKeyPath := filepath.Join(m.caDir, certName+".key")

	if err := os.Rename(oldCertPath, filepath.Join(m.caDir, fmt.Sprintf("%s.old.%s.crt", certName, timestamp))); err != nil {
		return fmt.Errorf("failed to archive old certificate: %w", err)
	}
	if err := os.Rename(oldKeyPath, filepath.Join(m.caDir, fmt.Sprintf("%s.old.%s.key", certName, timestamp))); err != nil {
		return fmt.Errorf("failed to archive old key: %w", err)
	}

	// Save new certificate and key.
	if err := m.saveCertAndKey(certName, certDER, newKey); err != nil {
		return fmt.Errorf("failed to save new certificate and key: %w", err)
	}

	// Reload CA to pick up changes.
	return m.loadCA()
}

// loadRootKey loads the root private key from disk.
// This is a helper for rotation, as we don't keep the root key in memory by default for security.
func (m *Manager) loadRootKey() *ecdsa.PrivateKey {
	key, err := m.loadKey("root-ca")
	if err != nil {
		// In a real HSM scenario, this would handle the HSM connection.
		// For embedded CA, we just load from disk.
		// If it fails (e.g. permissions), we panic or return nil, but here we assume it's available if we are rotating.
		return nil
	}
	return key
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

// CAStatus contains status information about the CA hierarchy.
type CAStatus struct {
	RootCA struct {
		Fingerprint string
		ExpiresAt   time.Time
		Path        string
	}
	ServerIntermediate struct {
		ExpiresAt time.Time
		Path      string
	}
	AgentIntermediate struct {
		ExpiresAt time.Time
		Path      string
	}
	PolicySigning struct {
		ExpiresAt time.Time
		Path      string
	}
	ColonySPIFFEID string
}

// GetStatus returns the current status of the CA hierarchy.
func (m *Manager) GetStatus() *CAStatus {
	status := &CAStatus{
		ColonySPIFFEID: fmt.Sprintf("spiffe://coral/colony/%s", m.colonyID),
	}

	status.RootCA.Fingerprint = m.GetCAFingerprint()
	status.RootCA.ExpiresAt = m.rootCert.NotAfter
	status.RootCA.Path = filepath.Join(m.caDir, "root-ca.crt")

	status.ServerIntermediate.ExpiresAt = m.serverIntermediateCert.NotAfter
	status.ServerIntermediate.Path = filepath.Join(m.caDir, "server-intermediate.crt")

	status.AgentIntermediate.ExpiresAt = m.agentIntermediateCert.NotAfter
	status.AgentIntermediate.Path = filepath.Join(m.caDir, "agent-intermediate.crt")

	status.PolicySigning.ExpiresAt = m.policySigningCert.NotAfter
	status.PolicySigning.Path = filepath.Join(m.caDir, "policy-signing.crt")

	return status
}

// GetServerIntermediateCertPEM returns the server intermediate CA certificate in PEM format.
func (m *Manager) GetServerIntermediateCertPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: m.serverIntermediateCert.Raw,
	})
}

// GetAgentIntermediateCertPEM returns the agent intermediate CA certificate in PEM format.
func (m *Manager) GetAgentIntermediateCertPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: m.agentIntermediateCert.Raw,
	})
}

// IssueServerCertificate issues a TLS server certificate for the colony.
// The certificate includes the colony's SPIFFE ID in SAN.
func (m *Manager) IssueServerCertificate(dnsNames []string) (certPEM, keyPEM []byte, err error) {
	// Generate server key.
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate server key: %w", err)
	}

	// Generate serial number.
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	// Build SPIFFE ID URI for the colony.
	spiffeID, err := url.Parse(fmt.Sprintf("spiffe://coral/colony/%s", m.colonyID))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse SPIFFE ID: %w", err)
	}

	// Create server certificate template.
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Coral"},
			CommonName:   fmt.Sprintf("colony.%s", m.colonyID),
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().AddDate(0, 0, 90), // 90-day validity.
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    dnsNames,
		URIs:        []*url.URL{spiffeID}, // SPIFFE ID in SAN.
	}

	// Sign with Server Intermediate CA.
	certDER, err := x509.CreateCertificate(rand.Reader, template, m.serverIntermediateCert, &serverKey.PublicKey, m.serverIntermediateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create server certificate: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	keyBytes, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal server key: %w", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: keyBytes,
	})

	return certPEM, keyPEM, nil
}

// GetServerCertChainPEM returns the server certificate chain in PEM format.
// Chain: Server Intermediate -> Root.
func (m *Manager) GetServerCertChainPEM() []byte {
	return append(pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: m.serverIntermediateCert.Raw,
	}), pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: m.rootCert.Raw,
	})...)
}

// InitResult contains the result of CA initialization.
type InitResult struct {
	CADir           string
	RootFingerprint string
	ColonySPIFFEID  string
	RootCAPath      string
	ServerIntPath   string
	AgentIntPath    string
	PolicySignPath  string
}

// Initialize generates a new CA hierarchy on the filesystem.
// This is a standalone function that doesn't require a database.
// Use this during `coral init` to generate the CA before the colony starts.
func Initialize(caDir, colonyID string) (*InitResult, error) {
	// Check if CA already exists.
	rootCertPath := filepath.Join(caDir, "root-ca.crt")
	if _, err := os.Stat(rootCertPath); err == nil {
		// CA already exists, load and return fingerprint.
		return loadInitResult(caDir, colonyID)
	}

	// Create CA directory with secure permissions.
	if err := os.MkdirAll(caDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create CA directory: %w", err)
	}

	// Fix ownership of parent colony directory if running as root.
	colonyDir := filepath.Dir(caDir)
	if err := privilege.FixFileOwnership(colonyDir); err != nil {
		return nil, fmt.Errorf("failed to fix colony directory ownership: %w", err)
	}

	// Generate Root CA (10-year validity).
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate root key: %w", err)
	}

	rootTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Coral"},
			CommonName:   fmt.Sprintf("Coral Root CA - %s", colonyID),
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            2,
	}

	rootCertDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create root certificate: %w", err)
	}

	// Generate Server Intermediate CA (1-year validity).
	serverIntKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate server intermediate key: %w", err)
	}

	rootCert, _ := x509.ParseCertificate(rootCertDER)

	serverIntTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			Organization: []string{"Coral"},
			CommonName:   fmt.Sprintf("Coral Server Intermediate CA - %s", colonyID),
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}

	serverIntCertDER, err := x509.CreateCertificate(rand.Reader, serverIntTemplate, rootCert, &serverIntKey.PublicKey, rootKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create server intermediate certificate: %w", err)
	}

	// Generate Agent Intermediate CA (1-year validity).
	agentIntKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate agent intermediate key: %w", err)
	}

	agentIntTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject: pkix.Name{
			Organization: []string{"Coral"},
			CommonName:   fmt.Sprintf("Coral Agent Intermediate CA - %s", colonyID),
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}

	agentIntCertDER, err := x509.CreateCertificate(rand.Reader, agentIntTemplate, rootCert, &agentIntKey.PublicKey, rootKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create agent intermediate certificate: %w", err)
	}

	// Generate Policy Signing Certificate (10-year validity).
	policySignKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate policy signing key: %w", err)
	}

	policySignTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(4),
		Subject: pkix.Name{
			Organization: []string{"Coral"},
			CommonName:   fmt.Sprintf("Coral Policy Signing - %s", colonyID),
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  false,
	}

	policySignCertDER, err := x509.CreateCertificate(rand.Reader, policySignTemplate, rootCert, &policySignKey.PublicKey, rootKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create policy signing certificate: %w", err)
	}

	// Save all certificates and keys to filesystem.
	if err := saveCertAndKeyStandalone(caDir, "root-ca", rootCertDER, rootKey); err != nil {
		return nil, fmt.Errorf("failed to save root CA: %w", err)
	}
	if err := saveCertAndKeyStandalone(caDir, "server-intermediate", serverIntCertDER, serverIntKey); err != nil {
		return nil, fmt.Errorf("failed to save server intermediate: %w", err)
	}
	if err := saveCertAndKeyStandalone(caDir, "agent-intermediate", agentIntCertDER, agentIntKey); err != nil {
		return nil, fmt.Errorf("failed to save agent intermediate: %w", err)
	}
	if err := saveCertAndKeyStandalone(caDir, "policy-signing", policySignCertDER, policySignKey); err != nil {
		return nil, fmt.Errorf("failed to save policy signing cert: %w", err)
	}

	// Fix ownership if running as root (e.g., via sudo or setuid).
	if err := fixCAOwnership(caDir); err != nil {
		return nil, fmt.Errorf("failed to fix CA ownership: %w", err)
	}

	// Compute fingerprint.
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(rootCertDER))

	return &InitResult{
		CADir:           caDir,
		RootFingerprint: fingerprint,
		ColonySPIFFEID:  fmt.Sprintf("spiffe://coral/colony/%s", colonyID),
		RootCAPath:      filepath.Join(caDir, "root-ca.crt"),
		ServerIntPath:   filepath.Join(caDir, "server-intermediate.crt"),
		AgentIntPath:    filepath.Join(caDir, "agent-intermediate.crt"),
		PolicySignPath:  filepath.Join(caDir, "policy-signing.crt"),
	}, nil
}

// loadInitResult loads CA info from existing files.
func loadInitResult(caDir, colonyID string) (*InitResult, error) {
	rootCertPath := filepath.Join(caDir, "root-ca.crt")
	//nolint:gosec // G304: Path is constructed from trusted CA directory with fixed filename.
	certPEM, err := os.ReadFile(rootCertPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read root CA: %w", err)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode root CA PEM")
	}

	fingerprint := fmt.Sprintf("%x", sha256.Sum256(block.Bytes))

	return &InitResult{
		CADir:           caDir,
		RootFingerprint: fingerprint,
		ColonySPIFFEID:  fmt.Sprintf("spiffe://coral/colony/%s", colonyID),
		RootCAPath:      filepath.Join(caDir, "root-ca.crt"),
		ServerIntPath:   filepath.Join(caDir, "server-intermediate.crt"),
		AgentIntPath:    filepath.Join(caDir, "agent-intermediate.crt"),
		PolicySignPath:  filepath.Join(caDir, "policy-signing.crt"),
	}, nil
}

// saveCertAndKeyStandalone saves a certificate and private key to a directory.
func saveCertAndKeyStandalone(caDir, name string, certDER []byte, key *ecdsa.PrivateKey) error {
	// Save certificate.
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})
	certPath := filepath.Join(caDir, name+".crt")
	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		return fmt.Errorf("failed to write certificate: %w", err)
	}

	// Save private key with restricted permissions.
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: keyBytes,
	})
	keyPath := filepath.Join(caDir, name+".key")
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return fmt.Errorf("failed to write private key: %w", err)
	}

	return nil
}

// fixCAOwnership fixes file ownership for the CA directory and all files within
// when running as root (e.g., via sudo or setuid binary).
func fixCAOwnership(caDir string) error {
	if !privilege.IsRoot() {
		return nil
	}

	// Fix CA directory ownership.
	if err := privilege.FixFileOwnership(caDir); err != nil {
		return err
	}

	// Fix ownership of all files in the CA directory.
	entries, err := os.ReadDir(caDir)
	if err != nil {
		return fmt.Errorf("failed to read CA directory: %w", err)
	}

	for _, entry := range entries {
		path := filepath.Join(caDir, entry.Name())
		if err := privilege.FixFileOwnership(path); err != nil {
			return err
		}
	}

	return nil
}
