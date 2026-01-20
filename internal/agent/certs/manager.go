// Package certs manages agent certificate lifecycle.
// This implements RFD 048 - Agent Certificate Bootstrap.
package certs

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/internal/agent/bootstrap"
	"github.com/coral-mesh/coral/internal/privilege"
)

const (
	// CertFileName is the name of the agent certificate file.
	CertFileName = "agent.crt"

	// KeyFileName is the name of the agent private key file.
	KeyFileName = "agent.key"

	// RootCAFileName is the name of the validated Root CA file.
	RootCAFileName = "root-ca.crt"

	// CAChainFileName is the name of the CA chain file.
	CAChainFileName = "ca-chain.crt"

	// AgentIDFileName stores the agent ID for persistence.
	AgentIDFileName = "agent-id"

	// RenewalThreshold is when to start renewing (30 days before expiry).
	RenewalThreshold = 30 * 24 * time.Hour

	// GraceThreshold is when to show warnings (7 days before expiry).
	GraceThreshold = 7 * 24 * time.Hour
)

// Manager handles agent certificate lifecycle management.
type Manager struct {
	certsDir string
	logger   zerolog.Logger

	mu          sync.RWMutex
	certificate *tls.Certificate
	rootCAPool  *x509.CertPool
	certInfo    *CertificateInfo
}

// CertificateInfo contains metadata about the agent's certificate.
type CertificateInfo struct {
	AgentID       string
	ColonyID      string
	SPIFFEID      string
	SerialNumber  string
	NotBefore     time.Time
	NotAfter      time.Time
	Issuer        string
	DaysRemaining int
	Status        CertStatus
}

// CertStatus represents the certificate status.
type CertStatus string

const (
	// CertStatusValid indicates the certificate is valid and not near expiry.
	CertStatusValid CertStatus = "valid"

	// CertStatusRenewalNeeded indicates the certificate needs renewal.
	CertStatusRenewalNeeded CertStatus = "renewal_needed"

	// CertStatusExpiringSoon indicates the certificate is expiring soon (grace period).
	CertStatusExpiringSoon CertStatus = "expiring_soon"

	// CertStatusExpired indicates the certificate has expired.
	CertStatusExpired CertStatus = "expired"

	// CertStatusMissing indicates no certificate exists.
	CertStatusMissing CertStatus = "missing"
)

// Config contains configuration for the certificate manager.
type Config struct {
	// CertsDir is the directory for storing certificates.
	// Default: ~/.coral/certs/
	CertsDir string

	// Logger for logging certificate operations.
	Logger zerolog.Logger
}

// NewManager creates a new certificate manager.
func NewManager(cfg Config) *Manager {
	if cfg.CertsDir == "" {
		homeDir, _ := os.UserHomeDir()
		cfg.CertsDir = filepath.Join(homeDir, ".coral", "certs")
	}

	return &Manager{
		certsDir: cfg.CertsDir,
		logger:   cfg.Logger,
	}
}

// CertificateExists checks if a valid certificate exists.
func (m *Manager) CertificateExists() bool {
	certPath := filepath.Join(m.certsDir, CertFileName)
	keyPath := filepath.Join(m.certsDir, KeyFileName)

	_, certErr := os.Stat(certPath)
	_, keyErr := os.Stat(keyPath)

	return certErr == nil && keyErr == nil
}

// Load loads the certificate and key from disk.
func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	certPath := filepath.Join(m.certsDir, CertFileName)
	keyPath := filepath.Join(m.certsDir, KeyFileName)
	rootCAPath := filepath.Join(m.certsDir, RootCAFileName)

	// Load certificate and key.
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return fmt.Errorf("failed to load certificate/key pair: %w", err)
	}

	// Parse the certificate for metadata.
	if len(cert.Certificate) == 0 {
		return fmt.Errorf("certificate has no data")
	}

	x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return fmt.Errorf("failed to parse certificate: %w", err)
	}

	// Load Root CA pool.
	// #nosec G304: Path is constructed from trusted config.
	rootCAPEM, err := os.ReadFile(rootCAPath)
	if err != nil {
		return fmt.Errorf("failed to read root CA: %w", err)
	}

	rootCAPool := x509.NewCertPool()
	if !rootCAPool.AppendCertsFromPEM(rootCAPEM) {
		return fmt.Errorf("failed to parse root CA")
	}

	// Extract certificate info.
	info := m.extractCertInfo(x509Cert)

	m.certificate = &cert
	m.rootCAPool = rootCAPool
	m.certInfo = info

	m.logger.Info().
		Str("agent_id", info.AgentID).
		Str("spiffe_id", info.SPIFFEID).
		Int("days_remaining", info.DaysRemaining).
		Str("status", string(info.Status)).
		Msg("Certificate loaded")

	return nil
}

// Save saves the bootstrap result to disk.
func (m *Manager) Save(result *bootstrap.Result) error {
	// Ensure directory exists with secure permissions.
	if err := os.MkdirAll(m.certsDir, 0700); err != nil {
		return fmt.Errorf("failed to create certs directory: %w", err)
	}

	// Save certificate (readable by user for inspection).
	certPath := filepath.Join(m.certsDir, CertFileName)
	// #nosec G306: Certificate file should be readable (not secret).
	if err := os.WriteFile(certPath, result.Certificate, 0644); err != nil {
		return fmt.Errorf("failed to write certificate: %w", err)
	}

	// Save private key (restricted).
	keyPath := filepath.Join(m.certsDir, KeyFileName)
	if err := os.WriteFile(keyPath, result.PrivateKey, 0600); err != nil {
		return fmt.Errorf("failed to write private key: %w", err)
	}

	// Save Root CA (readable for verification).
	rootCAPath := filepath.Join(m.certsDir, RootCAFileName)
	// #nosec G306: CA certificate should be readable (public trust anchor).
	if err := os.WriteFile(rootCAPath, result.RootCA, 0644); err != nil {
		return fmt.Errorf("failed to write root CA: %w", err)
	}

	// Save CA chain (readable for verification).
	chainPath := filepath.Join(m.certsDir, CAChainFileName)
	// #nosec G306: CA chain should be readable (public certificates).
	if err := os.WriteFile(chainPath, result.CAChain, 0644); err != nil {
		return fmt.Errorf("failed to write CA chain: %w", err)
	}

	// Fix ownership if running as root.
	if err := privilege.FixFileOwnership(m.certsDir); err != nil {
		m.logger.Warn().Err(err).Msg("Failed to fix certificate directory ownership")
	}

	m.logger.Info().
		Str("cert_path", certPath).
		Str("key_path", keyPath).
		Str("root_ca_path", rootCAPath).
		Msg("Certificates saved successfully")

	return nil
}

// SaveAgentID persists the agent ID for future reference.
func (m *Manager) SaveAgentID(agentID string) error {
	// Ensure directory exists.
	if err := os.MkdirAll(m.certsDir, 0700); err != nil {
		return fmt.Errorf("failed to create certs directory: %w", err)
	}

	agentIDPath := filepath.Join(m.certsDir, AgentIDFileName)
	// #nosec G306: Agent ID is not secret, readable for inspection.
	if err := os.WriteFile(agentIDPath, []byte(agentID), 0644); err != nil {
		return fmt.Errorf("failed to write agent ID: %w", err)
	}

	return nil
}

// LoadAgentID loads the persisted agent ID.
func (m *Manager) LoadAgentID() (string, error) {
	agentIDPath := filepath.Join(m.certsDir, AgentIDFileName)
	// #nosec G304: Path is constructed from trusted config.
	data, err := os.ReadFile(agentIDPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// GetTLSConfig returns a TLS config for mTLS client connections.
func (m *Manager) GetTLSConfig() (*tls.Config, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.certificate == nil {
		return nil, fmt.Errorf("no certificate loaded")
	}

	return &tls.Config{
		Certificates: []tls.Certificate{*m.certificate},
		RootCAs:      m.rootCAPool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// GetCertificateInfo returns information about the loaded certificate.
func (m *Manager) GetCertificateInfo() *CertificateInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.certInfo
}

// GetStatus returns the current certificate status.
func (m *Manager) GetStatus() CertStatus {
	if !m.CertificateExists() {
		return CertStatusMissing
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.certInfo == nil {
		return CertStatusMissing
	}

	return m.certInfo.Status
}

// NeedsRenewal checks if the certificate needs renewal.
func (m *Manager) NeedsRenewal() bool {
	status := m.GetStatus()
	return status == CertStatusRenewalNeeded || status == CertStatusExpiringSoon || status == CertStatusExpired
}

// NeedsBootstrap checks if the agent needs to bootstrap (no valid certificate).
func (m *Manager) NeedsBootstrap() bool {
	if !m.CertificateExists() {
		return true
	}

	// Try to load and check if it's valid.
	if err := m.Load(); err != nil {
		return true
	}

	return m.GetStatus() == CertStatusExpired
}

// extractCertInfo extracts metadata from an X.509 certificate.
func (m *Manager) extractCertInfo(cert *x509.Certificate) *CertificateInfo {
	info := &CertificateInfo{
		SerialNumber: cert.SerialNumber.String(),
		NotBefore:    cert.NotBefore,
		NotAfter:     cert.NotAfter,
		Issuer:       cert.Issuer.CommonName,
	}

	// Parse CN: agent.{agent_id}.{colony_id}
	cn := cert.Subject.CommonName
	if len(cn) > 6 && cn[:6] == "agent." {
		parts := cn[6:] // Remove "agent." prefix
		// Find the first dot to separate agent_id from colony_id
		for i, ch := range parts {
			if ch == '.' {
				info.AgentID = parts[:i]
				info.ColonyID = parts[i+1:]
				break
			}
		}
	}

	// Extract SPIFFE ID from SAN.
	for _, uri := range cert.URIs {
		if uri.Scheme == "spiffe" {
			info.SPIFFEID = uri.String()
			break
		}
	}

	// Calculate status.
	now := time.Now()
	timeUntilExpiry := cert.NotAfter.Sub(now)
	info.DaysRemaining = int(timeUntilExpiry.Hours() / 24)

	switch {
	case now.After(cert.NotAfter):
		info.Status = CertStatusExpired
	case timeUntilExpiry <= GraceThreshold:
		info.Status = CertStatusExpiringSoon
	case timeUntilExpiry <= RenewalThreshold:
		info.Status = CertStatusRenewalNeeded
	default:
		info.Status = CertStatusValid
	}

	return info
}

// GetCertsDir returns the certificates directory path.
func (m *Manager) GetCertsDir() string {
	return m.certsDir
}

// GetCertPath returns the full path to the agent certificate.
func (m *Manager) GetCertPath() string {
	return filepath.Join(m.certsDir, CertFileName)
}

// GetKeyPath returns the full path to the agent private key.
func (m *Manager) GetKeyPath() string {
	return filepath.Join(m.certsDir, KeyFileName)
}

// GetRootCAPath returns the full path to the Root CA certificate.
func (m *Manager) GetRootCAPath() string {
	return filepath.Join(m.certsDir, RootCAFileName)
}

// GetCAChainPath returns the full path to the CA chain.
func (m *Manager) GetCAChainPath() string {
	return filepath.Join(m.certsDir, CAChainFileName)
}
