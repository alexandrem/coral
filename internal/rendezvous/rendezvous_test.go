package rendezvous

import (
	"testing"
	"time"
)

func TestDeriveKeyDeterministicAndDomainSeparated(t *testing.T) {
	k1, err := DeriveKey("coral-psk:abc123", "mesh-1")
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	k2, err := DeriveKey("coral-psk:abc123", "mesh-1")
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	if string(k1) != string(k2) {
		t.Fatal("expected deterministic key derivation for the same psk/meshID")
	}

	k3, err := DeriveKey("coral-psk:abc123", "mesh-2")
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	if string(k1) == string(k3) {
		t.Fatal("expected different mesh_id to produce a different key")
	}

	k4, err := DeriveKey("coral-psk:different", "mesh-1")
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	if string(k1) == string(k4) {
		t.Fatal("expected different psk to produce a different key")
	}
}

func TestDeriveKeyRejectsEmptyInputs(t *testing.T) {
	if _, err := DeriveKey("", "mesh-1"); err == nil {
		t.Fatal("expected error for empty psk")
	}
	if _, err := DeriveKey("coral-psk:abc", ""); err == nil {
		t.Fatal("expected error for empty mesh_id")
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	key, err := DeriveKey("coral-psk:abc123", "mesh-1")
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}

	sessionNonce, err := GenerateSessionNonce()
	if err != nil {
		t.Fatalf("GenerateSessionNonce: %v", err)
	}
	writeToken, err := GenerateWriteToken()
	if err != nil {
		t.Fatalf("GenerateWriteToken: %v", err)
	}

	payload := Payload{
		Endpoint:     "203.0.113.10:8444",
		SessionNonce: sessionNonce,
		WriteToken:   writeToken,
		ExpiresAt:    time.Now().Add(90 * time.Second).UTC(),
	}

	ciphertext, gcmNonce, err := Seal(key, payload)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if len(gcmNonce) != GCMNonceSize {
		t.Fatalf("expected gcm nonce of size %d, got %d", GCMNonceSize, len(gcmNonce))
	}

	got, err := Open(key, ciphertext, gcmNonce)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got.Endpoint != payload.Endpoint {
		t.Fatalf("endpoint mismatch: got %q want %q", got.Endpoint, payload.Endpoint)
	}
	if string(got.SessionNonce) != string(payload.SessionNonce) {
		t.Fatal("session nonce mismatch after round trip")
	}
	if string(got.WriteToken) != string(payload.WriteToken) {
		t.Fatal("write token mismatch after round trip")
	}
	if !got.ExpiresAt.Equal(payload.ExpiresAt) {
		t.Fatalf("expires_at mismatch: got %v want %v", got.ExpiresAt, payload.ExpiresAt)
	}
}

func TestSealGeneratesFreshNoncePerCall(t *testing.T) {
	key, err := DeriveKey("coral-psk:abc123", "mesh-1")
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	payload := Payload{Endpoint: "203.0.113.10:8444", ExpiresAt: time.Now()}

	_, nonce1, err := Seal(key, payload)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// A later republish changes only expires_at, but the nonce must still be
	// freshly random — reusing a GCM nonce under the same key is a hard
	// AES-GCM security violation.
	payload.ExpiresAt = payload.ExpiresAt.Add(30 * time.Second)
	_, nonce2, err := Seal(key, payload)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if string(nonce1) == string(nonce2) {
		t.Fatal("expected a fresh gcm_nonce on every Seal call")
	}
}

func TestOpenFailsWithWrongKey(t *testing.T) {
	key, err := DeriveKey("coral-psk:abc123", "mesh-1")
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	wrongKey, err := DeriveKey("coral-psk:other", "mesh-1")
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}

	payload := Payload{Endpoint: "203.0.113.10:8444", ExpiresAt: time.Now()}
	ciphertext, gcmNonce, err := Seal(key, payload)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if _, err := Open(wrongKey, ciphertext, gcmNonce); err == nil {
		t.Fatal("expected decryption to fail with the wrong key (forged/garbage record)")
	}
}

func TestOpenWithKeysTriesActiveThenGrace(t *testing.T) {
	activeKey, err := DeriveKey("coral-psk:active", "mesh-1")
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	graceKey, err := DeriveKey("coral-psk:grace", "mesh-1")
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	unrelatedKey, err := DeriveKey("coral-psk:unrelated", "mesh-1")
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}

	payload := Payload{Endpoint: "203.0.113.10:8444", ExpiresAt: time.Now()}
	ciphertext, gcmNonce, err := Seal(graceKey, payload)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	got, err := OpenWithKeys([][]byte{activeKey, graceKey}, ciphertext, gcmNonce)
	if err != nil {
		t.Fatalf("OpenWithKeys: %v", err)
	}
	if got.Endpoint != payload.Endpoint {
		t.Fatal("payload mismatch after OpenWithKeys")
	}

	if _, err := OpenWithKeys([][]byte{unrelatedKey}, ciphertext, gcmNonce); err == nil {
		t.Fatal("expected OpenWithKeys to fail when no candidate key matches")
	}
}
