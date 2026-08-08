// Package rendezvous implements the PSK-encrypted rendezvous payload used by
// RFD 108 to let a NAT'd Colony and a dialable Agent bootstrap mTLS trust via
// Discovery as a blind relay. Discovery only ever sees AEAD ciphertext it
// cannot decrypt or forge; both sides derive the same key independently from
// the Bootstrap PSK (RFD 088).
package rendezvous

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"golang.org/x/crypto/hkdf"

	"github.com/coral-mesh/coral/internal/constants"
)

const (
	// KeySize is the derived AEAD key size (AES-256).
	KeySize = 32

	// GCMNonceSize is the AES-256-GCM nonce size in bytes.
	GCMNonceSize = 12

	// SessionNonceSize is the size, in bytes, of the application-level
	// session nonce embedded in the payload (distinct from GCMNonceSize).
	SessionNonceSize = 32

	// WriteTokenSize is the size, in bytes, of the write capability token.
	WriteTokenSize = 32
)

// Payload is the plaintext rendezvous record, AEAD-sealed with the
// PSK-derived key before being published to Discovery. See Appendix of
// RFD 108.
type Payload struct {
	// Endpoint is the dial target (ip:port) the publishing side is listening
	// on (Agent's --bootstrap-public-endpoint).
	Endpoint string `json:"endpoint"`

	// SessionNonce is an application-level session token, generated once
	// when the listener opens and stable across every republish. Later sent
	// as the Coral-Rendezvous-Nonce header. Not a cryptographic nonce.
	SessionNonce []byte `json:"session_nonce"`

	// WriteToken is the capability required to republish or ack this record
	// with Discovery. Generated once by the Agent and stable across
	// republishes; recovered by the Colony only by decrypting the record.
	WriteToken []byte `json:"write_token"`

	// ExpiresAt changes on every republish, guaranteeing a fresh plaintext
	// (and therefore requiring a fresh gcm_nonce) even though SessionNonce
	// and WriteToken stay fixed.
	ExpiresAt time.Time `json:"expires_at"`
}

// DeriveKey derives the rendezvous AEAD key from the Bootstrap PSK via
// HKDF-SHA256(psk, salt=mesh_id, info="coral-bootstrap-rendezvous-v1"). This
// is a distinct derived key from any other PSK-derived material in the
// system (see RFD 108 Key Design Decisions #2).
func DeriveKey(psk, meshID string) ([]byte, error) {
	if psk == "" {
		return nil, fmt.Errorf("bootstrap PSK is empty")
	}
	if meshID == "" {
		return nil, fmt.Errorf("mesh ID is empty")
	}
	reader := hkdf.New(sha256.New, []byte(psk), []byte(meshID), []byte(constants.RendezvousHKDFInfo))
	key := make([]byte, KeySize)
	if _, err := reader.Read(key); err != nil {
		return nil, fmt.Errorf("HKDF expansion failed: %w", err)
	}
	return key, nil
}

// GenerateSessionNonce returns a fresh random session nonce.
func GenerateSessionNonce() ([]byte, error) {
	return randomBytes(SessionNonceSize)
}

// GenerateWriteToken returns a fresh random write-capability token.
func GenerateWriteToken() ([]byte, error) {
	return randomBytes(WriteTokenSize)
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return b, nil
}

// Seal encrypts payload with AES-256-GCM under key, generating a fresh
// random 96-bit GCM nonce. A fresh nonce MUST be generated for every single
// call, including every republish of "the same" logical record — reusing a
// GCM nonce with the same key breaks AES-GCM's security guarantees.
func Seal(key []byte, payload Payload) (ciphertext, gcmNonce []byte, err error) {
	plaintext, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal rendezvous payload: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	gcmNonce, err = randomBytes(gcm.NonceSize())
	if err != nil {
		return nil, nil, err
	}

	ciphertext = gcm.Seal(nil, gcmNonce, plaintext, nil)
	return ciphertext, gcmNonce, nil
}

// Open decrypts and unmarshals a rendezvous record with key. A failed
// authentication tag (wrong PSK, corrupted/forged ciphertext) returns an
// error — the caller should discard the record, not treat this as fatal.
func Open(key, ciphertext, gcmNonce []byte) (Payload, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return Payload{}, fmt.Errorf("failed to create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Payload{}, fmt.Errorf("failed to create GCM: %w", err)
	}

	plaintext, err := gcm.Open(nil, gcmNonce, ciphertext, nil)
	if err != nil {
		return Payload{}, fmt.Errorf("failed to decrypt rendezvous payload: %w", err)
	}

	var payload Payload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return Payload{}, fmt.Errorf("failed to unmarshal rendezvous payload: %w", err)
	}
	return payload, nil
}

// OpenWithKeys tries Open against each key in order, returning the first
// successful decryption. This supports PSK rotation: during the grace
// period the Colony holds both the active and grace HKDF-derived keys (RFD
// 088's existing dual-acceptance pattern).
func OpenWithKeys(keys [][]byte, ciphertext, gcmNonce []byte) (Payload, error) {
	var lastErr error
	for _, key := range keys {
		payload, err := Open(key, ciphertext, gcmNonce)
		if err == nil {
			return payload, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no keys provided")
	}
	return Payload{}, fmt.Errorf("failed to decrypt with any known key: %w", lastErr)
}
