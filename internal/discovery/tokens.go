package discovery

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// TokenManager handles bootstrap token issuance for agent certificate enrollment.
// Implements RFD 022 - Embedded step-ca for Agent mTLS Bootstrap.
type TokenManager struct {
	db         *sql.DB
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
func NewTokenManager(db *sql.DB, cfg TokenConfig) *TokenManager {
	if cfg.DefaultTTL == 0 {
		cfg.DefaultTTL = 5 * time.Minute // Default 5-minute TTL per RFD 022.
	}
	if cfg.Issuer == "" {
		cfg.Issuer = "reef-control"
	}
	if cfg.Audience == "" {
		cfg.Audience = "colony-step-ca"
	}

	return &TokenManager{
		db:         db,
		signingKey: cfg.SigningKey,
		defaultTTL: cfg.DefaultTTL,
		issuer:     cfg.Issuer,
		audience:   cfg.Audience,
	}
}

// BootstrapClaims contains JWT claims for bootstrap tokens.
type BootstrapClaims struct {
	ReefID   string `json:"reef_id"`
	ColonyID string `json:"colony_id"`
	AgentID  string `json:"agent_id"`
	Intent   string `json:"intent"`
	jwt.RegisteredClaims
}

// CreateBootstrapToken creates a new single-use bootstrap token.
func (tm *TokenManager) CreateBootstrapToken(reefID, colonyID, agentID, intent string) (string, int64, error) {
	tokenID := uuid.New().String()
	now := time.Now()
	expiresAt := now.Add(tm.defaultTTL)

	// Create JWT claims.
	claims := &BootstrapClaims{
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

	// Compute token hash for single-use tracking.
	tokenHash := sha256.Sum256([]byte(tokenString))
	tokenHashHex := hex.EncodeToString(tokenHash[:])

	// Store token metadata.
	_, err = tm.db.Exec(`
		INSERT INTO bootstrap_tokens (
			token_id, jwt_hash, reef_id, colony_id, agent_id, intent,
			issued_at, expires_at, status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'active')
	`, tokenID, tokenHashHex, reefID, colonyID, agentID, intent, now, expiresAt)
	if err != nil {
		return "", 0, fmt.Errorf("failed to store token metadata: %w", err)
	}

	return tokenString, expiresAt.Unix(), nil
}

// generateSecureToken generates a cryptographically secure random token.
func generateSecureToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// CleanupExpiredTokens removes expired tokens from the database.
func (tm *TokenManager) CleanupExpiredTokens() error {
	_, err := tm.db.Exec(`
		DELETE FROM bootstrap_tokens
		WHERE expires_at < ? AND status = 'active'
	`, time.Now())
	if err != nil {
		return fmt.Errorf("failed to cleanup expired tokens: %w", err)
	}
	return nil
}
