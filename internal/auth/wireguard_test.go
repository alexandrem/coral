package auth

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateWireGuardKeyPair(t *testing.T) {
	keypair, err := GenerateWireGuardKeyPair()
	require.NoError(t, err)
	require.NotNil(t, keypair)

	// Verify private key
	assert.NotEmpty(t, keypair.PrivateKey)
	privateBytes, err := base64.StdEncoding.DecodeString(keypair.PrivateKey)
	require.NoError(t, err, "private key should be valid base64")
	assert.Len(t, privateBytes, 32, "private key should be 32 bytes")

	// Verify public key
	assert.NotEmpty(t, keypair.PublicKey)
	publicBytes, err := base64.StdEncoding.DecodeString(keypair.PublicKey)
	require.NoError(t, err, "public key should be valid base64")
	assert.Len(t, publicBytes, 32, "public key should be 32 bytes")

	// Verify clamping on private key (WireGuard requirement)
	assert.Equal(t, byte(0), privateBytes[0]&7, "private key bit 0-2 should be cleared")
	assert.Equal(t, byte(0), privateBytes[31]&128, "private key bit 255 should be cleared")
	assert.Equal(t, byte(64), privateBytes[31]&64, "private key bit 254 should be set")
}

func TestGenerateWireGuardKeyPair_Uniqueness(t *testing.T) {
	// Generate multiple key pairs and ensure they're unique
	privateKeys := make(map[string]bool)
	publicKeys := make(map[string]bool)

	for i := 0; i < 100; i++ {
		keypair, err := GenerateWireGuardKeyPair()
		require.NoError(t, err)

		assert.False(t, privateKeys[keypair.PrivateKey], "generated duplicate private key")
		assert.False(t, publicKeys[keypair.PublicKey], "generated duplicate public key")

		privateKeys[keypair.PrivateKey] = true
		publicKeys[keypair.PublicKey] = true
	}
}

func TestValidateWireGuardKey(t *testing.T) {
	// Generate a valid key pair
	keypair, err := GenerateWireGuardKeyPair()
	require.NoError(t, err)

	// Valid keys should pass
	err = ValidateWireGuardKey(keypair.PrivateKey)
	assert.NoError(t, err, "valid private key should pass validation")

	err = ValidateWireGuardKey(keypair.PublicKey)
	assert.NoError(t, err, "valid public key should pass validation")

	// Invalid base64 should fail
	err = ValidateWireGuardKey("not-base64!!!")
	assert.Error(t, err, "invalid base64 should fail")

	// Wrong length should fail (31 bytes instead of 32)
	shortKey := base64.StdEncoding.EncodeToString(make([]byte, 31))
	err = ValidateWireGuardKey(shortKey)
	assert.Error(t, err, "key with wrong length should fail")

	// Wrong length should fail (33 bytes instead of 32)
	longKey := base64.StdEncoding.EncodeToString(make([]byte, 33))
	err = ValidateWireGuardKey(longKey)
	assert.Error(t, err, "key with wrong length should fail")
}
