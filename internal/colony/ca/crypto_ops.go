// Package ca provides certificate authority management for mTLS.
package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net/url"
	"time"
)

// CryptoOperations handles pure cryptographic operations for certificate management.
type CryptoOperations struct {
	rootCert               *x509.Certificate
	serverIntermediateCert *x509.Certificate
	serverIntermediateKey  *ecdsa.PrivateKey
	agentIntermediateCert  *x509.Certificate
	agentIntermediateKey   *ecdsa.PrivateKey
	policySigningCert      *x509.Certificate
	policySigningKey       *ecdsa.PrivateKey
}

// NewCryptoOperations creates a new CryptoOperations instance.
func NewCryptoOperations(
	rootCert *x509.Certificate,
	serverIntCert *x509.Certificate,
	serverIntKey *ecdsa.PrivateKey,
	agentIntCert *x509.Certificate,
	agentIntKey *ecdsa.PrivateKey,
	policySignCert *x509.Certificate,
	policySignKey *ecdsa.PrivateKey,
) *CryptoOperations {
	return &CryptoOperations{
		rootCert:               rootCert,
		serverIntermediateCert: serverIntCert,
		serverIntermediateKey:  serverIntKey,
		agentIntermediateCert:  agentIntCert,
		agentIntermediateKey:   agentIntKey,
		policySigningCert:      policySignCert,
		policySigningKey:       policySignKey,
	}
}

// CertRequest contains parameters for certificate generation.
type CertRequest struct {
	AgentID  string
	ColonyID string
	CSR      *x509.CertificateRequest
	CertType string   // "agent" or "server"
	DNSNames []string // For server certificates
	Validity time.Duration
}

// GenerateAgentCertificate generates and signs an agent certificate.
func (c *CryptoOperations) GenerateAgentCertificate(req CertRequest) (*x509.Certificate, []byte, error) {
	// Verify CSR signature.
	if err := req.CSR.CheckSignature(); err != nil {
		return nil, nil, fmt.Errorf("invalid CSR signature: %w", err)
	}

	// Generate serial number.
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	// Build SPIFFE ID URI for the agent.
	spiffeID, err := url.Parse(fmt.Sprintf("spiffe://coral/colony/%s/agent/%s", req.ColonyID, req.AgentID))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse SPIFFE ID: %w", err)
	}

	// Create certificate with SPIFFE ID in SAN.
	notBefore := time.Now()
	notAfter := notBefore.Add(req.Validity)

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      req.CSR.Subject,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:         []*url.URL{spiffeID}, // SPIFFE ID in SAN.
	}

	// Sign with Agent Intermediate CA.
	certDER, err := x509.CreateCertificate(rand.Reader, template, c.agentIntermediateCert, req.CSR.PublicKey, c.agentIntermediateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	return cert, certDER, nil
}

// GenerateServerCertificate generates and signs a server certificate.
func (c *CryptoOperations) GenerateServerCertificate(colonyID string, dnsNames []string, validity time.Duration) (*x509.Certificate, *ecdsa.PrivateKey, []byte, []byte, error) {
	// Generate server key.
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to generate server key: %w", err)
	}

	// Generate serial number.
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	// Build SPIFFE ID URI for the colony.
	spiffeID, err := url.Parse(fmt.Sprintf("spiffe://coral/colony/%s", colonyID))
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to parse SPIFFE ID: %w", err)
	}

	// Create server certificate template.
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Coral"},
			CommonName:   fmt.Sprintf("colony.%s", colonyID),
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(validity),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    dnsNames,
		URIs:        []*url.URL{spiffeID}, // SPIFFE ID in SAN.
	}

	// Sign with Server Intermediate CA.
	certDER, err := x509.CreateCertificate(rand.Reader, template, c.serverIntermediateCert, &serverKey.PublicKey, c.serverIntermediateKey)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to create server certificate: %w", err)
	}

	// Marshal private key.
	keyBytes, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to marshal server key: %w", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	return cert, serverKey, certDER, keyBytes, nil
}

// GenerateIntermediateCertificate generates a new intermediate CA certificate.
func (c *CryptoOperations) GenerateIntermediateCertificate(colonyID, certType string, rootKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey, []byte, error) {
	var commonNamePrefix string

	switch certType {
	case "server":
		commonNamePrefix = "Coral Server Intermediate CA"
	case "agent":
		commonNamePrefix = "Coral Agent Intermediate CA"
	default:
		return nil, nil, nil, fmt.Errorf("invalid certificate type: %s", certType)
	}

	// Generate new key.
	newKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate new key: %w", err)
	}

	// Create new certificate template.
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Coral"},
			CommonName:   fmt.Sprintf("%s - %s", commonNamePrefix, colonyID),
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(1, 0, 0), // 1 year.
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}

	// Sign with Root CA.
	certDER, err := x509.CreateCertificate(rand.Reader, template, c.rootCert, &newKey.PublicKey, rootKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create new intermediate certificate: %w", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	return cert, newKey, certDER, nil
}

// GetAgentCertChain returns the agent certificate chain (Agent Intermediate -> Root).
func (c *CryptoOperations) GetAgentCertChain() []*x509.Certificate {
	return []*x509.Certificate{c.agentIntermediateCert, c.rootCert}
}

// GetServerCertChain returns the server certificate chain (Server Intermediate -> Root).
func (c *CryptoOperations) GetServerCertChain() []*x509.Certificate {
	return []*x509.Certificate{c.serverIntermediateCert, c.rootCert}
}

// GetRootCert returns the root certificate.
func (c *CryptoOperations) GetRootCert() *x509.Certificate {
	return c.rootCert
}

// GetServerIntermediateCert returns the server intermediate certificate.
func (c *CryptoOperations) GetServerIntermediateCert() *x509.Certificate {
	return c.serverIntermediateCert
}

// GetAgentIntermediateCert returns the agent intermediate certificate.
func (c *CryptoOperations) GetAgentIntermediateCert() *x509.Certificate {
	return c.agentIntermediateCert
}

// GetPolicySigningCert returns the policy signing certificate.
func (c *CryptoOperations) GetPolicySigningCert() *x509.Certificate {
	return c.policySigningCert
}

// UpdateServerIntermediate updates the server intermediate certificate and key.
func (c *CryptoOperations) UpdateServerIntermediate(cert *x509.Certificate, key *ecdsa.PrivateKey) {
	c.serverIntermediateCert = cert
	c.serverIntermediateKey = key
}

// UpdateAgentIntermediate updates the agent intermediate certificate and key.
func (c *CryptoOperations) UpdateAgentIntermediate(cert *x509.Certificate, key *ecdsa.PrivateKey) {
	c.agentIntermediateCert = cert
	c.agentIntermediateKey = key
}
