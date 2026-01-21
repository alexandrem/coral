// Package keys implements management of Ed25519 signing keys, including generation, rotation, and persistence.
package keys

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/internal/safe"
)

// KeyPair represents an Ed25519 key pair with metadata.
type KeyPair struct {
	ID         string             `json:"id"`
	Algorithm  string             `json:"alg"` // "EdDSA"
	CreatedAt  time.Time          `json:"created_at"`
	PublicKey  ed25519.PublicKey  `json:"public_key"`
	PrivateKey ed25519.PrivateKey `json:"private_key"`
}

// Manager handles key generation, rotation, and storage.
type Manager struct {
	mu             sync.RWMutex
	currentKey     *KeyPair
	previousKeys   []*KeyPair
	storagePath    string
	rotationPeriod time.Duration
	logger         zerolog.Logger
}

// JWK represents a JSON Web Key.
type JWK struct {
	KID string `json:"kid"`
	KTY string `json:"kty"` // "OKP"
	CRV string `json:"crv"` // "Ed25519"
	X   string `json:"x"`   // Base64URL encoded public key
	USE string `json:"use"` // "sig"
	ALG string `json:"alg"` // "EdDSA"
}

// JWKS represents a JSON Web Key Set.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// NewManager creates a new key manager.
func NewManager(
	storagePath string,
	rotationPeriod time.Duration,
	logger zerolog.Logger,
) (*Manager, error) {
	if rotationPeriod == 0 {
		rotationPeriod = 30 * 24 * time.Hour // Default 30 days
	}

	km := &Manager{
		storagePath:    storagePath,
		rotationPeriod: rotationPeriod,
		previousKeys:   make([]*KeyPair, 0),
		logger:         logger.With().Str("component", "keys_manager").Logger(),
	}

	if err := km.loadKeys(); err != nil {
		// If loading fails (e.g. file doesn't exist), generate a new key
		if os.IsNotExist(err) {
			if err := km.RotateKey(); err != nil {
				return nil, fmt.Errorf("failed to generate initial key: %w", err)
			}
		} else {
			return nil, fmt.Errorf("failed to load keys: %w", err)
		}
	}

	// Check if rotation is needed
	if time.Since(km.currentKey.CreatedAt) > km.rotationPeriod {
		if err := km.RotateKey(); err != nil {
			return nil, fmt.Errorf("failed to rotate expired key: %w", err)
		}
	}

	return km, nil
}

// CurrentKey returns the current active signing key.
func (km *Manager) CurrentKey() *KeyPair {
	km.mu.RLock()
	defer km.mu.RUnlock()
	return km.currentKey
}

// RotateKey generates a new key pair and archives the current one.
func (km *Manager) RotateKey() error {
	km.mu.Lock()
	defer km.mu.Unlock()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate ed25519 key: %w", err)
	}

	newKey := &KeyPair{
		ID:         ulid.Make().String(),
		Algorithm:  "EdDSA",
		CreatedAt:  time.Now(),
		PublicKey:  pub,
		PrivateKey: priv,
	}

	if km.currentKey != nil {
		km.previousKeys = append([]*KeyPair{km.currentKey}, km.previousKeys...)
		// Keep only last 5 keys for verification overlap
		if len(km.previousKeys) > 5 {
			km.previousKeys = km.previousKeys[:5]
		}
	}

	km.currentKey = newKey
	return km.saveKeys()
}

// GetJWKS returns the JSON Web Key Set containing current and previous public keys.
func (km *Manager) GetJWKS() *JWKS {
	km.mu.RLock()
	defer km.mu.RUnlock()

	jwks := &JWKS{
		Keys: make([]JWK, 0, len(km.previousKeys)+1),
	}

	addKey := func(kp *KeyPair) {
		jwks.Keys = append(jwks.Keys, JWK{
			KID: kp.ID,
			KTY: "OKP",
			CRV: "Ed25519",
			X:   base64.RawURLEncoding.EncodeToString(kp.PublicKey),
			USE: "sig",
			ALG: "EdDSA",
		})
	}

	addKey(km.currentKey)
	for _, kp := range km.previousKeys {
		addKey(kp)
	}

	return jwks
}

// storageStruct is used for JSON serialization to file.
type storageStruct struct {
	CurrentKey   *KeyPair   `json:"current_key"`
	PreviousKeys []*KeyPair `json:"previous_keys"`
}

func (km *Manager) saveKeys() error {
	if km.storagePath == "" {
		return nil
	}

	data := storageStruct{
		CurrentKey:   km.currentKey,
		PreviousKeys: km.previousKeys,
	}

	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal keys: %w", err)
	}

	// Atomic write: create temp file, write, sync, rename
	dir := filepath.Dir(km.storagePath)
	tmpFile, err := os.CreateTemp(dir, "discovery-keys-*.json.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp key file: %w", err)
	}
	defer safe.RemoveFile(tmpFile, km.logger) // Clean up if something fails

	if _, err := tmpFile.Write(bytes); err != nil {
		safe.Close(tmpFile, km.logger, "failed to close temp key file")
		return fmt.Errorf("failed to write to temp key file: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		safe.Close(tmpFile, km.logger, "failed to close temp key file")
		return fmt.Errorf("failed to sync temp key file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp key file: %w", err)
	}

	if err := os.Rename(tmpFile.Name(), km.storagePath); err != nil {
		return fmt.Errorf("failed to rename temp key file to storage path: %w", err)
	}

	return nil
}

func (km *Manager) loadKeys() error {
	if km.storagePath == "" {
		return os.ErrNotExist
	}

	bytes, err := os.ReadFile(km.storagePath)
	if err != nil {
		return err
	}

	var data storageStruct
	if err := json.Unmarshal(bytes, &data); err != nil {
		return fmt.Errorf("failed to unmarshal keys: %w", err)
	}

	km.currentKey = data.CurrentKey
	km.previousKeys = data.PreviousKeys
	return nil
}
