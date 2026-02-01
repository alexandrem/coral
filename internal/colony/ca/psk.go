// Package ca provides certificate authority management for mTLS.
package ca

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/hkdf"
)

const (
	// pskPrefix identifies a Coral bootstrap PSK.
	pskPrefix = "coral-psk:"

	// pskEntropyBytes is the number of random bytes in a PSK (256 bits).
	pskEntropyBytes = 32

	// pskHKDFInfo is the HKDF info string for PSK encryption key derivation.
	pskHKDFInfo = "coral-psk-encryption"

	// pskFileName is the filename for the encrypted PSK stored on the filesystem.
	pskFileName = "bootstrap-psk.enc"
)

// PSKFile represents the encrypted PSK stored on the filesystem during init.
// It is imported into DuckDB on first colony start.
type PSKFile struct {
	EncryptedPSK []byte `json:"encrypted_psk"`
	Nonce        []byte `json:"nonce"`
}

// GeneratePSK creates a new bootstrap PSK with 256 bits of entropy.
// Returns the PSK as a string with the coral-psk: prefix.
func GeneratePSK() (string, error) {
	b := make([]byte, pskEntropyBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate PSK entropy: %w", err)
	}
	return pskPrefix + hex.EncodeToString(b), nil
}

// ValidatePSKFormat checks that a PSK string has the correct format.
func ValidatePSKFormat(psk string) error {
	if len(psk) <= len(pskPrefix) {
		return fmt.Errorf("PSK too short")
	}
	if psk[:len(pskPrefix)] != pskPrefix {
		return fmt.Errorf("PSK must start with %q", pskPrefix)
	}
	hexPart := psk[len(pskPrefix):]
	if len(hexPart) != pskEntropyBytes*2 {
		return fmt.Errorf("PSK hex part must be %d characters, got %d", pskEntropyBytes*2, len(hexPart))
	}
	if _, err := hex.DecodeString(hexPart); err != nil {
		return fmt.Errorf("PSK contains invalid hex: %w", err)
	}
	return nil
}

// DeriveEncryptionKey derives a 32-byte AES-256 key from an ECDSA private key
// using HKDF-SHA256. The key material is the raw elliptic curve D value.
func DeriveEncryptionKey(rootKey *ecdsa.PrivateKey) ([]byte, error) {
	if rootKey == nil {
		return nil, fmt.Errorf("root key is nil")
	}

	// Use the raw private key scalar as input key material.
	ikm := rootKey.D.Bytes()
	// Pad to the full curve byte length for consistency.
	curveSize := (rootKey.Curve.Params().BitSize + 7) / 8
	if len(ikm) < curveSize {
		padded := make([]byte, curveSize)
		copy(padded[curveSize-len(ikm):], ikm)
		ikm = padded
	}

	// HKDF-SHA256 with no salt and info string.
	hkdfReader := hkdf.New(sha256.New, ikm, nil, []byte(pskHKDFInfo))
	key := make([]byte, 32)
	if _, err := hkdfReader.Read(key); err != nil {
		return nil, fmt.Errorf("HKDF expansion failed: %w", err)
	}
	return key, nil
}

// EncryptPSK encrypts a PSK plaintext with AES-256-GCM.
func EncryptPSK(plaintext string, key []byte) (ciphertext, nonce []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext = gcm.Seal(nil, nonce, []byte(plaintext), nil)
	return ciphertext, nonce, nil
}

// DecryptPSK decrypts an AES-256-GCM encrypted PSK.
func DecryptPSK(ciphertext, nonce, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt PSK: %w", err)
	}

	return string(plaintext), nil
}

// PSKConstantTimeEqual compares two PSK strings in constant time.
func PSKConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// SavePSKToFile encrypts and saves a PSK to the CA directory filesystem.
// Used during colony init (before database is available).
func SavePSKToFile(caDir string, psk string, rootKey *ecdsa.PrivateKey) error {
	encKey, err := DeriveEncryptionKey(rootKey)
	if err != nil {
		return fmt.Errorf("failed to derive encryption key: %w", err)
	}

	ciphertext, nonce, err := EncryptPSK(psk, encKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt PSK: %w", err)
	}

	pskFile := PSKFile{
		EncryptedPSK: ciphertext,
		Nonce:        nonce,
	}

	data, err := json.Marshal(pskFile)
	if err != nil {
		return fmt.Errorf("failed to marshal PSK file: %w", err)
	}

	path := filepath.Join(caDir, pskFileName)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write PSK file: %w", err)
	}

	return nil
}

// LoadPSKFromFile loads and decrypts a PSK from the CA directory filesystem.
func LoadPSKFromFile(caDir string, rootKey *ecdsa.PrivateKey) (string, error) {
	path := filepath.Join(caDir, pskFileName)
	//nolint:gosec // G304: Path is constructed from trusted CA directory.
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read PSK file: %w", err)
	}

	var pskFile PSKFile
	if err := json.Unmarshal(data, &pskFile); err != nil {
		return "", fmt.Errorf("failed to unmarshal PSK file: %w", err)
	}

	encKey, err := DeriveEncryptionKey(rootKey)
	if err != nil {
		return "", fmt.Errorf("failed to derive encryption key: %w", err)
	}

	psk, err := DecryptPSK(pskFile.EncryptedPSK, pskFile.Nonce, encKey)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt PSK: %w", err)
	}

	return psk, nil
}

// PSKFileExists checks whether a PSK file exists in the CA directory.
func PSKFileExists(caDir string) bool {
	_, err := os.Stat(filepath.Join(caDir, pskFileName))
	return err == nil
}

// generateECDSAKey generates a new P-256 ECDSA key for testing.
func generateECDSAKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}
