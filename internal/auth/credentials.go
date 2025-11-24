// Package auth provides authentication and credential management.
package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// GenerateColonyID creates a unique colony identifier.
// Format: <app-name>-<environment>-<random-hex>
// Example: my-shop-production-a3f2e1
func GenerateColonyID(appName, environment string) (string, error) {
	// Normalize app name and environment
	normalizedApp := normalize(appName)
	normalizedEnv := normalize(environment)

	// Generate 6 hex characters (3 random bytes)
	randomBytes := make([]byte, 3)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	randomHex := hex.EncodeToString(randomBytes)

	colonyID := fmt.Sprintf("%s-%s-%s", normalizedApp, normalizedEnv, randomHex)
	return colonyID, nil
}

// GenerateColonySecret creates a secure random colony secret.
// Returns a base64-encoded random string (32 bytes = 256 bits).
func GenerateColonySecret() (string, error) {
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return "", fmt.Errorf("failed to generate colony secret: %w", err)
	}

	// Use base64 URL encoding (no padding) for cleaner secrets
	secret := base64.RawURLEncoding.EncodeToString(secretBytes)
	return secret, nil
}

// normalize converts a string to lowercase and replaces non-alphanumeric with hyphens.
// Example: "My Shop!" -> "my-shop"
func normalize(s string) string {
	s = strings.ToLower(s)

	var result strings.Builder
	lastWasHyphen := false

	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			result.WriteRune(r)
			lastWasHyphen = false
		} else if !lastWasHyphen && result.Len() > 0 {
			result.WriteRune('-')
			lastWasHyphen = true
		}
	}

	// Trim trailing hyphen
	normalized := result.String()
	normalized = strings.TrimSuffix(normalized, "-")

	return normalized
}
