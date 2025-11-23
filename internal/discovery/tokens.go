// Package discovery provides token generation and validation for NAT traversal.
package discovery

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// TokenManager handles referral ticket issuance for agent certificate enrollment.
// Implements RFD 049 - Discovery-Based Agent Authorization.
type TokenManager struct {
	signingKey []byte
	defaultTTL time.Duration
	issuer     string
	audience   string
}

// TokenConfig contains token manager configuration.
type TokenConfig struct {
	SigningKey []byte
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
		cfg.Issuer = "coral-discovery"
	}
	if cfg.Audience == "" {
		cfg.Audience = "coral-colony"
	}

	return &TokenManager{
		signingKey: cfg.SigningKey,
		defaultTTL: cfg.DefaultTTL,
		issuer:     cfg.Issuer,
		audience:   cfg.Audience,
	}
}

// ReferralClaims contains JWT claims for referral tickets.
type ReferralClaims struct {
	ReefID   string `json:"reef_id"`
	ColonyID string `json:"colony_id"`
	AgentID  string `json:"agent_id"`
	Intent   string `json:"intent"`
	jwt.RegisteredClaims
}

// CreateReferralTicket creates a new stateless referral ticket.
func (tm *TokenManager) CreateReferralTicket(reefID, colonyID, agentID, intent string) (string, int64, error) {
	tokenID := uuid.New().String()
	now := time.Now()
	expiresAt := now.Add(tm.defaultTTL)

	// Create JWT claims.
	claims := &ReferralClaims{
		ReefID:   reefID,
		ColonyID: colonyID,
		AgentID:  agentID,
		Intent:   intent,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        tokenID,
			Issuer:    tm.issuer,
			Audience:  jwt.ClaimStrings{tm.audience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}

	// Sign token.
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(tm.signingKey)
	if err != nil {
		return "", 0, fmt.Errorf("failed to sign token: %w", err)
	}

	return tokenString, expiresAt.Unix(), nil
}
