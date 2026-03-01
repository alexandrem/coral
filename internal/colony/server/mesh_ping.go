package server

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"connectrpc.com/connect"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/constants"
	"github.com/coral-mesh/coral/internal/safe"
	"github.com/coral-mesh/coral/internal/wireguard"
)

// MeshPing handles mesh troubleshooting pings from colony to agents (RFD 097).
func (s *Server) MeshPing(
	ctx context.Context,
	req *connect.Request[colonyv1.MeshPingRequest],
) (*connect.Response[colonyv1.MeshPingResponse], error) {
	s.logger.Info().
		Str("agent_id", req.Msg.AgentId).
		Int32("count", req.Msg.Count).
		Int32("timeout_ms", req.Msg.TimeoutMs).
		Msg("Mesh ping request received")

	// 1. Identify target agents.
	type pingTarget struct {
		id string
		ip string
	}
	var targets []pingTarget

	if req.Msg.AgentId != "" {
		entry, err := s.registry.Get(req.Msg.AgentId)
		if err != nil {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("agent %s not found", req.Msg.AgentId))
		}
		targets = append(targets, pingTarget{id: entry.AgentID, ip: entry.MeshIPv4})
	} else {
		all := s.registry.ListAll()
		for _, e := range all {
			targets = append(targets, pingTarget{id: e.AgentID, ip: e.MeshIPv4})
		}
	}

	if len(targets) == 0 {
		return connect.NewResponse(&colonyv1.MeshPingResponse{}), nil
	}

	// 2. Set defaults.
	count := int(req.Msg.Count)
	if count <= 0 {
		count = 3
	}
	timeout := time.Duration(req.Msg.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 1 * time.Second
	}

	// 3. Perform pings concurrently.
	var wg sync.WaitGroup
	results := make([]*colonyv1.MeshPingResponse_AgentPingResult, len(targets))

	for i, target := range targets {
		wg.Add(1)
		go func(idx int, id, ip string) {
			defer wg.Done()
			results[idx] = s.pingAgent(ctx, id, ip, count, timeout)
		}(i, target.id, target.ip)
	}

	wg.Wait()

	return connect.NewResponse(&colonyv1.MeshPingResponse{
		Results: results,
	}), nil
}

func (s *Server) pingAgent(ctx context.Context, agentID, meshIP string, count int, timeout time.Duration) *colonyv1.MeshPingResponse_AgentPingResult {
	result := &colonyv1.MeshPingResponse_AgentPingResult{
		AgentId: agentID,
		MeshIp:  meshIP,
	}

	if meshIP == "" {
		result.Error = "No mesh IP assigned"
		return result
	}

	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", meshIP, constants.DefaultMeshPingPort))
	if err != nil {
		result.Error = fmt.Sprintf("Failed to resolve address: %v", err)
		return result
	}

	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		result.Error = fmt.Sprintf("Failed to open UDP socket: %v", err)
		return result
	}
	defer safe.Close(conn, s.logger, "failed to close socket")

	buffer := make([]byte, 1024)
	var minRTT = 100 * time.Second // sentinel; replaced on first received packet
	var maxRTT time.Duration
	var totalRTT time.Duration
	received := 0

	for i := 0; i < count; i++ {
		select {
		case <-ctx.Done():
			result.Error = "Context cancelled"
			return result
		default:
		}

		msg := fmt.Sprintf("PING %d %d", i, time.Now().UnixNano())
		_ = conn.SetDeadline(time.Now().Add(timeout))
		start := time.Now()
		_, err := conn.Write([]byte(msg))
		if err != nil {
			continue
		}

		_, err = conn.Read(buffer)
		if err != nil {
			continue
		}

		rtt := time.Since(start)
		received++
		totalRTT += rtt
		if rtt < minRTT {
			minRTT = rtt
		}
		if rtt > maxRTT {
			maxRTT = rtt
		}

		if i < count-1 {
			// Short inter-ping delay; respect context cancellation.
			select {
			case <-ctx.Done():
				result.Error = "Context cancelled"
				return result
			case <-time.After(100 * time.Millisecond):
			}
		}
	}

	result.Sent = int32(count)
	result.Received = int32(received)
	if count > 0 {
		result.PacketLossPercentage = 100.0 * float64(count-received) / float64(count)
	}

	if received > 0 {
		result.MinRttMs = float64(minRTT.Microseconds()) / 1000.0
		result.MaxRttMs = float64(maxRTT.Microseconds()) / 1000.0
		result.AvgRttMs = float64((totalRTT / time.Duration(received)).Microseconds()) / 1000.0
	}

	return result
}

// MeshAudit audits the WireGuard mesh topology by comparing Colony's live UAPI observations
// against agent-announced STUN endpoints at registration.
func (s *Server) MeshAudit(
	ctx context.Context,
	req *connect.Request[colonyv1.MeshAuditRequest],
) (*connect.Response[colonyv1.MeshAuditResponse], error) {
	if s.wgStatsProvider == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("WireGuard stats not available"))
	}

	// Fetch live WireGuard stats and configured peer list.
	deviceStats, peers := s.wgStatsProvider()

	// Build meshIP → PeerConfig lookup.
	// PeerConfig.AllowedIPs[0] is the mesh CIDR "meshIP/32".
	peerByMeshIP := buildPeerByMeshIP(peers)

	// Build pubkey → PeerStats lookup from live UAPI.
	var statsByPubkey map[string]*wireguard.PeerStats
	if deviceStats != nil {
		statsByPubkey = deviceStats.Peers
	}

	// Identify target registry entries.
	var entries []*registryEntry
	if req.Msg.AgentId != "" {
		entry, err := s.registry.Get(req.Msg.AgentId)
		if err != nil {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("agent %s not found", req.Msg.AgentId))
		}
		entries = []*registryEntry{{id: entry.AgentID, meshIP: entry.MeshIPv4}}
	} else {
		for _, e := range s.registry.ListAll() {
			entries = append(entries, &registryEntry{id: e.AgentID, meshIP: e.MeshIPv4})
		}
	}

	results := make([]*colonyv1.MeshAuditAgentResult, 0, len(entries))
	for _, e := range entries {
		result := &colonyv1.MeshAuditAgentResult{
			AgentId:             e.id,
			MeshIp:              e.meshIP,
			HandshakeAgeSeconds: -1,
		}

		peerCfg, ok := peerByMeshIP[e.meshIP]
		if !ok {
			result.Error = "not found in WireGuard peer list"
			result.NatType = "error"
			results = append(results, result)
			continue
		}

		result.AgentRegisteredEndpoint = peerCfg.Endpoint

		if ps, found := statsByPubkey[peerCfg.PublicKey]; found {
			result.ColonyObservedEndpoint = ps.Endpoint
			result.RxBytes = ps.RxBytes
			result.TxBytes = ps.TxBytes
			if !ps.LastHandshakeTime.IsZero() {
				result.HandshakeAgeSeconds = int64(time.Since(ps.LastHandshakeTime).Seconds())
			}
		}

		result.NatType = assessNAT(result.AgentRegisteredEndpoint, result.ColonyObservedEndpoint, result.HandshakeAgeSeconds)
		results = append(results, result)
	}

	return connect.NewResponse(&colonyv1.MeshAuditResponse{Results: results}), nil
}

// registryEntry is a minimal view of a registry Entry used within the audit.
type registryEntry struct {
	id     string
	meshIP string
}

// buildPeerByMeshIP maps each peer's mesh IP to its PeerConfig.
// The mesh IP is derived by stripping the /32 prefix from AllowedIPs[0].
func buildPeerByMeshIP(peers []*wireguard.PeerConfig) map[string]*wireguard.PeerConfig {
	m := make(map[string]*wireguard.PeerConfig, len(peers))
	for _, p := range peers {
		if len(p.AllowedIPs) == 0 {
			continue
		}
		ip, _, err := net.ParseCIDR(p.AllowedIPs[0])
		if err != nil {
			continue
		}
		m[ip.String()] = p
	}
	return m
}

// assessNAT classifies the NAT type by comparing the agent's STUN-announced endpoint
// at registration against what Colony's WireGuard currently observes in live traffic.
func assessNAT(registered, observed string, handshakeAge int64) string {
	if handshakeAge == -1 {
		return "no_handshake"
	}
	if registered == "" {
		return "roaming" // No STUN at registration; WireGuard roaming mode.
	}
	if observed == "" {
		return "no_handshake" // Colony has not received any packets yet.
	}
	regHost, regPort, err1 := net.SplitHostPort(registered)
	obsHost, obsPort, err2 := net.SplitHostPort(observed)
	if err1 != nil || err2 != nil {
		return "error"
	}
	switch {
	case regHost == obsHost && regPort == obsPort:
		return "direct" // Exact match: cone NAT (port preserved) or public IP.
	case regHost == obsHost:
		return "symmetric" // Same IP but different port: symmetric NAT.
	default:
		return "unexpected" // Different IP: double NAT, relay, or carrier-grade NAT.
	}
}
