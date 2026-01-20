// Package bootstrap implements agent certificate bootstrap for mTLS.
// This implements RFD 048 - Agent Certificate Bootstrap.
package bootstrap

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// CAValidator validates the Root CA fingerprint and colony identity during TLS handshake.
// This provides defense against MITM attacks during agent bootstrap.
type CAValidator struct {
	expectedFingerprint string
	expectedColonyID    string
}

// NewCAValidator creates a new CA validator with the expected fingerprint and colony ID.
// fingerprint should be in the format "sha256:hexstring" or just "hexstring".
// colonyID is the expected colony ID that should appear in the server certificate SAN.
func NewCAValidator(fingerprint, colonyID string) *CAValidator {
	// Normalize: remove prefix, remove colons, and lowercase
	fp := strings.TrimPrefix(fingerprint, "sha256:")
	fp = strings.ReplaceAll(fp, ":", "")
	fp = strings.ToLower(strings.TrimSpace(fp))

	return &CAValidator{
		expectedFingerprint: fp,
		expectedColonyID:    colonyID,
	}
}

// ValidationResult contains the result of certificate validation.
type ValidationResult struct {
	// RootCA is the extracted root certificate.
	RootCA *x509.Certificate

	// ComputedFingerprint is the SHA256 fingerprint of the Root CA.
	ComputedFingerprint string

	// ServerSPIFFEID is the SPIFFE ID extracted from the server certificate SAN.
	ServerSPIFFEID string

	// ExtractedColonyID is the colony ID extracted from the SPIFFE ID.
	ExtractedColonyID string
}

// ValidateConnectionState validates the TLS connection state, extracting and validating:
// 1. Root CA fingerprint (defense against MITM)
// 2. Colony ID in server certificate SAN (defense against cross-colony impersonation)
// 3. Certificate chain integrity.
func (v *CAValidator) ValidateConnectionState(state *tls.ConnectionState) (*ValidationResult, error) {
	if state == nil {
		return nil, fmt.Errorf("TLS connection state is nil")
	}

	if len(state.PeerCertificates) == 0 {
		return nil, fmt.Errorf("no peer certificates in TLS connection")
	}

	// Extract certificates from the connection.
	serverCert := state.PeerCertificates[0]
	var rootCA *x509.Certificate

	// Find Root CA in the verified chains or peer certificates.
	// The chain order is typically: [ServerCert, Intermediate, Root].
	if len(state.VerifiedChains) > 0 && len(state.VerifiedChains[0]) > 0 {
		// Use the last certificate in the verified chain (should be Root CA).
		chain := state.VerifiedChains[0]
		rootCA = chain[len(chain)-1]
	} else if len(state.PeerCertificates) > 1 {
		// Fallback: use the last peer certificate.
		rootCA = state.PeerCertificates[len(state.PeerCertificates)-1]
	} else {
		return nil, fmt.Errorf("cannot find Root CA in certificate chain")
	}

	// Validate that we found an actual CA certificate.
	if !rootCA.IsCA {
		return nil, fmt.Errorf("extracted certificate is not a CA certificate")
	}

	// Compute fingerprint of the Root CA.
	fingerprint := computeFingerprint(rootCA)

	result := &ValidationResult{
		RootCA:              rootCA,
		ComputedFingerprint: fingerprint,
	}

	// Validate fingerprint matches expected.
	if fingerprint != v.expectedFingerprint {
		return result, &FingerprintMismatchError{
			Expected: v.expectedFingerprint,
			Received: fingerprint,
		}
	}

	// Extract and validate colony ID from server certificate SAN.
	spiffeID, colonyID, err := extractColonyFromSAN(serverCert)
	if err != nil {
		return result, fmt.Errorf("failed to extract colony ID from server certificate: %w", err)
	}

	result.ServerSPIFFEID = spiffeID
	result.ExtractedColonyID = colonyID

	// Validate colony ID matches expected.
	if colonyID != v.expectedColonyID {
		return result, &ColonyMismatchError{
			Expected: v.expectedColonyID,
			Received: colonyID,
		}
	}

	// Validate certificate chain integrity.
	if err := v.validateChainIntegrity(state); err != nil {
		return result, fmt.Errorf("certificate chain validation failed: %w", err)
	}

	return result, nil
}

// validateChainIntegrity verifies the certificate chain is properly signed.
func (v *CAValidator) validateChainIntegrity(state *tls.ConnectionState) error {
	if len(state.VerifiedChains) == 0 {
		// If using InsecureSkipVerify with manual validation, verify the chain ourselves.
		return v.manualChainVerification(state.PeerCertificates)
	}

	// Chain was already verified by Go's TLS stack.
	return nil
}

// manualChainVerification verifies the certificate chain when using InsecureSkipVerify.
func (v *CAValidator) manualChainVerification(certs []*x509.Certificate) error {
	if len(certs) == 0 {
		return fmt.Errorf("no certificates to verify")
	}

	now := time.Now()
	for i, cert := range certs {
		// Idiom: Check expiration in manual verification
		if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
			return fmt.Errorf("certificate at index %d is expired or not yet valid", i)
		}

		if i < len(certs)-1 {
			parent := certs[i+1]
			if err := cert.CheckSignatureFrom(parent); err != nil {
				return fmt.Errorf("certificate at index %d not signed by parent: %w", i, err)
			}
		}
	}

	// Verify the root is self-signed.
	root := certs[len(certs)-1]
	if err := root.CheckSignatureFrom(root); err != nil {
		return fmt.Errorf("root certificate is not self-signed (chain might be incomplete): %w", err)
	}

	return nil
}

// GetTLSConfig returns a TLS config suitable for bootstrap connections.
// It uses InsecureSkipVerify because we perform manual Root CA fingerprint validation.
func (v *CAValidator) GetTLSConfig() *tls.Config {
	return &tls.Config{
		// We skip automatic verification and perform manual fingerprint validation instead.
		InsecureSkipVerify: true, // #nosec G402: Manual validation via fingerprint.
	}
}

// computeFingerprint computes the SHA256 fingerprint of a certificate.
func computeFingerprint(cert *x509.Certificate) string {
	hash := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(hash[:])
}

// extractColonyFromSAN extracts the SPIFFE ID and colony ID from a server certificate's SAN.
// Expected format: spiffe://coral/colony/{colony-id}
func extractColonyFromSAN(cert *x509.Certificate) (string, string, error) {
	for _, uri := range cert.URIs {
		// Ensure standard SPIFFE format: spiffe://trust-domain/path
		if uri.Scheme != "spiffe" || uri.Host != "coral" {
			continue
		}

		// Use TrimPrefix to handle the leading slash safely
		path := strings.TrimPrefix(uri.Path, "/")
		parts := strings.Split(path, "/")

		if len(parts) >= 2 && parts[0] == "colony" {
			return uri.String(), parts[1], nil
		}
	}

	return "", "", fmt.Errorf("no valid Coral Colony SPIFFE ID found in SAN URIs")
}

// BuildAgentSPIFFEID builds a SPIFFE ID URI for an agent.
// Format: spiffe://coral/colony/{colony-id}/agent/{agent-id}
func BuildAgentSPIFFEID(colonyID, agentID string) (*url.URL, error) {
	return url.Parse(fmt.Sprintf("spiffe://coral/colony/%s/agent/%s", colonyID, agentID))
}

// FingerprintMismatchError indicates the Root CA fingerprint doesn't match expected.
// This likely indicates a MITM attack or misconfiguration.
type FingerprintMismatchError struct {
	Expected string
	Received string
}

func (e *FingerprintMismatchError) Error() string {
	return fmt.Sprintf("root CA fingerprint mismatch: expected %s, received %s (possible MITM attack)", e.Expected, e.Received)
}

// ColonyMismatchError indicates the colony ID in the server certificate doesn't match expected.
// This could indicate cross-colony impersonation.
type ColonyMismatchError struct {
	Expected string
	Received string
}

func (e *ColonyMismatchError) Error() string {
	return fmt.Sprintf("colony ID mismatch: expected %s, received %s (possible cross-colony impersonation)", e.Expected, e.Received)
}
