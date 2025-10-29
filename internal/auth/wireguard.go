package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/curve25519"
)

// WireGuardKeyPair represents a WireGuard private/public key pair.
type WireGuardKeyPair struct {
	PrivateKey string // Base64-encoded private key
	PublicKey  string // Base64-encoded public key
}

// GenerateWireGuardKeyPair creates a new WireGuard key pair.
// Uses Curve25519 for key generation.
func GenerateWireGuardKeyPair() (*WireGuardKeyPair, error) {
	// Generate 32 random bytes for private key
	privateKeyBytes := make([]byte, 32)
	if _, err := rand.Read(privateKeyBytes); err != nil {
		return nil, fmt.Errorf("failed to generate WireGuard private key: %w", err)
	}

	// Clamp the private key (WireGuard requirement)
	privateKeyBytes[0] &= 248
	privateKeyBytes[31] &= 127
	privateKeyBytes[31] |= 64

	// Derive public key using X25519
	publicKeyBytes, err := curve25519.X25519(privateKeyBytes, curve25519.Basepoint)
	if err != nil {
		return nil, fmt.Errorf("failed to derive WireGuard public key: %w", err)
	}

	// Encode keys to base64
	privateKey := base64.StdEncoding.EncodeToString(privateKeyBytes)
	publicKey := base64.StdEncoding.EncodeToString(publicKeyBytes)

	return &WireGuardKeyPair{
		PrivateKey: privateKey,
		PublicKey:  publicKey,
	}, nil
}

// ValidateWireGuardKey checks if a base64-encoded key is valid (32 bytes).
func ValidateWireGuardKey(keyBase64 string) error {
	keyBytes, err := base64.StdEncoding.DecodeString(keyBase64)
	if err != nil {
		return fmt.Errorf("invalid base64 encoding: %w", err)
	}

	if len(keyBytes) != 32 {
		return fmt.Errorf("WireGuard key must be 32 bytes, got %d", len(keyBytes))
	}

	return nil
}
