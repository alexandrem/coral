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
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/constants"
	discoveryclient "github.com/coral-mesh/coral/internal/discovery/client"
	"github.com/coral-mesh/coral/internal/retry"
	"github.com/coral-mesh/coral/internal/safe"
)

type Config struct {
	AgentID           string
	ColonyID          string
	CAFingerprint     string
	DiscoveryEndpoint string

	// ColonyEndpoint is an optional direct URL to the colony.
	// If provided, discovery lookup is bypassed.
	ColonyEndpoint string

	// BootstrapPSK is the pre-shared key for bootstrap authorization (RFD 088).
	BootstrapPSK string

	ReefID string
	Logger zerolog.Logger
}

type Result struct {
	Certificate   []byte
	PrivateKey    []byte
	CAChain       []byte
	RootCA        []byte
	ExpiresAt     time.Time
	AgentSPIFFEID string
}

type Client struct {
	cfg       Config
	validator *CAValidator
	logger    zerolog.Logger
}

func NewClient(cfg Config) *Client {
	if cfg.ReefID == "" {
		cfg.ReefID = "default"
	}
	return &Client{
		cfg:       cfg,
		validator: NewCAValidator(cfg.CAFingerprint, cfg.ColonyID),
		logger:    cfg.Logger,
	}
}

// --- High Level API ---

// Bootstrap performs the flow using a Discovery Token.
func (c *Client) Bootstrap(ctx context.Context) (*Result, error) {
	c.logger.Info().Msg("Starting certificate bootstrap")

	token, err := c.requestBootstrapToken(ctx)
	if err != nil {
		return nil, err
	}

	endpoint, err := c.findValidColonyEndpoint(ctx)
	if err != nil {
		return nil, err
	}

	// Use standard transport for bootstrap (validates server via fingerprint)
	httpClient := &http.Client{
		Transport: &http.Transport{TLSClientConfig: c.validator.GetTLSConfig()},
	}

	return c.executeFlow(ctx, httpClient, endpoint, token)
}

// Renew performs the flow using an existing mTLS certificate.
func (c *Client) Renew(ctx context.Context, certPath, keyPath, rootCAPath string) (*Result, error) {
	c.logger.Info().Msg("Starting certificate renewal")

	tlsCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load keypair: %w", err)
	}

	rootCAPEM, err := safe.ReadFile(rootCAPath, nil)
	if err != nil {
		return nil, fmt.Errorf("read root CA: %w", err)
	}

	rootPool := x509.NewCertPool()
	rootPool.AppendCertsFromPEM(rootCAPEM)

	// Build mTLS client
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{tlsCert},
				RootCAs:      rootPool,
				MinVersion:   tls.VersionTLS12,
			},
		},
	}

	// For renewal, we assume the endpoint is known or found via discovery
	endpoint, err := c.findValidColonyEndpoint(ctx)
	if err != nil {
		return nil, err
	}

	return c.executeFlow(ctx, httpClient, endpoint, "")
}

// --- Private Implementation Helpers ---

// requestBootstrapToken requests a bootstrap token from Discovery.
func (c *Client) requestBootstrapToken(ctx context.Context) (string, error) {
	client := discoveryclient.New(c.cfg.DiscoveryEndpoint)

	req := &discoveryclient.CreateBootstrapTokenRequest{
		ReefID:   c.cfg.ReefID,
		ColonyID: c.cfg.ColonyID,
		AgentID:  c.cfg.AgentID,
		Intent:   "register",
	}

	resp, err := client.CreateBootstrapToken(ctx, req)
	if err != nil {
		return "", fmt.Errorf("discovery token request failed: %w", err)
	}

	return resp.JWT, nil
}

func (c *Client) executeFlow(ctx context.Context, httpClient *http.Client, colonyURL, token string) (*Result, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}

	csr, err := c.createCSR(pub, priv)
	if err != nil {
		return nil, err
	}

	c.logger.Info().
		Str("token", token).
		Str("colonyEndpoint", colonyURL).
		Bool("psk_provided", c.cfg.BootstrapPSK != "").
		Msg("Requesting certificate bootstrap")
	client := colonyv1connect.NewColonyServiceClient(httpClient, colonyURL)
	resp, err := client.RequestCertificate(ctx, connect.NewRequest(&colonyv1.RequestCertificateRequest{
		Jwt:          token,
		Csr:          csr,
		BootstrapPsk: c.cfg.BootstrapPSK,
	}))
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return c.parseAndVerifyResult(resp.Msg, pub, priv)
}

// findValidColonyEndpoint infers colony endpoint from config override
// or fallbacks to the discovery endpoint lookup, then validates the certificate.
func (c *Client) findValidColonyEndpoint(ctx context.Context) (string, error) {
	// Priority 1: Manual Override
	if c.cfg.ColonyEndpoint != "" {
		c.logger.Debug().Str("endpoint", c.cfg.ColonyEndpoint).Msg("Using manual colony endpoint override")
		// We still validate the manual endpoint to ensure the fingerprint matches
		_, tlsConn, err := c.connectAndValidate(ctx, c.cfg.ColonyEndpoint)
		if err != nil {
			return "", fmt.Errorf("manual colony endpoint failed validation: %w", err)
		}

		if err := tlsConn.Close(); err != nil {
			c.logger.Warn().
				Err(err).
				Msg("failed to close tls connection during colony endpoint validation")
		}
		return c.cfg.ColonyEndpoint, nil
	}

	// Priority 2: Automatic Discovery
	c.logger.Debug().Msg("No colony endpoint provided, performing discovery lookup")

	// ... existing lookupColony and retry logic ...
	// This will return the validated endpoint from the discovery list
	return c.lookupAndValidateFromDiscovery(ctx)
}

// lookupAndValidateFromDiscovery lookups endpoints and validates the TLS handshake/fingerprint.
func (c *Client) lookupAndValidateFromDiscovery(ctx context.Context) (string, error) {
	var colonyInfo *discoveryclient.LookupColonyResponse

	retryCfg := retry.Config{
		MaxRetries:     30,
		InitialBackoff: 2 * time.Second,
		MaxBackoff:     10 * time.Second,
		Jitter:         0.1,
	}

	c.logger.Info().
		Str("endpoint", c.cfg.DiscoveryEndpoint).
		Str("colony_id", c.cfg.ColonyID).
		Msg("Performing discovery lookup for bootstrap")

	// 1. Lookup with Retry
	err := retry.Do(ctx, retryCfg, func() error {
		client := discoveryclient.New(c.cfg.DiscoveryEndpoint)
		resp, err := client.LookupColony(ctx, c.cfg.ColonyID)
		if err != nil {
			return err
		}
		if len(resp.Endpoints) == 0 {
			return fmt.Errorf("no colony endpoints found")
		}
		colonyInfo = resp
		return nil
	}, func(err error) bool {
		// Retry if not found (colony still spinning up)
		return strings.Contains(strings.ToLower(err.Error()), "not found")
	})

	if err != nil {
		return "", fmt.Errorf("colony lookup failed: %w", err)
	}

	// 2. Validate Fingerprints across endpoints
	// Use public endpoint port for TLS bootstrap, not ConnectPort which is HTTP-only mesh port.
	publicPort := colonyInfo.PublicPort
	if publicPort == 0 {
		publicPort = uint32(constants.DefaultPublicEndpointPort) // Fallback to default 8443.
	}

	for _, endpoint := range colonyInfo.Endpoints {
		// Extract host from WireGuard endpoint (host:port) and use public TLS port for bootstrap.
		host := endpoint
		if idx := strings.LastIndex(endpoint, ":"); idx != -1 {
			host = endpoint[:idx]
		}
		colonyURL := fmt.Sprintf("https://%s:%d", host, publicPort)

		_, tlsConn, err := c.connectAndValidate(ctx, colonyURL)
		if err != nil {
			c.logger.Debug().Err(err).Str("endpoint", colonyURL).Msg("Skipping unreachable/invalid endpoint")
			continue
		}
		if tlsConn != nil {
			_ = tlsConn.Close()
		}

		c.logger.Info().Str("endpoint", colonyURL).Msg("Validated colony trust via fingerprint")
		// Return the full URL for the client to use.
		return colonyURL, nil
	}

	return "", fmt.Errorf("none of the discovered endpoints passed fingerprint validation")
}

// connectAndValidate performs the low-level TLS dial to check the CA fingerprint.
func (c *Client) connectAndValidate(
	ctx context.Context,
	colonyURL string,
) (*ValidationResult, *tls.Conn, error) {
	u, err := url.Parse(colonyURL)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid URL: %w", err)
	}

	dialer := &tls.Dialer{Config: c.validator.GetTLSConfig()}
	conn, err := dialer.DialContext(ctx, "tcp", u.Host)
	if err != nil {
		return nil, nil, err
	}

	tlsConn := conn.(*tls.Conn)
	state := tlsConn.ConnectionState()
	res, err := c.validator.ValidateConnectionState(&state)
	if err != nil {
		_ = tlsConn.Close()
		return nil, nil, err
	}
	return res, tlsConn, nil
}

func (c *Client) createCSR(pub ed25519.PublicKey, priv ed25519.PrivateKey) ([]byte, error) {
	spiffeID, _ := BuildAgentSPIFFEID(c.cfg.ColonyID, c.cfg.AgentID)
	template := &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   fmt.Sprintf("agent.%s.%s", c.cfg.AgentID, c.cfg.ColonyID),
			Organization: []string{c.cfg.ColonyID},
		},
		URIs: []*url.URL{spiffeID},
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, template, priv)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), nil
}

func (c *Client) parseAndVerifyResult(res *colonyv1.RequestCertificateResponse, pub ed25519.PublicKey, priv ed25519.PrivateKey) (*Result, error) {
	// 1. Decode PEM
	block, _ := pem.Decode(res.Certificate)
	if block == nil {
		return nil, fmt.Errorf("invalid cert pem")
	}

	// 2. Parse and Verify Logic (shared)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}

	// Ensure the cert matches the key we just generated
	if certPub, ok := cert.PublicKey.(ed25519.PublicKey); !ok || !certPub.Equal(pub) {
		return nil, fmt.Errorf("certificate public key mismatch")
	}

	privBytes, _ := x509.MarshalPKCS8PrivateKey(priv)

	// Extract Root CA from chain (last cert in chain).
	// The colony sends [Intermediate, Root].
	var certBlocks []*pem.Block
	rest := res.CaChain
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			certBlocks = append(certBlocks, block)
		}
	}

	if len(certBlocks) == 0 {
		return nil, fmt.Errorf("no certificates found in chain")
	}

	// The last cert in the chain is the Root CA.
	lastBlock := certBlocks[len(certBlocks)-1]

	// Re-encode to ensure clean PEM.
	rootCA := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: lastBlock.Bytes,
	})

	if len(rootCA) == 0 {
		return nil, fmt.Errorf("failed to encode root CA")
	}

	return &Result{
		Certificate:   res.Certificate,
		PrivateKey:    pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}),
		CAChain:       res.CaChain,
		RootCA:        rootCA,
		ExpiresAt:     time.Unix(res.ExpiresAt, 0),
		AgentSPIFFEID: cert.URIs[0].String(),
	}, nil
}
