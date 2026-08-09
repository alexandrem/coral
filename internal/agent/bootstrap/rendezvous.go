package bootstrap

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/constants"
	"github.com/coral-mesh/coral/internal/discovery"
	"github.com/coral-mesh/coral/internal/rendezvous"
)

// ProbeFailedError indicates Discovery rejected a rendezvous publish because
// its opt-in reachability probe could not connect to the configured
// endpoint (RFD 108). Distinct from a generic publish failure so operators
// get an actionable, specific error instead of a silent 120s timeout.
type ProbeFailedError struct {
	Endpoint string
}

func (e *ProbeFailedError) Error() string {
	return fmt.Sprintf("configured endpoint %s is not reachable from the internet — check firewall/security-group rules", e.Endpoint)
}

// roundTripperFunc adapts a function to http.RoundTripper.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// rendezvousBootstrap implements the RFD 108 fallback: publish a
// PSK-encrypted "how to reach me" record to Discovery, then serve
// RequestCertificate over whichever connection the Colony dials back with.
func (c *Client) rendezvousBootstrap(ctx context.Context, token string) (*Result, error) {
	if c.cfg.BootstrapPublicEndpoint == "" {
		return nil, fmt.Errorf("no --bootstrap-public-endpoint/CORAL_BOOTSTRAP_PUBLIC_ENDPOINT configured; " +
			"cannot fall back to PSK-encrypted rendezvous bootstrap (RFD 108)")
	}

	key, err := rendezvous.DeriveKey(c.cfg.BootstrapPSK, c.cfg.ColonyID)
	if err != nil {
		return nil, fmt.Errorf("failed to derive rendezvous key: %w", err)
	}

	listenPort := c.cfg.BootstrapListenPort
	if listenPort == 0 {
		listenPort = constants.DefaultAgentBootstrapPort
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", listenPort))
	if err != nil {
		return nil, fmt.Errorf("failed to open rendezvous listener on port %d: %w", listenPort, err)
	}
	defer func() { _ = ln.Close() }()

	sessionNonce, err := rendezvous.GenerateSessionNonce()
	if err != nil {
		return nil, fmt.Errorf("failed to generate session nonce: %w", err)
	}
	writeToken, err := rendezvous.GenerateWriteToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate write token: %w", err)
	}

	c.logger.Info().
		Int("port", listenPort).
		Str("endpoint", c.cfg.BootstrapPublicEndpoint).
		Msg("Bootstrap rendezvous listening")

	discoveryClient := discovery.NewClient(c.cfg.DiscoveryEndpoint)

	var recordID string
	publish := func() error {
		payload := rendezvous.Payload{
			Endpoint:     c.cfg.BootstrapPublicEndpoint,
			SessionNonce: sessionNonce,
			WriteToken:   writeToken,
			ExpiresAt:    time.Now().Add(constants.DefaultRendezvousRecordTTL),
		}
		// A fresh gcm_nonce is required on every Seal call, including every
		// republish — even though session_nonce/write_token stay stable,
		// expires_at changes, so the plaintext (and therefore the nonce)
		// must be fresh too. rendezvous.Seal always generates one.
		ciphertext, gcmNonce, err := rendezvous.Seal(key, payload)
		if err != nil {
			return fmt.Errorf("failed to seal rendezvous payload: %w", err)
		}

		resp, err := discoveryClient.PublishBootstrapRendezvous(ctx, &discovery.PublishBootstrapRendezvousRequest{
			MeshID:             c.cfg.ColonyID,
			Ciphertext:         ciphertext,
			GCMNonce:           gcmNonce,
			TTLSeconds:         int32(constants.DefaultRendezvousRecordTTL.Seconds()),
			ProbeEndpoint:      c.cfg.BootstrapPublicEndpoint,
			RecordID:           recordID,
			WriteToken:         writeToken,
			VerifyReachability: c.cfg.VerifyBootstrapReachability,
		})
		if err != nil {
			return fmt.Errorf("failed to publish rendezvous record: %w", err)
		}
		if !resp.Success {
			if resp.ProbeFailed {
				return &ProbeFailedError{Endpoint: c.cfg.BootstrapPublicEndpoint}
			}
			return fmt.Errorf("discovery rejected rendezvous publish")
		}
		recordID = resp.RecordID
		return nil
	}

	if err := publish(); err != nil {
		return nil, err
	}

	type outcome struct {
		res *Result
		err error
	}
	resultCh := make(chan outcome, 1)
	var once sync.Once
	deliver := func(o outcome) { once.Do(func() { resultCh <- o }) }

	sem := make(chan struct{}, constants.DefaultRendezvousMaxConcurrentHandshakes)
	acceptCtx, cancelAccept := context.WithCancel(ctx)
	defer cancelAccept()

	// Accept loop: keeps calling Accept() and dispatches each connection's
	// handshake to a bounded pool of goroutines so a silent connection
	// (Discovery's own reachability probe, a scanner) can never block
	// accepting the next one. "Single-use" describes the listener's
	// lifetime, not a single accepted socket.
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			select {
			case sem <- struct{}{}:
			case <-acceptCtx.Done():
				_ = conn.Close()
				return
			}
			go func(conn net.Conn) {
				defer func() { <-sem }()
				res, err := c.handleRendezvousConn(acceptCtx, conn, sessionNonce, token)
				if err != nil {
					c.logger.Debug().Err(err).Msg("rendezvous: inbound connection rejected")
					return
				}
				deliver(outcome{res: res})
			}(conn)
		}
	}()

	deadline := time.Now().Add(constants.DefaultRendezvousWaitBudget)
	ticker := time.NewTicker(constants.DefaultRendezvousRepublishInterval)
	defer ticker.Stop()

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, fmt.Errorf("rendezvous bootstrap timed out after %s waiting for colony dial-back; "+
				"no dial-back received (check the colony's Discovery configuration and network path)", constants.DefaultRendezvousWaitBudget)
		}
		timeoutTimer := time.NewTimer(remaining)

		select {
		case o := <-resultCh:
			timeoutTimer.Stop()
			return o.res, o.err
		case <-ticker.C:
			timeoutTimer.Stop()
			if err := publish(); err != nil {
				c.logger.Warn().Err(err).Msg("rendezvous: republish failed")
			}
		case <-timeoutTimer.C:
			return nil, fmt.Errorf("rendezvous bootstrap timed out after %s waiting for colony dial-back; "+
				"no dial-back received (check the colony's Discovery configuration and network path)", constants.DefaultRendezvousWaitBudget)
		case <-ctx.Done():
			timeoutTimer.Stop()
			return nil, ctx.Err()
		}
	}
}

// handleRendezvousConn validates an inbound connection as the real Colony
// (RFD 048 fingerprint/SAN check, unchanged) and, if valid, issues
// RequestCertificate over it, attaching the rendezvous session nonce.
func (c *Client) handleRendezvousConn(ctx context.Context, conn net.Conn, sessionNonce []byte, token string) (*Result, error) {
	if err := conn.SetDeadline(time.Now().Add(constants.DefaultRendezvousHandshakeDeadline)); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to set handshake deadline: %w", err)
	}

	// Agent is the TCP passive acceptor here, but still plays the TLS
	// *client* role — the Colony flips dial direction while keeping its
	// TLS server role (RFD 108 Key Insight).
	tlsConn := tls.Client(conn, c.validator.GetTLSConfig())
	if err := tlsConn.Handshake(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("TLS handshake failed: %w", err)
	}

	state := tlsConn.ConnectionState()
	if _, err := c.validator.ValidateConnectionState(&state); err != nil {
		_ = tlsConn.Close()
		return nil, fmt.Errorf("colony validation failed: %w", err)
	}

	// Handshake + validation succeeded: this is the real colony. The
	// remaining exchange gets its own timeout via ctx, not the handshake
	// deadline.
	if err := tlsConn.SetDeadline(time.Time{}); err != nil {
		_ = tlsConn.Close()
		return nil, fmt.Errorf("failed to clear handshake deadline: %w", err)
	}

	tr := &http2.Transport{}
	cc, err := tr.NewClientConn(tlsConn)
	if err != nil {
		_ = tlsConn.Close()
		return nil, fmt.Errorf("failed to establish HTTP/2 client connection: %w", err)
	}
	defer func() { _ = tlsConn.Close() }()

	httpClient := &http.Client{Transport: roundTripperFunc(cc.RoundTrip)}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	csr, err := c.createCSR(pub, priv)
	if err != nil {
		return nil, err
	}

	c.logger.Info().Bool("psk_provided", c.cfg.BootstrapPSK != "").Msg("Requesting certificate over rendezvous dial-back connection")

	client := colonyv1connect.NewColonyServiceClient(httpClient, "https://coral-rendezvous")
	req := connect.NewRequest(&colonyv1.RequestCertificateRequest{
		Jwt:          token,
		Csr:          csr,
		BootstrapPsk: c.cfg.BootstrapPSK,
	})
	req.Header().Set(constants.RendezvousNonceHeader, base64.StdEncoding.EncodeToString(sessionNonce))

	resp, err := client.RequestCertificate(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("RequestCertificate over rendezvous connection failed: %w", err)
	}

	return c.parseAndVerifyResult(resp.Msg, pub, priv)
}
