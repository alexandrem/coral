// Package discovery provides token generation and validation for NAT traversal.
package discovery

import (
	"fmt"
	"time"

	cryptojwt "github.com/coral-mesh/coral-crypto/jwt"

	"github.com/coral-mesh/coral/internal/discovery/keys"
)

// TokenManager handles referral ticket issuance for agent certificate enrollment.
// Implements RFD 049 - Discovery-Based Agent Authorization.
type TokenManager struct {
	keyManager *keys.Manager
	defaultTTL time.Duration
	issuer     string
	audience   string
}

// TokenConfig contains token manager configuration.
type TokenConfig struct {
	KeyManager *keys.Manager
	DefaultTTL time.Duration
	Issuer     string
	Audience   string
}

// NewTokenManager creates a new token manager instance.
func NewTokenManager(cfg TokenConfig) *TokenManager {
	if cfg.DefaultTTL == 0 {
		cfg.DefaultTTL = 1 * time.Minute // Default 1-minute TTL per RFD 049.
	}
	if cfg.Issuer == "" {
		cfg.Issuer = cryptojwt.DefaultIssuer
	}
	if cfg.Audience == "" {
		cfg.Audience = cryptojwt.DefaultAudience
	}

	return &TokenManager{
		keyManager: cfg.KeyManager,
		defaultTTL: cfg.DefaultTTL,
		issuer:     cfg.Issuer,
		audience:   cfg.Audience,
	}
}

// ReferralClaims is an alias to coral-crypto's ReferralClaims for API compatibility.
type ReferralClaims = cryptojwt.ReferralClaims

// CreateReferralTicket creates a new stateless referral ticket.
func (tm *TokenManager) CreateReferralTicket(reefID, colonyID, agentID, intent string) (string, int64, error) {
	currentKey := tm.keyManager.CurrentKey()
	if currentKey == nil {
		return "", 0, fmt.Errorf("no active signing key available")
	}

	// Create token using coral-crypto static function with configured issuer/audience.
	return cryptojwt.CreateReferralTicketStatic(
		currentKey.PrivateKey,
		currentKey.ID,
		reefID,
		colonyID,
		agentID,
		intent,
		int(tm.defaultTTL.Seconds()),
		tm.issuer,
		tm.audience,
	)
}
