package wireguard

import (
	"fmt"
	"net"
	"net/netip"
)

// PeerConfig contains the configuration for a WireGuard peer.
type PeerConfig struct {
	// PublicKey is the base64-encoded Curve25519 public key of the peer.
	PublicKey string

	// Endpoint is the peer's network endpoint (host:port).
	// Optional for dynamic peers (they'll connect to us).
	Endpoint string

	// AllowedIPs is the list of IP addresses/subnets allowed for this peer.
	AllowedIPs []string

	// PersistentKeepalive is the interval in seconds to send keepalive packets.
	// Zero means disabled. Used for NAT traversal.
	PersistentKeepalive int
}

// ParsePeerConfig validates and normalizes a peer configuration.
func ParsePeerConfig(config *PeerConfig) error {
	if config == nil {
		return fmt.Errorf("peer config is nil")
	}

	if config.PublicKey == "" {
		return fmt.Errorf("public key is required")
	}

	// Validate public key length (base64-encoded 32-byte Curve25519 key)
	if len(config.PublicKey) != 44 {
		return fmt.Errorf("invalid public key length: expected 44 characters, got %d", len(config.PublicKey))
	}

	// Validate allowed IPs
	if len(config.AllowedIPs) == 0 {
		return fmt.Errorf("at least one allowed IP is required")
	}

	for _, allowedIP := range config.AllowedIPs {
		if _, err := netip.ParsePrefix(allowedIP); err != nil {
			// Try parsing as a single IP
			if _, err := netip.ParseAddr(allowedIP); err != nil {
				return fmt.Errorf("invalid allowed IP %q: %w", allowedIP, err)
			}
		}
	}

	// Validate and resolve endpoint if provided.
	// WireGuard requires IP:port format, not hostname:port.
	if config.Endpoint != "" {
		host, port, err := net.SplitHostPort(config.Endpoint)
		if err != nil {
			return fmt.Errorf("invalid endpoint %q: %w", config.Endpoint, err)
		}
		if host == "" {
			return fmt.Errorf("endpoint host is empty")
		}
		if port == "" {
			return fmt.Errorf("endpoint port is empty")
		}

		// Check if host is already an IP address.
		if net.ParseIP(host) == nil {
			// Host is a hostname - resolve it to an IP address.
			ips, err := net.LookupIP(host)
			if err != nil {
				return fmt.Errorf("failed to resolve endpoint hostname %q: %w", host, err)
			}
			if len(ips) == 0 {
				return fmt.Errorf("no IP addresses found for hostname %q", host)
			}

			// Use the first IP (prefer IPv4).
			var selectedIP net.IP
			for _, ip := range ips {
				if ip.To4() != nil {
					selectedIP = ip
					break
				}
			}
			if selectedIP == nil {
				selectedIP = ips[0] // Fallback to first IP (might be IPv6)
			}

			// Update endpoint with resolved IP.
			config.Endpoint = net.JoinHostPort(selectedIP.String(), port)
		}
	}

	// Validate persistent keepalive
	if config.PersistentKeepalive < 0 {
		return fmt.Errorf("persistent keepalive must be non-negative, got %d", config.PersistentKeepalive)
	}

	return nil
}

// AllowedIPsString returns the allowed IPs as a comma-separated string.
func (p *PeerConfig) AllowedIPsString() string {
	result := ""
	for i, ip := range p.AllowedIPs {
		if i > 0 {
			result += ","
		}
		result += ip
	}
	return result
}
