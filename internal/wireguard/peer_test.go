package wireguard

import (
	"testing"
)

const testPublicKey = "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI="

func TestParsePeerConfig_Valid(t *testing.T) {
	validConfig := &PeerConfig{
		PublicKey:           testPublicKey,
		Endpoint:            "192.168.1.100:51820",
		AllowedIPs:          []string{"100.64.0.15/32"},
		PersistentKeepalive: 25,
	}

	err := ParsePeerConfig(validConfig)
	if err != nil {
		t.Errorf("expected valid config to pass, got error: %v", err)
	}
}

func TestParsePeerConfig_NilConfig(t *testing.T) {
	err := ParsePeerConfig(nil)
	if err == nil {
		t.Error("expected error for nil config")
	}
}

func TestParsePeerConfig_EmptyPublicKey(t *testing.T) {
	config := &PeerConfig{
		PublicKey:  "",
		AllowedIPs: []string{"100.64.0.15/32"},
	}

	err := ParsePeerConfig(config)
	if err == nil {
		t.Error("expected error for empty public key")
	}
}

func TestParsePeerConfig_InvalidPublicKeyLength(t *testing.T) {
	config := &PeerConfig{
		PublicKey:  "too-short",
		AllowedIPs: []string{"100.64.0.15/32"},
	}

	err := ParsePeerConfig(config)
	if err == nil {
		t.Error("expected error for invalid public key length")
	}
}

func TestParsePeerConfig_EmptyAllowedIPs(t *testing.T) {
	config := &PeerConfig{
		PublicKey:  testPublicKey,
		AllowedIPs: []string{},
	}

	err := ParsePeerConfig(config)
	if err == nil {
		t.Error("expected error for empty allowed IPs")
	}
}

func TestParsePeerConfig_InvalidAllowedIP(t *testing.T) {
	config := &PeerConfig{
		PublicKey:  testPublicKey,
		AllowedIPs: []string{"invalid-ip"},
	}

	err := ParsePeerConfig(config)
	if err == nil {
		t.Error("expected error for invalid allowed IP")
	}
}

func TestParsePeerConfig_ValidAllowedIPWithoutCIDR(t *testing.T) {
	config := &PeerConfig{
		PublicKey:  testPublicKey,
		AllowedIPs: []string{"100.64.0.15"},
	}

	err := ParsePeerConfig(config)
	if err != nil {
		t.Errorf("expected single IP without CIDR to be valid, got error: %v", err)
	}
}

func TestParsePeerConfig_InvalidEndpoint(t *testing.T) {
	config := &PeerConfig{
		PublicKey:  testPublicKey,
		AllowedIPs: []string{"100.64.0.15/32"},
		Endpoint:   "invalid-endpoint",
	}

	err := ParsePeerConfig(config)
	if err == nil {
		t.Error("expected error for invalid endpoint")
	}
}

func TestParsePeerConfig_NoEndpoint(t *testing.T) {
	config := &PeerConfig{
		PublicKey:  testPublicKey,
		AllowedIPs: []string{"100.64.0.15/32"},
		Endpoint:   "",
	}

	err := ParsePeerConfig(config)
	if err != nil {
		t.Errorf("expected config without endpoint to be valid, got error: %v", err)
	}
}

func TestParsePeerConfig_NegativeKeepalive(t *testing.T) {
	config := &PeerConfig{
		PublicKey:           testPublicKey,
		AllowedIPs:          []string{"100.64.0.15/32"},
		PersistentKeepalive: -1,
	}

	err := ParsePeerConfig(config)
	if err == nil {
		t.Error("expected error for negative keepalive")
	}
}

func TestPeerConfig_AllowedIPsString(t *testing.T) {
	config := &PeerConfig{
		PublicKey:  testPublicKey,
		AllowedIPs: []string{"100.64.0.15/32", "100.64.0.16/32", "fd42::1/128"},
	}

	result := config.AllowedIPsString()
	expected := "100.64.0.15/32,100.64.0.16/32,fd42::1/128"

	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestPeerConfig_AllowedIPsString_SingleIP(t *testing.T) {
	config := &PeerConfig{
		PublicKey:  testPublicKey,
		AllowedIPs: []string{"100.64.0.15/32"},
	}

	result := config.AllowedIPsString()
	expected := "100.64.0.15/32"

	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}
