// Package ca provides certificate authority management for mTLS.
package ca

import (
	"crypto/x509"
	"fmt"
	"time"

	cryptojwt "github.com/coral-mesh/coral-crypto/jwt"

	"github.com/coral-mesh/coral/internal/colony/jwks"
)

// PolicyEnforcer handles access control and policy enforcement for certificate operations.
type PolicyEnforcer struct {
	jwksClient *jwks.Client
	colonyID   string
}

// NewPolicyEnforcer creates a new PolicyEnforcer instance.
func NewPolicyEnforcer(jwksClient *jwks.Client, colonyID string) *PolicyEnforcer {
	return &PolicyEnforcer{
		jwksClient: jwksClient,
		colonyID:   colonyID,
	}
}

// ReferralClaims is an alias to coral-crypto's ReferralClaims for API compatibility.
type ReferralClaims = cryptojwt.ReferralClaims

// BootstrapClaims is an alias to coral-crypto's BootstrapClaims for API compatibility.
type BootstrapClaims = cryptojwt.BootstrapClaims

// ValidateReferralTicket validates a referral ticket JWT.
// This is a stateless validation per RFD 049 using JWKS.
func (p *PolicyEnforcer) ValidateReferralTicket(tokenString string) (*ReferralClaims, error) {
	if p.jwksClient == nil {
		return nil, fmt.Errorf("JWKS client not initialized")
	}

	// Use the JWKS client's validator which uses coral-crypto.
	return p.jwksClient.ValidateReferralTicket(tokenString)
}

// ValidateAgentCSR validates a CSR for agent certificate issuance.
func (p *PolicyEnforcer) ValidateAgentCSR(csr *x509.CertificateRequest, agentID, colonyID string) error {
	// Verify CSR signature.
	if err := csr.CheckSignature(); err != nil {
		return fmt.Errorf("invalid CSR signature: %w", err)
	}

	// Enforce policy: CN must match agent_id.
	expectedCN := fmt.Sprintf("agent.%s.%s", agentID, colonyID)
	if csr.Subject.CommonName != expectedCN {
		return fmt.Errorf("CSR CN mismatch: expected %s, got %s", expectedCN, csr.Subject.CommonName)
	}

	return nil
}

// CanIssueAgentCertificate checks if a certificate can be issued for an agent.
func (p *PolicyEnforcer) CanIssueAgentCertificate(agentID, colonyID string) error {
	// Basic validation: ensure IDs are not empty.
	if agentID == "" {
		return fmt.Errorf("agent ID cannot be empty")
	}
	if colonyID == "" {
		return fmt.Errorf("colony ID cannot be empty")
	}

	// Additional policy checks can be added here.
	// For example: check if the agent is allowed to join this colony.

	return nil
}

// CanRotateIntermediate checks if an intermediate certificate can be rotated.
func (p *PolicyEnforcer) CanRotateIntermediate(certType string) error {
	// Validate certificate type.
	if certType != "server" && certType != "agent" {
		return fmt.Errorf("invalid certificate type: %s (must be 'server' or 'agent')", certType)
	}

	// Additional policy checks can be added here.
	// For example: check if rotation is allowed based on time since last rotation.

	return nil
}

// GetCertificateValidity returns the validity period for a certificate type.
func (p *PolicyEnforcer) GetCertificateValidity(certType string) time.Duration {
	switch certType {
	case "agent":
		return 90 * 24 * time.Hour // 90 days.
	case "server":
		return 90 * 24 * time.Hour // 90 days.
	case "intermediate":
		return 365 * 24 * time.Hour // 1 year.
	case "root":
		return 10 * 365 * 24 * time.Hour // 10 years.
	case "policy-signing":
		return 10 * 365 * 24 * time.Hour // 10 years.
	default:
		return 90 * 24 * time.Hour // Default to 90 days.
	}
}
