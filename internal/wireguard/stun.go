package wireguard

import (
	"fmt"
	"net"
	"time"

	"github.com/pion/stun"

	discoveryv1 "github.com/coral-mesh/coral/coral/discovery/v1"
	"github.com/coral-mesh/coral/internal/logging"
)

// DiscoverPublicEndpoint uses STUN to discover the public IP and port.
// Returns nil if discovery fails (not behind NAT or STUN servers unavailable).
func DiscoverPublicEndpoint(stunServers []string, localPort int, logger logging.Logger) *discoveryv1.Endpoint {
	if len(stunServers) == 0 {
		logger.Debug().Msg("No STUN servers configured, skipping public endpoint discovery")
		return nil
	}

	// Create a UDP connection bound to the local WireGuard port.
	// STUN runs before WireGuard starts, so we bind to the port exclusively.
	// The socket is closed before WireGuard is initialized.
	localAddr := &net.UDPAddr{
		IP:   net.IPv4zero,
		Port: localPort,
	}

	conn, err := net.ListenUDP("udp4", localAddr)
	if err != nil {
		logger.Warn().Err(err).Msg("Failed to create UDP connection for STUN")
		return nil
	}
	defer func() { _ = conn.Close() }() // TODO: errcheck

	// Try each STUN server with individual timeouts
	for _, stunServer := range stunServers {
		logger.Debug().
			Str("stun_server", stunServer).
			Int("local_port", localPort).
			Msg("Attempting STUN discovery")

		// Set a fresh deadline for each attempt (3 seconds per server)
		deadline := time.Now().Add(3 * time.Second)
		_ = conn.SetReadDeadline(deadline) // TODO: errcheck

		endpoint, err := querySTUNServer(conn, stunServer, logger)
		if err != nil {
			logger.Warn().
				Err(err).
				Str("stun_server", stunServer).
				Msg("STUN query failed")
			continue
		}

		if endpoint != nil {
			logger.Info().
				Str("stun_server", stunServer).
				Str("public_ip", endpoint.Ip).
				Uint32("public_port", endpoint.Port).
				Msg("Discovered public endpoint via STUN")
			return endpoint
		}
	}

	logger.Warn().Msg("Failed to discover public endpoint from any STUN server")
	return nil
}

// querySTUNServer sends a STUN binding request to the specified server.
func querySTUNServer(conn *net.UDPConn, stunServer string, logger logging.Logger) (*discoveryv1.Endpoint, error) {
	// Resolve STUN server address
	logger.Debug().Str("stun_server", stunServer).Msg("Resolving STUN server address")
	serverAddr, err := net.ResolveUDPAddr("udp4", stunServer)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve STUN server: %w", err)
	}
	logger.Debug().Str("resolved_addr", serverAddr.String()).Msg("STUN server address resolved")

	// Create STUN binding request
	message := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
	logger.Debug().Int("request_size", len(message.Raw)).Msg("Created STUN binding request")

	// Send request
	n, err := conn.WriteToUDP(message.Raw, serverAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to send STUN request: %w", err)
	}
	logger.Debug().Int("bytes_sent", n).Msg("Sent STUN request")

	// Read response
	logger.Debug().Msg("Waiting for STUN response...")
	buf := make([]byte, 1024)
	n, fromAddr, err := conn.ReadFromUDP(buf)
	if err != nil {
		logger.Debug().Err(err).Msg("Read error occurred")
		return nil, fmt.Errorf("failed to read STUN response: %w", err)
	}
	logger.Debug().
		Int("bytes_received", n).
		Str("from_addr", fromAddr.String()).
		Msg("Received STUN response")

	// Parse STUN response
	var stunResp stun.Message
	stunResp.Raw = buf[:n]
	if err := stunResp.Decode(); err != nil {
		return nil, fmt.Errorf("failed to decode STUN response: %w", err)
	}

	// Extract XOR-MAPPED-ADDRESS
	var xorAddr stun.XORMappedAddress
	if err := xorAddr.GetFrom(&stunResp); err != nil {
		return nil, fmt.Errorf("failed to get XOR-MAPPED-ADDRESS: %w", err)
	}

	return &discoveryv1.Endpoint{
		Ip:       xorAddr.IP.String(),
		Port:     uint32(xorAddr.Port),
		Protocol: "udp",
		ViaRelay: false,
	}, nil
}

// ClassifyNAT attempts to determine the NAT type using STUN.
// This is a simplified classification - full RFC 5780 classification would require more tests.
// STUN runs before WireGuard starts, so we bind to the port exclusively.
func ClassifyNAT(stunServers []string, localPort int, logger logging.Logger) discoveryv1.NatHint {
	if len(stunServers) < 2 {
		logger.Debug().Msg("Need at least 2 STUN servers for NAT classification")
		return discoveryv1.NatHint_NAT_UNKNOWN
	}

	// Create UDP connection bound to the WireGuard port (before WireGuard starts).
	localAddr := &net.UDPAddr{
		IP:   net.IPv4zero,
		Port: localPort,
	}

	conn, err := net.ListenUDP("udp4", localAddr)
	if err != nil {
		logger.Warn().Err(err).Msg("Failed to create UDP connection for NAT classification")
		return discoveryv1.NatHint_NAT_UNKNOWN
	}
	defer func() { _ = conn.Close() }() // TODO: errcheck

	// Query first STUN server with timeout
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second)) // TODO: errcheck
	endpoint1, err := querySTUNServer(conn, stunServers[0], logger)
	if err != nil || endpoint1 == nil {
		logger.Debug().Err(err).Msg("Failed to query first STUN server for NAT classification")
		return discoveryv1.NatHint_NAT_UNKNOWN
	}

	// Query second STUN server with fresh timeout
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second)) // TODO: errcheck
	endpoint2, err := querySTUNServer(conn, stunServers[1], logger)
	if err != nil || endpoint2 == nil {
		logger.Debug().Err(err).Msg("Failed to query second STUN server for NAT classification")
		return discoveryv1.NatHint_NAT_UNKNOWN
	}

	// Simple heuristic: if both servers see the same port, likely cone NAT
	// If they see different ports, likely symmetric NAT
	if endpoint1.Port == endpoint2.Port {
		logger.Info().
			Uint32("observed_port", endpoint1.Port).
			Msg("NAT classification: Cone NAT (same port observed by different servers)")
		return discoveryv1.NatHint_NAT_CONE
	}

	logger.Info().
		Uint32("port1", endpoint1.Port).
		Uint32("port2", endpoint2.Port).
		Msg("NAT classification: Symmetric NAT (different ports observed)")
	return discoveryv1.NatHint_NAT_SYMMETRIC
}
