package auth

import (
	"time"
)

// APIToken represents a stored API token configuration.
type APIToken struct {
	// TokenID is the unique identifier for this token.
	TokenID string `yaml:"token_id" json:"token_id"`

	// TokenHash is the bcrypt hash of the token (never store plaintext).
	TokenHash string `yaml:"token_hash" json:"-"`

	// Permissions is the list of permissions granted to this token.
	Permissions []Permission `yaml:"permissions" json:"permissions"`

	// RateLimit is the optional rate limit (e.g., "100/hour").
	RateLimit string `yaml:"rate_limit,omitempty" json:"rate_limit,omitempty"`

	// CreatedAt is when the token was created.
	CreatedAt time.Time `yaml:"created_at" json:"created_at"`

	// LastUsedAt is when the token was last used.
	LastUsedAt *time.Time `yaml:"last_used_at,omitempty" json:"last_used_at,omitempty"`

	// Revoked indicates if the token has been revoked.
	Revoked bool `yaml:"revoked,omitempty" json:"revoked,omitempty"`
}

// TokenInfo is returned after token creation (contains plaintext token once).
type TokenInfo struct {
	// TokenID is the unique identifier for this token.
	TokenID string `json:"token_id"`

	// Token is the plaintext token value (shown only once after creation).
	Token string `json:"token"`

	// Permissions is the list of permissions granted to this token.
	Permissions []Permission `json:"permissions"`

	// RateLimit is the optional rate limit.
	RateLimit string `json:"rate_limit,omitempty"`
}

// TokensFile represents the structure of the tokens.yaml file.
type TokensFile struct {
	// Version is the schema version for the tokens file.
	Version string `yaml:"version"`

	// Tokens is the list of API tokens.
	Tokens []APIToken `yaml:"tokens"`
}
