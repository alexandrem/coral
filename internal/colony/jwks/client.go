package jwks

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog"
)

// Client handles fetching and caching of JWKS keys.
type Client struct {
	discoveryURL string
	httpClient   *http.Client
	logger       zerolog.Logger

	mu          sync.RWMutex
	keys        map[string]ed25519.PublicKey
	lastRefresh time.Time
}

// Global cache settings
const (
	refreshInterval = 5 * time.Minute
	minRefreshRate  = 10 * time.Second // Prevent spamming discovery
)

// JWK represents a JSON Web Key.
type JWK struct {
	KID string `json:"kid"`
	KTY string `json:"kty"`
	CRV string `json:"crv"`
	X   string `json:"x"`
	USE string `json:"use"`
	ALG string `json:"alg"`
}

// JWKS represents a JSON Web Key Set.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// NewClient creates a new JWKS client.
func NewClient(discoveryURL string, logger zerolog.Logger) *Client {
	return &Client{
		discoveryURL: discoveryURL,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
		logger:       logger,
		keys:         make(map[string]ed25519.PublicKey),
	}
}

// GetKeyFunc returns a Keyfunc compatible with jwt.Parse.
func (c *Client) GetKeyFunc() jwt.Keyfunc {
	return func(token *jwt.Token) (interface{}, error) {
		// Strict algorithm check: Whitelist EdDSA.
		if _, ok := token.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v (expected EdDSA)", token.Header["alg"])
		}

		kid, ok := token.Header["kid"].(string)
		if !ok {
			return nil, fmt.Errorf("missing kid in token header")
		}

		return c.GetKey(kid)
	}
}

// GetKey returns the public key for the given key ID.
// It will try to refresh the cache if the key is not found.
func (c *Client) GetKey(kid string) (interface{}, error) {
	// First check cache (optimistic read)
	c.mu.RLock()
	key, exists := c.keys[kid]
	c.mu.RUnlock()

	if exists {
		return key, nil
	}

	// Key not found, trigger refresh
	if err := c.Refresh(); err != nil {
		return nil, fmt.Errorf("key not found locally and refresh failed: %w", err)
	}

	// Check again after refresh
	c.mu.RLock()
	key, exists = c.keys[kid]
	c.mu.RUnlock()

	if exists {
		return key, nil
	}

	return nil, fmt.Errorf("key %q not found in JWKS", kid)
}

// Refresh fetches the current JWKS from the discovery service.
func (c *Client) Refresh() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Rate limiting: Don't refresh if we just did recently
	if time.Since(c.lastRefresh) < minRefreshRate {
		return nil
	}

	url := fmt.Sprintf("%s/.well-known/jwks.json", c.discoveryURL)
	c.logger.Debug().Str("url", url).Msg("Refreshing JWKS")

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return fmt.Errorf("failed to fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var jwks JWKS
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("failed to decode JWKS: %w", err)
	}

	newKeys := make(map[string]ed25519.PublicKey)
	for _, jwk := range jwks.Keys {
		if jwk.KTY != "OKP" || jwk.CRV != "Ed25519" {
			continue // Skip unsupported keys
		}

		pubKeyBytes, err := base64.RawURLEncoding.DecodeString(jwk.X)
		if err != nil {
			c.logger.Warn().Str("kid", jwk.KID).Err(err).Msg("Failed to decode public key")
			continue
		}

		if len(pubKeyBytes) != ed25519.PublicKeySize {
			c.logger.Warn().Str("kid", jwk.KID).Int("size", len(pubKeyBytes)).Msg("Invalid public key size")
			continue
		}

		newKeys[jwk.KID] = ed25519.PublicKey(pubKeyBytes)
	}

	c.keys = newKeys
	c.lastRefresh = time.Now()
	c.logger.Debug().Int("keys_count", len(c.keys)).Msg("JWKS refreshed")

	return nil
}
