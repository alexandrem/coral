// Package ca provides certificate authority management for mTLS.
package ca

import (
	"context"
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
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/internal/colony/jwks"
	"github.com/coral-mesh/coral/internal/privilege"
	"github.com/coral-mesh/coral/internal/safe"
)

// IssuedCertificate represents a certificate issued to an agent (RFD 047).
type IssuedCertificate struct {
	SerialNumber     string     `duckdb:"serial_number,pk"`
	AgentID          string     `duckdb:"agent_id,immutable"`
	ColonyID         string     `duckdb:"colony_id,immutable"`
	CertificatePEM   string     `duckdb:"certificate_pem,immutable"`
	IssuedAt         time.Time  `duckdb:"issued_at,immutable"`
	ExpiresAt        time.Time  `duckdb:"expires_at,immutable"`
	RevokedAt        *time.Time `duckdb:"revoked_at"`        // Nullable
	RevocationReason *string    `duckdb:"revocation_reason"` // Nullable
	Status           string     `duckdb:"status"`
}

// Revocation represents a certificate revocation event.
type Revocation struct {
	ID           int64     `duckdb:"id,pk"` // Generated manually
	SerialNumber string    `duckdb:"serial_number"`
	RevokedAt    time.Time `duckdb:"revoked_at"`
	Reason       string    `duckdb:"reason"`
	RevokedBy    string    `duckdb:"revoked_by"`
}

// Manager handles certificate authority operations for agent mTLS.
// Implements RFD 047 - Colony CA Infrastructure & Policy Signing.
type Manager struct {
	db        *sql.DB
	crypto    *CryptoOperations
	fsStorage *FilesystemStorage
	dbStorage *DatabaseStorage
	policy    *PolicyEnforcer
	colonyID  string
	caDir     string // Filesystem path for CA storage.
	logger    zerolog.Logger
}

// Config contains CA manager configuration.
type Config struct {
	ColonyID   string
	CADir      string // Filesystem path for CA storage (~/.coral/colonies/<id>/ca/).
	JWKSClient *jwks.Client
	KMSKeyID   string // Optional KMS key for envelope encryption.
}

// NewManager creates a new CA manager instance.
func NewManager(db *sql.DB, logger zerolog.Logger, cfg Config) (*Manager, error) {
	m := &Manager{
		db:        db,
		colonyID:  cfg.ColonyID,
		caDir:     cfg.CADir,
		fsStorage: NewFilesystemStorage(cfg.CADir),
		dbStorage: NewDatabaseStorage(db, logger),
		policy:    NewPolicyEnforcer(cfg.JWKSClient, cfg.ColonyID),
		logger:    logger.With().Str("component", "ca_manager").Logger(),
	}

	// Initialize or load CA state from filesystem.
	if err := m.initializeCA(cfg.KMSKeyID); err != nil {
		return nil, fmt.Errorf("failed to initialize CA: %w", err)
	}

	// Ensure bootstrap_psks table exists (RFD 088).
	if err := m.ensurePSKTable(); err != nil {
		return nil, fmt.Errorf("failed to ensure PSK table: %w", err)
	}

	return m, nil
}

// initializeCA initializes the CA or loads existing CA state from the filesystem.
func (m *Manager) initializeCA(kmsKeyID string) error {
	// Check if CA already exists on filesystem.
	if m.fsStorage.CAExists() {
		return m.loadCA()
	}

	// Generate new CA hierarchy.
	return m.generateCA(kmsKeyID)
}

// generateCA generates a new 3-level PKI hierarchy per RFD 047.
// Structure: Root CA -> Server Intermediate + Agent Intermediate + Policy Signing.
func (m *Manager) generateCA(kmsKeyID string) error {
	// Create CA directory with secure permissions.
	if err := m.fsStorage.EnsureCADirectory(); err != nil {
		return err
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
	if err := m.fsStorage.SaveCertAndKey("root-ca", rootCertDER, rootKey); err != nil {
		return fmt.Errorf("failed to save root CA: %w", err)
	}
	if err := m.fsStorage.SaveCertAndKey("server-intermediate", serverIntCertDER, serverIntKey); err != nil {
		return fmt.Errorf("failed to save server intermediate: %w", err)
	}
	if err := m.fsStorage.SaveCertAndKey("agent-intermediate", agentIntCertDER, agentIntKey); err != nil {
		return fmt.Errorf("failed to save agent intermediate: %w", err)
	}
	if err := m.fsStorage.SaveCertAndKey("policy-signing", policySignCertDER, policySignKey); err != nil {
		return fmt.Errorf("failed to save policy signing cert: %w", err)
	}

	// Initialize crypto operations.
	m.crypto = NewCryptoOperations(
		rootCert,
		serverIntCert,
		serverIntKey,
		agentIntCert,
		agentIntKey,
		policySignCert,
		policySignKey,
	)

	// Fix ownership if running as root (e.g., via sudo).
	if err := m.fsStorage.FixOwnership(); err != nil {
		return fmt.Errorf("failed to fix CA ownership: %w", err)
	}

	return nil
}

// loadCA loads existing CA state from the filesystem.
func (m *Manager) loadCA() error {
	var err error

	// Load Root CA certificate.
	rootCert, err := m.fsStorage.LoadCert("root-ca")
	if err != nil {
		return fmt.Errorf("failed to load root CA: %w", err)
	}

	// Load Server Intermediate CA.
	serverIntermediateCert, err := m.fsStorage.LoadCert("server-intermediate")
	if err != nil {
		return fmt.Errorf("failed to load server intermediate cert: %w", err)
	}
	serverIntermediateKey, err := m.fsStorage.LoadKey("server-intermediate")
	if err != nil {
		return fmt.Errorf("failed to load server intermediate key: %w", err)
	}

	// Load Agent Intermediate CA.
	agentIntermediateCert, err := m.fsStorage.LoadCert("agent-intermediate")
	if err != nil {
		return fmt.Errorf("failed to load agent intermediate cert: %w", err)
	}
	agentIntermediateKey, err := m.fsStorage.LoadKey("agent-intermediate")
	if err != nil {
		return fmt.Errorf("failed to load agent intermediate key: %w", err)
	}

	// Load Policy Signing Certificate.
	policySigningCert, err := m.fsStorage.LoadCert("policy-signing")
	if err != nil {
		return fmt.Errorf("failed to load policy signing cert: %w", err)
	}
	policySigningKey, err := m.fsStorage.LoadKey("policy-signing")
	if err != nil {
		return fmt.Errorf("failed to load policy signing key: %w", err)
	}

	// Initialize crypto operations.
	m.crypto = NewCryptoOperations(
		rootCert,
		serverIntermediateCert,
		serverIntermediateKey,
		agentIntermediateCert,
		agentIntermediateKey,
		policySigningCert,
		policySigningKey,
	)

	return nil
}

// ValidateReferralTicket validates a referral ticket JWT.
// This is a stateless validation per RFD 049.
func (m *Manager) ValidateReferralTicket(tokenString string) (*ReferralClaims, error) {
	return m.policy.ValidateReferralTicket(tokenString)
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

	// Validate CSR using policy enforcer.
	if err := m.policy.ValidateAgentCSR(csr, agentID, colonyID); err != nil {
		return nil, nil, time.Time{}, err
	}

	// Check if certificate can be issued.
	if err := m.policy.CanIssueAgentCertificate(agentID, colonyID); err != nil {
		return nil, nil, time.Time{}, err
	}

	// Get certificate validity period from policy.
	validity := m.policy.GetCertificateValidity("agent")

	// Generate certificate using crypto operations.
	req := CertRequest{
		AgentID:  agentID,
		ColonyID: colonyID,
		CSR:      csr,
		CertType: "agent",
		Validity: validity,
	}

	cert, certDER, err := m.crypto.GenerateAgentCertificate(req)
	if err != nil {
		return nil, nil, time.Time{}, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	// Build CA chain: Agent Intermediate -> Root.
	chain := m.crypto.GetAgentCertChain()
	caChain := append(pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: chain[0].Raw,
	}), pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: chain[1].Raw,
	})...)

	// Store certificate in database.
	certMetadata := &CertificateMetadata{
		SerialNumber:   cert.SerialNumber.String(),
		AgentID:        agentID,
		ColonyID:       colonyID,
		CertificatePEM: string(certPEM),
		IssuedAt:       cert.NotBefore,
		ExpiresAt:      cert.NotAfter,
		Status:         "active",
	}

	if err := m.dbStorage.StoreCertificate(context.Background(), certMetadata); err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("failed to store certificate: %w", err)
	}

	return certPEM, caChain, cert.NotAfter, nil
}

// RevokeCertificate revokes an issued certificate.
func (m *Manager) RevokeCertificate(serialNumber, reason, revokedBy string) error {
	return m.dbStorage.RevokeCertificate(context.Background(), serialNumber, reason, revokedBy)
}

// RotateIntermediate rotates an intermediate CA certificate.
// typeStr must be "server" or "agent".
func (m *Manager) RotateIntermediate(typeStr string) error {
	// Validate using policy enforcer.
	if err := m.policy.CanRotateIntermediate(typeStr); err != nil {
		return err
	}

	var certName string

	switch typeStr {
	case "server":
		certName = "server-intermediate"
	case "agent":
		certName = "agent-intermediate"
	default:
		return fmt.Errorf("invalid certificate type: %s", typeStr)
	}

	// Load root key for signing.
	rootKey, err := m.fsStorage.LoadKey("root-ca")
	if err != nil {
		return fmt.Errorf("failed to load root key: %w", err)
	}

	// Generate new intermediate certificate.
	newCert, newKey, certDER, err := m.crypto.GenerateIntermediateCertificate(m.colonyID, typeStr, rootKey)
	if err != nil {
		return err
	}

	// Archive current certificate and key.
	if err := m.fsStorage.ArchiveCertAndKey(certName); err != nil {
		return err
	}

	// Save new certificate and key.
	if err := m.fsStorage.SaveCertAndKey(certName, certDER, newKey); err != nil {
		return fmt.Errorf("failed to save new certificate and key: %w", err)
	}

	// Update crypto operations with new certificate.
	switch typeStr {
	case "server":
		m.crypto.UpdateServerIntermediate(newCert, newKey)
	case "agent":
		m.crypto.UpdateAgentIntermediate(newCert, newKey)
	}

	return nil
}

// GetCAFingerprint returns the root CA fingerprint.
func (m *Manager) GetCAFingerprint() string {
	rootCert := m.crypto.GetRootCert()
	hash := sha256.Sum256(rootCert.Raw)
	return hex.EncodeToString(hash[:])
}

// GetRootCertPEM returns the root CA certificate in PEM format.
func (m *Manager) GetRootCertPEM() []byte {
	rootCert := m.crypto.GetRootCert()
	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: rootCert.Raw,
	})
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

	rootCert := m.crypto.GetRootCert()
	serverIntCert := m.crypto.GetServerIntermediateCert()
	agentIntCert := m.crypto.GetAgentIntermediateCert()
	policySignCert := m.crypto.GetPolicySigningCert()

	status.RootCA.Fingerprint = m.GetCAFingerprint()
	status.RootCA.ExpiresAt = rootCert.NotAfter
	status.RootCA.Path = filepath.Join(m.caDir, "root-ca.crt")

	status.ServerIntermediate.ExpiresAt = serverIntCert.NotAfter
	status.ServerIntermediate.Path = filepath.Join(m.caDir, "server-intermediate.crt")

	status.AgentIntermediate.ExpiresAt = agentIntCert.NotAfter
	status.AgentIntermediate.Path = filepath.Join(m.caDir, "agent-intermediate.crt")

	status.PolicySigning.ExpiresAt = policySignCert.NotAfter
	status.PolicySigning.Path = filepath.Join(m.caDir, "policy-signing.crt")

	return status
}

// GetServerIntermediateCertPEM returns the server intermediate CA certificate in PEM format.
func (m *Manager) GetServerIntermediateCertPEM() []byte {
	serverIntCert := m.crypto.GetServerIntermediateCert()
	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: serverIntCert.Raw,
	})
}

// GetAgentIntermediateCertPEM returns the agent intermediate CA certificate in PEM format.
func (m *Manager) GetAgentIntermediateCertPEM() []byte {
	agentIntCert := m.crypto.GetAgentIntermediateCert()
	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: agentIntCert.Raw,
	})
}

// IssueServerCertificate issues a TLS server certificate for the colony.
// The certificate includes the colony's SPIFFE ID in SAN.
func (m *Manager) IssueServerCertificate(dnsNames []string) (certPEM, keyPEM []byte, err error) {
	// Get certificate validity period from policy.
	validity := m.policy.GetCertificateValidity("server")

	// Generate server certificate using crypto operations.
	_, _, certDER, keyBytes, err := m.crypto.GenerateServerCertificate(m.colonyID, dnsNames, validity)
	if err != nil {
		return nil, nil, err
	}

	certPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	keyPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: keyBytes,
	})

	return certPEM, keyPEM, nil
}

// GetServerCertChainPEM returns the server certificate chain in PEM format.
// Chain: Server Intermediate -> Root.
func (m *Manager) GetServerCertChainPEM() []byte {
	chain := m.crypto.GetServerCertChain()
	return append(pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: chain[0].Raw,
	}), pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: chain[1].Raw,
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
	BootstrapPSK    string // Plaintext PSK for display (RFD 088).
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

	// Create filesystem storage.
	fsStorage := NewFilesystemStorage(caDir)

	// Save all certificates and keys to filesystem.
	if err := fsStorage.SaveCertAndKey("root-ca", rootCertDER, rootKey); err != nil {
		return nil, fmt.Errorf("failed to save root CA: %w", err)
	}
	if err := fsStorage.SaveCertAndKey("server-intermediate", serverIntCertDER, serverIntKey); err != nil {
		return nil, fmt.Errorf("failed to save server intermediate: %w", err)
	}
	if err := fsStorage.SaveCertAndKey("agent-intermediate", agentIntCertDER, agentIntKey); err != nil {
		return nil, fmt.Errorf("failed to save agent intermediate: %w", err)
	}
	if err := fsStorage.SaveCertAndKey("policy-signing", policySignCertDER, policySignKey); err != nil {
		return nil, fmt.Errorf("failed to save policy signing cert: %w", err)
	}

	// Fix ownership if running as root (e.g., via sudo or setuid).
	if err := fsStorage.FixOwnership(); err != nil {
		return nil, fmt.Errorf("failed to fix CA ownership: %w", err)
	}

	// Compute fingerprint.
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(rootCertDER))

	// Generate Bootstrap PSK (RFD 088).
	psk, err := GeneratePSK()
	if err != nil {
		return nil, fmt.Errorf("failed to generate bootstrap PSK: %w", err)
	}

	// Store encrypted PSK to filesystem (imported into DB on colony start).
	if err := SavePSKToFile(caDir, psk, rootKey); err != nil {
		return nil, fmt.Errorf("failed to save bootstrap PSK: %w", err)
	}

	return &InitResult{
		CADir:           caDir,
		RootFingerprint: fingerprint,
		ColonySPIFFEID:  fmt.Sprintf("spiffe://coral/colony/%s", colonyID),
		RootCAPath:      filepath.Join(caDir, "root-ca.crt"),
		ServerIntPath:   filepath.Join(caDir, "server-intermediate.crt"),
		AgentIntPath:    filepath.Join(caDir, "agent-intermediate.crt"),
		PolicySignPath:  filepath.Join(caDir, "policy-signing.crt"),
		BootstrapPSK:    psk,
	}, nil
}

// ensurePSKTable creates the bootstrap_psks table if it does not exist.
func (m *Manager) ensurePSKTable() error {
	_, err := m.db.Exec(`CREATE TABLE IF NOT EXISTS bootstrap_psks (
		id TEXT PRIMARY KEY,
		encrypted_psk BLOB NOT NULL,
		encryption_nonce BLOB NOT NULL,
		status TEXT NOT NULL DEFAULT 'active',
		created_at TIMESTAMP NOT NULL,
		grace_expires_at TIMESTAMP,
		revoked_at TIMESTAMP
	)`)
	if err != nil {
		return fmt.Errorf("failed to create bootstrap_psks table: %w", err)
	}
	return nil
}

// BootstrapPSK represents a stored bootstrap PSK record (RFD 088).
type BootstrapPSK struct {
	ID              string     `duckdb:"id,pk,immutable"`
	EncryptedPSK    []byte     `duckdb:"encrypted_psk,immutable"`
	EncryptionNonce []byte     `duckdb:"encryption_nonce,immutable"`
	Status          string     `duckdb:"status"`
	CreatedAt       time.Time  `duckdb:"created_at,immutable"`
	GraceExpiresAt  *time.Time `duckdb:"grace_expires_at"`
	RevokedAt       *time.Time `duckdb:"revoked_at"`
}

// ImportPSKFromFile imports a PSK from the filesystem into the database.
// Called during colony startup if a PSK file exists but the DB has no active PSK.
func (m *Manager) ImportPSKFromFile(ctx context.Context) error {
	if !PSKFileExists(m.caDir) {
		return nil
	}

	// Check if DB already has an active PSK.
	var count int
	err := m.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM bootstrap_psks WHERE status IN ('active', 'grace')").Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to check existing PSKs: %w", err)
	}
	if count > 0 {
		return nil // Already imported.
	}

	rootKey, err := m.fsStorage.LoadKey("root-ca")
	if err != nil {
		return fmt.Errorf("failed to load root CA key: %w", err)
	}

	psk, err := LoadPSKFromFile(m.caDir, rootKey)
	if err != nil {
		return fmt.Errorf("failed to load PSK from file: %w", err)
	}

	return m.StorePSK(ctx, psk)
}

// StorePSK encrypts and stores a new active PSK in the database.
func (m *Manager) StorePSK(ctx context.Context, psk string) error {
	rootKey, err := m.fsStorage.LoadKey("root-ca")
	if err != nil {
		return fmt.Errorf("failed to load root CA key: %w", err)
	}

	encKey, err := DeriveEncryptionKey(rootKey)
	if err != nil {
		return fmt.Errorf("failed to derive encryption key: %w", err)
	}

	ciphertext, nonce, err := EncryptPSK(psk, encKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt PSK: %w", err)
	}

	id := fmt.Sprintf("%d", time.Now().UnixNano())
	_, err = m.db.ExecContext(ctx,
		`INSERT INTO bootstrap_psks (id, encrypted_psk, encryption_nonce, status, created_at)
		 VALUES (?, ?, ?, 'active', ?)`,
		id, ciphertext, nonce, time.Now())
	if err != nil {
		return fmt.Errorf("failed to store PSK: %w", err)
	}

	return nil
}

// ValidateBootstrapPSK checks a PSK against active and grace-period PSKs.
// Returns nil if valid, error if invalid.
func (m *Manager) ValidateBootstrapPSK(ctx context.Context, psk string) error {
	// Lazy cleanup of expired grace PSKs.
	_, _ = m.db.ExecContext(ctx,
		`UPDATE bootstrap_psks SET status = 'revoked', revoked_at = ?
		 WHERE status = 'grace' AND grace_expires_at IS NOT NULL AND grace_expires_at < ?`,
		time.Now(), time.Now())

	rows, err := m.db.QueryContext(ctx,
		`SELECT encrypted_psk, encryption_nonce FROM bootstrap_psks
		 WHERE status IN ('active', 'grace')
		 AND (grace_expires_at IS NULL OR grace_expires_at > ?)`,
		time.Now())
	if err != nil {
		return fmt.Errorf("failed to query PSKs: %w", err)
	}
	defer safe.Close(rows, m.logger, "failed to close rows")

	rootKey, err := m.fsStorage.LoadKey("root-ca")
	if err != nil {
		return fmt.Errorf("failed to load root CA key: %w", err)
	}

	encKey, err := DeriveEncryptionKey(rootKey)
	if err != nil {
		return fmt.Errorf("failed to derive encryption key: %w", err)
	}

	found := false
	for rows.Next() {
		var ciphertext, nonce []byte
		if err := rows.Scan(&ciphertext, &nonce); err != nil {
			return fmt.Errorf("failed to scan PSK row: %w", err)
		}

		stored, err := DecryptPSK(ciphertext, nonce, encKey)
		if err != nil {
			continue // Skip corrupted entries.
		}

		if PSKConstantTimeEqual(stored, psk) {
			found = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating PSK rows: %w", err)
	}

	if !found {
		return fmt.Errorf("invalid bootstrap PSK")
	}
	return nil
}

// RotatePSK generates a new PSK and moves the current active PSK to grace status.
func (m *Manager) RotatePSK(ctx context.Context, gracePeriod time.Duration) (string, error) {
	newPSK, err := GeneratePSK()
	if err != nil {
		return "", fmt.Errorf("failed to generate new PSK: %w", err)
	}

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // Rollback is best-effort.

	graceExpiry := time.Now().Add(gracePeriod)

	// Move current active to grace.
	_, err = tx.ExecContext(ctx,
		`UPDATE bootstrap_psks SET status = 'grace', grace_expires_at = ?
		 WHERE status = 'active'`,
		graceExpiry)
	if err != nil {
		return "", fmt.Errorf("failed to update existing PSK: %w", err)
	}

	// Insert new active PSK.
	rootKey, err := m.fsStorage.LoadKey("root-ca")
	if err != nil {
		return "", fmt.Errorf("failed to load root CA key: %w", err)
	}

	encKey, err := DeriveEncryptionKey(rootKey)
	if err != nil {
		return "", fmt.Errorf("failed to derive encryption key: %w", err)
	}

	ciphertext, nonce, err := EncryptPSK(newPSK, encKey)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt new PSK: %w", err)
	}

	id := fmt.Sprintf("%d", time.Now().UnixNano())
	_, err = tx.ExecContext(ctx,
		`INSERT INTO bootstrap_psks (id, encrypted_psk, encryption_nonce, status, created_at)
		 VALUES (?, ?, ?, 'active', ?)`,
		id, ciphertext, nonce, time.Now())
	if err != nil {
		return "", fmt.Errorf("failed to insert new PSK: %w", err)
	}

	// Update the filesystem PSK file.
	if err := SavePSKToFile(m.caDir, newPSK, rootKey); err != nil {
		return "", fmt.Errorf("failed to update PSK file: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("failed to commit PSK rotation: %w", err)
	}

	return newPSK, nil
}

// GetActivePSK returns the current active PSK (decrypted).
func (m *Manager) GetActivePSK(ctx context.Context) (string, error) {
	var ciphertext, nonce []byte
	err := m.db.QueryRowContext(ctx,
		`SELECT encrypted_psk, encryption_nonce FROM bootstrap_psks
		 WHERE status = 'active' LIMIT 1`).Scan(&ciphertext, &nonce)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("no active PSK found")
		}
		return "", fmt.Errorf("failed to query active PSK: %w", err)
	}

	rootKey, err := m.fsStorage.LoadKey("root-ca")
	if err != nil {
		return "", fmt.Errorf("failed to load root CA key: %w", err)
	}

	encKey, err := DeriveEncryptionKey(rootKey)
	if err != nil {
		return "", fmt.Errorf("failed to derive encryption key: %w", err)
	}

	return DecryptPSK(ciphertext, nonce, encKey)
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
