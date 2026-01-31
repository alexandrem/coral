package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

// TokenStore manages API tokens with in-memory caching and file persistence.
type TokenStore struct {
	mu       sync.RWMutex
	tokens   map[string]*APIToken // tokenID -> token
	filePath string               // Path to tokens.yaml
}

// NewTokenStore creates a new token store.
// If filePath is provided, tokens will be loaded from and persisted to that file.
func NewTokenStore(filePath string) *TokenStore {
	ts := &TokenStore{
		tokens:   make(map[string]*APIToken),
		filePath: filePath,
	}

	// Load existing tokens if file exists.
	if filePath != "" {
		_ = ts.loadFromFile()
	}

	return ts
}

// GenerateToken creates a new API token with the given ID and permissions.
// Returns the token info including the plaintext token (shown only once).
func (ts *TokenStore) GenerateToken(
	tokenID string,
	permissions []Permission,
	rateLimit string,
) (*TokenInfo, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	// Check for duplicate token ID.
	if _, exists := ts.tokens[tokenID]; exists {
		return nil, fmt.Errorf("token with ID %q already exists", tokenID)
	}

	// Generate 32 random bytes for the token.
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("failed to generate random token: %w", err)
	}

	// Encode as URL-safe base64.
	plainToken := base64.RawURLEncoding.EncodeToString(tokenBytes)

	// Hash for storage using bcrypt.
	hash, err := bcrypt.GenerateFromPassword([]byte(plainToken), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash token: %w", err)
	}

	// Create stored token.
	stored := &APIToken{
		TokenID:     tokenID,
		TokenHash:   string(hash),
		Permissions: permissions,
		RateLimit:   rateLimit,
		CreatedAt:   time.Now(),
	}

	// Store in memory.
	ts.tokens[tokenID] = stored

	// Persist to file.
	if err := ts.saveToFile(); err != nil {
		// Remove from memory if persistence fails.
		delete(ts.tokens, tokenID)
		return nil, fmt.Errorf("failed to persist token: %w", err)
	}

	return &TokenInfo{
		TokenID:     tokenID,
		Token:       fmt.Sprintf("coral_%s", plainToken), // Add prefix for identification.
		Permissions: permissions,
		RateLimit:   rateLimit,
	}, nil
}

// ValidateToken checks a Bearer token and returns the stored token if valid.
// The token should include the "coral_" prefix.
func (ts *TokenStore) ValidateToken(token string) (*APIToken, error) {
	// Strip "coral_" prefix if present.
	plainToken := token
	if len(token) > 6 && token[:6] == "coral_" {
		plainToken = token[6:]
	}

	ts.mu.RLock()

	// Check all tokens (O(n) but typically small number of tokens).
	var matched *APIToken
	for _, stored := range ts.tokens {
		if stored.Revoked {
			continue
		}

		if err := bcrypt.CompareHashAndPassword([]byte(stored.TokenHash), []byte(plainToken)); err == nil {
			matched = stored
			break
		}
	}

	ts.mu.RUnlock()

	if matched == nil {
		return nil, fmt.Errorf("invalid token")
	}

	// Update last used time synchronously now that the read lock is released.
	ts.updateLastUsed(matched.TokenID)
	return matched, nil
}

// GetToken returns a token by ID (without validation).
func (ts *TokenStore) GetToken(tokenID string) (*APIToken, bool) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	token, exists := ts.tokens[tokenID]
	return token, exists
}

// ListTokens returns all non-revoked tokens.
func (ts *TokenStore) ListTokens() []*APIToken {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	result := make([]*APIToken, 0, len(ts.tokens))
	for _, t := range ts.tokens {
		if !t.Revoked {
			result = append(result, t)
		}
	}
	return result
}

// RevokeToken marks a token as revoked.
func (ts *TokenStore) RevokeToken(tokenID string) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	token, exists := ts.tokens[tokenID]
	if !exists {
		return fmt.Errorf("token with ID %q not found", tokenID)
	}

	token.Revoked = true

	if err := ts.saveToFile(); err != nil {
		token.Revoked = false
		return fmt.Errorf("failed to persist token revocation: %w", err)
	}

	return nil
}

// DeleteToken permanently removes a token.
func (ts *TokenStore) DeleteToken(tokenID string) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if _, exists := ts.tokens[tokenID]; !exists {
		return fmt.Errorf("token with ID %q not found", tokenID)
	}

	delete(ts.tokens, tokenID)

	if err := ts.saveToFile(); err != nil {
		return fmt.Errorf("failed to persist token deletion: %w", err)
	}

	return nil
}

// updateLastUsed updates the last used timestamp for a token.
func (ts *TokenStore) updateLastUsed(tokenID string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if token, exists := ts.tokens[tokenID]; exists {
		now := time.Now()
		token.LastUsedAt = &now
		// Best effort persistence - don't block on this.
		_ = ts.saveToFile()
	}
}

// loadFromFile loads tokens from the YAML file.
func (ts *TokenStore) loadFromFile() error {
	if ts.filePath == "" {
		return nil
	}

	data, err := os.ReadFile(ts.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // File doesn't exist yet, that's OK.
		}
		return fmt.Errorf("failed to read tokens file: %w", err)
	}

	var tokensFile TokensFile
	if err := yaml.Unmarshal(data, &tokensFile); err != nil {
		return fmt.Errorf("failed to parse tokens file: %w", err)
	}

	for i := range tokensFile.Tokens {
		t := &tokensFile.Tokens[i]
		ts.tokens[t.TokenID] = t
	}

	return nil
}

// saveToFile persists tokens to the YAML file.
func (ts *TokenStore) saveToFile() error {
	if ts.filePath == "" {
		return nil
	}

	tokens := make([]APIToken, 0, len(ts.tokens))
	for _, t := range ts.tokens {
		tokens = append(tokens, *t)
	}

	tokensFile := TokensFile{
		Version: "1",
		Tokens:  tokens,
	}

	data, err := yaml.Marshal(tokensFile)
	if err != nil {
		return fmt.Errorf("failed to marshal tokens: %w", err)
	}

	// Write with restrictive permissions (owner read/write only).
	if err := os.WriteFile(ts.filePath, data, 0600); err != nil {
		return fmt.Errorf("failed to write tokens file: %w", err)
	}

	return nil
}

// HasPermission checks if a token has a specific permission.
func HasPermission(token *APIToken, required Permission) bool {
	for _, p := range token.Permissions {
		// Admin permission grants all permissions.
		if p == PermissionAdmin {
			return true
		}
		if p == required {
			return true
		}
	}
	return false
}
