// Package keys wraps the coral-crypto/keys package for backward compatibility.
package keys

import (
	"time"

	cryptokeys "github.com/coral-mesh/coral-crypto/keys"
	"github.com/rs/zerolog"
)

// KeyPair is an alias to coral-crypto's KeyPair for API compatibility.
type KeyPair = cryptokeys.KeyPair

// JWK is an alias to coral-crypto's JWK for API compatibility.
type JWK = cryptokeys.JWK

// JWKS is an alias to coral-crypto's JWKS for API compatibility.
type JWKS = cryptokeys.JWKS

// Manager wraps coral-crypto's Manager with logging support.
type Manager struct {
	*cryptokeys.Manager
	logger zerolog.Logger
}

// NewManager creates a new key manager.
func NewManager(
	storagePath string,
	rotationPeriod time.Duration,
	logger zerolog.Logger,
) (*Manager, error) {
	mgr, err := cryptokeys.NewManager(cryptokeys.ManagerConfig{
		StoragePath:    storagePath,
		RotationPeriod: rotationPeriod,
	})
	if err != nil {
		return nil, err
	}

	return &Manager{
		Manager: mgr,
		logger:  logger.With().Str("component", "keys_manager").Logger(),
	}, nil
}
