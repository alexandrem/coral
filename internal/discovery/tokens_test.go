package discovery

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coral-mesh/coral/internal/discovery/keys"
)

func TestTokenManager_Ed25519(t *testing.T) {
	// Create temp dir for keys
	tempDir := t.TempDir()
	keyPath := filepath.Join(tempDir, "keys.json")

	// Initialize Key Manager
	keyMgr, err := keys.NewManager(keyPath, 30*24*time.Hour, zerolog.Nop())
	require.NoError(t, err)

	// Initialize Token Manager
	tm := NewTokenManager(TokenConfig{
		KeyManager: keyMgr,
		DefaultTTL: time.Minute,
		Issuer:     "test-issuer",
		Audience:   "test-audience",
	})

	// Create Ticket
	tokenStr, _, err := tm.CreateReferralTicket("reef-1", "colony-1", "agent-1", "bootstrap")
	require.NoError(t, err)

	// Parse and Verify Token
	token, err := jwt.ParseWithClaims(tokenStr, &ReferralClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Verify Algorithm
		if _, ok := token.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, jwt.ErrSignatureInvalid
		}

		// Get KID from header
		kid, ok := token.Header["kid"].(string)
		if !ok {
			return nil, jwt.ErrTokenInvalidClaims
		}

		// Find key in Key Manager (Current or Previous)
		jwkSet := keyMgr.GetJWKS()
		for _, k := range jwkSet.Keys {
			if k.KID == kid {
				return keyMgr.CurrentKey().PublicKey, nil // Simplified for test, in real world we'd map public key from JWKS
			}
		}

		return keyMgr.CurrentKey().PublicKey, nil
	})

	require.NoError(t, err)
	assert.True(t, token.Valid)

	claims, ok := token.Claims.(*ReferralClaims)
	require.True(t, ok)
	assert.Equal(t, "reef-1", claims.ReefID)
	assert.Equal(t, "colony-1", claims.ColonyID)
	assert.Equal(t, "agent-1", claims.AgentID)
	assert.Equal(t, "test-issuer", claims.Issuer)
}
