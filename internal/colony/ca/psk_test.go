package ca

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratePSK(t *testing.T) {
	psk, err := GeneratePSK()
	require.NoError(t, err)
	assert.True(t, len(psk) > len(pskPrefix))
	assert.Equal(t, pskPrefix, psk[:len(pskPrefix)])
	assert.NoError(t, ValidatePSKFormat(psk))
}

func TestGeneratePSK_Uniqueness(t *testing.T) {
	psk1, err := GeneratePSK()
	require.NoError(t, err)
	psk2, err := GeneratePSK()
	require.NoError(t, err)
	assert.NotEqual(t, psk1, psk2)
}

func TestValidatePSKFormat(t *testing.T) {
	tests := []struct {
		name    string
		psk     string
		wantErr bool
	}{
		{"valid", "coral-psk:" + "ab" + string(make([]byte, 62)), true}, // wrong length
		{"valid_real", "coral-psk:a3f2e1d4c5b6a7f8e9d0c1b2a3f4e5d6c7b8a9f0e1d2c3b4a5f6e7d8c9b0a1f2", false},
		{"no_prefix", "a3f2e1d4c5b6a7f8e9d0c1b2a3f4e5d6", true},
		{"empty", "", true},
		{"prefix_only", "coral-psk:", true},
		{"wrong_hex_length", "coral-psk:abcd", true},
		{"invalid_hex", "coral-psk:" + "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePSKFormat(tt.psk)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestEncryptDecryptPSK_Roundtrip(t *testing.T) {
	key, err := generateECDSAKey()
	require.NoError(t, err)

	encKey, err := DeriveEncryptionKey(key)
	require.NoError(t, err)
	assert.Len(t, encKey, 32)

	psk, err := GeneratePSK()
	require.NoError(t, err)

	ciphertext, nonce, err := EncryptPSK(psk, encKey)
	require.NoError(t, err)
	assert.NotEmpty(t, ciphertext)
	assert.Len(t, nonce, 12) // GCM nonce size.

	decrypted, err := DecryptPSK(ciphertext, nonce, encKey)
	require.NoError(t, err)
	assert.Equal(t, psk, decrypted)
}

func TestDecryptPSK_WrongKey(t *testing.T) {
	key1, err := generateECDSAKey()
	require.NoError(t, err)
	key2, err := generateECDSAKey()
	require.NoError(t, err)

	encKey1, _ := DeriveEncryptionKey(key1)
	encKey2, _ := DeriveEncryptionKey(key2)

	ciphertext, nonce, err := EncryptPSK("coral-psk:test", encKey1)
	require.NoError(t, err)

	_, err = DecryptPSK(ciphertext, nonce, encKey2)
	assert.Error(t, err)
}

func TestDeriveEncryptionKey_Deterministic(t *testing.T) {
	key, err := generateECDSAKey()
	require.NoError(t, err)

	k1, err := DeriveEncryptionKey(key)
	require.NoError(t, err)
	k2, err := DeriveEncryptionKey(key)
	require.NoError(t, err)
	assert.Equal(t, k1, k2)
}

func TestDeriveEncryptionKey_NilKey(t *testing.T) {
	_, err := DeriveEncryptionKey(nil)
	assert.Error(t, err)
}

func TestPSKConstantTimeEqual(t *testing.T) {
	assert.True(t, PSKConstantTimeEqual("abc", "abc"))
	assert.False(t, PSKConstantTimeEqual("abc", "def"))
	assert.False(t, PSKConstantTimeEqual("abc", "abcd"))
}

func TestSaveLoadPSKFile(t *testing.T) {
	tmpDir := t.TempDir()
	key, err := generateECDSAKey()
	require.NoError(t, err)

	psk, err := GeneratePSK()
	require.NoError(t, err)

	err = SavePSKToFile(tmpDir, psk, key)
	require.NoError(t, err)

	assert.True(t, PSKFileExists(tmpDir))
	assert.FileExists(t, filepath.Join(tmpDir, pskFileName))

	loaded, err := LoadPSKFromFile(tmpDir, key)
	require.NoError(t, err)
	assert.Equal(t, psk, loaded)
}

func TestLoadPSKFromFile_MissingFile(t *testing.T) {
	key, err := generateECDSAKey()
	require.NoError(t, err)
	_, err = LoadPSKFromFile(t.TempDir(), key)
	assert.Error(t, err)
}

func TestPSKFileExists_NoFile(t *testing.T) {
	assert.False(t, PSKFileExists(t.TempDir()))
}

func TestSavePSKToFile_Permissions(t *testing.T) {
	tmpDir := t.TempDir()
	key, err := generateECDSAKey()
	require.NoError(t, err)

	psk, err := GeneratePSK()
	require.NoError(t, err)

	err = SavePSKToFile(tmpDir, psk, key)
	require.NoError(t, err)

	info, err := os.Stat(filepath.Join(tmpDir, pskFileName))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}
