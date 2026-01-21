// Package ca provides certificate authority management for mTLS.
package ca

import (
	"crypto/x509"
	"fmt"
	"time"

	"github.com/coral-mesh/coral/internal/colony/jwks"
	"github.com/golang-jwt/jwt/v5"
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

// ReferralClaims contains JWT claims for referral tickets (RFD 049).
type ReferralClaims struct {
	ReefID   string `json:"reef_id"`
	ColonyID string `json:"colony_id"`
	AgentID  string `json:"agent_id"`
	Intent   string `json:"intent"`
	jwt.RegisteredClaims
}

// BootstrapClaims contains JWT claims for bootstrap tokens.
type BootstrapClaims struct {
	ReefID   string `json:"reef_id"`
	ColonyID string `json:"colony_id"`
	AgentID  string `json:"agent_id"`
	Intent   string `json:"intent"`
	jwt.RegisteredClaims
}

// ValidateReferralTicket validates a referral ticket JWT.
// This is a stateless validation per RFD 049 using JWKS.
func (p *PolicyEnforcer) ValidateReferralTicket(tokenString string) (*ReferralClaims, error) {
	if p.jwksClient == nil {
		return nil, fmt.Errorf("JWKS client not initialized")
	}

	// Parse and validate JWT using JWKS keyfunc.
	// The Keyfunc in jwks.Client enforces EdDSA whitelist.
	token, err := jwt.ParseWithClaims(tokenString, &ReferralClaims{}, p.jwksClient.GetKeyFunc())
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
