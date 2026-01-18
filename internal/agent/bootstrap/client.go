// Package bootstrap implements agent certificate bootstrap for mTLS.
// This implements RFD 048 - Agent Certificate Bootstrap.
package bootstrap

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	discoverypb "github.com/coral-mesh/coral/coral/discovery/v1"
	"github.com/coral-mesh/coral/coral/discovery/v1/discoveryv1connect"
)

// Config contains configuration for the bootstrap client.
type Config struct {
	// AgentID is the unique identifier for this agent.
	AgentID string

	// ColonyID is the colony to bootstrap with.
	ColonyID string

	// CAFingerprint is the expected Root CA fingerprint (sha256:hex or just hex).
	CAFingerprint string

	// DiscoveryEndpoint is the Discovery service URL.
	DiscoveryEndpoint string

	// ReefID is the reef identifier (optional, defaults to "default").
	ReefID string

	// Logger for logging bootstrap events.
	Logger zerolog.Logger
}

// Result contains the result of a successful bootstrap.
type Result struct {
	// Certificate is the issued agent certificate (PEM).
	Certificate []byte

	// PrivateKey is the agent's private key (PEM).
	PrivateKey []byte

	// CAChain is the CA certificate chain (PEM).
	CAChain []byte

	// RootCA is the validated Root CA certificate (PEM).
	RootCA []byte

	// ExpiresAt is when the certificate expires.
	ExpiresAt time.Time

	// AgentSPIFFEID is the agent's SPIFFE ID.
	AgentSPIFFEID string
}

// Client handles the agent certificate bootstrap flow.
type Client struct {
	config    Config
	validator *CAValidator
	logger    zerolog.Logger
}

// NewClient creates a new bootstrap client.
func NewClient(cfg Config) *Client {
	if cfg.ReefID == "" {
		cfg.ReefID = "default"
	}

	return &Client{
		config:    cfg,
		validator: NewCAValidator(cfg.CAFingerprint, cfg.ColonyID),
		logger:    cfg.Logger,
	}
}

// Bootstrap performs the full certificate bootstrap flow:
// 1. Request bootstrap token from Discovery
// 2. Lookup colony endpoints from Discovery
// 3. Connect to colony and validate Root CA fingerprint
// 4. Generate keypair and CSR
// 5. Request certificate from colony
// 6. Validate and return result.
func (c *Client) Bootstrap(ctx context.Context) (*Result, error) {
	c.logger.Info().
		Str("agent_id", c.config.AgentID).
		Str("colony_id", c.config.ColonyID).
		Msg("Starting certificate bootstrap")

	// Step 1: Get bootstrap token from Discovery.
	c.logger.Debug().Msg("Requesting bootstrap token from Discovery")
	token, err := c.requestBootstrapToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get bootstrap token: %w", err)
	}
	c.logger.Debug().Msg("Bootstrap token received")

	// Step 2: Lookup colony endpoints from Discovery.
	c.logger.Debug().Msg("Looking up colony endpoints")
	colonyInfo, err := c.lookupColony(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup colony: %w", err)
	}

	if len(colonyInfo.Endpoints) == 0 {
		return nil, fmt.Errorf("no colony endpoints returned from Discovery")
	}

	c.logger.Debug().
		Strs("endpoints", colonyInfo.Endpoints).
		Msg("Colony endpoints found")

	// Step 3: Connect to colony and validate Root CA fingerprint.
	// Try each endpoint until one succeeds.
	var validationResult *ValidationResult
	var colonyEndpoint string
	var tlsConn *tls.Conn

	for _, endpoint := range colonyInfo.Endpoints {
		c.logger.Debug().Str("endpoint", endpoint).Msg("Attempting connection to colony")

		// Build colony URL (assuming HTTPS on connect port).
		colonyURL := fmt.Sprintf("https://%s:%d", endpoint, colonyInfo.ConnectPort)

		validationResult, tlsConn, err = c.connectAndValidate(ctx, colonyURL)
		if err != nil {
			c.logger.Warn().
				Err(err).
				Str("endpoint", endpoint).
				Msg("Failed to connect to colony endpoint, trying next")
			continue
		}

		colonyEndpoint = colonyURL
		if tlsConn != nil {
			_ = tlsConn.Close() // Connection validated, no longer needed.
		}
		break
	}

	if validationResult == nil {
		return nil, fmt.Errorf("failed to connect to any colony endpoint")
	}

	c.logger.Info().
		Str("fingerprint", validationResult.ComputedFingerprint).
		Str("colony_spiffe_id", validationResult.ServerSPIFFEID).
		Msg("Root CA fingerprint validated - trust established")

	// Step 4: Generate Ed25519 keypair.
	c.logger.Debug().Msg("Generating Ed25519 keypair")
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate keypair: %w", err)
	}

	// Step 5: Create CSR with SPIFFE ID in SAN.
	c.logger.Debug().Msg("Creating certificate signing request")
	csrPEM, err := c.createCSR(publicKey, privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create CSR: %w", err)
	}

	// Step 6: Request certificate from colony.
	c.logger.Debug().Msg("Requesting certificate from Colony")
	certResult, err := c.requestCertificate(ctx, colonyEndpoint, token, csrPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to request certificate: %w", err)
	}

	// Step 7: Validate received certificate.
	cert, err := c.validateReceivedCertificate(certResult.Certificate, publicKey)
	if err != nil {
		return nil, fmt.Errorf("certificate validation failed: %w", err)
	}

	// Marshal private key to PEM.
	privateKeyBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal private key: %w", err)
	}

	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privateKeyBytes,
	})

	// Encode Root CA to PEM.
	rootCAPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: validationResult.RootCA.Raw,
	})

	// Extract SPIFFE ID from certificate.
	spiffeID := ""
	if len(cert.URIs) > 0 {
		spiffeID = cert.URIs[0].String()
	}

	result := &Result{
		Certificate:   certResult.Certificate,
		PrivateKey:    privateKeyPEM,
		CAChain:       certResult.CaChain,
		RootCA:        rootCAPEM,
		ExpiresAt:     time.Unix(certResult.ExpiresAt, 0),
		AgentSPIFFEID: spiffeID,
	}

	c.logger.Info().
		Time("expires_at", result.ExpiresAt).
		Str("spiffe_id", spiffeID).
		Msg("Certificate bootstrap completed successfully")

	return result, nil
}

// requestBootstrapToken requests a bootstrap token from Discovery.
func (c *Client) requestBootstrapToken(ctx context.Context) (string, error) {
	client := discoveryv1connect.NewDiscoveryServiceClient(
		http.DefaultClient,
		c.config.DiscoveryEndpoint,
	)

	req := &discoverypb.CreateBootstrapTokenRequest{
		ReefId:   c.config.ReefID,
		ColonyId: c.config.ColonyID,
		AgentId:  c.config.AgentID,
		Intent:   "register",
	}

	resp, err := client.CreateBootstrapToken(ctx, connect.NewRequest(req))
	if err != nil {
		return "", err
	}

	return resp.Msg.Jwt, nil
}

// lookupColony looks up colony information from Discovery.
func (c *Client) lookupColony(ctx context.Context) (*discoverypb.LookupColonyResponse, error) {
	client := discoveryv1connect.NewDiscoveryServiceClient(
		http.DefaultClient,
		c.config.DiscoveryEndpoint,
	)

	req := &discoverypb.LookupColonyRequest{
		MeshId: c.config.ColonyID,
	}

	resp, err := client.LookupColony(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}

	return resp.Msg, nil
}

// connectAndValidate connects to the colony and validates the Root CA fingerprint.
func (c *Client) connectAndValidate(ctx context.Context, colonyURL string) (*ValidationResult, *tls.Conn, error) {
	// Get TLS config with manual verification.
	tlsConfig := c.validator.GetTLSConfig()

	// Parse URL to get host.
	// URL format: https://host:port
	// We need to extract host:port for dial.
	dialer := &tls.Dialer{
		Config: tlsConfig,
	}

	// Extract host:port from URL.
	// Strip https:// prefix.
	hostPort := colonyURL[8:] // Remove "https://"

	conn, err := dialer.DialContext(ctx, "tcp", hostPort)
	if err != nil {
		return nil, nil, fmt.Errorf("TLS dial failed: %w", err)
	}

	tlsConn := conn.(*tls.Conn)

	// Validate the connection state.
	state := tlsConn.ConnectionState()
	result, err := c.validator.ValidateConnectionState(&state)
	if err != nil {
		_ = tlsConn.Close() // Close on validation error.
		return result, nil, err
	}

	return result, tlsConn, nil
}

// createCSR creates a Certificate Signing Request with the agent's identity.
func (c *Client) createCSR(publicKey ed25519.PublicKey, privateKey ed25519.PrivateKey) ([]byte, error) {
	// Build SPIFFE ID for the agent.
	spiffeID, err := BuildAgentSPIFFEID(c.config.ColonyID, c.config.AgentID)
	if err != nil {
		return nil, fmt.Errorf("failed to build SPIFFE ID: %w", err)
	}

	// Create CSR template.
	// CN format matches policy.go: agent.{agent_id}.{colony_id}
	template := &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   fmt.Sprintf("agent.%s.%s", c.config.AgentID, c.config.ColonyID),
			Organization: []string{c.config.ColonyID},
		},
		URIs: []*url.URL{spiffeID},
	}

	// Create CSR.
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, template, privateKey)
	if err != nil {
		return nil, err
	}

	csrPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: csrDER,
	})

	return csrPEM, nil
}

// requestCertificate requests a certificate from the colony.
func (c *Client) requestCertificate(ctx context.Context, colonyURL, token string, csrPEM []byte) (*colonyv1.RequestCertificateResponse, error) {
	// Create HTTP client with custom TLS config for fingerprint validation.
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: c.validator.GetTLSConfig(),
		},
	}

	client := colonyv1connect.NewColonyServiceClient(httpClient, colonyURL)

	req := &colonyv1.RequestCertificateRequest{
		Jwt: token,
		Csr: csrPEM,
	}

	resp, err := client.RequestCertificate(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}

	return resp.Msg, nil
}

// validateReceivedCertificate validates that the received certificate is valid.
func (c *Client) validateReceivedCertificate(certPEM []byte, publicKey ed25519.PublicKey) (*x509.Certificate, error) {
	// Parse the certificate.
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode certificate PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	// Verify the certificate's public key matches ours.
	certPubKey, ok := cert.PublicKey.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("certificate has unexpected key type: %T", cert.PublicKey)
	}

	if !certPubKey.Equal(publicKey) {
		return nil, fmt.Errorf("certificate public key doesn't match our keypair")
	}

	// Verify SPIFFE ID is present.
	expectedSPIFFE, _ := BuildAgentSPIFFEID(c.config.ColonyID, c.config.AgentID)
	found := false
	for _, uri := range cert.URIs {
		if uri.String() == expectedSPIFFE.String() {
			found = true
			break
		}
	}

	if !found {
		return nil, fmt.Errorf("certificate doesn't contain expected SPIFFE ID: %s", expectedSPIFFE)
	}

	// Verify certificate is for client authentication.
	hasClientAuth := false
	for _, usage := range cert.ExtKeyUsage {
		if usage == x509.ExtKeyUsageClientAuth {
			hasClientAuth = true
			break
		}
	}

	if !hasClientAuth {
		return nil, fmt.Errorf("certificate is not valid for client authentication")
	}

	return cert, nil
}

// RenewalConfig contains configuration for certificate renewal.
type RenewalConfig struct {
	// AgentID is the unique identifier for this agent.
	AgentID string

	// ColonyID is the colony to renew certificate with.
	ColonyID string

	// CAFingerprint is the expected Root CA fingerprint.
	CAFingerprint string

	// ColonyEndpoint is the colony HTTPS endpoint URL.
	ColonyEndpoint string

	// ExistingCertPath is the path to the existing client certificate.
	ExistingCertPath string

	// ExistingKeyPath is the path to the existing client private key.
	ExistingKeyPath string

	// RootCAPath is the path to the validated Root CA certificate.
	RootCAPath string

	// Logger for logging renewal events.
	Logger zerolog.Logger
}

// RenewalClient handles certificate renewal using existing mTLS credentials.
// This implements RFD 048's renewal flow that doesn't require Discovery.
type RenewalClient struct {
	config    RenewalConfig
	validator *CAValidator
	logger    zerolog.Logger
}

// NewRenewalClient creates a new certificate renewal client.
func NewRenewalClient(cfg RenewalConfig) *RenewalClient {
	return &RenewalClient{
		config:    cfg,
		validator: NewCAValidator(cfg.CAFingerprint, cfg.ColonyID),
		logger:    cfg.Logger,
	}
}

// Renew performs certificate renewal using existing mTLS certificate.
// This flow doesn't require Discovery - the agent authenticates with its
// existing certificate and requests a new one directly from Colony.
func (c *RenewalClient) Renew(ctx context.Context) (*Result, error) {
	c.logger.Info().
		Str("agent_id", c.config.AgentID).
		Str("colony_id", c.config.ColonyID).
		Msg("Starting certificate renewal using mTLS")

	// Step 1: Load existing mTLS credentials.
	c.logger.Debug().Msg("Loading existing mTLS credentials")
	tlsCert, err := tls.LoadX509KeyPair(c.config.ExistingCertPath, c.config.ExistingKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load existing certificate: %w", err)
	}

	// Load Root CA for server validation.
	//nolint:gosec // G304: Path from trusted config.
	rootCAPEM, err := os.ReadFile(c.config.RootCAPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load root CA: %w", err)
	}

	rootCAPool := x509.NewCertPool()
	if !rootCAPool.AppendCertsFromPEM(rootCAPEM) {
		return nil, fmt.Errorf("failed to parse root CA")
	}

	// Parse existing certificate to get the Root CA for result.
	block, _ := pem.Decode(rootCAPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode root CA PEM")
	}
	rootCACert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse root CA: %w", err)
	}

	// Step 2: Generate new Ed25519 keypair.
	c.logger.Debug().Msg("Generating new Ed25519 keypair")
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate keypair: %w", err)
	}

	// Step 3: Create CSR with SPIFFE ID in SAN.
	c.logger.Debug().Msg("Creating certificate signing request")
	csrPEM, err := c.createCSR(publicKey, privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create CSR: %w", err)
	}

	// Step 4: Request new certificate from Colony using mTLS.
	c.logger.Debug().Msg("Requesting new certificate from Colony using mTLS")
	certResult, err := c.requestCertificateWithMTLS(ctx, tlsCert, rootCAPool, csrPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to request certificate: %w", err)
	}

	// Step 5: Validate received certificate.
	cert, err := c.validateReceivedCertificate(certResult.Certificate, publicKey)
	if err != nil {
		return nil, fmt.Errorf("certificate validation failed: %w", err)
	}

	// Marshal private key to PEM.
	privateKeyBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal private key: %w", err)
	}

	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privateKeyBytes,
	})

	// Encode Root CA to PEM.
	rootCAPEMResult := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: rootCACert.Raw,
	})

	// Extract SPIFFE ID from certificate.
	spiffeID := ""
	if len(cert.URIs) > 0 {
		spiffeID = cert.URIs[0].String()
	}

	result := &Result{
		Certificate:   certResult.Certificate,
		PrivateKey:    privateKeyPEM,
		CAChain:       certResult.CaChain,
		RootCA:        rootCAPEMResult,
		ExpiresAt:     time.Unix(certResult.ExpiresAt, 0),
		AgentSPIFFEID: spiffeID,
	}

	c.logger.Info().
		Time("expires_at", result.ExpiresAt).
		Str("spiffe_id", spiffeID).
		Msg("Certificate renewal completed successfully")

	return result, nil
}

// createCSR creates a Certificate Signing Request with the agent's identity.
func (c *RenewalClient) createCSR(publicKey ed25519.PublicKey, privateKey ed25519.PrivateKey) ([]byte, error) {
	// Build SPIFFE ID for the agent.
	spiffeID, err := BuildAgentSPIFFEID(c.config.ColonyID, c.config.AgentID)
	if err != nil {
		return nil, fmt.Errorf("failed to build SPIFFE ID: %w", err)
	}

	// Create CSR template.
	template := &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   fmt.Sprintf("agent.%s.%s", c.config.AgentID, c.config.ColonyID),
			Organization: []string{c.config.ColonyID},
		},
		URIs: []*url.URL{spiffeID},
	}

	// Create CSR.
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, template, privateKey)
	if err != nil {
		return nil, err
	}

	csrPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: csrDER,
	})

	return csrPEM, nil
}

// requestCertificateWithMTLS requests a certificate from the colony using mTLS.
func (c *RenewalClient) requestCertificateWithMTLS(ctx context.Context, tlsCert tls.Certificate, rootCAs *x509.CertPool, csrPEM []byte) (*colonyv1.RequestCertificateResponse, error) {
	// Create HTTP client with mTLS configuration.
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		RootCAs:      rootCAs,
		MinVersion:   tls.VersionTLS12,
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}

	client := colonyv1connect.NewColonyServiceClient(httpClient, c.config.ColonyEndpoint)

	// For renewal, JWT field is empty (mTLS authentication is used instead).
	req := &colonyv1.RequestCertificateRequest{
		Jwt: "", // No JWT needed for renewal - mTLS auth.
		Csr: csrPEM,
	}

	resp, err := client.RequestCertificate(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}

	return resp.Msg, nil
}

// validateReceivedCertificate validates that the received certificate is valid.
func (c *RenewalClient) validateReceivedCertificate(certPEM []byte, publicKey ed25519.PublicKey) (*x509.Certificate, error) {
	// Parse the certificate.
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode certificate PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	// Verify the certificate's public key matches ours.
	certPubKey, ok := cert.PublicKey.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("certificate has unexpected key type: %T", cert.PublicKey)
	}

	if !certPubKey.Equal(publicKey) {
		return nil, fmt.Errorf("certificate public key doesn't match our keypair")
	}

	// Verify SPIFFE ID is present.
	expectedSPIFFE, _ := BuildAgentSPIFFEID(c.config.ColonyID, c.config.AgentID)
	found := false
	for _, uri := range cert.URIs {
		if uri.String() == expectedSPIFFE.String() {
			found = true
			break
		}
	}

	if !found {
		return nil, fmt.Errorf("certificate doesn't contain expected SPIFFE ID: %s", expectedSPIFFE)
	}

	// Verify certificate is for client authentication.
	hasClientAuth := false
	for _, usage := range cert.ExtKeyUsage {
		if usage == x509.ExtKeyUsageClientAuth {
			hasClientAuth = true
			break
		}
	}

	if !hasClientAuth {
		return nil, fmt.Errorf("certificate is not valid for client authentication")
	}

	return cert, nil
}
