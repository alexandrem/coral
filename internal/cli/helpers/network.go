package helpers

import (
	"fmt"
	"net"

	"github.com/coral-mesh/coral/internal/logging"
)

// ResolveToIPv4 resolves a hostname to an IPv4 address.
// This ensures we don't accidentally use IPv6 addresses that may cause issues.
func ResolveToIPv4(host string, logger logging.Logger) (string, error) {
	// If already an IP address, validate it's IPv4
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() != nil {
			return host, nil
		}
		return "", fmt.Errorf("address is IPv6, need IPv4")
	}

	// Resolve hostname to IP addresses
	ips, err := net.LookupIP(host)
	if err != nil {
		return "", fmt.Errorf("failed to resolve hostname: %w", err)
	}

	// Find first IPv4 address
	for _, ip := range ips {
		if ip.To4() != nil {
			logger.Debug().
				Str("hostname", host).
				Str("resolved_ipv4", ip.String()).
				Msg("Resolved hostname to IPv4")
			return ip.String(), nil
		}
	}

	return "", fmt.Errorf("no IPv4 address found for hostname %s", host)
}
